package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// Scopes (§3.8): read = ferramentas readOnlyHint; write = as demais.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// Meta são os metadados de uma ferramenta (ficam junto da definição — regra 3 do AYD).
type Meta struct {
	Name        string // snake_case, inglês, <verbo>_<objeto>
	Title       string // pt-BR, curto, imperativo
	Description string // pt-BR (§3.1)
	ReadOnly    bool
	Destructive bool // só escrita
	Idempotent  bool
	Dedupe      bool // criação deduplicada (§3.7) — exige o estágio de dedupe do pipeline
}

// Scope é o scope exigido da Connection.
func (m Meta) Scope() string {
	if m.ReadOnly {
		return ScopeRead
	}
	return ScopeWrite
}

// Tool é uma ferramenta pronta para registro: metadados, schemas e o executor tipado.
type Tool struct {
	Meta         Meta
	InputSchema  *jsonschema.Schema
	OutputSchema *jsonschema.Schema

	inputResolved  *jsonschema.Resolved
	outputResolved *jsonschema.Resolved
	call           func(ctx context.Context, gw Gateway, args json.RawMessage) (any, error)
}

// SchemaOption ajusta os schemas de entrada e saída de uma ferramenta (ver Define).
type SchemaOption func(in, out *jsonschema.Schema)

// MinItems exige ao menos n elementos na propriedade array prop da entrada (§4.3: "≥ 1").
func MinItems(prop string, n int) SchemaOption {
	return func(in, _ *jsonschema.Schema) {
		if p := in.Properties[prop]; p != nil {
			p.MinItems = &n
		}
	}
}

// OutConst fixa o valor de uma propriedade da saída (ex.: delivery_status "CANCELED").
func OutConst(prop string, v any) SchemaOption {
	return func(_, out *jsonschema.Schema) {
		if p := out.Properties[prop]; p != nil {
			c := v
			*p = jsonschema.Schema{Type: "string", Const: &c}
		}
	}
}

// NotZero proíbe o valor 0 na propriedade numérica prop da entrada (§4.3: "≠ 0").
func NotZero(prop string) SchemaOption {
	return func(in, _ *jsonschema.Schema) {
		if p := in.Properties[prop]; p != nil {
			var zero any = 0
			p.Not = &jsonschema.Schema{Const: &zero}
		}
	}
}

// Handler é o corpo de uma ferramenta. Devolve o Out, ou *ToolError / *APIError.
type Handler[In, Out any] func(ctx context.Context, gw Gateway, in In) (Out, error)

// Define cria uma ferramenta; os schemas de entrada e saída são derivados de In e Out.
// Entra em pânico se os tipos não geram schema válido (erro de programação, pego no teste).
//
// opts refinam os schemas derivados quando as tags não bastam (ex.: minItems, const); rodam
// antes da resolução, então a validação de entrada e de saída os enxerga.
func Define[In, Out any](meta Meta, h Handler[In, Out], opts ...SchemaOption) Tool {
	in := mustSchema(reflect.TypeFor[In]())
	out := mustSchema(reflect.TypeFor[Out]())
	for _, o := range opts {
		o(in, out)
	}
	t := Tool{Meta: meta, InputSchema: in, OutputSchema: out}
	var err error
	if t.inputResolved, err = in.Resolve(&jsonschema.ResolveOptions{}); err != nil {
		panic(fmt.Sprintf("tool %s: input schema: %v", meta.Name, err))
	}
	if t.outputResolved, err = out.Resolve(&jsonschema.ResolveOptions{}); err != nil {
		panic(fmt.Sprintf("tool %s: output schema: %v", meta.Name, err))
	}
	t.call = func(ctx context.Context, gw Gateway, args json.RawMessage) (any, error) {
		var v In
		if len(bytes.TrimSpace(args)) > 0 {
			if err := json.Unmarshal(args, &v); err != nil {
				return nil, NewToolError(CodeInvalidInput, "argumentos inválidos: "+err.Error())
			}
		}
		return h(ctx, gw, v)
	}
	return t
}

func mustSchema(rt reflect.Type) *jsonschema.Schema {
	s, err := jsonschema.ForType(rt, &jsonschema.ForOptions{TypeSchemas: TypeSchemas()})
	if err != nil {
		panic(fmt.Sprintf("schema de %v: %v", rt, err))
	}
	stripNull(s)
	return s
}

// stripNull troca ["null", T] por T: opcionais de entrada são omitidos (não null — §3.2) e
// as saídas nunca emitem null (campos ausentes são omitidos).
func stripNull(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if len(s.Types) > 0 {
		var rest []string
		for _, t := range s.Types {
			if t != "null" {
				rest = append(rest, t)
			}
		}
		if len(rest) == 1 {
			s.Type, s.Types = rest[0], nil
		} else {
			s.Types = rest
		}
	}
	for _, p := range s.Properties {
		stripNull(p)
	}
	stripNull(s.Items)
	for _, l := range [][]*jsonschema.Schema{s.AllOf, s.AnyOf, s.OneOf, s.PrefixItems} {
		for _, x := range l {
			stripNull(x)
		}
	}
	if s.AdditionalProperties != nil {
		stripNull(s.AdditionalProperties)
	}
	for _, d := range s.Defs {
		stripNull(d)
	}
}

// ValidateInput valida os argumentos contra o schema de entrada.
func (t Tool) ValidateInput(args json.RawMessage) error {
	var v any = map[string]any{}
	if len(bytes.TrimSpace(args)) > 0 {
		if err := json.Unmarshal(args, &v); err != nil {
			return err
		}
	}
	if _, ok := v.(map[string]any); !ok {
		return fmt.Errorf("os argumentos devem ser um objeto")
	}
	return t.inputResolved.Validate(v)
}

// ValidateOutput valida a saída (JSON) contra o schema de saída — usado nos testes.
func (t Tool) ValidateOutput(out json.RawMessage) error {
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return err
	}
	return t.outputResolved.Validate(v)
}

// Call executa o handler (após a validação do pipeline).
func (t Tool) Call(ctx context.Context, gw Gateway, args json.RawMessage) (any, error) {
	return t.call(ctx, gw, args)
}

// Entry é a forma serializada (determinística) da ferramenta no catálogo/golden.
type Entry struct {
	Name         string             `json:"name"`
	Title        string             `json:"title"`
	Description  string             `json:"description"`
	Scope        string             `json:"scope"`
	Annotations  Annotations        `json:"annotations"`
	Dedupe       bool               `json:"dedupe"`
	InputSchema  *jsonschema.Schema `json:"inputSchema"`
	OutputSchema *jsonschema.Schema `json:"outputSchema"`
}

// Annotations espelha as dicas do MCP (§3.6).
type Annotations struct {
	ReadOnlyHint    bool  `json:"readOnlyHint"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  bool  `json:"idempotentHint"`
	OpenWorldHint   bool  `json:"openWorldHint"`
}

// Entry devolve a forma serializável da ferramenta.
func (t Tool) Entry() Entry {
	a := Annotations{ReadOnlyHint: t.Meta.ReadOnly, IdempotentHint: t.Meta.Idempotent, OpenWorldHint: false}
	if !t.Meta.ReadOnly {
		a.DestructiveHint = ptr(t.Meta.Destructive)
	}
	return Entry{
		Name: t.Meta.Name, Title: t.Meta.Title, Description: t.Meta.Description, Scope: t.Meta.Scope(),
		Annotations: a, Dedupe: t.Meta.Dedupe, InputSchema: t.InputSchema, OutputSchema: t.OutputSchema,
	}
}
