---
id: SPEC-001
type: spec
title: Espinha Quote → Sale (api)
status: review
updated: 2026-07-16
parents: [AYD-001@context]
related: [GLO, ADR-001@context]
---

# Spec: Espinha Quote → Sale (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-001**. `approved` = contrato local congelado;
> `done` = implementado (este documento vira o registro histórico da entrega).
>
> O contrato (enums, rotas, payloads, mapeamento de conversão) é definido em **AYD-001** e
> **ADR-001** e não é redefinido aqui — apenas referenciado e traduzido em implementação.

## Objetivo

Papel deste repo (conforme AYD-001): expor a entidade **Quote** (CRUD + Items), as
transições de status **OPEN/WON/LOST** e o endpoint de **conversão**
(`POST /quotes/{id}/convert`) que cria a **Sale** (`vendas`) a partir de um Quote **OPEN**,
de forma transacional — sem redigitar Items nem valores (RF-07). Inclui a migração que
introduz `quotes`, `quote_items`, `vendas.quote_id` e a view `vw_quotes_resumo`.

## Critérios de aceite

```gherkin
Cenário: Emitir Quote com Items (RF-05)
  Dado um Customer existente
  Quando faço POST /quotes com customer_id e uma lista de items (product_name, quantity,
    unit_price, unit_cost obrigatórios)
  Então recebo 201 com status "OPEN", os items criados e total_quote = Σ quantity*unit_price

Cenário: Marcar Quote como perdido com motivo (RF-06 / RN-07)
  Dado um Quote em status "OPEN"
  Quando faço PATCH /quotes/{id}/status com { status: "LOST", lost_reason: "preço" }
  Então recebo 200 com status "LOST" e lost_reason preenchido
  E nenhuma Sale é criada

Cenário: Reabrir Quote perdido
  Dado um Quote em status "LOST"
  Quando faço PATCH /quotes/{id}/status com { status: "OPEN" }
  Então recebo 200 com status "OPEN"

Cenário: Não é possível setar WON via PATCH
  Dado um Quote em status "OPEN"
  Quando faço PATCH /quotes/{id}/status com { status: "WON" }
  Então recebo 409 (WON só é alcançado via /convert)

Cenário: Converter Quote ganho em Sale (RF-07 / RN-01)
  Dado um Quote em status "OPEN" com ao menos 1 item
  Quando faço POST /quotes/{id}/convert
  Então recebo 201 com { sale_id, quote_id }
  E o Quote passa a status "WON"
  E existe uma Sale (vendas) com quote_id apontando para este Quote, status "A_ENTREGAR",
    data_venda = hoje, frete_* = 0, comissao_* = 0
  E os itens_venda espelham 1:1 os quote_items (mapeamento EN→PT do AYD-001), com
    pago_fornecedor = 0

Cenário: Não converter Quote que não está OPEN (RN-07)
  Dado um Quote em status "LOST" (ou já "WON")
  Quando faço POST /quotes/{id}/convert
  Então recebo 409 e nenhuma Sale é criada

Cenário: Não converter Quote sem items
  Dado um Quote em status "OPEN" sem nenhum item
  Quando faço POST /quotes/{id}/convert
  Então recebo 422 e nenhuma Sale é criada

Cenário: Editar Quote e Items apenas enquanto OPEN
  Dado um Quote em status "WON" ou "LOST"
  Quando faço PUT /quotes/{id} ou qualquer escrita em /quotes/{id}/items
  Então recebo 409 (edição bloqueada fora de OPEN)
```

## Contratos consumidos/expostos

Fonte da verdade: **AYD-001**, seção "Contratos" (enum `QuoteStatus`, rotas `/quotes*`,
payloads, tabela de erros e tabela de mapeamento de conversão Quote EN → Sale/itens PT). Ver
também **ADR-001** para a convenção de nomenclatura (entidade nova ⇒ inglês; fronteira com
legado PT traduzida explicitamente na conversão). Este repo **não** redefine nada disso —
qualquer ambiguidade encontrada durante a implementação volta ao orquestrador para decisão no
AYD/ADR, não é resolvida aqui.

