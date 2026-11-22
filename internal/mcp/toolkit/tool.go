package toolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

type Meta struct {
	Name        string
	Title       string
	Description string
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	Dedupe      bool
}

func (m Meta) Scope() string {
	if m.ReadOnly {
		return ScopeRead
	}
	return ScopeWrite
}

type Tool struct {
	Meta         Meta
	InputSchema  *jsonschema.Schema
	OutputSchema *jsonschema.Schema

	inputResolved  *jsonschema.Resolved
	outputResolved *jsonschema.Resolved
	call           func(ctx context.Context, gw Gateway, args json.RawMessage) (any, error)
}

type SchemaOption func(in, out *jsonschema.Schema)

func MinItems(prop string, n int) SchemaOption {
	return func(in, _ *jsonschema.Schema) {
		if p := in.Properties[prop]; p != nil {
			p.MinItems = &n
		}
	}
}

func OutConst(prop string, v any) SchemaOption {
	return func(_, out *jsonschema.Schema) {
		if p := out.Properties[prop]; p != nil {
			c := v
			*p = jsonschema.Schema{Type: "string", Const: &c}
		}
	}
}

func NotZero(prop string) SchemaOption {
	return func(in, _ *jsonschema.Schema) {
		if p := in.Properties[prop]; p != nil {
			var zero any = 0
			p.Not = &jsonschema.Schema{Const: &zero}
		}
	}
}

type Handler[In, Out any] func(ctx context.Context, gw Gateway, in In) (Out, error)

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

// stripNull troca ["null", T] por T: opcionais de entrada são omitidos (não null — §3.2) e as saídas nunca emitem null (campos ausentes são omitidos).
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

func (t Tool) ValidateOutput(out json.RawMessage) error {
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		return err
	}
	return t.outputResolved.Validate(v)
}

func (t Tool) Call(ctx context.Context, gw Gateway, args json.RawMessage) (any, error) {
	return t.call(ctx, gw, args)
}

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

type Annotations struct {
	ReadOnlyHint    bool  `json:"readOnlyHint"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  bool  `json:"idempotentHint"`
	OpenWorldHint   bool  `json:"openWorldHint"`
}

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
