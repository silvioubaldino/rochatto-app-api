package tools

import (
	"context"
	"net/url"
	"strconv"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

// FinancialTools: finanças, contas, quitações e movimentações (AYD-006 §4.1 — módulo Financial;
// AYD-007 §8). record_receipt e confirm_receipt são DESCONTINUADAS: continuam funcionando,
// traduzidas para parcela/quitação, até a 2.0.0.
func FinancialTools() []toolkit.Tool {
	return []toolkit.Tool{
		getFinanceSummary, listPaymentAlerts,
		listObligations, listFinancialMovements,
		settleObligations, recordExpense, setInstallments,
		recordReceipt, confirmReceipt,
	}
}

// --- tipos EN das contas e movimentações (as rotas REST já são EN — AYD-007) ---

type ObligationOrigin struct {
	Type  toolkit.OriginType `json:"type"`
	ID    UUID               `json:"id"`
	Label string             `json:"label"`
}

type ObligationCounterparty struct {
	Type toolkit.CounterpartyType `json:"type"`
	ID   UUID                     `json:"id"`
	Name string                   `json:"name"`
}

type NextInstallment struct {
	DueDate    Date    `json:"due_date"`
	AmountOpen float64 `json:"amount_open"`
}

// ObligationSummary é a conta a pagar/receber de uma compra ou venda, por credor.
type ObligationSummary struct {
	ID              UUID                        `json:"id"`
	Direction       toolkit.ObligationDirection `json:"direction"`
	Origin          ObligationOrigin            `json:"origin"`
	Counterparty    *ObligationCounterparty     `json:"counterparty,omitempty"`
	Amount          float64                     `json:"amount"`
	Settled         float64                     `json:"settled"`
	Open            float64                     `json:"open"`
	Status          toolkit.ObligationStatus    `json:"status"`
	DueDate         *Date                       `json:"due_date,omitempty"`
	Overdue         bool                        `json:"overdue"`
	NextInstallment *NextInstallment            `json:"next_installment,omitempty"`
	Notes           *string                     `json:"notes,omitempty"`
	Warnings        []string                    `json:"warnings,omitempty"`
}

type ObligationComponent struct {
	Type   string  `json:"type"`
	ID     UUID    `json:"id"`
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
}

type Installment struct {
	ID            UUID                      `json:"id"`
	DueDate       Date                      `json:"due_date"`
	Amount        float64                   `json:"amount"`
	PaymentMethod *toolkit.PaymentMethod    `json:"payment_method,omitempty"`
	Notes         *string                   `json:"notes,omitempty"`
	Covered       float64                   `json:"covered"`
	Status        toolkit.InstallmentStatus `json:"status"`
	Overdue       bool                      `json:"overdue"`
}

type MovementSummary struct {
	ID               UUID                     `json:"id"`
	OccurredOn       Date                     `json:"occurred_on"`
	Amount           float64                  `json:"amount"`
	Category         toolkit.MovementCategory `json:"category"`
	PaymentMethod    *toolkit.PaymentMethod   `json:"payment_method,omitempty"`
	CounterpartyName *string                  `json:"counterparty_name,omitempty"`
	Description      *string                  `json:"description,omitempty"`
}

type ObligationSettlement struct {
	ID       UUID            `json:"id"`
	Amount   float64         `json:"amount"`
	Movement MovementSummary `json:"movement"`
}

// ObligationDetail é a conta com componentes, parcelas (plano) e quitações.
type ObligationDetail struct {
	ObligationSummary
	Components   []ObligationComponent  `json:"components"`
	Installments []Installment          `json:"installments"`
	Settlements  []ObligationSettlement `json:"settlements"`
}

type ObligationTotals struct {
	Amount  float64 `json:"amount"`
	Settled float64 `json:"settled"`
	Open    float64 `json:"open"`
}

type ObligationList struct {
	Items   []ObligationSummary `json:"items"`
	Total   int                 `json:"total"`
	Offset  int                 `json:"offset"`
	HasMore bool                `json:"has_more"`
	Totals  ObligationTotals    `json:"totals"`
}

type MovementSettlement struct {
	ID           UUID    `json:"id"`
	ObligationID UUID    `json:"obligation_id"`
	Amount       float64 `json:"amount"`
}

// FinancialMovement é o dinheiro executado: entrada (+) ou saída (−).
type FinancialMovement struct {
	MovementSummary
	Settlements []MovementSettlement `json:"settlements"`
	Unallocated float64              `json:"unallocated"`
}

type MovementTotals struct {
	Inflow  float64 `json:"inflow"`
	Outflow float64 `json:"outflow"`
	Net     float64 `json:"net"`
}

type MovementList struct {
	Items   []FinancialMovement `json:"items"`
	Total   int                 `json:"total"`
	Offset  int                 `json:"offset"`
	HasMore bool                `json:"has_more"`
	Totals  MovementTotals      `json:"totals"`
}

// settleResult é a resposta de POST /settlements e de POST /obligations/{id}/settle.
type settleResult struct {
	Movement    FinancialMovement   `json:"movement"`
	Obligations []ObligationSummary `json:"obligations"`
	Warnings    []string            `json:"warnings"`
}

// --- get_finance_summary (resposta de /finance/summary — AYD-004/AYD-007, já EN) ---

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
	PaymentMethod *toolkit.PaymentMethod `json:"payment_method,omitempty"` // ausente = "não informado"
	Amount        float64                `json:"amount"`
}

