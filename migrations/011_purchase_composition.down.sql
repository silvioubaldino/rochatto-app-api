-- Reverte a 011: volta ao custo de entrada sem rateio (unit_cost da linha) e remove
-- purchase_charges. LOSSY: os custos adicionais da compra (e o credor de cada um) se perdem; as
-- contas da compra voltam a ter só o valor das linhas. allocation_method se perde.

DROP TRIGGER IF EXISTS trg_obligations_sync ON purchase_charges;
DROP FUNCTION IF EXISTS trg_sync_obligations_purchase_charge();

DROP VIEW IF EXISTS vw_vendas_resumo;
DROP VIEW IF EXISTS vw_purchases_resumo;
DROP VIEW IF EXISTS vw_stock_balance;

-- Componentes sem PURCHASE_CHARGE
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
WHERE ROUND(pc.total_cost, 2) > 0;

CREATE OR REPLACE FUNCTION trg_sync_obligations_purchase() RETURNS trigger AS $$
BEGIN
  PERFORM sp_sync_obligations('PURCHASE', NEW.id);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP VIEW IF EXISTS vw_purchase_line_costs;
DROP TABLE purchase_charges;
ALTER TABLE purchases DROP COLUMN allocation_method;
DROP TYPE purchase_charge_kind;
DROP TYPE allocation_method;

CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(it.total_venda, 0)    AS total_venda,
  COALESCE(it.total_custo, 0)    AS total_custo,
  COALESCE(rc.settled, 0)        AS total_recebido,
  COALESCE(it.total_venda, 0)
    - COALESCE(it.total_custo, 0)
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

-- Resumo por Purchase: custo, pago e a pagar = Σ das contas da compra; payment_status derivado.
CREATE VIEW vw_purchases_resumo AS
SELECT
  p.*,
  pc.total_cost,
  pc.line_count,
  COALESCE(ob.settled, 0) AS paid,
  COALESCE(ob.open, 0)    AS payable,
  CASE WHEN COALESCE(ob.amount, 0) > 0 AND COALESCE(ob.open, 0) <= 0 THEN 'PAID' ELSE 'PENDING' END AS payment_status
FROM purchases p
JOIN vw_purchase_costs pc ON pc.purchase_id = p.id
LEFT JOIN (
  SELECT purchase_id, SUM(amount) AS amount, SUM(settled) AS settled, SUM(open) AS open
  FROM vw_obligations
  WHERE direction = 'PAYABLE' AND purchase_id IS NOT NULL
  GROUP BY purchase_id
) ob ON ob.purchase_id = p.id
WHERE p.deleted_at IS NULL;

-- Balance por Product (RN-03/RN-16): Inflow (reposição recebida) − Outflow (Items FROM_STOCK
-- de Sales não canceladas) + ajustes. Custo da última entrada: a mais recente entre linhas de
-- reposição recebidas e ajustes INITIAL/COUNT com custo.
CREATE VIEW vw_stock_balance AS
WITH replenishment AS (
  SELECT pi.product_id, pi.quantity, pi.unit_cost, p.received_at AS event_date, pi.created_at
  FROM purchase_items pi
  JOIN purchases p ON p.id = pi.purchase_id
  WHERE p.deleted_at IS NULL
    AND p.kind = 'STOCK_REPLENISHMENT'
    AND p.status = 'RECEIVED'
),
inflow AS (
  SELECT product_id, SUM(quantity) AS qty FROM replenishment GROUP BY product_id
),
outflow AS (
  SELECT i.produto_id AS product_id, SUM(i.quantidade) AS qty
  FROM itens_venda i
  JOIN vendas v ON v.id = i.venda_id
  WHERE v.deleted_at IS NULL
    AND v.delivery_status <> 'CANCELED'
    AND i.origin = 'FROM_STOCK'
  GROUP BY i.produto_id
),
adjustment AS (
  SELECT product_id, SUM(quantity) AS qty FROM stock_adjustments GROUP BY product_id
),
last_cost AS (
  SELECT DISTINCT ON (product_id) product_id, unit_cost, event_date
  FROM (
    SELECT product_id, unit_cost, event_date, created_at FROM replenishment
    UNION ALL
    SELECT product_id, unit_cost, adjusted_at, created_at
    FROM stock_adjustments
    WHERE reason IN ('INITIAL', 'COUNT') AND unit_cost IS NOT NULL
  ) e
  ORDER BY product_id, event_date DESC, created_at DESC
)
SELECT
  pr.id                                                                        AS product_id,
  pr.nome                                                                      AS product_name,
  pr.unidade                                                                   AS unit,
  COALESCE(inf.qty, 0)                                                         AS inflow_qty,
  COALESCE(outf.qty, 0)                                                        AS outflow_qty,
  COALESCE(adj.qty, 0)                                                         AS adjustment_qty,
  COALESCE(inf.qty, 0) - COALESCE(outf.qty, 0) + COALESCE(adj.qty, 0)          AS balance,
  lc.unit_cost                                                                 AS last_inflow_cost,
  lc.event_date                                                                AS last_inflow_date
FROM produtos pr
LEFT JOIN inflow     inf  ON inf.product_id  = pr.id
LEFT JOIN outflow    outf ON outf.product_id = pr.id
LEFT JOIN adjustment adj  ON adj.product_id  = pr.id
LEFT JOIN last_cost  lc   ON lc.product_id   = pr.id
WHERE pr.deleted_at IS NULL;
