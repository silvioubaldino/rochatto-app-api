package closing

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/saleitem"
)

func sp(s string) *string       { return &s }
func ip(i int) *int             { return &i }
func up(u uuid.UUID) *uuid.UUID { return &u }

func quoteItems() []QuoteItem {
	return []QuoteItem{
		{ID: uuid.New(), Kind: saleitem.KindProduct, HasProduct: true},
		{ID: uuid.New(), Kind: saleitem.KindProduct, HasProduct: false},
		{ID: uuid.New(), Kind: saleitem.KindService},
	}
}

func wantField(t *testing.T, ferr *FieldError, status int, field string) {
	t.Helper()
	if ferr == nil {
		t.Fatalf("esperava erro %d %s, veio nil", status, field)
	}
	if ferr.Status != status || ferr.Field != field {
		t.Errorf("erro = %d %q (%s), want %d %q", ferr.Status, ferr.Field, ferr.Msg, status, field)
	}
}

func TestParse_Empty(t *testing.T) {
	for _, r := range []*Request{nil, {}} {
		p, ferr := Parse(r)
		if ferr != nil || p.HasPlan || p.Down != nil || p.DeliveryDue != nil || len(p.Costs) != 0 {
			t.Errorf("Parse(%v) = %+v %v", r, p, ferr)
		}
	}
}

