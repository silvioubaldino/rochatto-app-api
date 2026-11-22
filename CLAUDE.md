# CLAUDE.md — Backend: Sistema de Gestão de Vendas

## O que é este componente

API REST em **Go** responsável por toda a lógica de negócio, persistência de dados e validações do Sistema de Gestão de Vendas. É o único ponto de contato entre o frontend e o banco de dados.

## Objetivo

Fornecer endpoints HTTP seguros, validados e tipados para:
- Gerenciar o ciclo completo de vendas (criação, itens, status, entrega)
- Vender serviços (instalação, consultoria) ao lado das pedras: catálogo `/services` e Items `kind = SERVICE` no Quote/Sale, custo via custo da venda LABOR (SPEC-011)
- Compor a venda: preço ao cliente (Items + cobranças, ex.: frete cobrado) e custos da venda com credor (`/vendas/{id}/charges` e `/costs`, SPEC-010)
- Controlar o fluxo financeiro: contas a pagar/receber por credor (`/obligations`), parcelas, quitações (`/settlements`) e movimentações (`/financial-movements`) — substituem Receipts e o pagamento único da Purchase (SPEC-008)
- Manter cadastros de clientes, produtos, fornecedores e vendedores externos
- Expor o **Assistant connector** (RF-37): servidor MCP em `/mcp` e servidor OAuth 2.1 próprio, para o dono operar o sistema pelo Claude (AYD-006)
- Atender as vendas: origem do item (sob encomenda × de estoque), compras consolidadas por fornecedor (`/purchases`), saldo e ajustes de estoque (`/stock`), status de entrega da venda (SPEC-002)
- Fornecer dados agregados para o dashboard e alertas de pagamentos

## Stack

| Tecnologia | Versão | Papel |
|---|---|---|
| Go | 1.26 (ver `go.mod`) | Runtime e HTTP server |
| `net/http` stdlib | — | Router HTTP (`ServeMux` com padrões `METHOD /path/{id}`, sem framework) |
| GORM (`gorm.io/gorm` + `gorm.io/driver/postgres`) | 1.31 | Acesso a dados (modelos + SQL explícito via `Raw`) |
| `pgx/v5` | 5.x | Driver PostgreSQL (por baixo do GORM e do migrate) |
| `golang-jwt/jwt` | v5 | Validação de Firebase JWT |
| PostgreSQL | 16 (Neon) | Banco de dados principal |
| `golang-migrate` | 4.x | Migrations versionadas, **embutidas** no binário (TDR-001) |

## Arquitetura

```
Monolito simples — sem microserviços, sem message queues, sem cache, seguindo clean architecture.
```

```
cmd/server/                 → bootstrap: env, allowlist, migrations (opcional), DB, router, listen
cmd/migrate/                → CLI de migrations: up | down N | version | force N
internal/
  auth/                     → middleware: Firebase JWT + allowlist do dono (ALLOWED_EMAILS); CORS
  handlers/                 → um arquivo por recurso (clientes, vendas, quotes, purchases, stock, obligations, settlements, financial_movements, services etc.)
  fulfillment/              → regras puras do atendimento da Sale (transições, cancelamento — SPEC-002)
  purchasing/               → regras puras da composição da compra (SPEC-009): tipos/métodos, rateio (`Allocate`), unidades, rótulo
  saleitem/                 → regras puras do tipo de Item PRODUCT × SERVICE (coerência, valor do custo LABOR da conversão — SPEC-011)
  closing/ pendingactions/ search/ → regras puras da jornada do dono (SPEC-012): fechamento da venda (`closing`, erros com `field`), pendências do dia (kinds, janela, urgência) e busca (termo, tipos, limite)
  obligations/              → contas, parcelas, quitações e movimentações (SPEC-008): regras puras (`rules.go`: categoria × sinal, categoria padrão, geração/validação/cobertura de parcelas, alocação) + acesso a dados transacional (`store.go`, `movements.go`, `summary.go`)
  dbmigrate/                → golang-migrate sobre as migrations embutidas
  models/                   → structs GORM de domínio e tipos compartilhados
  oauth/                    → servidor de autorização OAuth 2.1 do Assistant connector (discovery, DCR, authorize, consentimento, token, revoke, Connections, verificador — SPEC-006)
  mcp/                      → servidor MCP `/mcp` (stateless): pipeline único (scope → rate limit → validação → dedupe → handler → erro → auditoria), registro por módulo, auditoria, `dedupe.go` (deduplicação de criações sobre `mcp_tool_calls` — SPEC-007)
    toolkit/                → Meta + Define[In,Out] (schemas derivados dos tipos), Gateway, erros, paginação
    gateway/                → Gateway em processo para os handlers /api/v1 (mesmas regras do app)
    tools/                  → ferramentas por domínio (catalog, crm, sales, procurement, stock, financial, analytics): leitura (SPEC-006) e escrita (SPEC-007) + adaptadores EN↔PT
  testdb/                   → banco Postgres descartável por teste de integração (TEST_DATABASE_URL)
migrations/                 → NNN_nome.{up,down}.sql + embed.go (package migrations)
docs/ops/                   → operação: baseline de migrations, backup, nota de remoção do pg_cron
```

