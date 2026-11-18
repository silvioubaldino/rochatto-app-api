package tools

import (
	"context"
	"net/url"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// SalesTools: Sales, leitura e escrita (AYD-006 §4.1 — módulo Sales).
func SalesTools() []toolkit.Tool {
	return []toolkit.Tool{listSales, getSale, updateSale, updateSaleItem, setSaleDeliveryStatus, cancelSale}
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
		"estoque, fornecedor, compra) e os recebimentos (receipts: quitações RECEIVED e parcelas " +
		"PENDING da conta a receber, com forma de pagamento). Use para conferir uma venda antes de " +
		"lançar recebimento ou ajuste. Pré-requisito: obtenha o sale_id com list_sales. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in GetSaleIn) (GetSaleOut, error) {
	d, err := get[legacySaleDetail](ctx, gw, "/vendas/"+pathID(string(in.SaleID)), nil)
	if err != nil {
		return GetSaleOut{}, err
	}
	out := d.toEN()
	acc, err := saleReceivable(ctx, gw, in.SaleID)
	if err != nil {
		return GetSaleOut{}, err
	}
	out.Receipts = receiptsOf(acc)
	return GetSaleOut{Sale: out}, nil
})

// --- update_sale (W · I): atualização parcial (GET + PUT completo) ---

type UpdateSaleIn struct {
	SaleID               UUID                           `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales)"`
	ExpectedDeliveryDate toolkit.Nullable[toolkit.Date] `json:"expected_delivery_date,omitzero" jsonschema:"prazo de entrega YYYY-MM-DD; null limpa"`
	FreightAmount        *toolkit.Money                 `json:"freight_amount,omitempty" jsonschema:"valor do frete (Freight) em BRL"`
	FreightPaid          *toolkit.Money                 `json:"freight_paid,omitempty" jsonschema:"quanto do frete já foi pago, em BRL (não pode passar do valor do frete); a diferença vira quitação da conta do frete"`
	CommissionAmount     *toolkit.Money                 `json:"commission_amount,omitempty" jsonschema:"valor da comissão (Commission) em BRL"`
	CommissionPaid       *bool                          `json:"commission_paid,omitempty" jsonschema:"true quita a comissão a pagar (vira quitação da conta)"`
	Notes                toolkit.Nullable[string]       `json:"notes,omitzero" jsonschema:"observações; null limpa"`
}

type UpdateSaleOut struct {
	Sale     SaleSummary `json:"sale"`
	Warnings []string    `json:"warnings"`
}

var updateSale = toolkit.Define(toolkit.Meta{
	Name:  "update_sale",
	Title: "Ajustar venda",
	Description: "Ajusta campos de uma Sale (venda): prazo de entrega, valor do frete (Freight) e quanto dele foi " +
		"pago, valor da comissão (Commission) e se foi paga, observações. Só os campos enviados mudam; os " +
		"demais permanecem. Use para lançar o frete ou marcar a comissão paga (viram quitações das contas a " +
		"pagar da venda; pagamento não é desfeito por aqui). Pré-requisito: sale_id de list_sales; envie ao " +
		"menos um campo. null limpa o prazo ou as observações. Não muda status de entrega, itens nem recebimentos.",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in UpdateSaleIn) (UpdateSaleOut, error) {
	if !in.ExpectedDeliveryDate.Present && in.FreightAmount == nil && in.FreightPaid == nil &&
		in.CommissionAmount == nil && in.CommissionPaid == nil && !in.Notes.Present {
		return UpdateSaleOut{}, invalid("informe ao menos um campo para ajustar além de sale_id")
	}
	for f, v := range map[string]*toolkit.Money{"freight_amount": in.FreightAmount, "freight_paid": in.FreightPaid, "commission_amount": in.CommissionAmount} {
		if v != nil {
			if err := checkMoney(f, float64(*v)); err != nil {
				return UpdateSaleOut{}, err
			}
		}
	}
	path := "/vendas/" + pathID(string(in.SaleID))
	cur, err := get[legacySaleDetail](ctx, gw, path, nil)
	if err != nil {
		return UpdateSaleOut{}, err
	}
	// Valida antes de gravar: o frete pago não passa do valor (novo) do frete.
	newFreight := cur.FreteValor
	if in.FreightAmount != nil {
		newFreight = float64(*in.FreightAmount)
	}
	if in.FreightPaid != nil && float64(*in.FreightPaid) > newFreight+0.004 {
		return UpdateSaleOut{}, invalid("frete_pago não pode ser maior que frete_valor")
	}
	body := toLegacySalePut(cur.legacySale, in)
	updated, err := send[legacySale](ctx, gw, "PUT", path, body)
	if err != nil {
		return UpdateSaleOut{}, err
	}
	if in.FreightPaid != nil || in.CommissionPaid != nil {
		if err := applyPaidFlags(ctx, gw, updated, in); err != nil {
			return UpdateSaleOut{}, err
		}
		if updated, err = get[legacySale](ctx, gw, path, nil); err != nil {
			return UpdateSaleOut{}, afterWrite(err)
		}
	}
	return UpdateSaleOut{Sale: updated.toEN(), Warnings: noWarnings()}, nil
})

