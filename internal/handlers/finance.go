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

type FinanceHandler struct {
	db *gorm.DB
}

func NewFinanceHandler(db *gorm.DB) *FinanceHandler {
	return &FinanceHandler{db: db}
}

func (h *FinanceHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /finance/summary", h.summary)
}

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
	OtherCosts float64 `json:"other_costs" gorm:"column:other_costs"`
	Profit     float64 `json:"profit" gorm:"column:profit"`
	Received   float64 `json:"received" gorm:"column:received"`
	Receivable float64 `json:"receivable" gorm:"column:receivable"`
}

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

// Cada agregado é uma consulta própria sobre as views (itens e quitações já pré-agregados) — nunca um SUM sobre join (fan-out).
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

	if err := querySalesBlock(db, from, to, &resp.Sales); err != nil {
		fail("sales", err)
		return
	}
	if resp.Cash, err = obligations.CashSummary(db, from, to); err != nil {
		fail("cash", err)
		return
	}
	if resp.Expenses, err = obligations.ExpensesSummary(db, from, to); err != nil {
		fail("expenses", err)
		return
	}
	resp.Result = financeResult{
		SalesProfit: resp.Sales.Profit,
		Expenses:    resp.Expenses.Total,
		NetResult:   obligations.Round2(resp.Sales.Profit - resp.Expenses.Total),
	}
	if resp.Balances, err = obligations.BalancesSummary(db); err != nil {
		fail("balances", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// Fonte única de /finance/summary e /dashboard/kpis.
func querySalesBlock(db *gorm.DB, from, to time.Time, out *financeSales) error {
	return db.Raw(`
		SELECT
			COUNT(*)                                         AS count,
			ROUND(COALESCE(SUM(vr.total_venda), 0), 2)       AS revenue,
			ROUND(COALESCE(SUM(vr.total_custo), 0), 2)       AS cost,
			ROUND(COALESCE(SUM(vr.costs_freight), 0), 2)    AS freight,
			ROUND(COALESCE(SUM(vr.costs_commission), 0), 2) AS commission,
			ROUND(COALESCE(SUM(vr.costs_other), 0), 2)      AS other_costs,
			ROUND(COALESCE(SUM(vr.lucro), 0), 2)             AS profit,
			ROUND(COALESCE(SUM(vr.total_recebido), 0), 2)    AS received,
			ROUND(COALESCE(SUM(vr.receivable), 0), 2)        AS receivable
		FROM vw_vendas_resumo vr
		WHERE vr.data_venda >= ? AND vr.data_venda <= ?
			AND vr.delivery_status <> 'CANCELED'
	`, from, to).Scan(out).Error
}

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
		SELECT ROUND(COALESCE(SUM(o.open) FILTER (WHERE o.counterparty_type IS DISTINCT FROM 'PARTNER'), 0), 2)
		FROM vw_obligations o WHERE o.direction = 'PAYABLE' AND o.sale_id IS NOT NULL
	`).Scan(&out.FreightPayable).Error; err != nil {
		return "balances.freight_payable", err
	}
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(o.open) FILTER (WHERE o.counterparty_type = 'PARTNER'), 0), 2)
		FROM vw_obligations o WHERE o.direction = 'PAYABLE' AND o.sale_id IS NOT NULL
	`).Scan(&out.CommissionPayable).Error; err != nil {
		return "balances.commission_payable", err
	}
	return "", nil
}
