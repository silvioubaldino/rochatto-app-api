// Package toolkit declara ferramentas do Assistant connector com schemas derivados dos tipos.
package toolkit

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

type (
	UUID string
	Date string

	Limit  int
	Offset int
	Months int

	Money     float64
	PosMoney  float64
	Qty       float64
	SignedQty float64

	SettableQuoteStatus    string
	SettableDeliveryStatus string
	StockAdjustmentReason  string

	LeadSource            string
	PartnerType           string
	QuoteStatus           string
	DeliveryStatus        string
	PaymentStatus         string
	ItemOrigin            string
	ItemKind              string
	ReceiptStatus         string
	PaymentMethod         string
	PurchaseKind          string
	PurchaseStatus        string
	SupplierPaymentStatus string
	AlertKind             string
	MovementKind          string

	SupplierKind        string
	ObligationDirection string
	ObligationStatus    string
	OriginType          string
	CounterpartyType    string
	InstallmentStatus   string
	MovementCategory    string
	ExpenseCategory     string
	MovementDirection   string

	PurchaseChargeKind string
	SaleChargeKind     string
	SaleCostKind       string
	AllocationMethod   string
	PurchaseLineType   string
)

var Enums = map[reflect.Type][]string{
	reflect.TypeFor[LeadSource]():            {"REFERRAL", "INSTAGRAM", "PARTNER", "OTHER"},
	reflect.TypeFor[PartnerType]():           {"ARCHITECT", "ENGINEER", "BUILDER", "OTHER"},
	reflect.TypeFor[QuoteStatus]():           {"OPEN", "WON", "LOST"},
	reflect.TypeFor[DeliveryStatus]():        {"TO_DELIVER", "DELIVERED", "CANCELED"},
	reflect.TypeFor[PaymentStatus]():         {"PENDING", "PARTIAL", "PAID"},
	reflect.TypeFor[ItemOrigin]():            {"MADE_TO_ORDER", "FROM_STOCK"},
	reflect.TypeFor[ItemKind]():              {"PRODUCT", "SERVICE"},
	reflect.TypeFor[ReceiptStatus]():         {"PENDING", "RECEIVED"},
	reflect.TypeFor[PaymentMethod]():         {"PIX", "CASH", "CARD", "BOLETO", "TRANSFER"},
	reflect.TypeFor[PurchaseKind]():          {"MADE_TO_ORDER", "STOCK_REPLENISHMENT"},
	reflect.TypeFor[PurchaseStatus]():        {"OPEN", "ORDERED", "RECEIVED"},
	reflect.TypeFor[SupplierPaymentStatus](): {"PENDING", "PAID"},
	reflect.TypeFor[AlertKind]():             {"OVERDUE", "DUE_SOON"},
	reflect.TypeFor[MovementKind]():          {"INFLOW", "OUTFLOW", "ADJUSTMENT"},

	reflect.TypeFor[SupplierKind]():        {"STONE", "TRANSPORT", "LABOR", "TAX_AUTHORITY", "OTHER"},
	reflect.TypeFor[ObligationDirection](): {"PAYABLE", "RECEIVABLE"},
	reflect.TypeFor[ObligationStatus]():    {"OPEN", "PARTIAL", "SETTLED"},
	reflect.TypeFor[OriginType]():          {"SALE", "PURCHASE"},
	reflect.TypeFor[CounterpartyType]():    {"SUPPLIER", "PARTNER", "CUSTOMER"},
	reflect.TypeFor[InstallmentStatus]():   {"OPEN", "PARTIAL", "COVERED"},
	reflect.TypeFor[MovementCategory](): {"SALE_RECEIPT", "OTHER_INCOME", "PURCHASE_PAYMENT", "FREIGHT", "LABOR", "COMMISSION",
		"VEHICLE", "TAXES", "FIXED_COSTS", "MARKETING", "BANK_FEES", "OTHER_EXPENSE"},
	reflect.TypeFor[ExpenseCategory]():   {"VEHICLE", "TAXES", "FIXED_COSTS", "MARKETING", "BANK_FEES", "LABOR", "OTHER_EXPENSE", "OTHER_INCOME"},
	reflect.TypeFor[MovementDirection](): {"IN", "OUT"},

	reflect.TypeFor[PurchaseChargeKind](): {"FREIGHT", "UNLOADING", "TAX", "OTHER"},
	reflect.TypeFor[SaleChargeKind]():     {"FREIGHT", "OTHER"},
	reflect.TypeFor[SaleCostKind]():       {"FREIGHT", "LABOR", "COMMISSION", "OTHER"},
	reflect.TypeFor[AllocationMethod]():   {"BY_QUANTITY", "BY_VALUE"},
	reflect.TypeFor[PurchaseLineType]():   {"STOCK_ITEM", "SALE_ITEM"},

	reflect.TypeFor[SettableQuoteStatus]():    {"OPEN", "LOST"},
	reflect.TypeFor[SettableDeliveryStatus](): {"TO_DELIVER", "DELIVERED"},
	reflect.TypeFor[StockAdjustmentReason]():  {"INITIAL", "COUNT", "OTHER"},
}

