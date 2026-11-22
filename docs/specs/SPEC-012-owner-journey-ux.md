---
id: SPEC-012
type: spec
title: Jornada do dono — números, fechamento da venda, lote, busca e pendências (api)
status: done
updated: 2026-11-22
parents: [AYD-011@context]
children: []
related: [GLO, ADR-001@context]
---

# Spec: Jornada do dono (parte deste repo — api)

> O QUÊ e COMO este repo cumpre os contratos **C1–C10** do **AYD-011** §6. Rotas, campos, erros e
> enums vêm do AYD e **não são redefinidos aqui**. Tudo é **aditivo**: o front-end atual continua
> funcionando sem mudança; o catálogo do Assistant connector (`catalog.golden.json`) **não muda**.

## Objetivo

Dar à web (SPEC-013@web) o que a jornada do dono precisa: número humano de Quote/Sale/Purchase,
vínculo Quote ↔ Sale e cabeçalho editável, resumo de Items e filtros nas listas, **fechamento da
venda numa operação**, atendimento em lote, saldo atual no Item de estoque, fornecedor habitual do
Product, **busca global** e **pendências do dia** (calculadas na leitura — RNF-05).

## Critérios de aceite

```gherkin
Cenário: Número humano (C1)
  Então Quote, Sale e Purchase (list/detalhe/criação) trazem number inteiro, sequencial por entidade, sem reuso
  E a venda rápida consome um número de Quote e um de Sale
  E Obligation.origin.label vira "Venda nº 98 · Alejandra" / "Compra nº 12 · Matheus RJ"

Cenário: Quote ↔ Sale e cabeçalho (C2)
  Dado Quote WON  Então GET /quotes e /quotes/{id} trazem sale_id e sale_number (null nos demais)
  E PUT /quotes/{id} aceita customer_id? e quote_date? (ausente = não muda); só OPEN (409); customer inexistente ou excluído → 400

Cenário: Resumo e filtros das listas (C3, C4)
  Então GET /quotes e GET /vendas trazem items_count e first_item_name (1º Item por criação)
  E GET /vendas?q= casa cliente, número da Sale ou nome de Item (sem acento/caixa); ?referrer_id= filtra o Partner
  E GET /quotes?referrer_id= filtra o Partner; ?q= casa também o número do Quote

Cenário: Saldo e fornecedor habitual (C7, C8)
  Então Item FROM_STOCK em GET /vendas/{id} traz stock_balance (Balance atual); demais null
  E /produtos aceita e devolve default_supplier_id / default_supplier_name; PUT ausente mantém, null limpa; inexistente → 400

Cenário: Fechamento da venda (C5)
  Dado POST /quotes/{id}/convert ou /quotes/quick-sale com closing
  Então Item, custo da venda, parcelas e entrada são gravados na mesma transação da conversão, com as regras vigentes
  E qualquer erro desfaz tudo e responde { error, field: "closing.items[1].supplier_id" }
  E sem closing (ou {}) o comportamento é o de hoje; resposta 201 { sale_id, quote_id, sale_number, warnings[] }

Cenário: Atendimento em lote (C6)
  Dado PATCH /vendas/{id}/itens/fulfillment com 1..50 Items
  Então origem e fornecedor são aplicados como no PUT por Item, tudo ou nada; 409 traz item_id; warnings INSUFFICIENT_STOCK por Item

Cenário: Busca global (C9)  E  Pendências (C10)
  Então GET /search e GET /pending-actions respondem como o AYD (grupos só com resultado; 400 fora dos limites)
```

## Contratos consumidos/expostos

Expõe, sem redefinir, os de **AYD-011 §6**: `number`; `sale_id`/`sale_number`; `items_count`/
`first_item_name`; `GET /vendas?q&referrer_id`, `GET /quotes?referrer_id`; `closing` em
`POST /quotes/{id}/convert` e `/quotes/quick-sale`; `PATCH /vendas/{id}/itens/fulfillment`;
`itens[].stock_balance`; `/produtos` `default_supplier_id`/`default_supplier_name`;
`GET /search`; `GET /pending-actions`. Consome AYD-001/002 (conversão, atendimento do Item),
AYD-007 (contas, parcelas, quitação), AYD-009 (custos da venda), AYD-010 (Item de serviço).
OpenAPI: `docs/swagger.yaml`.

## Abordagem (como)

**Migração 014** (`migrations/014_owner_journey.{up,down}.sql`): `number` em `quotes`, `vendas` e
`purchases` (sequência própria por tabela, backfill por `created_at, id`, `NOT NULL UNIQUE`; números
podem pular em rollback, nunca se reutilizam); `produtos.default_supplier_id` → `fornecedores`;
`vw_obligations` recriada só com o novo `origin_label` (mesmas colunas — `CREATE OR REPLACE`).
Views de resumo **não mudam**: os handlers juntam a tabela base para ler `number`. Os models
**não** ganham `Number` (um `Create` com zero violaria a sequência).

**Regras puras** (sem banco, com testes unitários):
- `internal/closing`: tipos do `closing`, validação de forma (datas, valores, métodos), resolução
  dos Items por `quote_item_id`/`item_index` (origem, fornecedor, credor LABOR) e totais
  (Σ parcelas e entrada × preço), sempre com o caminho `field`.
- `internal/pendingactions`: ordem dos `kind`, horizonte, ordenação por urgência, corte em 20,
  contagens e Σ do grupo.
- `internal/search`: `q` (≥ 2, sem acento/caixa, número exato), `types`, `limit`.
- `internal/fulfillment`: validação do corpo do lote (1..50, ids únicos, enum).

