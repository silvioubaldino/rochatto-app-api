-- ATENÇÃO (produção): parte desta migration é irreversível sem perda — pagamentos_cliente,
-- purchases.payment_status/paid_amount/paid_at e vendas.frete_pago/comissao_paga são removidos
-- depois de migrados. O down recria as estruturas e repopula best-effort (ver docs/ops/database.md).

CREATE TYPE supplier_kind AS ENUM ('STONE', 'TRANSPORT', 'LABOR', 'TAX_AUTHORITY', 'OTHER');
CREATE TYPE obligation_direction AS ENUM ('PAYABLE', 'RECEIVABLE');
CREATE TYPE movement_category AS ENUM (
  'SALE_RECEIPT', 'OTHER_INCOME',
  'PURCHASE_PAYMENT',
  'FREIGHT', 'LABOR', 'COMMISSION',
  'VEHICLE', 'TAXES', 'FIXED_COSTS', 'MARKETING', 'BANK_FEES', 'OTHER_EXPENSE'
);

ALTER TABLE fornecedores ADD COLUMN kind supplier_kind NOT NULL DEFAULT 'STONE';
CREATE INDEX idx_fornecedores_kind ON fornecedores(kind);

-- Conta: "casca" por (origem, credor). Origem = Sale OU Purchase; credor = Supplier | Partner |
-- Customer (nenhum = "a definir"). As colunas generated só normalizam a leitura.
CREATE TABLE obligations (
  id                UUID                 PRIMARY KEY DEFAULT gen_random_uuid(),
  direction         obligation_direction NOT NULL,
  sale_id           UUID                 REFERENCES vendas(id),
  purchase_id       UUID                 REFERENCES purchases(id),
  supplier_id       UUID                 REFERENCES fornecedores(id),
  partner_id        UUID                 REFERENCES vendedores_externos(id),
  customer_id       UUID                 REFERENCES clientes(id),
  due_date          DATE,
  notes             TEXT,
  deleted_at        TIMESTAMPTZ,
  created_at        TIMESTAMPTZ          NOT NULL DEFAULT NOW(),
  origin_type       TEXT GENERATED ALWAYS AS (CASE WHEN sale_id IS NOT NULL THEN 'SALE' ELSE 'PURCHASE' END) STORED,
  origin_id         UUID GENERATED ALWAYS AS (COALESCE(sale_id, purchase_id)) STORED,
  counterparty_type TEXT GENERATED ALWAYS AS (
                      CASE WHEN supplier_id IS NOT NULL THEN 'SUPPLIER'
                           WHEN partner_id  IS NOT NULL THEN 'PARTNER'
                           WHEN customer_id IS NOT NULL THEN 'CUSTOMER' END) STORED,
  counterparty_id   UUID GENERATED ALWAYS AS (COALESCE(supplier_id, partner_id, customer_id)) STORED,
  CONSTRAINT chk_obligations_one_origin CHECK ((sale_id IS NULL) <> (purchase_id IS NULL)),
  CONSTRAINT chk_obligations_one_counterparty CHECK (num_nonnulls(supplier_id, partner_id, customer_id) <= 1),
  CONSTRAINT chk_obligations_direction CHECK (
    (direction = 'RECEIVABLE' AND sale_id IS NOT NULL AND customer_id IS NOT NULL)
    OR (direction = 'PAYABLE' AND customer_id IS NULL)
  )
);

-- Única por (origem, direção, credor); credor nulo conta como "a definir".
CREATE UNIQUE INDEX uq_obligations_origin_counterparty
  ON obligations (origin_id, direction, COALESCE(counterparty_id, '00000000-0000-0000-0000-000000000000'::uuid))
  WHERE deleted_at IS NULL;
