CREATE TABLE notificacoes (
  id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  tipo           TEXT        NOT NULL,
  venda_id       UUID        REFERENCES vendas(id),
  pagamento_id   UUID        UNIQUE REFERENCES pagamentos_cliente(id),
  cliente_nome   TEXT        NOT NULL,
  valor          DECIMAL(12,2) NOT NULL,
  data_ref       DATE        NOT NULL,
  dias_restantes INT,
  lida           BOOLEAN     NOT NULL DEFAULT FALSE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notificacoes_lida ON notificacoes(lida) WHERE lida = FALSE;
