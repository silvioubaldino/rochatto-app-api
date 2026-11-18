package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/silvioubaldino/sales-backend/internal/finance"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"gorm.io/gorm"
)

// FinanceHandler serve o painel Financeiro (AYD-004/AYD-007): visão por competência (Sales do
// período), dinheiro executado (movimentações), despesas, resultado e saldos em aberto hoje.
type FinanceHandler struct {
	db *gorm.DB
}

func NewFinanceHandler(db *gorm.DB) *FinanceHandler {
	return &FinanceHandler{db: db}
}

func (h *FinanceHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /finance/summary", h.summary)
}

// --- Response types (contrato do AYD-004) ---

type financePeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type financeSales struct {
	Count      int64   `json:"count" gorm:"column:count"`
	Revenue    float64 `json:"revenue" gorm:"column:revenue"`
	Cost       float64 `json:"cost" gorm:"column:cost"`
	Freight    float64 `json:"freight" gorm:"column:freight"`
	Commission float64 `json:"commission" gorm:"column:commission"`
	Profit     float64 `json:"profit" gorm:"column:profit"`
	Received   float64 `json:"received" gorm:"column:received"`
	Receivable float64 `json:"receivable" gorm:"column:receivable"`
}

// financeBalances: saldos em aberto no formato do dashboard (/dashboard/kpis — AYD-005),
// re-derivados das contas (AYD-007). /finance/summary usa obligations.Balances.
type financeBalances struct {
	Receivable         float64 `json:"receivable"`
	PayableToSuppliers float64 `json:"payable_to_suppliers"`
	FreightPayable     float64 `json:"freight_payable"`
	CommissionPayable  float64 `json:"commission_payable"`
}

type financeResult struct {
	SalesProfit float64 `json:"sales_profit"`
	Expenses    float64 `json:"expenses"`
	NetResult   float64 `json:"net_result"`
}

type financeSummaryResponse struct {
	Period   financePeriod        `json:"period"`
	Sales    financeSales         `json:"sales"`
	Cash     obligations.Cash     `json:"cash"`
	Expenses obligations.Expenses `json:"expenses"`
	Result   financeResult        `json:"result"`
	Balances obligations.Balances `json:"balances"`
}

// summary: GET /finance/summary?from&to (AYD-007 §6). Cada agregado é uma consulta própria
// sobre as views (itens e quitações já pré-agregados) — nunca um SUM sobre join (fan-out).
// Valores com ROUND(…, 2).
func (h *FinanceHandler) summary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to, err := finance.ResolvePeriod(q.Get("from"), q.Get("to"), today())
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	db := h.db.WithContext(r.Context())

	resp := financeSummaryResponse{
		Period: financePeriod{From: from.Format("2006-01-02"), To: to.Format("2006-01-02")},
	}

	fail := func(what string, err error) {
		log.Printf("ERROR FinanceSummary %s: %v", what, err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
	}

	// sales: Sales com data_venda no período, não CANCELED (compartilhado com /dashboard/kpis);
	// received = Σ quitações das contas RECEIVABLE dessas Sales.
	if err := querySalesBlock(db, from, to, &resp.Sales); err != nil {
		fail("sales", err)
		return
	}
	// cash e expenses: movimentações do período (dinheiro executado).
	if resp.Cash, err = obligations.CashSummary(db, from, to); err != nil {
		fail("cash", err)
		return
	}
	if resp.Expenses, err = obligations.ExpensesSummary(db, from, to); err != nil {
		fail("expenses", err)
		return
	}
	// result (RN-17): Profit das Sales do período − despesas.
	resp.Result = financeResult{
		SalesProfit: resp.Sales.Profit,
		Expenses:    resp.Expenses.Total,
		NetResult:   obligations.Round2(resp.Sales.Profit - resp.Expenses.Total),
	}
	// balances: Σ open das contas HOJE.
	if resp.Balances, err = obligations.BalancesSummary(db); err != nil {
		fail("balances", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// querySalesBlock preenche o bloco `sales` do painel: Sales com data_venda em [from, to],
// não CANCELED (a view já exclui as excluídas). Fonte única de /finance/summary e
// /dashboard/kpis (AYD-005: os dois nunca divergem).
func querySalesBlock(db *gorm.DB, from, to time.Time, out *financeSales) error {
	return db.Raw(`
		SELECT
			COUNT(*)                                         AS count,
			ROUND(COALESCE(SUM(vr.total_venda), 0), 2)       AS revenue,
			ROUND(COALESCE(SUM(vr.total_custo), 0), 2)       AS cost,
			ROUND(COALESCE(SUM(vr.frete_valor), 0), 2)       AS freight,
			ROUND(COALESCE(SUM(vr.comissao_valor), 0), 2)    AS commission,
			ROUND(COALESCE(SUM(vr.lucro), 0), 2)             AS profit,
			ROUND(COALESCE(SUM(vr.total_recebido), 0), 2)    AS received,
			ROUND(COALESCE(SUM(vr.receivable), 0), 2)        AS receivable
		FROM vw_vendas_resumo vr
		WHERE vr.data_venda >= ? AND vr.data_venda <= ?
			AND vr.delivery_status <> 'CANCELED'
	`, from, to).Scan(out).Error
}

// queryBalances preenche os saldos em aberto HOJE no formato do dashboard (ignoram o período;
// Sales CANCELED saem — a conta delas tem amount 0). Em caso de erro devolve o nome do saldo que
// falhou (para o log). Fonte do bloco `balances` de /dashboard/kpis.
func queryBalances(db *gorm.DB, out *financeBalances) (string, error) {
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(vr.receivable), 0), 2)
		FROM vw_vendas_resumo vr WHERE vr.delivery_status <> 'CANCELED'
	`).Scan(&out.Receivable).Error; err != nil {
		return "balances.receivable", err
	}
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(pr.payable), 0), 2) FROM vw_purchases_resumo pr
	`).Scan(&out.PayableToSuppliers).Error; err != nil {
		return "balances.payable_to_suppliers", err
	}
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(vr.freight_payable), 0), 2)
		FROM vw_vendas_resumo vr WHERE vr.delivery_status <> 'CANCELED'
	`).Scan(&out.FreightPayable).Error; err != nil {
		return "balances.freight_payable", err
	}
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(vr.commission_payable), 0), 2)
		FROM vw_vendas_resumo vr WHERE vr.delivery_status <> 'CANCELED'
	`).Scan(&out.CommissionPayable).Error; err != nil {
		return "balances.commission_payable", err
	}
	return "", nil
}
