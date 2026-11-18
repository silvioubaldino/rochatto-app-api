---
id: SPEC-002
type: spec
title: Atendimento da Sale — origem do Item, Purchase, Stock e status (api)
status: done
updated: 2026-11-18
parents: [AYD-002@context]
related: [PDR-001@context, ADR-001@context, GLO]
---

# Spec: Atendimento da Sale (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-002**. `done` = implementado (registro histórico da
> entrega). Contratos (enums, rotas, payloads, erros) vêm do AYD-002 e não são redefinidos
> aqui; leituras de pontos ambíguos estão em "Desvios/interpretações".

## Objetivo

Atender RF-09, RF-12, RF-13, RF-30, RF-31, RF-32 e RN-03/04/05/06/11–16 com as
simplificações do PDR-001: o dono define a **origem** de cada Item da Sale (made-to-order ×
from stock), agrupa os made-to-order numa **Purchase** por Supplier com **um pagamento**,
enxerga o **Balance** de cada Product (com ajustes) e controla o **status de entrega** da Sale
(com cancelamento); o **status de pagamento** é calculado. `/estoque` e `POST /vendas` saem.

## Critérios de aceite

```gherkin
Cenário: Migração dos dados legados (006)
  Dado o banco na 005 com Sales em todos os status antigos, Items com/sem Supplier e
       pago_fornecedor, e entradas de estoque com/sem produto_id
  Quando aplico a 006
  Então ENTREGUE → DELIVERED e os demais → TO_DELIVER
  E cada Supplier com Items (Sale ENTREGUE ou pago_fornecedor > 0, Sale não excluída) ganha
    uma Purchase MADE_TO_ORDER RECEIVED com notes "Migração: itens anteriores ao módulo de
    compras", paid_amount = Σ pago_fornecedor, PAID se cobre o custo (senão PENDING com to_pay)
  E os demais Items ficam sem Purchase (na fila); todos com origin = MADE_TO_ORDER
  E estoque com produto_id vira ajuste INITIAL; sem produto_id não migra
  E vendas.status, itens_venda.pago_fornecedor e estoque deixam de existir
  E down 1 volta a um 005 funcional e up de novo funciona

Cenário: Item FROM_STOCK (RN-16)
  Quando faço PUT/POST de Item com origin FROM_STOCK sem custo_unit
  Então custo_unit = custo da última entrada (reposição recebida ou ajuste INITIAL/COUNT com custo)
  E sem saldo suficiente a resposta traz warnings ["INSUFFICIENT_STOCK"] e o Balance fica negativo
  E FROM_STOCK sem produto_id → 400; custo_unit enviado é respeitado

Cenário: Fila "a comprar" (RF-31)
  Quando faço GET /purchases/pending-sale-items
  Então vêm só Items MADE_TO_ORDER sem Purchase de Sales não canceladas, agrupados por Supplier,
    com o grupo sem Supplier por último (FROM_STOCK, comprados e de Sale cancelada ficam fora)

Cenário: Anexar Items à Purchase
  Quando anexo Item de outro Supplier/sem Supplier, já em outra Purchase, FROM_STOCK ou de Sale
    cancelada → 409; Purchase ≠ OPEN ou kind ≠ MADE_TO_ORDER → 409; Item inexistente → 404
  E desanexar (só OPEN) devolve o Item à fila; excluir Purchase OPEN libera os Items

Cenário: Travas do Item em Purchase
  Dado um Item com purchase_id
  Quando PUT muda origin, fornecedor_id, quantidade ou custo_unit, ou faço DELETE → 409
  E PUT que só muda preço/nome → 200
  E Items de Sale CANCELED não podem ser criados/alterados/excluídos → 409

Cenário: Status da Purchase
  Só OPEN→ORDERED (carimba ordered_at), ORDERED→OPEN (limpa ordered_at), ORDERED→RECEIVED
    (carimba received_at); qualquer outra → 409; ORDERED sem linhas → 422

Cenário: Pagamento ao Supplier
  PAID sem valor/data → paid_amount = total_cost, paid_at = hoje, to_pay = 0
  PENDING → paid_amount e paid_at null, to_pay = total_cost; paid_amount < 0 → 400
  E o "a pagar" do painel = Σ to_pay das Purchases não excluídas

Cenário: Cancelar Sale (RN-15)
  Com Item em Purchase ORDERED/RECEIVED → 409; com Item em Purchase OPEN → 200 e o Item é
    liberado na mesma transação; Sale já CANCELED → 409 (terminal)

Cenário: Balance (RN-03)
  Balance = Σ reposição RECEIVED − Σ FROM_STOCK de Sales não canceladas ± ajustes
  E reposição OPEN/ORDERED não conta; cancelar a Sale remove o Outflow
  E ajustes: quantity 0 / reason inválido / produto inexistente → 400; imutáveis

Cenário: Sale status
  payment_status = PENDING (recebido 0) / PARTIAL / PAID (recebido ≥ total)
  GET /vendas filtra delivery_status e payment_status (aceitam lista por vírgula)
  PUT /vendas/{id} com status → 400; POST /vendas não existe

Cenário: Dashboard
  Sales CANCELED saem de a receber, Profit, vendido, comissões pendentes, vendas abertas,
    série mensal e mais vendidos
```

