// Package mcp monta o servidor MCP do Assistant connector (AYD-006 §1, ADR-002@context):
// transporte Streamable HTTP stateless sobre o SDK oficial, autenticação Bearer com
// recheck da allowlist, o pipeline único de chamadas e o catálogo versionado de ferramentas.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/oauth"
)

// Options configura o handler do /mcp.
type Options struct {
	Config   oauth.Config
	Verifier TokenVerifier
	Pipeline PipelineOptions
	// Tools é o catálogo; nil usa Catalog().
	Tools []toolkit.Tool
}

// TokenVerifier valida o access token (implementado por *oauth.Server).
type TokenVerifier interface {
	VerifyAccessToken(ctx context.Context, token string) (*oauth.Principal, error)
}

const principalKey = "principal"

// NewHandler devolve o http.Handler do endpoint /mcp. Ordem das verificações HTTP:
// Origin (403) → método (405 para GET/DELETE) → Bearer (401) → JSON-RPC (SDK).
func NewHandler(o Options) (http.Handler, error) {
	catalog := o.Tools
	if catalog == nil {
		catalog = Catalog()
	}
	pipe, err := NewPipeline(o.Pipeline, catalog)
	if err != nil {
		return nil, err
	}

	srv := sdk.NewServer(
		&sdk.Implementation{Name: "rochatto", Title: "Rochatto", Version: CatalogVersion},
		&sdk.ServerOptions{
			Instructions: Instructions,
			// tools: { listChanged: false } — o catálogo não muda em tempo de execução.
			Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{ListChanged: false}},
		},
	)
	scopeOf := map[string]string{}
	for _, t := range catalog {
		t := t
		scopeOf[t.Meta.Name] = t.Meta.Scope()
		srv.AddTool(sdkTool(t), func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			pr := principalOf(req.GetExtra())
			if pr == nil {
				return nil, errors.New("não autenticado")
			}
			return pipe.Call(ctx, t, pr, req.Params.Arguments), nil
		})
	}
	// tools/list devolve só as ferramentas que os scopes da Connection permitem (§3.8).
	srv.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return res, err
			}
			lr, ok := res.(*sdk.ListToolsResult)
			if !ok {
				return res, err
			}
			pr := principalOf(req.GetExtra())
			kept := make([]*sdk.Tool, 0, len(lr.Tools))
			for _, t := range lr.Tools {
				if pr != nil && pr.HasScope(scopeOf[t.Name]) {
					kept = append(kept, t)
				}
			}
			lr.Tools = kept
			return lr, nil
		}
	})

	stream := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	h := &handler{cfg: o.Config, verifier: o.Verifier, stream: stream}
	h.allowedOrigins = map[string]bool{"https://claude.ai": true, "https://claude.com": true}
	if o.Config.WebAppURL != "" {
		h.allowedOrigins[o.Config.WebAppURL] = true
	}
	return h, nil
}

func sdkTool(t toolkit.Tool) *sdk.Tool {
	e := t.Entry()
	open := false
	return &sdk.Tool{
		Name:         e.Name,
		Title:        e.Title,
		Description:  e.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
		Annotations: &sdk.ToolAnnotations{
			Title:           e.Title,
			ReadOnlyHint:    e.Annotations.ReadOnlyHint,
			DestructiveHint: e.Annotations.DestructiveHint,
			IdempotentHint:  e.Annotations.IdempotentHint,
			OpenWorldHint:   &open,
		},
	}
}

func principalOf(x *sdk.RequestExtra) *oauth.Principal {
	if x == nil || x.TokenInfo == nil {
		return nil
	}
	p, _ := x.TokenInfo.Extra[principalKey].(*oauth.Principal)
	return p
}

type handler struct {
	cfg            oauth.Config
	verifier       TokenVerifier
	stream         http.Handler
	allowedOrigins map[string]bool
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Origin: proteção contra DNS rebinding. Sem Origin (servidor-a-servidor) segue.
	if origin := r.Header.Get("Origin"); origin != "" && !h.allowedOrigins[origin] {
		writeJSONError(w, http.StatusForbidden, "origin não permitida")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "método não permitido")
		return
	}

	token, hasBearer := bearerToken(r)
	if !hasBearer {
		h.unauthorized(w, false)
		return
	}
	pr, err := h.verifier.VerifyAccessToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, oauth.ErrInvalidToken) {
			h.unauthorized(w, true)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	info := &auth.TokenInfo{
		Scopes: pr.Scopes, Expiration: pr.ExpiresAt, UserID: pr.ConnectionID.String(),
		Extra: map[string]any{principalKey: pr},
	}
	// O SDK guarda o TokenInfo no contexto e o repassa aos handlers (RequestExtra).
	auth.RequireBearerToken(func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
		return info, nil
	}, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h.stream).ServeHTTP(w, r) // validade já checada pelo verificador
}

func bearerToken(r *http.Request) (string, bool) {
	f := strings.Fields(r.Header.Get("Authorization"))
	if len(f) != 2 || !strings.EqualFold(f[0], "Bearer") {
		return "", false
	}
	return f[1], true
}

// unauthorized responde 401 com WWW-Authenticate (dispara o login no Claude — §1).
func (h *handler) unauthorized(w http.ResponseWriter, invalid bool) {
	v := `Bearer resource_metadata="` + h.cfg.PublicBaseURL + `/.well-known/oauth-protected-resource/mcp", scope="read write"`
	if invalid {
		v += `, error="invalid_token"`
	}
	w.Header().Set("WWW-Authenticate", v)
	writeJSONError(w, http.StatusUnauthorized, "unauthorized")
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
