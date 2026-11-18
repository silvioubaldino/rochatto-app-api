---
id: SPEC-004
type: spec
title: Financeiro — payment method, painel entra × sai e alertas (api)
status: done
updated: 2026-11-18
parents: [AYD-004@context]
related: [PDR-001@context, ADR-001@context, AYD-002@context, GLO]
---

# Spec: Financeiro (parte deste repo — api)

> O QUÊ e COMO este repo cumpre o **AYD-004**. Contratos (enum, rotas, payloads, erros,
> formato do painel) vêm do AYD-004 e não são redefinidos aqui; leituras de pontos ambíguos
> estão em "Desvios/interpretações".

## Objetivo

Atender RF-10, RF-11, RF-14, RF-15, RF-19 e o lado "por período" do RF-32 (RN-04/05/06/08):
todo Receipt recebido tem **payment method** e deixa de ser apagado fisicamente; a Sale expõe
**receivable** e **freight_payable**; `GET /finance/summary` entrega o painel entra × sai
(por competência, movimento datado e saldos em aberto); os alertas de Receipt vencendo/vencido
são gerados pela própria API, sem `pg_cron`, preservando `lida`.

## Critérios de aceite

```gherkin
Cenário: Payment method (RF-11)
  Quando crio Receipt com data_pagamento sem payment_method → 400; com método fora do enum → 400
  E crio Receipt com data_pagamento + método → 201 "recebido" com o método
  E crio Receipt sem data_pagamento → "pendente" (método opcional)
  E confirmo Receipt sem método (e o pendente não tinha) → 400; com método → 200
  E confirmo Receipt pendente que já tinha método sem enviar outro → 200 e o método é mantido
  E PUT com status "recebido" sem método → 400; PUT "pendente" sem método → 200

Cenário: Soft-delete e bloqueio (RN-08)
  Quando excluo um Receipt → 204, a linha fica com deleted_at
  Então some de lista, detalhe da Sale, total_recebido e payment_status; novo DELETE/PUT/confirmar → 404
  E em Sale CANCELED, POST/PUT/confirmar/DELETE de Receipt → 409 "venda cancelada: recebimentos bloqueados"
  E a leitura (lista) segue permitida e os existentes ficam como estão

Cenário: Saldos da Sale (RN-05, RF-10)
  receivable = GREATEST(total_venda − total_recebido, 0); freight_payable = GREATEST(frete_valor − frete_pago, 0)
  GET /vendas filtra freight_pending, commission_pending, from/to (inclusivos; data inválida → 400)
  PUT /vendas/{id} → 400 se frete_valor < 0, frete_pago < 0 ou frete_pago > frete_valor

Cenário: Painel financeiro
  Dado Sale ativa, Sale cancelada, Receipt excluído, Receipt legado sem método e Purchases pagas
  Quando faço GET /finance/summary?from&to
  Então sales/cash/balances conferem com a fixture; a Sale cancelada e o Receipt excluído não entram
  E received_by_method vem por amount desc, com payment_method null para o legado
  E sem from/to: 1º dia do mês corrente até hoje; from > to ou data inválida → 400

Cenário: Alertas (RF-19)
  Quando leio GET /dashboard/notificacoes, a API gera os alertas antes (vencido/vencendo ≤ hoje+5)
  E alerta lido continua lido ao reler/gerar; só reabre se tipo ou data_ref mudaram
  E o alerta some quando o Receipt é recebido, excluído, reagendado além da janela ou a Sale é cancelada

Cenário: Migração 007
  up: colunas/tipo/view/função criados sem converter dados; down → up funciona
```

## Contratos consumidos/expostos

Os do AYD-004 (enum `PaymentMethod`, Receipt, Sale, `GET /finance/summary`, Alertas). Nada
além disso é exposto. OpenAPI: `docs/swagger.yaml`.

## Abordagem (como)

- **Migração 007** (`migrations/007_finance.{up,down}.sql`): enum `payment_method`;
  `pagamentos_cliente.payment_method` (nulo) e `deleted_at`; índice parcial em
  `data_pagamento` dos recebidos; `vw_vendas_resumo` recriada (colunas existentes idênticas,
  `receivable` e `freight_payable` ao final; subconsulta de Receipts ignora excluídos —
  nenhuma outra view/função depende dela); `gerar_notificacoes_pagamentos()` por
  `CREATE OR REPLACE` (remove alertas que não se qualificam mais; upsert por `pagamento_id`
  que só reabre `lida` quando `tipo`/`data_ref` mudam). `down` restaura a view e a função da 006.
- **Regras puras** em `internal/finance` (`NormalizeMethod`, `CheckMethodForStatus`,
  `ResolvePeriod`) com testes unitários; os handlers só orquestram.
- **Receipts** (`pagamentos.go`): `models.PagamentoCliente` ganha `PaymentMethod` e
  `gorm.DeletedAt` — com isso `First/Find/Delete` do GORM já respeitam o soft-delete (nenhuma
  consulta SQL crua usa a tabela fora das views). `loadVendaForWrite` devolve 404/409 antes de
  qualquer escrita.
- **Sales** (`vendas.go`): `receivable`/`freight_payable` vêm da view (lista e detalhe);
  filtros novos; validação do frete no PUT.
