package toolkit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type sIn struct {
	Query  string         `json:"query,omitempty" jsonschema:"texto"`
	ID     UUID           `json:"id"`
	Opt    *UUID          `json:"opt,omitempty"`
	Status DeliveryStatus `json:"status,omitempty"`
	When   Date           `json:"when,omitempty"`
	Flag   *bool          `json:"flag,omitempty"`
	Page
}
type sOut struct {
	Items []string `json:"items"`
	Note  *string  `json:"note,omitempty"`
}

func TestDefineDerivesStrictSchemasFromTypes(t *testing.T) {
	tool := Define(Meta{Name: "get_x", Title: "X", Description: "d", ReadOnly: true, Idempotent: true},
		func(context.Context, Gateway, sIn) (sOut, error) { return sOut{Items: []string{}}, nil })
	in, _ := json.Marshal(tool.InputSchema)
	for _, frag := range []string{
		`"additionalProperties":false`, `"required":["id"]`, `"format":"uuid"`, `"enum":["TO_DELIVER","DELIVERED","CANCELED"]`,
		`"maximum":100`, `"limit"`, `"offset"`, `"pattern":"^\\d{4}-\\d{2}-\\d{2}$"`,
	} {
		if !strings.Contains(string(in), frag) {
			t.Errorf("inputSchema sem %s: %s", frag, in)
		}
	}
	if strings.Contains(string(in), `"null"`) || tool.OutputSchema.Properties["note"].Type != "string" {
		t.Errorf("opcionais não podem aceitar null: %s", in)
	}

	ok := []string{`{"id":"3f2c3a5e-1111-4a2a-9b6a-0123456789ab"}`, `{"id":"3f2c3a5e-1111-4a2a-9b6a-0123456789ab","limit":100,"status":"DELIVERED"}`}
	bad := []string{`{}`, `{"id":"x","extra":1}`, `{"id":"3f2c3a5e-1111-4a2a-9b6a-0123456789ab","limit":101}`, `{"id":"3f2c3a5e-1111-4a2a-9b6a-0123456789ab","status":"X"}`,
		`{"id":"3f2c3a5e-1111-4a2a-9b6a-0123456789ab","when":"18/11/2026"}`, `[]`, `{"id":"3f2c3a5e-1111-4a2a-9b6a-0123456789ab","flag":null}`}
	for _, a := range ok {
		if err := tool.ValidateInput(json.RawMessage(a)); err != nil {
			t.Errorf("%s deveria ser válido: %v", a, err)
		}
	}
	for _, a := range bad {
		if err := tool.ValidateInput(json.RawMessage(a)); err == nil {
			t.Errorf("%s deveria ser inválido", a)
		}
	}
	if err := tool.ValidateOutput(json.RawMessage(`{"items":["a"]}`)); err != nil {
		t.Error(err)
	}
	if err := tool.ValidateOutput(json.RawMessage(`{"items":null}`)); err == nil {
		t.Error("saída com items null deve violar o schema")
	}
}

func TestToolErrorMapping(t *testing.T) {
	for status, code := range map[int]string{400: CodeInvalidInput, 404: CodeNotFound, 409: CodeConflict, 422: CodeUnprocessable, 500: CodeInternal, 502: CodeInternal, 403: CodeInternal} {
		if got := (&APIError{Status: status, Message: "m"}).ToToolError().Code; got != code {
			t.Errorf("status %d → %s, quero %s", status, got, code)
		}
	}
}

func TestScopeFollowsReadOnly(t *testing.T) {
	if (Meta{ReadOnly: true}).Scope() != ScopeRead || (Meta{}).Scope() != ScopeWrite {
		t.Error("read-only exige scope read; o resto, write")
	}
}
