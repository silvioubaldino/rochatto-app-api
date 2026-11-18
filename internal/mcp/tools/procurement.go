package tools

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// ProcurementTools: Purchases, leitura e escrita (AYD-006 §4.1 — módulo Procurement).
func ProcurementTools() []toolkit.Tool {
	return []toolkit.Tool{
		listPendingPurchaseItems, listPurchases, getPurchase,
		createPurchase, addSaleItemsToPurchase, setPurchaseStatus, addPurchaseCharge,
	}
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
		"período de criação. Traz custo total, pago, valor a pagar (to_pay) e datas, sem as linhas (use " +
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
	rows, err := get[[]restPurchaseSummary](ctx, gw, "/purchases", q)
	if err != nil {
		return toolkit.PageOut[PurchaseSummary]{}, err
	}
	all := make([]PurchaseSummary, 0, len(rows))
	for _, r := range rows {
		if len(in.Status) > 1 && !slices.Contains(in.Status, r.Status) {
			continue
		}
		all = append(all, r.toEN())
	}
	return toolkit.Paginate(all, in.Page), nil
})

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
	Description: "Mostra uma Purchase (compra) completa: fornecedor, status, datas, método de rateio " +
		"(allocation_method), a composição do custo (composition: pedras + custos adicionais como frete de " +
		"compra, descarga e imposto, cada um com seu credor), as contas por credor (obligations), o total " +
		"devido, pago e em aberto (payable, paid, open), as linhas com rateio e custo de entrada (lines: " +
		"allocated_charges, landed_total, landed_unit_cost), os itens de venda atendidos (sale_items, " +
		"compra sob encomenda) e as linhas de reposição (items). Pré-requisito: obtenha o purchase_id com " +
		"list_purchases. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in GetPurchaseIn) (GetPurchaseOut, error) {
	r, err := get[restPurchaseDetail](ctx, gw, "/purchases/"+pathID(string(in.PurchaseID)), nil)
	if err != nil {
		return GetPurchaseOut{}, err
	}
	p := r.toEN()
	return GetPurchaseOut{Purchase: p}, nil
})

// toEN traduz o PurchaseDetail do REST (sale_items com campos PT) para o contrato EN (§4.2/§5).
func (r restPurchaseDetail) toEN() PurchaseDetail {
	s := r.restPurchaseSummary
	p := PurchaseDetail{
		ID: s.ID, Kind: s.Kind, Status: s.Status, SupplierID: s.SupplierID, SupplierName: s.SupplierName,
		ExpectedDate: optDate(s.ExpectedDate), Notes: opt(s.Notes), OrderedAt: optDate(s.OrderedAt), ReceivedAt: optDate(s.ReceivedAt),
		AllocationMethod: r.AllocationMethod, TotalCost: s.TotalCost, LineCount: s.LineCount, CreatedAt: s.CreatedAt,
		Composition: r.Composition, Obligations: r.Obligations,
		Payable: s.Payable, Paid: s.Paid, Open: r.Open, Lines: r.Lines,
		SaleItems: make([]PurchaseSaleItem, 0, len(r.SaleItems)), Items: r.Items,
	}
	for _, i := range r.SaleItems {
		p.SaleItems = append(p.SaleItems, PurchaseSaleItem{
			ID: i.ID, SaleID: i.SaleID, CustomerName: i.CustomerName, ProductID: i.ProdutoID,
			ProductName: i.ProdutoNome, Quantity: i.Quantidade, UnitCost: i.CustoUnit,
		})
	}
	if p.Items == nil {
		p.Items = []PurchaseLine{}
	}
	if p.Lines == nil {
		p.Lines = []PurchaseCostLine{}
	}
	if p.Obligations == nil {
		p.Obligations = []ObligationSummary{}
	}
	if p.Composition.Charges == nil {
		p.Composition.Charges = []PurchaseCharge{}
	}
	return p
}

// --- create_purchase (W · dd) ---

type PurchaseLineIn struct {
	ProductID UUID          `json:"product_id" jsonschema:"id do produto (obtenha com search_products)"`
	Quantity  toolkit.Qty   `json:"quantity" jsonschema:"quantidade comprada"`
	UnitCost  toolkit.Money `json:"unit_cost" jsonschema:"custo unitário em BRL"`
}

type CreatePurchaseIn struct {
	Kind         toolkit.PurchaseKind `json:"kind" jsonschema:"MADE_TO_ORDER (atende itens de venda sob encomenda) ou STOCK_REPLENISHMENT (repor estoque)"`
	SupplierID   UUID                 `json:"supplier_id" jsonschema:"id do fornecedor (obtenha com search_suppliers)"`
	ExpectedDate Date                 `json:"expected_date,omitempty" jsonschema:"data prevista de entrega pelo fornecedor"`
	Notes        string               `json:"notes,omitempty" jsonschema:"observações"`
	SaleItemIDs  []UUID               `json:"sale_item_ids,omitempty" jsonschema:"só MADE_TO_ORDER: ids dos itens de venda a comprar (obtenha com list_pending_purchase_items)"`
	Items        []PurchaseLineIn     `json:"items,omitempty" jsonschema:"só STOCK_REPLENISHMENT: linhas de reposição"`
	toolkit.Dedupable
}

