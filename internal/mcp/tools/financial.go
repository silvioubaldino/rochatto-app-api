package tools

import (
	"context"
	"net/url"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// FinancialTools: finanças (AYD-006 §4.1 — módulo Financial; os Receipts de escrita entram
// com a SPEC-007).
func FinancialTools() []toolkit.Tool {
	return []toolkit.Tool{getFinanceSummary, listPaymentAlerts}
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
