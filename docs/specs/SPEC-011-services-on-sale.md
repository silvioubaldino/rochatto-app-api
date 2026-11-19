---
id: SPEC-011
type: spec
title: Serviços na Sale — catálogo de Services e Items de serviço (api)
status: done
updated: 2026-11-19
parents: [AYD-010@context]
related: [PDR-002@context, AYD-001@context, AYD-002@context, AYD-005@context, AYD-006@context, AYD-007@context, AYD-009@context, SPEC-001, SPEC-002, SPEC-005, SPEC-007, SPEC-008, SPEC-010, GLO]
---

# Spec: Serviços na Sale (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-010**. Enums, rotas, payloads, erros e catálogo do connector
> vêm do AYD-010 e **não são redefinidos aqui**. Pontos que o AYD deixa em aberto estão em
> "Interpretações e pendências".

## Objetivo

Atender o **RF-41** no lado da API: o dono vende **serviços** (instalação, consultoria) no mesmo
Quote/Sale das pedras ou sozinhos. O serviço é um **Item de tipo `SERVICE`** (catálogo `services`
opcional); entra no preço, no Profit e na conta a receber, mas **não** passa por Stock, fila de
compra nem pelo ranking de Products. O custo do serviço vive no **custo da venda `LABOR`** (AYD-009)
ligado ao Item — o Profit o conta uma vez só — e vira conta a pagar ao instalador (AYD-007).

## Critérios de aceite

```gherkin
Cenário: Catálogo de Services (§1)
  Dado POST /services {name, unit, reference_price, reference_cost, notes}
  Então 201 com {id, name, unit, reference_price, reference_cost, notes, created_at}
  E name vazio → 400; name igual (sem diferenciar caixa) a Service ativo → 409 (POST e PUT)
  E GET /services?q&limit&offset busca sem acento nem caixa, ordem por nome; GET /services/{id} → 404 se não existe
  E DELETE → 204 (soft-delete); o nome volta a poder ser usado; Items já vendidos mantêm service_id

Cenário: Item por tipo (§2) — Quote (/quotes, /quotes/{id}/items, quick-sale) e Sale (/vendas/{id}/itens)
  Então kind default PRODUCT; kind e service_id voltam em todas as respostas de Item
  E SERVICE com product_id/produto_id, origin ou fornecedor_id → 400; PRODUCT com service_id → 400
  E service_id inexistente (ou excluído, num Item novo) → 404; kind inválido → 400
  E mudar o kind de um Item existente (PUT) → 409
  E Item de serviço de catálogo sem nome assume o nome do serviço

Cenário: Conversão Quote → Sale (§2)
  Dado Quote com Item SERVICE (qty 12, unit_cost 240)
  Então o Item de serviço nasce com origin null e custo_unit 0, copiando kind e service_id
  E nasce um custo da venda LABOR de 2880 (qty × unit_cost), payee null, sale_item_id = o Item
  E unit_cost estimado 0 → nenhum custo criado
  E lucro = preço − custo das pedras − custos da venda (o custo do serviço conta uma vez)

Cenário: Item de serviço na Sale
  Então custo_unit ≠ 0 → 400 (o custo é editado no custo da venda ligado)
  E remover o Item remove o custo ligado (soft-delete); se a conta do credor desse custo já tem
    quitação → 409 e nada muda; remover Item de pedra só desfaz o vínculo de custos ligados

Cenário: Onde o serviço entra e não entra (§3)
  Então total_venda, lucro, receivable, payment_status, /finance/summary e /dashboard/kpis incluem serviços
  E GET /purchases/pending-sale-items, vw_stock_balance (Outflow) e GET /relatorios/produtos não os incluem
  E POST /purchases e POST /purchases/{id}/sale-items com Item SERVICE → 409
  E GET /relatorios/servicos?from&to → { dados: [{service_id, service_name, unit, total_vendas,
    total_quantidade, total_receita, preco_medio, custo_medio, margem_media_percent}] }

Cenário: Assistant connector (AYD-006)
  Então search_services (R) e get_top_services (R) novas; kind/service_id (opcionais) em create_quote
    e create_quick_sale; get_quote, get_sale e update_sale_item expõem kind/service_id;
    update_sale_item rejeita origin/supplier_id/unit_cost ≠ 0 em serviço;
    catálogo 1.0.0 com 41 ferramentas (22 R + 19 W); golden regenerado
```

## Contratos consumidos/expostos

Expõe, sem redefinir, os do **AYD-010**: enum `ItemKind`; `Service`; rotas

```
GET/POST   /services          GET/PUT/DELETE /services/{id}
POST/PUT   /quotes, /quotes/quick-sale, /quotes/{id}/items[/{itemId}]   (+ kind, service_id nos Items)
GET/POST/PUT /vendas/{id}/itens[/{itemId}]                              (+ kind, service_id; origin anulável)
GET /relatorios/servicos?from&to
```

