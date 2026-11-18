---
id: SPEC-008
type: spec
title: Contas e movimentações — Obligation, Installment, Settlement e Financial movement (api)
status: review
updated: 2026-11-18
parents: [AYD-007@context]
related: [PDR-002@context, PDR-001@context, ADR-001@context, AYD-002@context, AYD-004@context, AYD-006@context, AYD-008@context, AYD-009@context, SPEC-004, SPEC-006, SPEC-007, GLO]
---

# Spec: Contas e movimentações (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-007**. Enums, rotas, payloads, erros, formato do
> painel, migração e catálogo do connector vêm do AYD-007 (§1–§8, incl. §7a) e **não são
> redefinidos aqui**. Pontos que o AYD deixa em aberto estão em "Interpretações e pendências".

## Objetivo

Atender o **RF-38** (RN-17) no lado da API: o **Supplier** vira cadastro de fornecedores e
prestadores (`kind`); a API passa a manter **Obligations** (conta a pagar/receber por origem e
credor) derivadas de componentes, com **Installments** (plano) e **Settlements** (quitação)
ligando **Financial movements** (dinheiro executado). Substitui Receipt
(`/vendas/{id}/pagamentos`) e o pagamento único da Purchase (`PATCH /purchases/{id}/payment`);
Sale e Purchase passam a derivar recebido/pago/a pagar das contas; alertas e
`/finance/summary` passam a olhar parcelas e prazos de contas; o Assistant connector ganha 5
ferramentas e **remove** 3 (`record_receipt`, `confirm_receipt`, `record_supplier_payment` — o
catálogo nunca foi lançado, então sem depreciação), no catálogo 1.0.0 (36 ferramentas). Enquanto AYD-008/009 não existem, os componentes vêm das fontes existentes (§7a).

## Critérios de aceite

