---
id: SPEC-003
type: spec
title: Cadastros comerciais — Partner, lead source e categoria de Product (api)
status: done
updated: 2026-11-18
parents: [AYD-003@context]
related: [GLO, PDR-001@context, ADR-001@context]
---

# Spec: Cadastros comerciais (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-003**. `done` = implementado (este documento é o
> registro histórico da entrega).
>
> Contratos (enums, rotas, payloads, erros) vêm do **AYD-003** (com ADR-001: rotas/campos
> novos em inglês) e não são redefinidos aqui. Pontos ambíguos encontrados estão em
> "Ambiguidades sinalizadas ao AYD" — decididos localmente de forma conservadora, a confirmar
> no AYD.

## Objetivo

Atender RF-01, RF-02 e RF-04 com as decisões 13–15 do PDR-001:

- **Partner (referrer)**: o cadastro legado `vendedores_externos` vira o recurso `/partners`,
  com **tipo** (`PartnerType`). `/vendedores-externos` é removida (o web migra no mesmo release).
- **Customer** ganha **lead source** (lista fixa) e, quando `PARTNER`, **qual** Partner.
- **Product** ganha **Product category**, cadastrável pelo dono (`/product-categories`).

## Critérios de aceite

```gherkin
Cenário: Cadastrar Partner com tipo (RF-04)
  Quando faço POST /partners com { name, type: "ARCHITECT" }
  Então recebo 201 com { id, name, phone, type, notes, created_at } (sem campos PT)

Cenário: Validações do Partner
  Quando faço POST/PUT /partners sem name, sem type ou com type fora de PartnerType
  Então recebo 400 com { "error": "..." }

Cenário: Listar Partners com filtros
  Quando faço GET /partners?q=<trecho do nome>&type=ARCHITECT
  Então recebo só os Partners não excluídos que casam (ordem por nome)
  E type inválido no filtro dá 400

Cenário: Partners legados viram OTHER
  Dado um vendedor externo cadastrado antes da migração 005
  Quando faço GET /partners/{id}
  Então recebo type = "OTHER" com name/phone/notes mapeados de nome/telefone/observacoes

Cenário: Excluir Partner (soft-delete)
  Quando faço DELETE /partners/{id}
  Então recebo 204, GET passa a 404 e o registro permanece com deleted_at
  E Quotes/Sales/Customers que o referenciam mantêm o vínculo (mesmo id)

Cenário: Customer indicado por Partner (RF-01)
  Quando faço POST /clientes { nome, lead_source: "PARTNER", referrer_id }
  Então recebo 201 com lead_source, referrer_id e referrer_name

Cenário: Regras de lead source
  Quando lead_source = PARTNER sem referrer_id                → 400
  Quando referrer_id preenchido com lead_source ≠ PARTNER/null → 400
  Quando referrer_id não existe (ou Partner excluído)          → 400
  Quando lead_source fora de LeadSource                        → 400
  Quando lead_source e referrer_id são null                    → 201 (Customers antigos)

Cenário: Filtrar Customers por lead source
  Quando faço GET /clientes?lead_source=PARTNER&referrer_id=<id>
  Então recebo só os Customers que casam; valores inválidos dão 400

Cenário: Categoria de Product (RF-02)
  Quando faço POST /product-categories { name }
  Então recebo 201; GET lista as não excluídas em ordem alfabética
  E outro nome igual ignorando caixa dá 409 (POST e PUT)
  E DELETE faz soft-delete (204), libera o nome e PUT/DELETE posteriores dão 404

Cenário: Product com categoria
  Quando faço POST /produtos { nome, category_id }
  Então recebo 201 com category_id e category_name
  E category_id inexistente ou excluído dá 400
  E GET /produtos?category_id=<id> filtra
  E após excluir a categoria o Product mantém category_id e continua exibindo category_name
```

## Contratos consumidos/expostos

Fonte da verdade: **AYD-003**, seção "Contratos". Rotas (sob `/api/v1`, auth Firebase — RNF-02):

```
GET    /partners            (q, type)        POST /partners
GET    /partners/{id}       PUT /partners/{id}       DELETE /partners/{id}

GET    /product-categories  POST /product-categories
PUT    /product-categories/{id}              DELETE /product-categories/{id}

/clientes  (legado PT) + lead_source, referrer_id (req/res), referrer_name (res);
           GET filtros lead_source, referrer_id
/produtos  (legado PT) + category_id (req/res), category_name (res); GET filtro category_id
```

