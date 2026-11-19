// Package saleitem concentra as regras puras do tipo de Item de Quote/Sale (AYD-010@context):
// PRODUCT (pedra do catálogo) × SERVICE (instalação, consultoria…). Sem acesso a banco.
package saleitem

import "math"

const (
	KindProduct = "PRODUCT"
	KindService = "SERVICE"
)

// Kinds são os valores válidos de `kind`.
var Kinds = map[string]bool{KindProduct: true, KindService: true}

// Resolve devolve o tipo efetivo: ausente/vazio = PRODUCT (default do contrato). ok=false se inválido.
func Resolve(kind *string) (string, bool) {
	if kind == nil || *kind == "" {
		return KindProduct, true
	}
	return *kind, Kinds[*kind]
}

// Fields são os campos do payload de um Item relevantes para a coerência por tipo.
type Fields struct {
	Kind        string
	HasProduct  bool
	HasService  bool
	HasOrigin   bool
	HasSupplier bool
}

// Validate confere a coerência do Item com o tipo (AYD-010 §2); devolve a mensagem de erro (400)
// ou "" quando válido.
func Validate(f Fields) string {
	switch f.Kind {
	case KindService:
		if f.HasProduct {
			return "item SERVICE não aceita 'produto_id'/'product_id'"
		}
		if f.HasOrigin {
			return "item SERVICE não aceita 'origin'"
		}
		if f.HasSupplier {
			return "item SERVICE não aceita 'fornecedor_id'"
		}
	case KindProduct:
		if f.HasService {
			return "item PRODUCT não aceita 'service_id'"
		}
	}
	return ""
}

// LaborCostAmount é o valor do custo da venda LABOR criado na conversão: quantidade × custo
// unitário estimado do Quote, em centavos.
func LaborCostAmount(quantity, unitCost float64) float64 {
	return math.Round(quantity*unitCost*100) / 100
}
