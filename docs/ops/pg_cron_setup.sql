-- =============================================================
-- Script para configurar no Supabase Dashboard → SQL Editor
-- Pré-requisito: habilitar pg_cron em Database → Extensions
-- =============================================================

-- Função que gera/atualiza notificacoes de pagamentos
CREATE OR REPLACE FUNCTION gerar_notificacoes_pagamentos()
RETURNS void AS $$
BEGIN
  -- Limpar notificacoes de pagamentos que ja foram confirmados
  DELETE FROM notificacoes
  WHERE pagamento_id IN (
    SELECT id FROM pagamentos_cliente WHERE status = 'recebido'
  );

  -- Inserir/atualizar notificacoes para pagamentos pendentes
  -- nos proximos 5 dias ou vencidos
  INSERT INTO notificacoes (tipo, venda_id, pagamento_id, cliente_nome, valor, data_ref, dias_restantes)
  SELECT
    CASE WHEN p.data_agendada < CURRENT_DATE THEN 'pagamento_vencido'
         ELSE 'pagamento_vencendo'
    END,
    v.id,
    p.id,
    c.nome,
    p.valor,
    p.data_agendada,
    (p.data_agendada - CURRENT_DATE)::int
  FROM pagamentos_cliente p
  JOIN vendas v ON v.id = p.venda_id
  JOIN clientes c ON c.id = v.cliente_id
  WHERE p.status = 'pendente'
    AND p.data_agendada IS NOT NULL
    AND p.data_agendada <= CURRENT_DATE + 5
    AND v.deleted_at IS NULL
  ON CONFLICT (pagamento_id) DO UPDATE SET
    tipo = EXCLUDED.tipo,
    dias_restantes = EXCLUDED.dias_restantes,
    lida = FALSE;
END;
$$ LANGUAGE plpgsql;

-- Agendar para rodar todo dia as 8h (horario UTC)
SELECT cron.schedule(
  'notificacoes-diarias',
  '0 8 * * *',
  'SELECT gerar_notificacoes_pagamentos()'
);