CREATE INDEX idx_obligations_sale_id     ON obligations(sale_id)     WHERE deleted_at IS NULL;
CREATE INDEX idx_obligations_purchase_id ON obligations(purchase_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_obligations_due_date    ON obligations(due_date)    WHERE deleted_at IS NULL;

CREATE TABLE installments (
  id             UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
  obligation_id  UUID           NOT NULL REFERENCES obligations(id),
  due_date       DATE           NOT NULL,
  amount         DECIMAL(12,2)  NOT NULL CHECK (amount > 0),
  payment_method payment_method,
  notes          TEXT,
  deleted_at     TIMESTAMPTZ,
  created_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_installments_obligation_id ON installments(obligation_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_installments_due_date      ON installments(due_date)      WHERE deleted_at IS NULL;

CREATE TABLE financial_movements (
  id                UUID              PRIMARY KEY DEFAULT gen_random_uuid(),
  occurred_on       DATE              NOT NULL,
  amount            DECIMAL(12,2)     NOT NULL CHECK (amount <> 0),
  category          movement_category NOT NULL,
  payment_method    payment_method,
  counterparty_name TEXT,
  description       TEXT,
  corrected_at      TIMESTAMPTZ,
  deleted_at        TIMESTAMPTZ,
  created_at        TIMESTAMPTZ       NOT NULL DEFAULT NOW(),
  CONSTRAINT chk_movements_sign CHECK (
    (category IN ('SALE_RECEIPT', 'OTHER_INCOME') AND amount > 0)
    OR (category NOT IN ('SALE_RECEIPT', 'OTHER_INCOME') AND amount < 0)
  )
);
CREATE INDEX idx_financial_movements_occurred_on ON financial_movements(occurred_on) WHERE deleted_at IS NULL;

CREATE TABLE settlements (
  id            UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  obligation_id UUID          NOT NULL REFERENCES obligations(id),
  movement_id   UUID          NOT NULL REFERENCES financial_movements(id),
  amount        DECIMAL(12,2) NOT NULL CHECK (amount > 0),
  deleted_at    TIMESTAMPTZ,
  created_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_settlements_obligation_id ON settlements(obligation_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_settlements_movement_id   ON settlements(movement_id)   WHERE deleted_at IS NULL;

DROP VIEW IF EXISTS vw_vendas_resumo;
DROP VIEW IF EXISTS vw_purchases_resumo;

CREATE VIEW vw_purchase_costs AS
SELECT
  p.id                                          AS purchase_id,
  p.supplier_id,
  COALESCE(si.total, 0) + COALESCE(pi.total, 0) AS total_cost,
  COALESCE(si.lines, 0) + COALESCE(pi.lines, 0) AS line_count
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

-- Componentes das contas
-- `amount` zera numa Sale CANCELED (a conta sai dos totais); `base_amount`
-- é o valor "de verdade" e decide se a conta existe (usado pela sincronização).
CREATE VIEW vw_obligation_components AS
SELECT
  'SALE'::text AS origin_type, v.id AS origin_id,
  'RECEIVABLE'::text AS direction, 'CUSTOMER'::text AS counterparty_type, v.cliente_id AS counterparty_id,
  'SALE_ITEMS'::text AS type, v.id AS component_id, 'Itens da venda'::text AS label,
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE ROUND(it.total, 2) END)::numeric(12,2) AS amount,
  ROUND(it.total, 2)::numeric(12,2) AS base_amount
FROM vendas v
JOIN (
  SELECT venda_id, SUM(quantidade * preco_venda_unit) AS total FROM itens_venda GROUP BY venda_id
) it ON it.venda_id = v.id
WHERE v.deleted_at IS NULL AND ROUND(it.total, 2) > 0
UNION ALL
SELECT
  'SALE', v.id, 'PAYABLE', NULL::text, NULL::uuid,
  'LEGACY_FREIGHT', v.id, 'Frete',
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE v.frete_valor END)::numeric(12,2), v.frete_valor::numeric(12,2)
FROM vendas v
WHERE v.deleted_at IS NULL AND v.frete_valor > 0
UNION ALL
SELECT
  'SALE', v.id, 'PAYABLE',
  CASE WHEN v.vendedor_externo_id IS NOT NULL THEN 'PARTNER' END, v.vendedor_externo_id,
  'LEGACY_COMMISSION', v.id, 'Comissão',
  (CASE WHEN v.delivery_status = 'CANCELED' THEN 0 ELSE v.comissao_valor END)::numeric(12,2), v.comissao_valor::numeric(12,2)
FROM vendas v
WHERE v.deleted_at IS NULL AND v.comissao_valor > 0
UNION ALL
SELECT
  'PURCHASE', pc.purchase_id, 'PAYABLE', 'SUPPLIER', pc.supplier_id,
  'PURCHASE_ITEMS', pc.purchase_id, 'Itens da compra',
  ROUND(pc.total_cost, 2)::numeric(12,2), ROUND(pc.total_cost, 2)::numeric(12,2)
FROM vw_purchase_costs pc
WHERE ROUND(pc.total_cost, 2) > 0;

CREATE VIEW vw_installment_coverage AS
SELECT
  x.*,
  CASE WHEN x.covered >= x.amount THEN 'COVERED' WHEN x.covered > 0 THEN 'PARTIAL' ELSE 'OPEN' END AS status,
  (x.covered < x.amount AND x.due_date < (NOW() AT TIME ZONE 'America/Sao_Paulo')::date)           AS overdue
FROM (
  SELECT
    i.id, i.obligation_id, i.due_date, i.amount, i.payment_method, i.notes, i.created_at,
    LEAST(i.amount, GREATEST(
      COALESCE(s.settled, 0) - (SUM(i.amount) OVER (PARTITION BY i.obligation_id ORDER BY i.due_date, i.created_at, i.id) - i.amount),
      0)) AS covered
  FROM installments i
  LEFT JOIN (
    SELECT obligation_id, SUM(amount) AS settled FROM settlements WHERE deleted_at IS NULL GROUP BY obligation_id
  ) s ON s.obligation_id = i.obligation_id
  WHERE i.deleted_at IS NULL
) x;

-- Contas: valor = Σ componentes do credor; quitado = Σ quitações (pré-agregados — nunca juntar
-- componentes e quitações no mesmo GROUP BY).
CREATE VIEW vw_obligations AS
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

CREATE VIEW vw_movement_allocation AS
SELECT
  m.id AS movement_id,
  COALESCE(s.allocated, 0)                                      AS allocated,
  GREATEST(ABS(m.amount) - COALESCE(s.allocated, 0), 0)         AS unallocated
FROM financial_movements m
LEFT JOIN (
  SELECT movement_id, SUM(amount) AS allocated FROM settlements WHERE deleted_at IS NULL GROUP BY movement_id
) s ON s.movement_id = m.id
WHERE m.deleted_at IS NULL;

-- MANUTENÇÃO AUTOMÁTICA DAS CONTAS (§2): sincronização idempotente por origem.
-- Garante a conta de cada (origem, direção, credor) presente nos componentes e remove (soft)
-- a conta sem componentes e sem quitações. Chamada por triggers em toda escrita que altera
-- componentes — nenhum caminho de escrita (API, conector, SQL) precisa lembrar de sincronizar.
CREATE FUNCTION sp_sync_obligations(p_origin_type TEXT, p_origin_id UUID) RETURNS void AS $$
BEGIN
  IF p_origin_id IS NULL THEN
    RETURN;
  END IF;

  INSERT INTO obligations (direction, sale_id, purchase_id, supplier_id, partner_id, customer_id)
  SELECT d.direction::obligation_direction,
         CASE WHEN p_origin_type = 'SALE' THEN p_origin_id END,
         CASE WHEN p_origin_type = 'PURCHASE' THEN p_origin_id END,
         CASE WHEN d.counterparty_type = 'SUPPLIER' THEN d.counterparty_id END,
         CASE WHEN d.counterparty_type = 'PARTNER'  THEN d.counterparty_id END,
         CASE WHEN d.counterparty_type = 'CUSTOMER' THEN d.counterparty_id END
  FROM (
    SELECT DISTINCT direction, counterparty_type, counterparty_id
    FROM vw_obligation_components
    WHERE origin_type = p_origin_type AND origin_id = p_origin_id
  ) d
  WHERE NOT EXISTS (
    SELECT 1 FROM obligations o
    WHERE o.deleted_at IS NULL AND o.origin_id = p_origin_id
      AND o.direction::text = d.direction
      AND o.counterparty_id IS NOT DISTINCT FROM d.counterparty_id
  )
  ON CONFLICT DO NOTHING;

  UPDATE obligations o SET deleted_at = NOW()
  WHERE o.deleted_at IS NULL AND o.origin_id = p_origin_id
    AND NOT EXISTS (
      SELECT 1 FROM vw_obligation_components c
      WHERE c.origin_type = p_origin_type AND c.origin_id = p_origin_id
        AND c.direction = o.direction::text
        AND c.counterparty_id IS NOT DISTINCT FROM o.counterparty_id
    )
    AND NOT EXISTS (
      SELECT 1 FROM settlements s WHERE s.obligation_id = o.id AND s.deleted_at IS NULL
    );
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION trg_sync_obligations_sale() RETURNS trigger AS $$
BEGIN
  PERFORM sp_sync_obligations('SALE', NEW.id);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION trg_sync_obligations_sale_item() RETURNS trigger AS $$
BEGIN
  IF TG_OP IN ('INSERT', 'UPDATE') THEN
    PERFORM sp_sync_obligations('SALE', NEW.venda_id);
    PERFORM sp_sync_obligations('PURCHASE', NEW.purchase_id);
  END IF;
  IF TG_OP IN ('UPDATE', 'DELETE') THEN
    PERFORM sp_sync_obligations('SALE', OLD.venda_id);
    PERFORM sp_sync_obligations('PURCHASE', OLD.purchase_id);
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION trg_sync_obligations_purchase() RETURNS trigger AS $$
BEGIN
  PERFORM sp_sync_obligations('PURCHASE', NEW.id);
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE FUNCTION trg_sync_obligations_purchase_item() RETURNS trigger AS $$
BEGIN
  IF TG_OP IN ('INSERT', 'UPDATE') THEN
    PERFORM sp_sync_obligations('PURCHASE', NEW.purchase_id);
  END IF;
  IF TG_OP IN ('UPDATE', 'DELETE') THEN
    PERFORM sp_sync_obligations('PURCHASE', OLD.purchase_id);
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

SELECT sp_sync_obligations('SALE', id) FROM vendas;
SELECT sp_sync_obligations('PURCHASE', id) FROM purchases;

-- 2) Receipts (não excluídos) → parcelas da conta RECEIVABLE da Sale. O id do Receipt vira o id
--    da parcela (preserva o `lida` dos alertas). Sale sem conta (ex.: sem Items) ganha a casca.
INSERT INTO obligations (direction, sale_id, customer_id)
SELECT DISTINCT 'RECEIVABLE'::obligation_direction, v.id, v.cliente_id
FROM pagamentos_cliente p
JOIN vendas v ON v.id = p.venda_id
WHERE p.deleted_at IS NULL AND p.valor > 0
  AND NOT EXISTS (SELECT 1 FROM obligations o
                  WHERE o.deleted_at IS NULL AND o.sale_id = v.id AND o.direction = 'RECEIVABLE');

INSERT INTO installments (id, obligation_id, due_date, amount, payment_method, notes, created_at)
SELECT p.id, o.id, COALESCE(p.data_agendada, p.data_pagamento, p.created_at::date), p.valor,
       p.payment_method, p.observacoes, p.created_at
FROM pagamentos_cliente p
JOIN obligations o ON o.sale_id = p.venda_id AND o.direction = 'RECEIVABLE' AND o.deleted_at IS NULL
WHERE p.deleted_at IS NULL AND p.valor > 0;

-- 3) Receipts recebidos → movimentação SALE_RECEIPT + quitação (o id do Receipt vira o da movimentação)
INSERT INTO financial_movements (id, occurred_on, amount, category, payment_method, description, created_at)
SELECT p.id, COALESCE(p.data_pagamento, p.data_agendada, p.created_at::date), p.valor, 'SALE_RECEIPT',
       p.payment_method, COALESCE(p.observacoes, 'Migração: recebimento'), p.created_at
FROM pagamentos_cliente p
WHERE p.deleted_at IS NULL AND p.valor > 0 AND p.status = 'recebido';

INSERT INTO settlements (obligation_id, movement_id, amount, created_at)
SELECT o.id, p.id, p.valor, p.created_at
FROM pagamentos_cliente p
JOIN obligations o ON o.sale_id = p.venda_id AND o.direction = 'RECEIVABLE' AND o.deleted_at IS NULL
WHERE p.deleted_at IS NULL AND p.valor > 0 AND p.status = 'recebido';

INSERT INTO obligations (direction, purchase_id, supplier_id)
SELECT 'PAYABLE'::obligation_direction, p.id, p.supplier_id
FROM purchases p
WHERE p.paid_amount > 0
  AND NOT EXISTS (SELECT 1 FROM obligations o
                  WHERE o.deleted_at IS NULL AND o.purchase_id = p.id AND o.direction = 'PAYABLE'
                    AND o.supplier_id = p.supplier_id);

CREATE TEMP TABLE mig_purchase_payments AS
SELECT gen_random_uuid() AS movement_id, p.id AS purchase_id, p.supplier_id, p.paid_amount,
       COALESCE(p.paid_at, p.created_at::date) AS occurred_on, p.created_at
FROM purchases p
WHERE p.paid_amount > 0;

INSERT INTO financial_movements (id, occurred_on, amount, category, description, created_at)
SELECT movement_id, occurred_on, -paid_amount, 'PURCHASE_PAYMENT', 'Migração: pagamento ao fornecedor', created_at
FROM mig_purchase_payments;

INSERT INTO settlements (obligation_id, movement_id, amount, created_at)
SELECT o.id, m.movement_id, m.paid_amount, m.created_at
FROM mig_purchase_payments m
JOIN obligations o ON o.purchase_id = m.purchase_id AND o.direction = 'PAYABLE'
                  AND o.supplier_id = m.supplier_id AND o.deleted_at IS NULL;

CREATE TEMP TABLE mig_sale_payments AS
SELECT gen_random_uuid() AS movement_id, v.id AS sale_id, 'FREIGHT'::movement_category AS category,
       v.frete_pago AS amount, NULL::uuid AS partner_id, v.data_venda, v.created_at
FROM vendas v
WHERE v.frete_pago > 0
UNION ALL
SELECT gen_random_uuid(), v.id, 'COMMISSION', v.comissao_valor, v.vendedor_externo_id, v.data_venda, v.created_at
FROM vendas v
WHERE v.comissao_paga AND v.comissao_valor > 0;

-- garante a casca da conta (a Sale pode ter frete_pago > 0 com frete_valor = 0, por ex.)
INSERT INTO obligations (direction, sale_id, partner_id)
SELECT DISTINCT 'PAYABLE'::obligation_direction, sp.sale_id, sp.partner_id
FROM mig_sale_payments sp
WHERE NOT EXISTS (SELECT 1 FROM obligations o
                  WHERE o.deleted_at IS NULL AND o.sale_id = sp.sale_id AND o.direction = 'PAYABLE'
                    AND o.counterparty_id IS NOT DISTINCT FROM sp.partner_id);

INSERT INTO financial_movements (id, occurred_on, amount, category, description, created_at)
SELECT movement_id, data_venda, -amount, category, 'Migração: pagamento legado (data = data da venda)', created_at
FROM mig_sale_payments;

INSERT INTO settlements (obligation_id, movement_id, amount, created_at)
SELECT o.id, sp.movement_id, sp.amount, sp.created_at
FROM mig_sale_payments sp
JOIN obligations o ON o.sale_id = sp.sale_id AND o.direction = 'PAYABLE' AND o.deleted_at IS NULL
                  AND o.counterparty_id IS NOT DISTINCT FROM sp.partner_id;

-- 6) Verificação interna: o dinheiro movimentado antes = depois (aborta a migração se divergir)
DO $$
DECLARE
  v_bad INT;
  v_notice INT;
