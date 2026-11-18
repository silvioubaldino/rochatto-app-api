package mcp_test

// Teste de ponta a ponta do Assistant connector (AYD-006): DCR → authorize → consentimento
// → token (PKCE) → /mcp com o access token → revogação / allowlist. Usa o SDK oficial como
// cliente MCP. Roda só com TEST_DATABASE_URL.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/silvioubaldino/sales-backend/internal/auth"
	"github.com/silvioubaldino/sales-backend/internal/handlers"
	"github.com/silvioubaldino/sales-backend/internal/mcp"
	"github.com/silvioubaldino/sales-backend/internal/mcp/gateway"
	"github.com/silvioubaldino/sales-backend/internal/oauth"
	"github.com/silvioubaldino/sales-backend/internal/testdb"
	"gorm.io/gorm"
)

const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"

type stack struct {
	t     *testing.T
	db    *gorm.DB
	srv   *httptest.Server
	oauth *oauth.Server
	cfg   oauth.Config
	owner string
}

func newStack(t *testing.T, allowlist string) *stack {
	t.Helper()
	db := testdb.New(t)
	apiMux := http.NewServeMux()
	handlers.NewClientesHandler(db).Register(apiMux)
	handlers.NewProdutosHandler(db).Register(apiMux)
	handlers.NewFornecedoresHandler(db).Register(apiMux)
	handlers.NewPartnersHandler(db).Register(apiMux)
	handlers.NewProductCategoriesHandler(db).Register(apiMux)
	handlers.NewVendasHandler(db).Register(apiMux)
	handlers.NewQuotesHandler(db).Register(apiMux)
	handlers.NewObligationsHandler(db).Register(apiMux)
	handlers.NewSettlementsHandler(db).Register(apiMux)
	handlers.NewFinancialMovementsHandler(db).Register(apiMux)
	handlers.NewPurchasesHandler(db).Register(apiMux)
	handlers.NewStockHandler(db).Register(apiMux)
	handlers.NewDashboardHandler(db).Register(apiMux)
	handlers.NewFinanceHandler(db).Register(apiMux)

	s := &stack{t: t, db: db, owner: "dono@example.com"}
	root := http.NewServeMux()
	// O issuer precisa ser o endereço do servidor de teste: sobe o servidor e monta depois.
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(s.srv.Close)
	s.cfg = oauth.Config{Enabled: true, PublicBaseURL: s.srv.URL, WebAppURL: "https://app.test", AllowedRedirectURIs: []string{oauth.DefaultRedirectURI}}
	s.oauth = oauth.New(db, s.cfg, auth.ParseAllowlist(allowlist))
	s.oauth.RegisterPublic(root)
	oauthAPI := http.NewServeMux()
	s.oauth.RegisterAPI(oauthAPI)
	// Superfície /api/v1 com o "Firebase" simulado (uid/e-mail do dono).
	root.Handle("/api/v1/", http.StripPrefix("/api/v1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithUser(r.Context(), "uid-dono", s.owner))
		if strings.HasPrefix(r.URL.Path, "/oauth/") {
			oauthAPI.ServeHTTP(w, r)
			return
		}
		apiMux.ServeHTTP(w, r)
	})))
	h, err := mcp.NewHandler(mcp.Options{
		Config: s.cfg, Verifier: s.oauth,
		Pipeline: mcp.PipelineOptions{Gateway: gateway.New(apiMux), Audit: mcp.NewDBAuditor(db), Deduper: mcp.NewDBDeduper(db, nil), ValidateOutput: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	root.Handle("/mcp", h)
	return s
}

func (s *stack) do(method, path string, body io.Reader, hdr map[string]string) (*http.Response, []byte) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.srv.URL+path, body)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// connect percorre o fluxo OAuth completo e devolve o access e o refresh token.
func (s *stack) connect(scope string) (access, refresh, connID string) {
	s.t.Helper()
	body, _ := json.Marshal(map[string]any{"redirect_uris": []string{oauth.DefaultRedirectURI}, "client_name": "Claude"})
	resp, b := s.do("POST", "/oauth/register", bytes.NewReader(body), map[string]string{"Content-Type": "application/json"})
	var reg map[string]any
	_ = json.Unmarshal(b, &reg)
	if resp.StatusCode != 201 {
		s.t.Fatalf("register = %d %s", resp.StatusCode, b)
	}
	cid := reg["client_id"].(string)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {cid}, "redirect_uri": {oauth.DefaultRedirectURI},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"xyz"}, "resource": {s.cfg.Resource()}}
	if scope != "" {
		q.Set("scope", scope)
	}
	resp, _ = s.do("GET", "/oauth/authorize?"+q.Encode(), nil, nil)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != 302 || loc.Host != "app.test" {
		s.t.Fatalf("authorize = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, b = s.do("POST", "/api/v1/oauth/authorization-requests/"+loc.Query().Get("request_id")+"/approve", strings.NewReader("{}"), nil)
	var ap map[string]string
	_ = json.Unmarshal(b, &ap)
	if resp.StatusCode != 200 {
		s.t.Fatalf("approve = %d %s", resp.StatusCode, b)
	}
	cb, _ := url.Parse(ap["redirect_to"])
	resp, b = s.do("POST", "/oauth/token", strings.NewReader(url.Values{
		"grant_type": {"authorization_code"}, "code": {cb.Query().Get("code")}, "redirect_uri": {oauth.DefaultRedirectURI},
		"client_id": {cid}, "code_verifier": {verifier}, "resource": {s.cfg.Resource()}}.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var tok map[string]any
	_ = json.Unmarshal(b, &tok)
	if resp.StatusCode != 200 {
		s.t.Fatalf("token = %d %s", resp.StatusCode, b)
	}
	s.db.Raw(`SELECT id::text FROM oauth_connections ORDER BY created_at DESC LIMIT 1`).Scan(&connID)
	return tok["access_token"].(string), tok["refresh_token"].(string), connID
}

type bearer struct {
	rt    http.RoundTripper
	token string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.rt.RoundTrip(r)
}

func (s *stack) session(token string) (*sdk.ClientSession, error) {
	c := sdk.NewClient(&sdk.Implementation{Name: "teste-e2e", Version: "1"}, nil)
	return c.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint:   s.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{http.DefaultTransport, token}},
	}, nil)
}

