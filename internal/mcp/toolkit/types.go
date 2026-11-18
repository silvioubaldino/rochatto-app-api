// Package toolkit é o núcleo para declarar ferramentas do Assistant connector (AYD-006
// §Arquitetura de extensão): Meta + tipos Go + handler, com schemas derivados dos tipos.
package toolkit

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tipos nomeados que carregam restrições de schema (registradas em TypeSchemas).
type (
	UUID string // string, format uuid
	Date string // "YYYY-MM-DD" (America/Sao_Paulo)

	// Limit e Offset: paginação (§3.4). Months: janela de get_monthly_sales.
	Limit  int
	Offset int
	Months int

	// Money (BRL, ≥ 0), PosMoney (> 0), Qty (> 0) e SignedQty (≠ 0 é imposto por NotZero).
	// O número de casas (2 e 3) é conferido pelos handlers das escritas (CheckMoney/CheckQty).
	Money     float64
	PosMoney  float64
	Qty       float64
	SignedQty float64

	// Enums de entrada das escritas (subconjuntos dos enums da API).
	SettableQuoteStatus    string // OPEN | LOST (WON só por convert)
	SettableDeliveryStatus string // TO_DELIVER | DELIVERED (CANCELED é cancel_sale)
	StockAdjustmentReason  string // INITIAL | COUNT | OTHER

	// Enums canônicos da API (maiúsculas).
	LeadSource            string
	PartnerType           string
	QuoteStatus           string
	DeliveryStatus        string
	PaymentStatus         string
	ItemOrigin            string
	ReceiptStatus         string
	PaymentMethod         string
	PurchaseKind          string
	PurchaseStatus        string
	SupplierPaymentStatus string
	AlertKind             string
	MovementKind          string

	// AYD-007 (catálogo 1.0.0): contas, parcelas e movimentações financeiras.
	SupplierKind        string // STONE | TRANSPORT | LABOR | TAX_AUTHORITY | OTHER
	ObligationDirection string // PAYABLE | RECEIVABLE
	ObligationStatus    string // OPEN | PARTIAL | SETTLED
	OriginType          string // SALE | PURCHASE
	CounterpartyType    string // SUPPLIER | PARTNER | CUSTOMER
	InstallmentStatus   string // OPEN | PARTIAL | COVERED
	MovementCategory    string // as 12 categorias de movimentação
	ExpenseCategory     string // categorias aceitas por record_expense (despesa ou receita avulsa)
	MovementDirection   string // IN (entrada) | OUT (saída)

	// AYD-008 (catálogo 1.0.0): composição da compra.
	PurchaseChargeKind string // FREIGHT | UNLOADING | TAX | OTHER
	AllocationMethod   string // BY_QUANTITY | BY_VALUE
	PurchaseLineType   string // STOCK_ITEM | SALE_ITEM
)

// Valores de enum (fonte para os schemas e para os adaptadores).
var Enums = map[reflect.Type][]string{
	reflect.TypeFor[LeadSource]():            {"REFERRAL", "INSTAGRAM", "PARTNER", "OTHER"},
	reflect.TypeFor[PartnerType]():           {"ARCHITECT", "ENGINEER", "BUILDER", "OTHER"},
	reflect.TypeFor[QuoteStatus]():           {"OPEN", "WON", "LOST"},
	reflect.TypeFor[DeliveryStatus]():        {"TO_DELIVER", "DELIVERED", "CANCELED"},
	reflect.TypeFor[PaymentStatus]():         {"PENDING", "PARTIAL", "PAID"},
	reflect.TypeFor[ItemOrigin]():            {"MADE_TO_ORDER", "FROM_STOCK"},
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
	reflect.TypeFor[AllocationMethod]():   {"BY_QUANTITY", "BY_VALUE"},
	reflect.TypeFor[PurchaseLineType]():   {"STOCK_ITEM", "SALE_ITEM"},

	reflect.TypeFor[SettableQuoteStatus]():    {"OPEN", "LOST"},
	reflect.TypeFor[SettableDeliveryStatus](): {"TO_DELIVER", "DELIVERED"},
	reflect.TypeFor[StockAdjustmentReason]():  {"INITIAL", "COUNT", "OTHER"},
}

func ptr[T any](v T) *T { return &v }

// TypeSchemas devolve os schemas dos tipos nomeados acima.
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

// Page são os parâmetros de paginação de toda ferramenta search_/list_ (§3.4). Incorpore
// em Input structs.
type Page struct {
	Limit  Limit  `json:"limit,omitempty"`
	Offset Offset `json:"offset,omitempty"`
}

// Normalize aplica os padrões (limit 20, máx. 100; offset ≥ 0).
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

// PageOut é a saída paginada de toda ferramenta search_/list_ (§3.4).
type PageOut[T any] struct {
	Items   []T  `json:"items"`
	Total   int  `json:"total"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

// Paginate fatia a lista completa (a rota REST ainda não pagina — transparente ao contrato).
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

// Nullable é um campo opcional de entrada em que null tem significado explícito (limpar o
// valor — §3.2): Present distingue "omitido" de enviado; Null, "enviado como null".
type Nullable[T any] struct {
	Present bool
	Null    bool
	Value   T
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Present = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

// Dedupable é incorporado nas entradas das ferramentas dedupe (§3.7). O pipeline lê o
// argumento dos argumentos brutos; o campo existe para entrar no schema.
type Dedupable struct {
	AllowDuplicate bool `json:"allow_duplicate,omitempty" jsonschema:"true força a criação mesmo que o mesmo lançamento tenha acabado de ser feito (padrão false: repetições em 120 s são ignoradas)"`
}
