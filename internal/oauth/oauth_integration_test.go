package oauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silvioubaldino/sales-backend/internal/auth"
	"github.com/silvioubaldino/sales-backend/internal/testdb"
	"gorm.io/gorm"
)

const (
	tBase    = "https://api.test"
	tWeb     = "https://app.test"
	tOwner   = "dono@example.com"
	tVerif   = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	tCallbak = DefaultRedirectURI
)

type harness struct {
	t     *testing.T
	db    *gorm.DB
	s     *Server
	root  *http.ServeMux
	api   http.Handler
	clock time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := testdb.New(t)
	h := &harness{t: t, db: db, clock: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	cfg := Config{Enabled: true, PublicBaseURL: tBase, WebAppURL: tWeb, AllowedRedirectURIs: []string{DefaultRedirectURI}}
	h.s = New(db, cfg, auth.ParseAllowlist(tOwner))
	h.s.now = func() time.Time { return h.clock }
	h.s.lastCleanup.Store(1 << 60)
	h.root = http.NewServeMux()
	h.s.RegisterPublic(h.root)
	apiMux := http.NewServeMux()
	h.s.RegisterAPI(apiMux)
	h.api = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiMux.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), "uid-dono", tOwner)))
	})
	return h
}

func (h *harness) advance(d time.Duration) { h.clock = h.clock.Add(d) }

func (h *harness) serve(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func (h *harness) jsonBody(rec *httptest.ResponseRecorder) map[string]any {
	h.t.Helper()
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return m
}

func (h *harness) form(path string, v url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return h.serve(h.root, req)
}

func (h *harness) register(redirect string) (int, map[string]any) {
	body, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirect}, "client_name": "Claude"})
	req := httptest.NewRequest("POST", "/oauth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := h.serve(h.root, req)
	return rec.Code, h.jsonBody(rec)
}

func (h *harness) clientID() string {
	h.t.Helper()
	code, m := h.register(tCallbak)
	if code != 201 {
		h.t.Fatalf("register = %d %v", code, m)
	}
	return m["client_id"].(string)
}

func challengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func authorizeURL(clientID string, extra map[string]string) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {tCallbak},
		"code_challenge": {challengeOf(tVerif)}, "code_challenge_method": {"S256"},
		"state": {"st4te"}, "scope": {"read write offline_access"}, "resource": {tBase + "/mcp"},
	}
	for k, v := range extra {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	return "/oauth/authorize?" + q.Encode()
}

func (h *harness) authorizeRequest(clientID string) string {
	h.t.Helper()
	rec := h.serve(h.root, httptest.NewRequest("GET", authorizeURL(clientID, nil), nil))
	loc := rec.Header().Get("Location")
	if rec.Code != 302 || !strings.HasPrefix(loc, tWeb+"/conectar?request_id=") {
		h.t.Fatalf("authorize = %d Location=%q", rec.Code, loc)
	}
	u, _ := url.Parse(loc)
	return u.Query().Get("request_id")
}

func (h *harness) decide(reqID, action, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/oauth/authorization-requests/"+reqID+"/"+action, strings.NewReader(body))
	return h.serve(h.api, req)
}

func (h *harness) approve(reqID string) string {
	h.t.Helper()
	rec := h.decide(reqID, "approve", "{}")
	if rec.Code != 200 {
		h.t.Fatalf("approve = %d %s", rec.Code, rec.Body.String())
	}
	u, err := url.Parse(h.jsonBody(rec)["redirect_to"].(string))
	if err != nil || u.Host != "claude.ai" || u.Query().Get("state") != "st4te" || u.Query().Get("code") == "" {
		h.t.Fatalf("redirect_to inválido: %s", rec.Body.String())
	}
	return u.Query().Get("code")
}

func (h *harness) exchange(clientID, code, verifier string) *httptest.ResponseRecorder {
	return h.form("/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {tCallbak},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {tBase + "/mcp"},
	})
}

type tokens struct{ client, access, refresh string }

