package oauth

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// revokeConnection revoga a Connection e todos os tokens dela. Devolve false se já estava
// revogada (ou não existe). db pode ser uma transação.
func (s *Server) revokeConnection(ctx context.Context, db *gorm.DB, id uuid.UUID, reason string) (bool, error) {
	db = db.WithContext(ctx)
	now := s.now()
	res := db.Exec(`UPDATE oauth_connections SET revoked_at = ?, revoked_reason = ?
		WHERE id = ? AND revoked_at IS NULL`, now, reason, id)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	if err := db.Exec(`UPDATE oauth_tokens SET revoked_at = ? WHERE connection_id = ? AND revoked_at IS NULL`, now, id).Error; err != nil {
		return true, err
	}
	return true, nil
}

// revoke: POST /oauth/revoke (RFC 7009; AYD-006 §2.6). Sempre 200 {}. Refresh token revoga
// a Connection inteira; access token revoga só ele. O client_id precisa ser o dono do token.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err == nil {
		s.doRevoke(r.Context(), r.PostForm.Get("token"), r.PostForm.Get("token_type_hint"), r.PostForm.Get("client_id"))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (s *Server) doRevoke(ctx context.Context, token, hint, clientID string) {
	if token == "" || clientID == "" {
		return
	}
	var row struct {
		ID           uuid.UUID `gorm:"column:id"`
		Kind         string    `gorm:"column:kind"`
		ConnectionID uuid.UUID `gorm:"column:connection_id"`
		ClientID     string    `gorm:"column:client_id"`
	}
	res := s.db.WithContext(ctx).Raw(`
		SELECT t.id, t.kind, t.connection_id, c.client_id
		FROM oauth_tokens t JOIN oauth_connections c ON c.id = t.connection_id
		WHERE t.token_hash = ?`, hashToken(token)).Scan(&row)
	if res.Error != nil || res.RowsAffected == 0 || row.ClientID != clientID {
		return
	}
	_ = hint // o tipo real vem do banco; a dica não muda o resultado
	if row.Kind == "REFRESH" {
		_, _ = s.revokeConnection(ctx, s.db, row.ConnectionID, "CLIENT_REVOKE")
		return
	}
	_ = s.db.WithContext(ctx).Exec(`UPDATE oauth_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, s.now(), row.ID).Error
}