type CategoryAmount struct {
	Category toolkit.MovementCategory `json:"category"`
	Amount   float64                  `json:"amount"`
}

type FinanceCash struct {
	Inflow           float64            `json:"inflow"`
	Outflow          float64            `json:"outflow"`
	Net              float64            `json:"net"`
	ByCategory       []CategoryAmount   `json:"by_category"`
	ReceivedByMethod []ReceivedByMethod `json:"received_by_method"`
}

type FinanceExpenses struct {
	Total      float64          `json:"total"`
	ByCategory []CategoryAmount `json:"by_category"`
}

type FinanceResult struct {
	SalesProfit float64 `json:"sales_profit"`
	Expenses    float64 `json:"expenses"`
	NetResult   float64 `json:"net_result"`
}

type PayableByKind struct {
	Kind string  `json:"kind"`
	Open float64 `json:"open"`
}

type FinanceBalances struct {
	Receivable                float64         `json:"receivable"`
	Payable                   float64         `json:"payable"`
	PayableByCounterpartyKind []PayableByKind `json:"payable_by_counterparty_kind"`
}

type FinanceSummary struct {
	Period   Period          `json:"period"`
	Sales    FinanceSales    `json:"sales"`
	Cash     FinanceCash     `json:"cash"`
	Expenses FinanceExpenses `json:"expenses"`
	Result   FinanceResult   `json:"result"`
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
	Description: "Resumo financeiro de um período: vendas (quantidade, faturamento, custo, frete, comissão, " +
		"Profit/lucro, recebido, a receber), dinheiro executado (entradas, saídas e saldo por categoria e por " +
		"forma de pagamento), despesas operacionais, resultado (lucro das vendas − despesas) e saldos em " +
		"aberto de hoje (a receber, a pagar, a pagar por tipo de credor). Sem datas, usa o mês corrente até " +
		"hoje (fuso de São Paulo). Use para perguntas de fluxo de caixa, resultado e saldos. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in PeriodIn) (FinanceSummary, error) {
	s, err := get[FinanceSummary](ctx, gw, "/finance/summary", in.query())
	if err != nil {
		return FinanceSummary{}, err
	}
	if s.Cash.ByCategory == nil {
		s.Cash.ByCategory = []CategoryAmount{}
	}
	if s.Cash.ReceivedByMethod == nil {
		s.Cash.ReceivedByMethod = []ReceivedByMethod{}
	}
	if s.Expenses.ByCategory == nil {
		s.Expenses.ByCategory = []CategoryAmount{}
	}
	if s.Balances.PayableByCounterpartyKind == nil {
		s.Balances.PayableByCounterpartyKind = []PayableByKind{}
	}
	return s, nil
})

// --- list_payment_alerts ---

type ListPaymentAlertsIn struct {
	OnlyUnread *bool `json:"only_unread,omitempty" jsonschema:"só alertas não lidos (padrão true)"`
}

type PaymentAlert struct {
	ID               UUID                        `json:"id"`
	Kind             toolkit.AlertKind           `json:"kind"`
	Direction        toolkit.ObligationDirection `json:"direction"`
	SaleID           *UUID                       `json:"sale_id,omitempty"`
	ObligationID     UUID                        `json:"obligation_id"`
	InstallmentID    *UUID                       `json:"installment_id,omitempty"`
	CounterpartyName string                      `json:"counterparty_name"`
	CustomerName     *string                     `json:"customer_name,omitempty"` // só nos alertas a receber
	Amount           float64                     `json:"amount"`
	DueDate          Date                        `json:"due_date"`
	DaysRemaining    *int                        `json:"days_remaining,omitempty"`
	Read             bool                        `json:"read"`
}

type ListPaymentAlertsOut struct {
	Items []PaymentAlert `json:"items"`
}

