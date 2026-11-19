package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// SalesTools: Sales, leitura e escrita (AYD-006 §4.1 — módulo Sales).
func SalesTools() []toolkit.Tool {
	return []toolkit.Tool{listSales, getSale, updateSale, updateSaleItem, setSaleDeliveryStatus, cancelSale, setSaleCosts, addSaleCharge}
}

type ListSalesIn struct {
	CustomerID     UUID                     `json:"customer_id,omitempty" jsonschema:"id do cliente"`
	DeliveryStatus []toolkit.DeliveryStatus `json:"delivery_status,omitempty" jsonschema:"status de entrega (um ou mais)"`
	PaymentStatus  []toolkit.PaymentStatus  `json:"payment_status,omitempty" jsonschema:"status de pagamento (um ou mais)"`
	From           Date                     `json:"from,omitempty" jsonschema:"data da venda a partir de (inclusive)"`
	To             Date                     `json:"to,omitempty" jsonschema:"data da venda até (inclusive)"`
	toolkit.Page
}

var listSales = toolkit.Define(toolkit.Meta{
	Name:  "list_sales",
	Title: "Listar vendas",
	Description: "Lista Sales (vendas) com filtros opcionais de cliente, status de entrega (TO_DELIVER a " +
		"entregar, DELIVERED entregue, CANCELED cancelada), status de pagamento (PENDING, PARTIAL, " +
		"PAID), período da data da venda. Cada venda traz total (preço ao cliente), recebido, a receber " +
		"(receivable), custos da venda (previsto, pago, em aberto) e lucro (profit), calculados pela API. Use para achar o " +
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
		"a composição (itens + cobranças = preço ao cliente; custos da venda com credor e conta), as contas, os itens (produto, origem MADE_TO_ORDER sob encomenda ou FROM_STOCK de " +
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
	Notes                toolkit.Nullable[string]       `json:"notes,omitzero" jsonschema:"observações; null limpa"`
}

type UpdateSaleOut struct {
	Sale     SaleSummary `json:"sale"`
	Warnings []string    `json:"warnings"`
}

var updateSale = toolkit.Define(toolkit.Meta{
	Name:  "update_sale",
	Title: "Ajustar venda",
	Description: "Ajusta campos de uma Sale (venda): prazo de entrega e observações. Só os campos enviados mudam; os " +
		"demais permanecem. Pré-requisito: sale_id de list_sales; envie ao menos um campo. null limpa o prazo ou " +
		"as observações. Frete pago, instalador e comissão são custos da venda (use set_sale_costs); frete cobrado " +
		"do cliente é add_sale_charge. Não muda status de entrega, itens nem recebimentos.",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in UpdateSaleIn) (UpdateSaleOut, error) {
	if !in.ExpectedDeliveryDate.Present && !in.Notes.Present {
		return UpdateSaleOut{}, invalid("informe ao menos um campo para ajustar além de sale_id")
	}
	path := "/vendas/" + pathID(string(in.SaleID))
	cur, err := get[legacySaleDetail](ctx, gw, path, nil)
	if err != nil {
		return UpdateSaleOut{}, err
	}
	body := toLegacySalePut(cur.legacySale, in)
	updated, err := send[legacySale](ctx, gw, "PUT", path, body)
	if err != nil {
		return UpdateSaleOut{}, err
	}
	return UpdateSaleOut{Sale: updated.toEN(), Warnings: noWarnings()}, nil
})

// toLegacySalePut aplica só os campos enviados sobre a representação atual da Sale e monta o
// corpo do PUT legado (substituição completa — AYD-006 §4.3). Função pura (testável sem banco).
func toLegacySalePut(cur legacySale, in UpdateSaleIn) map[string]any {
	body := map[string]any{
		"vendedor_externo_id":   cur.VendedorExternoID,
		"data_entrega_prevista": optDate(cur.DataEntregaPrevista),
		"observacoes":           cur.Observacoes,
	}
	if d := in.ExpectedDeliveryDate; d.Present {
		if d.Null {
			body["data_entrega_prevista"] = nil
		} else {
			body["data_entrega_prevista"] = d.Value
		}
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

// --- set_sale_costs (W · I) e add_sale_charge (W · dd): composição da venda (AYD-009) ---

type SaleCostIn struct {
	Kind            toolkit.SaleCostKind `json:"kind" jsonschema:"FREIGHT (frete pago a motorista/transportadora), LABOR (instalador/mão de obra), COMMISSION (comissão) ou OTHER"`
	Amount          toolkit.Money        `json:"amount" jsonschema:"valor previsto do custo em BRL (>= 0); pode ser corrigido depois para o real"`
	Description     string               `json:"description,omitempty" jsonschema:"descrição curta; com kind e credor identifica o custo a atualizar"`
	PayeeSupplierID UUID                 `json:"payee_supplier_id,omitempty" jsonschema:"credor de FREIGHT/LABOR/OTHER: id do fornecedor ou prestador (search_suppliers); sem ele, fica a definir"`
	PayeePartnerID  UUID                 `json:"payee_partner_id,omitempty" jsonschema:"credor de COMMISSION: id do Partner (search_partners); padrão: o Partner indicador da venda"`
}

type SetSaleCostsIn struct {
	SaleID UUID         `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales); aceita venda cancelada"`
	Costs  []SaleCostIn `json:"costs" jsonschema:"custos a definir; cada um é atualizado se já existir um com o mesmo kind, credor e descrição, senão criado; os não citados permanecem"`
}

type SetSaleCostsOut struct {
	Costs    []SaleCost `json:"costs"`
	Sale     SaleDetail `json:"sale"`
	Warnings []string   `json:"warnings"`
}

var setSaleCosts = toolkit.Define(toolkit.Meta{
	Name:  "set_sale_costs",
	Title: "Definir custos da venda",
	Description: "Define os custos da venda (Sale costs) além das pedras: frete pago (FREIGHT), instalador/mão de obra " +
		"(LABOR), comissão (COMMISSION) e outros (OTHER), cada um com valor previsto e credor. Comissão tem " +
		"Partner como credor (padrão: o indicador da venda); os demais, fornecedor ou prestador (sem credor = " +
		"a definir). Custos do mesmo credor viram uma conta a pagar. Repetir a chamada atualiza o custo de mesmo " +
		"kind, credor e descrição em vez de duplicar; custos não citados permanecem e nada é excluído. " +
		"Pré-requisitos: sale_id (list_sales) e credores (search_suppliers/search_partners). Devolve os custos e " +
		"a venda com o lucro previsto. Não paga nada (use settle_obligations) e não é o frete cobrado do cliente " +
		"(add_sale_charge).",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SetSaleCostsIn) (SetSaleCostsOut, error) {
	if len(in.Costs) == 0 {
		return SetSaleCostsOut{}, invalid("informe ao menos um custo")
	}
	for i, c := range in.Costs {
		if err := checkMoney(fmt.Sprintf("costs[%d].amount", i), float64(c.Amount)); err != nil {
			return SetSaleCostsOut{}, err
		}
		if c.PayeeSupplierID != "" && c.PayeePartnerID != "" {
			return SetSaleCostsOut{}, invalid("costs[%d]: informe só um credor", i)
		}
		if c.Kind == "COMMISSION" && c.PayeeSupplierID != "" {
			return SetSaleCostsOut{}, invalid("costs[%d]: comissão tem Partner como credor (payee_partner_id)", i)
		}
		if c.Kind != "COMMISSION" && c.PayeePartnerID != "" {
			return SetSaleCostsOut{}, invalid("costs[%d]: só comissão aceita payee_partner_id", i)
		}
	}
	base := "/vendas/" + pathID(string(in.SaleID))
	existing, err := get[[]SaleCost](ctx, gw, base+"/costs", nil)
	if err != nil {
		return SetSaleCostsOut{}, err
	}
	var out []SaleCost
	warnings := noWarnings()
	for _, c := range in.Costs {
		body := map[string]any{"kind": c.Kind, "amount": c.Amount, "description": optStr(c.Description)}
		payee := string(c.PayeeSupplierID)
		if c.Kind == "COMMISSION" {
			payee = string(c.PayeePartnerID)
			if payee != "" {
				body["payee_partner_id"] = payee
			}
		} else if payee != "" {
			body["payee_supplier_id"] = payee
		}
		method, path := "POST", base+"/costs"
		for _, e := range existing {
			if string(e.Kind) != string(c.Kind) || ptrStr(e.Description) != c.Description {
				continue
			}
			ePayee := ""
			if e.Payee != nil {
				ePayee = string(e.Payee.ID)
			}
			if payee == "" && c.Kind == "COMMISSION" || ePayee == payee {
				method, path = "PUT", base+"/costs/"+pathID(string(e.ID))
				break
			}
		}
		r, err := send[struct {
			SaleCost
			Warnings []string `json:"warnings"`
		}](ctx, gw, method, path, body)
		if err != nil {
			return SetSaleCostsOut{}, err
		}
		out = append(out, r.SaleCost)
		warnings = append(warnings, r.Warnings...)
	}
	d, err := get[legacySaleDetail](ctx, gw, base, nil)
	if err != nil {
		return SetSaleCostsOut{}, afterWrite(err)
	}
	return SetSaleCostsOut{Costs: out, Sale: d.toEN(), Warnings: dedupStrings(warnings)}, nil
})

type AddSaleChargeIn struct {
	SaleID      UUID                   `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales); venda cancelada não aceita"`
	Kind        toolkit.SaleChargeKind `json:"kind" jsonschema:"FREIGHT (frete cobrado do cliente) ou OTHER (outra cobrança, ex.: descarga)"`
	Amount      toolkit.PosMoney       `json:"amount" jsonschema:"valor cobrado do cliente em BRL (> 0)"`
	Description string                 `json:"description,omitempty" jsonschema:"descrição curta"`
	toolkit.Dedupable
}

type AddSaleChargeOut struct {
	Charge       SaleCharge `json:"charge"`
	Sale         SaleDetail `json:"sale"`
	Warnings     []string   `json:"warnings"`
	Deduplicated bool       `json:"deduplicated,omitempty"`
}

var addSaleCharge = toolkit.Define(toolkit.Meta{
	Name:  "add_sale_charge",
	Title: "Lançar cobrança adicional na venda",
	Description: "Lança uma Sale charge (cobrança adicional ao cliente) numa venda existente: frete cobrado (FREIGHT) ou " +
		"outra cobrança (OTHER). Soma ao preço da venda e à conta a receber do cliente. Para uma venda nova, " +
		"prefira o campo charges de create_quote/create_quick_sale. Pré-requisito: sale_id (list_sales). " +
		"Devolve a cobrança e a venda atualizada. Não registra recebimento (use settle_obligations) e não é o " +
		"frete pago ao motorista (set_sale_costs).",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in AddSaleChargeIn) (AddSaleChargeOut, error) {
	if err := checkMoney("amount", float64(in.Amount)); err != nil {
		return AddSaleChargeOut{}, err
	}
	base := "/vendas/" + pathID(string(in.SaleID))
	r, err := send[struct {
		SaleCharge
		Warnings []string `json:"warnings"`
	}](ctx, gw, "POST", base+"/charges", map[string]any{"kind": in.Kind, "amount": in.Amount, "description": optStr(in.Description)})
	if err != nil {
		return AddSaleChargeOut{}, err
	}
	d, err := get[legacySaleDetail](ctx, gw, base, nil)
	if err != nil {
		return AddSaleChargeOut{}, afterWrite(err)
	}
	w := r.Warnings
	if w == nil {
		w = noWarnings()
	}
	return AddSaleChargeOut{Charge: r.SaleCharge, Sale: d.toEN(), Warnings: w}, nil
})

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := noWarnings()
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
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
