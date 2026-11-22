// Package salecomp reúne as regras puras da composição da venda.
package salecomp

import "strings"

const (
	ChargeFreight = "FREIGHT"
	ChargeOther   = "OTHER"
)

var ChargeKinds = map[string]bool{ChargeFreight: true, ChargeOther: true}

const (
	CostFreight    = "FREIGHT"
	CostLabor      = "LABOR"
	CostCommission = "COMMISSION"
	CostOther      = "OTHER"
)

var CostKinds = map[string]bool{CostFreight: true, CostLabor: true, CostCommission: true, CostOther: true}

const (
	PayeeSupplier = "SUPPLIER"
	PayeePartner  = "PARTNER"
)

const WarningOversettled = "OVERSETTLED"

const (
	ComponentSaleCharge = "SALE_CHARGE"
	ComponentSaleCost   = "SALE_COST"
)

const MaxAmount = 9_999_999_999.99

// PayeeType devolve o tipo de credor aceito pelo kind do custo: COMMISSION → Partner; os demais → Supplier.
func PayeeType(costKind string) string {
	if costKind == CostCommission {
		return PayeePartner
	}
	return PayeeSupplier
}

func withDescription(base string, description *string) string {
	if description != nil {
		if d := strings.TrimSpace(*description); d != "" {
			return base + " · " + d
		}
	}
	return base
}

func ChargeLabel(kind string, description *string) string {
	base := "Outras cobranças"
	if kind == ChargeFreight {
		base = "Frete cobrado"
	}
	return withDescription(base, description)
}

func CostLabel(kind string, description *string) string {
	base := map[string]string{CostFreight: "Frete pago", CostLabor: "Mão de obra", CostCommission: "Comissão"}[kind]
	if base == "" {
		base = "Outro custo"
	}
	return withDescription(base, description)
}

func Profit(itemsTotal, chargesTotal, stonesCost, saleCosts float64) float64 {
	return itemsTotal + chargesTotal - stonesCost - saleCosts
}
