# Operação do banco (Neon Postgres)

## Migrations (golang-migrate, embutidas no binário)

- Arquivos em `migrations/NNN_nome.{up,down}.sql`, embutidos via `migrations/embed.go`.
  Só entram nomes `*.up.sql` / `*.down.sql`; scripts avulsos ficam em `docs/ops/`.
- Controle de versão na tabela `schema_migrations` (criada pelo golang-migrate).
- Aplicar:
  - no startup: `MIGRATE_ON_START=true` (o servidor aplica as pendentes antes de abrir a porta
    e não sobe se falhar);
  - manualmente: `DATABASE_URL=... go run ./cmd/migrate up` (no container: `./migrate up`).
- Outros comandos: `version`, `down N`, `force N` (marca a versão sem rodar SQL).
- Use a connection string **direta** do Neon (sem `-pooler`) para migrar: o golang-migrate usa
  advisory lock de sessão, que não funciona via PgBouncer em modo transaction.
- Migration que falhar no meio deixa `dirty=true`: corrija o banco à mão, depois
  `force <última versão boa>` e rode `up` de novo.

### Baseline do banco de produção (migrado à mão)

O banco de produção recebeu 001–003 manualmente e **não tem** `schema_migrations`. Antes do
primeiro `up` (e antes de ligar `MIGRATE_ON_START`), registre a baseline:

```bash
# 1. backup (ver abaixo)
# 2. confira que 001–003 estão aplicadas: tabelas quotes/quote_items e vendas.quote_id existem
psql "$DATABASE_URL" -c '\d vendas' | grep quote_id
# 3. marque a versão 3 sem rodar SQL
DATABASE_URL=... go run ./cmd/migrate force 3
# 4. aplique o que falta (004 em diante)
DATABASE_URL=... go run ./cmd/migrate up
DATABASE_URL=... go run ./cmd/migrate version   # version=4 dirty=false (ou maior)
```

Se o banco não tiver tudo da 003, ajuste o número do `force` para a última migration
realmente aplicada. Nunca rode `up` sem `force` num banco já populado: a 001 tentaria recriar
as tabelas e falharia (deixando `dirty`).

## pg_cron (notificações de pagamento)

`docs/ops/pg_cron_setup.sql` cria `gerar_notificacoes_pagamentos()` e agenda o job diário.
**Não é migration** (depende da extensão pg_cron, criada no painel do provedor). O endpoint
`POST /dashboard/gerar-notificacoes` chama essa função — sem ela, responde 500.
O script foi escrito para o Supabase; no Neon, confirme se pg_cron está disponível no plano.

## Backup

- **Neon (automático):** point-in-time restore dentro da janela de histórico do plano
  (free tier: curta — confira no console). Restaurar: Console → projeto → *Branches* →
  *Restore* (ou crie um branch a partir de um timestamp e aponte `DATABASE_URL` para ele
  depois de conferir os dados).
- **Manual (antes de migrations em produção e periodicamente):**

  ```bash
  pg_dump "$DATABASE_URL" --format=custom --no-owner --no-privileges \
    --file="rochatto-$(date +%F).dump"
  # restaurar num banco vazio:
  pg_restore --no-owner --no-privileges --dbname="$RESTORE_DATABASE_URL" rochatto-AAAA-MM-DD.dump
  ```

  Use um `pg_dump` da mesma major do servidor (ou mais nova). Guarde o arquivo fora da máquina
  (contém dados de clientes — LGPD).