var listPaymentAlerts = toolkit.Define(toolkit.Meta{
	Name:  "list_payment_alerts",
	Title: "Alertas de pagamento",
	Description: "Lista os alertas de contas: a receber do Customer (cliente) e a pagar a credores, OVERDUE " +
		"(vencido) e DUE_SOON (vencendo em até 5 dias), por parcela não coberta ou prazo da conta, com " +
		"valor, vencimento, dias restantes, direção (RECEIVABLE ou PAYABLE) e o obligation_id para abrir a " +
		"conta. Por padrão só os não lidos; passe only_unread false para incluir os já lidos. Use para " +
		"cobranças e para ver o que vence. Não marca alertas como lidos nem altera dados.",
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
		ObligationID  UUID    `json:"obligation_id"`
		InstallmentID *UUID   `json:"installment_id"`
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
		if n.Tipo == "pagamento_vencido" || n.Tipo == "conta_pagar_vencida" {
			kind = "OVERDUE"
		}
		direction := toolkit.ObligationDirection("RECEIVABLE")
		var customer *string
		if n.Tipo == "conta_pagar_vencida" || n.Tipo == "conta_pagar_vencendo" {
			direction = "PAYABLE"
		} else {
			name := n.ClienteNome
			customer = &name
		}
		out.Items = append(out.Items, PaymentAlert{
			ID: n.ID, Kind: kind, Direction: direction, SaleID: optUUID(n.VendaID), ObligationID: n.ObligationID,
			InstallmentID: optUUID(n.InstallmentID), CounterpartyName: n.ClienteNome, CustomerName: customer,
			Amount: n.Valor, DueDate: n.DataRef, DaysRemaining: n.DiasRestantes, Read: n.Lida,
		})
	}
	return out, nil
})

// --- list_obligations (R) ---

type ListObligationsIn struct {
	Direction        toolkit.ObligationDirection `json:"direction,omitempty" jsonschema:"PAYABLE (a pagar) ou RECEIVABLE (a receber)"`
	Status           []toolkit.ObligationStatus  `json:"status,omitempty" jsonschema:"status da conta (um ou mais; padrão: OPEN e PARTIAL, as em aberto)"`
	CounterpartyType toolkit.CounterpartyType    `json:"counterparty_type,omitempty" jsonschema:"tipo do credor/devedor: SUPPLIER, PARTNER ou CUSTOMER"`
	CounterpartyID   UUID                        `json:"counterparty_id,omitempty" jsonschema:"id do credor/devedor (search_suppliers, search_partners, search_customers)"`
	OriginType       toolkit.OriginType          `json:"origin_type,omitempty" jsonschema:"origem da conta: SALE (venda) ou PURCHASE (compra)"`
	OriginID         UUID                        `json:"origin_id,omitempty" jsonschema:"id da venda ou da compra"`
	Overdue          bool                        `json:"overdue,omitempty" jsonschema:"true: só contas vencidas (parcela não coberta ou prazo vencido)"`
	DueTo            Date                        `json:"due_to,omitempty" jsonschema:"só contas com vencimento até esta data (inclusive)"`
	toolkit.Page
}

var listObligations = toolkit.Define(toolkit.Meta{
	Name:  "list_obligations",
	Title: "Listar contas a pagar e a receber",
	Description: "Lista Obligations (contas): o que se deve a um credor dentro de uma compra ou venda (PAYABLE, a " +
		"pagar) ou o cliente deve (RECEIVABLE, a receber), com valor, quitado, em aberto, status, vencimento, " +
		"se está vencida e a próxima parcela. Por padrão só as em aberto (OPEN e PARTIAL), mais próximas do " +
		"vencimento primeiro. Filtros: direção, credor, origem, vencidas e vencimento. Traz totais do " +
		"filtro. Use para 'o que tenho a pagar/receber'. Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ListObligationsIn) (ObligationList, error) {
	limit, offset := in.Page.Normalize()
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	setIf(q, "direction", string(in.Direction))
	if len(in.Status) > 0 {
		q.Set("status", joinEnum(in.Status))
	} else {
		q.Set("status", "OPEN,PARTIAL")
	}
	setIf(q, "counterparty_type", string(in.CounterpartyType))
	setIf(q, "counterparty_id", string(in.CounterpartyID))
	setIf(q, "origin_type", string(in.OriginType))
	setIf(q, "origin_id", string(in.OriginID))
	if in.Overdue {
		q.Set("overdue", "true")
	}
	setIf(q, "due_to", string(in.DueTo))
	out, err := get[ObligationList](ctx, gw, "/obligations", q)
	if err != nil {
		return ObligationList{}, err
	}
	if out.Items == nil {
		out.Items = []ObligationSummary{}
	}
	return out, nil
})

// --- list_financial_movements (R) ---