type PurchaseOut struct {
	Purchase PurchaseDetail `json:"purchase"`
	Warnings []string       `json:"warnings"`
}

type CreatePurchaseOut struct {
	Purchase     PurchaseDetail `json:"purchase"`
	Warnings     []string       `json:"warnings"`
	Deduplicated bool           `json:"deduplicated,omitempty"`
}

var createPurchase = toolkit.Define(toolkit.Meta{
	Name:  "create_purchase",
	Title: "Criar compra",
	Description: "Cria uma Purchase (compra) OPEN (aberta) a um Supplier (fornecedor). MADE_TO_ORDER (sob " +
		"encomenda) agrupa itens de venda pendentes (sale_item_ids, de list_pending_purchase_items) de um mesmo " +
		"fornecedor; STOCK_REPLENISHMENT (reposição) leva linhas items (produto, quantidade, custo) e, quando " +
		"recebida, gera entrada no estoque. Use quando o dono for comprar. Pré-requisito: supplier_id " +
		"(search_suppliers). Só um dos dois campos, conforme o tipo. Não registra pagamento ao fornecedor.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in CreatePurchaseIn) (CreatePurchaseOut, error) {
	lines := make([]map[string]any, 0, len(in.Items))
	for i, l := range in.Items {
		if err := checkQty(fmt.Sprintf("items[%d].quantity", i), float64(l.Quantity)); err != nil {
			return CreatePurchaseOut{}, err
		}
		if err := checkMoney(fmt.Sprintf("items[%d].unit_cost", i), float64(l.UnitCost)); err != nil {
			return CreatePurchaseOut{}, err
		}
		lines = append(lines, map[string]any{"product_id": l.ProductID, "quantity": l.Quantity, "unit_cost": l.UnitCost})
	}
	body := map[string]any{
		"kind": in.Kind, "supplier_id": in.SupplierID, "expected_date": optStr(string(in.ExpectedDate)), "notes": optStr(in.Notes),
	}
	if len(in.SaleItemIDs) > 0 {
		body["sale_item_ids"] = in.SaleItemIDs
	}
	if len(in.Items) > 0 {
		body["items"] = lines
	}
	r, err := send[restPurchaseDetail](ctx, gw, "POST", "/purchases", body)
	if err != nil {
		return CreatePurchaseOut{}, err
	}
	return CreatePurchaseOut{Purchase: r.toEN(), Warnings: noWarnings()}, nil
})

// --- add_sale_items_to_purchase (W) ---

type AddSaleItemsToPurchaseIn struct {
	PurchaseID  UUID   `json:"purchase_id" jsonschema:"id da compra MADE_TO_ORDER aberta (obtenha com list_purchases)"`
	SaleItemIDs []UUID `json:"sale_item_ids" jsonschema:"ids dos itens de venda a incluir (obtenha com list_pending_purchase_items); pelo menos 1"`
}

var addSaleItemsToPurchase = toolkit.Define(toolkit.Meta{
	Name:  "add_sale_items_to_purchase",
	Title: "Incluir itens na compra",
	Description: "Inclui itens de venda pendentes (sob encomenda) numa Purchase (compra) MADE_TO_ORDER ainda OPEN " +
		"(aberta). Use para acrescentar à compra de um fornecedor itens que entraram na fila depois. " +
		"Pré-requisitos: purchase_id (list_purchases) e sale_item_ids (list_pending_purchase_items), todos do " +
		"mesmo fornecedor da compra. Devolve a compra atualizada. Não remove itens nem altera compra pedida " +
		"ou recebida.",
}, func(ctx context.Context, gw toolkit.Gateway, in AddSaleItemsToPurchaseIn) (PurchaseOut, error) {
	r, err := send[restPurchaseDetail](ctx, gw, "POST", "/purchases/"+pathID(string(in.PurchaseID))+"/sale-items",
		map[string]any{"sale_item_ids": in.SaleItemIDs})
	if err != nil {
		return PurchaseOut{}, err
	}
	return PurchaseOut{Purchase: r.toEN(), Warnings: noWarnings()}, nil
}, toolkit.MinItems("sale_item_ids", 1))

// --- set_purchase_status (W · I) ---

type SetPurchaseStatusIn struct {
	PurchaseID UUID                   `json:"purchase_id" jsonschema:"id da compra (obtenha com list_purchases)"`
	Status     toolkit.PurchaseStatus `json:"status" jsonschema:"OPEN (aberta), ORDERED (pedida) ou RECEIVED (recebida)"`
}