## Convenções obrigatórias

### HTTP
- Prefixo de todas as rotas: `/api/v1`
- Autenticação: Bearer Token (Firebase JWT) em **todos** os endpoints
- Content-Type: `application/json` em todas as respostas
- Erros retornam `{"error": "mensagem legível"}` com status HTTP adequado

### Códigos de status
| Situação | Status |
|---|---|
| Sucesso com body | 200 OK |
| Criação bem-sucedida | 201 Created |
| Sem body | 204 No Content |
| Validação falhou | 400 Bad Request |
| Não autenticado | 401 Unauthorized |
| Autenticado, mas fora da allowlist do dono | 403 Forbidden |
| Não encontrado | 404 Not Found |
| Erro interno | 500 Internal Server Error |

### Banco de dados
- Todas as PKs são `UUID` geradas pelo PostgreSQL (`gen_random_uuid()`)
- Todas as entidades principais têm `created_at TIMESTAMPTZ` e `deleted_at TIMESTAMPTZ` (soft delete)
- **Nunca deletar fisicamente** registros de vendas, clientes, produtos ou pagamentos
- Campos calculados (total_venda, lucro, a_receber, payment_status, total_cost/paid/payable, balance, valor/quitado/em aberto/status das contas, cobertura das parcelas) **nunca são persistidos** — vêm das views `vw_vendas_resumo` (inclui `receivable`, `charges_total`, `costs_total`/`costs_paid`/`costs_open`; preço = Items + cobranças; custo com o rateio dos custos adicionais da compra; lucro = preço − custo − custos da venda), `vw_purchases_resumo`, `vw_purchase_line_costs` (rateio e custo de entrada), `vw_obligations`, `vw_installment_coverage`, `vw_movement_allocation` e `vw_stock_balance` (custo da última entrada = custo de entrada; `stock_value`). As contas são mantidas por **triggers** (`sp_sync_obligations`) a partir dos componentes (`vw_obligation_components`)
- Valores monetários: `DECIMAL(12,2)` no banco, `float64` no Go

### Código Go
- GORM para CRUD simples; agregações em SQL explícito (`Raw`) — somas por Sale sempre
  pré-agregadas (nunca juntar itens e recebimentos no mesmo `GROUP BY`: fan-out)
- Handlers não contêm lógica de negócio — delegam para funções em `internal/`
- Cada handler valida o input antes de chamar o banco
- Erros de banco são logados internamente mas nunca expostos ao cliente
- Variáveis de ambiente via `.env` local e variáveis do serviço no Cloud Run em produção

## Variáveis de ambiente

```env
DATABASE_URL=postgresql://user:pass@host:5432/dbname
FIREBASE_PROJECT_ID=seu-projeto-firebase
PORT=8080
ENV=development             # ou production
ALLOWED_EMAILS=dono@exemplo.com   # vírgulas; obrigatório em production (senão não sobe)
MIGRATE_ON_START=false      # true = aplica migrations pendentes no startup
FRONTEND_URL=http://localhost:3000  # origem permitida no CORS (default "*")

# Assistant connector (AYD-006) — MCP_ENABLED=false (padrão): /mcp, /oauth/* e /.well-known/* não são montados
MCP_ENABLED=false
PUBLIC_BASE_URL=https://api.rochatto.com.br   # obrigatória com MCP_ENABLED=true: https, porta 443, sem barra final (issuer; resource = {PUBLIC_BASE_URL}/mcp)
WEB_APP_URL=https://app.rochatto.com.br       # obrigatória com MCP_ENABLED=true: origem do app web (destino do consentimento)
MCP_ALLOWED_REDIRECT_URIS=https://claude.ai/api/mcp/auth_callback  # vírgulas; default = o valor ao lado
```

