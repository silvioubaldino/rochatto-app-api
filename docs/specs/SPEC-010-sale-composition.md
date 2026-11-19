---
id: SPEC-010
type: spec
title: Composição da venda — frete cobrado e custos da venda com credor (api)
status: done
updated: 2026-11-19
parents: [AYD-009@context]
related: [PDR-002@context, PDR-001@context, AYD-001@context, AYD-004@context, AYD-005@context, AYD-006@context, AYD-007@context, AYD-008@context, AYD-010@context, SPEC-001, SPEC-002, SPEC-004, SPEC-006, SPEC-007, SPEC-008, SPEC-009, GLO]
---

# Spec: Composição da venda (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-009**. Enums, rotas, payloads, erros, Profit e catálogo do
> connector vêm do AYD-009 (§1–§4) e **não são redefinidos aqui**. Pontos que o AYD deixa em aberto
> estão em "Interpretações e pendências".

## Objetivo

Atender o **RF-40** no lado da API: o preço ao cliente passa a ser Items + **cobranças adicionais**
(*Sale charges*: frete cobrado) e a venda ganha **custos da venda** (*Sale costs*: frete pago,
instalador, comissão, outro), cada um com **credor** editável. Cobranças entram na conta a receber
do Customer; custos entram nas contas a pagar por credor (AYD-007). `frete_valor`/`comissao_valor`
saem de `vendas`. O Profit passa a ser (Items + cobranças) − custo das pedras − custos da venda.

## Critérios de aceite

```gherkin
Cenário: Cobranças no orçamento e na venda (§1)
  Dado POST /quotes ou /quotes/quick-sale com charges [{kind: FREIGHT, amount: 100}]
  Então o Quote traz charges, items_total, charges_total e total_quote = Items + cobranças
  E PUT /quotes/{id} com charges: ausente não mexe; lista substitui (id mantém a linha)
  E a conversão copia as cobranças para sale_charges; a conta a receber soma SALE_ITEMS + SALE_CHARGE
  E GET/POST /vendas/{id}/charges, PUT/DELETE /vendas/{id}/charges/{chargeId} (DELETE → 204)
  E amount ≤ 0 / kind inválido → 400; venda inexistente → 404; venda CANCELED → 409
  E mudar o valor abaixo do recebido → 200/201 com warnings ["OVERSETTLED"]

Cenário: Custos da venda (§2)
  Dado POST /vendas/{id}/costs {kind: FREIGHT, amount: 150, payee_supplier_id}
  Então 201; payee {type, id, name}; obligation_id = conta a pagar do credor; custos do mesmo credor = 1 conta
  E COMMISSION sem credor informado usa o Partner indicador da Sale; payee null explícito = a definir
  E FREIGHT/LABOR/OTHER só aceitam Supplier; COMMISSION só Partner → 400 se incompatível ou os dois
  E amount < 0 → 400; credor ou venda inexistente → 404; sale_item_id de outra venda → 400
  E venda CANCELED aceita custos
  E trocar o Partner indicador da Sale leva junto a comissão que estava no Partner anterior
  E a quitação da conta usa a categoria padrão do tipo do custo (FREIGHT / LABOR / COMMISSION)

Cenário: Sale (§3)
  Então GET /vendas/{id} traz composition {items_total, charges, charges_total, price_total, costs,
       costs_total}, received, receivable, payment_status (da conta RECEIVABLE), costs_paid,
       costs_open e obligations; total_venda = price_total
  E lucro = (Items + cobranças) − custo das pedras (com rateio da compra) − Σ custos da venda; o pago
    aparece ao lado e nunca entra duas vezes
  E frete_valor, comissao_valor, freight_payable, commission_payable, a_pagar_frete e os filtros
    freight_pending/commission_pending saem; PUT /vendas/{id} com frete_*/comissao_* → 400

Cenário: Migração (§4)
  Então frete_valor > 0 → custo FREIGHT sem credor; comissao_valor > 0 → custo COMMISSION do Partner
  indicador; as quitações da 010 continuam nas mesmas contas; valor de toda conta da Sale idêntico
  antes e depois (verificação interna aborta se divergir); colunas legadas removidas

Cenário: Assistant connector (AYD-006)
  Então set_sale_costs (W · I) e add_sale_charge (W · dd) novas; charges em create_quote e
  create_quick_sale; update_sale sem freight_*/commission_*; get_sale/list_sales com composição e
  custos; catálogo 1.0.0 com 39 ferramentas (20 R + 19 W); golden regenerado
```