func (h *harness) connect() tokens {
	h.t.Helper()
	cid := h.clientID()
	code := h.approve(h.authorizeRequest(cid))
	rec := h.exchange(cid, code, tVerif)
	if rec.Code != 200 {
		h.t.Fatalf("token = %d %s", rec.Code, rec.Body.String())
	}
	m := h.jsonBody(rec)
	return tokens{cid, m["access_token"].(string), m["refresh_token"].(string)}
}

func (h *harness) refresh(tk tokens, extra url.Values) *httptest.ResponseRecorder {
	v := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tk.refresh}, "client_id": {tk.client}}
	for k, vals := range extra {
		v[k] = vals
	}
	return h.form("/oauth/token", v)
}

func (h *harness) valid(access string) bool {
	_, err := h.s.VerifyAccessToken(h.t.Context(), access)
	return err == nil
}

func (h *harness) errCode(rec *httptest.ResponseRecorder, status int) string {
	h.t.Helper()
	if rec.Code != status {
		h.t.Fatalf("status = %d (%s), quero %d", rec.Code, rec.Body.String(), status)
	}
	return h.jsonBody(rec)["error"].(string)
}

func TestIntegration_Discovery(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
		rec := h.serve(h.root, httptest.NewRequest("GET", p, nil))
		m := h.jsonBody(rec)
		if rec.Code != 200 || m["resource"] != tBase+"/mcp" || m["authorization_servers"].([]any)[0] != tBase {
			t.Errorf("%s = %d %v", p, rec.Code, m)
		}
		if rec.Header().Get("Cache-Control") != "public, max-age=3600" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s: cabeçalhos %v", p, rec.Header())
		}
		if !slices.Equal(anyStrings(m["scopes_supported"]), []string{"read", "write"}) || m["resource_name"] != "Rochatto" {
			t.Errorf("%s: %v", p, m)
		}
	}
	m := h.jsonBody(h.serve(h.root, httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)))
	want := map[string]any{
		"issuer": tBase, "authorization_endpoint": tBase + "/oauth/authorize", "token_endpoint": tBase + "/oauth/token",
		"registration_endpoint": tBase + "/oauth/register", "revocation_endpoint": tBase + "/oauth/revoke",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, quero %v", k, m[k], v)
		}
	}
	if !slices.Equal(anyStrings(m["code_challenge_methods_supported"]), []string{"S256"}) ||
		!slices.Equal(anyStrings(m["token_endpoint_auth_methods_supported"]), []string{"none"}) ||
		!slices.Equal(anyStrings(m["grant_types_supported"]), []string{"authorization_code", "refresh_token"}) {
		t.Errorf("metadata: %v", m)
	}
	if _, ok := m["client_id_metadata_document_supported"]; ok {
		t.Error("não deve anunciar CIMD na v1")
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestIntegration_FullConnectionFlow(t *testing.T) {
	h := newHarness(t)

	code, m := h.register(tCallbak)
	if code != 201 || m["token_endpoint_auth_method"] != "none" || m["client_id"] == "" || m["client_name"] != "Claude" {
		t.Fatalf("register = %d %v", code, m)
	}
	cid := m["client_id"].(string)

	reqID := h.authorizeRequest(cid)
	rec := h.serve(h.api, httptest.NewRequest("GET", "/oauth/authorization-requests/"+reqID, nil))
	c := h.jsonBody(rec)
	if rec.Code != 200 || c["client_name"] != "Claude" || c["redirect_host"] != "claude.ai" ||
		!slices.Equal(anyStrings(c["scopes"]), []string{"read", "write"}) || c["expires_at"] != "2026-11-18T12:10:00Z" {
		t.Fatalf("consent GET = %d %v", rec.Code, c)
	}

	code2 := h.approve(reqID)
	if rec := h.decide(reqID, "approve", ""); rec.Code != 409 {
		t.Errorf("2ª aprovação = %d, quero 409 (já decidido)", rec.Code)
	}
	if rec := h.decide(reqID, "deny", ""); rec.Code != 409 {
		t.Errorf("deny após approve = %d, quero 409", rec.Code)
	}
	if rec := h.serve(h.api, httptest.NewRequest("GET", "/oauth/authorization-requests/"+reqID, nil)); rec.Code != 404 {
		t.Errorf("GET após decidido = %d, quero 404", rec.Code)
	}

	rec = h.exchange(cid, code2, tVerif)
	tok := h.jsonBody(rec)
	if rec.Code != 200 || tok["token_type"] != "Bearer" || tok["expires_in"] != float64(3600) ||
		tok["scope"] != "read write" || tok["access_token"] == "" || tok["refresh_token"] == "" {
		t.Fatalf("token = %d %v", rec.Code, tok)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("token sem Cache-Control: no-store")
	}

	p, err := h.s.VerifyAccessToken(t.Context(), tok["access_token"].(string))
	if err != nil || p.OwnerEmail != tOwner || p.OwnerUID != "uid-dono" || !slices.Equal(p.Scopes, []string{"read", "write"}) ||
		p.ClientName != "Claude" || !p.ExpiresAt.Equal(h.clock.Add(time.Hour)) {
		t.Fatalf("principal = %+v err=%v", p, err)
	}

	var n int64
	h.db.Raw(`SELECT COUNT(*) FROM oauth_tokens WHERE token_hash IN (?, ?)`, tok["access_token"], tok["refresh_token"]).Scan(&n)
	h.db.Raw(`SELECT COUNT(*) FROM oauth_authorization_requests WHERE code_hash = ?`, code2).Scan(&n)
	if n != 0 {
		t.Error("token/code em texto puro no banco")
	}
	h.db.Raw(`SELECT COUNT(*) FROM oauth_tokens WHERE token_hash = ?`, hashToken(tok["access_token"].(string))).Scan(&n)
	if n != 1 {
		t.Error("access token deveria estar guardado como hash")
	}
}

func TestIntegration_DynamicClientRegistration(t *testing.T) {
	h := newHarness(t)
	post := func(body string) (int, map[string]any) {
		req := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(body))
		rec := h.serve(h.root, req)
		return rec.Code, h.jsonBody(rec)
	}
	cases := []struct{ name, body, wantErr string }{
		{"redirect fora da lista", `{"redirect_uris":["https://evil.example/cb"]}`, "invalid_redirect_uri"},
		{"sem redirect_uris", `{"client_name":"x"}`, "invalid_redirect_uri"},
		{"http não-loopback", `{"redirect_uris":["http://claude.ai/api/mcp/auth_callback"]}`, "invalid_redirect_uri"},
		{"auth method com secret", `{"redirect_uris":["` + tCallbak + `"],"token_endpoint_auth_method":"client_secret_basic"}`, "invalid_client_metadata"},
		{"grant não suportado", `{"redirect_uris":["` + tCallbak + `"],"grant_types":["client_credentials"]}`, "invalid_client_metadata"},
		{"client_name longo", `{"redirect_uris":["` + tCallbak + `"],"client_name":"` + strings.Repeat("a", 101) + `"}`, "invalid_client_metadata"},
		{"JSON inválido", `{`, "invalid_client_metadata"},
	}
	for _, c := range cases {
		if code, m := post(c.body); code != 400 || m["error"] != c.wantErr {
			t.Errorf("%s: %d %v, quero 400 %s", c.name, code, m, c.wantErr)
		}
	}
	code, m := post(`{"redirect_uris":["` + tCallbak + `"],"grant_types":["authorization_code"]}`)
	if code != 201 || !slices.Equal(anyStrings(m["grant_types"]), []string{"authorization_code"}) {
		t.Errorf("subconjunto de grant_types: %d %v", code, m)
	}
}

