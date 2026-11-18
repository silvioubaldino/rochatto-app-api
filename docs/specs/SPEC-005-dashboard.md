---
id: SPEC-005
type: spec
title: Dashboard — KPIs do período, conversão Quote → Sale e relatórios (api)
status: done
updated: 2026-11-18
parents: [AYD-005@context]
related: [AYD-004@context, AYD-001@context, PDR-001@context, ADR-001@context, GLO]
---

# Spec: Dashboard (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-005**. Contratos (rotas, campos, regras de período e
> de conversão) vêm do AYD-005 e não são redefinidos aqui; leituras de pontos ambíguos estão
> em "Desvios / decisões".

## Objetivo

Atender RF-16, RF-17 e RF-18: `GET /dashboard/kpis` entrega os KPIs do período (faturamento,
Profit, nº de Sales, **ticket médio**, **conversão Quote → Sale**) e os saldos em aberto de hoje,
com `sales` e `balances` **idênticos** aos de `GET /finance/summary`; `/relatorios/vendas`
devolve exatamente N meses (zeros incluídos); `/relatorios/produtos` aceita `from`/`to` e
informa a `unit`. A venda rápida passa a ser marcada no Quote para não inflar a conversão.

## Critérios de aceite

```gherkin
Cenário: KPIs do período
  Dado Sales ativas, uma Sale cancelada e uma Sale fora do período
  Quando faço GET /dashboard/kpis?from&to
  Então sales.count/revenue/profit consideram só as ativas do período; average_ticket = revenue / count (2 casas), 0 se count = 0
  E sem from/to: 1º dia do mês corrente até hoje (America/Sao_Paulo); data inválida ou from > to → 400
  E open_deliveries conta as Sales não excluídas TO_DELIVER (não depende do período)

Cenário: Dashboard e Financeiro não divergem
  Quando comparo GET /dashboard/kpis e GET /finance/summary para o mesmo período
  Então sales.count/revenue/profit e balances.* são iguais (mesmo código)

Cenário: Conversão por coorte de Quote (RF-18)
  Dado Quotes aberto, perdido, ganho (convertido), excluído, fora do período e um Quote de venda rápida
  Então quotes.created = 3, won = 1, lost = 1, open = 1 e conversion_rate = 0.3333
  E o Quote da venda rápida, o excluído e o fora do período não entram
  E sem Quotes no período: conversion_rate = null (created = 0)

Cenário: Marca de venda rápida
  Quando crio pela venda rápida → Quote com quick_sale = true; demais fluxos → false
  E quick_sale enviado em POST/PUT de Quote é ignorado (somente leitura)

Cenário: Vendas por mês (RF-17)
  Quando faço GET /relatorios/vendas?meses=N (1..60, default 12; fora disso → 400)
  Então vêm EXATAMENTE N itens, do mês (corrente − N + 1) ao corrente (São Paulo), crescentes, mes = YYYY-MM-01
  E meses sem Sale vêm com total_venda = 0, lucro = 0, sales_count = 0; Sales canceladas não entram

Cenário: Products mais vendidos (RF-16)
  Quando faço GET /relatorios/produtos?from&to (ou os legados data_inicio/data_fim)
  Então top 20 por receita com médias ponderadas e unit (null em Item legado sem Product)
  E from/to vencem os legados se ambos forem enviados; data inválida ou from > to → 400

Cenário: Migração 008
  up: quotes.quick_sale (DEFAULT false) + backfill heurístico (WON, mesma data, Sale criada em ≤ 10 s); down → up funciona
```

## Contratos consumidos/expostos

Os do AYD-005 (`GET /dashboard/kpis`, `quick_sale` no Quote, `/relatorios/vendas` e
`/relatorios/produtos` corrigidos). `GET /dashboard/resumo` fica **deprecated**, sem mudança de
comportamento. OpenAPI: `docs/swagger.yaml`.

## Abordagem (como)

- **Migração 008** (`migrations/008_quote_quick_sale.{up,down}.sql`): `quotes.quick_sale BOOLEAN NOT NULL DEFAULT false`;
  backfill `true` para Quotes `WON` com Sale (`vendas.quote_id`) de `data_venda = quote_date` e
  `vendas.created_at` entre `quotes.created_at` e `+ 10 s`; `vw_quotes_resumo` **recriada** (a view usa
  `q.*`, cuja lista de colunas é fixada na criação — sem recriar, `quick_sale` não apareceria).
  `down` derruba a view, a coluna e recria a view como na 003/004.
- **Quote**: `models.Quote.QuickSale` (`json:"quick_sale"`); `insertQuote(..., quickSale bool)` — só
  `quickSale` (`POST /quotes/quick-sale`) passa `true`. Os payloads de create/update não têm o campo
  (o JSON extra é ignorado). Respostas de lista/detalhe expõem `quick_sale`.
- **Consultas compartilhadas** (`finance.go`): `querySalesBlock(db, from, to, *financeSales)` e
  `queryBalances(db, *financeBalances)` são chamadas por `/finance/summary` e `/dashboard/kpis` — nenhum SQL duplicado.
