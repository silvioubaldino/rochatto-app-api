---
id: SPEC-009
type: spec
title: Composição da compra — custos adicionais com credor, rateio e custo de entrada (api)
status: done
updated: 2026-11-18
parents: [AYD-008@context]
related: [PDR-002@context, PDR-001@context, AYD-002@context, AYD-006@context, AYD-007@context, AYD-009@context, SPEC-002, SPEC-006, SPEC-007, SPEC-008, GLO]
---

# Spec: Composição da compra (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-008**. Enums, rotas, payloads, erros, regras de rateio e
> catálogo do connector vêm do AYD-008 (§1–§4) e **não são redefinidos aqui**. Pontos que o AYD
> deixa em aberto estão em "Interpretações e pendências".

## Objetivo

Atender o **RF-39** no lado da API: a **Purchase** passa a ter **custos adicionais**
(*Purchase charges*: frete de compra, descarga, imposto, outros), cada um com **credor** editável.
Os custos entram nas **contas** da compra (AYD-007, uma por credor), são **rateados** entre as
linhas (`BY_QUANTITY` | `BY_VALUE`) e dão o **custo de entrada** (*Landed cost*) de cada linha, que
alimenta o Stock (`last_inflow_cost`, valor em estoque), o custo do Item **FROM_STOCK** (snapshot)
e o **Profit** dos Items sob encomenda (RN-04). O Assistant connector ganha `add_purchase_charge` e
`get_purchase`/`get_stock_balance` passam a trazer a composição, o custo de entrada e o valor em
estoque (catálogo segue `1.0.0`, agora com 37 ferramentas).

## Critérios de aceite

```gherkin
Cenário: Custos adicionais (§1)
  Dado uma Purchase de reposição (100 m² × 27 e 200 m² × 90)
  Quando POST /purchases/{id}/charges {kind: FREIGHT, amount: 600} (sem payee)
  Então 201; payee_supplier_id = supplier da compra; obligation_id = conta do Supplier; warnings []
  E {kind: UNLOADING, amount: 150, payee_supplier_id: <outro>} → conta própria do outro credor
  E {kind: TAX, amount: 30, payee_supplier_id: null} → conta sem credor ("a definir")
  E qualquer status da compra (inclusive RECEIVED) aceita custo adicional
  E GET /purchases/{id}/charges lista os não excluídos; PUT substitui; DELETE → 204 (soft-delete)
  E amount ≤ 0 / kind inválido → 400; compra ou Supplier inexistente → 404; compra excluída → 409
  E PUT/POST que deixa conta com quitado > devido → 200/201 com warnings ["OVERSETTLED"]

Cenário: Composição e contas (§2)
  Então GET /purchases/{id} traz allocation_method, composition {goods_total, charges,
       charges_total, total}, obligations (uma por credor), paid/payable/open (Σ das contas),
       lines[] (line_type, base_cost, allocated_charges, landed_total, landed_unit_cost) e warnings
  E a conta do Supplier tem componentes PURCHASE_ITEMS (Σ base_cost) e PURCHASE_CHARGE
    (id = chargeId; label = rótulo do kind + descrição); pedra + frete do mesmo credor = 1 conta
  E editar/excluir um custo, ou trocar o credor, recalcula as contas sem ação manual (triggers);
    conta sem componentes e sem quitações some
  E PUT /purchases/{id} {supplier_id} (OPEN) move as linhas e os custos com o credor padrão
  E GET /purchases?open_only=true (open > 0) e ?overdue=true (alguma conta vencida)

Cenário: Rateio (§3)
  Dado BY_QUANTITY (padrão): allocated = charges_total × quantity / Σ quantity
  E BY_VALUE: allocated = charges_total × base_cost / Σ base_cost (Σ = 0 cai para BY_QUANTITY)
  E bonificação (custo 0) recebe rateio por quantidade
  E precisão total na view; 2 casas só na resposta
  E BY_QUANTITY com unidades diferentes (produtos.unidade normalizada; linha sem Product assume as
    demais) → 422 ao lançar custo, ao pedir BY_QUANTITY (PUT) ou ao acrescentar linha com custos
  E allocation_method em POST/PUT /purchases (inválido → 400)

Cenário: Para onde vai o custo de entrada (§4)
  Então last_inflow_cost (vw_stock_balance) = landed_unit_cost da última reposição RECEIVED
  E custo lançado depois de recebida muda o custo de entrada (e o extrato INFLOW)
  E Item FROM_STOCK recebe custo_unit = last_inflow_cost (snapshot; custo novo não o altera)
  E Item MADE_TO_ORDER: vw_vendas_resumo.total_custo/lucro somam o rateio, sem snapshot; Sale de
    outra compra não muda (rateio pré-agregado por Sale, sem fan-out)
  E GET /stock/balance traz stock_value = GREATEST(balance, 0) × last_inflow_cost por Product e
    o total no cabeçalho X-Total-Stock-Value

Cenário: Assistant connector (AYD-006)
  Então add_purchase_charge (W · dedupe) lança o custo e devolve {charge, purchase, warnings}
  E get_purchase traz allocation_method, composition, obligations, payable/paid/open, lines e
    não traz payment_status/paid_amount/paid_at/to_pay
  E get_stock_balance traz stock_value e (lista) total_stock_value
  E catálogo 1.0.0 com 37 ferramentas (20 R + 17 W); golden regenerado
```

