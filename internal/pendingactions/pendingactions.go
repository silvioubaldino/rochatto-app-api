// Package pendingactions reúne as regras puras das pendências do dia.
package pendingactions

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Kind string

const (
	ReceivableDue         Kind = "RECEIVABLE_DUE"
	PayableDue            Kind = "PAYABLE_DUE"
	DeliveryDue           Kind = "DELIVERY_DUE"
	ItemsWithoutSupplier  Kind = "ITEMS_WITHOUT_SUPPLIER"
	ItemsToPurchase       Kind = "ITEMS_TO_PURCHASE"
	PurchaseToOrder       Kind = "PURCHASE_TO_ORDER"
	PurchaseToReceive     Kind = "PURCHASE_TO_RECEIVE"
	ReceivableWithoutPlan Kind = "RECEIVABLE_WITHOUT_PLAN"
	CostWithoutPayee      Kind = "COST_WITHOUT_PAYEE"
	QuoteStale            Kind = "QUOTE_STALE"
	NegativeStock         Kind = "NEGATIVE_STOCK"
)

var Order = []Kind{
	ReceivableDue, PayableDue, DeliveryDue, ItemsWithoutSupplier, ItemsToPurchase, PurchaseToOrder,
	PurchaseToReceive, ReceivableWithoutPlan, CostWithoutPayee, QuoteStale, NegativeStock,
}

const (
	EntitySale       = "SALE"
	EntityQuote      = "QUOTE"
	EntityPurchase   = "PURCHASE"
	EntityObligation = "OBLIGATION"
	EntityProduct    = "PRODUCT"
	EntitySupplier   = "SUPPLIER"
)

const (
	DefaultHorizon = 7
	MaxHorizon     = 60
	MaxItems       = 20
	StaleQuoteDays = 7
)

// ParseHorizon interpreta horizon_days (vazio = 7; 0..60).
func ParseHorizon(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultHorizon, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 || n > MaxHorizon {
		return 0, errors.New("horizon_days inválido (0 a 60)")
	}
	return n, nil
}

func InWindow(d *time.Time, today time.Time, horizon int, includeNoDate bool) bool {
	if d == nil {
		return includeNoDate
	}
	return !dateOnly(*d).After(dateOnly(today).AddDate(0, 0, horizon))
}

func IsOverdue(d *time.Time, today time.Time) bool {
	return d != nil && dateOnly(*d).Before(dateOnly(today))
}

func IsStale(quoteDate, today time.Time) bool {
	return dateOnly(quoteDate).Before(dateOnly(today).AddDate(0, 0, -StaleQuoteDays))
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

type Entity struct {
	Type string    `json:"type"`
	ID   uuid.UUID `json:"id"`
}

type Item struct {
	Entity   Entity   `json:"entity"`
	Number   *int     `json:"number"`
	Title    string   `json:"title"`
	Date     *string  `json:"date"`
	Overdue  bool     `json:"overdue"`
	Amount   *float64 `json:"amount"`
	Quantity *float64 `json:"quantity"`
}

type Group struct {
	Kind         Kind     `json:"kind"`
	Count        int      `json:"count"`
	OverdueCount int      `json:"overdue_count"`
	Amount       *float64 `json:"amount"`
	Items        []Item   `json:"items"`
}

type Response struct {
	Today        string  `json:"today"`
	HorizonDays  int     `json:"horizon_days"`
	TotalOverdue int     `json:"total_overdue"`
	Groups       []Group `json:"groups"`
}

// less: mais urgente primeiro — vencidos, depois data crescente (sem data por último), maior valor, e por fim título/id para ordem estável.
func less(a, b Item) bool {
	if a.Overdue != b.Overdue {
		return a.Overdue
	}
	if (a.Date == nil) != (b.Date == nil) {
		return b.Date == nil
	}
	if a.Date != nil && *a.Date != *b.Date {
		return *a.Date < *b.Date
	}
	if av, bv := amountOf(a), amountOf(b); av != bv {
		return av > bv
	}
	if a.Title != b.Title {
		return a.Title < b.Title
	}
	return a.Entity.ID.String() < b.Entity.ID.String()
}

func amountOf(i Item) float64 {
	if i.Amount == nil {
		return math.Inf(-1)
	}
	return *i.Amount
}

func BuildGroup(kind Kind, items []Item) *Group {
	if len(items) == 0 {
		return nil
	}
	sorted := append([]Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return less(sorted[i], sorted[j]) })
	g := &Group{Kind: kind, Count: len(sorted)}
	var cents int64
	hasAmount := false
	for _, it := range sorted {
		if it.Overdue {
			g.OverdueCount++
		}
		if it.Amount != nil {
			hasAmount = true
			cents += int64(math.Round(*it.Amount * 100))
		}
	}
	if hasAmount {
		v := float64(cents) / 100
		g.Amount = &v
	}
	if len(sorted) > MaxItems {
		sorted = sorted[:MaxItems]
	}
	g.Items = sorted
	return g
}

func Assemble(today time.Time, horizon int, groups map[Kind]*Group) Response {
	resp := Response{Today: today.Format("2006-01-02"), HorizonDays: horizon, Groups: []Group{}}
	for _, k := range Order {
		if g := groups[k]; g != nil && g.Count > 0 {
			resp.Groups = append(resp.Groups, *g)
			resp.TotalOverdue += g.OverdueCount
		}
	}
	return resp
}
