---
id: TDR-001
type: tdr
title: Migrations embutidas (golang-migrate) e allowlist do dono
status: approved
updated: 2026-11-18
parents: []
related: [RNF-02, RNF-05, RN-10]
superseded_by: null
---

# TDR-001: Migrations embutidas (golang-migrate) e allowlist do dono

> Append-only: nunca reescreva. Decisão nova = novo TDR que substitui este.

## Contexto
- As migrations eram aplicadas à mão no banco de produção; nada registrava qual versão estava
  aplicada, e a correção dos cálculos (Fase 0) precisa de uma migration nova (004).
- A API aceitava **qualquer** token Firebase válido do projeto: qualquer conta Google que
  fizesse login no web teria acesso aos dados do dono (RNF-02, RN-10: um usuário).

## Decisão
1. **golang-migrate** com as migrations **embutidas** no binário (`migrations.FS`, source
   `iofs`, driver pgx/v5). Aplicadas no startup quando `MIGRATE_ON_START=true` e via CLI
   `cmd/migrate` (`up`, `down N`, `version`, `force N`). Produção faz baseline com
   `force 3` antes do primeiro `up` (ver `docs/ops/database.md`).
2. **Allowlist por e-mail** no middleware de auth: `ALLOWED_EMAILS` (vírgulas,
   case-insensitive) e `email_verified = true`; fora da lista → 403. Com `ENV=production` e
   lista vazia o servidor **não sobe**; em desenvolvimento lista vazia libera tudo com aviso.

## Alternativas & trade-offs
- *Migrations à mão / CLI externo do migrate:* sem rastreio de versão e sujeito a esquecer
  passos; o binário embutido garante que código e schema andam juntos. Custo: uma dependência
  a mais e o cuidado do baseline.
- *Custom claims do Firebase (`owner: true`) ou restringir no Firebase:* exige Admin SDK /
  script fora do repo; a allowlist por env é suficiente para 1 usuário e trivial de auditar.
  Se surgir multiusuário (RF-28), substituir por perfis — novo TDR/ADR.
