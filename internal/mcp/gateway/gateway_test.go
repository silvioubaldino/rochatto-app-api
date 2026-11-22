package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /echo/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "q": r.URL.Query().Get("q")})
	})
	mux.HandleFunc("POST /items", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /conflict", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"venda cancelada: recebimentos bloqueados"}`))
	})
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("x") })
	mux.HandleFunc("DELETE /empty", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /oauth/connections", func(http.ResponseWriter, *http.Request) {})
	return mux
}

func TestInProcessGateway(t *testing.T) {
	gw := New(newMux())
	ctx := context.Background()

	var got struct{ ID, Q string }
	if err := gw.Do(ctx, "GET", "/echo/abc", url.Values{"q": {"a b&c"}}, nil, &got); err != nil || got.ID != "abc" || got.Q != "a b&c" {
		t.Errorf("GET = %+v %v", got, err)
	}
	var created map[string]any
	if err := gw.Do(ctx, "POST", "/items", nil, map[string]any{"valor": 3}, &created); err != nil || created["valor"] != float64(3) {
		t.Errorf("POST = %v %v", created, err)
	}
	if err := gw.Do(ctx, "DELETE", "/empty", nil, nil, nil); err != nil {
		t.Errorf("204 = %v", err)
	}

	var ae *toolkit.APIError
	err := gw.Do(ctx, "GET", "/conflict", nil, nil, nil)
	if !errors.As(err, &ae) || ae.Status != 409 || ae.Message != "venda cancelada: recebimentos bloqueados" {
		t.Errorf("409 = %v", err)
	}
	if te := ae.ToToolError(); te.Code != toolkit.CodeConflict {
		t.Errorf("mapeamento = %v", te)
	}
	// Rota inexistente → 404 do mux; panic no handler → 500 (nunca derruba o processo).
	if err := gw.Do(ctx, "GET", "/nao-existe", nil, nil, nil); !errors.As(err, &ae) || ae.Status != 404 {
		t.Errorf("404 = %v", err)
	}
	if err := gw.Do(ctx, "GET", "/boom", nil, nil, nil); !errors.As(err, &ae) || ae.Status != 500 {
		t.Errorf("panic = %v", err)
	}
	// O servidor OAuth nunca é alcançável por ferramentas.
	for _, p := range []string{"/oauth/connections", "oauth/x", ""} {
		if err := gw.Do(ctx, "GET", p, nil, nil, nil); err == nil {
			t.Errorf("rota %q deveria ser recusada", p)
		}
	}
}
