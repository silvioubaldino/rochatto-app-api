package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/oauth"
)

const (
	RateLimitPerMinute = 120
	MaxOutputChars     = 100_000
	ToolTimeout        = 30 * time.Second
)

// Deduper é o ponto de extensão do estágio de deduplicação de criações (§3.7), usado só por ferramentas com Meta.Dedupe.
type Deduper interface {
	// Lookup devolve o resultado (JSON) da chamada bem-sucedida idêntica mais recente da mesma Connection e ferramenta dentro da janela, se houver.
	Lookup(ctx context.Context, connectionID uuid.UUID, tool, argsHash string) (result []byte, found bool, err error)
}

type PipelineOptions struct {
	Gateway        toolkit.Gateway
	Audit          Auditor
	Deduper        Deduper // obrigatório se houver ferramentas dedupe
	Now            func() time.Time
	ValidateOutput bool
	Timeout        time.Duration
}

// Pipeline é o ponto único por onde passa TODA chamada de ferramenta (regra 2 do AYD):  scope → rate limit → validação de entrada → dedupe → handler → mapeamento de erro → auditoria  Nenhuma ferramenta reimplementa nada disto.
type Pipeline struct {
	o       PipelineOptions
	limiter *rateLimiter
}

func NewPipeline(o PipelineOptions, catalog []toolkit.Tool) (*Pipeline, error) {
	if o.Gateway == nil {
		return nil, errors.New("mcp: Gateway é obrigatório")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = ToolTimeout
	}
	for _, t := range catalog {
		if t.Meta.Dedupe && o.Deduper == nil {
			return nil, fmt.Errorf("mcp: ferramenta %s é dedupe mas não há Deduper configurado", t.Meta.Name)
		}
	}
	return &Pipeline{o: o, limiter: newRateLimiter(RateLimitPerMinute, time.Minute)}, nil
}

// Nunca devolve erro de protocolo: falhas viram resultado de ferramenta (§3.5).
func (p *Pipeline) Call(ctx context.Context, tool toolkit.Tool, pr *oauth.Principal, args json.RawMessage) *sdk.CallToolResult {
	start := p.o.Now()
	callID := uuid.New()
	canon, hash := canonicalArgs(args)
	entry := AuditEntry{CallID: callID, ConnectionID: pr.ConnectionID, Tool: tool.Meta.Name, Arguments: canon, ArgumentsHash: hash, CreatedAt: start}

	var out any
	var terr *toolkit.ToolError
	deduplicated := false
	func() {
		if !pr.HasScope(tool.Meta.Scope()) {
			msg := "esta conexão não tem permissão de leitura"
			if !tool.Meta.ReadOnly {
				msg = "esta conexão não tem permissão de escrita"
			}
			terr = toolkit.NewToolError(toolkit.CodeForbidden, msg)
			return
		}
		if !p.limiter.Allow(pr.ConnectionID, start) {
			terr = toolkit.NewToolError(toolkit.CodeRateLimited, "muitas chamadas; aguarde um minuto")
			return
		}
		if err := tool.ValidateInput(args); err != nil {
			terr = toolkit.NewToolError(toolkit.CodeInvalidInput, "argumentos inválidos: "+err.Error())
			return
		}
		if tool.Meta.Dedupe && !allowDuplicate(args) {
			prev, found, err := p.o.Deduper.Lookup(ctx, pr.ConnectionID, tool.Meta.Name, hash)
			if err != nil {
				log.Printf("ERROR mcp dedupe (call_id=%s tool=%s): %v", callID, tool.Meta.Name, err)
			} else if found {
				var m map[string]any
				if json.Unmarshal(prev, &m) == nil {
					m["deduplicated"] = true
					out, deduplicated = m, true
					return
				}
			}
		}
		out, terr = p.run(ctx, tool, args, callID)
	}()

	var res *sdk.CallToolResult
	if terr == nil {
		res, terr = p.success(tool, out, callID)
	}
	if terr != nil {
		res = errorResult(terr)
		entry.Outcome, entry.ErrorCode = "ERROR", terr.Code
	} else {
		entry.Outcome = "OK"
		if tool.Meta.Dedupe && !deduplicated {
			if b, err := json.Marshal(out); err == nil {
				entry.Result = b
			}
		}
	}
	entry.DurationMS = p.o.Now().Sub(start).Milliseconds()
	if p.o.Audit != nil {
		p.o.Audit.Record(ctx, entry)
	}
	return res
}

