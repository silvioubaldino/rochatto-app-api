package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// CRMTools: Customers, Partners e Quotes, leitura e escrita (AYD-006 §4.1 — módulo Catalog/CRM e CRM).
func CRMTools() []toolkit.Tool {
	return []toolkit.Tool{
		searchCustomers, getCustomer, createCustomer, searchPartners, listQuotes, getQuote,
		createQuote, setQuoteStatus, convertQuoteToSale, createQuickSale,
	}
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

// --- create_customer (W · dd) ---

type CreateCustomerIn struct {
	Name       string             `json:"name" jsonschema:"nome do cliente (1 a 200 caracteres)"`
	Phone      string             `json:"phone,omitempty" jsonschema:"telefone"`
	Email      string             `json:"email,omitempty" jsonschema:"e-mail"`
	City       string             `json:"city,omitempty" jsonschema:"cidade da obra"`
	Address    string             `json:"address,omitempty" jsonschema:"endereço da obra"`
	Notes      string             `json:"notes,omitempty" jsonschema:"observações"`
	LeadSource toolkit.LeadSource `json:"lead_source,omitempty" jsonschema:"como o cliente chegou; com PARTNER, referrer_id é obrigatório"`
	ReferrerID UUID               `json:"referrer_id,omitempty" jsonschema:"id do Partner que indicou (só com lead_source PARTNER; obtenha com search_partners)"`
	toolkit.Dedupable
}

type CreateCustomerOut struct {
	Customer     CustomerSummary `json:"customer"`
	Warnings     []string        `json:"warnings"`
	Deduplicated bool            `json:"deduplicated,omitempty"`
}

var createCustomer = toolkit.Define(toolkit.Meta{
	Name:  "create_customer",
	Title: "Cadastrar cliente",
	Description: "Cadastra um novo Customer (cliente) com nome e, se houver, contato, cidade/endereço da obra, " +
		"observações e origem (lead_source: REFERRAL indicação, INSTAGRAM, PARTNER, OTHER). Use quando o dono " +
		"pedir para cadastrar um cliente que ainda não existe; antes, confira com search_customers para não " +
		"duplicar. Com lead_source PARTNER informe o referrer_id (obtido com search_partners). Devolve o " +
		"cliente criado. Não edita nem exclui clientes.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in CreateCustomerIn) (CreateCustomerOut, error) {
	body := map[string]any{
		"nome": in.Name, "telefone": optStr(in.Phone), "email": optStr(in.Email), "cidade": optStr(in.City),
		"endereco": optStr(in.Address), "observacoes": optStr(in.Notes), "lead_source": optStr(string(in.LeadSource)),
		"referrer_id": optUUIDStr(in.ReferrerID),
	}
	c, err := send[legacyCustomer](ctx, gw, "POST", "/clientes", body)
	if err != nil {
		return CreateCustomerOut{}, err
	}
	return CreateCustomerOut{Customer: c.toEN(), Warnings: noWarnings()}, nil
})

// --- create_quote (W · dd) e create_quick_sale (W · dd): mesma entrada (§4.3) ---

type QuoteItemIn struct {
	ProductID   UUID          `json:"product_id,omitempty" jsonschema:"id do produto do catálogo (obtenha com search_products)"`
	ProductName string        `json:"product_name" jsonschema:"nome do produto como aparece no orçamento"`
	Quantity    toolkit.Qty   `json:"quantity" jsonschema:"quantidade (na unidade do produto)"`
	UnitPrice   toolkit.Money `json:"unit_price" jsonschema:"preço de venda unitário em BRL (editável; o preço de referência é só sugestão)"`
	UnitCost    toolkit.Money `json:"unit_cost" jsonschema:"custo unitário estimado em BRL"`
}

type CreateQuoteIn struct {
	CustomerID UUID          `json:"customer_id" jsonschema:"id do cliente (obtenha com search_customers)"`
	ReferrerID UUID          `json:"referrer_id,omitempty" jsonschema:"id do Partner indicador (obtenha com search_partners)"`
	QuoteDate  Date          `json:"quote_date,omitempty" jsonschema:"data do orçamento (padrão: hoje em São Paulo)"`
	Notes      string        `json:"notes,omitempty" jsonschema:"observações"`
	Items      []QuoteItemIn `json:"items" jsonschema:"itens do orçamento (pelo menos 1)"`
	toolkit.Dedupable
}

func (in CreateQuoteIn) body() (map[string]any, error) {
	items := make([]map[string]any, 0, len(in.Items))
	for i, it := range in.Items {
		for field, v := range map[string]float64{"unit_price": float64(it.UnitPrice), "unit_cost": float64(it.UnitCost)} {
			if err := checkMoney(fmt.Sprintf("items[%d].%s", i, field), v); err != nil {
				return nil, err
			}
		}
		if err := checkQty(fmt.Sprintf("items[%d].quantity", i), float64(it.Quantity)); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"product_id": optUUIDStr(it.ProductID), "product_name": it.ProductName, "quantity": it.Quantity,
			"unit_price": it.UnitPrice, "unit_cost": it.UnitCost,
		})
	}
	return map[string]any{
		"customer_id": in.CustomerID, "referrer_id": optUUIDStr(in.ReferrerID), "quote_date": in.QuoteDate,
		"notes": optStr(in.Notes), "items": items,
	}, nil
}