## Contratos consumidos/expostos

Expõe, sem redefinir, os do **AYD-008**: enums `PurchaseChargeKind`, `AllocationMethod`;
`PurchaseCharge`, `PurchaseDetail` (+ `allocation_method`, `composition`, `obligations`,
`payable/paid/open`, `lines`, `warnings`); rotas

```
GET/POST   /purchases/{id}/charges
PUT/DELETE /purchases/{id}/charges/{chargeId}
POST/PUT   /purchases  (+ allocation_method; PUT + supplier_id)     GET /purchases ?open_only&overdue
GET /stock/balance[/{productId}]  (+ stock_value)
```

Consome as contas do **AYD-007** (`obligations`, `vw_obligation_components`, `sp_sync_obligations`)
e o `Obligation` resumo. Prefixo `/api/v1`, Bearer Firebase, `DECIMAL(12,2)`/`float64`, erros
`{"error": "..."}`. OpenAPI: `docs/swagger.yaml`.

## Abordagem (como)

**Camadas.** `purchase_charges` guarda só o que o dono informa (tipo, valor, descrição, credor).
Rateio, custo de entrada, valor em estoque e totais são **views** (RNF-05). As contas continuam
sendo mantidas por **triggers** (`sp_sync_obligations`): a 011 acrescenta o trigger de
`purchase_charges` (INSERT/UPDATE/DELETE) e a troca de Supplier em `purchases`.

### Migração 011 (`migrations/011_purchase_composition.{up,down}.sql`)

1. Enums `purchase_charge_kind` e `allocation_method`; `purchases.allocation_method` (default
   `BY_QUANTITY`); tabela `purchase_charges` (`amount > 0`, `payee_supplier_id` nulo = "a definir",
   `deleted_at`).
2. `vw_purchase_line_costs`: linhas de reposição (`STOCK_ITEM`) e Items de Sale sob encomenda
   (`SALE_ITEM`) com `base_cost`, `allocated_charges`, `landed_total`, `landed_unit_cost`, `sale_id`
   e `units_mixed` (unidade normalizada por `lower(btrim(unidade))`; linha sem Product não conta).
3. `vw_obligation_components` (CREATE OR REPLACE): + ramo `PURCHASE_CHARGE`; `PURCHASE_ITEMS`
   segue Σ `base_cost` (`vw_purchase_costs`). Credor nulo → conta sem credor.
