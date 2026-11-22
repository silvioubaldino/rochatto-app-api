CREATE TYPE quote_status AS ENUM ('OPEN', 'WON', 'LOST');

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

CREATE TABLE quote_items (
  id             UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_id       UUID          NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
  product_id     UUID          REFERENCES produtos(id),
  product_name   TEXT          NOT NULL,
  quantity       DECIMAL(12,3) NOT NULL,
  unit_price     DECIMAL(12,2) NOT NULL,
  unit_cost      DECIMAL(12,2) NOT NULL,
  created_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

ALTER TABLE vendas ADD COLUMN quote_id UUID UNIQUE REFERENCES quotes(id);

CREATE VIEW vw_quotes_resumo AS
SELECT
  q.*,
  COALESCE(SUM(qi.quantity * qi.unit_price), 0) AS total_quote
FROM quotes q
LEFT JOIN quote_items qi ON qi.quote_id = q.id
WHERE q.deleted_at IS NULL
GROUP BY q.id;

CREATE INDEX idx_quotes_customer_id  ON quotes(customer_id);
CREATE INDEX idx_quotes_status       ON quotes(status);
CREATE INDEX idx_quotes_quote_date   ON quotes(quote_date);
CREATE INDEX idx_quote_items_quote_id ON quote_items(quote_id);
