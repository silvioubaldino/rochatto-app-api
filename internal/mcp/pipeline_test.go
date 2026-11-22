package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/oauth"
)

type fakeGateway struct {
	do func(method, path string, q url.Values, body, out any) error
}

func (f fakeGateway) Do(_ context.Context, method, path string, q url.Values, body, out any) error {
	return f.do(method, path, q, body, out)
}

type memAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (m *memAudit) Record(_ context.Context, e AuditEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
}

func (m *memAudit) last(t *testing.T) AuditEntry {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		t.Fatal("nenhum registro de auditoria")
	}
	return m.entries[len(m.entries)-1]
}

type echoIn struct {
	Name string `json:"name"`
	N    int    `json:"n,omitempty"`
}
type echoOut struct {
	Echo string `json:"echo"`
}

func echoTool(name string, readOnly, dedupe bool, h toolkit.Handler[echoIn, echoOut]) toolkit.Tool {
	return toolkit.Define(toolkit.Meta{Name: name, Title: "Eco", Description: "teste", ReadOnly: readOnly, Idempotent: readOnly, Dedupe: dedupe}, h)
}

func principal(scopes ...string) *oauth.Principal {
	return &oauth.Principal{ConnectionID: uuid.New(), Scopes: scopes}
}

func newTestPipeline(t *testing.T, gw toolkit.Gateway, aud Auditor, d Deduper, cat []toolkit.Tool) *Pipeline {
	t.Helper()
	p, err := NewPipeline(PipelineOptions{Gateway: gw, Audit: aud, Deduper: d, ValidateOutput: true}, cat)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func errPayload(t *testing.T, structured any) toolkit.ToolError {
	t.Helper()
	b, _ := json.Marshal(structured)
	var v struct {
		Error toolkit.ToolError `json:"error"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v.Error
}

func TestPipeline_SuccessStructuredContentAndAudit(t *testing.T) {
	aud := &memAudit{}
	tool := echoTool("get_echo", true, false, func(_ context.Context, _ toolkit.Gateway, in echoIn) (echoOut, error) {
		return echoOut{Echo: "oi " + in.Name}, nil
	})
	p := newTestPipeline(t, fakeGateway{}, aud, nil, []toolkit.Tool{tool})
	res := p.Call(context.Background(), tool, principal("read"), json.RawMessage(`{"name":"Ana","n":2}`))
	if res.IsError {
		t.Fatalf("erro inesperado: %+v", res.Content)
	}
	sc, _ := json.Marshal(res.StructuredContent)
	if string(sc) != `{"echo":"oi Ana"}` {
		t.Errorf("structuredContent = %s", sc)
	}
	cj, _ := json.Marshal(res.Content[0])
	if !strings.Contains(string(cj), `{\"echo\":\"oi Ana\"}`) {
		t.Errorf("content = %s", cj)
	}
	e := aud.last(t)
	if e.Tool != "get_echo" || e.Outcome != "OK" || e.Result != nil || e.ErrorCode != "" {
		t.Errorf("auditoria = %+v (result só para dedupe)", e)
	}
	if string(e.Arguments) != `{"n":2,"name":"Ana"}` || e.ArgumentsHash == "" {
		t.Errorf("arguments canônicos = %s", e.Arguments)
	}
}

func TestPipeline_ScopeEnforcement(t *testing.T) {
	called := false
	read := echoTool("get_a", true, false, func(context.Context, toolkit.Gateway, echoIn) (echoOut, error) { return echoOut{}, nil })
	write := echoTool("create_a", false, false, func(context.Context, toolkit.Gateway, echoIn) (echoOut, error) {
		called = true
		return echoOut{}, nil
	})
	aud := &memAudit{}
	p := newTestPipeline(t, fakeGateway{}, aud, nil, []toolkit.Tool{read, write})

	res := p.Call(context.Background(), write, principal("read"), json.RawMessage(`{"name":"x"}`))
	if !res.IsError || errPayload(t, res.StructuredContent).Code != toolkit.CodeForbidden || called {
		t.Fatalf("esperava FORBIDDEN sem executar: %+v called=%v", res, called)
	}
	if e := aud.last(t); e.Outcome != "ERROR" || e.ErrorCode != "FORBIDDEN" {
		t.Errorf("auditoria = %+v", e)
	}
	if msg := errPayload(t, res.StructuredContent).Message; msg != "esta conexão não tem permissão de escrita" {
		t.Errorf("message = %q", msg)
	}
	if res := p.Call(context.Background(), write, principal("read", "write"), json.RawMessage(`{"name":"x"}`)); res.IsError || !called {
		t.Errorf("write deveria executar: %+v", res)
	}
	if res := p.Call(context.Background(), read, principal("write"), json.RawMessage(`{"name":"x"}`)); !res.IsError {
		t.Errorf("read sem scope read deveria falhar")
	}
}

func TestPipeline_InvalidInputAndErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		args     string
		handler  error
		wantCode string
		wantMsg  string
	}{
		{"schema: campo obrigatório ausente", `{}`, nil, "INVALID_INPUT", "argumentos inválidos"},
		{"schema: campo extra", `{"name":"x","zzz":1}`, nil, "INVALID_INPUT", "argumentos inválidos"},
		{"400", `{"name":"x"}`, &toolkit.APIError{Status: 400, Message: "campo 'valor' deve ser maior que zero"}, "INVALID_INPUT", "campo 'valor' deve ser maior que zero"},
		{"404", `{"name":"x"}`, &toolkit.APIError{Status: 404, Message: "venda não encontrada"}, "NOT_FOUND", "venda não encontrada"},
		{"409", `{"name":"x"}`, &toolkit.APIError{Status: 409, Message: "venda cancelada: recebimentos bloqueados"}, "CONFLICT", "venda cancelada: recebimentos bloqueados"},
		{"422", `{"name":"x"}`, &toolkit.APIError{Status: 422, Message: "orçamento sem itens"}, "UNPROCESSABLE", "orçamento sem itens"},
		{"500 não vaza detalhe", `{"name":"x"}`, &toolkit.APIError{Status: 500, Message: "pq: SECRET SQL"}, "INTERNAL", "erro interno; tente novamente (ref "},
		{"erro inesperado não vaza", `{"name":"x"}`, errors.New("dial tcp 10.0.0.1: SECRET"), "INTERNAL", "erro interno; tente novamente (ref "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool := echoTool("get_e", true, false, func(context.Context, toolkit.Gateway, echoIn) (echoOut, error) { return echoOut{}, c.handler })
			p := newTestPipeline(t, fakeGateway{}, nil, nil, []toolkit.Tool{tool})
			res := p.Call(context.Background(), tool, principal("read"), json.RawMessage(c.args))
			if !res.IsError {
				t.Fatalf("esperava isError")
			}
			e := errPayload(t, res.StructuredContent)
			if e.Code != c.wantCode || !strings.Contains(e.Message, c.wantMsg) {
				t.Errorf("erro = %+v, quero %s / %q", e, c.wantCode, c.wantMsg)
			}
			if strings.Contains(e.Message, "SECRET") {
				t.Errorf("vazou detalhe interno: %q", e.Message)
			}
		})
	}
}

