package auth

import (
	"errors"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Um token Firebase válido não basta: o e-mail precisa estar na lista e verificado.
type Allowlist struct {
	emails map[string]struct{}
}

var (
	errEmailMissing    = errors.New("token sem e-mail")
	errEmailUnverified = errors.New("e-mail não verificado")
	errEmailNotAllowed = errors.New("e-mail não autorizado")
)

func ParseAllowlist(raw string) Allowlist {
	a := Allowlist{emails: map[string]struct{}{}}
	for _, e := range strings.Split(raw, ",") {
		if e = normalizeEmail(e); e != "" {
			a.emails[e] = struct{}{}
		}
	}
	return a
}

func (a Allowlist) Empty() bool { return len(a.emails) == 0 }

func (a Allowlist) Len() int { return len(a.emails) }

func (a Allowlist) Check(claims jwt.MapClaims) error {
	if a.Empty() {
		return nil
	}
	email, _ := claims["email"].(string)
	email = normalizeEmail(email)
	if email == "" {
		return errEmailMissing
	}
	if verified, _ := claims["email_verified"].(bool); !verified {
		return errEmailUnverified
	}
	if _, ok := a.emails[email]; !ok {
		return errEmailNotAllowed
	}
	return nil
}

// AllowsEmail diz se o e-mail está na allowlist (lista vazia = aberta, só em desenvolvimento).
func (a Allowlist) AllowsEmail(email string) bool {
	if a.Empty() {
		return true
	}
	_, ok := a.emails[normalizeEmail(email)]
	return ok
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ErrAllowlistRequired: em produção a lista de e-mails é obrigatória.
var ErrAllowlistRequired = errors.New("ALLOWED_EMAILS vazia em produção: defina o(s) e-mail(s) do dono")

// Em ENV=production uma lista vazia é erro (o servidor não deve subir aberto a qualquer conta Google).
func LoadAllowlist(env, raw string) (Allowlist, error) {
	a := ParseAllowlist(raw)
	if a.Empty() && env == "production" {
		return a, ErrAllowlistRequired
	}
	return a, nil
}