**Handlers** finos: `quotes.go`/`vendas.go`/`purchases.go`/`produtos.go` ganham os campos e filtros;
`closing.go` orquestra o fechamento reutilizando `convertQuote`, `createSaleCost`,
`obligations.ReplaceInstallments` e `obligations.SettleOne` (transação aninhada = savepoint dentro
da transação da conversão); `vendas_fulfillment.go` (lote); `search.go`; `pending_actions.go`.
As pendências **reusam** `obligations.List` (novos filtros internos `NoPlan`/`NoCounterparty`),
`pendingSaleItemRows` (extraída de `/purchases/pending-sale-items`), `vw_purchases_resumo` e
`fetchStockBalances` (extraída de `/stock/balance`) — a mesma base das listas.

### Interpretações e divergências (não alteram o contrato)

1. **`closing.items[].supplier_id` com FROM_STOCK** → 400 (o AYD: "só MADE_TO_ORDER").
2. **`labor_payee_id` sem custo LABOR** (custo estimado 0 → a conversão não cria custo) é ignorado.
3. **`Σ parcelas > preço` no `closing` é 409** (AYD-011); o `PUT /obligations/{id}/installments`
   continua 400 (comportamento vigente, não alterado).
4. **Venda sem valor a receber** com `receivable_plan`/`down_payment` → 409.
5. **`down_payment`** usa a categoria padrão de recebimento de venda (`SALE_RECEIPT`) — regra existente.
6. **C9 `amount` da Purchase** = `total_cost` (pedras), o mesmo da lista de compras.
7. **C10 sem regra de `overdue` no AYD** (PURCHASE_TO_ORDER, QUOTE_STALE, ITEMS_*): `overdue` de
   ITEMS_* e DELIVERY_DUE usa o prazo de entrega da Sale; PURCHASE_TO_ORDER e QUOTE_STALE = false.
   `amount` só onde há dinheiro natural (contas, compras, orçamentos, vendas a entregar, fila a
   comprar = custo); `null` em ITEMS_WITHOUT_SUPPLIER e NEGATIVE_STOCK.
8. **C10 RECEIVABLE/PAYABLE_DUE**: `amount` do Item = aberto da próxima parcela (ou da conta, sem
   parcelas); itens e `count` cobrem todas as contas, o corte de 20 vale só para `items`.
9. **C6** Item de outra Sale ou inexistente → 400 com `item_id`; `fornecedor_id` com FROM_STOCK segue o
   PUT por Item (gravado como veio), ao contrário do `closing` (item 1).
10. **C10 COST_WITHOUT_PAYEE** só conta contas a pagar ainda **em aberto/parciais** sem credor (conta
    "a definir" já quitada não gera pendência); RECEIVABLE_WITHOUT_PLAN = sem parcela a vencer nem
    `due_date` (inclui plano totalmente coberto com saldo em aberto).
11. **`/convert` com corpo JSON malformado** responde 400 (antes o corpo era ignorado); corpo vazio ou
    sem `closing` segue idêntico.
12. **C9 `q` numérico** só busca número com ≥ 2 caracteres ("#7" ou "07"), como manda o AYD (q ≥ 2).
13. Os `Create` de Quote/Sale/Purchase não conhecem `number` (default da sequência); as views de
    resumo não foram recriadas.

## Passos de implementação

1. SPEC (este documento).
2. C1 migração 014 + `number` nas respostas + label.
3. C2, C3, C4, C7, C8.
4. C5 fechamento (`closing`) e C6 lote.
5. C9 busca, C10 pendências.
6. Testes, swagger, CLAUDE.md (fase 15), changelog.

## Arquivos / módulos afetados

- `migrations/014_owner_journey.{up,down}.sql`
- `internal/closing`, `internal/pendingactions`, `internal/search` (novos); `internal/fulfillment` (lote);
  `internal/obligations` (filtros internos da lista)
- `internal/handlers/`: `quotes.go`, `vendas.go`, `purchases.go`, `produtos.go`, `stock.go`,
  `closing.go`, `vendas_fulfillment.go`, `search.go`, `pending_actions.go`; `cmd/server/main.go`
- `internal/models/models.go` (Product), `docs/swagger.yaml`

## Testes (ver docs/conventions.md)

- **Aceite:** integração por contrato (C1–C10) em `internal/handlers/owner_journey_*_test.go`
  (pulam sem `TEST_DATABASE_URL`); atomicidade do `closing` e do lote; compatibilidade (PUT /produtos sem o campo).
- **Unit:** `internal/closing`, `internal/pendingactions`, `internal/search`, `internal/fulfillment`.
- **Não regressão:** `go test ./internal/mcp -run TestCatalogGolden` sem `-update`.

## Casos de borda & fora de escopo

- Borda: quick-sale com `item_index`; Item SERVICE com origem/fornecedor; FROM_STOCK sem Product;
  Σ parcelas > preço; entrada > preço; venda concorrente (lock do Quote); lote com Item travado em Purchase.
- Fora: mudanças no catálogo do connector; busca por conteúdo de notas; paginação de `/pending-actions`.

## Checklist de entrega

- [x] Migração 014 (up/down) aplicada e testada (up → down 1 → up)
- [x] C1–C10 implementados e testados
- [x] Nenhum campo removido/renomeado; catálogo do connector idêntico (`TestCatalogGolden` sem `-update`)
- [x] swagger, CLAUDE.md (fase 15) e changelog atualizados