type CreateQuoteOut struct {
	Quote        QuoteDetail `json:"quote"`
	Warnings     []string    `json:"warnings"`
	Deduplicated bool        `json:"deduplicated,omitempty"`
}

var createQuote = toolkit.Define(toolkit.Meta{
	Name:  "create_quote",
	Title: "Criar orçamento",
	Description: "Cria um Quote (orçamento) OPEN (aberto) para um Customer (cliente) com os itens informados: " +
		"produto, quantidade, preço unitário e custo unitário estimado. Use quando o dono passar os itens e " +
		"preços de uma proposta. Pré-requisitos: customer_id (search_customers) e, para cada item, preencha " +
		"product_name, unit_price e unit_cost a partir de search_products (preço e custo de referência são só " +
		"sugestões editáveis, RN-02). O orçamento não define fornecedor. Não gera Sale (venda): use " +
		"convert_quote_to_sale para isso.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in CreateQuoteIn) (CreateQuoteOut, error) {
	body, err := in.body()
	if err != nil {
		return CreateQuoteOut{}, err
	}
	q, err := send[QuoteDetail](ctx, gw, "POST", "/quotes", body)
	if err != nil {
		return CreateQuoteOut{}, err
	}
	return CreateQuoteOut{Quote: q.normalized(), Warnings: noWarnings()}, nil
}, toolkit.MinItems("items", 1))

func (q QuoteDetail) normalized() QuoteDetail {
	q.Notes, q.LostReason = opt(q.Notes), opt(q.LostReason)
	if q.Items == nil {
		q.Items = []QuoteItem{}
	}
	return q
}

// --- set_quote_status (W · I) ---

type SetQuoteStatusIn struct {
	QuoteID    UUID                        `json:"quote_id" jsonschema:"id do orçamento (obtenha com list_quotes)"`
	Status     toolkit.SettableQuoteStatus `json:"status" jsonschema:"novo status: LOST (perdido) ou OPEN (reabrir)"`
	LostReason string                      `json:"lost_reason,omitempty" jsonschema:"motivo da perda (só com LOST; opcional)"`
}

type SetQuoteStatusOut struct {
	QuoteID    UUID                `json:"quote_id"`
	Status     toolkit.QuoteStatus `json:"status"`
	LostReason *string             `json:"lost_reason,omitempty"`
	Warnings   []string            `json:"warnings"`
}

var setQuoteStatus = toolkit.Define(toolkit.Meta{
	Name:  "set_quote_status",
	Title: "Marcar orçamento perdido/aberto",
	Description: "Marca um Quote (orçamento) como LOST (perdido), com motivo opcional, ou o reabre como OPEN " +
		"(aberto). Use quando o cliente desistir ou o dono quiser reabrir uma proposta. Pré-requisito: " +
		"obtenha o quote_id com list_quotes. Orçamento já convertido em Sale (WON) não muda. Não converte em " +
		"venda (use convert_quote_to_sale) e não exclui orçamentos.",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SetQuoteStatusIn) (SetQuoteStatusOut, error) {
	r, err := send[struct {
		ID         UUID                `json:"id"`
		Status     toolkit.QuoteStatus `json:"status"`
		LostReason *string             `json:"lost_reason"`
	}](ctx, gw, "PATCH", "/quotes/"+pathID(string(in.QuoteID))+"/status",
		map[string]any{"status": in.Status, "lost_reason": optStr(in.LostReason)})
	if err != nil {
		return SetQuoteStatusOut{}, err
	}
	return SetQuoteStatusOut{QuoteID: r.ID, Status: r.Status, LostReason: opt(r.LostReason), Warnings: noWarnings()}, nil
})

