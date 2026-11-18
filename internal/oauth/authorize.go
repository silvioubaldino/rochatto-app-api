package oauth

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var pkceChallengeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`) // BASE64URL(SHA-256) sem padding

type clientRow struct {
	ClientID     string      `gorm:"column:client_id"`
	ClientName   *string     `gorm:"column:client_name"`
	RedirectURIs StringArray `gorm:"column:redirect_uris"`
}

func (s *Server) findClient(r *http.Request, clientID string) (*clientRow, error) {
	var c clientRow
	res := s.db.WithContext(r.Context()).Raw(
		`SELECT client_id, client_name, redirect_uris FROM oauth_clients WHERE client_id = ?`, clientID).Scan(&c)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &c, nil
}

// redirectError responde 302 para redirect_uri?error=...&state=... (RFC 6749 §4.1.2.1).
func redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	http.Redirect(w, r, withQuery(redirectURI, map[string]string{"error": code, "error_description": desc, "state": state}), http.StatusFound)
}

// withQuery acrescenta parâmetros (ignorando vazios) preservando a query já existente.
func withQuery(rawURL string, params map[string]string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// authorize: GET /oauth/authorize (AYD-006 §2.3). Valida em duas camadas e, em sucesso,
// cria o authorization request e redireciona ao app web (/conectar?request_id=…).
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	invalidLink := func() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("link de conexão inválido"))
	}

	// Camada 1: nunca redireciona.
	clientID, redirectURI := q.Get("client_id"), q.Get("redirect_uri")
	if clientID == "" || redirectURI == "" {
		invalidLink()
		return
	}
	client, err := s.findClient(r, clientID)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("erro interno"))
		return
	}
	if client == nil || !registeredRedirect(client.RedirectURIs, redirectURI) {
		invalidLink()
		return
	}

	// Camada 2: 302 para o redirect_uri.
	state := q.Get("state")
	if q.Get("response_type") != "code" {
		redirectError(w, r, redirectURI, state, "unsupported_response_type", "response_type deve ser code")
		return
	}
	challenge := q.Get("code_challenge")
	if challenge == "" || !pkceChallengeRE.MatchString(challenge) {
		redirectError(w, r, redirectURI, state, "invalid_request", "code_challenge ausente ou inválido")
		return
	}
	if q.Get("code_challenge_method") != "S256" {
		redirectError(w, r, redirectURI, state, "invalid_request", "code_challenge_method deve ser S256")
		return
	}
	if state == "" {
		redirectError(w, r, redirectURI, "", "invalid_request", "state é obrigatório")
		return
	}
	scopes, ok := normalizeScopes(parseScopes(q.Get("scope")))
	if !ok {
		redirectError(w, r, redirectURI, state, "invalid_scope", "scope deve ser read, write ou offline_access")
		return
	}
	if res := q.Get("resource"); res != "" && res != s.cfg.Resource() {
		redirectError(w, r, redirectURI, state, "invalid_target", "resource inválido")
		return
	}

	var id string
	if err := s.db.WithContext(r.Context()).Raw(`
		INSERT INTO oauth_authorization_requests(client_id, redirect_uri, state, code_challenge, scopes, resource, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id::text`,
		clientID, redirectURI, state, challenge, StringArray(scopes), s.cfg.Resource(),
		s.now().Add(authRequestTTL)).Scan(&id).Error; err != nil || id == "" {
		redirectError(w, r, redirectURI, state, "server_error", "erro interno")
		return
	}
	http.Redirect(w, r, s.cfg.WebAppURL+"/conectar?request_id="+url.QueryEscape(id), http.StatusFound)
}

func registeredRedirect(registered []string, given string) bool {
	for _, reg := range registered {
		if redirectMatches(reg, given) {
			return true
		}
	}
	return false
}

// redirectHost devolve o host (com porta) do redirect_uri, para o dono conferir no consentimento.
func redirectHost(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return strings.TrimSpace(uri)
	}
	return u.Host
}
