-- Reverte a 007 para um schema 006 funcional.
-- ATENÇÃO: Receipts soft-deletados (deleted_at) voltam a contar em total_recebido /
-- payment_status quando a coluna some; payment_method se perde. Para preservar a exclusão,
-- apague fisicamente os Receipts com deleted_at ANTES do down (ver docs/ops/database.md).

DROP VIEW IF EXISTS vw_vendas_resumo;

-- Função de alertas como em docs/ops/pg_cron_setup.sql da 006 (sem deleted_at / preservação de lida).
CREATE OR REPLACE FUNCTION gerar_notificacoes_pagamentos()
RETURNS void AS $$
BEGIN
  DELETE FROM notificacoes
  WHERE pagamento_id IN (
    SELECT id FROM pagamentos_cliente WHERE status = 'recebido'
  )
  OR venda_id IN (SELECT id FROM vendas WHERE delivery_status = 'CANCELED');

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
    AND v.delivery_status <> 'CANCELED'
  ON CONFLICT (pagamento_id) DO UPDATE SET
    tipo = EXCLUDED.tipo,
    dias_restantes = EXCLUDED.dias_restantes,
    lida = FALSE;
END;
$$ LANGUAGE plpgsql;

DROP INDEX IF EXISTS idx_pagamentos_cliente_data_pagamento;
ALTER TABLE pagamentos_cliente
  DROP COLUMN payment_method,
  DROP COLUMN deleted_at;
DROP TYPE payment_method;

-- vw_vendas_resumo como na 006.
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
  END                            AS payment_status
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
  GROUP BY venda_id
) rc ON rc.venda_id = v.id
WHERE v.deleted_at IS NULL;
