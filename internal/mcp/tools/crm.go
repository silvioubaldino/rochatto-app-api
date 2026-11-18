package tools

import (
	"context"
	"net/url"
	"strings"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// CRMTools: Customers, Partners e Quotes (AYD-006 §4.1 — módulo Catalog/CRM e CRM).
func CRMTools() []toolkit.Tool {
	return []toolkit.Tool{searchCustomers, getCustomer, searchPartners, listQuotes, getQuote}
}

func pathID(id string) string { return url.PathEscape(strings.TrimSpace(id)) }

// --- search_customers ---

type SearchCustomersIn struct {
	Query      string             `json:"query,omitempty" jsonschema:"trecho do nome do cliente (sem diferenciar acentos nem maiúsculas)"`
	LeadSource toolkit.LeadSource `json:"lead_source,omitempty" jsonschema:"origem do cliente"`
	ReferrerID UUID               `json:"referrer_id,omitempty" jsonschema:"id do Partner que indicou o cliente"`
	toolkit.Page
}

var searchCustomers = toolkit.Define(toolkit.Meta{
	Name:  "search_customers",
	Title: "Buscar clientes",
	Description: "Busca Customers (clientes) por trecho do nome, sem diferenciar acentos nem maiúsculas, " +
		"com filtros opcionais de origem (lead_source) e de Partner (parceiro) indicador (referrer_id). " +
		"Use para descobrir o customer_id antes de consultar ou lançar algo para um cliente; se houver " +
		"mais de um candidato, pergunte ao dono qual é. Devolve a lista paginada, ordenada por nome. " +
		"Não cria nem altera clientes.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SearchCustomersIn) (toolkit.PageOut[CustomerSummary], error) {
	q := url.Values{}
	setIf(q, "lead_source", string(in.LeadSource))
	setIf(q, "referrer_id", string(in.ReferrerID))
	rows, err := get[[]legacyCustomer](ctx, gw, "/clientes", q)
	if err != nil {
		return toolkit.PageOut[CustomerSummary]{}, err
	}
	all := make([]CustomerSummary, 0, len(rows))
	for _, r := range rows {
		if nameMatches(in.Query, r.Nome) {
			all = append(all, r.toEN())
		}
	}
	return toolkit.Paginate(all, in.Page), nil
})

// --- get_customer ---

type GetCustomerIn struct {
	CustomerID UUID `json:"customer_id" jsonschema:"id do cliente (obtenha com search_customers)"`
}

type GetCustomerOut struct {
	Customer    CustomerSummary `json:"customer"`
	RecentSales []SaleSummary   `json:"recent_sales"`
}

var getCustomer = toolkit.Define(toolkit.Meta{
	Name:  "get_customer",
	Title: "Ver cliente",
	Description: "Mostra os dados de um Customer (cliente) e as 10 Sales (vendas) mais recentes dele, " +
		"com totais, a receber (receivable) e status. Use quando o dono perguntar sobre um cliente " +
		"específico ou antes de lançar um recebimento para ele. Pré-requisito: obtenha o customer_id " +
		"com search_customers. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in GetCustomerIn) (GetCustomerOut, error) {
	c, err := get[legacyCustomer](ctx, gw, "/clientes/"+pathID(string(in.CustomerID)), nil)
	if err != nil {
		return GetCustomerOut{}, err
	}
	sales, err := get[[]legacySale](ctx, gw, "/vendas", url.Values{"cliente_id": {string(in.CustomerID)}})
	if err != nil {
		return GetCustomerOut{}, err
	}
	out := GetCustomerOut{Customer: c.toEN(), RecentSales: make([]SaleSummary, 0, 10)}
	for i, s := range sales {
		if i == 10 {
			break
		}
		out.RecentSales = append(out.RecentSales, s.toEN())
	}
	return out, nil
})

// --- search_partners ---

type SearchPartnersIn struct {
	Query string              `json:"query,omitempty" jsonschema:"trecho do nome do parceiro"`
	Type  toolkit.PartnerType `json:"type,omitempty" jsonschema:"tipo do parceiro"`
	toolkit.Page
}

var searchPartners = toolkit.Define(toolkit.Meta{
	Name:  "search_partners",
	Title: "Buscar parceiros",
	Description: "Busca Partners (parceiros/indicadores: arquiteto, engenheiro, construtora) por trecho do " +
		"nome, com filtro opcional de tipo. Use para obter o referrer_id ao criar um orçamento ou " +
		"filtrar clientes indicados por um parceiro. Devolve a lista paginada, ordenada por nome. " +
		"Não cria nem altera parceiros.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SearchPartnersIn) (toolkit.PageOut[PartnerSummary], error) {
	q := url.Values{}
	setIf(q, "type", string(in.Type))
	rows, err := get[[]PartnerSummary](ctx, gw, "/partners", q)
	if err != nil {
		return toolkit.PageOut[PartnerSummary]{}, err
	}
	all := make([]PartnerSummary, 0, len(rows))
	for _, r := range rows {
		if nameMatches(in.Query, r.Name) {
			r.Phone, r.Notes = opt(r.Phone), opt(r.Notes)
			all = append(all, r)
		}
	}
	return toolkit.Paginate(all, in.Page), nil
})

// --- list_quotes ---

type ListQuotesIn struct {
	Status     toolkit.QuoteStatus `json:"status,omitempty" jsonschema:"status do orçamento"`
	CustomerID UUID                `json:"customer_id,omitempty" jsonschema:"id do cliente"`
	Query      string              `json:"query,omitempty" jsonschema:"trecho do nome do cliente ou das observações"`
	From       Date                `json:"from,omitempty" jsonschema:"data do orçamento a partir de (inclusive)"`
	To         Date                `json:"to,omitempty" jsonschema:"data do orçamento até (inclusive)"`
	toolkit.Page
}

var listQuotes = toolkit.Define(toolkit.Meta{
	Name:  "list_quotes",
	Title: "Listar orçamentos",
	Description: "Lista Quotes (orçamentos) com filtros opcionais de status (OPEN aberto, WON ganho, LOST " +
		"perdido), cliente, texto e período da data do orçamento. Use para ver orçamentos em aberto " +
		"ou o histórico de um cliente. Devolve o resumo com total, sem os itens (use get_quote para " +
		"os itens), ordenado do mais recente ao mais antigo. Não cria nem altera orçamentos.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ListQuotesIn) (toolkit.PageOut[QuoteSummary], error) {
	q := url.Values{}
	setIf(q, "status", string(in.Status))
	setIf(q, "customer_id", string(in.CustomerID))
	setIf(q, "q", in.Query)
	setIf(q, "from", string(in.From))
	setIf(q, "to", string(in.To))
	rows, err := get[[]QuoteSummary](ctx, gw, "/quotes", q)
	if err != nil {
		return toolkit.PageOut[QuoteSummary]{}, err
	}
	for i := range rows {
		rows[i].Notes, rows[i].LostReason = opt(rows[i].Notes), opt(rows[i].LostReason)
	}
	return toolkit.Paginate(rows, in.Page), nil
})

// --- get_quote ---

type GetQuoteIn struct {
	QuoteID UUID `json:"quote_id" jsonschema:"id do orçamento (obtenha com list_quotes)"`
}

type GetQuoteOut struct {
	Quote QuoteDetail `json:"quote"`
}

var getQuote = toolkit.Define(toolkit.Meta{
	Name:  "get_quote",
	Title: "Ver orçamento",
	Description: "Mostra um Quote (orçamento) completo: cliente, status, motivo da perda, total e os itens " +
		"(produto, quantidade, preço e custo unitários). Use para conferir o conteúdo antes de " +
		"converter em Sale (venda) ou marcar como perdido. Pré-requisito: obtenha o quote_id com " +
		"list_quotes. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in GetQuoteIn) (GetQuoteOut, error) {
	q, err := get[QuoteDetail](ctx, gw, "/quotes/"+pathID(string(in.QuoteID)), nil)
	if err != nil {
		return GetQuoteOut{}, err
	}
	q.Notes, q.LostReason = opt(q.Notes), opt(q.LostReason)
	if q.Items == nil {
		q.Items = []QuoteItem{}
	}
	return GetQuoteOut{Quote: q}, nil
})