## Contratos consumidos/expostos

Os do AYD-002 (seções 1–4), mais estes formatos acertados com o web (o AYD não fixa):

- **GET /purchases/pending-sale-items** →
  `[{ supplier_id, supplier_name, items: [{ id, sale_id, customer_name, produto_id, produto_nome, quantidade, custo_unit, sale_date }] }]`
  — grupos por nome do Supplier; `supplier_id/supplier_name = null` (Items sem Supplier) por último.
- **GET /stock/balance/{productId}** → linha do Balance + `movements: [{ kind: INFLOW|OUTFLOW|ADJUSTMENT, date, quantity, unit_cost, reference_id, description }]`,
  data desc; `quantity` com sinal (OUTFLOW negativo); `reference_id` = Purchase / Sale / ajuste.
- **GET /purchases** → `PurchaseDetail` sem `sale_items`/`items`, com `line_count` e `created_at`.
- **Item de Sale (POST/PUT)** → o Item com `warnings: string[]` no mesmo objeto (sempre presente,
  pode ser `[]`). Todo Item (inclusive em GET) traz `origin`, `purchase_id` e o extra só-leitura
  `purchase_status` (status da Purchase do Item ou null).
- **GET /stock/adjustments** → linhas com `product_name`.
- **Detalhe/lista de Sale** perdem `status`, `a_pagar_fornecedor`, `total_pago_fornecedor`;
  Item perde `pago_fornecedor` e `a_pagar_fornecedor`.

OpenAPI: `docs/swagger.yaml`.

## Abordagem (como)

- **Migração 006** (`migrations/006_sale_fulfillment.{up,down}.sql`): enums, `purchases`,
  `purchase_items` (só reposição), `stock_adjustments`, `itens_venda.origin/purchase_id` (CHECKs:
  FROM_STOCK exige produto e não tem Purchase), `vendas.delivery_status`; migração de dados;
  remoção do legado; views:
  - `vw_vendas_resumo`: `v.*` + totais pré-agregados por subconsulta + `lucro` + `payment_status`.
    Uma linha por Sale, inclusive CANCELED (o filtro de cancelada fica nos totais).
  - `vw_purchases_resumo`: `total_cost` (Items de Sale + linhas de reposição), `to_pay`, `line_count`.
  - `vw_stock_balance`: por Product não excluído; `last_inflow_*` via `DISTINCT ON` sobre
    reposições recebidas ∪ ajustes INITIAL/COUNT com custo (data desc, created_at desc).
- **Regras puras** em `internal/fulfillment` (transições, carimbos, defaults de pagamento, regra
  de cancelamento, travas do Item, derivação de payment_status); handlers carregam estado em
  transação (`FOR UPDATE`), aplicam a regra e persistem. Erros de regra viram `httpError`
  (409/422/404/400) via `writeTxError`.
- **Handlers**: `purchases.go`, `stock.go`; `vendas.go` reescrito (sem POST, PATCH
  delivery-status, Items com origem); `estoque.go` removido; `quotes.go` grava
  `delivery_status`/`origin` explícitos; `dashboard.go` filtra CANCELED e soma `to_pay`.
- **pg_cron** (`docs/ops/pg_cron_setup.sql`, fora das migrations): a função ignora/limpa
  notificações de Sales CANCELED (não referenciava colunas removidas).

### Desvios/interpretações

1. **Legado de Sales excluídas** (soft-delete): seus Items não entram na Purchase legada — "Sales
   não canceladas" lido como Sales ativas. O `pago_fornecedor` delas (e de Items sem Supplier)
   é perdido (documentado em `docs/ops/database.md`).
2. **Purchase legada**: `ordered_at = received_at = data da migração`; `paid_at = data da
   migração` quando `paid_amount > 0`, senão null; `paid_amount` pode ser 0.
