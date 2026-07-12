---
id: CONV-git
type: convention
title: Convenções de Git
status: approved
updated: 2025-01-01
owner: <nome>
---

# Convenções de Git (deste repo)

- **Branches:** _<ex.: feature/<id-da-spec>-descricao>_.
- **Commits:** _<ex.: Conventional Commits>_; referencie o ID (SPEC/PLAN) quando aplicável.
- **PRs:** vinculam a SPEC; um PR não altera contrato (isso é PR no repo de contexto).
- **Antes do PR:** rode `docs/scripts/sync-context.sh` para validar contra o contexto atual.
