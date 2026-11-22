
CREATE TYPE item_kind AS ENUM ('PRODUCT', 'SERVICE');

CREATE TABLE services (
  id              UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  name            TEXT          NOT NULL,
  unit            TEXT,
  reference_price DECIMAL(12,2) CHECK (reference_price >= 0),
  reference_cost  DECIMAL(12,2) CHECK (reference_cost >= 0),
  notes           TEXT,
  deleted_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX ux_services_name ON services (lower(name)) WHERE deleted_at IS NULL;

ALTER TABLE quote_items
  ADD COLUMN kind       item_kind NOT NULL DEFAULT 'PRODUCT',
  ADD COLUMN service_id UUID      REFERENCES services(id),
  ADD CONSTRAINT chk_quote_items_kind CHECK (
    (kind = 'PRODUCT' AND service_id IS NULL) OR (kind = 'SERVICE' AND product_id IS NULL)
  );

ALTER TABLE itens_venda
  ADD COLUMN kind       item_kind NOT NULL DEFAULT 'PRODUCT',
  ADD COLUMN service_id UUID      REFERENCES services(id),
  ALTER COLUMN origin DROP NOT NULL,   -- o default MADE_TO_ORDER continua: SERVICE grava NULL explícito
  ADD CONSTRAINT chk_itens_venda_kind CHECK (
    (kind = 'PRODUCT' AND service_id IS NULL AND origin IS NOT NULL)
    OR (kind = 'SERVICE' AND produto_id IS NULL AND origin IS NULL
        AND fornecedor_id IS NULL AND purchase_id IS NULL AND custo_unit = 0)
  );

CREATE INDEX idx_quote_items_service_id  ON quote_items(service_id)  WHERE service_id IS NOT NULL;
CREATE INDEX idx_itens_venda_service_id  ON itens_venda(service_id)  WHERE service_id IS NOT NULL;
CREATE INDEX idx_sale_costs_sale_item_id ON sale_costs(sale_item_id) WHERE sale_item_id IS NOT NULL;
