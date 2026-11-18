-- AYD-008@context / SPEC-009: composição da compra.
--   pedras (linhas) + custos adicionais (purchase_charges: frete de compra, descarga, imposto,
--   outros) com credor próprio → contas por credor (AYD-007) + rateio → custo de entrada (landed).
-- Rateio, custo de entrada, valor em estoque e totais são VIEWS (RNF-05) — nunca gravados.

CREATE TYPE purchase_charge_kind AS ENUM ('FREIGHT', 'UNLOADING', 'TAX', 'OTHER');
CREATE TYPE allocation_method    AS ENUM ('BY_QUANTITY', 'BY_VALUE');

ALTER TABLE purchases ADD COLUMN allocation_method allocation_method NOT NULL DEFAULT 'BY_QUANTITY';

-- Custo adicional da compra. payee_supplier_id nulo = "a definir" (conta sem credor).
CREATE TABLE purchase_charges (
  id                UUID                 PRIMARY KEY DEFAULT gen_random_uuid(),
  purchase_id       UUID                 NOT NULL REFERENCES purchases(id),
  kind              purchase_charge_kind NOT NULL,
  amount            DECIMAL(12,2)        NOT NULL CHECK (amount > 0),
  description       TEXT,
  payee_supplier_id UUID                 REFERENCES fornecedores(id),
  deleted_at        TIMESTAMPTZ,
  created_at        TIMESTAMPTZ          NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_purchase_charges_purchase_id ON purchase_charges(purchase_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_purchase_charges_payee       ON purchase_charges(payee_supplier_id) WHERE deleted_at IS NULL;

-- Views que mudam (recriadas abaixo)
DROP VIEW IF EXISTS vw_vendas_resumo;
DROP VIEW IF EXISTS vw_purchases_resumo;
DROP VIEW IF EXISTS vw_stock_balance;

-- ============================================================
-- Rateio e custo de entrada por linha da compra (§3)
--   BY_QUANTITY: charges_total × quantity / Σ quantity
--   BY_VALUE:    charges_total × base_cost / Σ base_cost (Σ = 0 cai para BY_QUANTITY)
-- Linha = linha de reposição (STOCK_ITEM) ou Item de Sale sob encomenda (SALE_ITEM).
-- units_mixed: mais de uma unidade (normalizada) entre as linhas; linha sem Product não conta.
-- Precisão total — o arredondamento é só na exibição.
-- ============================================================
CREATE VIEW vw_purchase_line_costs AS
WITH lines AS (
  SELECT p.id AS purchase_id, 'STOCK_ITEM'::text AS line_type, pi.id AS line_id, NULL::uuid AS sale_id,
         pi.product_id, pr.nome AS product_name, pr.unidade AS unit,
         pi.quantity::numeric AS quantity, pi.unit_cost::numeric AS unit_cost, pi.created_at
  FROM purchase_items pi
  JOIN purchases p ON p.id = pi.purchase_id AND p.deleted_at IS NULL
  LEFT JOIN produtos pr ON pr.id = pi.product_id
  UNION ALL
  SELECT p.id, 'SALE_ITEM'::text, i.id, i.venda_id,
         i.produto_id, COALESCE(pr.nome, i.produto_nome), pr.unidade,
         i.quantidade::numeric, i.custo_unit::numeric, i.created_at
  FROM itens_venda i
  JOIN purchases p ON p.id = i.purchase_id AND p.deleted_at IS NULL
  LEFT JOIN produtos pr ON pr.id = i.produto_id
),
tot AS (
  SELECT purchase_id,
         SUM(quantity)              AS qty_total,
         SUM(quantity * unit_cost)  AS base_total,
         COUNT(DISTINCT NULLIF(lower(btrim(unit)), '')) > 1 AS units_mixed
  FROM lines
  GROUP BY purchase_id
),
ch AS (
  SELECT purchase_id, SUM(amount) AS total
  FROM purchase_charges
  WHERE deleted_at IS NULL
  GROUP BY purchase_id
)
SELECT
  l.purchase_id, l.line_type, l.line_id, l.sale_id, l.product_id, l.product_name, l.unit,
  l.quantity, l.unit_cost, l.created_at,
  l.quantity * l.unit_cost                                   AS base_cost,
  p.allocation_method::text                                  AS allocation_method,
  COALESCE(ch.total, 0)                                      AS charges_total,
  tot.units_mixed,
  alloc.allocated_charges,
  l.quantity * l.unit_cost + alloc.allocated_charges         AS landed_total,
  (l.quantity * l.unit_cost + alloc.allocated_charges) / NULLIF(l.quantity, 0) AS landed_unit_cost
FROM lines l
JOIN purchases p ON p.id = l.purchase_id
JOIN tot ON tot.purchase_id = l.purchase_id
LEFT JOIN ch ON ch.purchase_id = l.purchase_id
CROSS JOIN LATERAL (
  SELECT CASE
    WHEN p.allocation_method = 'BY_VALUE' AND tot.base_total > 0
      THEN COALESCE(ch.total, 0) * (l.quantity * l.unit_cost) / tot.base_total
    WHEN tot.qty_total > 0
      THEN COALESCE(ch.total, 0) * l.quantity / tot.qty_total
    ELSE 0
  END AS allocated_charges
) alloc;

-- ============================================================
-- Componentes das contas: + PURCHASE_CHARGE (um por custo adicional; id = chargeId).
-- PURCHASE_ITEMS segue = Σ base_cost das linhas (vw_purchase_costs). Credor nulo → conta sem credor.
-- ============================================================
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

-- ============================================================
-- Triggers: contas sincronizam em toda escrita de custo adicional e na troca de Supplier
-- ============================================================
CREATE OR REPLACE FUNCTION trg_sync_obligations_purchase() RETURNS trigger AS $$
BEGIN
  -- Troca de Supplier: os custos adicionais que estavam com o credor padrão acompanham a compra.
  IF TG_OP = 'UPDATE' AND NEW.supplier_id IS DISTINCT FROM OLD.supplier_id THEN
    UPDATE purchase_charges
       SET payee_supplier_id = NEW.supplier_id
     WHERE purchase_id = NEW.id AND deleted_at IS NULL AND payee_supplier_id = OLD.supplier_id;
  END IF;
  PERFORM sp_sync_obligations('PURCHASE', NEW.id);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION trg_sync_obligations_purchase_charge() RETURNS trigger AS $$
BEGIN
  IF TG_OP IN ('INSERT', 'UPDATE') THEN
    PERFORM sp_sync_obligations('PURCHASE', NEW.purchase_id);
  END IF;
  IF TG_OP IN ('UPDATE', 'DELETE') THEN
    PERFORM sp_sync_obligations('PURCHASE', OLD.purchase_id);
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_obligations_sync AFTER INSERT OR UPDATE OR DELETE ON purchase_charges
  FOR EACH ROW EXECUTE FUNCTION trg_sync_obligations_purchase_charge();

-- ============================================================
-- Resumo por Purchase: total_cost = Σ linhas (pedras); + charges_total; pago/a pagar = Σ contas
-- ============================================================
CREATE VIEW vw_purchases_resumo AS
SELECT
  p.*,
  pc.total_cost,
  pc.line_count,
  COALESCE(chg.total, 0)  AS charges_total,
  COALESCE(ob.settled, 0) AS paid,
  COALESCE(ob.open, 0)    AS payable,
  CASE WHEN COALESCE(ob.amount, 0) > 0 AND COALESCE(ob.open, 0) <= 0 THEN 'PAID' ELSE 'PENDING' END AS payment_status
FROM purchases p
JOIN vw_purchase_costs pc ON pc.purchase_id = p.id
LEFT JOIN (
  SELECT purchase_id, SUM(amount) AS total FROM purchase_charges WHERE deleted_at IS NULL GROUP BY purchase_id
) chg ON chg.purchase_id = p.id
LEFT JOIN (
  SELECT purchase_id, SUM(amount) AS amount, SUM(settled) AS settled, SUM(open) AS open
  FROM vw_obligations
  WHERE direction = 'PAYABLE' AND purchase_id IS NOT NULL
  GROUP BY purchase_id
) ob ON ob.purchase_id = p.id
WHERE p.deleted_at IS NULL;

-- ============================================================
-- Resumo por Sale: total_custo e lucro incluem os custos adicionais da compra rateados aos
-- Items sob encomenda (RN-04). Rateio pré-agregado por Sale (sem fan-out).
-- ============================================================
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

-- ============================================================
-- Balance por Product (RN-03/RN-16) com custo de ENTRADA (landed): a última entrada de
-- reposição usa landed_unit_cost (com o rateio dos custos adicionais). stock_value =
-- GREATEST(balance, 0) × last_inflow_cost (0 sem custo).
-- ============================================================
CREATE VIEW vw_stock_balance AS
WITH replenishment AS (
  SELECT pi.product_id, pi.quantity, lc.landed_unit_cost AS unit_cost, p.received_at AS event_date, pi.created_at
  FROM purchase_items pi
  JOIN purchases p ON p.id = pi.purchase_id
  JOIN vw_purchase_line_costs lc ON lc.line_type = 'STOCK_ITEM' AND lc.line_id = pi.id
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
  lc.event_date                                                                AS last_inflow_date,
  GREATEST(COALESCE(inf.qty, 0) - COALESCE(outf.qty, 0) + COALESCE(adj.qty, 0), 0)
    * COALESCE(lc.unit_cost, 0)                                                AS stock_value
FROM produtos pr
LEFT JOIN inflow     inf  ON inf.product_id  = pr.id
LEFT JOIN outflow    outf ON outf.product_id = pr.id
LEFT JOIN adjustment adj  ON adj.product_id  = pr.id
LEFT JOIN last_cost  lc   ON lc.product_id   = pr.id
WHERE pr.deleted_at IS NULL;
