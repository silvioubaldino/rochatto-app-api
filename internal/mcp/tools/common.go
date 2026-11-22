// Package tools reúne os módulos de ferramentas do Assistant connector, um arquivo por domínio do produto.
package tools

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"golang.org/x/text/unicode/norm"
)

type (
	UUID = toolkit.UUID
	Date = toolkit.Date
)

func get[T any](ctx context.Context, gw toolkit.Gateway, path string, q url.Values) (T, error) {
	var out T
	err := gw.Do(ctx, "GET", path, q, nil, &out)
	return out, err
}

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

func nameMatches(query, name string) bool {
	q := strings.TrimSpace(query)
	return q == "" || strings.Contains(fold(name), fold(q))
}

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

func send[T any](ctx context.Context, gw toolkit.Gateway, method, path string, body any) (T, error) {
	var out T
	if body == nil {
		body = struct{}{}
	}
	err := gw.Do(ctx, method, path, nil, body, &out)
	return out, err
}

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

func checkQty(field string, v float64) error {
	if hasDecimals(v, 3) {
		return invalid("campo '%s' aceita no máximo 3 casas decimais", field)
	}
	return nil
}

func noWarnings() []string { return []string{} }

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

func todaySP() string {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.FixedZone("BRT", -3*60*60)
	}
	return time.Now().In(loc).Format("2006-01-02")
}