4. Triggers: `trg_sync_obligations_purchase_charge` (novo) e `trg_sync_obligations_purchase`
   (troca de Supplier move os custos com o credor padrão antes de sincronizar).
5. Views recriadas: `vw_purchases_resumo` (+ `charges_total`; `total_cost` segue = pedras),
   `vw_vendas_resumo` (`total_custo`/`lucro` + rateio dos `SALE_ITEM`, pré-agregado por Sale),
   `vw_stock_balance` (custo da reposição = `landed_unit_cost`; + `stock_value`).
6. Sem backfill: compras existentes ficam sem custos e a paridade é exata (testada). Down: recria
   as views da 010/006, remove `purchase_charges` e `allocation_method` (**lossy**: custos
   adicionais e credores se perdem; ver `docs/ops/database.md`).

### Regras e handlers

- `internal/purchasing` (novo, puro): tipos/métodos, `ChargeLabel`, `NormalizeUnit`,
  `UnitsMixed`, `Allocate` e `LandedUnitCost` — espelho testado da view (a view é a fonte em
  runtime; um teste de integração compara os dois).
- `handlers/purchase_charges.go`: rotas dos custos; lock da Purchase (`FOR UPDATE`, `Unscoped`:
  inexistente 404, excluída 409); validação; `warnings` por `purchaseWarnings`.
- `handlers/purchases.go`: `allocation_method` (POST/PUT), `PUT supplier_id`, detalhe com
  `composition`/`obligations`/`lines`/`warnings`, filtros `open_only`/`overdue`,
  `ensureUnitsCompatible` (422) após custo/linha/anexo/troca de método.
- `handlers/stock.go`: `stock_value`, `X-Total-Stock-Value`, extrato INFLOW com custo de entrada.
- FROM_STOCK: `lastInflowCost` (vendas.go) já lê `vw_stock_balance.last_inflow_cost`, que agora é
  o custo de entrada — nenhuma mudança de código; o snapshot é o próprio `custo_unit` gravado.
- Connector (`internal/mcp/tools`): `add_purchase_charge` (dedupe), `PurchaseDetail` (flat, sem
  `payment_status/paid_amount/paid_at/to_pay`), `StockBalanceRow.stock_value`,
  `total_stock_value`; golden regenerado.

### Interpretações e pendências (não alteram o contrato)

1. **`payable` vs `open` em `PurchaseDetail`:** o AYD-007 define `payable` = Σ em aberto; o
   AYD-008 lista `payable, paid, open`. Mantido `payable` = Σ em aberto (sem quebrar consumidores)
   e `open` = o mesmo valor; o total devido está em `composition.total`.
2. **`payee_supplier_id` ausente × `null`:** ausente = padrão (Supplier da compra), `null`
   explícito = "a definir". Vale também no PUT (substituição completa). A ferramenta do connector
   só expressa "ausente" (credor padrão) ou um Supplier.
3. **DELETE de custo → 204:** sem corpo para carregar `warnings`; um eventual `OVERSETTLED` fica em
   `GET /purchases/{id}.warnings`. POST/PUT devolvem os `warnings` no corpo.
4. **`GET /stock/balance` segue lista (AYD-002):** o `total_stock_value` vai no cabeçalho
   `X-Total-Stock-Value` (e é campo da saída da ferramenta); o web também pode somar `stock_value`.
5. **`PUT /purchases/{id}` com `supplier_id`:** o AYD cita a troca de Supplier sem dizer a rota;
   aceito só com a compra OPEN e sem Items de venda anexados (409); Supplier inexistente 404.
6. **422 só com custos:** BY_QUANTITY misto só bloqueia quando há custo adicional (ou ao pedir
   BY_QUANTITY explicitamente), para não impedir compras mistas sem o que ratear.
7. **Totais legados:** `total_cost` do resumo segue Σ pedras (`composition.goods_total`);
   `/dashboard` por Product (`custo_medio`/margem por Item) **não** inclui o rateio — só
   `vw_vendas_resumo` (Profit por Sale, painel e KPIs) o inclui.
