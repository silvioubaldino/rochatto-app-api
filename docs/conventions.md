---
id: CONV
type: conventions
title: Convenções de engenharia (código, deste repo)
status: approved
updated: 2025-01-01
---

# Convenções de engenharia (deste repo)

> Padrão de engenharia (vivo). Decisão pontual que muda a abordagem → vira TDR.
> Convenções de **documentação** (IDs, frontmatter, ciclo de vida, `ID@repo`) ficam no
> repo de contexto: `docs/shared/conventions.md`.

## Estilo de código
- **Linguagem / versão:** _<preencher>_.
- **Linter / formatter:** _<ex.: configuração e comando>_.
- **Nomenclatura:** use os termos do glossário (`docs/shared/requirements.md`, seção Glossário) — sempre em **inglês** (variáveis, funções, classes, rotas, entidades canônicas). Documentação e comentários podem ser em português.
- **Estrutura de pastas:** _<descreva a organização do `src/`>_.
- **Tratamento de erros / logging:** _<padrão do repo>_.

## Testes
- **Estrutura:** AAA (Arrange, Act, Assert).
- **Localização:** _<ex.: arquivo `*.test.*` ao lado do código>_.
- **Cobertura:** todo critério de aceite de uma SPEC tem um teste correspondente.
- **Mocks:** mockar só na fronteira (rede, storage, relógio); não mockar a unidade testada.
- **Mínimo por feature:** _<ex.: unit + 1 teste de aceite por cenário Gherkin da SPEC>_.
- **Comando:** _<ex.: `npm test`>_.

## Git
- **Branches:** _<ex.: feature/<id-da-spec>-descricao>_.
- **Commits:** _<ex.: Conventional Commits>_; referencie o ID (SPEC) quando aplicável.
- **PRs:** vinculam a SPEC; um PR não altera contrato (isso é PR no repo de contexto).
- **Antes do PR:** rode `docs/scripts/sync-context.sh` para validar contra o contexto atual.