type ListFinancialMovementsIn struct {
	From             Date                      `json:"from,omitempty" jsonschema:"a partir da data do movimento (inclusive)"`
	To               Date                      `json:"to,omitempty" jsonschema:"até a data do movimento (inclusive)"`
	Category         toolkit.MovementCategory  `json:"category,omitempty" jsonschema:"categoria da movimentação"`
	Direction        toolkit.MovementDirection `json:"direction,omitempty" jsonschema:"IN (entradas) ou OUT (saídas)"`
	CounterpartyType toolkit.CounterpartyType  `json:"counterparty_type,omitempty" jsonschema:"só movimentações que quitam contas deste tipo de credor"`
	CounterpartyID   UUID                      `json:"counterparty_id,omitempty" jsonschema:"só movimentações que quitam contas deste credor"`
	UnallocatedOnly  bool                      `json:"unallocated_only,omitempty" jsonschema:"true: só movimentações sem quitação (despesas e receitas avulsas)"`
	toolkit.Page
}

var listFinancialMovements = toolkit.Define(toolkit.Meta{
	Name:  "list_financial_movements",
	Title: "Listar movimentações financeiras",
	Description: "Lista as Financial movements (movimentações): dinheiro que de fato entrou (+) ou saiu (−), com data, " +
		"valor, categoria, forma de pagamento, credor e as contas que quitou (settlements). Filtros: período, " +
		"categoria, entrada ou saída, credor e só as sem quitação (despesas e receitas avulsas). Traz totais de " +
		"entradas, saídas e saldo do filtro. Mais recentes primeiro. Use para 'o que saiu ou entrou no mês'. " +
		"Não altera dados.",
	ReadOnly: true, Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ListFinancialMovementsIn) (MovementList, error) {
	limit, offset := in.Page.Normalize()
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	setIf(q, "from", string(in.From))
	setIf(q, "to", string(in.To))
	setIf(q, "category", string(in.Category))
	setIf(q, "direction", string(in.Direction))
	setIf(q, "counterparty_type", string(in.CounterpartyType))
	setIf(q, "counterparty_id", string(in.CounterpartyID))
	if in.UnallocatedOnly {
		q.Set("unallocated_only", "true")
	}
	out, err := get[MovementList](ctx, gw, "/financial-movements", q)
	if err != nil {
		return MovementList{}, err
	}
	if out.Items == nil {
		out.Items = []FinancialMovement{}
	}
	return out, nil
})

// --- settle_obligations (W · dd) ---

type SettleAllocationIn struct {
	ObligationID UUID              `json:"obligation_id" jsonschema:"id da conta (obtenha com list_obligations)"`
	Amount       *toolkit.PosMoney `json:"amount,omitempty" jsonschema:"valor a quitar em BRL (padrão: o que falta na conta)"`
}

type SettleObligationsIn struct {
	Allocations   []SettleAllocationIn  `json:"allocations" jsonschema:"contas quitadas pela mesma movimentação, todas a pagar ou todas a receber (pelo menos 1)"`
	OccurredOn    Date                  `json:"occurred_on,omitempty" jsonschema:"data em que o dinheiro saiu ou entrou (padrão: hoje em São Paulo)"`
	PaymentMethod toolkit.PaymentMethod `json:"payment_method,omitempty" jsonschema:"forma de pagamento: PIX, CASH, CARD, BOLETO ou TRANSFER"`
	Description   string                `json:"description,omitempty" jsonschema:"observação da movimentação"`
	toolkit.Dedupable
}

type SettleObligationsOut struct {
	Movement     FinancialMovement   `json:"movement"`
	Obligations  []ObligationSummary `json:"obligations"`
	Warnings     []string            `json:"warnings"`
	Deduplicated bool                `json:"deduplicated,omitempty"`
}

var settleObligations = toolkit.Define(toolkit.Meta{
	Name:  "settle_obligations",
	Title: "Pagar ou receber contas",
	Description: "Paga (contas a pagar) ou recebe (contas a receber): lança UMA Financial movement com a data e a " +
		"forma de pagamento e quita N contas (Settlement), cada uma com o valor informado (padrão: o que falta). " +
		"Um Pix pode quitar várias contas, mas todas na mesma direção. Use quando o dono disser que pagou um " +
		"fornecedor, motorista ou comissão, ou que o cliente pagou. Pré-requisito: obligation_ids de " +
		"list_obligations. Passar do devido é recusado; credores diferentes geram o aviso " +
		"MIXED_COUNTERPARTIES. Não desfaz quitações: isso só no app web.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SettleObligationsIn) (SettleObligationsOut, error) {
	allocs := make([]map[string]any, 0, len(in.Allocations))
	for i, a := range in.Allocations {
		a2 := map[string]any{"obligation_id": a.ObligationID}
		if a.Amount != nil {
			if err := checkMoney("allocations["+strconv.Itoa(i)+"].amount", float64(*a.Amount)); err != nil {
				return SettleObligationsOut{}, err
			}
			a2["amount"] = float64(*a.Amount)
		}
		allocs = append(allocs, a2)
	}
	on := string(in.OccurredOn)
	if on == "" {
		on = todaySP()
	}
	r, err := send[settleResult](ctx, gw, "POST", "/settlements", map[string]any{
		"movement":    map[string]any{"occurred_on": on, "payment_method": optStr(string(in.PaymentMethod)), "description": optStr(in.Description)},
		"allocations": allocs,
	})
	if err != nil {
		return SettleObligationsOut{}, err
	}
	return SettleObligationsOut{Movement: r.Movement, Obligations: nonNilObligations(r.Obligations), Warnings: nonNilStrings(r.Warnings)}, nil
}, toolkit.MinItems("allocations", 1))