```gherkin
Cenário: Supplier kind (§1)
  Quando crio/edito Supplier com kind fora do enum → 400; sem kind → STONE
  E GET /fornecedores?kind=TRANSPORT filtra; req/res trazem kind
  E fornecedores existentes após a migração têm kind = STONE

Cenário: Contas mantidas pela API (§2, §7a)
  Dado uma Sale com Items (total 1000), frete_valor 150 e comissao_valor 80 com Partner
  Então existem 3 Obligations da Sale: RECEIVABLE do Customer (1000, componente SALE_ITEMS),
       PAYABLE sem credor (150, LEGACY_FREIGHT) e PAYABLE do Partner (80, LEGACY_COMMISSION)
  E uma Purchase com custo total 17995 gera 1 PAYABLE do Supplier (PURCHASE_ITEMS)
  E editar Items/frete/comissão/itens da Purchase recalcula amount sem ação manual
  E comissão sem Partner cai na conta sem credor junto com o frete (única por origem+credor)
  E conta sem componentes e sem quitações some; com quitações permanece (amount 0)
  E Sale CANCELED → amount = 0 em todas as contas, fora dos totais; quitações continuam visíveis
  E trocar o Partner da Sale move 80 para a conta do novo Partner; se a antiga ficar com
    settled > amount → warnings ["OVERSETTLED"]

Cenário: Leitura e prazo (§2)
  Quando GET /obligations com filtros (direction, status CSV, counterparty_*, origin_*, overdue,
         due_to, limit/offset)
  Então res = { items, totals{amount,settled,open}, total, offset, has_more }
  E GET /obligations/{id} traz components, installments, settlements e next_installment
  E PATCH /obligations/{id} {due_date, notes} altera só esses campos; id inexistente → 404
  E status = OPEN (settled=0) | PARTIAL | SETTLED (settled ≥ amount); open = GREATEST(amount − settled, 0)

Cenário: Parcelas e cobertura (§3)
  Quando PUT /obligations/{id}/installments com Σ ≤ amount → plano substituído; Σ > amount → 400
  E generate {count 2..24, first_due_date, interval_months=1, amount default open} →
    parcelas iguais, centavos na ÚLTIMA; count fora de 2..24 → 400
  E settled cobre as parcelas em ordem de due_date: COVERED / PARTIAL / OPEN
  E overdue = parcela não coberta vencida ou due_date < hoje com open > 0 ("hoje" em America/Sao_Paulo)

Cenário: Movimentação (§4)
  Quando POST /financial-movements amount = 0 → 400; sinal incoerente com a categoria → 400
  E PUT é correção (corpo **igual ao do POST**; corrected_at carimbado); PUT que deixe Σ quitações > |amount|
    ou inverta o sinal de uma movimentação que já quita contas → 409
  E DELETE anula a movimentação e desfaz suas quitações (soft-delete de ambos)
  E GET filtra from/to/category/direction/counterparty_*/unallocated_only e devolve
    totals{inflow,outflow,net}; unallocated = |amount| − Σ settlements

Cenário: Quitação (§5)
  Quando POST /settlements com movement{...} e 2 allocations → 201, 1 movimentação (±Σ)
         e 2 settlements; category default pela regra do AYD
  E allocations sem amount quitam o open da conta
  E allocations vazio, amount ≤ 0 ou direções diferentes → 400
  E passar do open → 409 "valor acima do devido"; Σ passar do valor da movimentação → 409
  E RECEIVABLE de Sale CANCELED → 409 "venda cancelada: recebimentos bloqueados"
  E credores diferentes → **201** com warnings ["MIXED_COUNTERPARTIES"] no corpo (permitido, mas raro)
  E POST /obligations/{id}/settle é atalho de 1 conta; DELETE /settlements/{id} não anula a movimentação
  E movement_id de movimentação já lançada é aceito (respeitando sinal/direção e saldo livre)

Cenário: Contratos legados removidos (§6)
  Então /vendas/{id}/pagamentos (todas as rotas) e PATCH /purchases/{id}/payment respondem 404
       (rota não existe mais) e saem do swagger
  E Sale: total_recebido, receivable, payment_status vêm da conta RECEIVABLE (mesmas regras do SPEC-004)
  E Purchase: payable/paid = Σ das contas da compra; payment_status derivado; sem paid_amount/paid_at gravados
  E vendas.frete_pago/comissao_paga e purchases.payment_status/paid_amount/paid_at deixam de existir

Cenário: Alertas (§6)
  Quando leio GET /dashboard/notificacoes
  Então RECEIVABLE → pagamento_vencendo/pagamento_vencido e PAYABLE → conta_pagar_vencendo/conta_pagar_vencida,
       por parcela não coberta ou prazo de conta, janela de 5 dias, lida preservada (reabre só se tipo/data_ref mudam)
  E cada alerta carrega obligation_id (e installment_id quando vem de parcela; null = prazo da conta) — substituem o pagamento_id
  E o alerta some ao cobrir a parcela/quitar a conta, reagendar além da janela ou cancelar a Sale

Cenário: Painel (§6)
  Quando GET /finance/summary?from&to
  Então sales.received = Σ quitações RECEIVABLE das Sales do período (não canceladas)
  E cash{inflow,outflow,net,by_category,received_by_method} vem das movimentações do período
  E expenses = movimentações SEM quitação de categoria de despesa; result = { sales_profit, expenses, net_result }
  E balances{receivable,payable,payable_by_counterparty_kind[]} = Σ open das contas

Cenário: Migração 010 (§7)
  Dado Receipts pendentes e recebidos, purchases.paid_amount > 0, frete_pago/comissao_paga
  Quando aplico a 010
  Então Receipts viram parcelas da RECEIVABLE (due_date = COALESCE(data_agendada, data_pagamento, created_at))
       e os recebidos geram SALE_RECEIPT + quitação; Receipts soft-deletados não migram
  E paid_amount > 0 vira PURCHASE_PAYMENT + quitação da conta do Supplier da compra
  E frete_pago/comissao_paga viram saída (FREIGHT/COMMISSION) com occurred_on = **data da Sale** + quitação das contas legadas
  E antes e depois: total_recebido, receivable, payment_status, a pagar por Purchase e Profit coincidem
  E up/down funcionam (down recria estruturas legadas; ver pendências)

Cenário: Assistant connector (§8)
  Então list_obligations, list_financial_movements (R) e settle_obligations, record_expense (W, dedupe),
       set_installments (W, idempotente) existem
  E record_receipt, confirm_receipt e record_supplier_payment **não existem mais** no catálogo (removidas, sem depreciação)
  E catalog_version = 1.0.0 (36 ferramentas: 20 R + 16 W) e o golden é regenerado
```

