---
id: SPEC-006
type: spec
title: Assistant connector — OAuth, plataforma MCP e ferramentas de leitura (api)
status: approved
updated: 2026-11-18
parents: [AYD-006@context]
children: [SPEC-007]
related: [ADR-002@context, ADR-001@context, AYD-002@context, AYD-004@context, AYD-005@context, RNF-07@context, GLO]
---

# Spec: Assistant connector — plataforma (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-006** na parte de **plataforma**: servidor OAuth 2.1,
> endpoint `/mcp`, arquitetura de extensão e as ferramentas **`readOnly`** do catálogo § 4.
> Contratos (rotas, campos, erros, catálogo, instruções) vêm do AYD-006 e do ADR-002 e **não
> são redefinidos aqui**; as leituras de pontos ambíguos estão em "Desvios / decisões".
> As **ferramentas de escrita, a deduplicação (§ 3.7)** e o catálogo completo 1.0.0 (34
> ferramentas) são da **SPEC-007@api**, que se apoia nos pontos de extensão deste documento.

## Objetivo

Entregar o **Assistant connector** (RF-37, RNF-07) sem tocar em regra de negócio:

- migração **009** com todas as tabelas técnicas do AYD (`oauth_*`, `mcp_tool_calls`);
- **servidor de autorização OAuth 2.1** (§ 2.1–2.7): discovery, DCR, authorize (delegando o
  login ao app web), API de consentimento, token com refresh rotativo, detecção de reuso e
  janela de 30 s, revogação e API de Connections;
- **`/mcp`** (§ 1): MCP Streamable HTTP *stateless* sobre o SDK oficial, Bearer com recheck da
  allowlist a cada chamada, Origin, 405, rate limit, teto de saída, instruções (§ 6);
- **auditoria** (§ 7) com retenção de 90 dias;
- **arquitetura de extensão**: registro por módulo de domínio, Gateway em processo para os
  handlers `/api/v1`, adaptadores EN ↔ PT, erros (§ 3.5), paginação (§ 3.4) e scopes (§ 3.8) num
  **pipeline único**;
- as **18 ferramentas `readOnly`** do § 4 e o **teste golden** do catálogo registrado.

## Critérios de aceite

