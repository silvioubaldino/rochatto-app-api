// Package closing reúne as regras puras do fechamento da venda.
package closing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/finance"
	"github.com/silvioubaldino/sales-backend/internal/fulfillment"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"github.com/silvioubaldino/sales-backend/internal/salecomp"
	"github.com/silvioubaldino/sales-backend/internal/saleitem"
)

const WarningInsufficientStock = fulfillment.WarningInsufficientStock

type Request struct {
	DeliveryDueDate *string      `json:"delivery_due_date"`
	Items           []ItemSpec   `json:"items"`
	Costs           []CostSpec   `json:"costs"`
	ReceivablePlan  *Plan        `json:"receivable_plan"`
	DownPayment     *DownPayment `json:"down_payment"`
}

type ItemSpec struct {
	QuoteItemID  *uuid.UUID `json:"quote_item_id"`
	ItemIndex    *int       `json:"item_index"`
	Origin       *string    `json:"origin"`
	SupplierID   *uuid.UUID `json:"supplier_id"`
	LaborPayeeID *uuid.UUID `json:"labor_payee_id"`
}

type CostSpec struct {
	Kind        string          `json:"kind"`
	Amount      float64         `json:"amount"`
	PayeeID     json.RawMessage `json:"payee_id"`
	Description *string         `json:"description"`
}

type Plan struct {
	Installments []Installment `json:"installments"`
}

type Installment struct {
	DueDate       *string `json:"due_date"`
	Amount        float64 `json:"amount"`
	PaymentMethod *string `json:"payment_method"`
}

type DownPayment struct {
	Amount        float64 `json:"amount"`
	OccurredOn    *string `json:"occurred_on"`
	PaymentMethod *string `json:"payment_method"`
}

type FieldError struct {
	Status int
	Field  string
	Msg    string
}

func (e *FieldError) Error() string { return e.Msg }

func bad(field, format string, a ...any) *FieldError {
	return &FieldError{Status: http.StatusBadRequest, Field: field, Msg: fmt.Sprintf(format, a...)}
}

func conflict(field, format string, a ...any) *FieldError {
	return &FieldError{Status: http.StatusConflict, Field: field, Msg: fmt.Sprintf(format, a...)}
}

func Wrap(field string, status int, msg string) *FieldError {
	return &FieldError{Status: status, Field: field, Msg: msg}
}

type PlanItem struct {
	DueDate       time.Time
	Amount        float64
	PaymentMethod *string
}

type Down struct {
	Amount        float64
	OccurredOn    time.Time
	PaymentMethod string
}

type Parsed struct {
	DeliveryDue *time.Time
	Costs       []CostSpec
	Plan        []PlanItem
	HasPlan     bool
	Down        *Down
}

const dateLayout = "2006-01-02"

func parseDate(field string, s *string) (*time.Time, *FieldError) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, strings.TrimSpace(*s))
	if err != nil {
		return nil, bad(field, "%s inválida (formato: YYYY-MM-DD)", field[strings.LastIndex(field, ".")+1:])
	}
	return &t, nil
}

func Parse(r *Request) (*Parsed, *FieldError) {
	p := &Parsed{}
	if r == nil {
		return p, nil
	}
	var ferr *FieldError
	if p.DeliveryDue, ferr = parseDate("closing.delivery_due_date", r.DeliveryDueDate); ferr != nil {
		return nil, ferr
	}
	for i, c := range r.Costs {
		if ferr := checkCost(i, c); ferr != nil {
			return nil, ferr
		}
	}
	p.Costs = r.Costs
	if r.ReceivablePlan != nil {
		p.HasPlan = true
		if p.Plan, ferr = parsePlan(r.ReceivablePlan); ferr != nil {
			return nil, ferr
		}
	}
	if r.DownPayment != nil {
		if p.Down, ferr = parseDown(r.DownPayment); ferr != nil {
			return nil, ferr
		}
	}
	return p, nil
}