func TestPipeline_PanicBecomesInternal(t *testing.T) {
	tool := echoTool("get_p", true, false, func(context.Context, toolkit.Gateway, echoIn) (echoOut, error) { panic("boom") })
	p := newTestPipeline(t, fakeGateway{}, nil, nil, []toolkit.Tool{tool})
	res := p.Call(context.Background(), tool, principal("read"), json.RawMessage(`{"name":"x"}`))
	if !res.IsError || errPayload(t, res.StructuredContent).Code != "INTERNAL" {
		t.Fatalf("res = %+v", res)
	}
}

func TestPipeline_RateLimit(t *testing.T) {
	tool := echoTool("get_r", true, false, func(context.Context, toolkit.Gateway, echoIn) (echoOut, error) { return echoOut{}, nil })
	p := newTestPipeline(t, fakeGateway{}, nil, nil, []toolkit.Tool{tool})
	pr := principal("read")
	for i := 0; i < RateLimitPerMinute; i++ {
		if res := p.Call(context.Background(), tool, pr, json.RawMessage(`{"name":"x"}`)); res.IsError {
			t.Fatalf("chamada %d bloqueada cedo demais", i)
		}
	}
	res := p.Call(context.Background(), tool, pr, json.RawMessage(`{"name":"x"}`))
	if !res.IsError || errPayload(t, res.StructuredContent).Code != "RATE_LIMITED" {
		t.Fatalf("esperava RATE_LIMITED: %+v", res)
	}
	if res := p.Call(context.Background(), tool, principal("read"), json.RawMessage(`{"name":"x"}`)); res.IsError {
		t.Errorf("limite deve ser por Connection")
	}
}