```gherkin
Cenário: conexão completa
  Dado MCP_ENABLED=true e o dono na allowlist
  Quando o Claude chama POST /mcp sem token
  Então recebe 401 com WWW-Authenticate contendo resource_metadata (e scope="read write")
  E após DCR, authorize, consentimento aprovado no web e troca do code com PKCE válido
  Então recebe access_token (expires_in 3600) e refresh_token
  E tools/list devolve todas as ferramentas registradas (nesta SPEC, as 18 readOnly; 34 com a SPEC-007)

Cenário: PKCE inválido
  Quando o token é pedido com code_verifier que não confere com o code_challenge
  Então 400 invalid_grant e nenhum token é emitido

Cenário: redirect não permitido no DCR
  Quando POST /oauth/register com redirect_uris ["https://evil.example/cb"]
  Então 400 invalid_redirect_uri

Cenário: consentimento por conta fora da allowlist
  Quando outra conta aprova o pedido em /conectar
  Então a API responde 403 (middleware Firebase + allowlist) e nenhuma Connection é criada

Cenário: consentimento com corpo vazio ou {}
  Quando o web faz POST …/approve ou …/deny com corpo vazio ou {}
  Então 200 { redirect_to } (o corpo é ignorado, nunca 400)

Cenário: rotação e reuso de refresh token
  Dado um refresh_token R1 usado para obter R2
  Quando R1 é reapresentado em até 30 s
  Então 400 invalid_grant sem revogar nada (R2 segue válido)
  E quando R1 é reapresentado após 30 s: 400 invalid_grant e a Connection é revogada (R2 também deixa de valer)

Cenário: reuso de code
  Quando um code já trocado é apresentado de novo
  Então 400 invalid_grant e a Connection emitida por ele é revogada

Cenário: revogação pela tela Integrações
  Quando o dono desconecta a Connection (DELETE /api/v1/oauth/connections/{id})
  Então a próxima chamada /mcp com o access token dela recebe 401

Cenário: allowlist rechecada
  Quando o e-mail do dono sai de ALLOWED_EMAILS
  Então a próxima chamada /mcp dá 401 e a Connection fica revogada (ALLOWLIST)

Cenário: erros HTTP do /mcp
  Então GET e DELETE dão 405; Origin fora de claude.ai/claude.com/WEB_APP_URL dá 403; sem Origin segue

Cenário: scope de leitura
  Dada uma Connection só com scope read
  Então tools/list não contém ferramentas de escrita; chamar uma ferramenta fora dos scopes devolve FORBIDDEN
  (pipeline pronto para a SPEC-007: ferramenta com ReadOnly=false exige "write")

Cenário: paridade de regra (leituras)
  Então cada ferramenta lê pelas rotas REST (mesmos handlers); números calculados vêm da API
  E get_finance_summary / get_dashboard_kpis coincidem com /finance/summary e /dashboard/kpis
  E erros da API viram isError com code/message literais (NOT_FOUND "venda não encontrada")

Cenário: auditoria
  Quando qualquer ferramenta é chamada (sucesso, erro de API, erro de schema, FORBIDDEN, RATE_LIMITED)
  Então existe um registro em mcp_tool_calls com tool, outcome e duration_ms (result só nas dedupe)

Cenário: catálogo como contrato
  Quando uma ferramenta muda nome, schema ou anotação sem atualizar o snapshot golden
  Então o CI da api falha

Cenário: MCP desligado
  Dado MCP_ENABLED ausente ou false
  Então /mcp, /oauth/* e /.well-known/* não existem (404) e nada de PUBLIC_BASE_URL/WEB_APP_URL é exigido
  E com MCP_ENABLED=true e PUBLIC_BASE_URL/WEB_APP_URL ausentes ou inválidas a api não sobe
```

## Contratos consumidos/expostos

**Expostos** (definidos no AYD-006, implementados aqui):

- Raiz (sem Firebase): `GET /.well-known/oauth-protected-resource[/mcp]`,
  `GET /.well-known/oauth-authorization-server`, `POST /oauth/register`, `GET /oauth/authorize`,
  `POST /oauth/token`, `POST /oauth/revoke`, `POST|GET|DELETE /mcp` (§ 1, § 2.1–2.3, 2.5, 2.6).
- `/api/v1` (Firebase + allowlist): `GET /oauth/authorization-requests/{id}`,
  `POST …/{id}/approve`, `POST …/{id}/deny`, `GET /oauth/connections`,
  `DELETE /oauth/connections/{id}` (§ 2.4, § 2.7).
- Variáveis: `MCP_ENABLED`, `PUBLIC_BASE_URL`, `WEB_APP_URL`, `MCP_ALLOWED_REDIRECT_URIS`.
- Catálogo (§ 4): as 18 ferramentas `readOnly` — `search_customers`, `get_customer`,
  `search_products`, `search_suppliers`, `search_partners`, `list_quotes`, `get_quote`,
  `list_sales`, `get_sale`, `list_pending_purchase_items`, `list_purchases`, `get_purchase`,
  `get_stock_balance`, `get_finance_summary`, `list_payment_alerts`, `get_dashboard_kpis`,
  `get_top_products`, `get_monthly_sales`.

**Consumidos:** as rotas REST existentes (AYD-001…005) via Gateway. Mapa leitura → rota:

