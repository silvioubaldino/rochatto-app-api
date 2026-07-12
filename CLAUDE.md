# CLAUDE.md — Backend: Sistema de Gestão de Vendas

## O que é este componente

API REST em **Go** responsável por toda a lógica de negócio, persistência de dados e validações do Sistema de Gestão de Vendas. É o único ponto de contato entre o frontend e o banco de dados.

## Objetivo

Fornecer endpoints HTTP seguros, validados e tipados para:
- Gerenciar o ciclo completo de vendas (criação, itens, status, entrega)
- Controlar o fluxo financeiro (recebimentos, pagamentos a fornecedores, comissões, frete)
- Manter cadastros de clientes, produtos, fornecedores e vendedores externos
- Controlar entradas de estoque físico
- Fornecer dados agregados para o dashboard e alertas de pagamentos

## Stack

| Tecnologia | Versão | Papel |
|---|---|---|
| Go | 1.22+ | Runtime e HTTP server |
| `net/http` stdlib | — | Router HTTP (sem framework externo) |
| `sqlc` | 1.26+ | Geração de queries type-safe a partir de SQL puro |
| `pgx/v5` | 5.x | Driver PostgreSQL |
| `golang-jwt/jwt` | v5 | Validação de Firebase JWT |
| PostgreSQL | 15+ (Supabase) | Banco de dados principal |
| `golang-migrate` | 4.x | Migrations versionadas |

## Arquitetura

```
Monolito simples — sem microserviços, sem message queues, sem cache, seguindo clean architecture.
```

```
cmd/server/main.go          → bootstrap: DB, router, middlewares, listen
internal/
  auth/                     → middleware: valida Firebase JWT em toda requisição
  handlers/                 → um arquivo por recurso (clientes, vendas, etc.)
  db/
    queries/                → arquivos .sql (lidos pelo sqlc)
    *.go                    → código gerado pelo sqlc (nunca editar manualmente)
  models/                   → structs de domínio e tipos compartilhados
  calculator/               → funções puras de cálculo financeiro
migrations/                 → arquivos SQL numerados (001_init.up.sql, etc.)
sqlc.yaml                   → config do sqlc
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
| Não encontrado | 404 Not Found |
| Erro interno | 500 Internal Server Error |

### Banco de dados
- Todas as PKs são `UUID` geradas pelo PostgreSQL (`gen_random_uuid()`)
- Todas as entidades principais têm `created_at TIMESTAMPTZ` e `deleted_at TIMESTAMPTZ` (soft delete)
- **Nunca deletar fisicamente** registros de vendas, clientes, produtos ou pagamentos
- Campos calculados (total_venda, lucro, a_receber) **nunca são persistidos** — vêm da view `vw_vendas_resumo`
- Valores monetários: `DECIMAL(12,2)` no banco, `float64` no Go

### Código Go
- Sem ORM — apenas `sqlc` com SQL explícito
- Handlers não contêm lógica de negócio — delegam para funções em `internal/`
- Cada handler valida o input antes de chamar o banco
- Erros de banco são logados internamente mas nunca expostos ao cliente
- Variáveis de ambiente via `.env` local e variáveis do Render em produção

## Variáveis de ambiente

```env
DATABASE_URL=postgresql://user:pass@host:5432/dbname
FIREBASE_PROJECT_ID=seu-projeto-firebase
PORT=8080
ENV=development   # ou production
```

## Como rodar localmente

```bash
# 1. Subir banco local (opcional — pode usar Supabase direto)
docker compose up -d db

# 2. Rodar migrations
migrate -path migrations -database $DATABASE_URL up

# 3. Gerar código sqlc (após alterar queries)
sqlc generate

# 4. Rodar servidor
go run cmd/server/main.go
```

## Fases de implementação

| Fase | Arquivo | Escopo |
|---|---|---|
| 1 | `specs/phase-01-foundation.md` | Setup, DB, JWT middleware, health check |
| 2 | `specs/phase-02-clients-catalogs.md` | CRUD: clientes, produtos, fornecedores, vendedores externos |
| 3 | `specs/phase-03-sales-core.md` | CRUD: vendas + itens de venda |
| 4 | `specs/phase-04-financial.md` | Pagamentos do cliente, pagamentos a fornecedores, view calculada |
| 5 | `specs/phase-05-dashboard.md` | Dashboard, notificações, relatórios e pg_cron |
| 6 | `specs/phase-06-estoque.md` | Módulo de estoque físico |

## Deploy (Render.com — Free Tier)

- `Dockerfile` na raiz do projeto
- Render detecta automaticamente e builda o binário Go
- Variáveis de ambiente configuradas no painel do Render
- ⚠️ Free tier: servidor hiberna após 15min de inatividade (cold start ~30s)

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