// applyPaidFlags traduz freight_paid e commission_paid (AYD-007: frete e comissão pagos são
// quitações das contas a pagar da Sale). Só quita a diferença; desfazer pagamento não é possível
// por aqui (erro de entrada com orientação).
func applyPaidFlags(ctx context.Context, gw toolkit.Gateway, sale legacySale, in UpdateSaleIn) error {
	accounts, err := get[ObligationList](ctx, gw, "/obligations", url.Values{
		"origin_type": {"SALE"}, "origin_id": {string(sale.ID)}, "direction": {"PAYABLE"}, "limit": {"200"}})
	if err != nil {
		return err
	}
	settle := func(acc ObligationSummary, amount float64, category string) error {
		_, err := send[settleResult](ctx, gw, "POST", "/obligations/"+pathID(string(acc.ID))+"/settle", map[string]any{
			"amount": amount, "movement": map[string]any{"occurred_on": todaySP(), "category": category},
		})
		return err
	}
	if in.FreightPaid != nil {
		paid := max(sale.FreteValor-sale.FreightPayable, 0)
		delta := round2(float64(*in.FreightPaid) - paid)
		switch {
		case delta < 0:
			return invalid("não é possível reduzir o frete pago: desfaça a quitação no app web")
		case delta > 0:
			var acc *ObligationSummary
			for i, a := range accounts.Items {
				if a.Counterparty == nil {
					acc = &accounts.Items[i]
				}
			}
			if acc == nil {
				return invalid("a venda não tem frete a pagar")
			}
			if err := settle(*acc, delta, "FREIGHT"); err != nil {
				return err
			}
		}
	}
	if in.CommissionPaid != nil {
		paid := sale.ComissaoValor > 0 && sale.CommissionPayable <= 0
		switch {
		case *in.CommissionPaid && !paid && sale.CommissionPayable > 0:
			var acc *ObligationSummary
			for i, a := range accounts.Items { // a conta do Partner; sem Partner, a conta "a definir"
				if a.Counterparty != nil && a.Counterparty.Type == "PARTNER" && a.Open > 0 {
					acc = &accounts.Items[i]
				}
			}
			amount := sale.CommissionPayable
			if acc != nil {
				amount = acc.Open
			} else {
				for i, a := range accounts.Items {
					if a.Counterparty == nil {
						acc = &accounts.Items[i]
					}
				}
			}
			if acc == nil {
				return invalid("a venda não tem comissão a pagar")
			}
			return settle(*acc, amount, "COMMISSION")
		case !*in.CommissionPaid && paid:
			return invalid("não é possível desfazer o pagamento da comissão: use o app web")
		}
	}
	return nil
}

// toLegacySalePut aplica só os campos enviados sobre a representação atual da Sale e monta o
// corpo do PUT legado (substituição completa — AYD-006 §4.3). Função pura (testável sem banco).
func toLegacySalePut(cur legacySale, in UpdateSaleIn) map[string]any {
	body := map[string]any{
		"vendedor_externo_id":   cur.VendedorExternoID,
		"data_entrega_prevista": optDate(cur.DataEntregaPrevista),
		"frete_valor":           cur.FreteValor,
		"comissao_valor":        cur.ComissaoValor,
		"observacoes":           cur.Observacoes,
	}
	if d := in.ExpectedDeliveryDate; d.Present {
		if d.Null {
			body["data_entrega_prevista"] = nil
		} else {
			body["data_entrega_prevista"] = d.Value
		}
	}
	if in.FreightAmount != nil {
		body["frete_valor"] = float64(*in.FreightAmount)
	}
	if in.CommissionAmount != nil {
		body["comissao_valor"] = float64(*in.CommissionAmount)
	}
	if n := in.Notes; n.Present {
		if n.Null {
			body["observacoes"] = nil
		} else {
			body["observacoes"] = n.Value
		}
	}
	return body
}

// --- update_sale_item (W · I): atualização parcial (GET + PUT completo) ---

