package purchasing

import (
	"errors"
	"strings"
)

const (
	ChargeFreight   = "FREIGHT"
	ChargeUnloading = "UNLOADING"
	ChargeTax       = "TAX"
	ChargeOther     = "OTHER"
)

var ChargeKinds = map[string]bool{ChargeFreight: true, ChargeUnloading: true, ChargeTax: true, ChargeOther: true}

const (
	ByQuantity = "BY_QUANTITY"
	ByValue    = "BY_VALUE"
)

var Methods = map[string]bool{ByQuantity: true, ByValue: true}

const WarningOversettled = "OVERSETTLED"

var ErrMixedUnits = errors.New("linhas com unidades diferentes: use rateio por valor (BY_VALUE)")

const MaxAmount = 9_999_999_999.99

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

type Line struct {
	Unit     string // unidade bruta do Product ("" = sem Product/unidade)
	Quantity float64
	UnitCost float64
}

func (l Line) BaseCost() float64 { return l.Quantity * l.UnitCost }

func NormalizeUnit(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

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

func LandedUnitCost(l Line, allocated float64) float64 {
	if l.Quantity == 0 {
		return 0
	}
	return (l.BaseCost() + allocated) / l.Quantity
}
