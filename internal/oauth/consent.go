package oauth

import (
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/auth"
	"gorm.io/gorm"
)

type authRequestRow struct {
	ID          uuid.UUID   `gorm:"column:id"`
	ClientID    string      `gorm:"column:client_id"`
	ClientName  *string     `gorm:"column:client_name"`
	RedirectURI string      `gorm:"column:redirect_uri"`
	State       string      `gorm:"column:state"`
	Scopes      StringArray `gorm:"column:scopes"`
	Resource    string      `gorm:"column:resource"`
	Status      string      `gorm:"column:status"`
	ExpiresAt   time.Time   `gorm:"column:expires_at"`
}

const authRequestSelect = `
	SELECT ar.id, ar.client_id, c.client_name, ar.redirect_uri, ar.state, ar.scopes, ar.resource, ar.status, ar.expires_at
	FROM oauth_authorization_requests ar JOIN oauth_clients c ON c.client_id = ar.client_id`

const msgRequestGone = "pedido de conexão expirado; refaça pelo Claude"

func clientDisplayName(n *string) string {
	if n == nil || *n == "" {
		return "Aplicativo sem nome"
	}
	return *n
}

func (s *Server) getAuthorizationRequest(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, msgRequestGone)
		return
	}
	var row authRequestRow
	res := s.db.WithContext(r.Context()).Raw(authRequestSelect+` WHERE ar.id = ?`, id).Scan(&row)
	if res.Error != nil {
		writeAPIError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if res.RowsAffected == 0 || row.Status != "PENDING" || !s.now().Before(row.ExpiresAt) {
		writeAPIError(w, http.StatusNotFound, msgRequestGone)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            row.ID,
		"client_name":   clientDisplayName(row.ClientName),
		"redirect_host": redirectHost(row.RedirectURI),
		"scopes":        []string(row.Scopes),
		"expires_at":    row.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) approveAuthorizationRequest(w http.ResponseWriter, r *http.Request) {
	s.decide(w, r, true)
}

func (s *Server) denyAuthorizationRequest(w http.ResponseWriter, r *http.Request) {
	s.decide(w, r, false)
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request, approve bool) {
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxFormBytes))
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, msgRequestGone)
		return
	}
	uid, _ := auth.UserUID(r.Context())
	email, _ := auth.UserEmail(r.Context())

	var redirectTo string
	var httpStatus int
	var msg string
	txErr := s.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var row authRequestRow
		res := tx.Raw(authRequestSelect+` WHERE ar.id = ? FOR UPDATE OF ar`, id).Scan(&row)
		if res.Error != nil {
			return res.Error
		}
		now := s.now()
		if res.RowsAffected == 0 || !now.Before(row.ExpiresAt) {
			httpStatus, msg = http.StatusNotFound, msgRequestGone
			return nil
		}
		if row.Status != "PENDING" {
			httpStatus, msg = http.StatusConflict, "pedido de conexão já decidido"
			return nil
		}
		if !approve {
			if err := tx.Exec(`UPDATE oauth_authorization_requests SET status = 'DENIED' WHERE id = ?`, id).Error; err != nil {
				return err
			}
			redirectTo = withQuery(row.RedirectURI, map[string]string{"error": "access_denied", "state": row.State})
			return nil
		}
		var connIDText string
		if err := tx.Raw(`
			INSERT INTO oauth_connections(client_id, owner_uid, owner_email, scopes, resource)
			VALUES (?, ?, ?, ?, ?) RETURNING id::text`,
			row.ClientID, uid, email, row.Scopes, row.Resource).Scan(&connIDText).Error; err != nil {
			return err
		}
		connID, err := uuid.Parse(connIDText)
		if err != nil {
			return err
		}
		code := randomToken()
		if err := tx.Exec(`
			UPDATE oauth_authorization_requests
			SET status = 'APPROVED', code_hash = ?, code_expires_at = ?, connection_id = ?
			WHERE id = ?`, hashToken(code), now.Add(codeTTL), connID, id).Error; err != nil {
			return err
		}
		redirectTo = withQuery(row.RedirectURI, map[string]string{"code": code, "state": row.State})
		return nil
	})
	if txErr != nil {
		log.Printf("ERROR oauth consent: %v", txErr)
		writeAPIError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if msg != "" {
		writeAPIError(w, httpStatus, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_to": redirectTo})
}