type UpdateSaleItemIn struct {
	SaleID     UUID                   `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales)"`
	ItemID     UUID                   `json:"item_id" jsonschema:"id do item da venda (obtenha com get_sale)"`
	Origin     toolkit.ItemOrigin     `json:"origin,omitempty" jsonschema:"origem do item: MADE_TO_ORDER (sob encomenda) ou FROM_STOCK (de estoque)"`
	SupplierID toolkit.Nullable[UUID] `json:"supplier_id,omitzero" jsonschema:"id do fornecedor (obtenha com search_suppliers); null limpa"`
	Quantity   *toolkit.Qty           `json:"quantity,omitempty" jsonschema:"nova quantidade"`
	UnitPrice  *toolkit.Money         `json:"unit_price,omitempty" jsonschema:"novo preço de venda unitário em BRL"`
	UnitCost   *toolkit.Money         `json:"unit_cost,omitempty" jsonschema:"novo custo unitário em BRL"`
}

type UpdateSaleItemOut struct {
	Item     SaleItem `json:"item"`
	Warnings []string `json:"warnings"`
}

var updateSaleItem = toolkit.Define(toolkit.Meta{
	Name:  "update_sale_item",
	Title: "Ajustar item da venda",
	Description: "Ajusta um item de uma Sale (venda): origem (MADE_TO_ORDER sob encomenda ou FROM_STOCK de " +
		"estoque), fornecedor (Supplier), quantidade, preço e custo unitários. Só os campos enviados mudam. " +
		"Use para definir a origem e o fornecedor depois de fechar a venda ou corrigir valores. Pré-requisitos: " +
		"sale_id e item_id (get_sale); envie ao menos um campo. Item já em compra pedida ou recebida não aceita " +
		"mudar origem, fornecedor, quantidade nem custo. Item FROM_STOCK sem saldo é permitido, com aviso " +
		"INSUFFICIENT_STOCK. Não cria nem remove itens.",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in UpdateSaleItemIn) (UpdateSaleItemOut, error) {
	if in.Origin == "" && !in.SupplierID.Present && in.Quantity == nil && in.UnitPrice == nil && in.UnitCost == nil {
		return UpdateSaleItemOut{}, invalid("informe ao menos um campo para ajustar além de sale_id e item_id")
	}
	if in.Quantity != nil {
		if err := checkQty("quantity", float64(*in.Quantity)); err != nil {
			return UpdateSaleItemOut{}, err
		}
	}
	for f, v := range map[string]*toolkit.Money{"unit_price": in.UnitPrice, "unit_cost": in.UnitCost} {
		if v != nil {
			if err := checkMoney(f, float64(*v)); err != nil {
				return UpdateSaleItemOut{}, err
			}
		}
	}
	cur, err := get[legacySaleDetail](ctx, gw, "/vendas/"+pathID(string(in.SaleID)), nil)
	if err != nil {
		return UpdateSaleItemOut{}, err
	}
	var item *legacySaleItem
	for i := range cur.Itens {
		if string(cur.Itens[i].ID) == string(in.ItemID) {
			item = &cur.Itens[i]
		}
	}
	if item == nil {
		return UpdateSaleItemOut{}, toolkit.NewToolError(toolkit.CodeNotFound, "item não encontrado")
	}
	body := toLegacyItemPut(*item, in)
	if in.SupplierID.Present && !in.SupplierID.Null {
		// O nome do fornecedor é texto no item: acompanha o novo id (falha aqui deixa a API validar o id).
		if sup, err := get[legacySupplier](ctx, gw, "/fornecedores/"+pathID(string(in.SupplierID.Value)), nil); err == nil {
			body["fornecedor_nome"] = sup.Nome
		}
	}
	r, err := send[struct {
		legacySaleItem
		Warnings []string `json:"warnings"`
	}](ctx, gw, "PUT", "/vendas/"+pathID(string(in.SaleID))+"/itens/"+pathID(string(in.ItemID)), body)
	if err != nil {
		return UpdateSaleItemOut{}, err
	}
	if r.Warnings == nil {
		r.Warnings = noWarnings()
	}
	return UpdateSaleItemOut{Item: r.toEN(), Warnings: r.Warnings}, nil
})