| Ferramenta | Rota(s) REST |
|---|---|
| `search_customers` | `GET /clientes` (filtro de nome no adaptador) |
| `get_customer` | `GET /clientes/{id}` + `GET /vendas?cliente_id` |
| `search_products` / `search_suppliers` / `search_partners` | `GET /produtos`, `GET /fornecedores`, `GET /partners` |
| `list_quotes` / `get_quote` | `GET /quotes`, `GET /quotes/{id}` |
| `list_sales` / `get_sale` | `GET /vendas` (`data_inicio`/`data_fim`), `GET /vendas/{id}` |
| `list_pending_purchase_items` | `GET /purchases/pending-sale-items` |
| `list_purchases` / `get_purchase` | `GET /purchases`, `GET /purchases/{id}` |
| `get_stock_balance` | `GET /stock/balance[/{productId}]` |
| `get_finance_summary` / `get_dashboard_kpis` | `GET /finance/summary`, `GET /dashboard/kpis` |
| `list_payment_alerts` | `GET /dashboard/notificacoes` |
| `get_top_products` / `get_monthly_sales` | `GET /relatorios/produtos`, `GET /relatorios/vendas` |

**Mudança aditiva em rota REST existente** (necessária para o contrato do AYD, sem quebrar nada):
`GET /dashboard/notificacoes` passa a devolver `pagamento_id` (→ `receipt_id`) e aceita
`only_unread=false` (inclui os lidos; padrão segue só os não lidos). Ver "Desvios / decisões".

## Abordagem (como)

### Estrutura (`internal/`)

```
oauth/                    servidor de autorização (config, store SQL, discovery, register, authorize,
                          consent, token, revoke, connections, verifier, cleanup)
mcp/
  server.go               NewHandler: Origin → método → Bearer → SDK (stateless, JSONResponse)
  pipeline.go             scope → rate limit → validação → dedupe(*) → handler → erro → auditoria
  audit.go ratelimit.go   DBAuditor (mcp_tool_calls + retenção 90 d); janela deslizante 120/min
  registry.go             Catalog() = concatena os módulos; CatalogVersion
  instructions.go         texto canônico do § 6
  toolkit/                Meta, Tool, Define[In,Out], Gateway, APIError/ToolError, Page, enums, schemas
  gateway/                Gateway em processo sobre o ServeMux de /api/v1 (recusa /oauth/*)
  tools/                  catalog.go crm.go sales.go procurement.go stock.go financial.go analytics.go
  testdata/catalog.golden.json
testdb/                   banco descartável por teste (CREATE DATABASE + migrations) p/ integração
```

(\*) o estágio de dedupe é **ponto de extensão** (interface `Deduper`), implementado na SPEC-007.
A estrutura difere da recomendada pelo AYD só no pacote `toolkit` (o `tool.go` do AYD), necessário
para evitar ciclo de importação entre `mcp` (registro) e `mcp/tools` (módulos).

### Pontos de extensão para a SPEC-007 (escrita)

1. **Scope `write`** — o pipeline compara `Meta.Scope()` (`read` se `ReadOnly`, senão `write`) com
   os scopes da Connection **antes** de rate limit, dedupe e handler; falha → `FORBIDDEN`
   ("esta conexão não tem permissão de escrita"), auditada, sem chegar ao Gateway. `tools/list`
   filtra pelo mesmo critério. Declarar uma ferramenta com `ReadOnly: false` já a submete a isso.
2. **Dedupe de criação (§ 3.7)** — `Meta.Dedupe` + interface `mcp.Deduper.Lookup(ctx, connectionID,
   tool, argsHash) (result, found, err)` em `PipelineOptions.Deduper`. O pipeline chama o `Deduper`
   só para `Dedupe && !allow_duplicate`; achando, devolve o `result` com `"deduplicated": true`
   sem chamar o Gateway e audita `OK` sem `result`. Registrar ferramenta `Dedupe` sem `Deduper` é
   **erro de montagem** (`NewPipeline` falha). `argsHash` já é o SHA-256 do JSON canônico **sem
   `allow_duplicate`**. A SPEC-007 implementa o `Deduper` (consulta em `mcp_tool_calls`, janela de
   120 s, `outcome = OK`, `result` não nulo), injeta em `main.go` e adiciona o `allow_duplicate` aos
   inputs (o teste de convenções já exige).
3. **Auditoria** — `AuditEntry.Result` só é preenchido para `Meta.Dedupe` e só quando a criação
   ocorreu (§ 7): é o insumo da dedupe.