BEGIN
  SELECT COUNT(*) INTO v_bad FROM (
    SELECT COALESCE(r.sale_id, s.sale_id) AS sale_id
    FROM (SELECT venda_id AS sale_id, SUM(valor) AS total FROM pagamentos_cliente
          WHERE deleted_at IS NULL AND status = 'recebido' AND valor > 0 GROUP BY venda_id) r
    FULL JOIN (SELECT o.sale_id, SUM(st.amount) AS total FROM settlements st
               JOIN obligations o ON o.id = st.obligation_id
               WHERE o.direction = 'RECEIVABLE' GROUP BY o.sale_id) s ON s.sale_id = r.sale_id
    WHERE COALESCE(r.total, 0) <> COALESCE(s.total, 0)
  ) x;
  IF v_bad > 0 THEN
    RAISE EXCEPTION 'migração 010: recebido por Sale divergente em % venda(s)', v_bad;
  END IF;

  SELECT COUNT(*) INTO v_bad FROM (
    SELECT p.id
    FROM purchases p
    LEFT JOIN (SELECT o.purchase_id, SUM(st.amount) AS total FROM settlements st
               JOIN obligations o ON o.id = st.obligation_id
               WHERE o.direction = 'PAYABLE' AND o.purchase_id IS NOT NULL GROUP BY o.purchase_id) s ON s.purchase_id = p.id
    WHERE COALESCE(p.paid_amount, 0) <> COALESCE(s.total, 0)
  ) x;
  IF v_bad > 0 THEN
    RAISE EXCEPTION 'migração 010: pago por Purchase divergente em % compra(s)', v_bad;
  END IF;

  SELECT COUNT(*) INTO v_bad FROM (
    SELECT 1
    WHERE (SELECT COALESCE(SUM(frete_pago), 0) + COALESCE(SUM(comissao_valor) FILTER (WHERE comissao_paga AND comissao_valor > 0), 0) FROM vendas)
       <> (SELECT COALESCE(SUM(st.amount), 0) FROM settlements st JOIN obligations o ON o.id = st.obligation_id
           WHERE o.direction = 'PAYABLE' AND o.sale_id IS NOT NULL)
  ) x;
  IF v_bad > 0 THEN
    RAISE EXCEPTION 'migração 010: frete/comissão pagos divergentes';
  END IF;

  SELECT COUNT(*) INTO v_notice
  FROM purchases p JOIN vw_purchase_costs pc ON pc.purchase_id = p.id
  WHERE p.payment_status = 'PAID' AND COALESCE(p.paid_amount, 0) < ROUND(pc.total_cost, 2);
  IF v_notice > 0 THEN
    RAISE NOTICE 'migração 010: % compra(s) PAID com valor pago menor que o custo passam a ter saldo em aberto', v_notice;
  END IF;
