package tools

import (
	"context"
	"net/url"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// StockTools: Stock (AYD-006 §4.1 — módulo Stock).
func StockTools() []toolkit.Tool { return []toolkit.Tool{getStockBalance} }

type GetStockBalanceIn struct {
	ProductID    UUID   `json:"product_id,omitempty" jsonschema:"id do produto: devolve o saldo dele com o extrato de movimentos"`
	Query        string `json:"query,omitempty" jsonschema:"trecho do nome do produto"`
	OnlyNegative bool   `json:"only_negative,omitempty" jsonschema:"só produtos com saldo negativo"`
	toolkit.Page
}

// GetStockBalanceOut: lista paginada (sem product_id) OU um saldo com extrato (com product_id).
type GetStockBalanceOut struct {
	Items   *[]StockBalanceRow  `json:"items,omitempty"`
	Total   *int                `json:"total,omitempty"`
	Offset  *int                `json:"offset,omitempty"`
	HasMore *bool               `json:"has_more,omitempty"`
	Balance *StockBalanceDetail `json:"balance,omitempty"`
}

var getStockBalance = toolkit.Define(toolkit.Meta{
	Name:  "get_stock_balance",
	Title: "Ver saldo de estoque",
	Description: "Mostra o Balance (saldo) de Stock (estoque) por Product (produto): entradas, saídas, " +
		"ajustes e saldo atual, com o custo e a data da última entrada. Sem product_id, lista os " +
		"produtos (paginado), com filtros opcionais por nome e só saldos negativos. Com product_id, " +
		"devolve o saldo daquele produto e o extrato de movimentos (INFLOW entrada, OUTFLOW saída, " +
		"ADJUSTMENT ajuste). Itens sob encomenda não movimentam o estoque. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in GetStockBalanceIn) (GetStockBalanceOut, error) {
	if in.ProductID != "" {
		d, err := get[StockBalanceDetail](ctx, gw, "/stock/balance/"+pathID(string(in.ProductID)), nil)
		if err != nil {
			return GetStockBalanceOut{}, err
		}
		if d.Movements == nil {
			d.Movements = []StockMovement{}
		}
		return GetStockBalanceOut{Balance: &d}, nil
	}
	q := url.Values{}
	setIf(q, "q", in.Query)
	if in.OnlyNegative {
		q.Set("only_negative", "true")
	}
	rows, err := get[[]StockBalanceRow](ctx, gw, "/stock/balance", q)
	if err != nil {
		return GetStockBalanceOut{}, err
	}
	p := toolkit.Paginate(rows, in.Page)
	return GetStockBalanceOut{Items: &p.Items, Total: &p.Total, Offset: &p.Offset, HasMore: &p.HasMore}, nil
})
