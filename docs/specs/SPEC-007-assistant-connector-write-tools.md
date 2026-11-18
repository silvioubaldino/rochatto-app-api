---
id: SPEC-007
type: spec
title: Assistant connector — ferramentas de escrita, confirmação, deduplicação e catálogo completo 1.0.0 (api)
status: done
updated: 2026-11-18
parents: [AYD-006@context]
related: [ADR-002@context, SPEC-006, GLO]
---

# Spec: Assistant connector — ferramentas de escrita (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-006** na parte de **escrita**. Contratos (nomes,
> entradas, saídas, anotações, mapeamento EN↔PT, deduplicação, scopes) vêm do AYD-006 §§ 3–5 e
> **não são redefinidos aqui**. **Depende do SPEC-006@api**, que entrega a migração 009, o servidor
> OAuth, a plataforma `/mcp` (registro por módulo, Gateway em processo, adaptadores EN↔PT,
> erros, paginação, scopes, auditoria) e todas as ferramentas `readOnly`.

## Objetivo

Completar o `catalog_version 1.0.0` (34 ferramentas) com as **16 ferramentas não `readOnly`** do
§ 4 do AYD-006, sobre a plataforma do SPEC-006, sem regra de negócio nova (paridade com o REST):

- **Escrita (16):** `create_customer`, `create_quote`, `set_quote_status`,
  `convert_quote_to_sale`, `create_quick_sale`, `update_sale`, `update_sale_item`,
  `set_sale_delivery_status`, `cancel_sale`, `record_receipt`, `confirm_receipt`,
  `create_purchase`, `add_sale_items_to_purchase`, `set_purchase_status`,
  `record_supplier_payment`, `create_stock_adjustment`.
- **Anotações e confirmação** (§ 3.6) em todas elas.
- **Deduplicação de criação** (§ 3.7) nas 6 ferramentas `dd`: `create_customer`, `create_quote`,
  `create_quick_sale`, `record_receipt`, `create_purchase`, `create_stock_adjustment`.
- **Enforcement do scope `write`** (§ 3.8).
- **Teste golden** com o catálogo completo (34 ferramentas, 18 R + 16 não R).

## Critérios de aceite

```gherkin
Cenário: catálogo completo como contrato
  Dado o registro com os módulos de leitura (SPEC-006) e de escrita (esta SPEC)
  Então tools/list de uma Connection read+write devolve 34 ferramentas (catalog_version 1.0.0)
  E o golden (testdata/catalog.golden.json) cobre nomes, títulos, descrições, anotações, scopes, schemas de entrada e saída
  E mudar nome, schema ou anotação sem regenerar o snapshot falha o CI

Cenário: anotações de risco (§ 3.6 / § 4.1)
  Então convert_quote_to_sale e cancel_sale têm destructiveHint true
  E as demais 14 escritas têm destructiveHint false
  E idempotentHint true em set_quote_status, update_sale, update_sale_item, set_sale_delivery_status, cancel_sale, confirm_receipt, set_purchase_status, record_supplier_payment
  E todas têm openWorldHint false e readOnlyHint false

Cenário: paridade de regra (AYD-006)
  Dado uma Sale CANCELED
  Quando record_receipt é chamado para ela
  Então isError true, code CONFLICT, message "venda cancelada: recebimentos bloqueados"

Cenário: deduplicação
  Quando record_receipt é chamado duas vezes com argumentos idênticos em 120 s (mesma Connection)
  Então só um Receipt existe e a segunda resposta tem deduplicated true
  E com allow_duplicate true a segunda chamada cria outro Receipt

Cenário: deduplicação — limites
  Então chamada anterior com erro (isError) não deduplica
  E argumentos diferentes, outra ferramenta, outra Connection ou chamada > 120 s depois não deduplicam
  E ordem diferente das chaves do JSON conta como argumentos idênticos (JSON canônico)
  E allow_duplicate não entra no hash dos argumentos

Cenário: atualização parcial
  Dado uma Sale com frete_valor 300 e observacoes "x"
  Quando update_sale {sale_id, freight_paid: 300}
  Então frete_pago = 300 e frete_valor/observacoes permanecem

Cenário: scope de leitura
  Dada uma Connection só com scope read
  Então tools/list não contém nenhuma das 16 escritas
  E chamá-las mesmo assim devolve isError true, code FORBIDDEN, e nenhuma chamada chega ao Gateway

Cenário: auditoria das escritas
  Quando uma escrita é chamada
  Então existe um registro em mcp_tool_calls com tool, outcome e duration_ms
  E nas ferramentas dd o campo result guarda a saída (usada pela deduplicação)
```

