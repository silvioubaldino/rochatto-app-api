package oauth

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

type connectionRow struct {
	ID         uuid.UUID   `gorm:"column:id"`
	ClientName *string     `gorm:"column:client_name"`
	Scopes     StringArray `gorm:"column:scopes"`
	CreatedAt  time.Time   `gorm:"column:created_at"`
	LastUsedAt *time.Time  `gorm:"column:last_used_at"`
}

// listConnections: GET /api/v1/oauth/connections (AYD-006 §2.7). Só Connections não
// revogadas e já ativadas (com tokens emitidos — um code nunca trocado não é uma conexão
// em uso); ordem last_used_at desc.
func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	var rows []connectionRow
	if err := s.db.WithContext(r.Context()).Raw(`
		SELECT c.id, cl.client_name, c.scopes, c.created_at, c.last_used_at
		FROM oauth_connections c JOIN oauth_clients cl ON cl.client_id = c.client_id
		WHERE c.revoked_at IS NULL
		  AND EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.connection_id = c.id)
		ORDER BY c.last_used_at DESC NULLS LAST, c.created_at DESC`).Scan(&rows).Error; err != nil {
		writeAPIError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		var last any
		if c.LastUsedAt != nil {
			last = c.LastUsedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]any{
			"id":           c.ID,
			"client_name":  clientDisplayName(c.ClientName),
			"scopes":       []string(c.Scopes),
			"created_at":   c.CreatedAt.UTC().Format(time.RFC3339),
			"last_used_at": last,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteConnection: DELETE /api/v1/oauth/connections/{id} → 204 (revoga a Connection; o
// próximo uso no Claude dá 401). 404 se inexistente ou já revogada.
func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "conexão não encontrada")
		return
	}
	ok, err := s.revokeConnection(r.Context(), s.db, id, "USER")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if !ok {
		writeAPIError(w, http.StatusNotFound, "conexão não encontrada")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