func TestIntegration_ClientCapAndOrphanCleanup(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < maxClients; i++ {
		if code, m := h.register(tCallbak); code != 201 {
			t.Fatalf("registro %d = %d %v", i, code, m)
		}
	}
	code, m := h.register(tCallbak)
	if code != 400 || m["error"] != "invalid_client_metadata" || !strings.Contains(m["error_description"].(string), "limite de clientes") {
		t.Fatalf("51º registro = %d %v", code, m)
	}
	h.db.Exec(`UPDATE oauth_clients SET created_at = ?`, h.clock.Add(-25*time.Hour))
	tk := h.connect()
	var n int64
	h.db.Raw(`SELECT COUNT(*) FROM oauth_clients`).Scan(&n)
	if n != 1 {
		t.Errorf("clientes restantes = %d, quero 1 (órfãos de 25 h apagados)", n)
	}
	h.advance(48 * time.Hour)
	h.s.Cleanup(t.Context())
	h.db.Raw(`SELECT COUNT(*) FROM oauth_clients WHERE client_id = ?`, tk.client).Scan(&n)
	if n != 1 {
		t.Error("cliente com Connection não pode ser apagado")
	}
}

func TestIntegration_AuthorizeValidation(t *testing.T) {
	h := newHarness(t)
	cid := h.clientID()
	get := func(u string) *httptest.ResponseRecorder { return h.serve(h.root, httptest.NewRequest("GET", u, nil)) }

	// Camada 1: 400 texto simples, NUNCA redireciona.
	for name, u := range map[string]string{
		"client desconhecido":     authorizeURL("naoexiste", nil),
		"redirect não registrado": authorizeURL(cid, map[string]string{"redirect_uri": "https://evil.example/cb"}),
		"sem client_id":           authorizeURL("", nil),
		"sem redirect_uri":        authorizeURL(cid, map[string]string{"redirect_uri": ""}),
	} {
		rec := get(u)
		if rec.Code != 400 || rec.Header().Get("Location") != "" || !strings.Contains(rec.Body.String(), "link de conexão inválido") ||
			!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
			t.Errorf("%s: %d loc=%q body=%q", name, rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
	}

	cases := []struct {
		name, wantErr string
		extra         map[string]string
	}{
		{"response_type", "unsupported_response_type", map[string]string{"response_type": "token"}},
		{"sem code_challenge", "invalid_request", map[string]string{"code_challenge": ""}},
		{"método plain", "invalid_request", map[string]string{"code_challenge_method": "plain"}},
		{"sem state", "invalid_request", map[string]string{"state": ""}},
		{"scope inválido", "invalid_scope", map[string]string{"scope": "read admin"}},
		{"resource inválido", "invalid_target", map[string]string{"resource": "https://outra.example/mcp"}},
	}
	for _, c := range cases {
		rec := get(authorizeURL(cid, c.extra))
		loc, err := url.Parse(rec.Header().Get("Location"))
		if rec.Code != 302 || err != nil || loc.Host != "claude.ai" || loc.Query().Get("error") != c.wantErr {
			t.Errorf("%s: %d %q", c.name, rec.Code, rec.Header().Get("Location"))
			continue
		}
		if c.name != "sem state" && loc.Query().Get("state") != "st4te" {
			t.Errorf("%s: state não devolvido", c.name)
		}
	}

	rec := get(authorizeURL(cid, map[string]string{"scope": "", "resource": ""}))
	if rec.Code != 302 || !strings.HasPrefix(rec.Header().Get("Location"), tWeb+"/conectar?request_id=") {
		t.Fatalf("scope/resource ausentes = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	u, _ := url.Parse(rec.Header().Get("Location"))
	rec = h.serve(h.api, httptest.NewRequest("GET", "/oauth/authorization-requests/"+u.Query().Get("request_id"), nil))
	if !slices.Equal(anyStrings(h.jsonBody(rec)["scopes"]), []string{"read", "write"}) {
		t.Errorf("scope default = %v", h.jsonBody(rec))
	}
}

func TestIntegration_ConsentLifecycle(t *testing.T) {
	h := newHarness(t)
	cid := h.clientID()

	id := h.authorizeRequest(cid)
	rec := h.decide(id, "deny", "{}")
	loc, _ := url.Parse(h.jsonBody(rec)["redirect_to"].(string))
	if rec.Code != 200 || loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") != "st4te" || loc.Query().Get("code") != "" {
		t.Fatalf("deny = %d %s", rec.Code, rec.Body.String())
	}
	var n int64
	h.db.Raw(`SELECT COUNT(*) FROM oauth_connections`).Scan(&n)
	if n != 0 {
		t.Error("negar não deve criar Connection")
	}

	for _, p := range []string{"/oauth/authorization-requests/00000000-0000-0000-0000-000000000000", "/oauth/authorization-requests/xyz"} {
		if rec := h.serve(h.api, httptest.NewRequest("GET", p, nil)); rec.Code != 404 {
			t.Errorf("GET %s = %d", p, rec.Code)
		}
	}
	id = h.authorizeRequest(cid)
	h.advance(10*time.Minute + time.Second)
	if rec := h.serve(h.api, httptest.NewRequest("GET", "/oauth/authorization-requests/"+id, nil)); rec.Code != 404 {
		t.Errorf("GET expirado = %d", rec.Code)
	}
	if rec := h.decide(id, "approve", ""); rec.Code != 404 {
		t.Errorf("approve expirado = %d, quero 404", rec.Code)
	}
	if rec := h.decide(id, "approve", "lixo não-JSON"); rec.Code != 404 {
		t.Errorf("corpo é ignorado: %d", rec.Code)
	}
}

func TestIntegration_TokenExchange(t *testing.T) {
	h := newHarness(t)
	cid := h.clientID()
	newCode := func() string { return h.approve(h.authorizeRequest(cid)) }

	code := newCode()
	if e := h.errCode(h.exchange(cid, code, tVerif+"x"), 400); e != "invalid_grant" {
		t.Errorf("PKCE inválido = %s", e)
	}
	var n int64
	h.db.Raw(`SELECT COUNT(*) FROM oauth_tokens`).Scan(&n)
	if n != 0 {
		t.Errorf("tokens emitidos com PKCE inválido: %d", n)
	}
	if rec := h.exchange(cid, code, tVerif); rec.Code != 200 {
		t.Fatalf("troca válida = %d %s", rec.Code, rec.Body.String())
	}

	access := h.jsonBody(h.exchange(cid, newCode(), tVerif))["access_token"].(string)
	_ = access
	reused := newCode()
	first := h.jsonBody(h.exchange(cid, reused, tVerif))
	if !h.valid(first["access_token"].(string)) {
		t.Fatal("token recém-emitido deveria valer")
	}
	if e := h.errCode(h.exchange(cid, reused, tVerif), 400); e != "invalid_grant" {
		t.Errorf("reuso do code = %s", e)
	}
	if h.valid(first["access_token"].(string)) {
		t.Error("reuso de code deve revogar a Connection (access token deixa de valer)")
	}
	var reason string
	h.db.Raw(`SELECT revoked_reason FROM oauth_connections WHERE revoked_at IS NOT NULL`).Scan(&reason)
	if reason != "CODE_REUSE" {
		t.Errorf("revoked_reason = %q", reason)
	}

	code = newCode()
	bad := func(name string, v url.Values, status int, want string) {
		t.Helper()
		if e := h.errCode(h.form("/oauth/token", v), status); e != want {
			t.Errorf("%s: %s, quero %s", name, e, want)
		}
	}
	base := func() url.Values {
		return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {tCallbak}, "client_id": {cid}, "code_verifier": {tVerif}}
	}
	set := func(k, v string) url.Values { x := base(); x.Set(k, v); return x }
	bad("grant_type", set("grant_type", "client_credentials"), 400, "unsupported_grant_type")
	bad("client desconhecido", set("client_id", "naoexiste"), 401, "invalid_client")
	bad("redirect_uri diferente", set("redirect_uri", "https://claude.ai/outro"), 400, "invalid_grant")
	bad("resource", set("resource", "https://outra.example/mcp"), 400, "invalid_target")
	bad("code inexistente", set("code", "nao-existe"), 400, "invalid_grant")
	bad("sem verifier", set("code_verifier", ""), 400, "invalid_request")
	other := h.clientID()
	bad("code de outro cliente", set("client_id", other), 400, "invalid_grant")
	h.advance(5*time.Minute + time.Second)
	bad("code expirado (5 min)", base(), 400, "invalid_grant")
}

func TestIntegration_RefreshRotationAndReuseDetection(t *testing.T) {
	h := newHarness(t)
	tk := h.connect()

	rec := h.refresh(tk, nil)
	if rec.Code != 200 {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body.String())
	}
	m := h.jsonBody(rec)
	r2, a2 := m["refresh_token"].(string), m["access_token"].(string)
	if r2 == tk.refresh || a2 == tk.access || m["expires_in"] != float64(3600) || m["scope"] != "read write" {
		t.Fatalf("par não rotacionou: %v", m)
	}

	h.advance(20 * time.Second)
	if e := h.errCode(h.refresh(tk, nil), 400); e != "invalid_grant" {
		t.Errorf("R1 na janela de 30 s = %s", e)
	}
	if !h.valid(a2) {
		t.Fatal("a corrida não pode revogar a Connection")
	}
	rec = h.refresh(tokens{client: tk.client, refresh: r2}, nil)
	if rec.Code != 200 {
		t.Fatalf("R2 deveria funcionar: %d %s", rec.Code, rec.Body.String())
	}
	r3, a3 := h.jsonBody(rec)["refresh_token"].(string), h.jsonBody(rec)["access_token"].(string)

	h.advance(31 * time.Second)
	if e := h.errCode(h.refresh(tk, nil), 400); e != "invalid_grant" {
		t.Errorf("R1 após 30 s = %s", e)
	}
	if h.valid(a3) || h.valid(a2) {
		t.Error("reuso de refresh deve revogar a Connection (access tokens deixam de valer)")
	}
	if e := h.errCode(h.refresh(tokens{client: tk.client, refresh: r3}, nil), 400); e != "invalid_grant" {
		t.Errorf("R3 após revogação = %s", e)
	}
	var reason string
	h.db.Raw(`SELECT revoked_reason FROM oauth_connections`).Scan(&reason)
	if reason != "TOKEN_REUSE" {
		t.Errorf("revoked_reason = %q", reason)
	}
}

