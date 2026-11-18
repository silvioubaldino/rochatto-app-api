package oauth

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"
)

type registerRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// register: DCR (RFC 7591) de cliente público (AYD-006 §2.2). Só aceita redirect_uri da
// lista MCP_ALLOWED_REDIRECT_URIS — tokens só podem ser entregues ao Claude.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFormBytes)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "JSON inválido")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris é obrigatório")
		return
	}
	for _, uri := range req.RedirectURIs {
		if validateRedirectURI(uri) != nil || !redirectAllowed(s.cfg.AllowedRedirectURIs, uri) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri não permitido: "+uri)
			return
		}
	}
	req.ClientName = strings.TrimSpace(req.ClientName)
	if utf8.RuneCountInString(req.ClientName) > 100 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name deve ter até 100 caracteres")
		return
	}
	if m := req.TokenEndpointAuthMethod; m != "" && m != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method deve ser none")
		return
	}
	grants := []string{"authorization_code", "refresh_token"}
	if len(req.GrantTypes) > 0 {
		if !subset(req.GrantTypes, grants) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types não suportado")
			return
		}
		grants = req.GrantTypes
	}
	responses := []string{"code"}
	if len(req.ResponseTypes) > 0 {
		if !subset(req.ResponseTypes, responses) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "response_types não suportado")
			return
		}
		responses = req.ResponseTypes
	}

	ctx := r.Context()
	s.MaybeCleanup(ctx)
	// Higiene: cliente sem Connection some 24 h após o registro; teto de 50 clientes.
	s.db.WithContext(ctx).Exec(`DELETE FROM oauth_clients c WHERE c.created_at < ?
		AND NOT EXISTS (SELECT 1 FROM oauth_connections o WHERE o.client_id = c.client_id)`,
		s.now().Add(-orphanClientTTL))
	var n int64
	if err := s.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM oauth_clients`).Scan(&n).Error; err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "erro interno")
		return
	}
	if n >= maxClients {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "limite de clientes atingido")
		return
	}

	clientID := randomToken()
	now := s.now()
	var name any
	if req.ClientName != "" {
		name = req.ClientName
	}
	if err := s.db.WithContext(ctx).Exec(
		`INSERT INTO oauth_clients(client_id, client_name, redirect_uris, created_at) VALUES (?, ?, ?, ?)`,
		clientID, name, StringArray(slices.Clone(req.RedirectURIs)), now).Error; err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "erro interno")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        now.Unix(),
		"client_name":                req.ClientName,
		"redirect_uris":              req.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                grants,
		"response_types":             responses,
	})
}
