-- Reverte a 012. LOSSY: cobranças (sale_charges/quote_charges), custos LABOR/OTHER e credores se
-- perdem; frete_valor / comissao_valor voltam como a soma dos custos FREIGHT / COMMISSION.
-- As contas continuam com o que foi quitado, mas o valor volta ao do legado.

DROP VIEW IF EXISTS vw_vendas_resumo;
DROP VIEW IF EXISTS vw_quotes_resumo;

ALTER TABLE vendas
  ADD COLUMN frete_valor    DECIMAL(12,2) NOT NULL DEFAULT 0,
  ADD COLUMN comissao_valor DECIMAL(12,2) NOT NULL DEFAULT 0;

UPDATE vendas v SET frete_valor = x.total
FROM (SELECT sale_id, SUM(amount) AS total FROM sale_costs
      WHERE deleted_at IS NULL AND kind = 'FREIGHT' GROUP BY sale_id) x
WHERE x.sale_id = v.id;

UPDATE vendas v SET comissao_valor = x.total
FROM (SELECT sale_id, SUM(amount) AS total FROM sale_costs
      WHERE deleted_at IS NULL AND kind = 'COMMISSION' GROUP BY sale_id) x
WHERE x.sale_id = v.id;

DROP TRIGGER IF EXISTS trg_obligations_sync ON sale_costs;
DROP TRIGGER IF EXISTS trg_obligations_sync ON sale_charges;
DROP FUNCTION IF EXISTS trg_sync_obligations_sale_component();

CREATE OR REPLACE FUNCTION trg_sync_obligations_sale() RETURNS trigger AS $$
BEGIN
  PERFORM sp_sync_obligations('SALE', NEW.id);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE VIEW vw_obligation_components AS
SELECT
  'SALE'::text AS origin_type, v.id AS origin_id,
  'RECEIVABLE'::text AS direction, 'CUSTOMER'::text AS counterparty_type, v.cliente_id AS counterparty_id,
  'SALE_ITEMS'::text AS type, v.id AS component_id, 'Itens da venda'::text AS label,
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE ROUND(it.total, 2) END)::numeric(12,2) AS amount,
  ROUND(it.total, 2)::numeric(12,2) AS base_amount
FROM vendas v
JOIN (
  SELECT venda_id, SUM(quantidade * preco_venda_unit) AS total FROM itens_venda GROUP BY venda_id
) it ON it.venda_id = v.id
WHERE v.deleted_at IS NULL AND ROUND(it.total, 2) > 0
UNION ALL
SELECT
  'SALE', v.id, 'PAYABLE', NULL::text, NULL::uuid,
  'LEGACY_FREIGHT', v.id, 'Frete',
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE v.frete_valor END)::numeric(12,2), v.frete_valor::numeric(12,2)
FROM vendas v
WHERE v.deleted_at IS NULL AND v.frete_valor > 0
UNION ALL
SELECT
  'SALE', v.id, 'PAYABLE',
  CASE WHEN v.vendedor_externo_id IS NOT NULL THEN 'PARTNER' END, v.vendedor_externo_id,
  'LEGACY_COMMISSION', v.id, 'Comissão',
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE v.comissao_valor END)::numeric(12,2), v.comissao_valor::numeric(12,2)
FROM vendas v
WHERE v.deleted_at IS NULL AND v.comissao_valor > 0
UNION ALL
SELECT
  'PURCHASE', pc.purchase_id, 'PAYABLE', 'SUPPLIER', pc.supplier_id,
  'PURCHASE_ITEMS', pc.purchase_id, 'Itens da compra',
  ROUND(pc.total_cost, 2)::numeric(12,2), ROUND(pc.total_cost, 2)::numeric(12,2)
FROM vw_purchase_costs pc
WHERE ROUND(pc.total_cost, 2) > 0
UNION ALL
SELECT
  'PURCHASE', c.purchase_id, 'PAYABLE',
  CASE WHEN c.payee_supplier_id IS NOT NULL THEN 'SUPPLIER' END, c.payee_supplier_id,
  'PURCHASE_CHARGE', c.id,
  (CASE c.kind WHEN 'FREIGHT' THEN 'Frete de compra' WHEN 'UNLOADING' THEN 'Descarga'
               WHEN 'TAX' THEN 'Imposto' ELSE 'Outros' END
    || COALESCE(' · ' || NULLIF(btrim(c.description), ''), ''))::text,
  ROUND(c.amount, 2)::numeric(12,2), ROUND(c.amount, 2)::numeric(12,2)