Removido: `/vendedores-externos` (todas as rotas). `docs/swagger.yaml` atualizado.

## Abordagem (como)

- **Migração 005** (`migrations/005_commercial_registries.up.sql` / `.down.sql`):
  - enum `partner_type` e `vendedores_externos.type partner_type NOT NULL DEFAULT 'OTHER'`
    (registros existentes ficam OTHER);
  - enum `lead_source_type`; `clientes.lead_source` (null) e `clientes.referrer_id →
    vendedores_externos(id)` (null), com
    `CHECK (COALESCE(lead_source = 'PARTNER', FALSE) = (referrer_id IS NOT NULL))`. O
    `COALESCE` é necessário: com o check literal do AYD, `lead_source NULL` torna a expressão
    NULL e o CHECK passaria com `referrer_id` preenchido. Assim a regra vale nos dois sentidos
    e `NULL/NULL` continua permitido;
  - tabela `product_categories` (`id`, `name`, `deleted_at`, `created_at`) com índice único
    parcial `lower(name) WHERE deleted_at IS NULL`;
  - `produtos.category_id → product_categories(id)` (null); índices nas FKs novas.
  - `clientes` **não tinha** coluna legada de origem em texto livre — nada a preservar.
- **Sem rename físico** de `vendedores_externos` (decisão local permitida pelo AYD-003): a
  tabela continua com esse nome; IDs e FKs preservados — `quotes.referrer_id` e
  `vendas.vendedor_externo_id` seguem apontando para ela. O rename para `partners`, se
  feito, será um TDR próprio (com a migração PT→EN incremental do ADR-001).
- **Modelos** (`internal/models/models.go`): `VendedorExterno` → `Partner` (`TableName()
  "vendedores_externos"`, colunas PT mapeadas para JSON EN: `nome→name`, `telefone→phone`,
  `observacoes→notes`, + `type`); novo `ProductCategory`; `Cliente` + `LeadSource`,
  `ReferrerID`, `ReferrerName` (só leitura); `Produto` + `CategoryID`, `CategoryName` (só
  leitura). Os nomes vêm de `LEFT JOIN` **sem** filtrar excluídos, para exibir o nome de
  Partner/categoria já excluídos.
- **Handlers**: `partners.go` (novo, substitui `vendedores_externos.go`),
  `product_categories.go` (novo), `clientes.go` e `produtos.go` (campos/filtros novos; as
  respostas de create/update releem o registro com o JOIN). `quotes.go` só troca o tipo usado
  na checagem do referrer. Helpers em `helpers.go`: `normalizeName`, `nullIfBlank`,
  `isUniqueViolation` (SQLSTATE 23505).
- **409 de categoria**: pré-checagem case-insensitive + índice único como garantia final
  (corrida → 23505 → 409).
- Mensagens de erro em PT-BR com o nome do campo do payload (padrão dos demais handlers).

### Decisões locais (dentro do contrato)

- Strings de texto (`name`, `phone`, `notes`, `lead_source`) passam por trim; `phone`/`notes`
  vazios viram `null`; `lead_source: ""` é tratado como `null`.
- `name` (Partner e categoria) até 200 caracteres (mesmo limite de `nome` no legado).
- `DELETE` de Partner/categoria inexistente ou já excluído → **404** (o legado devolvia 204).
- `PUT /clientes` e `PUT /produtos` continuam **substituição total** (como o legado): omitir
  `lead_source`/`referrer_id`/`category_id` no PUT **limpa** o campo.
- GET de Partner excluído → 404; o Customer/Quote que o referencia segue mostrando o nome.

### Ambiguidades sinalizadas ao AYD

1. **Vínculo com Partner/categoria já excluídos no PUT.** O AYD diz que Products "mantêm o
   vínculo" com a categoria excluída, mas não diz o que um `PUT /produtos` que reenvia esse
   `category_id` deve fazer. Implementado: atribuir uma categoria **nova** inexistente ou
   excluída → 400; **manter** a mesma já gravada → aceito (senão editar qualquer campo do
   Product obrigaria a perder a categoria). Mesma regra para `referrer_id` de Customer com
   Partner excluído. Confirmar no AYD.