- **Regras puras** em `internal/finance/dashboard.go` (`AverageTicket`, `ConversionRate`, `Round2`, `Round4`) com testes
  unitários; período via `finance.ResolvePeriod` (mesmo helper do painel).
- **KPIs** (`dashboard.go`): `quotes` por uma única consulta com `COUNT(*) FILTER (WHERE status = …)` sobre `quotes`
  (`deleted_at IS NULL AND quick_sale = FALSE AND quote_date BETWEEN`); `conversion_rate` é `*float64` (JSON `null`).
- **Vendas por mês**: o mês corrente vem de `today()` (São Paulo) em Go; SQL com
  `generate_series(first, last, '1 month')` `LEFT JOIN` subconsulta pré-agregada de `vw_vendas_resumo`
  (exclui CANCELED), `ROUND(…, 2)` e `sales_count`.
- **Produtos**: `from`/`to` por limite vencem `data_inicio`/`data_fim`; `unit = MAX(produtos.unidade)`;
  restante da consulta inalterado.
- **`/dashboard/resumo`**: comportamento intacto; comentário `deprecated` no código e `deprecated: true` no swagger.

### Desvios / decisões

1. **`vw_quotes_resumo` recriada na 008** — não estava no AYD, mas é necessária para expor `quick_sale` nas respostas.
2. **Swagger sem endpoints de Quote**: os endpoints `/quotes*` nunca foram documentados em `docs/swagger.yaml`;
   foi adicionado só o schema `Quote` (com `quick_sale`), sem documentar as rotas.
3. **Precedência `from`/`to` × legados é por limite** (`from` vence `data_inicio`; `to` vence `data_fim`); um limite
   legado ainda vale se o novo do mesmo lado não vier. Mensagens de 400 citam o parâmetro inválido
   (`from`, `data_inicio`…); `from > to` só é checado entre os limites efetivos.
4. **`average_ticket` e `revenue`**: `revenue` já vem arredondado (2 casas) da consulta compartilhada; o ticket é
   `ROUND(revenue / count, 2)` sobre ele.
5. **`open_deliveries`** conta `vendas.deleted_at IS NULL AND delivery_status = 'TO_DELIVER'` (CANCELED nunca é TO_DELIVER).
6. **Changelog**: linha sem link de PR (ainda não aberto).
7. **Backfill**: aplicado como no AYD (heurístico); o teste cobre o positivo (+2 s) e os negativos (Sale 1 min depois,
   `data_venda` diferente, Quote OPEN, WON sem Sale).

## Passos de implementação

1. Migração 008 + teste do ciclo up/down e do backfill (banco descartável).
2. `quick_sale` no model/handlers de Quote.
3. Refatorar `finance.go` em consultas compartilhadas; `internal/finance/dashboard.go` + testes unitários.
4. `GET /dashboard/kpis`; correção de `/relatorios/vendas` e `/relatorios/produtos`.
5. Testes de integração; swagger, changelog, CLAUDE.md.

## Arquivos / módulos afetados

- `migrations/008_quote_quick_sale.{up,down}.sql`
- `internal/models/models.go`, `internal/handlers/{quotes,finance,dashboard}.go`, `internal/finance/dashboard.go`
- `docs/swagger.yaml`, `docs/changelog.md`, `CLAUDE.md`

## Testes (ver docs/conventions.md)

- Unitários: `internal/finance/dashboard_test.go` (ticket médio, taxa de conversão, nulo com created = 0, arredondamento).
- Integração (`TEST_DATABASE_URL`):
  - `internal/dbmigrate/migration008_integration_test.go` — backfill positivo/negativos, DEFAULT, view, down → up.
  - `internal/handlers/dashboard_integration_test.go` — KPIs (período explícito e default, Sale cancelada,
    coorte com quick sale/excluído/fora do período, `conversion_rate` null), igualdade com `/finance/summary`,
    400s, `quick_sale` somente leitura, `/relatorios/vendas` (N exato, zeros, `sales_count`, limites 1..60),
    `/relatorios/produtos` (`from`/`to`, legado, precedência, `unit`, 400s).

## Casos de borda & fora de escopo

- Fora: comparação com o período anterior, conversão por lead source/Partner, funil formal (RF-26), exportação (RF-24), fluxo de caixa (RF-22).
- Falso positivo do backfill (Quote convertido em < 10 s) apenas subestima a conversão histórica.

## Checklist de entrega

- [x] Migração 008 (up/down) testada
- [x] `quick_sale` marcado só pela venda rápida e somente leitura
- [x] `GET /dashboard/kpis` com `sales`/`balances` do mesmo código de `/finance/summary`
- [x] `/relatorios/vendas` com N meses exatos e `sales_count`
- [x] `/relatorios/produtos` com `from`/`to` e `unit`
- [x] `/dashboard/resumo` deprecated
- [x] Testes unitários e de integração cobrindo os critérios
- [x] Swagger, changelog, CLAUDE.md
