// Package finance reúne as regras puras do Financeiro: payment method e validação do período do painel.
package finance

import (
	"errors"
	"strings"
	"time"
)

const (
	MethodPix      = "PIX"
	MethodCash     = "CASH"
	MethodCard     = "CARD"
	MethodBoleto   = "BOLETO"
	MethodTransfer = "TRANSFER"
)

var PaymentMethods = map[string]bool{
	MethodPix: true, MethodCash: true, MethodCard: true, MethodBoleto: true, MethodTransfer: true,
}

var (
	ErrInvalidMethod = errors.New("payment_method inválido (PIX | CASH | CARD | BOLETO | TRANSFER)")
)

func NormalizeMethod(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	m := strings.TrimSpace(*raw)
	if m == "" {
		return nil, nil
	}
	if !PaymentMethods[m] {
		return nil, ErrInvalidMethod
	}
	return &m, nil
}

func ResolvePeriod(from, to string, now time.Time) (time.Time, time.Time, error) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	f := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	t := day
	var err error
	if s := strings.TrimSpace(from); s != "" {
		if f, err = time.Parse("2006-01-02", s); err != nil {
			return f, t, errors.New("from inválida (formato: YYYY-MM-DD)")
		}
	}
	if s := strings.TrimSpace(to); s != "" {
		if t, err = time.Parse("2006-01-02", s); err != nil {
			return f, t, errors.New("to inválida (formato: YYYY-MM-DD)")
		}
	}
	if f.After(t) {
		return f, t, errors.New("from deve ser anterior ou igual a to")
	}
	return f, t, nil
}
