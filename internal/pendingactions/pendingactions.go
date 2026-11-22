// Package pendingactions reúne as regras puras das pendências do dia (Pending actions —
// AYD-011 C10): os tipos (kind) e a ordem de exibição, o horizonte, a janela de datas, a
// ordenação por urgência, o corte em 20 itens e as contagens/Σ do grupo. Sem banco: os handlers
// leem o estado com as mesmas consultas das listas e montam os grupos com estas regras.
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

// Kind é o tipo de pendência (PendingActionKind).
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

// Order é a ordem de exibição dos grupos (a do enum no AYD).
var Order = []Kind{
	ReceivableDue, PayableDue, DeliveryDue, ItemsWithoutSupplier, ItemsToPurchase, PurchaseToOrder,
	PurchaseToReceive, ReceivableWithoutPlan, CostWithoutPayee, QuoteStale, NegativeStock,
}

// Entidades apontadas pelos itens.
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
	MaxItems       = 20 // itens devolvidos por grupo
	StaleQuoteDays = 7  // QUOTE_STALE: quote_date < hoje − 7 dias
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

// InWindow: a data cai até hoje + horizonte? Sem data, vale includeNoDate (ex.: entrega sem prazo).
func InWindow(d *time.Time, today time.Time, horizon int, includeNoDate bool) bool {
	if d == nil {
		return includeNoDate
	}
	return !dateOnly(*d).After(dateOnly(today).AddDate(0, 0, horizon))
}

// IsOverdue: a data já passou (antes de hoje). Sem data não está vencido.
func IsOverdue(d *time.Time, today time.Time) bool {
	return d != nil && dateOnly(*d).Before(dateOnly(today))
}

// IsStale: Quote aberto parado — quote_date antes de hoje − 7 dias.
func IsStale(quoteDate, today time.Time) bool {
	return dateOnly(quoteDate).Before(dateOnly(today).AddDate(0, 0, -StaleQuoteDays))
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Entity aponta a entidade que resolve a pendência.
type Entity struct {
	Type string    `json:"type"`
	ID   uuid.UUID `json:"id"`
}

// Item é uma pendência. Date é "YYYY-MM-DD" (ordena como texto).
type Item struct {
	Entity   Entity   `json:"entity"`
	Number   *int     `json:"number"`
	Title    string   `json:"title"`
	Date     *string  `json:"date"`
	Overdue  bool     `json:"overdue"`
	Amount   *float64 `json:"amount"`
	Quantity *float64 `json:"quantity"`
}

// Group é o conjunto de pendências de um kind.
type Group struct {
	Kind         Kind     `json:"kind"`
	Count        int      `json:"count"`
	OverdueCount int      `json:"overdue_count"`
	Amount       *float64 `json:"amount"`
	Items        []Item   `json:"items"`
}

// Response é o corpo de GET /pending-actions.
type Response struct {
	Today        string  `json:"today"`
	HorizonDays  int     `json:"horizon_days"`
	TotalOverdue int     `json:"total_overdue"`
	Groups       []Group `json:"groups"`
}

// less: mais urgente primeiro — vencidos, depois data crescente (sem data por último), maior
// valor, e por fim título/id para ordem estável.
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

// BuildGroup ordena por urgência, conta (todos os itens) e corta os itens em MaxItems.
// Sem itens devolve nil (grupo omitido).
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

// Assemble monta a resposta com os grupos não vazios na ordem do enum; groups[kind] pode faltar.
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