## Contratos consumidos/expostos

Os do AYD-006: § 3.1–3.9 (convenções), § 4.1 (linhas 3, 9–12, 15–20, 24–27, 29), § 4.2 (tipos
de saída), § 4.3 (entradas/saídas por ferramenta), § 5 (EN↔PT) e § 7 (auditoria; `result` só nas
`dd`). Consome as rotas REST existentes (AYD-001…005) via Gateway; não cria rota nova. Tabela
`mcp_tool_calls` e seus índices vêm da migração 009 (SPEC-006) — **esta SPEC não cria migração**.

Mapa ferramenta → rota REST (do § 4.1):

| Ferramenta | Anot. | Rota(s) REST |
|---|---|---|
| `create_customer` | W·dd | `POST /clientes` |
| `create_quote` | W·dd | `POST /quotes` |
| `set_quote_status` | W·I | `PATCH /quotes/{id}/status` |
| `convert_quote_to_sale` | D | `POST /quotes/{id}/convert` + `GET /vendas/{id}` |
| `create_quick_sale` | W·dd | `POST /quotes/quick-sale` + `GET /vendas/{id}` |
| `update_sale` | W·I | `GET` + `PUT /vendas/{id}` |
| `update_sale_item` | W·I | `GET` + `PUT /vendas/{id}/itens/{itemId}` |
| `set_sale_delivery_status` | W·I | `PATCH /vendas/{id}/delivery-status` |
| `cancel_sale` | D·I | `PATCH /vendas/{id}/delivery-status` (`CANCELED`) |
| `record_receipt` | W·dd | `POST /vendas/{id}/pagamentos` + `GET /vendas/{id}` |
| `confirm_receipt` | W·I | `POST /vendas/{id}/pagamentos/{pagId}/confirmar` + `GET /vendas/{id}` |
| `create_purchase` | W·dd | `POST /purchases` |
| `add_sale_items_to_purchase` | W | `POST /purchases/{id}/sale-items` |
| `set_purchase_status` | W·I | `PATCH /purchases/{id}/status` |
| `record_supplier_payment` | W·I | `PATCH /purchases/{id}/payment` |
| `create_stock_adjustment` | W·dd | `POST /stock/adjustments` + `GET /stock/balance/{productId}` |

## Abordagem (como)

Premissas sobre o SPEC-006 (a confirmar na implementação; se divergirem, ajustar aqui **sem**
tocar o contrato): `tool.Define[In, Out](Meta, handler)` com `Meta{Name, Title, Description, Scope,
ReadOnly, Destructive, Idempotent, Dedupe}`; `registry.Catalog()` concatena módulos; o pipeline
(`scope → rate limit → dedupe → handler → erro → auditoria`) já existe com o passo de dedupe e a
checagem de scope como pontos de extensão; o Gateway tem `Do(ctx, method, path, query, body, out)`
com erro tipado `{Status, Message}`; o mapeamento de erros HTTP → `code` já cobre 400/404/409/422/5xx.

- **Escritas por módulo**, nos mesmos arquivos de `internal/mcp/tools/` do SPEC-006
  (`catalog.go`, `crm.go`, `sales.go`, `procurement.go`, `stock.go`, `financial.go`): cada módulo
  ganha as suas escritas em `Tools()`. O registro central não muda (só concatena).
- **Metadados:** `Scope: write`, `ReadOnly: false`, `Destructive`/`Idempotent`/`Dedupe` conforme
  a tabela acima. Títulos e descrições em pt-BR seguindo § 3.1 (o quê, quando, pré-requisitos,
  o que não faz; termo GLO em inglês + português na 1ª menção). `create_quote` orienta a preencher
  `product_name`/`unit_price`/`unit_cost` a partir de `search_products` (RN-02).
- **Schemas** derivados das structs Go (tags), `additionalProperties: false`; escritas só recebem
  IDs (§ 3.2); dinheiro ≥ 0 com 2 casas, quantidade > 0 com 3 casas, datas `YYYY-MM-DD` em
  America/Sao_Paulo ("hoje" = data de São Paulo). Ferramentas `dd` ganham `allow_duplicate?: bool`.