Com `MCP_ENABLED=true` e `PUBLIC_BASE_URL`/`WEB_APP_URL` ausentes ou inválidas a api **não sobe**. A URL
`{PUBLIC_BASE_URL}/mcp` cadastrada no Claude não pode mudar depois.

Testes de integração: `TEST_DATABASE_URL` (banco descartável com migrations aplicadas; os testes do conector — `oauth`, `mcp`, migração 009 — criam um banco próprio por teste e exigem permissão de `CREATE DATABASE`, senão são pulados).

Catálogo do Assistant connector (`catalog_version` **1.0.0** = `CatalogVersion` em `internal/mcp/registry.go`, publicada em `serverInfo.version`; 41 ferramentas: 22 de leitura + 19 de escrita; mudança de catálogo muda a versão — AYD-006 §3.9): `internal/mcp/testdata/catalog.golden.json` é contrato — mudou ferramenta,
schema ou anotação? Atualize o AYD-006 no repo de contexto e regenere com
`go test ./internal/mcp -run TestCatalogGolden -update` no mesmo PR.

## Como rodar localmente

```bash
# 1. Subir banco local
docker compose up -d db

# 2. Rodar migrations (embutidas)
go run ./cmd/migrate up

# 3. Rodar servidor
go run ./cmd/server

# 4. Testes (unitários; integração só com TEST_DATABASE_URL)
go vet ./... && TEST_DATABASE_URL=$DATABASE_URL go test ./...
```

## Fases de implementação

