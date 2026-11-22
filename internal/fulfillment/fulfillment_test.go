package fulfillment

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCheckPurchaseTransition(t *testing.T) {
	cases := []struct {
		from, to string
		lines    int64
		want     error
	}{
		{PurchaseOpen, PurchaseOrdered, 1, nil},
		{PurchaseOpen, PurchaseOrdered, 0, ErrEmptyPurchase},
		{PurchaseOrdered, PurchaseOpen, 1, nil},
		{PurchaseOrdered, PurchaseReceived, 1, nil},
		{PurchaseOpen, PurchaseReceived, 1, ErrIllegalTransition},
		{PurchaseOpen, PurchaseOpen, 1, ErrIllegalTransition},
		{PurchaseOrdered, PurchaseOrdered, 1, ErrIllegalTransition},
		{PurchaseReceived, PurchaseOpen, 1, ErrIllegalTransition},
		{PurchaseReceived, PurchaseOrdered, 1, ErrIllegalTransition},
		{PurchaseReceived, PurchaseReceived, 1, ErrIllegalTransition},
		{PurchaseOpen, PurchaseReceived, 0, ErrIllegalTransition}, // 409 vence o 422
	}
	for _, c := range cases {
		// Act
		got := CheckPurchaseTransition(c.from, c.to, c.lines)
		// Assert
		if !errors.Is(got, c.want) {
			t.Errorf("%s→%s (lines=%d) = %v, want %v", c.from, c.to, c.lines, got, c.want)
		}
	}
}

func TestStampTransition(t *testing.T) {
	today := time.Date(2026, 11, 18, 22, 30, 0, 0, time.FixedZone("BRT", -3*3600))
	earlier := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	o, r := StampTransition(PurchaseOrdered, nil, nil, today)
	if o == nil || o.Format("2006-01-02") != "2026-11-18" || r != nil {
		t.Errorf("ORDERED: ordered_at=%v received_at=%v", o, r)
	}
	o, r = StampTransition(PurchaseOpen, &earlier, nil, today)
	if o != nil || r != nil {
		t.Errorf("OPEN: ordered_at=%v received_at=%v, want nil/nil", o, r)
	}
	o, r = StampTransition(PurchaseReceived, &earlier, nil, today)
	if o == nil || !o.Equal(earlier) || r == nil || r.Format("2006-01-02") != "2026-11-18" {
		t.Errorf("RECEIVED: ordered_at=%v received_at=%v", o, r)
	}
}

func TestCheckDeliveryChange(t *testing.T) {
	cases := []struct {
		current, next string
		ordered       bool
		want          error
	}{
		{DeliveryToDeliver, DeliveryDelivered, false, nil},
		{DeliveryDelivered, DeliveryToDeliver, false, nil},
		{DeliveryToDeliver, DeliveryCanceled, false, nil},
		{DeliveryDelivered, DeliveryCanceled, false, nil},
		{DeliveryToDeliver, DeliveryCanceled, true, ErrItemsInOrderedPurchase},
		{DeliveryCanceled, DeliveryToDeliver, false, ErrSaleCanceled},
		{DeliveryCanceled, DeliveryCanceled, false, ErrSaleCanceled},
		{DeliveryToDeliver, DeliveryDelivered, true, nil}, // entregar não depende das Purchases
	}
	for _, c := range cases {
		if got := CheckDeliveryChange(c.current, c.next, c.ordered); !errors.Is(got, c.want) {
			t.Errorf("%s→%s ordered=%v = %v, want %v", c.current, c.next, c.ordered, got, c.want)
		}
	}
}

func TestSalePaymentStatus(t *testing.T) {
	cases := []struct {
		total, received float64
		want            string
	}{
		{1000, 0, SalePaymentPending},
		{1000, 400, SalePaymentPartial},
		{1000, 1000, SalePaymentPaid},
		{1000, 1200, SalePaymentPaid},
		{0, 0, SalePaymentPending},
	}
	for _, c := range cases {
		if got := SalePaymentStatus(c.total, c.received); got != c.want {
			t.Errorf("SalePaymentStatus(%v, %v) = %s, want %s", c.total, c.received, got, c.want)
		}
	}
}

func TestLockedFieldsChanged(t *testing.T) {
	base := ItemLockFields{Origin: OriginMadeToOrder, SupplierID: "s1", Quantity: 2, UnitCost: 100}
	if LockedFieldsChanged(base, base) {
		t.Error("mesmos valores não devem travar")
	}
	for name, mut := range map[string]func(*ItemLockFields){
		"origin":      func(f *ItemLockFields) { f.Origin = OriginFromStock },
		"supplier":    func(f *ItemLockFields) { f.SupplierID = "s2" },
		"no supplier": func(f *ItemLockFields) { f.SupplierID = "" },
		"quantity":    func(f *ItemLockFields) { f.Quantity = 3 },
		"cost":        func(f *ItemLockFields) { f.UnitCost = 90 },
	} {
		after := base
		mut(&after)
		if !LockedFieldsChanged(base, after) {
			t.Errorf("mudança de %s deveria travar", name)
		}
	}
}

func TestInsufficientStock(t *testing.T) {
	if InsufficientStock(0) || InsufficientStock(3) || !InsufficientStock(-0.5) {
		t.Error("InsufficientStock: só Balance < 0 avisa")
	}
}

func TestEnums(t *testing.T) {
	for set, values := range map[*map[string]bool][]string{
		&ItemOrigins:            {"MADE_TO_ORDER", "FROM_STOCK"},
		&PurchaseKinds:          {"MADE_TO_ORDER", "STOCK_REPLENISHMENT"},
		&PurchaseStatuses:       {"OPEN", "ORDERED", "RECEIVED"},
		&SupplierPaymentStatus:  {"PENDING", "PAID"},
		&SaleDeliveryStatuses:   {"TO_DELIVER", "DELIVERED", "CANCELED"},
		&SalePaymentStatuses:    {"PENDING", "PARTIAL", "PAID"},
		&StockAdjustmentReasons: {"INITIAL", "COUNT", "OTHER"},
	} {
		if len(*set) != len(values) {
			t.Errorf("enum %v tem %d valores, want %d", values, len(*set), len(values))
		}
		for _, v := range values {
			if !(*set)[v] {
				t.Errorf("enum sem %s", v)
			}
		}
	}
	if ItemOrigins["A_ENTREGAR"] || SaleDeliveryStatuses["ENTREGUE"] {
		t.Error("valores legados não podem ser aceitos")
	}
}

func TestCheckBatch(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ok := []BatchItem{{ID: a, Origin: OriginMadeToOrder}, {ID: b, Origin: OriginFromStock}}
	if err := CheckBatch(ok); err != nil {
		t.Errorf("lote válido: %v", err)
	}
	many := make([]BatchItem, MaxBatchItems+1)
	for i := range many {
		many[i] = BatchItem{ID: uuid.New(), Origin: OriginMadeToOrder}
	}
	if CheckBatch(many[:MaxBatchItems]) != nil {
		t.Error("50 Items deveria passar")
	}
	for name, items := range map[string][]BatchItem{
		"vazio":       nil,
		"51 itens":    many,
		"sem id":      {{Origin: OriginMadeToOrder}},
		"repetido":    {{ID: a, Origin: OriginMadeToOrder}, {ID: a, Origin: OriginFromStock}},
		"origem ruim": {{ID: a, Origin: "X"}},
	} {
		if CheckBatch(items) == nil {
			t.Errorf("%s deveria falhar", name)
		}
	}
}