func TestE2E_ConnectionToolsListAndCall(t *testing.T) {
	s := newStack(t, "dono@example.com")

	// Sem token → 401 com WWW-Authenticate apontando o resource metadata.
	resp, _ := s.do("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
		map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"})
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `resource_metadata="`+s.cfg.PublicBaseURL+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("sem token = %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	access, _, _ := s.connect("read write offline_access")
	ctx := context.Background()
	cs, err := s.session(access)
	if err != nil {
		t.Fatalf("connect MCP: %v", err)
	}
	defer cs.Close()

	init := cs.InitializeResult()
	if init.ServerInfo.Name != "rochatto" || init.ServerInfo.Title != "Rochatto" || init.ServerInfo.Version != mcp.CatalogVersion || init.Instructions != mcp.Instructions {
		t.Errorf("initialize = %+v", init)
	}
	if cs.ID() != "" { // stateless
		t.Error("stateless: sem Mcp-Session-Id")
	}

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != len(mcp.Catalog()) || len(list.Tools) != 36 || init.ServerInfo.Version != "1.0.0" {
		t.Errorf("tools/list = %d ferramentas (catálogo %d, versão %s), quero 36 e 1.0.0", len(list.Tools), len(mcp.Catalog()), init.ServerInfo.Version)
	}

	// Dado criado pela REST aparece pela ferramenta (mesmos handlers).
	req, _ := http.NewRequest("POST", s.srv.URL+"/api/v1/clientes", strings.NewReader(`{"nome":"Cliente E2E"}`))
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 201 {
		t.Fatalf("seed cliente: %v", err)
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "search_customers", Arguments: map[string]any{"query": "e2e"}})
	if err != nil || res.IsError {
		t.Fatalf("CallTool: %v %+v", err, res)
	}
	sc := res.StructuredContent.(map[string]any)
	if sc["total"] != float64(1) || sc["items"].([]any)[0].(map[string]any)["name"] != "Cliente E2E" {
		t.Errorf("structuredContent = %v", sc)
	}
	if txt := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(txt, `"Cliente E2E"`) {
		t.Errorf("content deve trazer o mesmo JSON: %s", txt)
	}

	// Erro de regra: isError com code/message da API.
	res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "get_sale", Arguments: map[string]any{"sale_id": "00000000-0000-0000-0000-000000000000"}})
	if err != nil || !res.IsError || res.StructuredContent.(map[string]any)["error"].(map[string]any)["code"] != "NOT_FOUND" {
		t.Errorf("erro de ferramenta = %v %+v", err, res)
	}

	// Auditoria: uma linha por tools/call.
	var n int64
	s.db.Raw(`SELECT COUNT(*) FROM mcp_tool_calls`).Scan(&n)
	if n != 2 {
		t.Errorf("auditoria = %d registros, quero 2", n)
	}
}