func TestRateLimiter_WindowSlides(t *testing.T) {
	l := newRateLimiter(2, time.Minute)
	id := uuid.New()
	t0 := time.Unix(1000, 0)
	if !l.Allow(id, t0) || !l.Allow(id, t0.Add(time.Second)) || l.Allow(id, t0.Add(2*time.Second)) {
		t.Fatal("janela inicial errada")
	}
	if !l.Allow(id, t0.Add(61*time.Second)) {
		t.Error("deveria liberar após a janela")
	}
}

func TestPipeline_OutputCap(t *testing.T) {
	big := strings.Repeat("a", MaxOutputChars+1)
	tool := echoTool("get_big", true, false, func(context.Context, toolkit.Gateway, echoIn) (echoOut, error) { return echoOut{Echo: big}, nil })
	p := newTestPipeline(t, fakeGateway{}, nil, nil, []toolkit.Tool{tool})
	res := p.Call(context.Background(), tool, principal("read"), json.RawMessage(`{"name":"x"}`))
	if !res.IsError || errPayload(t, res.StructuredContent).Code != "INVALID_INPUT" {
		t.Fatalf("saída acima de 100.000 caracteres deve virar erro: %+v", res.IsError)
	}
}

func TestPipeline_DedupeExtensionPoint(t *testing.T) {
	type dIn struct {
		Name           string `json:"name"`
		AllowDuplicate bool   `json:"allow_duplicate,omitempty"`
	}
	type dOut struct {
		ID           string `json:"id"`
		Deduplicated bool   `json:"deduplicated,omitempty"`
	}
	n := 0
	tool := toolkit.Define(toolkit.Meta{Name: "create_d", Title: "D", Description: "t", Dedupe: true},
		func(context.Context, toolkit.Gateway, dIn) (dOut, error) { n++; return dOut{ID: "novo"}, nil })

	if _, err := NewPipeline(PipelineOptions{Gateway: fakeGateway{}}, []toolkit.Tool{tool}); err == nil {
		t.Fatal("ferramenta dedupe sem Deduper deveria falhar na montagem")
	}

	aud := &memAudit{}
	var stored []byte
	d := dedupeFn(func(_ context.Context, _ uuid.UUID, tool, _ string) ([]byte, bool, error) {
		return stored, stored != nil, nil
	})
	p := newTestPipeline(t, fakeGateway{}, aud, d, []toolkit.Tool{tool})
	pr := principal("read", "write")

	res := p.Call(context.Background(), tool, pr, json.RawMessage(`{"name":"x"}`))
	if res.IsError || n != 1 {
		t.Fatalf("1ª chamada: %+v n=%d", res, n)
	}
	e := aud.last(t)
	if string(e.Result) != `{"id":"novo"}` {
		t.Errorf("auditoria da ferramenta dedupe deve guardar result, tem %q", e.Result)
	}
	stored = e.Result

	res = p.Call(context.Background(), tool, pr, json.RawMessage(`{"name":"x"}`))
	sc, _ := json.Marshal(res.StructuredContent)
	if n != 1 || !strings.Contains(string(sc), `"deduplicated":true`) || !strings.Contains(string(sc), `"id":"novo"`) {
		t.Errorf("2ª chamada deveria deduplicar: n=%d %s", n, sc)
	}
	if e := aud.last(t); e.Result != nil {
		t.Errorf("chamada deduplicada não grava result de novo")
	}

	res = p.Call(context.Background(), tool, pr, json.RawMessage(`{"name":"x","allow_duplicate":true}`))
	if res.IsError || n != 2 {
		t.Errorf("allow_duplicate deveria criar de novo: n=%d", n)
	}
}

