package oauth

import (
	"context"
	"log"
	"time"
)

const cleanupEvery = time.Hour

// MaybeCleanup faz a limpeza oportunista das tabelas técnicas (AYD-006 §Modelo de domínio),
// no máximo 1×/hora por instância: clientes sem Connection após 24 h, pedidos de
// autorização vencidos, tokens expirados há > 7 dias e Connections nunca ativadas.
func (s *Server) MaybeCleanup(ctx context.Context) {
	now := s.now()
	last := s.lastCleanup.Load()
	if now.Unix()-last < int64(cleanupEvery/time.Second) || !s.lastCleanup.CompareAndSwap(last, now.Unix()) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		s.Cleanup(ctx)
	}()
}

// Cleanup executa a limpeza imediatamente (exposta para testes).
func (s *Server) Cleanup(ctx context.Context) {
	now := s.now()
	db := s.db.WithContext(ctx)
	stmts := []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM oauth_authorization_requests WHERE expires_at < ?`, []any{now.Add(-24 * time.Hour)}},
		{`DELETE FROM oauth_tokens WHERE expires_at < ?`, []any{now.Add(-7 * 24 * time.Hour)}},
		{`DELETE FROM oauth_connections c WHERE c.created_at < ? AND c.revoked_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.connection_id = c.id)`, []any{now.Add(-time.Hour)}},
		{`DELETE FROM oauth_clients c WHERE c.created_at < ?
			AND NOT EXISTS (SELECT 1 FROM oauth_connections o WHERE o.client_id = c.client_id)`, []any{now.Add(-orphanClientTTL)}},
	}
	for _, st := range stmts {
		if err := db.Exec(st.sql, st.args...).Error; err != nil {
			log.Printf("WARN oauth cleanup: %v", err)
		}
	}
}