END $$;

DROP TABLE mig_purchase_payments;
DROP TABLE mig_sale_payments;

ALTER TABLE notificacoes
  ADD COLUMN obligation_id UUID REFERENCES obligations(id),
  ADD COLUMN installment_id UUID REFERENCES installments(id);

UPDATE notificacoes n
SET obligation_id = i.obligation_id, installment_id = i.id
FROM installments i
WHERE i.id = n.pagamento_id;

DELETE FROM notificacoes WHERE obligation_id IS NULL;

ALTER TABLE notificacoes DROP COLUMN pagamento_id;
ALTER TABLE notificacoes ALTER COLUMN obligation_id SET NOT NULL;
CREATE UNIQUE INDEX uq_notificacoes_obligation_installment
  ON notificacoes (obligation_id, COALESCE(installment_id, '00000000-0000-0000-0000-000000000000'::uuid));

DROP TABLE pagamentos_cliente;
DROP TYPE status_pagamento;

ALTER TABLE purchases
  DROP COLUMN payment_status,
  DROP COLUMN paid_amount,
  DROP COLUMN paid_at;
DROP TYPE supplier_payment_status;

ALTER TABLE vendas
  DROP COLUMN frete_pago,
  DROP COLUMN comissao_paga;

-- Resumo por Sale: uma linha por Sale (inclusive CANCELED — o painel/dashboard filtram).
-- total_recebido / receivable vêm da conta RECEIVABLE; freight_payable / commission_payable das
-- contas PAYABLE. Frete e comissão SEM Partner dividem a conta "a definir": o que foi quitado
-- com categoria COMMISSION abate a comissão; o restante abate o frete primeiro e, se sobrar, a comissão.
CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(it.total_venda, 0)    AS total_venda,
  COALESCE(it.total_custo, 0)    AS total_custo,
  COALESCE(rc.settled, 0)        AS total_recebido,
  COALESCE(it.total_venda, 0)
    - COALESCE(it.total_custo, 0)
    - v.frete_valor
    - v.comissao_valor           AS lucro,
  CASE
    WHEN COALESCE(rc.settled, 0) <= 0                                  THEN 'PENDING'
    WHEN COALESCE(rc.settled, 0) < ROUND(COALESCE(it.total_venda, 0), 2) THEN 'PARTIAL'
    ELSE 'PAID'
  END                            AS payment_status,
  COALESCE(rc.open, 0)           AS receivable,
  COALESCE(py.freight_payable, 0)                                       AS freight_payable,
  COALESCE(py.partner_open, 0) + COALESCE(py.nocred_commission_payable, 0) AS commission_payable
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
  SELECT sale_id, SUM(settled) AS settled, SUM(open) AS open
  FROM vw_obligations
  WHERE direction = 'RECEIVABLE'
  GROUP BY sale_id
) rc ON rc.sale_id = v.id
LEFT JOIN (
  SELECT
    p.sale_id,
    p.partner_open,
    GREATEST(p.freight_eff - p.settled_other, 0) AS freight_payable,
    GREATEST(p.nocred_amount - p.freight_eff - p.settled_comm - GREATEST(p.settled_other - p.freight_eff, 0), 0) AS nocred_commission_payable
  FROM (
    SELECT
      o.sale_id,
      COALESCE(SUM(o.open) FILTER (WHERE o.counterparty_type = 'PARTNER'), 0)                      AS partner_open,
      COALESCE(SUM(o.amount) FILTER (WHERE o.counterparty_id IS NULL), 0)                          AS nocred_amount,
      LEAST(MAX(sv.frete_valor), COALESCE(SUM(o.amount) FILTER (WHERE o.counterparty_id IS NULL), 0)) AS freight_eff,
      COALESCE(SUM(COALESCE(cs.comm, 0)) FILTER (WHERE o.counterparty_id IS NULL), 0)              AS settled_comm,
      COALESCE(SUM(o.settled - COALESCE(cs.comm, 0)) FILTER (WHERE o.counterparty_id IS NULL), 0)  AS settled_other
    FROM vw_obligations o
    JOIN vendas sv ON sv.id = o.sale_id
    LEFT JOIN (
      SELECT s.obligation_id, SUM(s.amount) AS comm
      FROM settlements s JOIN financial_movements m ON m.id = s.movement_id
      WHERE s.deleted_at IS NULL AND m.deleted_at IS NULL AND m.category = 'COMMISSION'
      GROUP BY s.obligation_id
    ) cs ON cs.obligation_id = o.id
    WHERE o.direction = 'PAYABLE' AND o.sale_id IS NOT NULL
    GROUP BY o.sale_id
  ) p
) py ON py.sale_id = v.id
WHERE v.deleted_at IS NULL;

