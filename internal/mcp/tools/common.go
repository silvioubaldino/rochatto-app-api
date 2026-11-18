// Package tools reúne os módulos de ferramentas do Assistant connector (AYD-006 §4), um
// arquivo por domínio do produto. Cada módulo expõe a sua lista; o registro central só
// concatena. Ferramentas não têm regra de negócio nem acessam o banco: leem/escrevem pelo
// Gateway e adaptam EN ↔ PT (§5) em funções do próprio módulo.
package tools

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"
	"unicode"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"golang.org/x/text/unicode/norm"
)

type (
	UUID = toolkit.UUID
	Date = toolkit.Date
)

// get lê uma rota REST (GET) para o tipo T.
func get[T any](ctx context.Context, gw toolkit.Gateway, path string, q url.Values) (T, error) {
	var out T
	err := gw.Do(ctx, "GET", path, q, nil, &out)
	return out, err
}

// fold normaliza para busca: minúsculas e sem acentos.
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// nameMatches: busca por nome sem acento nem caixa; query vazia casa tudo.
func nameMatches(query, name string) bool {
	q := strings.TrimSpace(query)
	return q == "" || strings.Contains(fold(name), fold(q))
}

// opt converte string vazia/nula em nil (campo omitido na saída).
func opt(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return s
}

func optUUID(s *UUID) *UUID {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// setIf acrescenta o parâmetro de query quando não vazio.
func setIf(q url.Values, key, val string) {
	if val != "" {
		q.Set(key, val)
	}
}

func joinEnum[T ~string](vals []T) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = string(v)
	}
	return strings.Join(parts, ",")
}

// --- escritas ---

// send despacha uma escrita (POST/PUT/PATCH) pelo Gateway e decodifica a resposta 2xx em T.
func send[T any](ctx context.Context, gw toolkit.Gateway, method, path string, body any) (T, error) {
	var out T
	if body == nil {
		body = struct{}{}
	}
	err := gw.Do(ctx, method, path, nil, body, &out)
	return out, err
}

// afterWrite marca a falha de uma leitura POSTERIOR a uma escrita já gravada: devolve um erro
// opaco (nem *APIError nem *ToolError), que o pipeline responde como INTERNAL com call_id.
// A escrita não é desfeita e o recurso continua recuperável pelas ferramentas de leitura.
func afterWrite(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("leitura posterior à escrita falhou: %v", err)
}

func invalid(format string, a ...any) error {
	return toolkit.NewToolError(toolkit.CodeInvalidInput, fmt.Sprintf(format, a...))
}

func hasDecimals(v float64, places int) bool {
	scale := math.Pow10(places)
	return math.Abs(v*scale-math.Round(v*scale)) > 1e-6
}

// checkMoney confere o número de casas (2) de um valor em BRL; o limite mínimo é do schema.
func checkMoney(field string, v float64) error {
	if hasDecimals(v, 2) {
		return invalid("campo '%s' aceita no máximo 2 casas decimais", field)
	}
	return nil
}

func checkMoneyPtr(field string, v *float64) error {
	if v == nil {
		return nil
	}
	return checkMoney(field, *v)
}

// checkQty confere o número de casas (3) de uma quantidade.
func checkQty(field string, v float64) error {
	if hasDecimals(v, 3) {
		return invalid("campo '%s' aceita no máximo 3 casas decimais", field)
	}
	return nil
}

func noWarnings() []string { return []string{} }

// optStr converte string vazia em nil (campo omitido no corpo REST).
func optStr(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func optUUIDStr(s UUID) *UUID {
	if s == "" {
		return nil
	}
	return &s
}
