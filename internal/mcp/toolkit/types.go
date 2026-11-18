// Package toolkit é o núcleo para declarar ferramentas do Assistant connector (AYD-006
// §Arquitetura de extensão): Meta + tipos Go + handler, com schemas derivados dos tipos.
package toolkit

import (
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
}

func ptr[T any](v T) *T { return &v }

// TypeSchemas devolve os schemas dos tipos nomeados acima.
func TypeSchemas() map[reflect.Type]*jsonschema.Schema {
	m := map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[UUID](): {Type: "string", Format: "uuid"},
		reflect.TypeFor[Date](): {Type: "string", Pattern: `^\d{4}-\d{2}-\d{2}$`, Description: "YYYY-MM-DD"},
		reflect.TypeFor[Limit](): {Type: "integer", Minimum: ptr(1.0), Maximum: ptr(100.0),
			Description: "itens por página (padrão 20, máximo 100)"},
		reflect.TypeFor[Months](): {Type: "integer", Minimum: ptr(1.0), Maximum: ptr(60.0), Description: "quantidade de meses (1 a 60; padrão 12)"},
		reflect.TypeFor[Offset](): {Type: "integer", Minimum: ptr(0.0), Description: "quantos itens pular (padrão 0)"},
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
