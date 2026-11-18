package tools

import (
	"context"
	"net/url"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// CatalogTools: Products e Suppliers (AYD-006 §4.1 — módulo Catalog).
func CatalogTools() []toolkit.Tool {
	return []toolkit.Tool{searchProducts, searchSuppliers}
}

type SearchProductsIn struct {
	Query      string `json:"query,omitempty" jsonschema:"trecho do nome do produto (sem diferenciar acentos nem maiúsculas)"`
	CategoryID UUID   `json:"category_id,omitempty" jsonschema:"id da categoria de produto"`
	toolkit.Page
}

var searchProducts = toolkit.Define(toolkit.Meta{
	Name:  "search_products",
	Title: "Buscar produtos",
	Description: "Busca Products (produtos/pedras) por trecho do nome, com filtro opcional de categoria. " +
		"Devolve unidade e os preços/custos de referência, que são só sugestões editáveis. Use para " +
		"obter o product_id e sugerir preço e custo ao montar um orçamento ou conferir estoque. " +
		"Lista paginada, ordenada por nome. Não cria nem altera produtos.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SearchProductsIn) (toolkit.PageOut[ProductSummary], error) {
	q := url.Values{}
	setIf(q, "category_id", string(in.CategoryID))
	rows, err := get[[]legacyProduct](ctx, gw, "/produtos", q)
	if err != nil {
		return toolkit.PageOut[ProductSummary]{}, err
	}
	all := make([]ProductSummary, 0, len(rows))
	for _, r := range rows {
		if nameMatches(in.Query, r.Nome) {
			all = append(all, r.toEN())
		}
	}
	return toolkit.Paginate(all, in.Page), nil
})

type SearchSuppliersIn struct {
	Query string               `json:"query,omitempty" jsonschema:"trecho do nome do fornecedor (sem diferenciar acentos nem maiúsculas)"`
	Kind  toolkit.SupplierKind `json:"kind,omitempty" jsonschema:"tipo: STONE (pedra), TRANSPORT (transporte), LABOR (mão de obra), TAX_AUTHORITY (imposto/órgão) ou OTHER"`
	toolkit.Page
}

var searchSuppliers = toolkit.Define(toolkit.Meta{
	Name:  "search_suppliers",
	Title: "Buscar fornecedores",
	Description: "Busca Suppliers (fornecedores e prestadores: pedra, transporte, mão de obra, imposto) por trecho " +
		"do nome, sem diferenciar acentos nem maiúsculas, com filtro opcional de tipo (kind). Use para obter " +
		"o supplier_id ao criar uma Purchase (compra), filtrar compras e itens a comprar ou achar o credor " +
		"de uma conta; se houver mais de um candidato, pergunte ao dono. Lista paginada, ordenada por nome. " +
		"Não cria nem altera fornecedores.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SearchSuppliersIn) (toolkit.PageOut[SupplierSummary], error) {
	q := url.Values{}
	setIf(q, "kind", string(in.Kind))
	rows, err := get[[]legacySupplier](ctx, gw, "/fornecedores", q)
	if err != nil {
		return toolkit.PageOut[SupplierSummary]{}, err
	}
	all := make([]SupplierSummary, 0, len(rows))
	for _, r := range rows {
		if nameMatches(in.Query, r.Nome) {
			all = append(all, r.toEN())
		}
	}
	return toolkit.Paginate(all, in.Page), nil
})
