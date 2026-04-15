-- Extensões
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ENUMS
CREATE TYPE status_venda AS ENUM (
  'A_ENTREGAR',
  'A_ENTREGAR_DATA',
  'A_RETIRAR',
  'ENTREGUE_PARCIAL',
  'ENTREGUE'
);

CREATE TYPE status_pagamento AS ENUM ('pendente', 'recebido');

-- CLIENTES
CREATE TABLE clientes (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  nome        TEXT        NOT NULL,
  telefone    TEXT,
  email       TEXT,
  endereco    TEXT,
  cidade      TEXT,
  observacoes TEXT,
  deleted_at  TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- PRODUTOS (CATÁLOGO)
CREATE TABLE produtos (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  nome             TEXT        NOT NULL,
  descricao        TEXT,
  preco_referencia DECIMAL(12,2),
  custo_referencia DECIMAL(12,2),
  unidade          TEXT,
  deleted_at       TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- FORNECEDORES
CREATE TABLE fornecedores (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  nome        TEXT        NOT NULL,
  telefone    TEXT,
  cidade      TEXT,
  observacoes TEXT,
  deleted_at  TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- VENDEDORES EXTERNOS
CREATE TABLE vendedores_externos (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  nome        TEXT        NOT NULL,
  telefone    TEXT,
  observacoes TEXT,
  deleted_at  TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- VENDAS
CREATE TABLE vendas (
  id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  cliente_id            UUID         NOT NULL REFERENCES clientes(id),
  vendedor_externo_id   UUID         REFERENCES vendedores_externos(id),
  status                status_venda NOT NULL DEFAULT 'A_ENTREGAR',
  data_venda            DATE         NOT NULL DEFAULT CURRENT_DATE,
  data_entrega_prevista DATE,
  frete_valor           DECIMAL(12,2) NOT NULL DEFAULT 0,
  frete_pago            DECIMAL(12,2) NOT NULL DEFAULT 0,
  comissao_valor        DECIMAL(12,2) NOT NULL DEFAULT 0,
  comissao_paga         BOOLEAN      NOT NULL DEFAULT FALSE,
  observacoes           TEXT,
  deleted_at            TIMESTAMPTZ,
  created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ITENS DE VENDA
CREATE TABLE itens_venda (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  venda_id         UUID        NOT NULL REFERENCES vendas(id) ON DELETE CASCADE,
  produto_id       UUID        REFERENCES produtos(id),
  produto_nome     TEXT        NOT NULL,
  fornecedor_id    UUID        REFERENCES fornecedores(id),
  fornecedor_nome  TEXT,
  quantidade       DECIMAL(12,3) NOT NULL,
  preco_venda_unit DECIMAL(12,2) NOT NULL,
  custo_unit       DECIMAL(12,2) NOT NULL,
  pago_fornecedor  DECIMAL(12,2) NOT NULL DEFAULT 0,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- PAGAMENTOS DO CLIENTE
CREATE TABLE pagamentos_cliente (
  id             UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
  venda_id       UUID             NOT NULL REFERENCES vendas(id) ON DELETE CASCADE,
  valor          DECIMAL(12,2)    NOT NULL,
  data_pagamento DATE,
  data_agendada  DATE,
  status         status_pagamento NOT NULL DEFAULT 'pendente',
  observacoes    TEXT,
  created_at     TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

-- ESTOQUE
CREATE TABLE estoque (
  id                 UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  produto_id         UUID          REFERENCES produtos(id),
  produto_nome       TEXT          NOT NULL,
  fornecedor_id      UUID          REFERENCES fornecedores(id),
  quantidade_entrada DECIMAL(12,3) NOT NULL,
  custo_unit         DECIMAL(12,2) NOT NULL,
  data_entrada       DATE          NOT NULL DEFAULT CURRENT_DATE,
  observacoes        TEXT,
  created_at         TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- VIEW DE RESUMO (campos calculados — nunca persistidos)
CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(SUM(i.quantidade * i.preco_venda_unit), 0)                          AS total_venda,
  COALESCE(SUM(i.quantidade * i.custo_unit), 0)                                AS total_custo,
  COALESCE(SUM(p.valor) FILTER (WHERE p.status = 'recebido'), 0)               AS total_recebido,
  COALESCE(SUM(i.quantidade * i.custo_unit) - SUM(i.pago_fornecedor), 0)       AS a_pagar_fornecedor,
  COALESCE(SUM(i.quantidade * i.preco_venda_unit), 0)
    - COALESCE(SUM(i.quantidade * i.custo_unit), 0)
    - v.frete_valor
    - v.comissao_valor                                                          AS lucro
FROM vendas v
LEFT JOIN itens_venda i       ON i.venda_id = v.id
LEFT JOIN pagamentos_cliente p ON p.venda_id = v.id
WHERE v.deleted_at IS NULL
GROUP BY v.id;

-- ÍNDICES
CREATE INDEX idx_vendas_cliente_id   ON vendas(cliente_id);
CREATE INDEX idx_vendas_status       ON vendas(status);
CREATE INDEX idx_vendas_data_venda   ON vendas(data_venda);
CREATE INDEX idx_itens_venda_id      ON itens_venda(venda_id);
CREATE INDEX idx_pagamentos_venda_id ON pagamentos_cliente(venda_id);
CREATE INDEX idx_pagamentos_agendada ON pagamentos_cliente(data_agendada)
  WHERE status = 'pendente' AND data_agendada IS NOT NULL;