## Contratos consumidos/expostos

Expõe, sem redefinir, os do **AYD-007**: enums `SupplierKind`, `ObligationDirection`,
`ObligationStatus`, `InstallmentStatus`, `MovementCategory`; `Obligation`, `Installment`,
`FinancialMovement`; rotas

```
GET/PATCH /obligations[/{id}] · PUT /obligations/{id}/installments · POST .../installments/generate
POST /obligations/{id}/settle · POST /settlements · DELETE /settlements/{id}
GET/POST /financial-movements · PUT/DELETE /financial-movements/{id}
/fornecedores (+kind, ?kind=) · /finance/summary (§6) · /dashboard/notificacoes (§6)
```

Prefixo `/api/v1`, Bearer Firebase, `DECIMAL(12,2)`/`float64`, erros `{"error": "..."}`. Consome
as fontes de componentes de §7a (e, depois, `purchase_charges`/`sale_costs` dos AYD-008/009,
fora desta SPEC). Reusa o enum `payment_method` (SPEC-004). OpenAPI: `docs/swagger.yaml`.

## Abordagem (como)

**Camadas.** Tabelas guardam só o que o dono informa (conta como "casca" por origem+credor,
prazo, parcelas, movimentações, quitações). Valor, quitado, em aberto, status, cobertura e
alocação são **views** (RNF-05). A "manutenção automática" das contas é uma **sincronização
idempotente por origem** (função SQL `sp_sync_obligations`) disparada por **triggers** em
`vendas`, `itens_venda`, `purchases` e `purchase_items` — na mesma transação de qualquer escrita
(API, conector, SQL) que altere componentes (Sale/Items/frete/comissão/partner/cancelamento;
Purchase/Items/supplier/exclusão). Garante a linha `obligations` de cada (origem, direção, credor)
presente na view de componentes e remove (soft-delete) as órfãs sem quitações. Um **job de
reconciliação** não é necessário e nenhum caminho de escrita precisa "lembrar" de sincronizar.

### Migração 010 (`migrations/010_obligations_and_movements.{up,down}.sql`)

1. Enums: `supplier_kind`, `obligation_direction`, `movement_category`. (Status de conta/parcela
   são calculados nas views; não há enum no banco além dos de uso em JSON — tipos Go.)
2. `fornecedores.kind supplier_kind NOT NULL DEFAULT 'STONE'` + índice.
3. Tabelas (todas com `id uuid default gen_random_uuid()`, `created_at`, `deleted_at`):
   - `obligations` (`direction`, `sale_id`|`purchase_id` com CHECK "exatamente um",
     `supplier_id`|`partner_id`|`customer_id` com CHECK "no máximo um", `due_date`, `notes`);
     único parcial por (origem, credor) usando `COALESCE(...)` em índice de expressão
     (credor nulo conta como "a definir") onde `deleted_at IS NULL`; CHECK de coerência
     direction × origem/credor (RECEIVABLE ⇒ sale + customer; PAYABLE ⇒ supplier|partner|nulo).
   - `installments` (`obligation_id`, `due_date`, `amount > 0`, `payment_method`, `notes`).
   - `financial_movements` (`occurred_on`, `amount <> 0`, `category`, `payment_method`,
     `counterparty_name`, `description`, `corrected_at`); CHECK sinal × categoria
     (entradas > 0, demais < 0).
   - `settlements` (`obligation_id`, `movement_id`, `amount > 0`); índices por ambas as FKs.
