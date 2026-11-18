// Package tools reúne os módulos de ferramentas do Assistant connector (AYD-006 §4), um
// arquivo por domínio do produto. Cada módulo expõe a sua lista; o registro central só
// concatena. Ferramentas não têm regra de negócio nem acessam o banco: leem/escrevem pelo
// Gateway e adaptam EN ↔ PT (§5) em funções do próprio módulo.
package tools

import (
	"context"
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