2. **"Exibe como inativa".** O contrato de `/produtos` só expõe `category_name`; não há flag de
   inatividade. O web pode inferir (o `category_id` não está em `GET /product-categories`).
   Se for preciso um campo explícito (ex.: `category_active`), é mudança de contrato no AYD.
3. **CHECK literal do AYD** não impede `referrer_id` com `lead_source NULL` (semântica de
   NULL no SQL). Implementado com `COALESCE`, cumprindo a regra do texto (400 "referrer_id
   preenchido com lead_source ≠ PARTNER"). Sugerir ajuste da redação no AYD.
4. **`GET /product-categories/{id}`** não consta do AYD — não implementado.
5. **Ordem alfabética** usa `lower(name)` com a collation do banco (acentos podem ordenar
   diferente do esperado em collation `C`).

## Passos de implementação

1. Migração 005 (up/down), testada com up → down → up.
2. Modelos `Partner`, `ProductCategory`; campos novos em `Cliente`/`Produto`.
3. `partners.go` e `product_categories.go`; remoção de `vendedores_externos.go`.
4. `clientes.go` (lead source + filtros), `produtos.go` (categoria + filtro).
5. Registro em `cmd/server/main.go` (`NewPartnersHandler`, `NewProductCategoriesHandler`).
6. Testes unitários e de integração; `docs/swagger.yaml`; `docs/changelog.md`.

## Arquivos / módulos afetados

- `migrations/005_commercial_registries.up.sql` / `.down.sql` (novos)
- `internal/models/models.go`
- `internal/handlers/partners.go` (novo), `product_categories.go` (novo),
  `vendedores_externos.go` (removido), `clientes.go`, `produtos.go`, `quotes.go`, `helpers.go`
- `internal/handlers/commercial_registries_test.go`,
  `commercial_registries_integration_test.go` (novos)
- `cmd/server/main.go`, `go.mod` (`pgx/v5` passa a dependência direta), `docs/swagger.yaml`,
  `docs/changelog.md`

## Testes (ver docs/conventions.md)

- **Unit** (`commercial_registries_test.go`): `validatePartnerRequest` (obrigatórios, enum,
  normalização), `validateLeadSource` (todas as combinações lead_source × referrer_id),
  `normalizeName`.
- **Integração** (`commercial_registries_integration_test.go`, rodam com `TEST_DATABASE_URL`
  num banco descartável com 001–005): um teste por bloco de cenários acima — Partners (CRUD,
  400s, filtros, 404s, soft-delete, Quote com `referrer_id` de Partner), Partner legado = OTHER,
  lead source (happy path, cada 400 do AYD, filtros, search, Partner excluído, CHECK do banco
  nos dois sentidos), categorias (ordem, 409 case-insensitive em POST/PUT, 404, soft-delete e
  reuso do nome), categoria em `/produtos` (category_name, filtro, inexistente/excluída → 400,
  vínculo mantido após exclusão).

```bash
go vet ./... && go test ./...
TEST_DATABASE_URL="host=... dbname=... sslmode=disable" go test -count=1 ./...
```

## Casos de borda & fora de escopo

- **Borda:** Customer sem lead source (null); REFERRAL não guarda quem indicou (AYD-003);
  recriar categoria com nome de uma excluída (permitido); renomear categoria só mudando a caixa
  (permitido).
- **Fora de escopo:** ranking de Partners/lead source no dashboard, Commission por Partner
  (RF-25), unidade como lista fixa (segue texto livre — PDR-001), rename físico das tabelas PT.

## Checklist de entrega

- [x] Migração 005 (up/down) aplicada em Postgres 16 após 001–003; down → up testado
- [x] CHECK lead_source × referrer_id efetivo nos dois sentidos (NULL/NULL permitido)
- [x] `/partners` (CRUD + filtros q/type) e remoção de `/vendedores-externos`
- [x] `/clientes` com lead_source/referrer_id/referrer_name e filtros
- [x] `/product-categories` (CRUD, ordem alfabética, 409, soft-delete)
- [x] `/produtos` com category_id/category_name e filtro
- [x] Testes unitários e de integração passando (`go vet`, `go test` com e sem `TEST_DATABASE_URL`)
- [x] `docs/swagger.yaml` e `docs/changelog.md` atualizados
- [ ] Orquestrador: confirmar no AYD-003 as ambiguidades 1–3 e registrar `SPEC-003@api` em `children`
