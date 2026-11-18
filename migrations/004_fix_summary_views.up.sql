-- Fase 0: corrige o fan-out de vw_vendas_resumo.
-- A versão da 001 juntava itens_venda e pagamentos_cliente no mesmo GROUP BY, então cada
-- soma era multiplicada (N itens × M recebimentos). Aqui cada lado é pré-agregado por Sale
-- antes do join (no máximo 1 linha por venda_id em cada subquery).
--
-- DROP + CREATE (em vez de CREATE OR REPLACE): `v.*` é expandido na criação, e a 003 adicionou
-- vendas.quote_id no fim da tabela — a coluna nova entraria no meio da lista de colunas da view,
-- o que CREATE OR REPLACE VIEW não permite.
DROP VIEW IF EXISTS vw_vendas_resumo;

CREATE VIEW vw_vendas_resumo AS
SELECT
  v.*,
  COALESCE(it.total_venda, 0)                                         AS total_venda,
  COALESCE(it.total_custo, 0)                                         AS total_custo,
  COALESCE(rc.total_recebido, 0)                                      AS total_recebido,
  COALESCE(it.total_custo, 0) - COALESCE(it.total_pago_fornecedor, 0) AS a_pagar_fornecedor,
  COALESCE(it.total_venda, 0)
    - COALESCE(it.total_custo, 0)
    - v.frete_valor
    - v.comissao_valor                                                AS lucro
FROM vendas v
LEFT JOIN (
  SELECT
    venda_id,
    SUM(quantidade * preco_venda_unit) AS total_venda,
    SUM(quantidade * custo_unit)       AS total_custo,
    SUM(pago_fornecedor)               AS total_pago_fornecedor
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

-- vw_quotes_resumo (003) tem um único join (quote_items) — sem fan-out; mantida como está.
