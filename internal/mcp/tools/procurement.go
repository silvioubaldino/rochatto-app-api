package tools

import (
	"context"
	"net/url"
	"slices"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// ProcurementTools: Purchases (AYD-006 §4.1 — módulo Procurement).
func ProcurementTools() []toolkit.Tool {
	return []toolkit.Tool{listPendingPurchaseItems, listPurchases, getPurchase}
}

// --- list_pending_purchase_items ---

type ListPendingPurchaseItemsIn struct {
	SupplierID UUID `json:"supplier_id,omitempty" jsonschema:"só os itens deste fornecedor"`
}

type PendingPurchaseItem struct {
	ID           UUID    `json:"id" jsonschema:"id do item de venda (sale_item_id)"`
	SaleID       UUID    `json:"sale_id"`
	CustomerName string  `json:"customer_name"`
	ProductID    *UUID   `json:"product_id,omitempty"`
	ProductName  string  `json:"product_name"`
	Quantity     float64 `json:"quantity"`
	UnitCost     float64 `json:"unit_cost"`
	SaleDate     Date    `json:"sale_date"`
}

type PendingPurchaseGroup struct {
	SupplierID   *UUID                 `json:"supplier_id,omitempty"`
	SupplierName *string               `json:"supplier_name,omitempty"`
	Items        []PendingPurchaseItem `json:"items"`
}

type ListPendingPurchaseItemsOut struct {
	Groups []PendingPurchaseGroup `json:"groups"`
}

var listPendingPurchaseItems = toolkit.Define(toolkit.Meta{
	Name:  "list_pending_purchase_items",
	Title: "Itens a comprar",
	Description: "Lista a fila de compra: itens de Sale (venda) MADE_TO_ORDER (sob encomenda) ainda não " +
		"incluídos em nenhuma Purchase (compra), agrupados por Supplier (fornecedor); itens sem " +
		"fornecedor definido vêm num grupo sem supplier_id. Use para saber o que falta comprar ou " +
		"para obter os sale_item_ids de uma compra. Opcionalmente filtra por fornecedor. Sem " +
		"paginação (fila operacional). Não cria compras.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ListPendingPurchaseItemsIn) (ListPendingPurchaseItemsOut, error) {
	type legacyItem struct {
		ID           UUID    `json:"id"`
		SaleID       UUID    `json:"sale_id"`
		CustomerName string  `json:"customer_name"`
		ProdutoID    *UUID   `json:"produto_id"`
		ProdutoNome  string  `json:"produto_nome"`
		Quantidade   float64 `json:"quantidade"`
		CustoUnit    float64 `json:"custo_unit"`
		SaleDate     Date    `json:"sale_date"`
	}
	type legacyGroup struct {
		SupplierID   *UUID        `json:"supplier_id"`
		SupplierName *string      `json:"supplier_name"`
		Items        []legacyItem `json:"items"`
	}
	groups, err := get[[]legacyGroup](ctx, gw, "/purchases/pending-sale-items", nil)
	if err != nil {
		return ListPendingPurchaseItemsOut{}, err
	}
	out := ListPendingPurchaseItemsOut{Groups: make([]PendingPurchaseGroup, 0, len(groups))}
	for _, g := range groups {
		if in.SupplierID != "" && (g.SupplierID == nil || *g.SupplierID != in.SupplierID) {
			continue
		}
		pg := PendingPurchaseGroup{SupplierID: optUUID(g.SupplierID), SupplierName: opt(g.SupplierName), Items: make([]PendingPurchaseItem, 0, len(g.Items))}
		for _, i := range g.Items {
			pg.Items = append(pg.Items, PendingPurchaseItem{
				ID: i.ID, SaleID: i.SaleID, CustomerName: i.CustomerName, ProductID: optUUID(i.ProdutoID),
				ProductName: i.ProdutoNome, Quantity: i.Quantidade, UnitCost: i.CustoUnit, SaleDate: i.SaleDate,
			})
		}
		out.Groups = append(out.Groups, pg)
	}
	return out, nil
})

// --- list_purchases ---

type ListPurchasesIn struct {
	Status        []toolkit.PurchaseStatus      `json:"status,omitempty" jsonschema:"status da compra (um ou mais)"`
	PaymentStatus toolkit.SupplierPaymentStatus `json:"payment_status,omitempty" jsonschema:"status do pagamento ao fornecedor"`
	Kind          toolkit.PurchaseKind          `json:"kind,omitempty" jsonschema:"tipo da compra"`
	SupplierID    UUID                          `json:"supplier_id,omitempty" jsonschema:"id do fornecedor"`
	From          Date                          `json:"from,omitempty" jsonschema:"criada a partir de (inclusive)"`
	To            Date                          `json:"to,omitempty" jsonschema:"criada até (inclusive)"`
	toolkit.Page
}

var listPurchases = toolkit.Define(toolkit.Meta{
	Name:  "list_purchases",
	Title: "Listar compras",
	Description: "Lista Purchases (compras ao fornecedor) com filtros opcionais de status (OPEN aberta, " +
		"ORDERED pedida, RECEIVED recebida), pagamento ao fornecedor (PENDING ou PAID), tipo " +
		"(MADE_TO_ORDER sob encomenda ou STOCK_REPLENISHMENT reposição de estoque), fornecedor e " +
		"período de criação. Traz custo total, valor a pagar (to_pay) e datas, sem as linhas (use " +
		"get_purchase). Ordem: mais recente primeiro. Não cria nem altera compras.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ListPurchasesIn) (toolkit.PageOut[PurchaseSummary], error) {
	q := url.Values{}
	if len(in.Status) == 1 { // a rota aceita um status; vários são filtrados aqui
		q.Set("status", string(in.Status[0]))
	}
	setIf(q, "payment_status", string(in.PaymentStatus))
	setIf(q, "kind", string(in.Kind))
	setIf(q, "supplier_id", string(in.SupplierID))
	setIf(q, "from", string(in.From))
	setIf(q, "to", string(in.To))
	rows, err := get[[]PurchaseSummary](ctx, gw, "/purchases", q)
	if err != nil {
		return toolkit.PageOut[PurchaseSummary]{}, err
	}
	all := make([]PurchaseSummary, 0, len(rows))
	for _, r := range rows {
		if len(in.Status) > 1 && !slices.Contains(in.Status, r.Status) {
			continue
		}
		all = append(all, r.normalized())
	}
	return toolkit.Paginate(all, in.Page), nil
})

func (p PurchaseSummary) normalized() PurchaseSummary {
	p.Notes = opt(p.Notes)
	return p
}

// --- get_purchase ---

type GetPurchaseIn struct {
	PurchaseID UUID `json:"purchase_id" jsonschema:"id da compra (obtenha com list_purchases)"`
}

type GetPurchaseOut struct {
	Purchase PurchaseDetail `json:"purchase"`
}

var getPurchase = toolkit.Define(toolkit.Meta{
	Name:  "get_purchase",
	Title: "Ver compra",
	Description: "Mostra uma Purchase (compra) completa: fornecedor, status, datas, pagamento ao fornecedor, " +
		"custo total, valor a pagar (to_pay), os itens de venda atendidos (sale_items, no caso de " +
		"compra sob encomenda) e as linhas de reposição (items, no caso de reposição de estoque). " +
		"Pré-requisito: obtenha o purchase_id com list_purchases. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in GetPurchaseIn) (GetPurchaseOut, error) {
	p, err := get[PurchaseDetail](ctx, gw, "/purchases/"+pathID(string(in.PurchaseID)), nil)
	if err != nil {
		return GetPurchaseOut{}, err
	}
	p.PurchaseSummary = p.PurchaseSummary.normalized()
	if p.SaleItems == nil {
		p.SaleItems = []PurchaseSaleItem{}
	}
	if p.Items == nil {
		p.Items = []PurchaseLine{}
	}
	return GetPurchaseOut{Purchase: p}, nil
})