4. **Módulos** — cada arquivo de `tools/` expõe `XxxTools()`; `registry.go` só concatena. Uma
   ferramenta nova = `toolkit.Define[In, Out](Meta{…}, handler)` no módulo + regenerar o golden.
5. **Erros** — `*toolkit.APIError` (do Gateway) e `*toolkit.ToolError` devolvidos pelo handler são
   mapeados pelo pipeline (§ 3.5); 5xx/inesperado → `INTERNAL` com `ref <call_id>` e log interno.

### OAuth

- **Armazenamento:** tokens e codes só como **SHA-256 hex**; `client_id`, code e tokens são 32
  bytes aleatórios em base64url (`crypto/rand`). SQL explícito via GORM (`Raw`/`Exec`); `TEXT[]`
  por `oauth.StringArray` (Valuer/Scanner, `GormDataType`).
- **authorize:** duas camadas (§ 2.3): `client_id` desconhecido/`redirect_uri` não registrado → 400
  texto simples, nunca redireciona; demais → 302 com `error`+`state`. `redirect_uri` casa por
  igualdade exata (exceção RFC 8252: `http` em loopback ignora a porta). `scope` normalizado para
  `read`/`write` (`offline_access` aceito e descartado; ausente = `read write`).
- **Consentimento:** `approve`/`deny` **ignoram o corpo** (vazio, `{}` ou qualquer coisa), usam
  `SELECT … FOR UPDATE` no pedido; `approve` cria a Connection (`owner_uid`/`owner_email` do token
  Firebase, agora também no contexto — `auth.UserEmail`) e o code (5 min, uso único).
- **token:** transação única por chamada; efeitos de revogação por reuso (code ou refresh fora da
  janela de 30 s) são **commitados** antes de responder `invalid_grant`. PKCE S256 em tempo
  constante. Refresh: rotação (`rotated_at`), janela deslizante de 30 dias, `scope` só reduz.
- **Verificador (`/mcp`):** hash → token `ACCESS` válido + Connection ativa + `resource` ==
  `{PUBLIC_BASE_URL}/mcp` + `Allowlist.AllowsEmail(owner_email)` (senão revoga com `ALLOWLIST`);
  `last_used_at` no máximo 1×/min.
- **Limpeza oportunista** (`MaybeCleanup`, ≤ 1×/h por instância, em `register`/`token`): clientes sem
  Connection > 24 h, pedidos vencidos > 1 dia, tokens expirados > 7 dias, Connections sem token.

### `/mcp`

- SDK `github.com/modelcontextprotocol/go-sdk` **v1.8.0** (`mcp.NewStreamableHTTPHandler`,
  `Stateless: true`, `JSONResponse: true`). Um único `*mcp.Server` com **todas** as ferramentas
  (chamada fora dos scopes cai no pipeline e devolve `FORBIDDEN`, não erro de protocolo);
  `tools/list` é filtrado por *receiving middleware* pelos scopes da Connection.
- `initialize`: `serverInfo{rochatto, Rochatto, CatalogVersion}`, `capabilities.tools.listChanged
  = false`, `instructions` = § 6. Ferramentas registradas pela API de baixo nível (`Server.AddTool`)
  com schemas derivados dos tipos Go (`jsonschema-go`), para controlar validação, `isError` e
  `structuredContent` (o `AddTool` genérico do SDK devolve erros só como texto).
- Auth: o middleware próprio responde 401 com o `WWW-Authenticate` exato do § 1 e corpo
  `{"error":"unauthorized"}`; em seguida repassa o `TokenInfo` ao SDK (`auth.RequireBearerToken`).
- Limites: 120 `tools/call`/min por Connection (janela deslizante em memória), saída ≤ 100.000
  caracteres, 30 s por chamada.

### Ferramentas

