// Package fulfillment concentra as regras puras do atendimento da Sale (AYD-002@context):
// enums, transições de status da Purchase, pagamento ao Supplier, cancelamento da Sale e
// travas de edição de Items que estão numa Purchase. Sem acesso a banco — os handlers
// carregam o estado, aplicam estas regras e persistem.
package fulfillment

import (
	"errors"
	"time"
)

// --- Enums (AYD-002) ---

const (
	OriginMadeToOrder = "MADE_TO_ORDER"
	OriginFromStock   = "FROM_STOCK"

	KindMadeToOrder        = "MADE_TO_ORDER"
	KindStockReplenishment = "STOCK_REPLENISHMENT"

	PurchaseOpen     = "OPEN"
	PurchaseOrdered  = "ORDERED"
	PurchaseReceived = "RECEIVED"

	PaymentPending = "PENDING"
	PaymentPaid    = "PAID"

	DeliveryToDeliver = "TO_DELIVER"
	DeliveryDelivered = "DELIVERED"
	DeliveryCanceled  = "CANCELED"

	SalePaymentPending = "PENDING"
	SalePaymentPartial = "PARTIAL"
	SalePaymentPaid    = "PAID"

	AdjustmentInitial = "INITIAL"
	AdjustmentCount   = "COUNT"
	AdjustmentOther   = "OTHER"

	// WarningInsufficientStock é devolvido em warnings[] do Item FROM_STOCK sem saldo (RN-16).
	WarningInsufficientStock = "INSUFFICIENT_STOCK"
)

var (
	ItemOrigins            = set(OriginMadeToOrder, OriginFromStock)
	PurchaseKinds          = set(KindMadeToOrder, KindStockReplenishment)
	PurchaseStatuses       = set(PurchaseOpen, PurchaseOrdered, PurchaseReceived)
	SupplierPaymentStatus  = set(PaymentPending, PaymentPaid)
	SaleDeliveryStatuses   = set(DeliveryToDeliver, DeliveryDelivered, DeliveryCanceled)
	SalePaymentStatuses    = set(SalePaymentPending, SalePaymentPartial, SalePaymentPaid)
	StockAdjustmentReasons = set(AdjustmentInitial, AdjustmentCount, AdjustmentOther)
)

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// --- Purchase: status ---

var (
	ErrIllegalTransition = errors.New("transição ilegal")
	ErrEmptyPurchase     = errors.New("purchase sem linhas não pode ser pedida")
)

// legalPurchaseTransitions: só OPEN→ORDERED, ORDERED→OPEN e ORDERED→RECEIVED (RECEIVED é terminal).
var legalPurchaseTransitions = map[string]map[string]bool{
	PurchaseOpen:    {PurchaseOrdered: true},
	PurchaseOrdered: {PurchaseOpen: true, PurchaseReceived: true},
}

// CheckPurchaseTransition valida a mudança de status. lineCount = linhas da Purchase (Items
// de Sale ou linhas de reposição). Devolve ErrIllegalTransition (409) ou ErrEmptyPurchase (422).
func CheckPurchaseTransition(from, to string, lineCount int64) error {
	if !legalPurchaseTransitions[from][to] {
		return ErrIllegalTransition
	}
	if to == PurchaseOrdered && lineCount == 0 {
		return ErrEmptyPurchase
	}
	return nil
}

// StampTransition devolve os novos ordered_at / received_at após a transição: ORDERED carimba
// ordered_at; voltar a OPEN limpa ordered_at; RECEIVED carimba received_at.
func StampTransition(to string, orderedAt, receivedAt *time.Time, today time.Time) (*time.Time, *time.Time) {
	d := dateOnly(today)
	switch to {
	case PurchaseOrdered:
		return &d, receivedAt
	case PurchaseOpen:
		return nil, receivedAt
	case PurchaseReceived:
		return orderedAt, &d
	}
	return orderedAt, receivedAt
}

// --- Purchase: pagamento ao Supplier ---

var ErrNegativePaidAmount = errors.New("paid_amount deve ser >= 0")

// ResolvePayment aplica os defaults do PATCH /purchases/{id}/payment: PAID exige paid_amount ≥ 0
// (default = total_cost) e paid_at (default = hoje); PENDING zera paid_amount e paid_at.
func ResolvePayment(status string, amount *float64, at *time.Time, totalCost float64, today time.Time) (*float64, *time.Time, error) {
	if status == PaymentPending {
		return nil, nil, nil
	}
	if amount == nil {
		v := totalCost
		amount = &v
	}
	if *amount < 0 {
		return nil, nil, ErrNegativePaidAmount
	}
	if at == nil {
		d := dateOnly(today)
		at = &d
	}
	return amount, at, nil
}

// ToPay replica a regra da view: total_cost − pago se PENDING; 0 se PAID.
func ToPay(status string, totalCost float64, paidAmount *float64) float64 {
	if status == PaymentPaid {
		return 0
	}
	paid := 0.0
	if paidAmount != nil {
		paid = *paidAmount
	}
	return totalCost - paid
}

// --- Sale ---

var (
	ErrSaleCanceled           = errors.New("sale cancelada")
	ErrItemsInOrderedPurchase = errors.New("sale com item em purchase pedida ou recebida")
)

// CheckDeliveryChange valida PATCH /vendas/{id}/delivery-status. CANCELED é terminal (MVP);
// cancelar exige que nenhum Item esteja numa Purchase ORDERED/RECEIVED.
func CheckDeliveryChange(current, next string, itemsInOrderedPurchase bool) error {
	if current == DeliveryCanceled {
		return ErrSaleCanceled
	}
	if next == DeliveryCanceled && itemsInOrderedPurchase {
		return ErrItemsInOrderedPurchase
	}
	return nil
}

// SalePaymentStatus replica a regra da view vw_vendas_resumo: PENDING se recebido = 0;
// PARTIAL se 0 < recebido < total; PAID se recebido ≥ total.
func SalePaymentStatus(total, received float64) string {
	switch {
	case received <= 0:
		return SalePaymentPending
	case received < total:
		return SalePaymentPartial
	default:
		return SalePaymentPaid
	}
}

// ItemLockFields são os campos de um Item de Sale travados quando ele está numa Purchase.
type ItemLockFields struct {
	Origin     string
	SupplierID string // "" = sem Supplier
	Quantity   float64
	UnitCost   float64
}

// LockedFieldsChanged indica se um PUT altera origin, fornecedor_id, quantidade ou custo_unit
// (o que é proibido — 409 — para Item com purchase_id).
func LockedFieldsChanged(before, after ItemLockFields) bool {
	return before.Origin != after.Origin ||
		before.SupplierID != after.SupplierID ||
		before.Quantity != after.Quantity ||
		before.UnitCost != after.UnitCost
}

// InsufficientStock: Balance já considerando o Item gravado abaixo de zero → aviso (RN-16).
func InsufficientStock(balanceAfter float64) bool { return balanceAfter < 0 }

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