- **Saída:** recurso resultante + `warnings: string[]` (ex.: `INSUFFICIENT_STOCK` vindo da API);
  campos calculados lidos da API, nunca recalculados (RNF-05).
- **Adaptadores EN↔PT** (§ 5) por módulo, em funções de mapeamento puras (`toLegacySalePut`,
  `fromLegacySale`, …), testáveis sem banco. Enums de entrada traduzidos (`PENDING/RECEIVED`↔
  `pendente/recebido` quando aplicável; `payment_method` passa igual).
- **Atualização parcial** (`update_sale`, `update_sale_item`): `GET` do registro, aplica só os
  campos enviados sobre a representação legada, `PUT` completo. `null` explícito só onde o AYD
  permite (`expected_delivery_date`, `notes`, `supplier_id`). Exige ≥ 1 campo além do ID, senão
  `INVALID_INPUT`. Regras (409 de Item em Purchase não aberta, validação de Freight) permanecem
  na API.
- **Escritas compostas:** `convert_quote_to_sale` e `create_quick_sale` leem o `GET /vendas/{id}`
  da Sale criada para devolver `SaleDetail`; `record_receipt`/`confirm_receipt` releem a Sale para
  devolver `sale` com o novo `receivable`; `create_stock_adjustment` lê o `balance` do Product.
  Falha na leitura **posterior** não desfaz a escrita: devolve `INTERNAL` com `call_id`, e o
  recurso gravado fica recuperável pelas ferramentas de leitura (a escrita já foi auditada).
- **`record_receipt`:** com `paid_at` exige `payment_method` (AYD-004) — validação feita pela
  API; sem `paid_at` nasce `PENDING`.
- **`set_sale_delivery_status`** aceita só `TO_DELIVER|DELIVERED` (schema); `cancel_sale` envia
  `CANCELED` pela mesma rota (RN-15 aplicada pela API).
- **Deduplicação** (`internal/mcp/dedupe.go`, passo do pipeline): chave = (`connection_id`, `tool`,
  `arguments_hash`) com hash SHA-256 do **JSON canônico** (chaves ordenadas) dos argumentos
  **sem** `allow_duplicate`. Consulta `mcp_tool_calls` por registro `outcome = OK`, mesma chave,
  `created_at >= now() - 120 s`, `result` não nulo; se achar, devolve o `result` original com
  `"deduplicated": true` **sem** chamar o Gateway (e audita a chamada como `OK`, sem `result`).
  Com `allow_duplicate: true` pula a consulta. Só atua em ferramentas `Dedupe`. Usa o índice
  (`connection_id`, `tool`, `arguments_hash`, `created_at`) da 009. Corrida entre duas chamadas
  simultâneas idênticas é aceita como risco residual na v1 (não há lock); documentado no código.
  A auditoria grava `result` apenas nas `dd` (§ 7) para a dedupe funcionar.
- **Scope `write`:** `tools/list` filtra por scopes da Connection; `tools/call` de ferramenta de
  escrita sem `write` → `FORBIDDEN` ("esta conexão não tem permissão de escrita") antes de rate
  limit, dedupe e Gateway. Comportamento já provido pelo pipeline do SPEC-006; aqui é verificado
  com as 16 ferramentas reais.
- **Golden:** `testdata/catalog.golden.json` (criado no SPEC-006 com as 18 R) é regenerado com as
  34; o `catalog_test.go` passa a validar também verbo × `readOnlyHint` (verbos de escrita
  `create_/record_/update_/set_/add_/convert_/confirm_/cancel_`), `title`, `additionalProperties:
  false`, `allow_duplicate` nas `dd` (e só nelas), `destructiveHint`/`idempotentHint` presentes nas
  escritas, e contagem exata 34 com `catalog_version` `1.0.0` em `serverInfo.version`.

## Passos de implementação

1. Confirmar com o código do SPEC-006 os pontos de extensão (Define, Meta, pipeline, Gateway,
   mapeamento de erros); registrar divergências na seção "Desvios / decisões" ao final.
2. Dedupe: `dedupe.go` (JSON canônico, hash, consulta, `deduplicated`), integrar ao pipeline e à
   auditoria (`result` só nas `dd`); testes unitários da canonização e de integração da janela.