`toolkit.Define[In, Out]` deriva `inputSchema`/`outputSchema` das structs (`json` + `jsonschema`),
com `additionalProperties:false`; opcionais são ponteiro/`omitempty` (omitidos, nunca `null`; o
schema não aceita `null`). Tipos nomeados (`UUID`, `Date`, `Limit`, enums…) carregam `format`,
`pattern`, `minimum/maximum` e `enum`. Listas paginam sobre a resposta completa da rota
(`toolkit.Paginate`), e filtros de nome sem acento/caixa (`fold`) rodam no adaptador. Saída com
`structuredContent` + o mesmo JSON em `content`. Adaptadores EN ↔ PT (§ 5) em `tools/types.go`
(`legacyCustomer`, `legacySale`, …). Descrições em pt-BR seguindo § 3.1.

### Desvios / decisões

1. **`list_payment_alerts`**: a rota não devolvia o Receipt do alerta nem os já lidos. Foram
   adicionados `pagamento_id` à resposta e `?only_unread=false` (aditivos; padrão intacto) em
   `GET /dashboard/notificacoes`, para cumprir `receipt_id` e `only_unread` do AYD.
2. **`get_purchase`**: os campos legados PT de `sale_items` são traduzidos no adaptador do conector
   (`produto_nome`→`product_name`, `quantidade`→`quantity`, `custo_unit`→`unit_cost`; AYD-006 §5,
   "Pending purchase item"); a rota REST não muda. `product_id` vem de `produto_id`, campo
   aditivo de `sale_items` na rota REST.
3. **`get_dashboard_kpis.quotes.conversion_rate`**: a rota devolve `null` sem Quotes no período;
   o schema do conector não aceita `null` (§ 3.2), então o campo fica **ausente**.
4. **`tokens.scopes` (coluna extra na 009)**: o AYD exige que o `scope` do refresh só reduza, mas
   `oauth_tokens` não tem onde guardar a redução. Coluna `scopes TEXT[]` **nullable** (NULL =
   scopes da Connection); o par seguinte herda a redução. Aditivo; não altera nenhum contrato.
5. **Connections sem tokens** (code aprovado e nunca trocado) **não aparecem** em
   `GET /oauth/connections` (conexão não ativada) e são apagadas pela limpeza.
6. **`PUBLIC_BASE_URL`** é validada estritamente (https, 443, sem barra/path). Desenvolvimento local
   precisa de um túnel https. `WEB_APP_URL` aceita `http` fora de produção (localhost).
7. **`CatalogVersion = "1.0.0-rc.1"`**: o catálogo registrado aqui é parcial (18 de 34); a SPEC-007
   o torna `1.0.0`. Com `MCP_ENABLED=false` (padrão) nada disso é exposto.
8. **`list_purchases.status[]`**: a rota aceita um status; com vários, o adaptador filtra. Nos
   filtros de lista da `list_sales`, `from`/`to` mapeiam para `data_inicio`/`data_fim` (tabela § 5).
9. **Resultado acima de 100.000 caracteres** vira `INVALID_INPUT` ("refine os filtros…") — o § 3.5
   não tem código específico; é o mais conservador (a paginação evita o caso na prática).
10. **Erro de schema do SDK de baixo nível**: validação de entrada é do pipeline (`INVALID_INPUT`
    "argumentos inválidos: …" com o texto do validador); mensagens do validador não são pt-BR.
11. **PKCE inválido não queima o code** (o mesmo code ainda vale com o verifier correto); só
    reuso de code já trocado revoga a Connection.
12. **Dependências:** `go-sdk v1.8.0` e `jsonschema-go` (diretas); `golang-jwt` subiu para v5.3.1
    (exigência transitiva do SDK).

## Passos de implementação

1. Migração 009 (+ teste de ciclo down → up e constraints).
2. `auth`: e-mail no contexto, `AllowsEmail`, `WithUser`.
3. `oauth`: config, store, discovery, DCR, authorize, consentimento, token, revoke, Connections,
   verificador, limpeza; testes unitários e de integração.
4. `mcp/toolkit`, `mcp/gateway`, pipeline, auditoria, rate limit, servidor `/mcp`, registro.
5. Módulos de leitura (Catalog, CRM, Sales, Procurement, Stock, Financial, Analytics) + REST aditivo.
6. Golden + testes de convenção; integração das ferramentas; E2E com o cliente do SDK.
7. Wiring em `cmd/server` (só com `MCP_ENABLED=true`); `.env.example`, swagger, CLAUDE.md, changelog.

