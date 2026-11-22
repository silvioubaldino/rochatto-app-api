-- Reverte a 014. LOSSY: os números humanos e o fornecedor habitual se perdem.
CREATE OR REPLACE VIEW vw_obligations AS
SELECT
  o.id, o.direction::text AS direction, o.origin_type, o.origin_id, o.sale_id, o.purchase_id,
  o.counterparty_type, o.counterparty_id, o.due_date, o.notes, o.created_at,
  CASE o.origin_type
    WHEN 'SALE' THEN 'Venda ' || to_char(sv.data_venda, 'DD/MM') || ' · ' || COALESCE(sc.nome, '')
    ELSE 'Compra ' || to_char(pu.created_at AT TIME ZONE 'America/Sao_Paulo', 'DD/MM') || ' · ' || COALESCE(pf.nome, '')
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

DROP INDEX IF EXISTS idx_produtos_default_supplier;
ALTER TABLE produtos DROP COLUMN default_supplier_id;

ALTER TABLE purchases DROP CONSTRAINT uq_purchases_number, DROP COLUMN number;
ALTER TABLE vendas    DROP CONSTRAINT uq_vendas_number,    DROP COLUMN number;
ALTER TABLE quotes    DROP CONSTRAINT uq_quotes_number,    DROP COLUMN number;
DROP SEQUENCE IF EXISTS purchases_number_seq, vendas_number_seq, quotes_number_seq;
