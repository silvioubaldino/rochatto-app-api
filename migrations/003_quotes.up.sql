-- QUOTE STATUS
CREATE TYPE quote_status AS ENUM ('OPEN', 'WON', 'LOST');

-- QUOTES
CREATE TABLE quotes (
  id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  customer_id  UUID         NOT NULL REFERENCES clientes(id),
  referrer_id  UUID         REFERENCES vendedores_externos(id),
  status       quote_status NOT NULL DEFAULT 'OPEN',
  lost_reason  TEXT,
  quote_date   DATE         NOT NULL DEFAULT CURRENT_DATE,
  notes        TEXT,
  deleted_at   TIMESTAMPTZ,
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- QUOTE ITEMS
CREATE TABLE quote_items (
  id             UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_id       UUID          NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
  product_id     UUID          REFERENCES produtos(id),
  product_name   TEXT          NOT NULL,
  supplier_id    UUID          REFERENCES fornecedores(id),
  supplier_name  TEXT,
  quantity       DECIMAL(12,3) NOT NULL,
  unit_price     DECIMAL(12,2) NOT NULL,
  unit_cost      DECIMAL(12,2) NOT NULL,
  created_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- SALE ORIGIN (RN-01): a Sale nasce de um Quote
ALTER TABLE vendas ADD COLUMN quote_id UUID UNIQUE REFERENCES quotes(id);

-- VIEW DE RESUMO (campo calculado — nunca persistido)
CREATE VIEW vw_quotes_resumo AS
SELECT
  q.*,
  COALESCE(SUM(qi.quantity * qi.unit_price), 0) AS total_quote
FROM quotes q
LEFT JOIN quote_items qi ON qi.quote_id = q.id
WHERE q.deleted_at IS NULL
GROUP BY q.id;

-- ÍNDICES
CREATE INDEX idx_quotes_customer_id  ON quotes(customer_id);
CREATE INDEX idx_quotes_status       ON quotes(status);
CREATE INDEX idx_quotes_quote_date   ON quotes(quote_date);
CREATE INDEX idx_quote_items_quote_id ON quote_items(quote_id);
