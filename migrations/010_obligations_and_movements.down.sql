-- Reverte a 010: recria as estruturas legadas (pagamentos_cliente, purchases.paid_*,
-- vendas.frete_pago/comissao_paga) e repopula BEST-EFFORT a partir de parcelas e quitações.
-- LOSSY: movimentações avulsas (despesas, OTHER_INCOME), parcelas de contas de Purchase, prazos e
-- notas das contas, e o `kind` dos Suppliers não voltam. Ver docs/ops/database.md.

-- Triggers e funções de sincronização
DROP TRIGGER IF EXISTS trg_obligations_sync ON vendas;
DROP TRIGGER IF EXISTS trg_obligations_sync ON itens_venda;
DROP TRIGGER IF EXISTS trg_obligations_sync ON purchases;
DROP TRIGGER IF EXISTS trg_obligations_sync ON purchase_items;
DROP FUNCTION IF EXISTS trg_sync_obligations_sale();
DROP FUNCTION IF EXISTS trg_sync_obligations_sale_item();
DROP FUNCTION IF EXISTS trg_sync_obligations_purchase();
DROP FUNCTION IF EXISTS trg_sync_obligations_purchase_item();

-- Views que dependem das colunas/tabelas novas (a função de alertas lê vw_payment_alerts)
DROP VIEW IF EXISTS vw_vendas_resumo;
DROP VIEW IF EXISTS vw_purchases_resumo;

-- ============================================================
-- Estruturas legadas
-- ============================================================
CREATE TYPE status_pagamento AS ENUM ('pendente', 'recebido');
CREATE TYPE supplier_payment_status AS ENUM ('PENDING', 'PAID');

CREATE TABLE pagamentos_cliente (
  id             UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
  venda_id       UUID             NOT NULL REFERENCES vendas(id) ON DELETE CASCADE,
  valor          DECIMAL(12,2)    NOT NULL,
  data_pagamento DATE,
  data_agendada  DATE,
  status         status_pagamento NOT NULL DEFAULT 'pendente',
  observacoes    TEXT,
  created_at     TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
  payment_method payment_method,
  deleted_at     TIMESTAMPTZ
);
CREATE INDEX idx_pagamentos_venda_id ON pagamentos_cliente(venda_id);
CREATE INDEX idx_pagamentos_agendada ON pagamentos_cliente(data_agendada)
  WHERE status = 'pendente' AND data_agendada IS NOT NULL;
CREATE INDEX idx_pagamentos_cliente_data_pagamento
  ON pagamentos_cliente(data_pagamento) WHERE deleted_at IS NULL AND status = 'recebido';

ALTER TABLE purchases
  ADD COLUMN payment_status supplier_payment_status NOT NULL DEFAULT 'PENDING',
  ADD COLUMN paid_amount    DECIMAL(12,2) CHECK (paid_amount >= 0),
  ADD COLUMN paid_at        DATE;

ALTER TABLE vendas
  ADD COLUMN frete_pago    DECIMAL(12,2) NOT NULL DEFAULT 0,
  ADD COLUMN comissao_paga BOOLEAN       NOT NULL DEFAULT FALSE;

-- ============================================================
-- Repovoar (best-effort)
-- ============================================================

-- Recebidos: uma quitação RECEIVABLE vira um Receipt "recebido".
INSERT INTO pagamentos_cliente (id, venda_id, valor, data_pagamento, status, payment_method, observacoes, created_at)
SELECT s.id, o.sale_id, s.amount, m.occurred_on, 'recebido', m.payment_method, m.description, s.created_at
FROM settlements s
JOIN obligations o ON o.id = s.obligation_id AND o.direction = 'RECEIVABLE'
JOIN financial_movements m ON m.id = s.movement_id
WHERE s.deleted_at IS NULL AND m.deleted_at IS NULL;

-- Agendados: a parte não coberta de cada parcela vira um Receipt "pendente".
INSERT INTO pagamentos_cliente (venda_id, valor, data_agendada, status, payment_method, observacoes, created_at)
SELECT o.sale_id, ic.amount - ic.covered, ic.due_date, 'pendente', ic.payment_method, ic.notes, ic.created_at
FROM vw_installment_coverage ic
JOIN obligations o ON o.id = ic.obligation_id AND o.direction = 'RECEIVABLE' AND o.deleted_at IS NULL
WHERE ic.status <> 'COVERED';

