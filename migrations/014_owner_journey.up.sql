--   number: sequência própria por tabela (quotes, vendas, purchases), backfill por created_at, id
--   (inclui soft-deletados). Pode pular número (rollback); nunca se reutiliza.
--   Views de resumo NÃO mudam (os handlers juntam a tabela base); vw_obligations só troca o
--   origin_label (mesmas colunas e tipos — CREATE OR REPLACE mantém os dependentes).

ALTER TABLE quotes ADD COLUMN number INTEGER;
UPDATE quotes t SET number = r.rn
FROM (SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, id) AS rn FROM quotes) r
WHERE r.id = t.id;
CREATE SEQUENCE quotes_number_seq OWNED BY quotes.number;
SELECT setval('quotes_number_seq', COALESCE((SELECT MAX(number) FROM quotes), 0) + 1, false);
ALTER TABLE quotes
  ALTER COLUMN number SET DEFAULT nextval('quotes_number_seq'),
  ALTER COLUMN number SET NOT NULL,
  ADD CONSTRAINT uq_quotes_number UNIQUE (number);

ALTER TABLE vendas ADD COLUMN number INTEGER;
UPDATE vendas t SET number = r.rn
FROM (SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, id) AS rn FROM vendas) r
WHERE r.id = t.id;
CREATE SEQUENCE vendas_number_seq OWNED BY vendas.number;
SELECT setval('vendas_number_seq', COALESCE((SELECT MAX(number) FROM vendas), 0) + 1, false);
ALTER TABLE vendas
  ALTER COLUMN number SET DEFAULT nextval('vendas_number_seq'),
  ALTER COLUMN number SET NOT NULL,
  ADD CONSTRAINT uq_vendas_number UNIQUE (number);

ALTER TABLE purchases ADD COLUMN number INTEGER;
UPDATE purchases t SET number = r.rn
FROM (SELECT id, ROW_NUMBER() OVER (ORDER BY created_at, id) AS rn FROM purchases) r
WHERE r.id = t.id;
CREATE SEQUENCE purchases_number_seq OWNED BY purchases.number;
SELECT setval('purchases_number_seq', COALESCE((SELECT MAX(number) FROM purchases), 0) + 1, false);
ALTER TABLE purchases
  ALTER COLUMN number SET DEFAULT nextval('purchases_number_seq'),
  ALTER COLUMN number SET NOT NULL,
  ADD CONSTRAINT uq_purchases_number UNIQUE (number);

ALTER TABLE produtos ADD COLUMN default_supplier_id UUID REFERENCES fornecedores(id);
CREATE INDEX idx_produtos_default_supplier ON produtos(default_supplier_id) WHERE default_supplier_id IS NOT NULL;

CREATE OR REPLACE VIEW vw_obligations AS
SELECT
  o.id, o.direction::text AS direction, o.origin_type, o.origin_id, o.sale_id, o.purchase_id,
  o.counterparty_type, o.counterparty_id, o.due_date, o.notes, o.created_at,
  CASE o.origin_type
    WHEN 'SALE' THEN 'Venda nº ' || sv.number::text || ' · ' || COALESCE(sc.nome, '')
    ELSE 'Compra nº ' || pu.number::text || ' · ' || COALESCE(pf.nome, '')
  END                                                    AS origin_label,
  COALESCE(cf.nome, cp.nome, cc.nome)                    AS counterparty_name,
  (sv.delivery_status = 'CANCELED')                      AS sale_canceled,
  COALESCE(c.amount, 0)                                  AS amount,
  COALESCE(s.settled, 0)                                 AS settled,
  GREATEST(COALESCE(c.amount, 0) - COALESCE(s.settled, 0), 0) AS open,
  CASE
    WHEN COALESCE(s.settled, 0) >= COALESCE(c.amount, 0) THEN 'SETTLED'
    WHEN COALESCE(s.settled, 0) > 0                      THEN 'PARTIAL'
    ELSE 'OPEN'
  END                                                    AS status,
  (GREATEST(COALESCE(c.amount, 0) - COALESCE(s.settled, 0), 0) > 0
    AND (COALESCE(ic.late, FALSE)
         OR COALESCE(o.due_date < (NOW() AT TIME ZONE 'America/Sao_Paulo')::date, FALSE))) AS overdue,
  ic.next_due                                            AS next_installment_due,
  ic.next_open                                           AS next_installment_open,
  COALESCE(ic.next_due, o.due_date)                      AS effective_due
FROM obligations o
LEFT JOIN vendas sv        ON sv.id = o.sale_id
LEFT JOIN clientes sc      ON sc.id = sv.cliente_id
LEFT JOIN purchases pu     ON pu.id = o.purchase_id
LEFT JOIN fornecedores pf  ON pf.id = pu.supplier_id
LEFT JOIN fornecedores cf  ON cf.id = o.supplier_id
LEFT JOIN vendedores_externos cp ON cp.id = o.partner_id
LEFT JOIN clientes cc      ON cc.id = o.customer_id
LEFT JOIN (
  SELECT origin_type, origin_id, direction, counterparty_id, SUM(amount) AS amount
  FROM vw_obligation_components
  GROUP BY origin_type, origin_id, direction, counterparty_id
) c ON c.origin_id = o.origin_id AND c.origin_type = o.origin_type
   AND c.direction::text = o.direction::text
   AND c.counterparty_id IS NOT DISTINCT FROM o.counterparty_id
LEFT JOIN (
  SELECT obligation_id, SUM(amount) AS settled FROM settlements WHERE deleted_at IS NULL GROUP BY obligation_id
) s ON s.obligation_id = o.id
LEFT JOIN LATERAL (
  SELECT
    (array_agg(k.due_date ORDER BY k.due_date, k.created_at, k.id) FILTER (WHERE k.status <> 'COVERED'))[1]            AS next_due,
    (array_agg(k.amount - k.covered ORDER BY k.due_date, k.created_at, k.id) FILTER (WHERE k.status <> 'COVERED'))[1]  AS next_open,
    COALESCE(bool_or(k.overdue), FALSE)                                                                                 AS late
  FROM vw_installment_coverage k
  WHERE k.obligation_id = o.id
) ic ON TRUE
WHERE o.deleted_at IS NULL;
