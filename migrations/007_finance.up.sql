-- AYD-004@context: Financeiro — payment method e soft-delete no Receipt, saldos na Sale
-- (receivable / freight_payable) e geração de alertas versionada (sem pg_cron).
-- Colunas/tipos novos em inglês (ADR-001); legado PT (pagamentos_cliente, vendas) mantido.
-- Sem conversão de dados: Receipts existentes ficam com payment_method e deleted_at nulos.

-- ============================================================
-- RECEIPT: payment method + soft-delete (RN-08)
-- ============================================================
CREATE TYPE payment_method AS ENUM ('PIX', 'CASH', 'CARD', 'BOLETO', 'TRANSFER');

ALTER TABLE pagamentos_cliente
  ADD COLUMN payment_method payment_method,
  ADD COLUMN deleted_at     TIMESTAMPTZ;

-- Movimento de caixa do painel: Receipts recebidos por data de pagamento.
CREATE INDEX idx_pagamentos_cliente_data_pagamento
  ON pagamentos_cliente(data_pagamento) WHERE deleted_at IS NULL AND status = 'recebido';

-- ============================================================
-- VIEW vw_vendas_resumo: + receivable, + freight_payable; Receipts excluídos não contam
-- (nada depende da view; colunas existentes inalteradas, novas ao final).
-- ============================================================
DROP VIEW IF EXISTS vw_vendas_resumo;

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

-- ============================================================
-- ALERTAS (RF-19): geração versionada, chamada pela API (GET /dashboard/notificacoes).
-- Substitui o script manual de pg_cron (docs/ops/pg_cron_setup.sql). Idempotente.
-- ============================================================
CREATE OR REPLACE FUNCTION gerar_notificacoes_pagamentos()
RETURNS void AS $$
DECLARE
  -- "Hoje" no calendário do negócio (a API usa America/Sao_Paulo); CURRENT_DATE seria UTC e,
  -- depois das 21h em São Paulo, já seria o dia seguinte.
  v_today CONSTANT DATE := (NOW() AT TIME ZONE 'America/Sao_Paulo')::date;
BEGIN
  -- Remove o alerta quando o Receipt deixa de se qualificar (recebido, excluído, reagendado
  -- para além da janela, Sale cancelada ou excluída).
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

  -- Cria/atualiza os alertas dos Receipts pendentes vencidos ou vencendo em até 5 dias.
  -- `lida` só volta a FALSE se o tipo (vencendo → vencido) ou a data_ref (reagendado) mudou;
  -- atualizar dias_restantes sozinho não reabre o alerta.
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
