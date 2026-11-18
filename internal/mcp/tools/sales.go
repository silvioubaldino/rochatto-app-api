package tools

import (
	"context"
	"net/url"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// SalesTools: Sales (AYD-006 §4.1 — módulo Sales).
func SalesTools() []toolkit.Tool {
	return []toolkit.Tool{listSales, getSale}
}

type ListSalesIn struct {
	CustomerID        UUID                     `json:"customer_id,omitempty" jsonschema:"id do cliente"`
	DeliveryStatus    []toolkit.DeliveryStatus `json:"delivery_status,omitempty" jsonschema:"status de entrega (um ou mais)"`
	PaymentStatus     []toolkit.PaymentStatus  `json:"payment_status,omitempty" jsonschema:"status de pagamento (um ou mais)"`
	From              Date                     `json:"from,omitempty" jsonschema:"data da venda a partir de (inclusive)"`
	To                Date                     `json:"to,omitempty" jsonschema:"data da venda até (inclusive)"`
	FreightPending    bool                     `json:"freight_pending,omitempty" jsonschema:"só vendas com frete a pagar"`
	CommissionPending bool                     `json:"commission_pending,omitempty" jsonschema:"só vendas com comissão a pagar"`
	toolkit.Page
}

var listSales = toolkit.Define(toolkit.Meta{
	Name:  "list_sales",
	Title: "Listar vendas",
	Description: "Lista Sales (vendas) com filtros opcionais de cliente, status de entrega (TO_DELIVER a " +
		"entregar, DELIVERED entregue, CANCELED cancelada), status de pagamento (PENDING, PARTIAL, " +
		"PAID), período da data da venda e pendências de frete ou comissão. Cada venda traz total, " +
		"recebido, a receber (receivable) e lucro (profit), calculados pela API. Use para achar o " +
		"sale_id antes de lançar recebimentos ou para ver o que falta receber ou entregar. Ordem: " +
		"data da venda, mais recente primeiro. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ListSalesIn) (toolkit.PageOut[SaleSummary], error) {
	q := url.Values{}
	setIf(q, "cliente_id", string(in.CustomerID))
	setIf(q, "delivery_status", joinEnum(in.DeliveryStatus))
	setIf(q, "payment_status", joinEnum(in.PaymentStatus))
	setIf(q, "data_inicio", string(in.From))
	setIf(q, "data_fim", string(in.To))
	if in.FreightPending {
		q.Set("freight_pending", "true")
	}
	if in.CommissionPending {
		q.Set("commission_pending", "true")
	}
	rows, err := get[[]legacySale](ctx, gw, "/vendas", q)
	if err != nil {
		return toolkit.PageOut[SaleSummary]{}, err
	}
	all := make([]SaleSummary, 0, len(rows))
	for _, r := range rows {
		all = append(all, r.toEN())
	}
	return toolkit.Paginate(all, in.Page), nil
})

type GetSaleIn struct {
	SaleID UUID `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales)"`
}

type GetSaleOut struct {
	Sale SaleDetail `json:"sale"`
}

var getSale = toolkit.Define(toolkit.Meta{
	Name:  "get_sale",
	Title: "Ver venda",
	Description: "Mostra uma Sale (venda) completa: cliente, status de entrega e de pagamento, totais, " +
		"frete, comissão, os itens (produto, origem MADE_TO_ORDER sob encomenda ou FROM_STOCK de " +
		"estoque, fornecedor, compra) e os Receipts (recebimentos) com forma de pagamento. Use para " +
		"conferir uma venda antes de lançar recebimento ou ajuste. Pré-requisito: obtenha o sale_id " +
		"com list_sales. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in GetSaleIn) (GetSaleOut, error) {
	d, err := get[legacySaleDetail](ctx, gw, "/vendas/"+pathID(string(in.SaleID)), nil)
	if err != nil {
		return GetSaleOut{}, err
	}
	return GetSaleOut{Sale: d.toEN()}, nil
})
