-- AYD-002@context: atendimento da Sale — origem do Item, Purchase, Stock e status da Sale.
-- Entidades/colunas novas em inglês (ADR-001); legado PT (vendas, itens_venda) mantido.
-- Simplificações do MVP: PDR-001@context.
--
-- ATENÇÃO (produção): parte desta migration é irreversível — vendas.status, itens_venda.pago_fornecedor
-- e a tabela estoque são removidos depois de migrados. Antes de aplicar, confira as entradas de
-- estoque sem produto_id (não migram) — ver docs/ops/database.md.

-- ============================================================
-- ENUMS
-- ============================================================
CREATE TYPE item_origin             AS ENUM ('MADE_TO_ORDER', 'FROM_STOCK');
CREATE TYPE purchase_kind           AS ENUM ('MADE_TO_ORDER', 'STOCK_REPLENISHMENT');
CREATE TYPE purchase_status         AS ENUM ('OPEN', 'ORDERED', 'RECEIVED');
CREATE TYPE supplier_payment_status AS ENUM ('PENDING', 'PAID');
CREATE TYPE sale_delivery_status    AS ENUM ('TO_DELIVER', 'DELIVERED', 'CANCELED');
CREATE TYPE stock_adjustment_reason AS ENUM ('INITIAL', 'COUNT', 'OTHER');

