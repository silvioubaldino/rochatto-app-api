package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/silvioubaldino/sales-backend/internal/auth"
	"gorm.io/gorm"
)

// Constantes do contrato (AYD-006 §2).
const (
	ScopeRead    = "read"
	ScopeWrite   = "write"
	scopeOffline = "offline_access"

	accessTokenTTL     = time.Hour
	refreshTokenTTL    = 30 * 24 * time.Hour
	refreshGrace       = 30 * time.Second
	authRequestTTL     = 10 * time.Minute
	codeTTL            = 5 * time.Minute
	maxClients         = 50
	orphanClientTTL    = 24 * time.Hour
	lastUsedResolution = time.Minute
	maxFormBytes       = 64 << 10
)

// Server é o servidor de autorização. Use RegisterPublic (raiz) e RegisterAPI (/api/v1).
type Server struct {
	cfg   Config
	db    *gorm.DB
	allow auth.Allowlist
	now   func() time.Time

	lastCleanup atomic.Int64
}

// New cria o servidor. A allowlist é a mesma do middleware Firebase (rechecada no /mcp).
func New(db *gorm.DB, cfg Config, allow auth.Allowlist) *Server {
	return &Server{cfg: cfg, db: db, allow: allow, now: time.Now}
}

// Config devolve a configuração em uso.
func (s *Server) Config() Config { return s.cfg }

// RegisterPublic monta discovery e /oauth/* no mux raiz (sem auth Firebase).
func (s *Server) RegisterPublic(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authorizationServer)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("POST /oauth/revoke", s.revoke)
}

// RegisterAPI monta a API de consentimento e de Connections (consumida pelo web). O mux
// recebido fica atrás do middleware Firebase + allowlist, como todo /api/v1.
func (s *Server) RegisterAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /oauth/authorization-requests/{id}", s.getAuthorizationRequest)
	mux.HandleFunc("POST /oauth/authorization-requests/{id}/approve", s.approveAuthorizationRequest)
	mux.HandleFunc("POST /oauth/authorization-requests/{id}/deny", s.denyAuthorizationRequest)
	mux.HandleFunc("GET /oauth/connections", s.listConnections)
	mux.HandleFunc("DELETE /oauth/connections/{id}", s.deleteConnection)
}

// --- helpers ---

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken devolve o SHA-256 (hex) — tokens e codes só são guardados assim.
func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type oauthError struct {
	Code string `json:"error"`
	Desc string `json:"error_description,omitempty"`
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, oauthError{Code: code, Desc: desc})
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// parseScopes separa a lista "a b c".
func parseScopes(raw string) []string { return strings.Fields(raw) }

// normalizeScopes valida e normaliza os scopes pedidos no authorize: só read/write/
// offline_access; offline_access é aceito e descartado; ausente (ou só offline_access)
// = read write. Devolve ok=false para scope desconhecido.
func normalizeScopes(requested []string) ([]string, bool) {
	var hasRead, hasWrite bool
	for _, sc := range requested {
		switch sc {
		case ScopeRead:
			hasRead = true
		case ScopeWrite:
			hasWrite = true
		case scopeOffline:
		default:
			return nil, false
		}
	}
	if !hasRead && !hasWrite {
		return []string{ScopeRead, ScopeWrite}, true
	}
	var out []string
	if hasRead {
		out = append(out, ScopeRead)
	}
	if hasWrite {
		out = append(out, ScopeWrite)
	}
	return out, true
}

func subset(sub, of []string) bool {
	for _, s := range sub {
		if !slices.Contains(of, s) {
			return false
		}
	}
	return true
}