func TestIntegration_RefreshErrorsExpiryAndScopes(t *testing.T) {
	h := newHarness(t)
	tk := h.connect()

	if e := h.errCode(h.form("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"nada"}, "client_id": {tk.client}}), 400); e != "invalid_grant" {
		t.Errorf("refresh inexistente = %s", e)
	}
	if e := h.errCode(h.refresh(tokens{client: "naoexiste", refresh: tk.refresh}, nil), 401); e != "invalid_client" {
		t.Errorf("client desconhecido = %s", e)
	}
	if e := h.errCode(h.refresh(tokens{client: h.clientID(), refresh: tk.refresh}, nil), 400); e != "invalid_grant" {
		t.Errorf("refresh de outro cliente = %s", e)
	}
	if e := h.errCode(h.refresh(tk, url.Values{"resource": {"https://outra.example/mcp"}}), 400); e != "invalid_target" {
		t.Errorf("resource = %s", e)
	}
	if e := h.errCode(h.refresh(tk, url.Values{"scope": {"read admin"}}), 400); e != "invalid_scope" {
		t.Errorf("scope inválido = %s", e)
	}

	rec := h.refresh(tk, url.Values{"scope": {"read"}})
	m := h.jsonBody(rec)
	if rec.Code != 200 || m["scope"] != "read" {
		t.Fatalf("refresh reduzido = %d %v", rec.Code, m)
	}
	p, err := h.s.VerifyAccessToken(t.Context(), m["access_token"].(string))
	if err != nil || !slices.Equal(p.Scopes, []string{"read"}) {
		t.Fatalf("access reduzido: %+v %v", p, err)
	}
	tk2 := tokens{tk.client, m["access_token"].(string), m["refresh_token"].(string)}
	if e := h.errCode(h.refresh(tk2, url.Values{"scope": {"read write"}}), 400); e != "invalid_scope" {
		t.Errorf("re-ampliar scope = %s", e)
	}
	rec = h.refresh(tk2, nil)
	if rec.Code != 200 || h.jsonBody(rec)["scope"] != "read" {
		t.Errorf("herança da redução: %d %v", rec.Code, h.jsonBody(rec))
	}

	tk3 := h.connect()
	h.advance(29 * 24 * time.Hour)
	rec = h.refresh(tk3, nil)
	if rec.Code != 200 {
		t.Fatalf("refresh aos 29 dias = %d", rec.Code)
	}
	tk3 = tokens{tk3.client, "", h.jsonBody(rec)["refresh_token"].(string)}
	h.advance(29 * 24 * time.Hour)
	rec = h.refresh(tk3, nil)
	if rec.Code != 200 {
		t.Fatalf("janela deslizante: refresh = %d %s", rec.Code, rec.Body.String())
	}
	tk3.refresh = h.jsonBody(rec)["refresh_token"].(string)
	h.advance(30*24*time.Hour + time.Second)
	if e := h.errCode(h.refresh(tk3, nil), 400); e != "invalid_grant" {
		t.Errorf("refresh após 30 dias sem uso = %s", e)
	}
}

func TestIntegration_AccessTokenLifetimeAndRevocation(t *testing.T) {
	h := newHarness(t)
	tk := h.connect()
	if !h.valid(tk.access) {
		t.Fatal("token novo deveria valer")
	}
	h.advance(time.Hour - time.Second)
	if !h.valid(tk.access) {
		t.Error("token vale por 1 h")
	}
	h.advance(2 * time.Second)
	if h.valid(tk.access) {
		t.Error("token expira em 1 h")
	}
	if h.valid("") || h.valid("lixo") {
		t.Error("token desconhecido não vale")
	}
}

func TestIntegration_RevokeEndpoint(t *testing.T) {
	h := newHarness(t)
	revoke := func(tok, hint, client string) {
		t.Helper()
		rec := h.form("/oauth/revoke", url.Values{"token": {tok}, "token_type_hint": {hint}, "client_id": {client}})
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "{}" {
			t.Fatalf("revoke = %d %s (sempre 200 {})", rec.Code, rec.Body.String())
		}
	}
	tk := h.connect()
	revoke("desconhecido", "access_token", tk.client)

	revoke(tk.access, "access_token", tk.client)
	if h.valid(tk.access) {
		t.Error("access revogado ainda vale")
	}
	rec := h.refresh(tk, nil)
	if rec.Code != 200 {
		t.Fatalf("refresh após revogar só o access = %d", rec.Code)
	}
	newAccess := h.jsonBody(rec)["access_token"].(string)
	newRefresh := h.jsonBody(rec)["refresh_token"].(string)

	revoke(newRefresh, "refresh_token", h.clientID())
	if !h.valid(newAccess) {
		t.Error("revogação por outro cliente não pode valer")
	}
	revoke(newRefresh, "refresh_token", tk.client)
	if h.valid(newAccess) {
		t.Error("revogar o refresh deve revogar a Connection (todos os tokens)")
	}
	var reason string
	h.db.Raw(`SELECT revoked_reason FROM oauth_connections`).Scan(&reason)
	if reason != "CLIENT_REVOKE" {
		t.Errorf("revoked_reason = %q", reason)
	}
}

func TestIntegration_ConnectionsAPI(t *testing.T) {
	h := newHarness(t)
	list := func() []any {
		rec := h.serve(h.api, httptest.NewRequest("GET", "/oauth/connections", nil))
		var out []any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 200 {
			t.Fatalf("GET connections = %d", rec.Code)
		}
		return out
	}
	if got := list(); got == nil || len(got) != 0 {
		t.Fatalf("lista vazia deve ser [] (não null): %v", got)
	}
	// Code aprovado mas nunca trocado não é uma conexão em uso.
	cid := h.clientID()
	h.approve(h.authorizeRequest(cid))
	if len(list()) != 0 {
		t.Error("Connection sem tokens não deve aparecer")
	}

	older := h.connect()
	h.advance(time.Minute)
	newer := h.connect()
	h.valid(older.access)
	got := list()
	if len(got) != 2 {
		t.Fatalf("connections = %v", got)
	}
	first := got[0].(map[string]any)
	if first["client_name"] != "Claude" || !slices.Equal(anyStrings(first["scopes"]), []string{"read", "write"}) ||
		first["created_at"] == nil || first["last_used_at"] == nil || got[1].(map[string]any)["last_used_at"] != nil {
		t.Errorf("formato/ordem: %v", got)
	}

	var before, after time.Time
	h.db.Raw(`SELECT last_used_at FROM oauth_connections WHERE id = ?`, first["id"]).Scan(&before)
	h.advance(30 * time.Second)
	h.valid(older.access)
	h.db.Raw(`SELECT last_used_at FROM oauth_connections WHERE id = ?`, first["id"]).Scan(&after)
	if !after.Equal(before) {
		t.Error("last_used_at atualizado em menos de 1 min")
	}
	h.advance(31 * time.Second)
	h.valid(older.access)
	h.db.Raw(`SELECT last_used_at FROM oauth_connections WHERE id = ?`, first["id"]).Scan(&after)
	if !after.After(before) {
		t.Error("last_used_at deveria atualizar após 1 min")
	}

	del := func(id string) int {
		return h.serve(h.api, httptest.NewRequest("DELETE", "/oauth/connections/"+id, nil)).Code
	}
	id := first["id"].(string)
	if code := del(id); code != 204 {
		t.Fatalf("DELETE = %d", code)
	}
	if h.valid(older.access) || !h.valid(newer.access) {
		t.Error("só a Connection removida deve parar de valer")
	}
	if del(id) != 404 || del("00000000-0000-0000-0000-000000000000") != 404 || del("xyz") != 404 {
		t.Error("DELETE de inexistente/já revogada deve ser 404")
	}
	if len(list()) != 1 {
		t.Error("revogadas saem da lista")
	}
	var reason string
	h.db.Raw(`SELECT revoked_reason FROM oauth_connections WHERE id = ?`, id).Scan(&reason)
	if reason != "USER" {
		t.Errorf("revoked_reason = %q", reason)
	}
}

func TestIntegration_AllowlistRecheckRevokesConnection(t *testing.T) {
	h := newHarness(t)
	tk := h.connect()
	if !h.valid(tk.access) {
		t.Fatal("dono deveria valer")
	}
	removed := New(h.db, h.s.cfg, auth.ParseAllowlist("outro@example.com"))
	removed.now = h.s.now
	if _, err := removed.VerifyAccessToken(t.Context(), tk.access); err != ErrInvalidToken {
		t.Fatalf("err = %v, quero ErrInvalidToken", err)
	}
	if h.valid(tk.access) {
		t.Error("a Connection deveria ter sido revogada (mesmo com a allowlist de volta)")
	}
	var reason string
	h.db.Raw(`SELECT revoked_reason FROM oauth_connections`).Scan(&reason)
	if reason != "ALLOWLIST" {
		t.Errorf("revoked_reason = %q", reason)
	}
}

func TestIntegration_ResourceBinding(t *testing.T) {
	h := newHarness(t)
	tk := h.connect()
	other := New(h.db, Config{Enabled: true, PublicBaseURL: "https://outra.test", WebAppURL: tWeb, AllowedRedirectURIs: h.s.cfg.AllowedRedirectURIs}, h.s.allow)
	other.now = h.s.now
	if _, err := other.VerifyAccessToken(t.Context(), tk.access); err != ErrInvalidToken {
		t.Errorf("token de outro resource: %v", err)
	}
}

func TestIntegration_CleanupRemovesExpiredTechnicalRows(t *testing.T) {
	h := newHarness(t)
	tk := h.connect()
	// Pedido nunca decidido, Connection nunca ativada, token expirado há > 7 dias.
	cid := h.clientID()
	h.authorizeRequest(cid)
	h.approve(h.authorizeRequest(cid))
	h.advance(8*24*time.Hour + 31*24*time.Hour)
	h.s.Cleanup(t.Context())
	count := func(q string) int64 {
		var n int64
		h.db.Raw(q).Scan(&n)
		return n
	}
	if n := count(`SELECT COUNT(*) FROM oauth_authorization_requests`); n != 0 {
		t.Errorf("authorization requests restantes = %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM oauth_tokens`); n != 0 {
		t.Errorf("tokens expirados restantes = %d", n)
	}
	// Connections sem nenhum token (a nunca ativada e a de refresh expirado/purgado) somem.
	if n := count(`SELECT COUNT(*) FROM oauth_connections`); n != 0 {
		t.Errorf("connections restantes = %d", n)
	}
	_ = tk
}