3. **Estoque legado com quantidade 0** não vira ajuste (ajuste exige `quantity ≠ 0`).
4. **FROM_STOCK sem nenhuma entrada** para o Product: sem "última entrada", o custo cai no
   custo atual do Item (PUT) ou no custo de referência do Product (POST), senão 0.
   `custo_unit` ausente em MADE_TO_ORDER: custo atual (PUT) / referência (POST). `origin`
   ausente no PUT mantém a atual.
5. **Aviso INSUFFICIENT_STOCK** = Balance (já contando o Item) < 0 após gravar.
6. **Trava do Item em Purchase OPEN**: mesmo 409 das Purchases pedidas, com mensagem pedindo
   para remover o Item da Purchase; DELETE de Item em qualquer Purchase → 409.
7. **PUT /vendas/{id} com `status` ou `delivery_status`** → 400 (em vez de ignorar).
8. **Filtros `from`/`to` de GET /purchases** usam a data de criação (fuso America/Sao_Paulo) —
   a Purchase não tem "data da compra".
9. **ORDERED → OPEN** limpa `ordered_at`.
10. **Item de Sale inexistente** ao anexar → 404 (o AYD só lista os 409).
11. **Respostas não fixadas pelo AYD**: anexar → 200 `PurchaseDetail`; desanexar e excluir
    linha → 204; linha de reposição POST/PUT → a linha; `PUT /purchases/{id}` → `PurchaseDetail`.
12. **Balance lista todos os Products não excluídos** (inclusive os sem movimento, balance 0).
13. **GET /vendas?pendente=true** também exclui Sales CANCELED ("a receber").
14. **Filtros `delivery_status`/`payment_status`** aceitam lista separada por vírgula (pedido do web).
15. Receipts de Sale cancelada continuam editáveis (estorno fora do MVP, AYD questão 3).

## Passos de implementação

1. Migração 006 + teste do mapeamento legado (banco descartável).
2. Models (`Purchase`, `PurchaseItem`, `StockAdjustment`; `Venda.DeliveryStatus`; `ItemVenda.Origin/PurchaseID`).
3. `internal/fulfillment` + testes unitários.
4. Handlers (vendas, purchases, stock, dashboard, quotes) + `cmd/server` (rotas).
5. Testes de integração; swagger, ops, changelog.

## Arquivos / módulos afetados

- `migrations/006_sale_fulfillment.{up,down}.sql`
- `internal/models/models.go`, `internal/fulfillment/`
- `internal/handlers/{vendas,purchases,stock,dashboard,quotes,helpers}.go` (`estoque.go` removido)
- `cmd/server/main.go`
- `docs/swagger.yaml`, `docs/ops/database.md`, `docs/ops/pg_cron_setup.sql`, `docs/changelog.md`, `CLAUDE.md`

## Testes (ver docs/conventions.md)

- Unitários: `internal/fulfillment/fulfillment_test.go` (transições + 409 antes de 422,
  carimbos, defaults/zeragem do pagamento, to_pay, cancelamento, payment_status, travas,
  enums); `internal/handlers/fulfillment_test.go` (validação de Item/linha/ajuste).
- Integração (`TEST_DATABASE_URL`):
  - `internal/dbmigrate/migration006_integration_test.go` — mapeamento legado, down/up.
  - `internal/handlers/fulfillment_integration_test.go` — FROM_STOCK (custo, aviso, negativo,
    extrato), reposição (Inflow só RECEIVED, custo da última entrada), fila e 409 de anexar,
    travas do Item, transições (422 vazia), pagamento e Σ to_pay no painel, cancelamento
    (409 ORDERED/RECEIVED, libera OPEN, terminal), payment_status e filtros, dashboard sem
    canceladas, ajustes.
  - `summary_integration_test.go` / `quotes_integration_test.go` ajustados ao novo schema.

## Casos de borda & fora de escopo

- Fora: Delivery Batch / Driver / Freight por rota, parcelas ao Supplier, entrega parcial,
  reserva/Stock mínimo, reabrir Sale cancelada, estorno de Receipts.
- Purchase RECEIVED é terminal; correção de quantidade recebida = ajuste de Stock.

## Checklist de entrega

- [x] Migração 006 (up/down) testada com dados legados
- [x] Rotas `/purchases`, `/stock`, `PATCH /vendas/{id}/delivery-status`; `/estoque` e `POST /vendas` removidos
- [x] Dashboard e "a receber" excluem CANCELED; a pagar = Σ to_pay
- [x] Testes unitários e de integração cobrindo os critérios
- [x] Swagger, ops (query de estoque sem produto), changelog, CLAUDE.md
