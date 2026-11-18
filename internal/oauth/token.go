package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var verifierRE = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// grantError é o resultado de uma falha de grant dentro da transação (para poder fazer
// commit de efeitos como a revogação por reuso antes de responder).
type grantError struct {
	status int
	code   string
	desc   string
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "corpo inválido")
		return
	}
	f := r.PostForm
	grant := f.Get("grant_type")
	if grant != "authorization_code" && grant != "refresh_token" {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type deve ser authorization_code ou refresh_token")
		return
	}
	clientID := f.Get("client_id")
	if clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id é obrigatório")
		return
	}
	client, err := s.findClient(r, clientID)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "erro interno")
		return
	}
	if client == nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client_id desconhecido")
		return
	}
	if res := f.Get("resource"); res != "" && res != s.cfg.Resource() {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource inválido")
		return
	}

	var resp *tokenResponse
	var gerr *grantError
	txErr := s.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if grant == "authorization_code" {
			resp, gerr = s.exchangeCode(tx, clientID, f.Get("code"), f.Get("redirect_uri"), f.Get("code_verifier"))
		} else {
			resp, gerr = s.refresh(tx, clientID, f.Get("refresh_token"), f.Get("scope"))
		}
		return nil // commit sempre: efeitos de revogação por reuso precisam persistir
	})
	if txErr != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "erro interno")
		return
	}
	if gerr != nil {
		writeOAuthError(w, gerr.status, gerr.code, gerr.desc)
		return
	}
	s.MaybeCleanup(r.Context())
	writeJSON(w, http.StatusOK, resp)
}

func invalidGrant(desc string) *grantError {
	return &grantError{http.StatusBadRequest, "invalid_grant", desc}
}

func serverFail() *grantError {
	return &grantError{http.StatusInternalServerError, "server_error", "erro interno"}
}