func TestParse_Valid(t *testing.T) {
	p, ferr := Parse(&Request{
		DeliveryDueDate: sp("2026-12-01"),
		Costs:           []CostSpec{{Kind: "LABOR", Amount: 100}},
		ReceivablePlan:  &Plan{Installments: []Installment{{DueDate: sp("2026-12-10"), Amount: 50, PaymentMethod: sp("PIX")}}},
		DownPayment:     &DownPayment{Amount: 20, OccurredOn: sp("2026-11-22"), PaymentMethod: sp("CASH")},
	})
	if ferr != nil || p.DeliveryDue == nil || !p.HasPlan || len(p.Plan) != 1 || p.Down == nil || p.Down.PaymentMethod != "CASH" {
		t.Fatalf("Parse = %+v %v", p, ferr)
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []struct {
		name  string
		req   Request
		field string
	}{
		{"data de entrega", Request{DeliveryDueDate: sp("22/11/2026")}, "closing.delivery_due_date"},
		{"kind do custo", Request{Costs: []CostSpec{{Kind: "X", Amount: 1}}}, "closing.costs[0].kind"},
		{"valor do custo", Request{Costs: []CostSpec{{Kind: "LABOR", Amount: 1}, {Kind: "LABOR", Amount: -1}}}, "closing.costs[1].amount"},
		{"parcela sem data", Request{ReceivablePlan: &Plan{Installments: []Installment{{Amount: 1}}}}, "closing.receivable_plan.installments[0].due_date"},
		{"parcela zero", Request{ReceivablePlan: &Plan{Installments: []Installment{{DueDate: sp("2026-12-10"), Amount: 0}}}}, "closing.receivable_plan.installments[0].amount"},
		{"parcela método", Request{ReceivablePlan: &Plan{Installments: []Installment{{DueDate: sp("2026-12-10"), Amount: 1, PaymentMethod: sp("FIADO")}}}}, "closing.receivable_plan.installments[0].payment_method"},
		{"entrada sem método", Request{DownPayment: &DownPayment{Amount: 5, OccurredOn: sp("2026-11-22")}}, "closing.down_payment.payment_method"},
		{"entrada sem data", Request{DownPayment: &DownPayment{Amount: 5, PaymentMethod: sp("PIX")}}, "closing.down_payment.occurred_on"},
		{"entrada zero", Request{DownPayment: &DownPayment{OccurredOn: sp("2026-11-22"), PaymentMethod: sp("PIX")}}, "closing.down_payment.amount"},
	}
	for _, c := range cases {
		req := c.req
		_, ferr := Parse(&req)
		t.Run(c.name, func(t *testing.T) { wantField(t, ferr, http.StatusBadRequest, c.field) })
	}
}

func TestCheckTotals(t *testing.T) {
	p, _ := Parse(&Request{
		ReceivablePlan: &Plan{Installments: []Installment{{DueDate: sp("2026-12-10"), Amount: 60}, {DueDate: sp("2027-01-10"), Amount: 40.01}}},
		DownPayment:    &DownPayment{Amount: 30, OccurredOn: sp("2026-11-22"), PaymentMethod: sp("PIX")},
	})
	if ferr := CheckTotals(p, 100.01); ferr != nil {
		t.Errorf("plano = preço deveria passar: %v", ferr)
	}
	wantField(t, CheckTotals(p, 100), http.StatusConflict, "closing.receivable_plan.installments")
	p2, _ := Parse(&Request{DownPayment: &DownPayment{Amount: 101, OccurredOn: sp("2026-11-22"), PaymentMethod: sp("PIX")}})
	wantField(t, CheckTotals(p2, 100), http.StatusConflict, "closing.down_payment.amount")
	if ferr := CheckTotals(&Parsed{}, 0); ferr != nil {
		t.Errorf("sem plano nem entrada: %v", ferr)
	}
}

func TestResolveItems_Defaults(t *testing.T) {
	items := quoteItems()
	got, ferr := ResolveItems(nil, items, ByQuoteItemID)
	if ferr != nil || len(got) != 3 {
		t.Fatalf("ResolveItems = %v %v", got, ferr)
	}
	for i, it := range items {
		if d := got[it.ID]; d.Origin != "MADE_TO_ORDER" || d.SupplierID != nil || d.Index != i {
			t.Errorf("default[%d] = %+v", i, d)
		}
	}
}

func TestResolveItems_ByIDAndIndex(t *testing.T) {
	items := quoteItems()
	sup, payee := uuid.New(), uuid.New()
	got, ferr := ResolveItems([]ItemSpec{
		{QuoteItemID: &items[0].ID, Origin: sp("FROM_STOCK")},
		{QuoteItemID: &items[2].ID, LaborPayeeID: &payee},
		{QuoteItemID: &items[1].ID, SupplierID: &sup},
	}, items, ByQuoteItemID)
	if ferr != nil {
		t.Fatal(ferr)
	}
	if got[items[0].ID].Origin != "FROM_STOCK" || got[items[2].ID].LaborPayeeID == nil || got[items[1].ID].SupplierID == nil {
		t.Errorf("decisões = %+v", got)
	}
	byIdx, ferr := ResolveItems([]ItemSpec{{ItemIndex: ip(1), SupplierID: &sup}}, items, ByItemIndex)
	if ferr != nil || byIdx[items[1].ID].SupplierID == nil {
		t.Errorf("por índice = %+v %v", byIdx, ferr)
	}
}

func TestResolveItems_Errors(t *testing.T) {
	items := quoteItems()
	sup := uuid.New()
	other := uuid.New()
	cases := []struct {
		name  string
		specs []ItemSpec
		mode  Mode
		field string
	}{
		{"sem id", []ItemSpec{{}}, ByQuoteItemID, "closing.items[0].quote_item_id"},
		{"id de outro quote", []ItemSpec{{QuoteItemID: &other}}, ByQuoteItemID, "closing.items[0].quote_item_id"},
		{"sem índice", []ItemSpec{{QuoteItemID: &items[0].ID}}, ByItemIndex, "closing.items[0].item_index"},
		{"índice fora", []ItemSpec{{ItemIndex: ip(3)}}, ByItemIndex, "closing.items[0].item_index"},
		{"índice negativo", []ItemSpec{{ItemIndex: ip(-1)}}, ByItemIndex, "closing.items[0].item_index"},
		{"repetido", []ItemSpec{{QuoteItemID: &items[0].ID}, {QuoteItemID: &items[0].ID}}, ByQuoteItemID, "closing.items[1]"},
		{"origem inválida", []ItemSpec{{QuoteItemID: &items[0].ID, Origin: sp("X")}}, ByQuoteItemID, "closing.items[0].origin"},
		{"FROM_STOCK sem product", []ItemSpec{{QuoteItemID: &items[1].ID, Origin: sp("FROM_STOCK")}}, ByQuoteItemID, "closing.items[0].origin"},
		{"FROM_STOCK com fornecedor", []ItemSpec{{QuoteItemID: &items[0].ID, Origin: sp("FROM_STOCK"), SupplierID: &sup}}, ByQuoteItemID, "closing.items[0].supplier_id"},
		{"SERVICE com origem", []ItemSpec{{QuoteItemID: &items[2].ID, Origin: sp("MADE_TO_ORDER")}}, ByQuoteItemID, "closing.items[0].origin"},
		{"SERVICE com fornecedor", []ItemSpec{{QuoteItemID: &items[2].ID, SupplierID: &sup}}, ByQuoteItemID, "closing.items[0].supplier_id"},
		{"labor em PRODUCT", []ItemSpec{{QuoteItemID: &items[0].ID, LaborPayeeID: &sup}}, ByQuoteItemID, "closing.items[0].labor_payee_id"},
	}
	for _, c := range cases {
		_, ferr := ResolveItems(c.specs, items, c.mode)
		t.Run(c.name, func(t *testing.T) { wantField(t, ferr, http.StatusBadRequest, c.field) })
	}
	_, ferr := ResolveItems([]ItemSpec{{QuoteItemID: &items[0].ID}, {QuoteItemID: &items[2].ID, SupplierID: &sup}}, items, ByQuoteItemID)
	wantField(t, ferr, http.StatusBadRequest, "closing.items[1].supplier_id")
}

func TestSupplierRefs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	refs := SupplierRefs([]ItemSpec{{SupplierID: &a}, {}, {LaborPayeeID: &b}})
	if len(refs) != 2 || refs[0].Field != "closing.items[0].supplier_id" || refs[1].Field != "closing.items[2].labor_payee_id" {
		t.Errorf("refs = %+v", refs)
	}
}
