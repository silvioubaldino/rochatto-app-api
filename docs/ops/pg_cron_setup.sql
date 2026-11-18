-- =============================================================
-- pg_cron NÃO é mais necessário (AYD-004 / SPEC-004).
--
-- A função gerar_notificacoes_pagamentos() agora é versionada na migration
-- migrations/007_finance.up.sql e a própria API a executa a cada
-- GET /dashboard/notificacoes (também disponível em POST /dashboard/gerar-notificacoes).
--
-- Passo de deploy: quem tiver agendado o job antigo remove com o comando abaixo
-- (SQL Editor / psql). Sem o job, nada mais precisa ser configurado.
-- Se o job não existir, o comando falha com "could not find valid entry" — pode ignorar;
-- se a extensão pg_cron não estiver instalada, também não há nada a fazer.
-- =============================================================

SELECT cron.unschedule('notificacoes-diarias');
