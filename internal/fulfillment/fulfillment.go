// Package fulfillment concentra as regras puras do atendimento da Sale.
package fulfillment

import (
	"errors"
	"time"
)

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

var (
	ErrIllegalTransition = errors.New("transição ilegal")
	ErrEmptyPurchase     = errors.New("purchase sem linhas não pode ser pedida")
)

var legalPurchaseTransitions = map[string]map[string]bool{
	PurchaseOpen:    {PurchaseOrdered: true},
	PurchaseOrdered: {PurchaseOpen: true, PurchaseReceived: true},
}

func CheckPurchaseTransition(from, to string, lineCount int64) error {
	if !legalPurchaseTransitions[from][to] {
		return ErrIllegalTransition
	}
	if to == PurchaseOrdered && lineCount == 0 {
		return ErrEmptyPurchase
	}
	return nil
}

// StampTransition devolve os novos ordered_at / received_at após a transição: ORDERED carimba ordered_at; voltar a OPEN limpa ordered_at; RECEIVED carimba received_at.
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

var (
	ErrSaleCanceled           = errors.New("sale cancelada")
	ErrItemsInOrderedPurchase = errors.New("sale com item em purchase pedida ou recebida")
)

func CheckDeliveryChange(current, next string, itemsInOrderedPurchase bool) error {
	if current == DeliveryCanceled {
		return ErrSaleCanceled
	}
	if next == DeliveryCanceled && itemsInOrderedPurchase {
		return ErrItemsInOrderedPurchase
	}
	return nil
}

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

type ItemLockFields struct {
	Origin     string
	SupplierID string
	Quantity   float64
	UnitCost   float64
}

// LockedFieldsChanged indica se um PUT altera origin, fornecedor_id, quantidade ou custo_unit (o que é proibido — 409 — para Item com purchase_id).
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
