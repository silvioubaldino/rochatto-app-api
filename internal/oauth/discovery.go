package oauth

import "net/http"

func discoveryHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func (s *Server) protectedResource(w http.ResponseWriter, r *http.Request) {
	discoveryHeaders(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.cfg.Resource(),
		"authorization_servers":    []string{s.cfg.PublicBaseURL},
		"scopes_supported":         []string{ScopeRead, ScopeWrite},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Rochatto",
	})
}

func (s *Server) authorizationServer(w http.ResponseWriter, r *http.Request) {
	discoveryHeaders(w)
	base := s.cfg.PublicBaseURL
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     base,
		"authorization_endpoint":                     base + "/oauth/authorize",
		"token_endpoint":                             base + "/oauth/token",
		"registration_endpoint":                      base + "/oauth/register",
		"revocation_endpoint":                        base + "/oauth/revoke",
		"response_types_supported":                   []string{"code"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none"},
		"revocation_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                           []string{ScopeRead, ScopeWrite, scopeOffline},
	})
}