- **Painel** (`finance.go`): uma consulta por agregado, sobre a view (itens e Receipts já
  pré-agregados por Sale) ou sobre `pagamentos_cliente`/`purchases` — nunca join Items × Receipts.
  Valores com `ROUND(…, 2)`.
- **Alertas** (`dashboard.go`): `GET /dashboard/notificacoes` executa
  `SELECT gerar_notificacoes_pagamentos()` e só então lê; se a geração falhar, **responde 500**
  (alertas desatualizados seriam piores que um erro visível). `POST /dashboard/gerar-notificacoes` mantido.
- **Ops**: `docs/ops/pg_cron_setup.sql` vira nota de `cron.unschedule`; `docs/ops/database.md` atualizado.

### Desvios/interpretações

1. **Receipt sem nenhuma data nasce `pendente`** (AYD: "sem data_pagamento → pendente"). Antes
   da 007 nascia "recebido hoje"; o web precisa enviar `data_pagamento` + `payment_method` ao registrar um recebimento.
2. **"Hoje" no calendário de São Paulo**: a função SQL usa `(NOW() AT TIME ZONE 'America/Sao_Paulo')::date`
   em vez de `CURRENT_DATE` (UTC no Neon, que viraria o dia às 21h BRT), alinhada ao `today()` da API;
   o default de `data_pagamento` em "confirmar" também usa esse dia.
3. **`a_receber` legado mantido** (`total_venda − total_recebido`, pode ser negativo) ao lado do
   novo `receivable` (≥ 0); `a_pagar_frete` do detalhe passou a valer `freight_payable`.
4. **`freight_pending`/`commission_pending` excluem Sales CANCELED** (AYD-004 ajustado), para o
   drill-down bater com `balances.*`. O link de `payment_status=PENDING,PARTIAL` (AYD-002) não
   exclui canceladas — uma Sale cancelada com saldo aparece na lista com o badge cinza.
5. **`paid_to_suppliers`** soma `paid_amount` das Purchases não excluídas com `paid_at` no período,
   sem exigir `payment_status = PAID` (`paid_at` só é carimbado ao pagar — SPEC-002).
6. **404 antes de 409/400 em escrita de Receipt**: Sale inexistente/excluída → 404; Sale cancelada
   → 409 antes de validar o payload; depois 400 de payload e 404 do Receipt.
7. **`received_by_method`** omite métodos com soma 0; empate de `amount` desempata por método (null por último).
8. **Receipts legados `recebido` sem método** seguem válidos; o método só é exigido quando editados (PUT) ou confirmados.
9. **`down` da 007**: Receipts soft-deletados voltam a contar (anotado em `docs/ops/database.md`).

## Passos de implementação

1. Migração 007 + teste do ciclo up/down (banco descartável).
2. Model `PagamentoCliente` (`PaymentMethod`, `DeletedAt`) e `internal/finance` + testes unitários.
3. Handlers: `pagamentos.go`, `vendas.go`, `finance.go` (+ rota em `cmd/server`), `dashboard.go`.
4. Testes de integração; swagger, ops, changelog, CLAUDE.md.

## Arquivos / módulos afetados

- `migrations/007_finance.{up,down}.sql`
- `internal/models/models.go`, `internal/finance/`
- `internal/handlers/{pagamentos,vendas,finance,dashboard}.go`, `cmd/server/main.go`
- `docs/swagger.yaml`, `docs/ops/{database.md,pg_cron_setup.sql}`, `docs/changelog.md`, `CLAUDE.md`

## Testes (ver docs/conventions.md)

- Unitários: `internal/finance/finance_test.go` (enum de método, regra "recebido exige método", período do painel).
- Integração (`TEST_DATABASE_URL`):
  - `internal/dbmigrate/migration007_integration_test.go` — colunas/view/função, soft-delete na view, down → up.
  - `internal/handlers/finance_integration_test.go` — regras de payment method (POST/PUT/confirmar),
    soft-delete, 409 em Sale cancelada, receivable/freight_payable + filtros + validação do PUT,
    painel numa fixture (Sale cancelada, Receipt excluído, Receipt legado sem método, Purchases),
    validação/default do período, geração de alertas (lida preservada, reabre por tipo/data_ref, remoções).
  - `fulfillment_integration_test.go` ajustado (Receipts recebidos agora levam `payment_method`).

## Casos de borda & fora de escopo

- Fora: Freight/Commission datados no caixa (RF-22), módulo de Commissions, parcelas ao Supplier,
  estorno de Receipt de Sale cancelada, alertas de pagamento a Supplier, mudanças em `/dashboard/resumo` (AYD-005).
- Sem aviso fora do sistema se o dono ficar dias sem abrir o app (aceito no AYD).

## Checklist de entrega

- [x] Migração 007 (up/down) testada
- [x] `payment_method` + soft-delete + 409 em Sale cancelada nos Receipts
- [x] `receivable`, `freight_payable`, filtros e validação de frete em `/vendas`
- [x] `GET /finance/summary`
- [x] Alertas gerados na leitura, `lida` preservada; pg_cron aposentado (nota de deploy)
- [x] Testes unitários e de integração cobrindo os critérios
- [x] Swagger, ops, changelog, CLAUDE.md