func TestE2E_RevocationAndAllowlist(t *testing.T) {
	s := newStack(t, "dono@example.com")
	access, _, connID := s.connect("")
	if cs, err := s.session(access); err != nil {
		t.Fatalf("antes de revogar: %v", err)
	} else {
		cs.Close()
	}

	// Revogação pela tela Integrações → próxima chamada com o access token = 401.
	resp, _ := s.do("DELETE", "/api/v1/oauth/connections/"+connID, nil, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("DELETE connection = %d", resp.StatusCode)
	}
	if _, err := s.session(access); err == nil {
		t.Error("access token de Connection revogada deveria falhar (401)")
	}
	resp, _ = s.do("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
		map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"})
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("revogada = %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	// E-mail removido da allowlist: a Connection é revogada na primeira chamada seguinte.
	access2, _, conn2 := s.connect("")
	s.oauth = oauth.New(s.db, s.cfg, auth.ParseAllowlist("outro@example.com"))
	h, _ := mcp.NewHandler(mcp.Options{Config: s.cfg, Verifier: s.oauth, Pipeline: mcp.PipelineOptions{Gateway: gateway.New(http.NewServeMux()), Deduper: mcp.NewDBDeduper(s.db, nil)}})
	rec := httptest.NewRecorder()
	rq := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	rq.Header.Set("Authorization", "Bearer "+access2)
	rq.Header.Set("Content-Type", "application/json")
	rq.Header.Set("Accept", "application/json, text/event-stream")
	h.ServeHTTP(rec, rq)
	if rec.Code != 401 {
		t.Fatalf("fora da allowlist = %d, quero 401", rec.Code)
	}
	var reason string
	s.db.Raw(`SELECT revoked_reason FROM oauth_connections WHERE id = ?`, conn2).Scan(&reason)
	if reason != "ALLOWLIST" {
		t.Errorf("revoked_reason = %q", reason)
	}
}

func TestE2E_ScopeWriteOnlyConnectionHasNoReadTools(t *testing.T) {
	s := newStack(t, "dono@example.com")
	access, _, _ := s.connect("write") // scopes futuros menores: sem read, as 20 ferramentas de leitura somem
	cs, err := s.session(access)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 16 { // só as 16 escritas
		t.Fatalf("tools/list = %d (%v), quero as 16 de escrita", len(list.Tools), err)
	}
	for _, tool := range list.Tools {
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			t.Errorf("%s é de leitura e apareceu para uma Connection write", tool.Name)
		}
	}
	// Chamada direta fora dos scopes → FORBIDDEN (não erro de protocolo).
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "search_customers", Arguments: map[string]any{}})
	if err != nil || !res.IsError || res.StructuredContent.(map[string]any)["error"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Errorf("CallTool sem scope = %v %+v", err, res)
	}
}

// SPEC-007/008 — scope de leitura: Connection só com read não lista as 16 escritas e chamá-las
// devolve FORBIDDEN sem nenhuma chamada ao Gateway (nenhum Customer criado).
func TestE2E_ReadOnlyConnectionHasNoWriteTools(t *testing.T) {
	s := newStack(t, "dono@example.com")
	access, _, _ := s.connect("read")
	cs, err := s.session(access)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil || len(list.Tools) != 20 {
		t.Fatalf("tools/list = %d (%v), quero as 20 de leitura", len(list.Tools), err)
	}
	for _, tool := range list.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s não é de leitura e apareceu para uma Connection read", tool.Name)
		}
	}
	for _, tool := range mcp.Catalog() {
		if tool.Meta.ReadOnly {
			continue
		}
		res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tool.Meta.Name, Arguments: map[string]any{"name": "x"}})
		if err != nil || !res.IsError {
			t.Fatalf("%s: %v %+v", tool.Meta.Name, err, res)
		}
		e := res.StructuredContent.(map[string]any)["error"].(map[string]any)
		if e["code"] != "FORBIDDEN" || e["message"] != "esta conexão não tem permissão de escrita" {
			t.Errorf("%s = %v", tool.Meta.Name, e)
		}
	}
	var n int64
	s.db.Raw(`SELECT COUNT(*) FROM clientes`).Scan(&n)
	if n != 0 {
		t.Errorf("nenhuma escrita deveria ter chegado à API, há %d clientes", n)
	}
}
