-- Restaura a definição da 001 exatamente como estava (colunas de vendas anteriores à 003,
-- sem quote_id), inclusive o fan-out — down é o espelho fiel do estado anterior.
DROP VIEW IF EXISTS vw_vendas_resumo;

CREATE VIEW vw_vendas_resumo AS
SELECT
  v.id, v.cliente_id, v.vendedor_externo_id, v.status, v.data_venda, v.data_entrega_prevista,
  v.frete_valor, v.frete_pago, v.comissao_valor, v.comissao_paga, v.observacoes,
  v.deleted_at, v.created_at,
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
