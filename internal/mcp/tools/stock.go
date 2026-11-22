package tools

import (
	"context"
	"math"
	"net/url"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

func StockTools() []toolkit.Tool { return []toolkit.Tool{getStockBalance, createStockAdjustment} }

type GetStockBalanceIn struct {
	ProductID    UUID   `json:"product_id,omitempty" jsonschema:"id do produto: devolve o saldo dele com o extrato de movimentos"`
	Query        string `json:"query,omitempty" jsonschema:"trecho do nome do produto"`
	OnlyNegative bool   `json:"only_negative,omitempty" jsonschema:"só produtos com saldo negativo"`
	toolkit.Page
}

type GetStockBalanceOut struct {
	Items           *[]StockBalanceRow  `json:"items,omitempty"`
	Total           *int                `json:"total,omitempty"`
	Offset          *int                `json:"offset,omitempty"`
	HasMore         *bool               `json:"has_more,omitempty"`
	Balance         *StockBalanceDetail `json:"balance,omitempty"`
	TotalStockValue *float64            `json:"total_stock_value,omitempty"`
}

var getStockBalance = toolkit.Define(toolkit.Meta{
	Name:  "get_stock_balance",
	Title: "Ver saldo de estoque",
	Description: "Mostra o Balance (saldo) de Stock (estoque) por Product (produto): entradas, saídas, " +
		"ajustes e saldo atual, com o custo (de entrada, já com frete e demais custos adicionais da compra) " +
		"e a data da última entrada e o valor em estoque (stock_value = saldo positivo × custo de entrada; " +
		"na lista, total_stock_value soma todos os produtos do filtro). Sem product_id, lista os " +
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
	var totalValue float64
	for _, r := range rows {
		totalValue += r.StockValue
	}
	totalValue = math.Round(totalValue*100) / 100
	p := toolkit.Paginate(rows, in.Page)
	return GetStockBalanceOut{Items: &p.Items, Total: &p.Total, Offset: &p.Offset, HasMore: &p.HasMore, TotalStockValue: &totalValue}, nil
})

type CreateStockAdjustmentIn struct {
	ProductID  UUID                          `json:"product_id" jsonschema:"id do produto (obtenha com search_products)"`
	Quantity   toolkit.SignedQty             `json:"quantity" jsonschema:"quantidade do ajuste, com sinal (+ soma, - subtrai); não pode ser 0"`
	Reason     toolkit.StockAdjustmentReason `json:"reason" jsonschema:"motivo: INITIAL (carga inicial), COUNT (contagem) ou OTHER (outro)"`
	UnitCost   *toolkit.Money                `json:"unit_cost,omitempty" jsonschema:"custo unitário em BRL (opcional)"`
	AdjustedAt Date                          `json:"adjusted_at,omitempty" jsonschema:"data do ajuste (padrão: hoje em São Paulo)"`
	Notes      string                        `json:"notes,omitempty" jsonschema:"observações"`
	toolkit.Dedupable
}

type StockAdjustment struct {
	ID         UUID                          `json:"id"`
	ProductID  UUID                          `json:"product_id"`
	Quantity   float64                       `json:"quantity"`
	UnitCost   *float64                      `json:"unit_cost,omitempty"`
	Reason     toolkit.StockAdjustmentReason `json:"reason"`
	AdjustedAt Date                          `json:"adjusted_at"`
	Notes      *string                       `json:"notes,omitempty"`
}

type CreateStockAdjustmentOut struct {
	Adjustment   StockAdjustment `json:"adjustment"`
	Balance      StockBalanceRow `json:"balance"`
	Warnings     []string        `json:"warnings"`
	Deduplicated bool            `json:"deduplicated,omitempty"`
}

var createStockAdjustment = toolkit.Define(toolkit.Meta{
	Name:  "create_stock_adjustment",
	Title: "Ajustar estoque",
	Description: "Registra um Stock adjustment (ajuste de estoque) de um Product (produto): quantidade com sinal " +
		"(+ soma, - subtrai), motivo (INITIAL carga inicial, COUNT contagem, OTHER outro), custo unitário " +
		"opcional e data (padrão: hoje). Use para carga inicial ou para corrigir o saldo após uma contagem. " +
		"Pré-requisito: product_id de search_products. Devolve o ajuste e o novo Balance (saldo). Ajuste é " +
		"imutável: erro se corrige com outro ajuste. Não registra compras nem vendas.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in CreateStockAdjustmentIn) (CreateStockAdjustmentOut, error) {
	if err := checkQty("quantity", float64(in.Quantity)); err != nil {
		return CreateStockAdjustmentOut{}, err
	}
	if err := checkMoneyPtr("unit_cost", (*float64)(in.UnitCost)); err != nil {
		return CreateStockAdjustmentOut{}, err
	}
	a, err := send[StockAdjustment](ctx, gw, "POST", "/stock/adjustments", map[string]any{
		"product_id": in.ProductID, "quantity": in.Quantity, "unit_cost": in.UnitCost, "reason": in.Reason,
		"adjusted_at": optStr(string(in.AdjustedAt)), "notes": optStr(in.Notes),
	})
	if err != nil {
		return CreateStockAdjustmentOut{}, err
	}
	a.Notes = opt(a.Notes)
	b, err := get[StockBalanceRow](ctx, gw, "/stock/balance/"+pathID(string(a.ProductID)), nil)
	if err != nil {
		return CreateStockAdjustmentOut{}, afterWrite(err)
	}
	return CreateStockAdjustmentOut{Adjustment: a, Balance: b, Warnings: noWarnings()}, nil
}, toolkit.NotZero("quantity"))