func checkCost(i int, c CostSpec) *FieldError {
	base := fmt.Sprintf("closing.costs[%d]", i)
	if !salecomp.CostKinds[c.Kind] {
		return bad(base+".kind", "kind inválido (FREIGHT | LABOR | COMMISSION | OTHER)")
	}
	if c.Amount < 0 || obligations.HasMoreThan2Decimals(c.Amount) {
		return bad(base+".amount", "campo 'amount' deve ser >= 0 com até 2 casas decimais")
	}
	if c.Amount > salecomp.MaxAmount {
		return bad(base+".amount", "campo 'amount' acima do limite")
	}
	if c.Description != nil && utf8.RuneCountInString(strings.TrimSpace(*c.Description)) > 500 {
		return bad(base+".description", "campo 'description' deve ter no máximo 500 caracteres")
	}
	return nil
}

func parsePlan(pl *Plan) ([]PlanItem, *FieldError) {
	out := make([]PlanItem, 0, len(pl.Installments))
	for i, in := range pl.Installments {
		base := fmt.Sprintf("closing.receivable_plan.installments[%d]", i)
		due, ferr := parseDate(base+".due_date", in.DueDate)
		if ferr != nil {
			return nil, ferr
		}
		if due == nil {
			return nil, bad(base+".due_date", "due_date é obrigatória (formato: YYYY-MM-DD)")
		}
		if obligations.Cents(in.Amount) <= 0 {
			return nil, bad(base+".amount", "amount deve ser maior que zero")
		}
		if obligations.HasMoreThan2Decimals(in.Amount) {
			return nil, bad(base+".amount", "amount aceita no máximo 2 casas decimais")
		}
		method, err := finance.NormalizeMethod(in.PaymentMethod)
		if err != nil {
			return nil, bad(base+".payment_method", "%s", err.Error())
		}
		out = append(out, PlanItem{DueDate: *due, Amount: in.Amount, PaymentMethod: method})
	}
	return out, nil
}

func parseDown(d *DownPayment) (*Down, *FieldError) {
	const base = "closing.down_payment"
	if obligations.Cents(d.Amount) <= 0 {
		return nil, bad(base+".amount", "amount deve ser maior que zero")
	}
	if obligations.HasMoreThan2Decimals(d.Amount) {
		return nil, bad(base+".amount", "amount aceita no máximo 2 casas decimais")
	}
	on, ferr := parseDate(base+".occurred_on", d.OccurredOn)
	if ferr != nil {
		return nil, ferr
	}
	if on == nil {
		return nil, bad(base+".occurred_on", "occurred_on é obrigatória (formato: YYYY-MM-DD)")
	}
	method, err := finance.NormalizeMethod(d.PaymentMethod)
	if err != nil {
		return nil, bad(base+".payment_method", "%s", err.Error())
	}
	if method == nil {
		return nil, bad(base+".payment_method", "payment_method é obrigatório na entrada")
	}
	return &Down{Amount: d.Amount, OccurredOn: *on, PaymentMethod: *method}, nil
}

func CheckTotals(p *Parsed, price float64) *FieldError {
	if p.HasPlan {
		var sum int64
		for _, in := range p.Plan {
			sum += obligations.Cents(in.Amount)
		}
		if sum > obligations.Cents(price) {
			return conflict("closing.receivable_plan.installments",
				"soma das parcelas (%.2f) maior que o preço ao cliente (%.2f)", obligations.FromCents(sum), price)
		}
	}
	if p.Down != nil && obligations.Cents(p.Down.Amount) > obligations.Cents(price) {
		return conflict("closing.down_payment.amount", "entrada (%.2f) maior que o preço ao cliente (%.2f)", p.Down.Amount, price)
	}
	return nil
}

type Mode int

const (
	ByQuoteItemID Mode = iota
	ByItemIndex
)

type QuoteItem struct {
	ID         uuid.UUID
	Kind       string
	HasProduct bool
}