| Fase | Arquivo | Escopo |
|---|---|---|
| 1 | `specs/phase-01-foundation.md` | Setup, DB, JWT middleware, health check |
| 2 | `specs/phase-02-clients-catalogs.md` | CRUD: clientes, produtos, fornecedores, vendedores externos |
| 3 | `specs/phase-03-sales-core.md` | CRUD: vendas + itens de venda |
| 4 | `specs/phase-04-financial.md` | Pagamentos do cliente, pagamentos a fornecedores, view calculada |
| 5 | `specs/phase-05-dashboard.md` | Dashboard, notificações, relatórios e pg_cron |
| 6 | `docs/specs/SPEC-002-sale-fulfillment.md` | Atendimento da Sale: Purchase, Stock (Balance/ajustes), delivery/payment status (substitui o antigo módulo `/estoque`) |
| 7 | `docs/specs/SPEC-004-finance.md` | Financeiro: payment method e soft-delete do Receipt, saldos da Sale, `GET /finance/summary`, alertas sem pg_cron |
| 8 | `docs/specs/SPEC-005-dashboard.md` | Dashboard: `GET /dashboard/kpis` (ticket médio, conversão Quote → Sale, saldos), `quotes.quick_sale`, `/relatorios/vendas` com N meses exatos e `/relatorios/produtos` com `from`/`to` e `unit` |
| 9 | `docs/specs/SPEC-006-assistant-connector-platform.md` | Assistant connector: migração 009, OAuth 2.1, `/mcp`, pipeline/extensão e ferramentas de leitura |
| 10 | `docs/specs/SPEC-007-assistant-connector-write-tools.md` | Assistant connector: 16 ferramentas de escrita, deduplicação de criações (120 s, `allow_duplicate`), scope `write` e catálogo completo `1.0.0` (34 ferramentas) |
| 11 | `docs/specs/SPEC-008-obligations-and-movements.md` | Contas e movimentações (AYD-007): `fornecedores.kind`, Obligation/Installment/Settlement/Financial movement, migração 010 (substitui Receipts e `purchases.paid_*`), `/finance/summary` com cash/despesas/resultado, alertas a pagar, catálogo do connector `1.0.0` (36 ferramentas) |
| 12 | `docs/specs/SPEC-009-purchase-composition.md` | Composição da compra (AYD-008): `purchase_charges` (frete de compra, descarga, imposto, outros, com credor), `allocation_method`, rateio e custo de entrada (`vw_purchase_line_costs`), migração 011, `stock_value`, rateio no Profit dos Items sob encomenda, `add_purchase_charge` no connector (catálogo `1.0.0`, 37 ferramentas) |
| 13 | `docs/specs/SPEC-010-sale-composition.md` | Composição da venda (AYD-009): `quote_charges`/`sale_charges` (frete cobrado), `sale_costs` (frete pago, instalador, comissão, outro, com credor), rotas `/vendas/{id}/charges` e `/costs`, Quote → Sale copia cobranças, `vw_vendas_resumo` com preço/custos/Profit novos, migração 012 (remove `frete_*`/`comissao_*`), `set_sale_costs` e `add_sale_charge` no connector (catálogo `1.0.0`, 39 ferramentas) |
| 14 | `docs/specs/SPEC-011-services-on-sale.md` | Serviços na Sale (AYD-010): `services` (`/services`), Items `kind`/`service_id` em Quote/Sale (`origin` anulável), conversão cria custo da venda `LABOR` ligado ao Item, fora da fila de compra/Stock/ranking de Products, `GET /relatorios/servicos`, migração 013, `search_services` e `get_top_services` no connector (catálogo `1.0.0`, 41 ferramentas) |
| 15 | `docs/specs/SPEC-012-owner-journey-ux.md` | Jornada do dono (AYD-011): `number` humano em Quote/Sale/Purchase (migração 014, label das contas com o número), `sale_id`/`sale_number` no Quote e cabeçalho editável (`customer_id`/`quote_date`), `items_count`/`first_item_name` e filtros `q`/`referrer_id` nas listas, fechamento da venda (`closing`) em `/quotes/{id}/convert` e `/quotes/quick-sale` numa transação, `PATCH /vendas/{id}/itens/fulfillment` (lote), `stock_balance` no Item, fornecedor habitual do Product, `GET /search` e `GET /pending-actions` (catálogo do connector inalterado) |

## Deploy (Google Cloud Run — free tier)

- `Dockerfile` na raiz: binários `server` e `migrate` (migrations embutidas, tzdata incluído)
- Variáveis de ambiente configuradas no serviço do Cloud Run (`ENV=production`,
  `ALLOWED_EMAILS` obrigatório)
- Banco: Neon Postgres. Baseline de migrations, alertas (sem pg_cron) e backup: `docs/ops/database.md`
- CI: `.github/workflows/ci.yml` (vet, build, migrations e testes com Postgres 16)
- Scale-to-zero: aceita cold start (RNF-04)

## This repo's role (docs framework)

Owner of the API contracts. I implement what the AYD defines; a contract change is a PR in
the context repo (`rochatto-app-context`), never local.

## Engineering conventions (local)
@docs/conventions.md

## Docs framework (summary)

How this repo connects to the shared context (`rochatto-app-context`). The **full rules**
live in the linked files — this is just the essentials.

- **READ-ONLY context:** run `docs/scripts/sync-context.sh` to populate `docs/shared/`
  (a **gitignored** mirror of the context repo — **do not edit here**). Map and rules:
  @docs/shared/CLAUDE.md (IDs, frontmatter, lifecycle, `ID@repo` refs) ·
  @docs/shared/requirements.md (requirements **and glossary** — ALWAYS use these terms).
- **What lives in this repo:** `docs/specs/` (SPEC — already includes the implementation
  plan: approach, steps, tests, checklist), `docs/technical_decisions/` (local TDR),
  `docs/conventions.md` (CONV), `docs/changelog.md`.
- **Contracts only change in the context** (AYD/ADR). If this API diverges from the AYD,
  **flag it** — do not adapt locally (see `docs/shared/CLAUDE.md`, "Core rule").
- **Feature flow:** read the AYD in `docs/shared/design/` → create/update the SPEC
  (`parents: [AYD-NNN@context]`, covers what + how) and implement → contract changed? go
  back to the AYD in the context repo before proceeding.