4. **Views** (`CREATE VIEW`; recriar `vw_vendas_resumo` e `vw_purchases_resumo` depois):
   - `vw_obligation_components` (interna): UNION ALL das fontes §7a —
     `SALE_ITEMS` (total da Sale, via agregação pré-feita de itens), `LEGACY_FREIGHT`,
     `LEGACY_COMMISSION`, `PURCHASE_ITEMS` (`vw_purchases_resumo.total_cost`), cada uma com
     (origem, credor, `type`, `id`, `label`, `amount`). Sale `CANCELED` ⇒ `amount = 0`.
     Quando AYD-008/009 chegarem, só esta view muda.
   - `vw_obligations`: `obligations` + `Σ componentes` (`amount`) + `Σ settlements` não
     excluídos (`settled`, pré-agregado, **nunca** juntar componentes e quitações no mesmo
     GROUP BY) + `open`, `status`, `overdue`, `next_installment`.
   - `vw_installment_coverage`: window `SUM(amount) OVER (PARTITION BY obligation_id ORDER BY
     due_date, created_at, id)` contra `settled` ⇒ `covered`, `status`, `overdue`.
   - `vw_movement_allocation`: `allocated = Σ settlements`, `unallocated = |amount| − allocated`.
5. **Migração de dados** (mesma transação, ordem importa; usa `INSERT … SELECT`):
   a. `sp_sync_obligations` (a mesma função dos triggers — Interpretações #3): cria `obligations`
      de todas as Sales/Purchases existentes a partir de `vw_obligation_components`.
   b. Receipts não excluídos → `installments` da RECEIVABLE (`due_date` conforme §7, valor =
      `valor`, `payment_method` copiado).
   c. Receipts `recebido` → `financial_movements` (`SALE_RECEIPT`, `occurred_on =
      data_pagamento`, `amount = +valor`, `payment_method`) + `settlements`.
   d. `purchases.paid_amount > 0` → movimentação `PURCHASE_PAYMENT` (`occurred_on = paid_at::date`
      com fallback `created_at`, `−paid_amount`) + settlement na conta do Supplier da compra.
   e. `vendas.frete_pago`/`comissao_paga` (valor/flag) → movimentação `FREIGHT`/`COMMISSION`
      (negativa, `occurred_on` = **data da Sale** — a data real do pagamento não era guardada;
      decidido no AYD-007 §7) + settlement nas contas legadas.
   f. **Verificação interna** (`DO $$ … RAISE EXCEPTION`): Σ recebido por Sale antes = Σ
      settlements RECEIVABLE depois; Σ pago por Purchase idem; falha aborta a migração.
6. Remoções: DROP de `pagamentos_cliente` (após recriar as views sem ela), de
   `purchases.payment_status/paid_amount/paid_at` e `vendas.frete_pago/comissao_paga`;
   `gerar_notificacoes_pagamentos()` e tabela/ colunas de `notificacoes` ajustadas
   (`pagamento_id` → `parcela_id`/`obligation_id` + tipos novos; ver Alertas).
7. `down`: recria colunas/tabela legadas e repopula **best-effort** a partir de parcelas e
   quitações; lossy (movimentações avulsas e parcelas de contas de Purchase não voltam).

### Regras e handlers

- **`internal/obligations`** (novo, com testes unitários): `rules.go` — regras puras:
  `GenerateInstallments` (centavos na última, mês com clamp de fim de mês), `ValidatePlan`
  (Σ ≤ amount, valores > 0), `CoverPlan` (espelha a view de cobertura), `Allocate` (valida `open`,
  direção única, Sale cancelada → 409), `DefaultCategory` (§5), `CheckSign`; `store.go`,
  `movements.go` e `summary.go` — acesso a dados transacional (listagem/detalhe, PATCH, parcelas,
  `Settle` com `FOR UPDATE`, CRUD de movimentações, blocos do painel).
- **`internal/handlers/obligations.go`**: `GET` lista/detalhe (listagem lê `vw_obligations`;
  detalhe compõe componentes + parcelas via `vw_installment_coverage` + quitações; `warnings`
  `OVERSETTLED` quando quitado > valor), `PATCH`, `PUT`/`generate` de parcelas, `settle`.
- **`internal/handlers/settlements.go`**: `POST /settlements` e `DELETE`; transação única
  com `SELECT … FOR UPDATE` das contas e da movimentação (evita estourar `open` em
  concorrência) → 409 nos limites do AYD.
- **`internal/handlers/financial_movements.go`**: CRUD conforme §4; o PUT (correção) tem o
  **mesmo corpo do POST** e confere o limite das quitações; DELETE faz soft-delete da
  movimentação e de seus settlements.
- **`fornecedores.go`**: `kind` em req/res, validação e filtro `?kind=`.
- **`vendas.go` / `purchases.go`**: removem Receipts/pagamento (Sale: `frete_pago`, `comissao_paga`
  e `pagamentos` saem — `frete_pago`/`comissao_paga` no PUT respondem 400 com orientação;
  Purchase: `paid_amount`, `paid_at`, `to_pay` saem, entram `paid` e `payable`);
  `total_recebido`, `receivable`, `payment_status` (Sale) e `paid`, `payable`, `payment_status`
  (Purchase) vêm de `vw_obligations` (via `vw_vendas_resumo`/`vw_purchases_resumo`, recriadas).
  A Sale ganha `commission_payable` e `warnings`. A regra de cancelamento (RN-15, SPEC-002) não
  muda (só Purchase pedida/recebida bloqueia). `freight_payable`/`commission_payable` vêm das
  contas PAYABLE da Sale (Interpretações #8).
- **`pagamentos.go`**: **removido**; `finance` (`internal/finance`) mantém `payment_method`
  e `ResolvePeriod`; helpers de Receipt saem.
- **`dashboard.go` + função SQL de alertas**: reescrita de `gerar_notificacoes_pagamentos()`
  sobre parcelas não cobertas (`vw_installment_coverage`) e `due_date` de contas sem parcela
  aberta com `open > 0` (view `vw_payment_alerts`); tipos `pagamento_vencendo|pagamento_vencido`
  (RECEIVABLE) e `conta_pagar_vencendo|conta_pagar_vencida` (PAYABLE); mesma política de `lida` e
  "hoje" em America/Sao_Paulo; `notificacoes.pagamento_id` vira `obligation_id` +
  `installment_id` e o payload do alerta os carrega (o `lida` migra, pois o id da parcela = id do
  Receipt); `GET /dashboard/notificacoes` segue gerando antes de ler e respondendo 500 se falhar.
- **`finance.go`** (`/finance/summary`): uma consulta por agregado sobre `financial_movements`/
  `settlements`/`vw_obligations`/`vw_vendas_resumo` (blocos em `obligations/summary.go`);
  `sales_profit` do período reusa a regra existente de Profit (RN-04, canceladas fora);
  `net_result = sales_profit − expenses.total`. Despesas = movimentações de categoria de despesa
  (VEHICLE, TAXES, FIXED_COSTS, MARKETING, BANK_FEES, OTHER_EXPENSE e `LABOR` avulso) **sem**
  settlement (`NOT EXISTS`). `/dashboard/kpis` mantém o formato dos `balances` do AYD-005
  (`payable_to_suppliers`, `freight_payable`, `commission_payable`), re-derivado das contas.
- **Rotas** em `cmd/server/main.go`; models GORM novos em `internal/models`.

### Assistant connector (`internal/mcp/tools`, `catalog_version` 1.0.0)

- **Novas** (módulo `financial`): `list_obligations` (R; padrão: em aberto), `list_financial_movements`
  (R), `settle_obligations` (W, deduplicação 120 s / `allow_duplicate`, SPEC-007; nome com o verbo
  `settle_` acrescentado aos reservados), `record_expense` (W, dedupe; categoria de despesa ou
  `OTHER_INCOME`, valor sempre positivo), `set_installments` (W, idempotente: lista completa OU
  `count` + `first_due_date`, e/ou `due_date`; `null` limpa). O Gateway em processo despacha as rotas
  novas — mesmas regras da API.
- **Removidas** (decisão do dono: o catálogo nunca foi lançado, sem depreciação): `record_receipt`,
  `confirm_receipt` e `record_supplier_payment`, com o código de tradução e os testes. Receber e pagar
  passam por `settle_obligations`; agendar, por `set_installments`.
- Leituras re-derivadas: `search_suppliers` ganha `kind` (saída e filtro); `get_sale.receipts` vem
  da conta a receber (quitações = `RECEIVED`, parcelas não cobertas pelo que falta = `PENDING`);
  `list_purchases`/`get_purchase` mapeiam `paid`/`payable` para `paid_amount`/`to_pay`;
  `freight_paid`/`commission_paid` da Sale são derivados do que resta a pagar; `update_sale`
  `freight_paid`/`commission_paid` viram quitações (só quita a diferença; reduzir/desfazer é erro de entrada);
  `get_finance_summary` e `list_payment_alerts` seguem o novo formato das rotas.
  Catálogo: 36 ferramentas (20 de leitura + 16 de escrita).
- `CatalogVersion` permanece **1.0.0** (catálogo não lançado); `go test ./internal/mcp -run TestCatalogGolden -update`
  no mesmo PR; instruções do servidor (`serverInfo` instructions) trocam Receipt por
  Obligation/Settlement. Atualização do AYD-006 é do contexto.

### Interpretações e pendências (não alteram o contrato)

1. **Component `type` no detalhe da conta** usa os valores de §7a (`SALE_ITEMS`,
   `LEGACY_FREIGHT`, `LEGACY_COMMISSION`, `PURCHASE_ITEMS`); AYD-008/009 acrescentarão os seus.
2. **Ferramentas de recebimento/pagamento antigas** — removidas do catálogo 1.0.0 (decisão do dono;
   o AYD-007 §8 previa a depreciação — atualizar no contexto). Diferenças de comportamento do novo
   modelo: receber **acima do devido** é 409; uma parcela criada depois de a venda já ter sido paga
   nasce **coberta** (o quitado cobre as parcelas mais antigas).
3. **Sync em triggers SQL, não em Go:** o AYD não fixa onde; os triggers (mesma função da
   migração) garantem a manutenção em qualquer caminho de escrita, sem hooks espalhados.
4. **Venda rápida/Quote→Sale**: a conta RECEIVABLE nasce pelo trigger ao inserir os Items.
5. **Migração de `frete_pago`/`comissao_paga`** usa a data da Sale (*resolvido no AYD-007 §7*).
6. **Down lossy** da 010 (acima).
7. Conta PAYABLE `LEGACY_COMMISSION` sem Partner e `LEGACY_FREIGHT` caem na mesma conta
   "a definir" (valor somado), conforme "única por (origem, credor)". O AYD mostra
   `components[]` separados dentro dela — compatível.
8. **`freight_payable`/`commission_payable` da Sale com a conta "a definir" compartilhada:** o que foi
   quitado com categoria `COMMISSION` abate a comissão; o restante abate o frete primeiro e, se sobrar, a
   comissão. A comissão com Partner é o aberto da conta do Partner. (A Sale mantém esses campos; o AYD
   só os cita para o painel.)
9. **Status com `amount` 0:** `SETTLED` (settled ≥ amount) — a conta de uma Sale cancelada sem quitação
   fica fora das listas de "em aberto"; com quitação, `settled` > 0 e `warnings: [OVERSETTLED]`.
10. **Formato do painel que o AYD deixa livre:** `cash.outflow`, `totals.outflow` e `expenses` são
    positivos (`net = inflow − outflow`); `cash.by_category` traz o valor com sinal;
    `received_by_method` = entradas `SALE_RECEIPT` do período; `payable_by_counterparty_kind[].kind` =
    `kind` do Supplier, `PARTNER` (comissões) ou `UNASSIGNED` (conta sem credor).
11. **Listagens:** `GET /obligations` ordena por vencimento efetivo (próxima parcela não coberta, senão
    `due_date`) e `due_to` filtra esse mesmo vencimento; `limit` padrão 50, máximo 200. `status=SETTLED`
    é aceito no filtro (o conector filtra `OPEN,PARTIAL` por padrão).
12. **PUT de movimentação com quitações:** inverter o sinal só é 409 quando a movimentação já quita contas
    (sem quitações, é uma correção comum, desde que categoria e sinal combinem).
13. **`PUT /fornecedores/{id}` sem `kind` mantém o atual** (POST sem `kind` → `STONE`).
14. **Alertas de prazo:** a conta com parcelas só alerta pelas parcelas não cobertas; o `due_date` da
    conta alerta quando não há parcela em aberto e `open > 0`. Para alertas a pagar, `cliente_nome`
    carrega o nome do credor (ou "A definir").
15. **Verificação interna da migração** (aborta se divergir) é exercitada pelo teste de paridade; um
    teste que a force a falhar não é viável (a carga é determinística). Compras `PAID` com valor pago
    menor que o custo ganham saldo em aberto (aviso `NOTICE` na migração; consulta em `docs/ops/database.md`).

## Passos de implementação

1. Migração 010 (tabelas, enums, views, dados, remoções) + teste up/down e de paridade de
   totais (banco descartável com dados legados semeados antes de aplicar a 010).
2. Models + `internal/obligations` (regras puras) e testes unitários.
3. Triggers de sincronização (`sp_sync_obligations`) em Sale/Items/Purchase/linhas.
4. Handlers `obligations`, `settlements`, `financial_movements`; `fornecedores.kind`; rotas.
5. Remover `pagamentos.go` e `PATCH /purchases/{id}/payment`; re-derivar resumos de
   Sale/Purchase; ajustar `fulfillment` (RN-15) e testes existentes que usavam Receipts.
6. Alertas (função SQL + `dashboard.go`) e `/finance/summary`.
7. MCP: ferramentas novas, remoção das 3 antigas, `kind`, `CatalogVersion` 1.0.0, golden e testes.
8. Swagger, `docs/ops/database.md` (baseline/down lossy), `CLAUDE.md` (fase 11, estrutura,
   catálogo 1.0.0), changelog.

## Arquivos / módulos afetados

- `migrations/010_obligations_and_movements.{up,down}.sql`
- `internal/models/models.go`, `internal/obligations/` (novo)
- `internal/handlers/{obligations,settlements,financial_movements,fornecedores,vendas,purchases,finance,dashboard}.go`;
  remove `pagamentos.go`; `internal/fulfillment/`
- `cmd/server/main.go`
- `internal/mcp/{registry.go,testdata/catalog.golden.json}`, `internal/mcp/gateway/`,
  `internal/mcp/tools/{financial,procurement,sales,crm,catalog}.go`
- `docs/swagger.yaml`, `docs/ops/database.md`, `docs/changelog.md`, `CLAUDE.md`

## Testes (ver docs/conventions.md)

- **Unitários:** `internal/obligations` — cobertura em ordem de vencimento (parcial/coberta),
  `GenerateInstallments` (centavos na última, fim de mês), Σ ≤ amount, alocação (open, direção,
  Sale cancelada, Σ ≤ |mov|), categoria default, sinal × categoria.
- **Integração (`TEST_DATABASE_URL`)** — um teste por cenário (implementados em `internal/dbmigrate/migration010_integration_test.go`,
  `internal/handlers/{obligations,installments,financial_movements,settlements,finance}_integration_test.go` e
  `internal/mcp/tools/*_integration_test.go`):
  - `internal/dbmigrate/migration010_integration_test.go`: semeia legado, aplica 010, confere
    paridade (recebido/receivable/payment_status/a pagar/Profit), mapeamento de datas,
    soft-deletados fora, abort da verificação, down → up.
  - `internal/handlers/obligations_integration_test.go`: contas por origem/credor (§7a), recalcula
    ao editar, cancelamento (`amount 0`, quitações visíveis), troca de Partner com `OVERSETTLED`,
    listagem/filtros/totais/paginação, PATCH, 404.
  - `.../installments_integration_test.go`: PUT/generate/erros e cobertura/overdue.
  - `.../financial_movements_integration_test.go`: 400/409, correção, DELETE desfaz quitações,
    filtros e totais, `unallocated`.
  - `.../settlements_integration_test.go`: um Pix → 2 contas, defaults, todos os 400/409,
    `MIXED_COUNTERPARTIES`, concorrência (2 quitações simultâneas não passam do `open`).
  - `.../finance_integration_test.go` (ajustado): novo `/finance/summary` (received, cash,
    expenses, result, balances) e alertas RECEIVABLE/PAYABLE com `lida` preservada;
    rotas legadas → 404.
  - `fulfillment_integration_test.go` e `fornecedores` (kind/filtro) ajustados.
  - `internal/mcp/tools/*_integration_test.go`: 5 ferramentas novas (dedupe, scopes, erros),
    ausência das 3 antigas no catálogo; `TestCatalogGolden` (1.0.0).
- Mocks só na fronteira; `go vet ./... && TEST_DATABASE_URL=... go test ./...`.

## Casos de borda & fora de escopo

- Borda: Pix maior que o `open` (409); correção de movimentação com quitações; parcelas
  vencidas com `settled` zero; conta com `amount` 0 e quitações (cancelamento); Sale cancelada
  não gera/recebe alertas; recriar Sale sem Partner após quitar a comissão; fuso São Paulo.
- Fora: componentes editáveis e credor por componente (AYD-008/009), serviços (AYD-010),
  conta bancária/saldo/transferência/retirada/empréstimo (RF-22), contas a pagar sem
  compra/venda, estorno ao cliente, frontend (SPEC-008@web), `account_id`.

## Checklist de entrega

- [x] Migração 010 (up/down) com verificação de paridade
- [x] `fornecedores.kind` + filtro
- [x] `obligations`/`installments`/`settlements`/`financial_movements` + views (`vw_obligations`, `vw_installment_coverage`, `vw_movement_allocation`)
- [x] Sync automático das contas (§7a) por triggers
- [x] Rotas de obligations, parcelas, settlements e financial-movements com erros do AYD
- [x] Rotas `/vendas/{id}/pagamentos` e `PATCH /purchases/{id}/payment` removidas
- [x] Sale/Purchase re-derivados das contas; RN-15 preservada
- [x] Alertas RECEIVABLE/PAYABLE (com `obligation_id`/`installment_id`) e `/finance/summary` (cash, expenses, result, balances)
- [x] MCP: 5 ferramentas novas, 3 antigas removidas, `kind`, catálogo 1.0.0 (36) + golden
- [x] Testes unitários e de integração por critério de aceite
- [x] Swagger, ops, CLAUDE.md, changelog
