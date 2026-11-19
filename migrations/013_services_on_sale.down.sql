-- Reverte a 013. LOSSY: o catálogo `services` e a ligação Item → Service se perdem. Items SERVICE
-- são preservados (a receita continua) como Items sob encomenda sem Product nem Supplier —
-- passam a aparecer na fila "a comprar": revise-os antes de reverter em produção.

ALTER TABLE itens_venda DROP CONSTRAINT chk_itens_venda_kind;
UPDATE itens_venda SET origin = 'MADE_TO_ORDER' WHERE origin IS NULL;

ALTER TABLE itens_venda
  ALTER COLUMN origin SET NOT NULL,
  DROP COLUMN service_id,
  DROP COLUMN kind;

ALTER TABLE quote_items
  DROP CONSTRAINT chk_quote_items_kind,
  DROP COLUMN service_id,
  DROP COLUMN kind;

DROP INDEX IF EXISTS idx_sale_costs_sale_item_id;
DROP TABLE services;
DROP TYPE item_kind;