8. **Rateio com Σ quantidade = 0:** rateio 0 (linhas com quantidade 0 não existem na prática).
9. **`get_purchase` sem `to_pay`:** além de `payment_status/paid_amount/paid_at` (AYD), saiu
   `to_pay` (substituído por `payable`/`open`); `list_purchases` não muda.

## Passos de implementação

1. Migração 011 + teste up/down e de paridade (banco descartável).
2. `internal/purchasing` (regras puras) + testes unitários.
3. Models, handlers de custos, `purchases`/`stock` e filtros.
4. Connector: `add_purchase_charge`, `get_purchase`, `get_stock_balance`; contagem 37; golden.
5. Testes de integração; swagger, `docs/ops/database.md`, `CLAUDE.md` (fase 12), changelog.

## Arquivos / módulos afetados

- `migrations/011_purchase_composition.{up,down}.sql`
- `internal/models/models.go`, `internal/purchasing/` (novo)
- `internal/handlers/{purchase_charges,purchases,stock}.go`
- `internal/mcp/{registry.go,testdata/catalog.golden.json}`, `internal/mcp/toolkit/types.go`,
  `internal/mcp/tools/{procurement,stock,types}.go`
- `docs/swagger.yaml`, `docs/ops/database.md`, `docs/changelog.md`, `CLAUDE.md`

## Testes (ver docs/conventions.md)

- **Unitários:** `internal/purchasing` — BY_QUANTITY (exemplo do AYD), BY_VALUE, Σ base 0,
  bonificação, unidades mistas, método inválido, rótulo.
- **Integração (`TEST_DATABASE_URL`):**
  - `internal/dbmigrate/migration011_integration_test.go`: paridade sem custos, contas por credor,
    rateio, custo de entrada, Profit, troca de Supplier, `units_mixed`, down → up.
  - `internal/handlers/purchase_composition_integration_test.go`: composição e contas, validações
    (400/404/409/422), OVERSETTLED, soft-delete, filtros, BY_VALUE e fallback, MADE_TO_ORDER no
    Profit da Sale, snapshot FROM_STOCK, troca de Supplier, valor em estoque.
  - `internal/mcp/tools/tools_purchase_charge_integration_test.go`: `add_purchase_charge`
    (dedupe, erros), `get_purchase`, `get_stock_balance`; `TestCatalogGolden`/`TestCatalogComplete`
    (37).
- Mocks só na fronteira; `go vet ./... && TEST_DATABASE_URL=... go test ./...`.

## Casos de borda & fora de escopo

- Borda: custo depois de recebida; credor trocado após quitar (OVERSETTLED); compra só com
  custos (sem pedras); Σ base_cost = 0; unidade com caixa/espaços (`"M2 "` = `"m2"`).
- Fora: recalcular Items de Sale já gravados quando o custo chega depois (snapshot — AYD-008),
  custo médio ponderado, rateio manual por linha, frontend (SPEC-009@web), frete de entrega
  (AYD-009).

## Checklist de entrega

- [x] Migração 011 (up/down) com teste de paridade
- [x] `purchase_charges` + `allocation_method` + `vw_purchase_line_costs`
- [x] Componentes `PURCHASE_CHARGE` e triggers (custo e troca de Supplier)
- [x] Rotas dos custos, `allocation_method`, composição/contas/linhas em `GET /purchases/{id}`
- [x] Filtros `open_only`/`overdue`; 422 BY_QUANTITY misto; OVERSETTLED
- [x] Custo de entrada em `vw_stock_balance` (+ `stock_value`); rateio em `vw_vendas_resumo`
- [x] Snapshot FROM_STOCK a partir do custo de entrada
- [x] MCP: `add_purchase_charge`, `get_purchase`, `get_stock_balance`; catálogo 1.0.0 (37) + golden
- [x] Testes unitários e de integração por critério de aceite
- [x] Swagger, ops, CLAUDE.md, changelog
