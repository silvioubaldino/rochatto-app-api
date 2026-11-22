// Package gateway despacha em processo para as rotas /api/v1, sem a autenticação Firebase (o Bearer já foi validado).
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

type InProcess struct {
	h http.Handler
}

func New(h http.Handler) *InProcess { return &InProcess{h: h} }

var _ toolkit.Gateway = (*InProcess)(nil)

type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (g *InProcess) Do(ctx context.Context, method, path string, query url.Values, body, out any) (err error) {
	// Só rotas de negócio: o servidor OAuth nunca é alcançável por ferramentas.
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "/oauth/") {
		return fmt.Errorf("gateway: rota não permitida %q", path)
	}
	var rd *bytes.Reader
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return fmt.Errorf("gateway: corpo: %w", merr)
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, rerr := http.NewRequestWithContext(ctx, method, target, rd)
	if rerr != nil {
		return fmt.Errorf("gateway: %w", rerr)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	rec := &recorder{header: http.Header{}}
	func() {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("ERROR gateway: panic em %s %s: %v", method, path, p)
				rec.status = http.StatusInternalServerError
				rec.body.Reset()
			}
		}()
		g.h.ServeHTTP(rec, req)
	}()
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if rec.status < 200 || rec.status > 299 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.body.Bytes(), &e)
		return &toolkit.APIError{Status: rec.status, Message: e.Error}
	}
	if out == nil || rec.body.Len() == 0 {
		return nil
	}
	if err := json.Unmarshal(rec.body.Bytes(), out); err != nil {
		return fmt.Errorf("gateway: resposta de %s %s: %w", method, path, err)
	}
	return nil
}
