package oauth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidToken: token inexistente, expirado, revogado, de outro resource, ou Connection
// revogada / fora da allowlist. O /mcp responde 401 em todos os casos.
var ErrInvalidToken = errors.New("invalid token")

// Principal é a Connection autenticada por um access token.
type Principal struct {
	ConnectionID uuid.UUID
	ClientName   string
	OwnerUID     string
	OwnerEmail   string
	Scopes       []string
	ExpiresAt    time.Time
}

// HasScope diz se a Connection tem o scope.
func (p *Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// VerifyAccessToken valida o access token do /mcp (AYD-006 §1): hash, validade, Connection
// ativa, resource e — a cada chamada — a allowlist (se o e-mail saiu, revoga a Connection).
// Atualiza last_used_at no máximo 1×/min.
func (s *Server) VerifyAccessToken(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrInvalidToken
	}
	var row struct {
		ConnectionID uuid.UUID   `gorm:"column:connection_id"`
		TokenScopes  StringArray `gorm:"column:token_scopes"`
		ConnScopes   StringArray `gorm:"column:conn_scopes"`
		ExpiresAt    time.Time   `gorm:"column:expires_at"`
		OwnerUID     string      `gorm:"column:owner_uid"`
		OwnerEmail   string      `gorm:"column:owner_email"`
		Resource     string      `gorm:"column:resource"`
		ClientName   *string     `gorm:"column:client_name"`
	}
	now := s.now()
	res := s.db.WithContext(ctx).Raw(`
		SELECT t.connection_id, t.scopes AS token_scopes, c.scopes AS conn_scopes, t.expires_at,
		       c.owner_uid, c.owner_email, c.resource, cl.client_name
		FROM oauth_tokens t
		JOIN oauth_connections c ON c.id = t.connection_id
		JOIN oauth_clients cl ON cl.client_id = c.client_id
		WHERE t.token_hash = ? AND t.kind = 'ACCESS'
		  AND t.revoked_at IS NULL AND t.expires_at > ? AND c.revoked_at IS NULL`,
		hashToken(token), now).Scan(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 || row.Resource != s.cfg.Resource() {
		return nil, ErrInvalidToken
	}
	if !s.allow.AllowsEmail(row.OwnerEmail) {
		_, _ = s.revokeConnection(ctx, s.db, row.ConnectionID, "ALLOWLIST")
		return nil, ErrInvalidToken
	}
	_ = s.db.WithContext(ctx).Exec(`UPDATE oauth_connections SET last_used_at = ?
		WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		now, row.ConnectionID, now.Add(-lastUsedResolution)).Error

	scopes := []string(row.ConnScopes)
	if row.TokenScopes != nil {
		scopes = row.TokenScopes
	}
	name := ""
	if row.ClientName != nil {
		name = *row.ClientName
	}
	return &Principal{
		ConnectionID: row.ConnectionID, ClientName: name, OwnerUID: row.OwnerUID,
		OwnerEmail: row.OwnerEmail, Scopes: scopes, ExpiresAt: row.ExpiresAt,
	}, nil
}
