package tools

import (
	"context"
	"net/url"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// FinancialTools: finanças e Receipts (AYD-006 §4.1 — módulo Financial).
func FinancialTools() []toolkit.Tool {
	return []toolkit.Tool{getFinanceSummary, listPaymentAlerts, recordReceipt, confirmReceipt}
}

// --- get_finance_summary (resposta de /finance/summary — AYD-004, já EN) ---

type Period struct {
	From Date `json:"from"`
	To   Date `json:"to"`
}

type FinanceSales struct {
	Count      int     `json:"count"`
	Revenue    float64 `json:"revenue"`
	Cost       float64 `json:"cost"`
	Freight    float64 `json:"freight"`
	Commission float64 `json:"commission"`
	Profit     float64 `json:"profit"`
	Received   float64 `json:"received"`
	Receivable float64 `json:"receivable"`
}

type ReceivedByMethod struct {
	PaymentMethod *toolkit.PaymentMethod `json:"payment_method,omitempty"` // ausente = legado "não informado"
	Amount        float64                `json:"amount"`
}

type FinanceCash struct {
	Received         float64            `json:"received"`
	ReceivedByMethod []ReceivedByMethod `json:"received_by_method"`
	PaidToSuppliers  float64            `json:"paid_to_suppliers"`
}

type FinanceBalances struct {
	Receivable         float64 `json:"receivable"`
	PayableToSuppliers float64 `json:"payable_to_suppliers"`
	FreightPayable     float64 `json:"freight_payable"`
	CommissionPayable  float64 `json:"commission_payable"`
}

type FinanceSummary struct {
	Period   Period          `json:"period"`
	Sales    FinanceSales    `json:"sales"`
	Cash     FinanceCash     `json:"cash"`
	Balances FinanceBalances `json:"balances"`
}

type PeriodIn struct {
	From Date `json:"from,omitempty" jsonschema:"início do período (padrão: dia 1 do mês corrente)"`
	To   Date `json:"to,omitempty" jsonschema:"fim do período, inclusive (padrão: hoje)"`
}

func (p PeriodIn) query() url.Values {
	q := url.Values{}
	setIf(q, "from", string(p.From))
	setIf(q, "to", string(p.To))
	return q
}

var getFinanceSummary = toolkit.Define(toolkit.Meta{
	Name:  "get_finance_summary",
	Title: "Resumo financeiro",
	Description: "Resumo financeiro entra × sai de um período: vendas (quantidade, faturamento, custo, frete, " +
		"comissão, Profit/lucro, recebido, a receber), movimento de caixa (recebido por forma de " +
		"pagamento e pago a fornecedores) e saldos em aberto de hoje (a receber, a pagar a " +
		"fornecedores, frete e comissão a pagar). Sem datas, usa o mês corrente até hoje (fuso de " +
		"São Paulo). Use para perguntas de fluxo de caixa e saldos. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in PeriodIn) (FinanceSummary, error) {
	s, err := get[FinanceSummary](ctx, gw, "/finance/summary", in.query())
	if err != nil {
		return FinanceSummary{}, err
	}
	if s.Cash.ReceivedByMethod == nil {
		s.Cash.ReceivedByMethod = []ReceivedByMethod{}
	}
	return s, nil
})

// --- list_payment_alerts ---

type ListPaymentAlertsIn struct {
	OnlyUnread *bool `json:"only_unread,omitempty" jsonschema:"só alertas não lidos (padrão true)"`
}

type PaymentAlert struct {
	ID            UUID              `json:"id"`
	Kind          toolkit.AlertKind `json:"kind"`
	SaleID        *UUID             `json:"sale_id,omitempty"`
	ReceiptID     *UUID             `json:"receipt_id,omitempty"`
	CustomerName  string            `json:"customer_name"`
	Amount        float64           `json:"amount"`
	DueDate       Date              `json:"due_date"`
	DaysRemaining *int              `json:"days_remaining,omitempty"`
	Read          bool              `json:"read"`
}

type ListPaymentAlertsOut struct {
	Items []PaymentAlert `json:"items"`
}

var listPaymentAlerts = toolkit.Define(toolkit.Meta{
	Name:  "list_payment_alerts",
	Title: "Alertas de pagamento",
	Description: "Lista os alertas de recebimento do Customer (cliente): OVERDUE (vencido) e DUE_SOON " +
		"(vencendo), com cliente, valor, data de vencimento e dias restantes. Por padrão só os não " +
		"lidos; passe only_unread false para incluir os já lidos. Use para cobranças e para ver o " +
		"que vence em breve. Não marca alertas como lidos nem altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ListPaymentAlertsIn) (ListPaymentAlertsOut, error) {
	q := url.Values{}
	if in.OnlyUnread != nil && !*in.OnlyUnread {
		q.Set("only_unread", "false")
	}
	type legacyAlert struct {
		ID            UUID    `json:"id"`
		Tipo          string  `json:"tipo"`
		VendaID       *UUID   `json:"venda_id"`
		PagamentoID   *UUID   `json:"pagamento_id"`
		ClienteNome   string  `json:"cliente_nome"`
		Valor         float64 `json:"valor"`
		DataRef       Date    `json:"data_ref"`
		DiasRestantes *int    `json:"dias_restantes"`
		Lida          bool    `json:"lida"`
	}
	resp, err := get[struct {
		Notificacoes []legacyAlert `json:"notificacoes"`
	}](ctx, gw, "/dashboard/notificacoes", q)
	if err != nil {
		return ListPaymentAlertsOut{}, err
	}
	out := ListPaymentAlertsOut{Items: make([]PaymentAlert, 0, len(resp.Notificacoes))}
	for _, n := range resp.Notificacoes {
		kind := toolkit.AlertKind("DUE_SOON")
		if n.Tipo == "pagamento_vencido" {
			kind = "OVERDUE"
		}
		out.Items = append(out.Items, PaymentAlert{
			ID: n.ID, Kind: kind, SaleID: optUUID(n.VendaID), ReceiptID: optUUID(n.PagamentoID),
			CustomerName: n.ClienteNome, Amount: n.Valor, DueDate: n.DataRef, DaysRemaining: n.DiasRestantes, Read: n.Lida,
		})
	}
	return out, nil
})

// --- record_receipt (W · dd) e confirm_receipt (W · I) ---

type RecordReceiptIn struct {
	SaleID        UUID                  `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales)"`
	Amount        toolkit.PosMoney      `json:"amount" jsonschema:"valor do recebimento em BRL (> 0)"`
	PaidAt        Date                  `json:"paid_at,omitempty" jsonschema:"data em que foi pago; com ela o recebimento nasce RECEIVED e payment_method é obrigatório"`
	ScheduledDate Date                  `json:"scheduled_date,omitempty" jsonschema:"data agendada de uma parcela futura (sem paid_at nasce PENDING)"`
	PaymentMethod toolkit.PaymentMethod `json:"payment_method,omitempty" jsonschema:"forma de pagamento: PIX, CASH, CARD, BOLETO ou TRANSFER"`
	Notes         string                `json:"notes,omitempty" jsonschema:"observações"`
	toolkit.Dedupable
}

type ReceiptOut struct {
	Receipt  Receipt     `json:"receipt"`
	Sale     SaleSummary `json:"sale"`
	Warnings []string    `json:"warnings"`
}

type RecordReceiptOut struct {
	Receipt      Receipt     `json:"receipt"`
	Sale         SaleSummary `json:"sale"`
	Warnings     []string    `json:"warnings"`
	Deduplicated bool        `json:"deduplicated,omitempty"`
}

// receiptWithSale relê a Sale após a escrita do Receipt para devolver o novo receivable.
func receiptWithSale(ctx context.Context, gw toolkit.Gateway, saleID UUID, r legacyReceipt) (ReceiptOut, error) {
	s, err := get[legacySale](ctx, gw, "/vendas/"+pathID(string(saleID)), nil)
	if err != nil {
		return ReceiptOut{}, afterWrite(err)
	}
	return ReceiptOut{Receipt: r.toEN(), Sale: s.toEN(), Warnings: noWarnings()}, nil
}

var recordReceipt = toolkit.Define(toolkit.Meta{
	Name:  "record_receipt",
	Title: "Registrar recebimento",
	Description: "Registra um Receipt (recebimento) de uma Sale (venda): com paid_at já foi pago (RECEIVED) e exige " +
		"payment_method; sem paid_at é uma parcela agendada (PENDING), opcionalmente com scheduled_date. Use " +
		"quando o dono disser que o cliente pagou ou combinou uma parcela. Pré-requisito: sale_id de " +
		"list_sales. Devolve o recebimento e a venda com o novo a receber (receivable). Venda cancelada não " +
		"aceita recebimentos. Não edita nem exclui recebimentos.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in RecordReceiptIn) (RecordReceiptOut, error) {
	if err := checkMoney("amount", float64(in.Amount)); err != nil {
		return RecordReceiptOut{}, err
	}
	r, err := send[legacyReceipt](ctx, gw, "POST", "/vendas/"+pathID(string(in.SaleID))+"/pagamentos", map[string]any{
		"valor": in.Amount, "data_pagamento": optStr(string(in.PaidAt)), "data_agendada": optStr(string(in.ScheduledDate)),
		"payment_method": optStr(string(in.PaymentMethod)), "observacoes": optStr(in.Notes),
	})
	if err != nil {
		return RecordReceiptOut{}, err
	}
	o, err := receiptWithSale(ctx, gw, in.SaleID, r)
	return RecordReceiptOut{Receipt: o.Receipt, Sale: o.Sale, Warnings: o.Warnings}, err
})

type ConfirmReceiptIn struct {
	SaleID        UUID                  `json:"sale_id" jsonschema:"id da venda (obtenha com list_sales)"`
	ReceiptID     UUID                  `json:"receipt_id" jsonschema:"id do recebimento PENDING (obtenha com get_sale)"`
	PaidAt        Date                  `json:"paid_at,omitempty" jsonschema:"data do pagamento (padrão: hoje em São Paulo)"`
	PaymentMethod toolkit.PaymentMethod `json:"payment_method,omitempty" jsonschema:"forma de pagamento; obrigatória se o recebimento ainda não tiver uma"`
}

var confirmReceipt = toolkit.Define(toolkit.Meta{
	Name:  "confirm_receipt",
	Title: "Confirmar recebimento",
	Description: "Confirma um Receipt (recebimento) PENDING (agendado) de uma Sale (venda) como RECEIVED " +
		"(recebido), com a data do pagamento (padrão: hoje) e a forma de pagamento, que é obrigatória se o " +
		"recebimento ainda não tiver uma. Use quando uma parcela agendada for paga. Pré-requisitos: sale_id e " +
		"receipt_id (get_sale). Devolve o recebimento e a venda com o novo a receber (receivable). Não cria " +
		"recebimentos novos (use record_receipt) nem desfaz confirmações.",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ConfirmReceiptIn) (ReceiptOut, error) {
	r, err := send[legacyReceipt](ctx, gw, "POST",
		"/vendas/"+pathID(string(in.SaleID))+"/pagamentos/"+pathID(string(in.ReceiptID))+"/confirmar",
		map[string]any{"data_pagamento": optStr(string(in.PaidAt)), "payment_method": optStr(string(in.PaymentMethod))})
	if err != nil {
		return ReceiptOut{}, err
	}
	return receiptWithSale(ctx, gw, in.SaleID, r)
})