FROM purchase_charges c
JOIN purchases p ON p.id = c.purchase_id AND p.deleted_at IS NULL
WHERE c.deleted_at IS NULL;

DROP TABLE sale_costs;
DROP TABLE sale_charges;
DROP TABLE quote_charges;
DROP TYPE sale_cost_kind;
DROP TYPE sale_charge_kind;

SELECT sp_sync_obligations('SALE', id) FROM vendas;

CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(it.total_venda, 0)    AS total_venda,
  COALESCE(it.total_custo, 0) + COALESCE(ac.allocated, 0) AS total_custo,
  COALESCE(rc.settled, 0)        AS total_recebido,
  COALESCE(it.total_venda, 0)
    - COALESCE(it.total_custo, 0)
    - COALESCE(ac.allocated, 0)
    - v.frete_valor
    - v.comissao_valor           AS lucro,
  CASE
    WHEN COALESCE(rc.settled, 0) <= 0                                  THEN 'PENDING'
    WHEN COALESCE(rc.settled, 0) < ROUND(COALESCE(it.total_venda, 0), 2) THEN 'PARTIAL'
    ELSE 'PAID'
  END                            AS payment_status,
  COALESCE(rc.open, 0)           AS receivable,
  COALESCE(py.freight_payable, 0)                                       AS freight_payable,
  COALESCE(py.partner_open, 0) + COALESCE(py.nocred_commission_payable, 0) AS commission_payable
FROM vendas v
LEFT JOIN (
  SELECT
    venda_id,
    SUM(quantidade * preco_venda_unit) AS total_venda,
    SUM(quantidade * custo_unit)       AS total_custo
  FROM itens_venda
  GROUP BY venda_id
) it ON it.venda_id = v.id
LEFT JOIN (
  SELECT sale_id, SUM(allocated_charges) AS allocated
  FROM vw_purchase_line_costs
  WHERE line_type = 'SALE_ITEM'
  GROUP BY sale_id
) ac ON ac.sale_id = v.id
LEFT JOIN (
  SELECT sale_id, SUM(settled) AS settled, SUM(open) AS open
  FROM vw_obligations
  WHERE direction = 'RECEIVABLE'
  GROUP BY sale_id
) rc ON rc.sale_id = v.id
LEFT JOIN (
  SELECT
    p.sale_id,
    p.partner_open,
    GREATEST(p.freight_eff - p.settled_other, 0) AS freight_payable,
    GREATEST(p.nocred_amount - p.freight_eff - p.settled_comm - GREATEST(p.settled_other - p.freight_eff, 0), 0) AS nocred_commission_payable
  FROM (
    SELECT
      o.sale_id,
      COALESCE(SUM(o.open) FILTER (WHERE o.counterparty_type = 'PARTNER'), 0)                      AS partner_open,
      COALESCE(SUM(o.amount) FILTER (WHERE o.counterparty_id IS NULL), 0)                          AS nocred_amount,
      LEAST(MAX(sv.frete_valor), COALESCE(SUM(o.amount) FILTER (WHERE o.counterparty_id IS NULL), 0)) AS freight_eff,
      COALESCE(SUM(COALESCE(cs.comm, 0)) FILTER (WHERE o.counterparty_id IS NULL), 0)              AS settled_comm,
      COALESCE(SUM(o.settled - COALESCE(cs.comm, 0)) FILTER (WHERE o.counterparty_id IS NULL), 0)  AS settled_other
    FROM vw_obligations o
    JOIN vendas sv ON sv.id = o.sale_id
    LEFT JOIN (
      SELECT s.obligation_id, SUM(s.amount) AS comm
      FROM settlements s JOIN financial_movements m ON m.id = s.movement_id
      WHERE s.deleted_at IS NULL AND m.deleted_at IS NULL AND m.category = 'COMMISSION'
      GROUP BY s.obligation_id
    ) cs ON cs.obligation_id = o.id
    WHERE o.direction = 'PAYABLE' AND o.sale_id IS NOT NULL
    GROUP BY o.sale_id
  ) p
) py ON py.sale_id = v.id
WHERE v.deleted_at IS NULL;

CREATE VIEW vw_quotes_resumo AS
SELECT
  q.*,
  COALESCE(SUM(qi.quantity * qi.unit_price), 0) AS total_quote
FROM quotes q
LEFT JOIN quote_items qi ON qi.quote_id = q.id
WHERE q.deleted_at IS NULL
GROUP BY q.id;
