package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/oauth"
)

type fakeVerifier map[string]*oauth.Principal

func (f fakeVerifier) VerifyAccessToken(_ context.Context, tok string) (*oauth.Principal, error) {
	if p, ok := f[tok]; ok {
		return p, nil
	}
	return nil, oauth.ErrInvalidToken
}

const (
	testBase = "https://api.example.com"
	testWeb  = "https://app.example.com"
)

func testHandler(t *testing.T, v TokenVerifier) http.Handler {
	t.Helper()
	read := echoTool("get_echo", true, false, func(_ context.Context, _ toolkit.Gateway, in echoIn) (echoOut, error) {
		return echoOut{Echo: "eco " + in.Name}, nil
	})
	write := echoTool("create_echo", false, false, func(_ context.Context, _ toolkit.Gateway, in echoIn) (echoOut, error) {
		return echoOut{Echo: "criado"}, nil
	})
	h, err := NewHandler(Options{
		Config:   oauth.Config{Enabled: true, PublicBaseURL: testBase, WebAppURL: testWeb},
		Verifier: v,
		Pipeline: PipelineOptions{Gateway: fakeGateway{}, ValidateOutput: true},
		Tools:    []toolkit.Tool{read, write},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func rpc(t *testing.T, h http.Handler, token string, headers map[string]string, method string, params any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	b, _ := io.ReadAll(rec.Body)
	_ = json.Unmarshal(b, &out)
	return rec, out
}

func initialize(t *testing.T, h http.Handler, tok string) map[string]any {
	rec, out := rpc(t, h, tok, nil, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "teste", "version": "1"},
	})
	if rec.Code != 200 {
		t.Fatalf("initialize = %d %s", rec.Code, rec.Body.String())
	}
	return out["result"].(map[string]any)
}

func TestMCP_HTTPErrors(t *testing.T) {
	h := testHandler(t, fakeVerifier{"ok": principal("read", "write")})

	// Sem token → 401 + WWW-Authenticate com resource_metadata (dispara o login no Claude).
	rec, out := rpc(t, h, "", nil, "tools/list", nil)
	wantMeta := `Bearer resource_metadata="` + testBase + `/.well-known/oauth-protected-resource/mcp", scope="read write"`
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != wantMeta || out["error"] != "unauthorized" {
		t.Errorf("sem token: %d %q %v", rec.Code, rec.Header().Get("WWW-Authenticate"), out)
	}
	// Token inválido → 401 com error="invalid_token".
	rec, _ = rpc(t, h, "lixo", nil, "tools/list", nil)
	if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != wantMeta+`, error="invalid_token"` {
		t.Errorf("token inválido: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	// GET e DELETE → 405.
	for _, m := range []string{"GET", "DELETE"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/mcp", nil))
		if rec.Code != 405 {
			t.Errorf("%s /mcp = %d, quero 405", m, rec.Code)
		}
	}
	// Origin: claude.ai, claude.com e WEB_APP_URL passam; outra → 403; sem Origin passa.
	for _, o := range []string{"https://claude.ai", "https://claude.com", testWeb} {
		if rec, _ := rpc(t, h, "ok", map[string]string{"Origin": o}, "tools/list", nil); rec.Code != 200 {
			t.Errorf("Origin %s = %d", o, rec.Code)
		}
	}
	if rec, _ := rpc(t, h, "ok", map[string]string{"Origin": "https://evil.example"}, "tools/list", nil); rec.Code != 403 {
		t.Errorf("Origin estranha = %d, quero 403", rec.Code)
	}
	if rec, _ := rpc(t, h, "ok", nil, "tools/list", nil); rec.Code != 200 {
		t.Errorf("sem Origin = %d", rec.Code)
	}
}

func TestMCP_InitializeServerInfoAndStateless(t *testing.T) {
	h := testHandler(t, fakeVerifier{"ok": principal("read", "write")})
	rec, out := rpc(t, h, "ok", nil, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "teste", "version": "1"},
	})
	if rec.Header().Get("Mcp-Session-Id") != "" {
		t.Error("stateless: Mcp-Session-Id não deve ser emitido")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, quero application/json (sem SSE)", ct)
	}
	res := out["result"].(map[string]any)
	si := res["serverInfo"].(map[string]any)
	if si["name"] != "rochatto" || si["title"] != "Rochatto" || si["version"] != CatalogVersion {
		t.Errorf("serverInfo = %v", si)
	}
	if res["instructions"] != Instructions {
		t.Errorf("instructions diferem do texto canônico")
	}
	tools := res["capabilities"].(map[string]any)["tools"].(map[string]any)
	if lc, _ := tools["listChanged"].(bool); lc {
		t.Errorf("tools.listChanged deve ser false: %v", tools)
	}
}

func toolNames(out map[string]any) []string {
	var names []string
	for _, x := range out["result"].(map[string]any)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}

func TestMCP_ToolsListFilteredByScope(t *testing.T) {
	h := testHandler(t, fakeVerifier{
		"rw": principal("read", "write"),
		"ro": principal("read"),
	})
	_, out := rpc(t, h, "rw", nil, "tools/list", nil)
	if got := strings.Join(toolNames(out), ","); got != "create_echo,get_echo" /* o SDK ordena por nome */ {
		t.Errorf("read+write vê %q", got)
	}
	_, out = rpc(t, h, "ro", nil, "tools/list", nil)
	if got := strings.Join(toolNames(out), ","); got != "get_echo" {
		t.Errorf("só read vê %q, quero só get_echo", got)
	}
	// Anotações publicadas pelo SDK.
	_, out = rpc(t, h, "rw", nil, "tools/list", nil)
	for _, x := range out["result"].(map[string]any)["tools"].([]any) {
		tl := x.(map[string]any)
		ann := tl["annotations"].(map[string]any)
		if ann["openWorldHint"] != false {
			t.Errorf("%v: openWorldHint deve ser false", tl["name"])
		}
		if tl["name"] == "get_echo" && ann["readOnlyHint"] != true {
			t.Errorf("get_echo deve ser readOnly")
		}
		if tl["outputSchema"] == nil || tl["title"] == nil {
			t.Errorf("%v: falta outputSchema/title", tl["name"])
		}
	}
}

func TestMCP_ToolsCall(t *testing.T) {
	h := testHandler(t, fakeVerifier{"rw": principal("read", "write"), "ro": principal("read")})

	_, out := rpc(t, h, "rw", nil, "tools/call", map[string]any{"name": "get_echo", "arguments": map[string]any{"name": "Ana"}})
	res := out["result"].(map[string]any)
	if res["isError"] == true || res["structuredContent"].(map[string]any)["echo"] != "eco Ana" {
		t.Errorf("tools/call = %v", res)
	}

	// Escrita numa Connection só-leitura: mesmo chamada direta (fora do tools/list) → FORBIDDEN.
	_, out = rpc(t, h, "ro", nil, "tools/call", map[string]any{"name": "create_echo", "arguments": map[string]any{"name": "x"}})
	res = out["result"].(map[string]any)
	e := res["structuredContent"].(map[string]any)["error"].(map[string]any)
	if res["isError"] != true || e["code"] != "FORBIDDEN" {
		t.Errorf("escrita sem scope write = %v", res)
	}

	// Entrada inválida → INVALID_INPUT (não erro de protocolo).
	_, out = rpc(t, h, "rw", nil, "tools/call", map[string]any{"name": "get_echo", "arguments": map[string]any{}})
	res = out["result"].(map[string]any)
	if e := res["structuredContent"].(map[string]any)["error"].(map[string]any); res["isError"] != true || e["code"] != "INVALID_INPUT" {
		t.Errorf("entrada inválida = %v", res)
	}
}

func TestMCP_ExpiredPrincipalsAreRejectedByVerifier(t *testing.T) {
	p := principal("read")
	p.ExpiresAt = time.Now().Add(-time.Minute)
	h := testHandler(t, fakeVerifier{"old": p})
	if rec, _ := rpc(t, h, "old", nil, "tools/list", nil); rec.Code != 401 {
		t.Errorf("token expirado = %d, quero 401", rec.Code)
	}
	_ = uuid.Nil
}
