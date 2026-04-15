-- Tabela de notificacoes gerada pelo pg_cron e lida pelo dashboard
CREATE TABLE notificacoes (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  tipo           TEXT        NOT NULL,  -- 'pagamento_vencendo', 'pagamento_vencido'
  venda_id       UUID        REFERENCES vendas(id),
  pagamento_id   UUID        UNIQUE REFERENCES pagamentos_cliente(id),
  cliente_nome   TEXT        NOT NULL,
  valor          DECIMAL(12,2) NOT NULL,
  data_ref       DATE        NOT NULL,  -- data_agendada do pagamento
  dias_restantes INT,                   -- negativo se vencido
  lida           BOOLEAN     NOT NULL DEFAULT FALSE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notificacoes_lida ON notificacoes(lida) WHERE lida = FALSE;
