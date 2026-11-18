-- Reverte a 008: a marca quick_sale se perde.
DROP VIEW IF EXISTS vw_quotes_resumo;

ALTER TABLE quotes DROP COLUMN quick_sale;

CREATE VIEW vw_quotes_resumo AS
SELECT
  q.*,
  COALESCE(SUM(qi.quantity * qi.unit_price), 0) AS total_quote
FROM quotes q
LEFT JOIN quote_items qi ON qi.quote_id = q.id
WHERE q.deleted_at IS NULL
GROUP BY q.id;