Rotas expostas (sob `/api/v1`, auth Firebase Bearer — RNF-02):

```
GET    /quotes
POST   /quotes
GET    /quotes/{id}
PUT    /quotes/{id}
DELETE /quotes/{id}

GET    /quotes/{id}/items
POST   /quotes/{id}/items
PUT    /quotes/{id}/items/{itemId}
DELETE /quotes/{id}/items/{itemId}

PATCH  /quotes/{id}/status
POST   /quotes/{id}/convert
```

## Abordagem (como)

Segue o padrão já usado em `internal/handlers/vendas.go` (handler com `db *gorm.DB`,
`Register(mux)`, tipos `*Request`/`*Response` próprios, view de resumo lida via
`gorm.Table(...).Scan`, transação com `Begin`/`defer Rollback`/`Commit`):

- **Migração 003** (`quotes`, `quote_items`, `quote_status` enum, `vendas.quote_id`,
  `vw_quotes_resumo`), seguindo o estilo de `001_init.up.sql`/`.down.sql`.
- **Modelos** novos em `internal/models/models.go`: `Quote` (`TableName() "quotes"`) e
  `QuoteItem` (`TableName() "quote_items"`), com campos em inglês (espelhando o padrão de
  `Venda`/`ItemVenda`, mas já na nomenclatura canônica do ADR-001). `Venda` ganha o campo
  `QuoteID *uuid.UUID` (`json:"quote_id"`).
- **Handler novo** `internal/handlers/quotes.go`: CRUD de Quote + Items (calcado em
  `vendas.go`), validações de status (só edita/adiciona/remove item enquanto `OPEN`),
  `PATCH /status` (OPEN↔LOST, `lost_reason` opcional só quando destino é LOST, 409 se
  tentar setar WON) e `POST /convert` (transação: cria `Venda` + `ItemVenda` a partir do
  Quote + `quote_items`, seta `quotes.status = WON`, `vendas.quote_id`; 409 se
  `status != OPEN`, 422 se sem items, 404 se Quote inexistente).
- Registro em `cmd/server/main.go`: `handlers.NewQuotesHandler(db).Register(apiMux)`.
- Erros seguem o padrão de `models.WriteError` já usado nos demais handlers (mensagens em
  PT-BR; nomes de campo no payload em inglês, ex.: `"campo 'customer_id' é obrigatório"`).

## Passos de implementação

1. Migração `migrations/003_quotes.up.sql` / `.down.sql`: enum `quote_status`
   (`OPEN|WON|LOST`), tabela `quotes`, tabela `quote_items`, `ALTER TABLE vendas ADD COLUMN
   quote_id UUID UNIQUE REFERENCES quotes(id)`, view `vw_quotes_resumo`, índices.
2. Modelos: `Quote`, `QuoteItem` em `internal/models/models.go`; adicionar `QuoteID
   *uuid.UUID` a `Venda`.
3. `internal/handlers/quotes.go`:
   a. CRUD `Quote` (list com filtros `status`, `from`, `to`, `customer_id`, `q`; create com
      items aninhados; get com items + `total_quote` via `vw_quotes_resumo`; update de
      cabeçalho só quando `OPEN`; soft-delete).
   b. CRUD `quote_items` (list/create/update/delete), bloqueado fora de `OPEN` (409).
   c. `PATCH /quotes/{id}/status`: valida transição (`OPEN↔LOST`; rejeita `WON` com 409;
      `lost_reason` opcional, só aceito quando destino é `LOST`).
   d. `POST /quotes/{id}/convert`: dentro de uma transação — valida `status == OPEN` (senão
      409) e `len(items) > 0` (senão 422); cria `Venda` (mapeamento EN→PT da tabela do
      AYD-001: `customer_id→cliente_id`, `referrer_id→vendedor_externo_id`,
      `notes→observacoes`, `status='A_ENTREGAR'`, `data_venda=hoje`, `frete_*=0`,
      `comissao_*=0`, `quote_id=quotes.id`); cria um `ItemVenda` por `quote_item`
      (`product_id→produto_id`, `product_name→produto_nome`, `supplier_id→fornecedor_id`,
      `supplier_name→fornecedor_nome`, `quantity→quantidade`,
      `unit_price→preco_venda_unit`, `unit_cost→custo_unit`, `pago_fornecedor=0`); seta
      `quotes.status = WON`; commit atômico (tudo ou nada).