Consome os custos da venda do **AYD-009** (`sale_costs.sale_item_id`, `LABOR`) e as contas do
**AYD-007** (`vw_obligations`) para a trava de remoção. OpenAPI: `docs/swagger.yaml`.

### Campos JSON (para o web)

- `Service`: `id`, `name`, `unit` (null), `reference_price` (null), `reference_cost` (null), `notes`
  (null), `created_at`. `GET /services` devolve **array** simples (sem envelope); sem `limit` devolve tudo.
- Item de Quote: `kind`, `service_id` (null) ao lado de `product_id`, `product_name`, `quantity`,
  `unit_price`, `unit_cost`. Item de Sale: `kind`, `service_id` (null), `origin` (**null** em serviço)
  ao lado dos legados `produto_id`, `produto_nome`, `fornecedor_id`, `quantidade`, `preco_venda_unit`,
  `custo_unit`.
- `GET /relatorios/servicos`: `{ dados: [...] }` com **os mesmos nomes PT do ranking de Products**
  (`total_vendas`, `total_quantidade`, `total_receita`, `preco_medio`, `custo_medio`,
  `margem_media_percent`) e `service_id`/`service_name`/`unit` no lugar de `produto_*`.

## Abordagem (como)

**Camadas.** `services` guarda o catálogo; o Item guarda só `kind`/`service_id`. Preço, Profit, conta a
receber e custo de serviço **não são persistidos no Item**: o custo é o `sale_costs` ligado por
`sale_item_id` (RNF-05). Nenhuma view precisou mudar — ver migração.

### Migração 013 (`migrations/013_services_on_sale.{up,down}.sql`)

1. Enum `item_kind`; tabela `services` (`name` único entre ativos via índice parcial sobre `lower(name)`,
   `reference_*` ≥ 0, soft-delete).
2. `quote_items` e `itens_venda`: `kind` (default `PRODUCT` → Items existentes), `service_id`→`services`,
   CHECKs de coerência: `PRODUCT` sem `service_id` (e, na Sale, com `origin`); `SERVICE` sem produto,
   origem, Supplier nem Purchase e com `custo_unit = 0`.
3. `itens_venda.origin` anulável (o default `MADE_TO_ORDER` permanece para os INSERTs existentes; o
   serviço grava NULL explícito). Índices em `service_id` e `sale_costs.sale_item_id`.
4. **Views inalteradas:** `vw_vendas_resumo`/`vw_obligation_components`/`vw_quotes_resumo` somam os
   Items inteiros (o serviço entra em preço, Profit e conta a receber; seu `custo_unit` 0 não duplica o
   custo do `sale_costs`); `vw_stock_balance` só lê `origin = 'FROM_STOCK'`; `vw_purchase_line_costs` só
   junta Items com `purchase_id`; a fila só lê `origin = 'MADE_TO_ORDER'`.
5. Down **lossy** (ver `docs/ops/database.md`): Items de serviço viram sob encomenda sem Product.

### Regras e handlers

- `internal/saleitem` (novo, puro): `Kinds`, `Resolve`, `Validate` (coerência por tipo) e
  `LaborCostAmount` (qty × custo estimado, 2 casas).
- `handlers/services.go`: CRUD; busca sem acento por `translate(lower(...))` (sem depender da extensão
  `unaccent`); 409 por nome duplicado (checagem + índice único como garantia final).
- `handlers/vendas.go`: POST/PUT de Item por tipo (`checkServiceRef`, `itemKindError`); o Item de
  serviço não passa por `checkItemRefs`, custo de estoque nem trava de Purchase; `releaseItemCosts` no
  DELETE do Item.
- `handlers/quotes.go`: `kind`/`service_id` em create/quick-sale/`/items`; `convertQuote` cria o custo
  `LABOR` (descrição = nome do Item).
- `handlers/purchases.go`: `attachToPurchase` responde 409 para Item `SERVICE`.
- `handlers/dashboard.go`: `reportPeriod` (from/to compartilhado); ranking de Products filtra
  `kind = 'PRODUCT'`; `getServicosMaisVendidos`.
- Connector: `search_services` (módulo Catalog), `get_top_services` (Analytics), `ItemKind`,
  `kind`/`service_id` em `QuoteItemIn`/`QuoteItem`/`SaleItem` (`origin` opcional na saída), guarda de
  `update_sale_item`.

### Interpretações e pendências (não alteram o contrato)

1. **Catálogo de versão:** como nas SPECs 008–010 (mudanças aditivas antes do primeiro release), o
   `catalog_version` permanece **1.0.0**; a contagem vai a 41 (22 R + 19 W) e o golden é regenerado.