-- ============================================================
-- PURCHASE (consolidada por Supplier; um pagamento por Purchase — RN-06)
-- ============================================================
CREATE TABLE purchases (
  id             UUID                    PRIMARY KEY DEFAULT gen_random_uuid(),
  kind           purchase_kind           NOT NULL,
  supplier_id    UUID                    NOT NULL REFERENCES fornecedores(id),
  status         purchase_status         NOT NULL DEFAULT 'OPEN',
  expected_date  DATE,
  ordered_at     DATE,
  received_at    DATE,
  payment_status supplier_payment_status NOT NULL DEFAULT 'PENDING',
  paid_amount    DECIMAL(12,2)           CHECK (paid_amount >= 0),
  paid_at        DATE,
  notes          TEXT,
  deleted_at     TIMESTAMPTZ,
  created_at     TIMESTAMPTZ             NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_purchases_supplier_id ON purchases(supplier_id);
CREATE INDEX idx_purchases_status      ON purchases(status);

-- Linhas de reposição de Stock (só kind STOCK_REPLENISHMENT). As linhas de uma Purchase
-- MADE_TO_ORDER são os próprios Items de Sale (itens_venda.purchase_id).
CREATE TABLE purchase_items (
  id          UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  purchase_id UUID          NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
  product_id  UUID          NOT NULL REFERENCES produtos(id),
  quantity    DECIMAL(12,3) NOT NULL CHECK (quantity > 0),
  unit_cost   DECIMAL(12,2) NOT NULL CHECK (unit_cost >= 0),
  created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_purchase_items_purchase_id ON purchase_items(purchase_id);
CREATE INDEX idx_purchase_items_product_id  ON purchase_items(product_id);

-- ============================================================
-- STOCK ADJUSTMENT (imutável — erro se corrige com outro ajuste)
-- ============================================================
CREATE TABLE stock_adjustments (
  id          UUID                    PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id  UUID                    NOT NULL REFERENCES produtos(id),
  quantity    DECIMAL(12,3)           NOT NULL CHECK (quantity <> 0),
  unit_cost   DECIMAL(12,2)           CHECK (unit_cost >= 0),
  reason      stock_adjustment_reason NOT NULL,
  adjusted_at DATE                    NOT NULL DEFAULT CURRENT_DATE,
  notes       TEXT,
  created_at  TIMESTAMPTZ             NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_stock_adjustments_product_id ON stock_adjustments(product_id);

-- ============================================================
-- ITEM DE SALE: origem + Purchase
-- ============================================================
ALTER TABLE itens_venda
  ADD COLUMN origin      item_origin NOT NULL DEFAULT 'MADE_TO_ORDER',
  ADD COLUMN purchase_id UUID REFERENCES purchases(id);

ALTER TABLE itens_venda
  ADD CONSTRAINT chk_itens_venda_from_stock_product
    CHECK (origin <> 'FROM_STOCK' OR produto_id IS NOT NULL),
  ADD CONSTRAINT chk_itens_venda_from_stock_no_purchase
    CHECK (origin <> 'FROM_STOCK' OR purchase_id IS NULL);

CREATE INDEX idx_itens_venda_purchase_id ON itens_venda(purchase_id);
CREATE INDEX idx_itens_venda_produto_id  ON itens_venda(produto_id);

-- ============================================================
-- SALE: status de entrega (substitui status_venda)
-- ============================================================
ALTER TABLE vendas
  ADD COLUMN delivery_status sale_delivery_status NOT NULL DEFAULT 'TO_DELIVER';

CREATE INDEX idx_vendas_delivery_status ON vendas(delivery_status);

-- ============================================================
-- MIGRAÇÃO DOS DADOS LEGADOS (tabela do AYD-002)
-- ============================================================

-- vendas.status → delivery_status (A_ENTREGAR*, A_RETIRAR, ENTREGUE_PARCIAL → TO_DELIVER)
UPDATE vendas SET delivery_status = 'DELIVERED' WHERE status = 'ENTREGUE';

-- itens_venda existentes: origin = MADE_TO_ORDER (default), purchase_id = null.

-- pago_fornecedor (questão em aberto 1, decidida): uma Purchase legada RECEIVED por Supplier com
-- os Items de Sales não canceladas (e não excluídas) que têm Supplier E (Sale ENTREGUE ou
-- pago_fornecedor > 0). paid_amount = Σ pago_fornecedor; PAID se cobre o custo.
CREATE TEMP TABLE legacy_purchase_items AS
SELECT i.id, i.fornecedor_id, i.pago_fornecedor, i.quantidade * i.custo_unit AS cost
FROM itens_venda i
JOIN vendas v ON v.id = i.venda_id
WHERE v.deleted_at IS NULL
  AND i.fornecedor_id IS NOT NULL
  AND (v.status = 'ENTREGUE' OR i.pago_fornecedor > 0);

CREATE TEMP TABLE legacy_purchases AS
SELECT gen_random_uuid() AS id, fornecedor_id, SUM(pago_fornecedor) AS paid, SUM(cost) AS total
FROM legacy_purchase_items
GROUP BY fornecedor_id;

INSERT INTO purchases (id, kind, supplier_id, status, ordered_at, received_at,
                       payment_status, paid_amount, paid_at, notes)
SELECT id, 'MADE_TO_ORDER', fornecedor_id, 'RECEIVED', CURRENT_DATE, CURRENT_DATE,
       CASE WHEN paid >= total THEN 'PAID' ELSE 'PENDING' END::supplier_payment_status,
       paid,
       CASE WHEN paid > 0 THEN CURRENT_DATE END,
       'Migração: itens anteriores ao módulo de compras'
FROM legacy_purchases;

UPDATE itens_venda i
SET purchase_id = lp.id
FROM legacy_purchase_items lpi
JOIN legacy_purchases lp ON lp.fornecedor_id = lpi.fornecedor_id
WHERE i.id = lpi.id;

DROP TABLE legacy_purchase_items;
DROP TABLE legacy_purchases;

-- estoque (entradas avulsas) com produto_id → ajuste INITIAL. Sem produto_id não migra
-- (não há Product para o Balance); quantidade 0 não é ajuste (CHECK quantity <> 0).
INSERT INTO stock_adjustments (product_id, quantity, unit_cost, reason, adjusted_at, notes, created_at)
SELECT produto_id, quantidade_entrada, custo_unit, 'INITIAL', data_entrada, observacoes, created_at
FROM estoque
WHERE produto_id IS NOT NULL AND quantidade_entrada <> 0;

-- ============================================================
-- APOSENTA O LEGADO
-- ============================================================
DROP VIEW IF EXISTS vw_vendas_resumo;   -- depende de vendas.status / itens_venda.pago_fornecedor

ALTER TABLE itens_venda DROP COLUMN pago_fornecedor;
DROP INDEX IF EXISTS idx_vendas_status;
ALTER TABLE vendas DROP COLUMN status;
DROP TYPE status_venda;
DROP TABLE estoque;

-- ============================================================
-- VIEWS (campos calculados — nunca persistidos; somas pré-agregadas por subconsulta)
-- ============================================================

-- Resumo por Sale. Uma linha por Sale (inclusive CANCELED — os totais do painel/dashboard é
-- que filtram delivery_status <> 'CANCELED').
CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(it.total_venda, 0)    AS total_venda,
  COALESCE(it.total_custo, 0)    AS total_custo,
  COALESCE(rc.total_recebido, 0) AS total_recebido,
  COALESCE(it.total_venda, 0)
    - COALESCE(it.total_custo, 0)
    - v.frete_valor
    - v.comissao_valor           AS lucro,
  CASE
    WHEN COALESCE(rc.total_recebido, 0) <= 0                         THEN 'PENDING'
    WHEN COALESCE(rc.total_recebido, 0) < COALESCE(it.total_venda, 0) THEN 'PARTIAL'
    ELSE 'PAID'
  END                            AS payment_status
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
  SELECT
    venda_id,
    SUM(valor) FILTER (WHERE status = 'recebido') AS total_recebido
  FROM pagamentos_cliente
  GROUP BY venda_id
) rc ON rc.venda_id = v.id
WHERE v.deleted_at IS NULL;

-- Resumo por Purchase: total_cost = Σ linhas (Items de Sale para MADE_TO_ORDER, linhas de
-- reposição para STOCK_REPLENISHMENT); to_pay = total_cost − pago (0 se PAID).
CREATE VIEW vw_purchases_resumo AS
SELECT
  p.*,
  COALESCE(si.total, 0) + COALESCE(pi.total, 0)                  AS total_cost,
  CASE
    WHEN p.payment_status = 'PAID' THEN 0
    ELSE COALESCE(si.total, 0) + COALESCE(pi.total, 0) - COALESCE(p.paid_amount, 0)
  END                                                            AS to_pay,
  COALESCE(si.lines, 0) + COALESCE(pi.lines, 0)                  AS line_count
FROM purchases p
LEFT JOIN (
  SELECT purchase_id, SUM(quantidade * custo_unit) AS total, COUNT(*) AS lines
  FROM itens_venda
  WHERE purchase_id IS NOT NULL
  GROUP BY purchase_id
) si ON si.purchase_id = p.id
LEFT JOIN (
  SELECT purchase_id, SUM(quantity * unit_cost) AS total, COUNT(*) AS lines
  FROM purchase_items
  GROUP BY purchase_id
) pi ON pi.purchase_id = p.id
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
