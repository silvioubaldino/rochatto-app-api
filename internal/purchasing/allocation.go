// Package purchasing reúne as regras puras da composição da compra (AYD-008 / SPEC-009):
// tipos de custo adicional, método de rateio e o rateio em si (espelho em Go da view
// vw_purchase_line_costs — a view é a fonte da verdade em runtime; esta função documenta e
// testa a regra). Nada aqui é persistido: rateio e custo de entrada são sempre calculados.
package purchasing

import (
	"errors"
	"strings"
)

// Tipos de custo adicional (enum purchase_charge_kind).
const (
	ChargeFreight   = "FREIGHT"
	ChargeUnloading = "UNLOADING"
	ChargeTax       = "TAX"
	ChargeOther     = "OTHER"
)

// ChargeKinds é o conjunto aceito de tipos de custo adicional.
var ChargeKinds = map[string]bool{ChargeFreight: true, ChargeUnloading: true, ChargeTax: true, ChargeOther: true}

// Métodos de rateio (enum allocation_method).
const (
	ByQuantity = "BY_QUANTITY"
	ByValue    = "BY_VALUE"
)

// Methods é o conjunto aceito de métodos de rateio; o padrão é BY_QUANTITY ("por m²").
var Methods = map[string]bool{ByQuantity: true, ByValue: true}

// WarningOversettled é o warning de conta com quitado > devido (mesmo valor do AYD-007).
const WarningOversettled = "OVERSETTLED"

// ErrMixedUnits: rateio por quantidade com linhas de unidades diferentes (§3) → 422.
var ErrMixedUnits = errors.New("linhas com unidades diferentes: use rateio por valor (BY_VALUE)")

// MaxAmount é o maior valor aceito num custo adicional (DECIMAL(12,2)).
const MaxAmount = 9_999_999_999.99

// ChargeLabel é o rótulo do componente PURCHASE_CHARGE (rótulo do kind + descrição).
func ChargeLabel(kind string, description *string) string {
	base := map[string]string{ChargeFreight: "Frete de compra", ChargeUnloading: "Descarga", ChargeTax: "Imposto"}[kind]
	if base == "" {
		base = "Outros"
	}
	if description != nil {
		if d := strings.TrimSpace(*description); d != "" {
			return base + " · " + d
		}
	}
	return base
}

// Line é uma linha da compra (reposição ou Item de Sale sob encomenda) para o rateio.
type Line struct {
	Unit     string // unidade bruta do Product ("" = sem Product/unidade)
	Quantity float64
	UnitCost float64
}

// BaseCost é quantidade × custo unitário.
func (l Line) BaseCost() float64 { return l.Quantity * l.UnitCost }

// NormalizeUnit normaliza a unidade para comparação (trim + minúsculas).
func NormalizeUnit(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

// UnitsMixed informa se há mais de uma unidade (normalizada) entre as linhas; linha sem unidade
// assume a das demais e não conta.
func UnitsMixed(lines []Line) bool {
	seen := ""
	for _, l := range lines {
		u := NormalizeUnit(l.Unit)
		if u == "" {
			continue
		}
		if seen == "" {
			seen = u
		} else if seen != u {
			return true
		}
	}
	return false
}

// Allocate rateia chargesTotal entre as linhas, na ordem recebida (precisão total — arredonde só
// na exibição). BY_QUANTITY: chargesTotal × quantidade / Σ quantidade. BY_VALUE: chargesTotal ×
// base_cost / Σ base_cost, caindo para BY_QUANTITY se Σ base_cost = 0. Bonificação (custo 0)
// recebe rateio por quantidade. BY_QUANTITY com unidades diferentes → ErrMixedUnits.
func Allocate(method string, chargesTotal float64, lines []Line) ([]float64, error) {
	if method == "" {
		method = ByQuantity
	}
	if !Methods[method] {
		return nil, errors.New("método de rateio inválido: " + method)
	}
	out := make([]float64, len(lines))
	var qty, base float64
	for _, l := range lines {
		qty += l.Quantity
		base += l.BaseCost()
	}
	useValue := method == ByValue && base > 0
	if !useValue && method == ByQuantity && chargesTotal != 0 && UnitsMixed(lines) {
		return nil, ErrMixedUnits
	}
	for i, l := range lines {
		switch {
		case useValue:
			out[i] = chargesTotal * l.BaseCost() / base
		case qty > 0:
			out[i] = chargesTotal * l.Quantity / qty
		}
	}
	return out, nil
}

// LandedUnitCost é o custo de entrada unitário: (base + rateio) / quantidade.
func LandedUnitCost(l Line, allocated float64) float64 {
	if l.Quantity == 0 {
		return 0
	}
	return (l.BaseCost() + allocated) / l.Quantity
}