// --- convert_quote_to_sale (D) e create_quick_sale (W · dd) ---

type ConvertQuoteToSaleIn struct {
	QuoteID UUID `json:"quote_id" jsonschema:"id do orçamento OPEN (obtenha com list_quotes)"`
}

type SaleCreatedOut struct {
	Sale         SaleDetail `json:"sale"`
	QuoteID      UUID       `json:"quote_id"`
	Warnings     []string   `json:"warnings"`
	Deduplicated bool       `json:"deduplicated,omitempty"`
}

type convertResult struct {
	SaleID  UUID `json:"sale_id"`
	QuoteID UUID `json:"quote_id"`
}

type ConvertQuoteToSaleOut struct {
	Sale     SaleDetail `json:"sale"`
	QuoteID  UUID       `json:"quote_id"`
	Warnings []string   `json:"warnings"`
}

// readCreatedSale lê a Sale criada para devolver o detalhe (a escrita já foi gravada).
func readCreatedSale(ctx context.Context, gw toolkit.Gateway, r convertResult) (SaleCreatedOut, error) {
	d, err := get[legacySaleDetail](ctx, gw, "/vendas/"+pathID(string(r.SaleID)), nil)
	if err != nil {
		return SaleCreatedOut{}, afterWrite(err)
	}
	return SaleCreatedOut{Sale: d.toEN(), QuoteID: r.QuoteID, Warnings: noWarnings()}, nil
}

var convertQuoteToSale = toolkit.Define(toolkit.Meta{
	Name:  "convert_quote_to_sale",
	Title: "Converter orçamento em venda",
	Description: "Converte um Quote (orçamento) OPEN (aberto) em Sale (venda): os itens e valores são copiados, o " +
		"orçamento passa a WON (ganho) e a venda nasce com entrega TO_DELIVER (a entregar). Use quando o " +
		"cliente aprovar a proposta. Pré-requisito: obtenha o quote_id com list_quotes e confirme com o dono. " +
		"Ação com efeitos em cascata e sem desfazer pelo chat. Origem dos itens e fornecedor são definidos " +
		"depois, na venda. Não registra recebimentos.",
	Destructive: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ConvertQuoteToSaleIn) (ConvertQuoteToSaleOut, error) {
	r, err := send[convertResult](ctx, gw, "POST", "/quotes/"+pathID(string(in.QuoteID))+"/convert", nil)
	if err != nil {
		return ConvertQuoteToSaleOut{}, err
	}
	o, err := readCreatedSale(ctx, gw, r)
	return ConvertQuoteToSaleOut{Sale: o.Sale, QuoteID: o.QuoteID, Warnings: o.Warnings}, err
})

var createQuickSale = toolkit.Define(toolkit.Meta{
	Name:  "create_quick_sale",
	Title: "Registrar venda rápida",
	Description: "Registra uma venda rápida (quick sale): cria o Quote (orçamento) e já o converte em Sale (venda) " +
		"num passo só, mantendo que toda venda nasce de um orçamento. Use quando o dono já fechou o negócio e " +
		"não há proposta aberta. Entrada igual à de create_quote (customer_id, itens com product_name, " +
		"unit_price e unit_cost). Origem dos itens e fornecedor são definidos depois, na venda. Não registra " +
		"recebimentos.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in CreateQuoteIn) (SaleCreatedOut, error) {
	body, err := in.body()
	if err != nil {
		return SaleCreatedOut{}, err
	}
	r, err := send[convertResult](ctx, gw, "POST", "/quotes/quick-sale", body)
	if err != nil {
		return SaleCreatedOut{}, err
	}
	return readCreatedSale(ctx, gw, r)
}, toolkit.MinItems("items", 1))