4. Registrar `QuotesHandler` em `cmd/server/main.go`.
5. Testes (unit + aceite) cobrindo os cenários Gherkin acima.
6. Atualizar `docs/changelog.md` deste repo (bloco `Unreleased`, 1 linha).

## Arquivos / módulos afetados

- `migrations/003_quotes.up.sql` (novo)
- `migrations/003_quotes.down.sql` (novo)
- `internal/models/models.go` (adiciona `Quote`, `QuoteItem`; altera `Venda` com `QuoteID`)
- `internal/handlers/quotes.go` (novo)
- `cmd/server/main.go` (registra `QuotesHandler`)
- `docs/changelog.md` (1 linha em `Unreleased`)

## Testes (ver docs/conventions.md)

- **Aceite (mapeia os critérios acima):** 1 teste por cenário Gherkin — criação de Quote com
  items; PATCH de status (OPEN→LOST com/sem motivo, LOST→OPEN, tentativa de WON via PATCH);
  convert com sucesso (valida Sale + itens_venda criados e Quote WON); convert bloqueado
  fora de OPEN (409); convert sem items (422); edição de Quote/Items bloqueada fora de OPEN.
- **Unit/integração:** validação de payload de `quote_items` (campos obrigatórios,
  `quantity`/`unit_price`/`unit_cost` não-negativos, mesmo padrão de `validateItemFields`
  em `vendas.go`); rollback da transação de `/convert` em caso de erro no meio (ex.: falha
  ao criar item — nem Sale nem `quotes.status` mudam); unicidade de `vendas.quote_id`
  (constraint garante 1 Sale por Quote).

## Casos de borda & fora de escopo

- **Borda:** Quote sem `referrer_id` (null, permitido); `lost_reason` omitido ao marcar
  LOST (RN-07: opcional); reabrir `LOST→OPEN` múltiplas vezes; tentar excluir/editar item de
  Quote fora de `OPEN`; `DELETE /quotes/{id}` (soft-delete) em Quote já `WON` — permitido
  (não afeta a Sale já criada, que referencia o Quote por id).
- **Fora de escopo (deste SPEC e do AYD-001):** origem do Item (`from stock` /
  `made-to-order`), `Purchase`, `Stock`/Outflow, `pago_fornecedor` no nível de
  `quote_items` — ficam para o próximo AYD (Purchase consolidada). Revisões de Quote com
  histórico (RF-08) e migração PT→EN das tabelas legadas — também fora.

## Checklist de entrega

- [x] Migração 003 criada (up/down); não aplicada localmente — sandbox sem Postgres/Docker
      disponível (ver nota abaixo)
- [x] Modelos `Quote`/`QuoteItem`/`Venda.QuoteID` compilando
- [x] Handler `quotes.go` com todas as rotas do contrato registradas
- [x] Transição de status (OPEN↔LOST, WON só via /convert) implementada (400 status
      inválido, 409 WON via PATCH, 409 quote já WON)
- [x] `/convert` transacional, com mapeamento EN→PT completo (sucesso + 409 fora de OPEN +
      422 sem items)
- [x] `vw_quotes_resumo` retornando `total_quote` (Σ quantity*unit_price)
- [ ] Todos os critérios de aceite (Gherkin) com teste correspondente — **parcial**: cobertos
      apenas os testes unitários de validação/enum (`quotes_test.go`); os cenários que
      dependem de Postgres real (criação, convert, transações) não puderam ser exercitados
      neste ambiente (sem Docker/DB disponível) — pendente rodar `go test ./...` com
      `DATABASE_URL` real antes do deploy
- [x] `docs/changelog.md` atualizado (bloco Unreleased)
- [x] `status: review` sinalizado para o orquestrador fechar `parents`/`children` no AYD-001