## Contratos consumidos/expostos

Expõe, sem redefinir, os do **AYD-009**: enums `SaleChargeKind`, `SaleCostKind`; `SaleCharge`,
`SaleCost`, `SaleComposition`; rotas

```
GET/POST   /vendas/{id}/charges          PUT/DELETE /vendas/{id}/charges/{chargeId}
GET/POST   /vendas/{id}/costs            PUT/DELETE /vendas/{id}/costs/{costId}
POST/PUT   /quotes, /quotes/quick-sale, /quotes/{id}   (+ charges)
GET /vendas, /vendas/{id}  (+ composition, costs_paid, costs_open, obligations, received, charges_total, costs_total)
```

Consome as contas do **AYD-007** (`vw_obligation_components`, `sp_sync_obligations`, `Obligation`
resumo). OpenAPI: `docs/swagger.yaml`.

## Abordagem (como)

**Camadas.** `sale_charges`, `quote_charges` e `sale_costs` guardam só o que o dono informa. Receita,
custos, Profit, pago e em aberto são **views** (RNF-05). As contas continuam mantidas por
**triggers** (`sp_sync_obligations`).

### Migração 012 (`migrations/012_sale_composition.{up,down}.sql`)

1. Enums `sale_charge_kind`, `sale_cost_kind`; tabelas `quote_charges`, `sale_charges`
   (`amount > 0`) e `sale_costs` (`amount >= 0`, um credor no máximo: `payee_supplier_id` |
   `payee_partner_id`, `sale_item_id`).
2. Foto dos valores das contas da Sale; migra `frete_valor`/`comissao_valor` para `sale_costs`.
3. `vw_obligation_components` (CREATE OR REPLACE): sai `LEGACY_FREIGHT`/`LEGACY_COMMISSION`; entram
   `SALE_CHARGE` (RECEIVABLE, Customer) e `SALE_COST` (PAYABLE, credor). `amount` zera em Sale
   CANCELED (a conta sai dos totais), `base_amount` mantém a existência da conta; custo de valor 0
   não gera conta.
4. Triggers `trg_sync_obligations_sale_component` (cobranças e custos) e extensão de
   `trg_sync_obligations_sale`: trocar `vendedor_externo_id` move os custos COMMISSION do Partner
   anterior para o novo.
5. Verificação interna (conta a conta) e remoção das colunas `frete_valor`/`comissao_valor`.
6. `vw_vendas_resumo` recriada (`items_total`, `charges_total`, `total_venda` = preço, `total_custo`,
   `costs_total`/`costs_freight`/`costs_commission`/`costs_other`, `lucro`, `costs_paid`,
   `costs_open`, tudo pré-agregado por Sale) e `vw_quotes_resumo` (`total_quote` com cobranças).
7. Down **lossy** (ver `docs/ops/database.md`).

### Regras e handlers

- `internal/salecomp` (novo, puro): tipos, credor por tipo, rótulos e `Profit`.
- `handlers/sale_composition.go`: rotas, lock da Sale (`FOR UPDATE`), validação, `warnings`,
  `composition`/`obligations` do detalhe.
- `handlers/vendas.go`: detalhe/lista sem os campos legados; PUT recusa campos removidos (400).
- `handlers/quotes.go`: `charges` em POST/PUT/quick-sale; conversão copia as cobranças.
- `obligations`: categoria padrão da quitação por tipo de custo (`SALE_COST` + `sale_costs.kind`).
- `finance.go`/`dashboard.go`: ver interpretações 6 e 7.
- Connector: `set_sale_costs`, `add_sale_charge`, `charges` nas criações, `update_sale` reduzido,
  `SaleSummary`/`SaleDetail` com composição.

### Interpretações e pendências (não alteram o contrato)

1. **frete_pago/comissao_paga:** a 010 já os havia convertido em quitações; a 012 migra só os valores.
2. **Credor ausente × null:** ausente = padrão (COMMISSION → Partner indicador; demais → a definir);
   `null` explícito = a definir. Vale no PUT (substituição completa).