type Decision struct {
	Index        int
	Origin       string
	SupplierID   *uuid.UUID
	LaborPayeeID *uuid.UUID
}

// Os existentes de fornecedor e credor são conferidos pelo chamador (precisam do banco).
func ResolveItems(specs []ItemSpec, items []QuoteItem, mode Mode) (map[uuid.UUID]Decision, *FieldError) {
	out := make(map[uuid.UUID]Decision, len(items))
	for i, it := range items {
		out[it.ID] = Decision{Index: i, Origin: fulfillment.OriginMadeToOrder}
	}
	seen := map[uuid.UUID]bool{}
	for i, s := range specs {
		base := fmt.Sprintf("closing.items[%d]", i)
		item, ferr := locate(base, s, items, mode)
		if ferr != nil {
			return nil, ferr
		}
		if seen[item.ID] {
			return nil, bad(base, "Item repetido em closing.items")
		}
		seen[item.ID] = true
		d, ferr := decide(base, s, item, out[item.ID])
		if ferr != nil {
			return nil, ferr
		}
		out[item.ID] = d
	}
	return out, nil
}

func locate(base string, s ItemSpec, items []QuoteItem, mode Mode) (QuoteItem, *FieldError) {
	if mode == ByItemIndex {
		if s.ItemIndex == nil {
			return QuoteItem{}, bad(base+".item_index", "item_index é obrigatório na venda rápida")
		}
		if *s.ItemIndex < 0 || *s.ItemIndex >= len(items) {
			return QuoteItem{}, bad(base+".item_index", "item_index fora de items[]")
		}
		return items[*s.ItemIndex], nil
	}
	if s.QuoteItemID == nil {
		return QuoteItem{}, bad(base+".quote_item_id", "quote_item_id é obrigatório na conversão")
	}
	for _, it := range items {
		if it.ID == *s.QuoteItemID {
			return it, nil
		}
	}
	return QuoteItem{}, bad(base+".quote_item_id", "quote_item_id não pertence a este Quote")
}

func decide(base string, s ItemSpec, item QuoteItem, d Decision) (Decision, *FieldError) {
	if item.Kind == saleitem.KindService {
		if s.Origin != nil {
			return d, bad(base+".origin", "Item SERVICE não tem origem")
		}
		if s.SupplierID != nil {
			return d, bad(base+".supplier_id", "Item SERVICE não tem fornecedor (use labor_payee_id)")
		}
		d.LaborPayeeID = s.LaborPayeeID
		return d, nil
	}
	if s.LaborPayeeID != nil {
		return d, bad(base+".labor_payee_id", "labor_payee_id só vale para Item SERVICE")
	}
	if s.Origin != nil {
		if !fulfillment.ItemOrigins[*s.Origin] {
			return d, bad(base+".origin", "origin inválido (MADE_TO_ORDER | FROM_STOCK)")
		}
		d.Origin = *s.Origin
	}
	if d.Origin == fulfillment.OriginFromStock {
		if !item.HasProduct {
			return d, bad(base+".origin", "Item FROM_STOCK exige Product")
		}
		if s.SupplierID != nil {
			return d, bad(base+".supplier_id", "fornecedor só vale para Item MADE_TO_ORDER")
		}
	}
	d.SupplierID = s.SupplierID
	return d, nil
}

func SupplierRefs(specs []ItemSpec) []Ref {
	var refs []Ref
	for i, s := range specs {
		base := fmt.Sprintf("closing.items[%d]", i)
		if s.SupplierID != nil {
			refs = append(refs, Ref{Field: base + ".supplier_id", ID: *s.SupplierID})
		}
		if s.LaborPayeeID != nil {
			refs = append(refs, Ref{Field: base + ".labor_payee_id", ID: *s.LaborPayeeID})
		}
	}
	return refs
}

type Ref struct {
	Field string
	ID    uuid.UUID
}