CREATE VIEW vw_purchases_resumo AS
SELECT
  p.*,
  pc.total_cost,
  pc.line_count,
  COALESCE(ob.settled, 0) AS paid,
  COALESCE(ob.open, 0)    AS payable,
  CASE WHEN COALESCE(ob.amount, 0) > 0 AND COALESCE(ob.open, 0) <= 0 THEN 'PAID' ELSE 'PENDING' END AS payment_status
FROM purchases p
JOIN vw_purchase_costs pc ON pc.purchase_id = p.id
LEFT JOIN (
  SELECT purchase_id, SUM(amount) AS amount, SUM(settled) AS settled, SUM(open) AS open
  FROM vw_obligations
  WHERE direction = 'PAYABLE' AND purchase_id IS NOT NULL
  GROUP BY purchase_id
) ob ON ob.purchase_id = p.id
WHERE p.deleted_at IS NULL;

CREATE VIEW vw_payment_alerts AS
SELECT
  o.id AS obligation_id, ic.id AS installment_id, o.sale_id, o.direction::text AS direction,
  COALESCE(o.counterparty_name, 'A definir') AS counterparty_name,
  (ic.amount - ic.covered) AS amount, ic.due_date AS due_date
FROM vw_obligations o
JOIN vw_installment_coverage ic ON ic.obligation_id = o.id
WHERE o.open > 0 AND ic.status <> 'COVERED'
  AND ic.due_date <= (NOW() AT TIME ZONE 'America/Sao_Paulo')::date + 5