// toLegacyItemPut aplica os campos enviados sobre o Item atual e monta o PUT legado completo.
// custo_unit: enviado se informado; se a origem mudar e não vier custo, é omitido para a API
// aplicar a regra RN-16 (FROM_STOCK usa o custo da última entrada); senão mantém o atual.
func toLegacyItemPut(cur legacySaleItem, in UpdateSaleItemIn) map[string]any {
	body := map[string]any{
		"produto_id": cur.ProdutoID, "produto_nome": cur.ProdutoNome, "fornecedor_id": cur.FornecedorID,
		"fornecedor_nome": cur.FornecedorNome, "quantidade": cur.Quantidade, "preco_venda_unit": cur.PrecoVendaUnit,
		"custo_unit": cur.CustoUnit, "origin": cur.Origin,
	}
	if in.Origin != "" {
		body["origin"] = in.Origin
		if in.Origin != cur.Origin && in.UnitCost == nil {
			delete(body, "custo_unit")
		}
	}
	if s := in.SupplierID; s.Present {
		if s.Null {
			body["fornecedor_id"], body["fornecedor_nome"] = nil, nil
		} else {
			body["fornecedor_id"], body["fornecedor_nome"] = s.Value, nil
		}
	}
	if in.Quantity != nil {
		body["quantidade"] = float64(*in.Quantity)
	}
	if in.UnitPrice != nil {
		body["preco_venda_unit"] = float64(*in.UnitPrice)
	}
	if in.UnitCost != nil {
		body["custo_unit"] = float64(*in.UnitCost)
	}
	return body
}

// --- set_sale_delivery_status (W · I) e cancel_sale (D · I) ---

type SetSaleDeliveryStatusIn struct {
	SaleID         UUID                           `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales)"`
	DeliveryStatus toolkit.SettableDeliveryStatus `json:"delivery_status" jsonschema:"TO_DELIVER (a entregar) ou DELIVERED (entregue)"`
}

type SetSaleDeliveryStatusOut struct {
	SaleID         UUID                   `json:"sale_id"`
	DeliveryStatus toolkit.DeliveryStatus `json:"delivery_status"`
	Warnings       []string               `json:"warnings"`
}

type CancelSaleIn struct {
	SaleID UUID `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales)"`
}

func patchDeliveryStatus(ctx context.Context, gw toolkit.Gateway, saleID UUID, status string) (SetSaleDeliveryStatusOut, error) {
	r, err := send[struct {
		ID             UUID                   `json:"id"`
		DeliveryStatus toolkit.DeliveryStatus `json:"delivery_status"`
	}](ctx, gw, "PATCH", "/vendas/"+pathID(string(saleID))+"/delivery-status", map[string]any{"delivery_status": status})
	if err != nil {
		return SetSaleDeliveryStatusOut{}, err
	}
	return SetSaleDeliveryStatusOut{SaleID: r.ID, DeliveryStatus: r.DeliveryStatus, Warnings: noWarnings()}, nil
}

var setSaleDeliveryStatus = toolkit.Define(toolkit.Meta{
	Name:  "set_sale_delivery_status",
	Title: "Marcar entrega",
	Description: "Marca o status de entrega (Delivery status) de uma Sale (venda): TO_DELIVER (a entregar) ou " +
		"DELIVERED (entregue). Use quando o dono avisar que a entrega foi feita (ou para voltar a a entregar). " +
		"Pré-requisito: sale_id de list_sales. Venda cancelada não muda de status. Não cancela a venda (use " +
		"cancel_sale) nem mexe em recebimentos.",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SetSaleDeliveryStatusIn) (SetSaleDeliveryStatusOut, error) {
	return patchDeliveryStatus(ctx, gw, in.SaleID, string(in.DeliveryStatus))
})

type CancelSaleOut struct {
	SaleID         UUID     `json:"sale_id"`
	DeliveryStatus string   `json:"delivery_status"`
	Warnings       []string `json:"warnings"`
}

var cancelSale = toolkit.Define(toolkit.Meta{
	Name:  "cancel_sale",
	Title: "Cancelar venda",
	Description: "Cancela uma Sale (venda): ela passa a CANCELED (cancelada), sai dos totais e da fila de compra, " +
		"e itens em compra aberta são liberados. Ação terminal, sem desfazer pelo chat: confirme com o dono " +
		"antes. Pré-requisito: sale_id de list_sales. Só é possível se nenhum item estiver em compra pedida ou " +
		"recebida. Não exclui o registro nem estorna recebimentos já lançados.",
	Destructive: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in CancelSaleIn) (CancelSaleOut, error) {
	r, err := patchDeliveryStatus(ctx, gw, in.SaleID, "CANCELED")
	if err != nil {
		return CancelSaleOut{}, err
	}
	return CancelSaleOut{SaleID: r.SaleID, DeliveryStatus: "CANCELED", Warnings: r.Warnings}, nil
}, toolkit.OutConst("delivery_status", "CANCELED"))