func nonNilObligations(o []ObligationSummary) []ObligationSummary {
	if o == nil {
		return []ObligationSummary{}
	}
	return o
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// --- record_expense (W · dd) ---

type RecordExpenseIn struct {
	Category         toolkit.ExpenseCategory `json:"category" jsonschema:"VEHICLE (veículo), TAXES (impostos), FIXED_COSTS (custos fixos), MARKETING, BANK_FEES (tarifas), LABOR (mão de obra avulsa), OTHER_EXPENSE (outra despesa) ou OTHER_INCOME (receita avulsa)"`
	Amount           toolkit.PosMoney        `json:"amount" jsonschema:"valor em BRL (> 0); o sinal vem da categoria: OTHER_INCOME entra, as demais saem"`
	OccurredOn       Date                    `json:"occurred_on,omitempty" jsonschema:"data do movimento (padrão: hoje em São Paulo)"`
	PaymentMethod    toolkit.PaymentMethod   `json:"payment_method,omitempty" jsonschema:"forma de pagamento: PIX, CASH, CARD, BOLETO ou TRANSFER"`
	CounterpartyName string                  `json:"counterparty_name,omitempty" jsonschema:"quem recebeu ou pagou (texto livre, ex.: Posto Shell)"`
	Description      string                  `json:"description,omitempty" jsonschema:"observação"`
	toolkit.Dedupable
}

type RecordExpenseOut struct {
	Movement     FinancialMovement `json:"movement"`
	Warnings     []string          `json:"warnings"`
	Deduplicated bool              `json:"deduplicated,omitempty"`
}

var recordExpense = toolkit.Define(toolkit.Meta{
	Name:  "record_expense",
	Title: "Registrar despesa ou receita avulsa",
	Description: "Lança uma Financial movement avulsa, sem quitar conta: despesa operacional (combustível, " +
		"manutenção, impostos do mês, tarifas, marketing, mão de obra avulsa) ou receita avulsa " +
		"(OTHER_INCOME). Informe o valor sempre positivo; o sinal vem da categoria. Use quando o dono " +
		"contar um gasto que não é de uma compra ou venda. Para pagar uma conta de compra ou venda use " +
		"settle_obligations. Retirada do sócio não é despesa e não é lançada por aqui.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in RecordExpenseIn) (RecordExpenseOut, error) {
	if err := checkMoney("amount", float64(in.Amount)); err != nil {
		return RecordExpenseOut{}, err
	}
	amount := -float64(in.Amount)
	if in.Category == "OTHER_INCOME" {
		amount = float64(in.Amount)
	}
	on := string(in.OccurredOn)
	if on == "" {
		on = todaySP()
	}
	mv, err := send[FinancialMovement](ctx, gw, "POST", "/financial-movements", map[string]any{
		"occurred_on": on, "amount": amount, "category": in.Category, "payment_method": optStr(string(in.PaymentMethod)),
		"counterparty_name": optStr(in.CounterpartyName), "description": optStr(in.Description),
	})
	if err != nil {
		return RecordExpenseOut{}, err
	}
	return RecordExpenseOut{Movement: mv, Warnings: noWarnings()}, nil
})

// --- set_installments (W · I) ---

type InstallmentIn struct {
	DueDate       Date                  `json:"due_date" jsonschema:"vencimento da parcela"`
	Amount        toolkit.PosMoney      `json:"amount" jsonschema:"valor da parcela em BRL"`
	PaymentMethod toolkit.PaymentMethod `json:"payment_method,omitempty" jsonschema:"forma de pagamento combinada"`
	Notes         string                `json:"notes,omitempty" jsonschema:"observações"`
}

type SetInstallmentsIn struct {
	ObligationID   UUID                           `json:"obligation_id" jsonschema:"id da conta (obtenha com list_obligations)"`
	Installments   []InstallmentIn                `json:"installments,omitempty" jsonschema:"plano completo: substitui as parcelas atuais (a soma não pode passar do valor da conta)"`
	Count          int                            `json:"count,omitempty" jsonschema:"em vez de installments: gera count parcelas iguais (2 a 24); exige first_due_date; os centavos sobram na última"`
	FirstDueDate   Date                           `json:"first_due_date,omitempty" jsonschema:"vencimento da 1ª parcela (com count)"`
	IntervalMonths int                            `json:"interval_months,omitempty" jsonschema:"meses entre as parcelas (com count; padrão 1)"`
	Amount         *toolkit.PosMoney              `json:"amount,omitempty" jsonschema:"valor total a parcelar em BRL (com count; padrão: o que falta na conta)"`
	DueDate        toolkit.Nullable[toolkit.Date] `json:"due_date,omitzero" jsonschema:"prazo final combinado da conta YYYY-MM-DD; null limpa"`
}

type SetInstallmentsOut struct {
	Obligation ObligationDetail `json:"obligation"`
	Warnings   []string         `json:"warnings"`
}

var setInstallments = toolkit.Define(toolkit.Meta{
	Name:  "set_installments",
	Title: "Definir parcelas e prazo da conta",
	Description: "Define o plano de Installments (parcelas) e/ou o prazo final de uma Obligation (conta). Envie " +
		"installments (lista completa, substitui as atuais) OU count + first_due_date (gera parcelas iguais; " +
		"centavos na última), e/ou due_date (null limpa o prazo). Parcela é plano, não dinheiro: o que for " +
		"pago cobre as mais antigas primeiro. Use para 'parcelar em 5 vezes' ou 'prazo até 09/04'. " +
		"Pré-requisito: obligation_id de list_obligations. Não registra pagamentos (use settle_obligations).",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in SetInstallmentsIn) (SetInstallmentsOut, error) {
	hasList := len(in.Installments) > 0
	generate := in.Count != 0
	if !hasList && !generate && !in.DueDate.Present {
		return SetInstallmentsOut{}, invalid("informe installments, count + first_due_date ou due_date")
	}
	if hasList && generate {
		return SetInstallmentsOut{}, invalid("use installments OU count, não os dois")
	}
	if generate && in.FirstDueDate == "" {
		return SetInstallmentsOut{}, invalid("count exige first_due_date")
	}
	if !generate && (in.FirstDueDate != "" || in.IntervalMonths != 0 || in.Amount != nil) {
		return SetInstallmentsOut{}, invalid("first_due_date, interval_months e amount só valem com count")
	}
	path := "/obligations/" + pathID(string(in.ObligationID))
	// Primeiro o plano (pode ser recusado pelas regras); depois o prazo.
	switch {
	case hasList:
		plan := make([]map[string]any, 0, len(in.Installments))
		for i, p := range in.Installments {
			if err := checkMoney("installments["+strconv.Itoa(i)+"].amount", float64(p.Amount)); err != nil {
				return SetInstallmentsOut{}, err
			}
			plan = append(plan, map[string]any{"due_date": p.DueDate, "amount": float64(p.Amount),
				"payment_method": optStr(string(p.PaymentMethod)), "notes": optStr(p.Notes)})
		}
		if _, err := send[ObligationDetail](ctx, gw, "PUT", path+"/installments", map[string]any{"installments": plan}); err != nil {
			return SetInstallmentsOut{}, err
		}
	case generate:
		body := map[string]any{"count": in.Count, "first_due_date": in.FirstDueDate}
		if in.IntervalMonths != 0 {
			body["interval_months"] = in.IntervalMonths
		}
		if in.Amount != nil {
			if err := checkMoney("amount", float64(*in.Amount)); err != nil {
				return SetInstallmentsOut{}, err
			}
			body["amount"] = float64(*in.Amount)
		}
		if _, err := send[ObligationDetail](ctx, gw, "POST", path+"/installments/generate", body); err != nil {
			return SetInstallmentsOut{}, err
		}
	}
	if in.DueDate.Present {
		var due any
		if !in.DueDate.Null {
			due = in.DueDate.Value
		}
		if _, err := send[ObligationDetail](ctx, gw, "PATCH", path, map[string]any{"due_date": due}); err != nil {
			return SetInstallmentsOut{}, err
		}
	}
	d, err := get[ObligationDetail](ctx, gw, path, nil)
	if err != nil {
		return SetInstallmentsOut{}, afterWrite(err)
	}
	return SetInstallmentsOut{Obligation: d.normalized(), Warnings: noWarnings()}, nil
})

func (d ObligationDetail) normalized() ObligationDetail {
	if d.Components == nil {
		d.Components = []ObligationComponent{}
	}
	if d.Installments == nil {
		d.Installments = []Installment{}
	}
	if d.Settlements == nil {
		d.Settlements = []ObligationSettlement{}
	}
	d.Notes = opt(d.Notes)
	return d
}

// --- Receipts derivados da conta a receber (get_sale, record_receipt, confirm_receipt) ---

// saleReceivable devolve a conta a receber da Sale (nil se a venda ainda não tem Items).
func saleReceivable(ctx context.Context, gw toolkit.Gateway, saleID UUID) (*ObligationDetail, error) {
	list, err := get[ObligationList](ctx, gw, "/obligations", url.Values{
		"origin_type": {"SALE"}, "origin_id": {string(saleID)}, "direction": {"RECEIVABLE"}, "limit": {"1"}})
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	d, err := get[ObligationDetail](ctx, gw, "/obligations/"+pathID(string(list.Items[0].ID)), nil)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// receiptsOf traduz a conta a receber para a lista de Receipts do contrato antigo: cada
// quitação é um Receipt RECEIVED e cada parcela ainda não coberta, um PENDING (pelo que falta).
func receiptsOf(d *ObligationDetail) []Receipt {
	out := []Receipt{}
	if d == nil {
		return out
	}
	if d.Open > 0 {
		for _, i := range d.Installments {
			if i.Status == "COVERED" {
				continue
			}
			due := i.DueDate
			out = append(out, Receipt{ID: i.ID, Amount: round2(i.Amount - i.Covered), Status: "PENDING", ScheduledDate: &due,
				PaymentMethod: i.PaymentMethod, Notes: opt(i.Notes)})
		}
	}
	for _, s := range d.Settlements {
		paid := s.Movement.OccurredOn
		out = append(out, Receipt{ID: s.ID, Amount: s.Amount, Status: "RECEIVED", PaidAt: &paid,
			PaymentMethod: s.Movement.PaymentMethod, Notes: opt(s.Movement.Description)})
	}
	return out
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5*sign(v))) / 100
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// --- record_receipt (W · dd) e confirm_receipt (W · I) — DESCONTINUADAS (AYD-007) ---

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

// errSaleCanceled é a mensagem da API para recebimentos de Sale cancelada (AYD-007 §5).
const errSaleCanceled = "venda cancelada: recebimentos bloqueados"

// saleSummary relê a Sale para devolver o novo receivable.
func saleSummary(ctx context.Context, gw toolkit.Gateway, saleID UUID) (SaleSummary, error) {
	s, err := get[legacySale](ctx, gw, "/vendas/"+pathID(string(saleID)), nil)
	if err != nil {
		return SaleSummary{}, err
	}
	return s.toEN(), nil
}

var recordReceipt = toolkit.Define(toolkit.Meta{
	Name:  "record_receipt",
	Title: "Registrar recebimento",
	Description: "[DESCONTINUADA — use settle_obligations / set_installments] Registra um recebimento de uma Sale " +
		"(venda): com paid_at é uma quitação da conta a receber (RECEIVED; exige payment_method); sem paid_at é " +
		"uma parcela agendada (PENDING; scheduled_date, padrão hoje) no plano da conta. Continua funcionando, " +
		"traduzida para parcela/quitação; será removida na 2.0.0. Pré-requisito: sale_id de list_sales. " +
		"Devolve o recebimento e a venda com o novo a receber. Venda cancelada não aceita recebimentos.",
	Dedupe: true,
}, func(ctx context.Context, gw toolkit.Gateway, in RecordReceiptIn) (RecordReceiptOut, error) {
	if err := checkMoney("amount", float64(in.Amount)); err != nil {
		return RecordReceiptOut{}, err
	}
	sale0, err := get[legacySale](ctx, gw, "/vendas/"+pathID(string(in.SaleID)), nil) // 404 "venda não encontrada"
	if err != nil {
		return RecordReceiptOut{}, err
	}
	if sale0.DeliveryStatus == "CANCELED" {
		return RecordReceiptOut{}, &toolkit.APIError{Status: 409, Message: errSaleCanceled}
	}
	if in.PaidAt != "" && in.ScheduledDate != "" {
		return RecordReceiptOut{}, invalid("informe paid_at ou scheduled_date, não os dois")
	}
	acc, err := saleReceivable(ctx, gw, in.SaleID)
	if err != nil {
		return RecordReceiptOut{}, err
	}
	if acc == nil {
		return RecordReceiptOut{}, toolkit.NewToolError(toolkit.CodeInvalidInput, "a venda não tem valor a receber (sem itens)")
	}
	var receipt Receipt
	if in.PaidAt != "" {
		if in.PaymentMethod == "" {
			return RecordReceiptOut{}, invalid("campo 'payment_method' é obrigatório para recebimento")
		}
		r, err := send[settleResult](ctx, gw, "POST", "/obligations/"+pathID(string(acc.ID))+"/settle", map[string]any{
			"amount":   float64(in.Amount),
			"movement": map[string]any{"occurred_on": in.PaidAt, "payment_method": in.PaymentMethod, "description": optStr(in.Notes)},
		})
		if err != nil {
			return RecordReceiptOut{}, err
		}
		paid := r.Movement.OccurredOn
		id := r.Movement.ID
		if len(r.Movement.Settlements) > 0 {
			id = r.Movement.Settlements[0].ID
		}
		receipt = Receipt{ID: id, Amount: float64(in.Amount), Status: "RECEIVED", PaidAt: &paid,
			PaymentMethod: r.Movement.PaymentMethod, Notes: opt(r.Movement.Description)}
	} else {
		due := string(in.ScheduledDate)
		if due == "" {
			due = todaySP()
		}
		plan := make([]map[string]any, 0, len(acc.Installments)+1)
		for _, i := range acc.Installments {
			plan = append(plan, map[string]any{"due_date": i.DueDate, "amount": i.Amount, "payment_method": i.PaymentMethod, "notes": i.Notes})
		}
		plan = append(plan, map[string]any{"due_date": due, "amount": float64(in.Amount), "payment_method": optStr(string(in.PaymentMethod)), "notes": optStr(in.Notes)})
		d, err := send[ObligationDetail](ctx, gw, "PUT", "/obligations/"+pathID(string(acc.ID))+"/installments", map[string]any{"installments": plan})
		if err != nil {
			return RecordReceiptOut{}, err
		}
		// O PUT recria o plano: a parcela nova é a última com a data e o valor pedidos.
		for _, i := range d.Installments {
			if string(i.DueDate) == due && i.Amount == float64(in.Amount) {
				d0 := i.DueDate
				receipt = Receipt{ID: i.ID, Amount: i.Amount, Status: "PENDING", ScheduledDate: &d0, PaymentMethod: i.PaymentMethod, Notes: opt(i.Notes)}
			}
		}
	}
	sale, err := saleSummary(ctx, gw, in.SaleID)
	if err != nil {
		return RecordReceiptOut{}, afterWrite(err)
	}
	return RecordReceiptOut{Receipt: receipt, Sale: sale, Warnings: noWarnings()}, nil
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
	Description: "[DESCONTINUADA — use settle_obligations / set_installments] Confirma o recebimento PENDING " +
		"(parcela agendada) de uma Sale (venda): quita o que falta da parcela, com a data do pagamento (padrão: " +
		"hoje) e a forma de pagamento, obrigatória se a parcela ainda não tiver uma. Continua funcionando, " +
		"traduzida para a quitação da conta; será removida na 2.0.0. Pré-requisitos: sale_id e receipt_id " +
		"(get_sale). Devolve o recebimento e a venda com o novo a receber. Não desfaz confirmações.",
	Idempotent: true,
}, func(ctx context.Context, gw toolkit.Gateway, in ConfirmReceiptIn) (ReceiptOut, error) {
	sale0, err := get[legacySale](ctx, gw, "/vendas/"+pathID(string(in.SaleID)), nil)
	if err != nil {
		return ReceiptOut{}, err
	}
	if sale0.DeliveryStatus == "CANCELED" {
		return ReceiptOut{}, &toolkit.APIError{Status: 409, Message: errSaleCanceled}
	}
	acc, err := saleReceivable(ctx, gw, in.SaleID)
	if err != nil {
		return ReceiptOut{}, err
	}
	var inst *Installment
	if acc != nil {
		for i := range acc.Installments {
			if acc.Installments[i].ID == in.ReceiptID {
				inst = &acc.Installments[i]
			}
		}
	}
	if inst == nil {
		return ReceiptOut{}, toolkit.NewToolError(toolkit.CodeNotFound, "pagamento não encontrado")
	}
	if inst.Status == "COVERED" {
		return ReceiptOut{}, invalid("pagamento já confirmado")
	}
	method := in.PaymentMethod
	if method == "" && inst.PaymentMethod != nil {
		method = *inst.PaymentMethod
	}
	if method == "" {
		return ReceiptOut{}, invalid("campo 'payment_method' é obrigatório para recebimento")
	}
	paidAt := string(in.PaidAt)
	if paidAt == "" {
		paidAt = todaySP()
	}
	amount := round2(inst.Amount - inst.Covered)
	if amount > acc.Open {
		amount = acc.Open
	}
	if _, err := send[settleResult](ctx, gw, "POST", "/obligations/"+pathID(string(acc.ID))+"/settle", map[string]any{
		"amount": amount, "movement": map[string]any{"occurred_on": paidAt, "payment_method": method},
	}); err != nil {
		return ReceiptOut{}, err
	}
	sale, err := saleSummary(ctx, gw, in.SaleID)
	if err != nil {
		return ReceiptOut{}, afterWrite(err)
	}
	paid := Date(paidAt)
	pm := method
	return ReceiptOut{Receipt: Receipt{ID: inst.ID, Amount: amount, Status: "RECEIVED", PaidAt: &paid, PaymentMethod: &pm,
		Notes: opt(inst.Notes)}, Sale: sale, Warnings: noWarnings()}, nil
})
