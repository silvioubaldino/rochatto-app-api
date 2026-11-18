// Package finance reúne as regras puras do Financeiro (AYD-004): payment method do Receipt e
// validação do período do painel. Sem acesso a banco — os handlers carregam o estado e
// aplicam estas regras.
package finance

import (
	"errors"
	"strings"
	"time"
)

// PaymentMethod (enum `payment_method` no banco).
const (
	MethodPix      = "PIX"
	MethodCash     = "CASH"
	MethodCard     = "CARD"
	MethodBoleto   = "BOLETO"
	MethodTransfer = "TRANSFER"
)

// PaymentMethods é o conjunto de valores aceitos.
var PaymentMethods = map[string]bool{
	MethodPix: true, MethodCash: true, MethodCard: true, MethodBoleto: true, MethodTransfer: true,
}

// Status do Receipt (enum legado `status_pagamento`).
const (
	ReceiptPending  = "pendente"
	ReceiptReceived = "recebido"
)

var (
	// ErrInvalidMethod: payment_method fora do enum.
	ErrInvalidMethod = errors.New("payment_method inválido (PIX | CASH | CARD | BOLETO | TRANSFER)")
	// ErrMethodRequired: Receipt "recebido" sem payment_method.
	ErrMethodRequired = errors.New("campo 'payment_method' é obrigatório para recebimento")
)

// NormalizeMethod trata vazio/espaços como ausente e valida o enum.
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

// CheckMethodForStatus aplica a regra do AYD-004: o Receipt que fica "recebido" exige
// payment_method; o "pendente" não (forma ainda combinada).
func CheckMethodForStatus(status string, method *string) error {
	if status == ReceiptReceived && method == nil {
		return ErrMethodRequired
	}
	return nil
}

// ResolvePeriod interpreta from/to ("YYYY-MM-DD", vazio = default) do painel: default
// from = 1º dia do mês de now, to = hoje (data de now). Erro se data inválida ou from > to.
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
