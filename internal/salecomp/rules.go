// Package salecomp reúne as regras puras da composição da venda (AYD-009 / SPEC-010): tipos de
// cobrança ao cliente (Sale charge) e de custo da venda (Sale cost), credor por tipo e rótulos.
// A view (vw_obligation_components / vw_vendas_resumo) é a fonte em runtime; os rótulos aqui
// espelham os da view e são testados contra ela.
package salecomp

import "strings"

const (
	ChargeFreight = "FREIGHT"
	ChargeOther   = "OTHER"
)

// ChargeKinds é o enum sale_charge_kind (cobrado do cliente).
var ChargeKinds = map[string]bool{ChargeFreight: true, ChargeOther: true}

const (
	CostFreight    = "FREIGHT"
	CostLabor      = "LABOR"
	CostCommission = "COMMISSION"
	CostOther      = "OTHER"
)

// CostKinds é o enum sale_cost_kind (pago a terceiros pela venda).
var CostKinds = map[string]bool{CostFreight: true, CostLabor: true, CostCommission: true, CostOther: true}

const (
	PayeeSupplier = "SUPPLIER"
	PayeePartner  = "PARTNER"
)

const WarningOversettled = "OVERSETTLED"

// ComponentSaleCharge e ComponentSaleCost são os tipos de componente das contas da Sale.
const (
	ComponentSaleCharge = "SALE_CHARGE"
	ComponentSaleCost   = "SALE_COST"
)

const MaxAmount = 9_999_999_999.99

// PayeeType devolve o tipo de credor aceito pelo kind do custo: COMMISSION → Partner; os demais
// → Supplier (AYD-009 §2).
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

// ChargeLabel é o rótulo do componente da cobrança.
func ChargeLabel(kind string, description *string) string {
	base := "Outras cobranças"
	if kind == ChargeFreight {
		base = "Frete cobrado"
	}
	return withDescription(base, description)
}

// CostLabel é o rótulo do componente do custo da venda.
func CostLabel(kind string, description *string) string {
	base := map[string]string{CostFreight: "Frete pago", CostLabor: "Mão de obra", CostCommission: "Comissão"}[kind]
	if base == "" {
		base = "Outro custo"
	}
	return withDescription(base, description)
}

// Profit é a leitura do RN-04 (AYD-009 §3): receita − custo, em que a receita soma Items e
// cobranças, e o custo soma pedras (já com o rateio da compra) e custos da venda.
func Profit(itemsTotal, chargesTotal, stonesCost, saleCosts float64) float64 {
	return itemsTotal + chargesTotal - stonesCost - saleCosts
}