func (p *Pipeline) run(ctx context.Context, tool toolkit.Tool, args json.RawMessage, callID uuid.UUID) (out any, terr *toolkit.ToolError) {
	ctx, cancel := context.WithTimeout(ctx, p.o.Timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ERROR mcp tool panic (call_id=%s tool=%s): %v", callID, tool.Meta.Name, r)
			out, terr = nil, internalError(callID)
		}
	}()
	v, err := tool.Call(ctx, p.o.Gateway, args)
	if err == nil {
		return v, nil
	}
	var te *toolkit.ToolError
	var ae *toolkit.APIError
	switch {
	case errors.As(err, &te):
		return nil, te
	case errors.As(err, &ae):
		mapped := ae.ToToolError()
		if mapped.Code == toolkit.CodeInternal {
			log.Printf("ERROR mcp api (call_id=%s tool=%s status=%d): %s", callID, tool.Meta.Name, ae.Status, ae.Message)
			return nil, internalError(callID)
		}
		return nil, mapped
	default:
		log.Printf("ERROR mcp tool (call_id=%s tool=%s): %v", callID, tool.Meta.Name, err)
		return nil, internalError(callID)
	}
}

// internalError nunca expõe detalhes (SQL, stack): só a referência do call_id (§3.5).
func internalError(callID uuid.UUID) *toolkit.ToolError {
	return toolkit.NewToolError(toolkit.CodeInternal, fmt.Sprintf("erro interno; tente novamente (ref %s)", callID))
}

func (p *Pipeline) success(tool toolkit.Tool, out any, callID uuid.UUID) (*sdk.CallToolResult, *toolkit.ToolError) {
	b, err := json.Marshal(out)
	if err != nil {
		log.Printf("ERROR mcp marshal (call_id=%s tool=%s): %v", callID, tool.Meta.Name, err)
		return nil, internalError(callID)
	}
	if utf8.RuneCount(b) > MaxOutputChars {
		return nil, toolkit.NewToolError(toolkit.CodeInvalidInput,
			"resultado grande demais; refine os filtros ou use limit e offset menores")
	}
	if p.o.ValidateOutput {
		if err := tool.ValidateOutput(b); err != nil {
			log.Printf("ERROR mcp output inválido (call_id=%s tool=%s): %v", callID, tool.Meta.Name, err)
			return nil, internalError(callID)
		}
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b),
	}, nil
}

func errorResult(te *toolkit.ToolError) *sdk.CallToolResult {
	payload := map[string]any{"error": te}
	b, _ := json.Marshal(payload)
	return &sdk.CallToolResult{
		IsError:           true,
		Content:           []sdk.Content{&sdk.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b),
	}
}

func allowDuplicate(args json.RawMessage) bool {
	var a struct {
		AllowDuplicate bool `json:"allow_duplicate"`
	}
	_ = json.Unmarshal(args, &a)
	return a.AllowDuplicate
}

func canonicalArgs(args json.RawMessage) (canonical []byte, hash string) {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []byte("{}"), hashOf([]byte(args))
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}"), hashOf(args)
	}
	if m, ok := v.(map[string]any); ok {
		if _, has := m["allow_duplicate"]; has {
			delete(m, "allow_duplicate")
			if hb, err := json.Marshal(m); err == nil {
				return b, hashOf(hb)
			}
		}
	}
	return b, hashOf(b)
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
