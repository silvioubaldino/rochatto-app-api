-- Reverte a 006 para um schema 005 funcional. Mapeamento de dados de volta é best-effort
-- (perdas documentadas em docs/ops/database.md):
--   * CANCELED volta como A_ENTREGAR (status_venda não tem cancelada);
--   * pago_fornecedor é reconstruído proporcionalmente ao pagamento de cada Purchase;
--   * estoque é reconstruído dos ajustes (qualquer motivo/sinal) e das linhas de reposição
--     recebidas; Outflows (Items FROM_STOCK) e a origem do Item se perdem;
--   * Purchases, linhas de reposição e ajustes são descartados.

DROP VIEW IF EXISTS vw_stock_balance;
DROP VIEW IF EXISTS vw_purchases_resumo;
DROP VIEW IF EXISTS vw_vendas_resumo;

CREATE TABLE estoque (
  id                 UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  produto_id         UUID          REFERENCES produtos(id),
  produto_nome       TEXT          NOT NULL,
  fornecedor_id      UUID          REFERENCES fornecedores(id),
  quantidade_entrada DECIMAL(12,3) NOT NULL,
  custo_unit         DECIMAL(12,2) NOT NULL,
  data_entrada       DATE          NOT NULL DEFAULT CURRENT_DATE,
  observacoes        TEXT,
  created_at         TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

INSERT INTO estoque (produto_id, produto_nome, quantidade_entrada, custo_unit, data_entrada, observacoes, created_at)
SELECT a.product_id, pr.nome, a.quantity, COALESCE(a.unit_cost, 0), a.adjusted_at, a.notes, a.created_at
FROM stock_adjustments a
JOIN produtos pr ON pr.id = a.product_id;

INSERT INTO estoque (produto_id, produto_nome, fornecedor_id, quantidade_entrada, custo_unit, data_entrada, created_at)
SELECT pi.product_id, pr.nome, p.supplier_id, pi.quantity, pi.unit_cost,
       COALESCE(p.received_at, CURRENT_DATE), pi.created_at
FROM purchase_items pi
JOIN purchases p ON p.id = pi.purchase_id
JOIN produtos pr ON pr.id = pi.product_id
WHERE p.deleted_at IS NULL AND p.kind = 'STOCK_REPLENISHMENT' AND p.status = 'RECEIVED';

CREATE TYPE status_venda AS ENUM (
  'A_ENTREGAR',
  'A_ENTREGAR_DATA',
  'A_RETIRAR',
  'ENTREGUE_PARCIAL',
  'ENTREGUE'
);
ALTER TABLE vendas ADD COLUMN status status_venda NOT NULL DEFAULT 'A_ENTREGAR';
UPDATE vendas SET status = 'ENTREGUE' WHERE delivery_status = 'DELIVERED';
CREATE INDEX idx_vendas_status ON vendas(status);

ALTER TABLE itens_venda ADD COLUMN pago_fornecedor DECIMAL(12,2) NOT NULL DEFAULT 0;
UPDATE itens_venda i
SET pago_fornecedor = ROUND(
  CASE
    WHEN p.payment_status = 'PAID' THEN i.quantidade * i.custo_unit
    WHEN p.total_cost > 0 THEN LEAST(i.quantidade * i.custo_unit,
                                     i.quantidade * i.custo_unit * COALESCE(p.paid_amount, 0) / p.total_cost)
    ELSE 0
  END, 2)
FROM (
  SELECT pu.id, pu.payment_status, pu.paid_amount, SUM(iv.quantidade * iv.custo_unit) AS total_cost
  FROM purchases pu
  JOIN itens_venda iv ON iv.purchase_id = pu.id
  GROUP BY pu.id
) p
WHERE i.purchase_id = p.id;

DROP INDEX IF EXISTS idx_itens_venda_produto_id;
DROP INDEX IF EXISTS idx_itens_venda_purchase_id;
ALTER TABLE itens_venda DROP CONSTRAINT IF EXISTS chk_itens_venda_from_stock_no_purchase;
ALTER TABLE itens_venda DROP CONSTRAINT IF EXISTS chk_itens_venda_from_stock_product;
ALTER TABLE itens_venda DROP COLUMN purchase_id;
ALTER TABLE itens_venda DROP COLUMN origin;

DROP INDEX IF EXISTS idx_vendas_delivery_status;
ALTER TABLE vendas DROP COLUMN delivery_status;

DROP TABLE stock_adjustments;
DROP TABLE purchase_items;
DROP TABLE purchases;

DROP TYPE stock_adjustment_reason;
DROP TYPE sale_delivery_status;
DROP TYPE supplier_payment_status;
DROP TYPE purchase_status;
DROP TYPE purchase_kind;
DROP TYPE item_origin;

CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(it.total_venda, 0)                                         AS total_venda,
  COALESCE(it.total_custo, 0)                                         AS total_custo,
  COALESCE(rc.total_recebido, 0)                                      AS total_recebido,
  COALESCE(it.total_custo, 0) - COALESCE(it.total_pago_fornecedor, 0) AS a_pagar_fornecedor,
  COALESCE(it.total_venda, 0)
    - COALESCE(it.total_custo, 0)
    - v.frete_valor
    - v.comissao_valor                                                AS lucro
FROM vendas v
LEFT JOIN (
  SELECT
    venda_id,
    SUM(quantidade * preco_venda_unit) AS total_venda,
    SUM(quantidade * custo_unit)       AS total_custo,
    SUM(pago_fornecedor)               AS total_pago_fornecedor
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
