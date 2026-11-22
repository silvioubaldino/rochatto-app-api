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

type Server struct {
	cfg   Config
	db    *gorm.DB
	allow auth.Allowlist
	now   func() time.Time

	lastCleanup atomic.Int64
}

func New(db *gorm.DB, cfg Config, allow auth.Allowlist) *Server {
	return &Server{cfg: cfg, db: db, allow: allow, now: time.Now}
}

func (s *Server) Config() Config { return s.cfg }

func (s *Server) RegisterPublic(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authorizationServer)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("POST /oauth/revoke", s.revoke)
}

func (s *Server) RegisterAPI(mux *http.ServeMux) {
	mux.HandleFunc("GET /oauth/authorization-requests/{id}", s.getAuthorizationRequest)
	mux.HandleFunc("POST /oauth/authorization-requests/{id}/approve", s.approveAuthorizationRequest)
	mux.HandleFunc("POST /oauth/authorization-requests/{id}/deny", s.denyAuthorizationRequest)
	mux.HandleFunc("GET /oauth/connections", s.listConnections)
	mux.HandleFunc("DELETE /oauth/connections/{id}", s.deleteConnection)
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

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

func parseScopes(raw string) []string { return strings.Fields(raw) }

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
