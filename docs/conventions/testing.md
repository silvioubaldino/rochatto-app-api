---
id: CONV-testing
type: convention
title: Padrões de teste
status: approved
updated: 2025-01-01
owner: <nome>
---

# Padrões de teste (deste repo)

> Padrão de engenharia (vivo). Decisão pontual sobre testes que mude a abordagem → vira TDR.

- **Estrutura:** AAA (Arrange, Act, Assert).
- **Localização:** _<ex.: arquivo `*.test.*` ao lado do código>_.
- **Cobertura:** todo critério de aceite de uma SPEC tem um teste correspondente.
- **Mocks:** mockar só na fronteira (rede, storage, relógio); não mockar a unidade testada.
- **Mínimo por feature:** _<ex.: unit + 1 teste de aceite por cenário Gherkin da SPEC>_.
- **Comando:** _<ex.: `npm test`>_.