-- Pagamento ao Supplier da Purchase.
UPDATE purchases p
SET paid_amount = x.paid, paid_at = x.last_day
FROM (
  SELECT o.purchase_id, SUM(s.amount) AS paid, MAX(m.occurred_on) AS last_day
  FROM settlements s
  JOIN obligations o ON o.id = s.obligation_id AND o.direction = 'PAYABLE' AND o.purchase_id IS NOT NULL
  JOIN financial_movements m ON m.id = s.movement_id
  WHERE s.deleted_at IS NULL AND m.deleted_at IS NULL
  GROUP BY o.purchase_id
) x
WHERE x.purchase_id = p.id;

UPDATE purchases p
SET payment_status = 'PAID'
FROM (
  SELECT o.purchase_id, SUM(c.amount) AS amount, SUM(COALESCE(s.settled, 0)) AS settled
  FROM obligations o
  JOIN (SELECT origin_id, direction, counterparty_id, SUM(amount) AS amount
        FROM vw_obligation_components WHERE origin_type = 'PURCHASE'
        GROUP BY origin_id, direction, counterparty_id) c
    ON c.origin_id = o.purchase_id AND c.direction = o.direction::text
   AND c.counterparty_id IS NOT DISTINCT FROM o.counterparty_id
  LEFT JOIN (SELECT obligation_id, SUM(amount) AS settled FROM settlements WHERE deleted_at IS NULL GROUP BY obligation_id) s
    ON s.obligation_id = o.id
  WHERE o.deleted_at IS NULL AND o.purchase_id IS NOT NULL
  GROUP BY o.purchase_id
) x
WHERE x.purchase_id = p.id AND x.amount > 0 AND x.settled >= x.amount;

-- Frete e comissão pagos (pela categoria da movimentação quitada).
UPDATE vendas v
SET frete_pago = LEAST(v.frete_valor, x.paid)
FROM (
  SELECT o.sale_id, SUM(s.amount) AS paid
  FROM settlements s
  JOIN obligations o ON o.id = s.obligation_id AND o.direction = 'PAYABLE' AND o.sale_id IS NOT NULL
  JOIN financial_movements m ON m.id = s.movement_id AND m.category = 'FREIGHT'
  WHERE s.deleted_at IS NULL AND m.deleted_at IS NULL
  GROUP BY o.sale_id
) x
WHERE x.sale_id = v.id;

UPDATE vendas v
SET comissao_paga = TRUE
FROM (
  SELECT o.sale_id, SUM(s.amount) AS paid
  FROM settlements s
  JOIN obligations o ON o.id = s.obligation_id AND o.direction = 'PAYABLE' AND o.sale_id IS NOT NULL
  JOIN financial_movements m ON m.id = s.movement_id AND m.category = 'COMMISSION'
  WHERE s.deleted_at IS NULL AND m.deleted_at IS NULL
  GROUP BY o.sale_id
) x
WHERE x.sale_id = v.id AND v.comissao_valor > 0 AND x.paid >= v.comissao_valor;

-- ============================================================
-- Alertas: volta ao pagamento_id (sem correspondente — a função regenera)
-- ============================================================
DELETE FROM notificacoes;
DROP INDEX IF EXISTS uq_notificacoes_obligation_installment;
ALTER TABLE notificacoes
  DROP COLUMN obligation_id,
  DROP COLUMN installment_id,
  ADD COLUMN pagamento_id UUID UNIQUE REFERENCES pagamentos_cliente(id);

-- ============================================================
-- Remove o modelo novo
-- ============================================================
DROP FUNCTION IF EXISTS sp_sync_obligations(TEXT, UUID);
DROP VIEW IF EXISTS vw_payment_alerts;
DROP VIEW IF EXISTS vw_movement_allocation;
DROP VIEW IF EXISTS vw_obligations;
DROP VIEW IF EXISTS vw_installment_coverage;
DROP VIEW IF EXISTS vw_obligation_components;
DROP VIEW IF EXISTS vw_purchase_costs;