func ptr[T any](v T) *T { return &v }

func TypeSchemas() map[reflect.Type]*jsonschema.Schema {
	m := map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[UUID](): {Type: "string", Format: "uuid"},
		reflect.TypeFor[Date](): {Type: "string", Pattern: `^\d{4}-\d{2}-\d{2}$`, Description: "YYYY-MM-DD"},
		reflect.TypeFor[Limit](): {Type: "integer", Minimum: ptr(1.0), Maximum: ptr(100.0),
			Description: "itens por página (padrão 20, máximo 100)"},
		reflect.TypeFor[Months]():           {Type: "integer", Minimum: ptr(1.0), Maximum: ptr(60.0), Description: "quantidade de meses (1 a 60; padrão 12)"},
		reflect.TypeFor[Money]():            {Type: "number", Minimum: ptr(0.0), Description: "valor em BRL (≥ 0, até 2 casas)"},
		reflect.TypeFor[PosMoney]():         {Type: "number", ExclusiveMinimum: ptr(0.0), Description: "valor em BRL (> 0, até 2 casas)"},
		reflect.TypeFor[Qty]():              {Type: "number", ExclusiveMinimum: ptr(0.0), Description: "quantidade (> 0, até 3 casas)"},
		reflect.TypeFor[SignedQty]():        {Type: "number", Description: "quantidade com sinal (≠ 0, até 3 casas)"},
		reflect.TypeFor[Nullable[Date]]():   nullable(&jsonschema.Schema{Type: "string", Pattern: `^\d{4}-\d{2}-\d{2}$`, Description: "YYYY-MM-DD; null limpa"}),
		reflect.TypeFor[Nullable[string]](): nullable(&jsonschema.Schema{Type: "string", Description: "texto; null limpa"}),
		reflect.TypeFor[Nullable[UUID]]():   nullable(&jsonschema.Schema{Type: "string", Format: "uuid", Description: "id; null limpa"}),
		reflect.TypeFor[Offset]():           {Type: "integer", Minimum: ptr(0.0), Description: "quantos itens pular (padrão 0)"},
	}
	for t, vals := range Enums {
		enum := make([]any, len(vals))
		for i, v := range vals {
			enum[i] = v
		}
		m[t] = &jsonschema.Schema{Type: "string", Enum: enum}
	}
	return m
}

type Page struct {
	Limit  Limit  `json:"limit,omitempty"`
	Offset Offset `json:"offset,omitempty"`
}

func (p Page) Normalize() (limit, offset int) {
	limit, offset = int(p.Limit), int(p.Offset)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

type PageOut[T any] struct {
	Items   []T  `json:"items"`
	Total   int  `json:"total"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

func Paginate[T any](all []T, p Page) PageOut[T] {
	limit, offset := p.Normalize()
	total := len(all)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	items := make([]T, 0, end-offset)
	items = append(items, all[offset:end]...)
	return PageOut[T]{Items: items, Total: total, Offset: offset, HasMore: end < total}
}

func nullable(s *jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{Description: s.Description, AnyOf: []*jsonschema.Schema{s, {Type: "null"}}}
}

type Nullable[T any] struct {
	Present bool
	Null    bool
	Value   T
}

func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Present = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

type Dedupable struct {
	AllowDuplicate bool `json:"allow_duplicate,omitempty" jsonschema:"true força a criação mesmo que o mesmo lançamento tenha acabado de ser feito (padrão false: repetições em 120 s são ignoradas)"`
}