// pkceOK confere BASE64URL(SHA256(verifier)) == challenge em tempo constante.
func pkceOK(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

// issueTokens grava o par access/refresh da Connection e monta a resposta.
func (s *Server) issueTokens(tx *gorm.DB, connID uuid.UUID, scopes, tokenScopes []string) (*tokenResponse, error) {
	now := s.now()
	access, refresh := randomToken(), randomToken()
	var ts any
	if tokenScopes != nil {
		ts = StringArray(tokenScopes)
	}
	for _, t := range []struct {
		kind, tok string
		ttl       time.Duration
	}{{"ACCESS", access, accessTokenTTL}, {"REFRESH", refresh, refreshTokenTTL}} {
		if err := tx.Exec(`INSERT INTO oauth_tokens(connection_id, kind, token_hash, scopes, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, connID, t.kind, hashToken(t.tok), ts, now.Add(t.ttl), now).Error; err != nil {
			return nil, err
		}
	}
	return &tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(accessTokenTTL / time.Second),
		RefreshToken: refresh,
		Scope:        strings.Join(scopes, " "),
	}, nil
}

// exchangeCode: authorization_code + PKCE (AYD-006 §2.5). Reuso de code revoga a Connection.
func (s *Server) exchangeCode(tx *gorm.DB, clientID, code, redirectURI, verifier string) (*tokenResponse, *grantError) {
	if code == "" || redirectURI == "" || verifier == "" {
		return nil, &grantError{http.StatusBadRequest, "invalid_request", "code, redirect_uri e code_verifier são obrigatórios"}
	}
	var row struct {
		ID            uuid.UUID   `gorm:"column:id"`
		ClientID      string      `gorm:"column:client_id"`
		RedirectURI   string      `gorm:"column:redirect_uri"`
		CodeChallenge string      `gorm:"column:code_challenge"`
		Scopes        StringArray `gorm:"column:scopes"`
		Resource      string      `gorm:"column:resource"`
		CodeExpiresAt *time.Time  `gorm:"column:code_expires_at"`
		CodeUsedAt    *time.Time  `gorm:"column:code_used_at"`
		ConnectionID  *uuid.UUID  `gorm:"column:connection_id"`
	}
	res := tx.Raw(`SELECT id, client_id, redirect_uri, code_challenge, scopes, resource, code_expires_at, code_used_at, connection_id
		FROM oauth_authorization_requests WHERE code_hash = ? AND status = 'APPROVED' FOR UPDATE`, hashToken(code)).Scan(&row)
	if res.Error != nil {
		return nil, serverFail()
	}
	if res.RowsAffected == 0 || row.ConnectionID == nil {
		return nil, invalidGrant("code inválido")
	}
	if row.CodeUsedAt != nil {
		// Reuso: invalida a Connection emitida por este code.
		_, _ = s.revokeConnection(tx.Statement.Context, tx, *row.ConnectionID, "CODE_REUSE")
		return nil, invalidGrant("code já utilizado")
	}
	now := s.now()
	if row.ClientID != clientID || row.CodeExpiresAt == nil || !now.Before(*row.CodeExpiresAt) {
		return nil, invalidGrant("code inválido ou expirado")
	}
	if row.RedirectURI != redirectURI {
		return nil, invalidGrant("redirect_uri não confere")
	}
	if !verifierRE.MatchString(verifier) || !pkceOK(verifier, row.CodeChallenge) {
		return nil, invalidGrant("code_verifier não confere")
	}
	var revoked bool
	if err := tx.Raw(`SELECT revoked_at IS NOT NULL FROM oauth_connections WHERE id = ?`, *row.ConnectionID).Scan(&revoked).Error; err != nil {
		return nil, serverFail()
	}
	if revoked {
		return nil, invalidGrant("conexão revogada")
	}
	if err := tx.Exec(`UPDATE oauth_authorization_requests SET code_used_at = ? WHERE id = ?`, now, row.ID).Error; err != nil {
		return nil, serverFail()
	}
	resp, err := s.issueTokens(tx, *row.ConnectionID, row.Scopes, nil)
	if err != nil {
		return nil, serverFail()
	}
	_ = tx.Exec(`UPDATE oauth_clients SET last_used_at = ? WHERE client_id = ?`, now, clientID).Error
	return resp, nil
}

// refresh: refresh token rotativo (AYD-006 §2.5). O refresh anterior apresentado até 30 s
// após a rotação recebe invalid_grant sem revogar; depois disso, reuso revoga a Connection.
func (s *Server) refresh(tx *gorm.DB, clientID, refreshToken, scope string) (*tokenResponse, *grantError) {
	if refreshToken == "" {
		return nil, &grantError{http.StatusBadRequest, "invalid_request", "refresh_token é obrigatório"}
	}
	var row struct {
		ID            uuid.UUID   `gorm:"column:id"`
		ConnectionID  uuid.UUID   `gorm:"column:connection_id"`
		TokenScopes   StringArray `gorm:"column:token_scopes"`
		ExpiresAt     time.Time   `gorm:"column:expires_at"`
		RotatedAt     *time.Time  `gorm:"column:rotated_at"`
		RevokedAt     *time.Time  `gorm:"column:revoked_at"`
		ClientID      string      `gorm:"column:client_id"`
		ConnScopes    StringArray `gorm:"column:conn_scopes"`
		ConnRevokedAt *time.Time  `gorm:"column:conn_revoked_at"`
	}
	res := tx.Raw(`
		SELECT t.id, t.connection_id, t.scopes AS token_scopes, t.expires_at, t.rotated_at, t.revoked_at,
		       c.client_id, c.scopes AS conn_scopes, c.revoked_at AS conn_revoked_at
		FROM oauth_tokens t JOIN oauth_connections c ON c.id = t.connection_id
		WHERE t.token_hash = ? AND t.kind = 'REFRESH' FOR UPDATE OF t`, hashToken(refreshToken)).Scan(&row)
	if res.Error != nil {
		return nil, serverFail()
	}
	if res.RowsAffected == 0 || row.ClientID != clientID {
		return nil, invalidGrant("refresh_token inválido")
	}
	now := s.now()
	if row.RotatedAt != nil {
		if now.Sub(*row.RotatedAt) > refreshGrace {
			_, _ = s.revokeConnection(tx.Statement.Context, tx, row.ConnectionID, "TOKEN_REUSE")
		}
		return nil, invalidGrant("refresh_token já utilizado")
	}
	if row.RevokedAt != nil || row.ConnRevokedAt != nil || !now.Before(row.ExpiresAt) {
		return nil, invalidGrant("refresh_token expirado ou revogado")
	}

	base := []string(row.ConnScopes)
	if row.TokenScopes != nil {
		base = row.TokenScopes
	}
	scopes, tokenScopes := base, []string(row.TokenScopes)
	if scope != "" {
		req := parseScopes(scope)
		if !subset(req, base) {
			return nil, &grantError{http.StatusBadRequest, "invalid_scope", "scope só pode reduzir os scopes da conexão"}
		}
		scopes = req
		if !subset(row.ConnScopes, req) { // reduziu de fato → o par seguinte herda a redução
			tokenScopes = req
		}
	}
	if err := tx.Exec(`UPDATE oauth_tokens SET rotated_at = ? WHERE id = ?`, now, row.ID).Error; err != nil {
		return nil, serverFail()
	}
	resp, err := s.issueTokens(tx, row.ConnectionID, scopes, tokenScopes)
	if err != nil {
		return nil, serverFail()
	}
	return resp, nil
}