DROP TABLE settlements;
DROP TABLE financial_movements;
DROP TABLE installments;
DROP TABLE obligations;

DROP INDEX IF EXISTS idx_fornecedores_kind;
ALTER TABLE fornecedores DROP COLUMN kind;
DROP TYPE movement_category;
DROP TYPE obligation_direction;
DROP TYPE supplier_kind;

-- ============================================================
-- Views legadas (definições da 007 e da 006)
-- ============================================================
CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(it.total_venda, 0)    AS total_venda,
  COALESCE(it.total_custo, 0)    AS total_custo,
  COALESCE(rc.total_recebido, 0) AS total_recebido,
  COALESCE(it.total_venda, 0)
    - COALESCE(it.total_custo, 0)
    - v.frete_valor
    - v.comissao_valor           AS lucro,
  CASE
    WHEN COALESCE(rc.total_recebido, 0) <= 0                         THEN 'PENDING'
    WHEN COALESCE(rc.total_recebido, 0) < COALESCE(it.total_venda, 0) THEN 'PARTIAL'
    ELSE 'PAID'
  END                            AS payment_status,
  GREATEST(COALESCE(it.total_venda, 0) - COALESCE(rc.total_recebido, 0), 0) AS receivable,
  GREATEST(v.frete_valor - v.frete_pago, 0)                                 AS freight_payable
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
  SELECT
    venda_id,
    SUM(valor) FILTER (WHERE status = 'recebido') AS total_recebido
  FROM pagamentos_cliente
  WHERE deleted_at IS NULL
  GROUP BY venda_id
) rc ON rc.venda_id = v.id
WHERE v.deleted_at IS NULL;

CREATE VIEW vw_purchases_resumo AS
SELECT
  p.*,
  COALESCE(si.total, 0) + COALESCE(pi.total, 0)                  AS total_cost,
  CASE
    WHEN p.payment_status = 'PAID' THEN 0
    ELSE COALESCE(si.total, 0) + COALESCE(pi.total, 0) - COALESCE(p.paid_amount, 0)
  END                                                            AS to_pay,
  COALESCE(si.lines, 0) + COALESCE(pi.lines, 0)                  AS line_count
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

-- Alertas de Receipt (007)
CREATE OR REPLACE FUNCTION gerar_notificacoes_pagamentos()
RETURNS void AS $$
DECLARE
  v_today CONSTANT DATE := (NOW() AT TIME ZONE 'America/Sao_Paulo')::date;
BEGIN
  DELETE FROM notificacoes n
  WHERE n.pagamento_id IS NOT NULL
    AND NOT EXISTS (
      SELECT 1
      FROM pagamentos_cliente p
      JOIN vendas v ON v.id = p.venda_id
      WHERE p.id = n.pagamento_id
        AND p.status = 'pendente'
        AND p.deleted_at IS NULL
        AND p.data_agendada IS NOT NULL
        AND p.data_agendada <= v_today + 5
        AND v.deleted_at IS NULL
        AND v.delivery_status <> 'CANCELED'
    );

  INSERT INTO notificacoes (tipo, venda_id, pagamento_id, cliente_nome, valor, data_ref, dias_restantes)
  SELECT
    CASE WHEN p.data_agendada < v_today THEN 'pagamento_vencido'
         ELSE 'pagamento_vencendo'
    END,
    v.id,
    p.id,
    c.nome,
    p.valor,
    p.data_agendada,
    (p.data_agendada - v_today)::int
  FROM pagamentos_cliente p
  JOIN vendas v ON v.id = p.venda_id
  JOIN clientes c ON c.id = v.cliente_id
  WHERE p.status = 'pendente'
    AND p.deleted_at IS NULL
    AND p.data_agendada IS NOT NULL
    AND p.data_agendada <= v_today + 5
    AND v.deleted_at IS NULL
    AND v.delivery_status <> 'CANCELED'
  ON CONFLICT (pagamento_id) DO UPDATE SET
    tipo           = EXCLUDED.tipo,
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