## Arquivos / módulos afetados

- `migrations/009_assistant_connector.{up,down}.sql`
- `internal/oauth/*`, `internal/mcp/**`, `internal/testdb/testdb.go`
- `internal/auth/{middleware,allowlist}.go`, `internal/handlers/dashboard.go` (aditivo)
- `cmd/server/main.go`, `go.mod`/`go.sum`, `.env.example`
- `docs/swagger.yaml`, `docs/changelog.md`, `CLAUDE.md`

## Testes (ver docs/conventions.md)

- **Aceite (mapeia os critérios):**
  - `internal/mcp/e2e_integration_test.go` — conexão completa com o **cliente MCP do SDK** (DCR →
    authorize → consentimento → token → initialize/tools/list/tools/call), 401, revogação, allowlist,
    scope só-`write` (sem leitura + `FORBIDDEN`), auditoria.
  - `internal/oauth/oauth_integration_test.go` — discovery, DCR (+ teto de 50 e limpeza), authorize
    (as duas camadas), consentimento (corpo vazio/`{}`, 404/409, expiração), token (PKCE, reuso de
    code, expiração do code), refresh (rotação, janela de 30 s, reuso, escopo reduzido, janela
    deslizante de 30 dias), revogação, Connections, allowlist, binding de `resource`, limpeza.
  - `internal/mcp/server_test.go` — 401/`WWW-Authenticate`, 405, Origin, `serverInfo`/instructions/
    `listChanged`, stateless, `tools/list` por scope, `tools/call`.
  - `internal/mcp/catalog_test.go` — **golden** + convenções § 3 (nome, verbo × `readOnlyHint`,
    descrição, `additionalProperties:false`, sem `null`, paginação, `allow_duplicate`, `warnings`).
- **Unit/integração:**
  - `internal/mcp/pipeline_test.go` (scope, rate limit, erros, panic, teto de saída, extensão de
    dedupe, hash canônico), `internal/mcp/toolkit/toolkit_test.go`, `internal/mcp/gateway/gateway_test.go`,
    `internal/mcp/tools/tools_test.go`, `internal/oauth/config_test.go`, `internal/auth/allowlist_test.go`.
  - `internal/mcp/tools/tools_integration_test.go` — as 18 ferramentas contra o REST real (saída
    validada contra o `outputSchema`, paridade com `/finance/summary` e `/dashboard/kpis`, erros,
    auditoria); `internal/dbmigrate/migration009_integration_test.go`;
    `internal/handlers/notifications_connector_integration_test.go`.
  - Integração roda com `TEST_DATABASE_URL` e cria um banco descartável por teste (`testdb`).

## Casos de borda & fora de escopo

- Fora: ferramentas de escrita e dedupe (**SPEC-007**); tela `/conectar` e `/integracoes` (**SPEC-006@web**);
  CIMD, prompts/resources MCP, chat no app, escopos < `read write` na v1 (o pipeline já os suporta).
- Borda: corrida entre duas instâncias no rate limit (limite é por instância, como no AYD); Cloud
  Run escalando a zero perde o contador (aceitável); a limpeza oportunista é *best effort*.

## Checklist de entrega

- [x] Migração 009 (up/down) testada
- [x] Servidor OAuth 2.1 (§ 2.1–2.7) com refresh rotativo, reuso e janela de 30 s
- [x] `/mcp` stateless com Bearer, allowlist rechecada, Origin, 405, rate limit, instruções
- [x] Auditoria + retenção de 90 dias
- [x] Pipeline único, Gateway em processo, adaptadores EN ↔ PT, erros e paginação padronizados
- [x] 18 ferramentas `readOnly` + golden + testes de convenção
- [x] Pontos de extensão (scope `write`, `Deduper`, `result` da auditoria) documentados e testados
- [x] `MCP_ENABLED=false` por padrão; falha de boot com configuração inválida
- [x] Swagger, `.env.example`, CLAUDE.md, changelog