UNION ALL
SELECT
  o.id, NULL::uuid, o.sale_id, o.direction::text, COALESCE(o.counterparty_name, 'A definir'), o.open, o.due_date
FROM vw_obligations o
WHERE o.open > 0 AND o.due_date IS NOT NULL
  AND o.due_date <= (NOW() AT TIME ZONE 'America/Sao_Paulo')::date + 5
  AND NOT EXISTS (SELECT 1 FROM vw_installment_coverage ic WHERE ic.obligation_id = o.id AND ic.status <> 'COVERED');

CREATE OR REPLACE FUNCTION gerar_notificacoes_pagamentos()
RETURNS void AS $$
DECLARE
  -- "Hoje" no calendário do negócio (America/Sao_Paulo), não em UTC.
  v_today CONSTANT DATE := (NOW() AT TIME ZONE 'America/Sao_Paulo')::date;
BEGIN
  DELETE FROM notificacoes n
  WHERE NOT EXISTS (
    SELECT 1 FROM vw_payment_alerts a
    WHERE a.obligation_id = n.obligation_id AND a.installment_id IS NOT DISTINCT FROM n.installment_id
  );

  -- Cria/atualiza os alertas. `lida` só volta a FALSE se o tipo (vencendo → vencido) ou a
  -- data_ref (reagendado) mudou; atualizar dias_restantes ou valor sozinho não reabre.
  INSERT INTO notificacoes (tipo, venda_id, obligation_id, installment_id, cliente_nome, valor, data_ref, dias_restantes)
  SELECT
    CASE WHEN a.direction = 'RECEIVABLE'
         THEN CASE WHEN a.due_date < v_today THEN 'pagamento_vencido' ELSE 'pagamento_vencendo' END
         ELSE CASE WHEN a.due_date < v_today THEN 'conta_pagar_vencida' ELSE 'conta_pagar_vencendo' END
    END,
    a.sale_id, a.obligation_id, a.installment_id, a.counterparty_name, a.amount, a.due_date,
    (a.due_date - v_today)::int
  FROM vw_payment_alerts a
  ON CONFLICT (obligation_id, (COALESCE(installment_id, '00000000-0000-0000-0000-000000000000'::uuid))) DO UPDATE SET
    tipo           = EXCLUDED.tipo,
    venda_id       = EXCLUDED.venda_id,
    valor          = EXCLUDED.valor,
    data_ref       = EXCLUDED.data_ref,
    dias_restantes = EXCLUDED.dias_restantes,
    cliente_nome   = EXCLUDED.cliente_nome,
    lida = CASE
             WHEN notificacoes.tipo     IS DISTINCT FROM EXCLUDED.tipo
               OR notificacoes.data_ref IS DISTINCT FROM EXCLUDED.data_ref THEN FALSE
             ELSE notificacoes.lida
           END;
END;
$$ LANGUAGE plpgsql;

-- TRIGGERS de sincronização das contas (criados por último: a carga acima já sincronizou)
CREATE TRIGGER trg_obligations_sync AFTER INSERT OR UPDATE ON vendas
  FOR EACH ROW EXECUTE FUNCTION trg_sync_obligations_sale();
CREATE TRIGGER trg_obligations_sync AFTER INSERT OR UPDATE OR DELETE ON itens_venda
  FOR EACH ROW EXECUTE FUNCTION trg_sync_obligations_sale_item();
CREATE TRIGGER trg_obligations_sync AFTER INSERT OR UPDATE ON purchases
  FOR EACH ROW EXECUTE FUNCTION trg_sync_obligations_purchase();
CREATE TRIGGER trg_obligations_sync AFTER INSERT OR UPDATE OR DELETE ON purchase_items
  FOR EACH ROW EXECUTE FUNCTION trg_sync_obligations_purchase_item();
