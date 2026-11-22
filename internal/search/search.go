// Package search concentra as regras puras da busca global.
package search

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	Customer = "CUSTOMER"
	Quote    = "QUOTE"
	Sale     = "SALE"
	Purchase = "PURCHASE"
	Product  = "PRODUCT"
	Service  = "SERVICE"
	Supplier = "SUPPLIER"
	Partner  = "PARTNER"
)

var AllTypes = []string{Customer, Quote, Sale, Purchase, Product, Service, Supplier, Partner}

const (
	MinQueryLen  = 2
	DefaultLimit = 5
	MaxLimit     = 10
)

var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "ç", "c",
)

// Normalize devolve o termo sem acento, em minúsculas e sem espaços nas pontas — a mesma dobra que o SQL aplica às colunas (translate(lower(col), ...)).
func Normalize(s string) string {
	return accents.Replace(strings.ToLower(strings.TrimSpace(s)))
}

func AsNumber(q string) (int, bool) {
	q = strings.TrimPrefix(strings.TrimSpace(q), "#")
	if q == "" || len(q) > 9 {
		return 0, false
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 0 {
		return 0, false
	}
	for _, r := range q {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	return n, true
}

func EscapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

type Query struct {
	Text   string
	Number *int
	Digits string
}

func ParseQuery(raw string) (Query, error) {
	trimmed := strings.TrimSpace(raw)
	if utf8.RuneCountInString(trimmed) < MinQueryLen {
		return Query{}, errors.New("q deve ter ao menos 2 caracteres")
	}
	q := Query{Text: Normalize(trimmed)}
	if n, ok := AsNumber(trimmed); ok {
		q.Number = &n
	}
	var digits strings.Builder
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if strings.Trim(trimmed, "0123456789 ()+-.") == "" {
		q.Digits = digits.String()
	}
	return q, nil
}

// Duplicados somem; ordem do pedido.
func ParseTypes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return append([]string(nil), AllTypes...), nil
	}
	valid := map[string]bool{}
	for _, t := range AllTypes {
		valid[t] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.ToUpper(strings.TrimSpace(t))
		if !valid[t] {
			return nil, errors.New("types inválido (" + strings.Join(AllTypes, " | ") + ")")
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}

// ParseLimit interpreta `limit` (vazio = 5; 1..10).
func ParseLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultLimit, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > MaxLimit {
		return 0, errors.New("limit inválido (1 a 10)")
	}
	return n, nil
}