type dedupeFn func(ctx context.Context, conn uuid.UUID, tool, hash string) ([]byte, bool, error)

func (f dedupeFn) Lookup(ctx context.Context, conn uuid.UUID, tool, hash string) ([]byte, bool, error) {
	return f(ctx, conn, tool, hash)
}

func TestCanonicalArgs_KeyOrderIndependent(t *testing.T) {
	_, h1 := canonicalArgs(json.RawMessage(`{"a":1,"b":{"y":2,"x":3}}`))
	_, h2 := canonicalArgs(json.RawMessage(`{ "b": {"x":3,"y":2}, "a": 1 }`))
	_, h3 := canonicalArgs(json.RawMessage(`{"a":2,"b":{"y":2,"x":3}}`))
	if h1 != h2 || h1 == h3 {
		t.Errorf("hash canônico: h1=%s h2=%s h3=%s", h1, h2, h3)
	}
	c4, h4 := canonicalArgs(json.RawMessage(`{"a":1,"b":{"y":2,"x":3},"allow_duplicate":true}`))
	if h4 != h1 || !strings.Contains(string(c4), "allow_duplicate") {
		t.Errorf("allow_duplicate deve ficar fora do hash: %s", h4)
	}
}

func TestPipeline_ReadScopeBlocksAllRealWriteTools(t *testing.T) {
	calls := 0
	gw := fakeGateway{do: func(string, string, url.Values, any, any) error { calls++; return nil }}
	d := dedupeFn(func(context.Context, uuid.UUID, string, string) ([]byte, bool, error) {
		calls++
		return nil, false, nil
	})
	aud := &memAudit{}
	p := newTestPipeline(t, gw, aud, d, Catalog())
	writes := 0
	for _, tool := range Catalog() {
		if tool.Meta.ReadOnly {
			continue
		}
		writes++
		res := p.Call(context.Background(), tool, principal("read"), json.RawMessage(`{}`))
		sc, _ := json.Marshal(res.StructuredContent)
		if !res.IsError || !strings.Contains(string(sc), `"code":"FORBIDDEN"`) || !strings.Contains(string(sc), "esta conexão não tem permissão de escrita") {
			t.Errorf("%s com scope read = %s", tool.Meta.Name, sc)
		}
		if e := aud.last(t); e.Tool != tool.Meta.Name || e.Outcome != "ERROR" || e.ErrorCode != "FORBIDDEN" {
			t.Errorf("auditoria de %s = %+v", tool.Meta.Name, e)
		}
	}
	if writes != 19 || calls != 0 {
		t.Errorf("escritas=%d, chamadas ao Gateway/Deduper=%d (quero 19 e 0)", writes, calls)
	}
}

func TestPipeline_RealDedupeToolsAreTheCreations(t *testing.T) {
	var dd []string
	for _, tool := range Catalog() {
		if tool.Meta.Dedupe {
			dd = append(dd, tool.Meta.Name)
		}
	}
	want := "create_customer create_quote create_quick_sale add_sale_charge create_purchase add_purchase_charge create_stock_adjustment settle_obligations record_expense"
	got := strings.Join(dd, " ")
	for _, n := range strings.Fields(want) {
		if !strings.Contains(got, n) {
			t.Errorf("%s deveria ser dedupe (got %s)", n, got)
		}
	}
	if len(dd) != 9 {
		t.Errorf("dedupe = %v, quero 9", dd)
	}
}