3. Módulo Catalog/CRM: `create_customer`, `create_quote`, `set_quote_status`,
   `convert_quote_to_sale`, `create_quick_sale` (+ adaptadores e releitura da Sale).
4. Módulo Sales: `update_sale`, `update_sale_item` (atualização parcial), `set_sale_delivery_status`,
   `cancel_sale`.
5. Módulo Financial: `record_receipt`, `confirm_receipt`.
6. Módulo Procurement: `create_purchase`, `add_sale_items_to_purchase`, `set_purchase_status`,
   `record_supplier_payment`.
7. Módulo Stock: `create_stock_adjustment`.
8. Descrições pt-BR revisadas contra § 3.1 (≤ ~800 caracteres, sem instruções ocultas).
9. Ampliar `catalog_test.go` (convenções de escrita) e regenerar o golden com as 34 ferramentas.
10. Testes de integração das escritas ponta a ponta (`/mcp` real, banco descartável).
11. `docs/changelog.md` (linha em inglês), `CLAUDE.md` do repo (fases de implementação e estrutura
    `internal/mcp`), swagger sem mudança (nenhuma rota REST nova).

## Arquivos / módulos afetados

- `internal/mcp/dedupe.go` (novo), `internal/mcp/pipeline.go` (integração do passo de dedupe e do
  `result` na auditoria)
- `internal/mcp/tools/{catalog,crm,sales,procurement,stock,financial}.go` (escritas, structs
  In/Out, adaptadores)
- `internal/mcp/catalog_test.go`, `internal/mcp/testdata/catalog.golden.json`
- `docs/changelog.md`, `CLAUDE.md`
- **Sem** migração nova, **sem** alteração de handlers REST.

## Testes (ver docs/conventions.md)

- **Aceite (mapeia os critérios acima):**
  - Catálogo como contrato → `catalog_test.go` (golden + convenções; falha ao alterar o snapshot
    sem regenerar) e `tools/list` em integração (34 ferramentas, 1.0.0).
  - Anotações de risco → teste de tabela sobre `Catalog()` com as 16 escritas esperadas.
  - Paridade de regra → integração: Sale cancelada + `record_receipt` → `CONFLICT` com a mensagem
    literal.
  - Deduplicação → integração: duas chamadas idênticas (1 Receipt, `deduplicated: true`);
    `allow_duplicate` (2 Receipts); erro anterior; argumentos/ferramenta/Connection diferentes;
    > 120 s (relógio injetado); ordem de chaves diferente.
  - Atualização parcial → integração: Sale com `frete_valor` 300 e `observacoes` "x" +
    `update_sale {freight_paid: 300}`.
  - Scope de leitura → integração: Connection `read` não lista nem executa escritas; Gateway não
    é invocado (mock do Gateway na fronteira).
  - Auditoria → integração: registro com `tool`, `outcome`, `duration_ms`; `result` só nas `dd`.
- **Unit/integração:**
  - Adaptadores EN↔PT por entidade (Sale, SaleItem, Receipt, Customer, Quote) — testes de tabela.
  - JSON canônico e hash estável; remoção de `allow_duplicate` do hash.
  - Por ferramenta: caminho feliz + 1 erro de regra da API propagado (`INVALID_INPUT`, `NOT_FOUND`,
    `CONFLICT`, `UNPROCESSABLE`) com a mensagem literal da API.
  - Fluxos: `create_quote` → `convert_quote_to_sale` (Sale + Items); `create_quick_sale`;
    `create_purchase` MADE_TO_ORDER → `set_purchase_status` → `record_supplier_payment`;
    `create_stock_adjustment` devolve `balance`; `INSUFFICIENT_STOCK` em `warnings` de
    `update_sale_item` para `FROM_STOCK`.
  - Validação de schema: `additionalProperties`, `uuid`, ≥ 1 campo em `update_*`.

## Casos de borda & fora de escopo

- Borda: falha na releitura pós-escrita (ver Abordagem); corrida de duas chamadas `dd` simultâneas
  (risco residual aceito); `update_*` com `null` apenas nos campos permitidos; `paid_at` sem
  `payment_method` (400 da API).
