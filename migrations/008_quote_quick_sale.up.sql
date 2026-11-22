ALTER TABLE quotes ADD COLUMN quick_sale BOOLEAN NOT NULL DEFAULT false;

-- Backfill heurístico (sem marca histórica): venda rápida cria Quote e Sale na mesma
-- transação — Quote WON cuja Sale tem data_venda = quote_date e foi criada até 10 s depois
-- do Quote. Falso positivo só para quem converteu um Quote em menos de 10 s.
UPDATE quotes q
SET quick_sale = true
WHERE q.status = 'WON'
  AND EXISTS (
    SELECT 1 FROM vendas v
    WHERE v.quote_id = q.id
      AND v.data_venda = q.quote_date
      AND v.created_at BETWEEN q.created_at AND q.created_at + INTERVAL '10 seconds'
  );

DROP VIEW IF EXISTS vw_quotes_resumo;

CREATE VIEW vw_quotes_resumo AS
SELECT
  q.*,
  COALESCE(SUM(qi.quantity * qi.unit_price), 0) AS total_quote
FROM quotes q
LEFT JOIN quote_items qi ON qi.quote_id = q.id
WHERE q.deleted_at IS NULL
GROUP BY q.id;