2. **Custo médio/margem de serviço:** `custo_unit` do Item de serviço é 0; o AYD manda "as mesmas
   regras do ranking de Products", então o custo do ranking é o **Σ dos custos da venda ligados aos
   Items** (`sale_costs.sale_item_id`, não excluídos), com média ponderada pela quantidade e margem
   sobre os totais. Serviço sem custo ligado (consultoria do dono) tem custo 0 e margem 100%.
3. **Agrupamento do ranking:** por `service_id`; Items em texto livre (sem `service_id`) agrupam por
   nome sem diferenciar caixa. Limite de 20 linhas, como o de Products.
4. **"Conta dele já tem quitação" (409 ao remover):** a conta PAYABLE da Sale do credor do custo
   ligado (`payee` ou "a definir") com `settled > 0`. Como uma conta agrega os custos do mesmo credor, a
   quitação de qualquer um trava a remoção; nenhuma alteração é feita no 409.
5. **Remover o Item:** o custo ligado sai por soft-delete e o vínculo `sale_item_id` de custos
   restantes é zerado (FK física do Item). Item de **pedra** com custo ligado perde só o vínculo.
6. **PUT de Item de serviço:** `produto_nome`, `quantidade`, `preco_venda_unit` e `service_id` mudam;
   `kind` ausente mantém o atual. Alterar quantidade/preço **não** recalcula o custo `LABOR` (o custo é
   editado em `/vendas/{id}/costs`).
7. **Nome do Item de serviço sem `produto_nome`/`product_name`:** assume o nome do catálogo. Em
   `create_quote`/`create_quick_sale` (connector) `product_name` continua obrigatório no schema.
8. **Vocabulário do connector (`instructions`):** não foi alterado — o AYD-010 não o lista; fica
   como sugestão de minor para o AYD-006.
9. **service_id excluído:** um Item que já o referencia o mantém (PUT com o mesmo id); Item novo → 404.

## Passos de implementação

1. Migração 013 + teste up/down (banco descartável).
2. `internal/saleitem` + testes unitários; models.
3. `/services`; Item por tipo na Sale e no Quote; conversão; trava na Purchase.
4. Relatório de serviços e filtro do ranking de Products.
5. Connector + golden; testes de integração; swagger, ops, CLAUDE.md, changelog.

## Arquivos / módulos afetados

- `migrations/013_services_on_sale.{up,down}.sql`
- `internal/models/models.go`, `internal/saleitem/`
- `internal/handlers/{services,vendas,quotes,purchases,dashboard}.go`, `cmd/server/main.go`
- `internal/mcp/{testdata/catalog.golden.json,catalog_test.go}`, `internal/mcp/toolkit/types.go`,
  `internal/mcp/tools/{catalog,analytics,crm,sales,types}.go`
- `docs/swagger.yaml`, `docs/ops/database.md`, `docs/changelog.md`, `CLAUDE.md`

## Testes (ver docs/conventions.md)

- **Unitários:** `internal/saleitem` (tipo efetivo, coerência, valor do custo LABOR); `toLegacyItemPut`
  com Item de serviço.
- **Integração (`TEST_DATABASE_URL`):** `dbmigrate/migration013_integration_test.go` (Items viram
  PRODUCT, CHECKs, nome único, serviço no preço/Profit/conta, down → up);
  `handlers/services_integration_test.go` (CRUD e busca, validações no Quote e na Sale, conversão com
  custo LABOR e Profit, fila/Purchase/Stock, remoção do Item com e sem quitação, relatório e ranking de
  Products, KPIs); `mcp/tools/tools_services_integration_test.go` (`search_services`, `get_top_services`,
  `kind`/`service_id` nas ferramentas, `update_sale_item`); `TestCatalogGolden`/`TestCatalogComplete` (41).
- Mocks só na fronteira; `go vet ./... && TEST_DATABASE_URL=... go test ./...`.

## Casos de borda & fora de escopo

- Borda: serviço sem nome com `service_id`; custo estimado 0; serviço excluído do catálogo depois de
  vendido; Venda só de serviço (A entregar/Entregue lê-se a executar/executado na tela); Sale cancelada
  sai do relatório.
- Fora: agenda e status de execução por Item; serviço comprado de terceiros via Purchase; frontend
  (SPEC-011@web).

## Checklist de entrega

- [x] Migração 013 (up/down) com teste
- [x] `services` e CRUD `/services`
- [x] `kind`/`service_id` em Quote e Sale; `origin` anulável; validações por tipo
- [x] Conversão cria o custo LABOR ligado; remoção do Item remove o custo (409 se quitado)
- [x] Fora da fila de compra, do Stock e do ranking de Products; 409 em Purchase
- [x] `GET /relatorios/servicos`
- [x] Connector: `search_services`, `get_top_services`, `kind`/`service_id`; catálogo 1.0.0 (41) + golden
- [x] Testes unitários e de integração por critério de aceite
- [x] Swagger, ops, CLAUDE.md, changelog