- Fora: ferramentas de exclusão e desfazer pagamento ao Supplier (só no app web); edição de
  cadastros, Items novos em Sale existente, Partner/Supplier/Product novos pelo chat; ferramentas
  de leitura, OAuth, plataforma `/mcp` e migração 009 (SPEC-006@api); telas (SPEC-006@web).

## Desvios / decisões

Nenhum desvio do contrato (AYD-006 §§ 3–5). Decisões de implementação, todas dentro do contrato:

- **Pontos de extensão do SPEC-006 confirmados:** `toolkit.Define`, `Meta`, `Gateway.Do` (inclui `PATCH`), `PipelineOptions.Deduper` e o scope `write` já existiam; o pipeline não mudou (o passo de dedupe e o `result` só nas `dd` já estavam lá). Novo: `internal/mcp/dedupe.go` (`DBDeduper`, relógio injetável) ligado em `cmd/server/main.go`.
- **`toolkit.Define` ganhou opções de schema** (`MinItems`, `OutConst`, `NotZero`) para o que as tags não expressam: `items`/`sale_item_ids` ≥ 1, `cancel_sale.delivery_status` = `"CANCELED"`, `record_supplier_payment.payment_status` = `"PAID"`, `quantity ≠ 0`.
- **`null` explícito** (§ 3.2) só em `update_sale.expected_delivery_date`/`notes` e `update_sale_item.supplier_id`, via `toolkit.Nullable[T]` (`anyOf [T, null]`); o teste do catálogo permite `null` apenas nesses três campos.
- **Casas decimais** (dinheiro 2, quantidade 3) são conferidas na ferramenta (`INVALID_INPUT`); o schema só carrega os limites (≥ 0 / > 0), pois `multipleOf` em ponto flutuante é instável.
- **`update_sale_item`:** `custo_unit` do PUT é o atual, salvo `unit_cost` informado; se a `origin` muda e não vem custo, `custo_unit` é omitido para a API aplicar a RN-16 (custo da última entrada). Ao trocar `supplier_id`, o `fornecedor_nome` do item acompanha o Supplier (lido por `GET /fornecedores/{id}`); `null` limpa id e nome.
- **`record_supplier_payment`:** `to_pay` é o devolvido pela API (um pagamento `PAID` quita o acerto da Purchase, mesmo com `paid_amount` menor que o custo — regra da API, não do conector).
- **Falha na releitura pós-escrita** vira `INTERNAL` com `call_id` (erro opaco `afterWrite`); em ferramentas `dd` a chamada fica auditada como `ERROR` (sem `result`), então uma nova tentativa do modelo não é deduplicada.
- **Testes:** integração em `tools_write_integration_test.go` e `e2e_integration_test.go` (Connection `read` e `write`), unitários em `tools_test.go`, `pipeline_test.go` e `catalog_test.go` (golden + convenções de escrita + anotações da § 4.1).

## Checklist de entrega

- [x] SPEC-006@api entregue e mergeado (dependência)
- [x] 16 ferramentas de escrita registradas nos módulos, com anotações da tabela do AYD § 4.1
- [x] `allow_duplicate` só nas 6 ferramentas `dd`; dedupe de 120 s com JSON canônico e `deduplicated: true`
- [x] `result` auditado só nas `dd`; chamadas deduplicadas também auditadas
- [x] Scope `write` enforced em `tools/list` e `tools/call` (`FORBIDDEN`)
- [x] `update_sale`/`update_sale_item` parciais (GET + PUT completo)
- [x] Golden regenerado com 34 ferramentas, `catalog_version` 1.0.0; convenções validadas
- [x] Testes unitários e de integração cobrindo cada critério de aceite
- [x] Changelog e `CLAUDE.md` atualizados; nenhuma rota REST nem migração alteradas

## Notas de integração com a SPEC-006 (implementada)
- Funções de módulo por domínio em `internal/mcp/tools` (`CatalogTools()`, `SalesTools()`, …), concatenadas em `registry.go`; núcleo em `internal/mcp/toolkit`.
- Deduplicação: implementar `mcp.Deduper` (`Lookup(ctx, connID, tool, argsHash)`) e passar em `PipelineOptions.Deduper`; o pipeline já o chama.
- Scope `write` já é exigido pelo pipeline para `ReadOnly:false`.
- `CatalogVersion` agora é `1.0.0` (34 ferramentas).