3. **"Credor de tipo incompatível"** = Supplier em COMMISSION ou Partner nos demais; o `kind` do
   Supplier (STONE/TRANSPORT…) é dica de catálogo, não é validado.
4. **sale_item_id:** aceito se o Item pertence à venda; o tipo de Item de serviço é do AYD-010.
5. **Sale CANCELED:** custos aceitos; o valor da conta zera (como as demais contas da Sale cancelada),
   então uma quitação anterior pode aparecer como OVERSETTLED.
6. **`/finance/summary`:** `sales.freight`/`commission` = Σ custos FREIGHT/COMMISSION previstos;
   `sales.other_costs` (novo) = LABOR + OTHER, para `profit = revenue − cost − freight − commission −
   other_costs`. `balances.freight_payable`/`commission_payable` de `/dashboard/kpis` (AYD-005, ainda
   não revisto) foram mantidos: comissão = aberto das contas a pagar de Sale com credor Partner;
   frete = aberto das demais contas a pagar de Sale. `total_comissoes_pendentes` (legado) idem.
7. **Quote:** `total_quote` inclui as cobranças; `items_total`/`charges_total` novos.
8. **`set_sale_costs`:** upsert por (kind, credor, descrição); nada é excluído pelo connector.

## Passos de implementação

1. Migração 012 + teste up/down (banco descartável).
2. `internal/salecomp` + testes unitários; models.
3. Handlers de cobranças/custos, Sale, Quote, finance/dashboard.
4. Obligations: categoria padrão por tipo de custo.
5. Connector + golden; testes de integração; swagger, ops, CLAUDE.md, changelog.

## Arquivos / módulos afetados

- `migrations/012_sale_composition.{up,down}.sql`
- `internal/models/models.go`, `internal/salecomp/`
- `internal/handlers/{sale_composition,vendas,quotes,finance,dashboard}.go`
- `internal/obligations/{rules,store}.go`
- `internal/mcp/{instructions.go,testdata/catalog.golden.json}`, `internal/mcp/toolkit/types.go`,
  `internal/mcp/tools/{sales,crm,types}.go`
- `docs/swagger.yaml`, `docs/ops/database.md`, `docs/changelog.md`, `CLAUDE.md`

## Testes (ver docs/conventions.md)

- **Unitários:** `internal/salecomp` (credor por tipo, rótulos, Profit do exemplo do AYD),
  `internal/obligations` (categoria padrão por tipo de custo).
- **Integração (`TEST_DATABASE_URL`):** `dbmigrate/migration012_integration_test.go` (migração, quitação
  preservada, contas iguais, cobrança, troca de Partner, down → up);
  `handlers/sale_composition_integration_test.go` (cobranças no Quote/Sale, conversão, custos, credor,
  validações, OVERSETTLED, categoria padrão, cancelada); `mcp/tools/tools_write_integration_test.go`
  (`set_sale_costs`, `add_sale_charge`, `charges` nas criações, `update_sale`); `TestCatalogGolden`/
  `TestCatalogComplete` (39).
- Mocks só na fronteira; `go vet ./... && TEST_DATABASE_URL=... go test ./...`.

## Casos de borda & fora de escopo

- Borda: custo de valor 0 (sem conta); credor trocado após quitar; comissão sem Partner (a definir);
  Partner indicador trocado; cobrança com recebimento já feito.
- Fora: Delivery Batch (rateio do frete por rota), módulo de comissões (RF-25), custo por Item,
  serviços (AYD-010), frontend (SPEC-010@web).

## Checklist de entrega

- [x] Migração 012 (up/down) com verificação e teste
- [x] `quote_charges`, `sale_charges`, `sale_costs`; componentes `SALE_CHARGE`/`SALE_COST` e triggers
- [x] Rotas de cobranças e custos; Quote com `charges`; conversão copia
- [x] `vw_vendas_resumo` com preço, custos, Profit, pago e em aberto; `composition` e `obligations`
- [x] Campos/filtros legados removidos
- [x] Connector: `set_sale_costs`, `add_sale_charge`, `charges`; catálogo 1.0.0 (39) + golden
- [x] Testes unitários e de integração por critério de aceite
- [x] Swagger, ops, CLAUDE.md, changelog
