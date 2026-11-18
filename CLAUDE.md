# CLAUDE.md — Backend: Sistema de Gestão de Vendas

## O que é este componente

API REST em **Go** responsável por toda a lógica de negócio, persistência de dados e validações do Sistema de Gestão de Vendas. É o único ponto de contato entre o frontend e o banco de dados.

## Objetivo

Fornecer endpoints HTTP seguros, validados e tipados para:
- Gerenciar o ciclo completo de vendas (criação, itens, status, entrega)
- Controlar o fluxo financeiro (recebimentos, pagamentos a fornecedores, comissões, frete)
- Manter cadastros de clientes, produtos, fornecedores e vendedores externos
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
  handlers/                 → um arquivo por recurso (clientes, vendas, quotes, purchases, stock, etc.)
  fulfillment/              → regras puras do atendimento da Sale (transições, pagamento, cancelamento — SPEC-002)
  dbmigrate/                → golang-migrate sobre as migrations embutidas
  models/                   → structs GORM de domínio e tipos compartilhados
migrations/                 → NNN_nome.{up,down}.sql + embed.go (package migrations)
docs/ops/                   → operação: baseline de migrations, backup, script pg_cron
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
- Campos calculados (total_venda, lucro, a_receber, payment_status, total_cost/to_pay, balance) **nunca são persistidos** — vêm das views `vw_vendas_resumo`, `vw_purchases_resumo` e `vw_stock_balance`
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
```

Testes de integração: `TEST_DATABASE_URL` (banco descartável com migrations aplicadas).

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

## Deploy (Google Cloud Run — free tier)

- `Dockerfile` na raiz: binários `server` e `migrate` (migrations embutidas, tzdata incluído)
- Variáveis de ambiente configuradas no serviço do Cloud Run (`ENV=production`,
  `ALLOWED_EMAILS` obrigatório)
- Banco: Neon Postgres. Baseline de migrations, backup e pg_cron: `docs/ops/database.md`
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