type SetPurchaseStatusOut struct {
	PurchaseID UUID                   `json:"purchase_id"`
	Status     toolkit.PurchaseStatus `json:"status"`
	OrderedAt  *Date                  `json:"ordered_at,omitempty"`
	ReceivedAt *Date                  `json:"received_at,omitempty"`
	Warnings   []string               `json:"warnings"`
}

var setPurchaseStatus = toolkit.Define(toolkit.Meta{
	Name:  "set_purchase_status",
	Title: "Atualizar status da compra",
	Description: "Atualiza o status de uma Purchase (compra): OPEN (aberta), ORDERED (pedida ao fornecedor) ou " +
		"RECEIVED (recebida). Use quando o pedido for feito ao Supplier (fornecedor) ou a mercadoria chegar; " +
		"as datas de pedido e recebimento são gravadas. Compra de reposição recebida gera entrada no estoque. " +
		"Pré-requisito: purchase_id de list_purchases. As transições seguem as regras da API. Não registra o " +
		"pagamento ao fornecedor (use settle_obligations).",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SetPurchaseStatusIn) (SetPurchaseStatusOut, error) {
	r, err := send[struct {
		ID         UUID                   `json:"id"`
		Status     toolkit.PurchaseStatus `json:"status"`
		OrderedAt  *Date                  `json:"ordered_at"`
		ReceivedAt *Date                  `json:"received_at"`
	}](ctx, gw, "PATCH", "/purchases/"+pathID(string(in.PurchaseID))+"/status", map[string]any{"status": in.Status})
	if err != nil {
		return SetPurchaseStatusOut{}, err
	}
	return SetPurchaseStatusOut{PurchaseID: r.ID, Status: r.Status, OrderedAt: optDate(r.OrderedAt), ReceivedAt: optDate(r.ReceivedAt), Warnings: noWarnings()}, nil
})

type AddPurchaseChargeIn struct {
	PurchaseID      UUID                       `json:"purchase_id" jsonschema:"id da compra (obtenha com list_purchases); qualquer status"`
	Kind            toolkit.PurchaseChargeKind `json:"kind" jsonschema:"FREIGHT (frete de compra), UNLOADING (descarga), TAX (imposto, ex.: ICMS) ou OTHER (outros)"`
	Amount          toolkit.PosMoney           `json:"amount" jsonschema:"valor do custo adicional em BRL (> 0)"`
	Description     string                     `json:"description,omitempty" jsonschema:"descrição curta (ex.: ICMS MS)"`
	PayeeSupplierID UUID                       `json:"payee_supplier_id,omitempty" jsonschema:"credor: id do fornecedor ou prestador (search_suppliers); padrão: o fornecedor da compra"`
	toolkit.Dedupable
}

type AddPurchaseChargeOut struct {
	Charge       PurchaseCharge `json:"charge"`
	Purchase     PurchaseDetail `json:"purchase"`
	Warnings     []string       `json:"warnings"`
	Deduplicated bool           `json:"deduplicated,omitempty"`
}

var addPurchaseCharge = toolkit.Define(toolkit.Meta{
	Name:  "add_purchase_charge",
	Title: "Lançar custo adicional da compra",
	Description: "Lança um Purchase charge (custo adicional da compra) numa Purchase: frete de compra (FREIGHT), " +
		"descarga (UNLOADING), imposto (TAX) ou outros (OTHER), com valor e credor (por padrão, o fornecedor " +
		"da compra; informe payee_supplier_id para outro, ex.: o Jairo da descarga ou o órgão do imposto). " +
		"Pode ser lançado em qualquer status da compra (o frete costuma chegar depois do pedido). O custo " +
		"entra na conta a pagar do credor e é rateado entre as linhas (custo de entrada). Pré-requisitos: " +
		"purchase_id (list_purchases) e, se houver, payee_supplier_id (search_suppliers). Devolve o custo " +
		"lançado e a compra atualizada. Não paga nada (use settle_obligations) e não é o frete de entrega " +
		"ao cliente.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in AddPurchaseChargeIn) (AddPurchaseChargeOut, error) {
	if err := checkMoney("amount", float64(in.Amount)); err != nil {
		return AddPurchaseChargeOut{}, err
	}
	body := map[string]any{"kind": in.Kind, "amount": in.Amount, "description": optStr(in.Description)}
	if in.PayeeSupplierID != "" {
		body["payee_supplier_id"] = in.PayeeSupplierID
	}
	base := "/purchases/" + pathID(string(in.PurchaseID))
	r, err := send[struct {
		PurchaseCharge
		Warnings []string `json:"warnings"`
	}](ctx, gw, "POST", base+"/charges", body)
	if err != nil {
		return AddPurchaseChargeOut{}, err
	}
	pd, err := get[restPurchaseDetail](ctx, gw, base, nil)
	if err != nil {
		return AddPurchaseChargeOut{}, afterWrite(err)
	}
	warnings := r.Warnings
	if warnings == nil {
		warnings = noWarnings()
	}
	return AddPurchaseChargeOut{Charge: r.PurchaseCharge, Purchase: pd.toEN(), Warnings: warnings}, nil
})
