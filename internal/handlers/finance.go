package handlers

import (
	"log"
	"net/http"

	"github.com/silvioubaldino/sales-backend/internal/finance"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

// FinanceHandler serve o painel Financeiro (AYD-004): visão por competência (Sales do
// período), movimento de caixa datado e saldos em aberto hoje.
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

type receivedByMethod struct {
	PaymentMethod *string `json:"payment_method" gorm:"column:payment_method"` // null = legado "Não informado"
	Amount        float64 `json:"amount" gorm:"column:amount"`
}

type financeCash struct {
	Received         float64            `json:"received"`
	ReceivedByMethod []receivedByMethod `json:"received_by_method"`
	PaidToSuppliers  float64            `json:"paid_to_suppliers"`
}

type financeBalances struct {
	Receivable         float64 `json:"receivable"`
	PayableToSuppliers float64 `json:"payable_to_suppliers"`
	FreightPayable     float64 `json:"freight_payable"`
	CommissionPayable  float64 `json:"commission_payable"`
}

type financeSummaryResponse struct {
	Period   financePeriod   `json:"period"`
	Sales    financeSales    `json:"sales"`
	Cash     financeCash     `json:"cash"`
	Balances financeBalances `json:"balances"`
}

// summary: GET /finance/summary?from&to. Cada agregado é uma consulta própria sobre as
// views (itens e Receipts já pré-agregados por Sale) ou sobre a tabela de Receipts —
// nunca um SUM sobre join de Items × Receipts (fan-out). Valores com ROUND(…, 2).
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
		Cash:   financeCash{ReceivedByMethod: []receivedByMethod{}},
	}

	fail := func(what string, err error) {
		log.Printf("ERROR FinanceSummary %s: %v", what, err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
	}

	// sales: Sales com data_venda no período, não CANCELED (a view já exclui as excluídas).
	if err := db.Raw(`
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
	`, from, to).Scan(&resp.Sales).Error; err != nil {
		fail("sales", err)
		return
	}

	// cash.received e received_by_method: Receipts recebidos com data_pagamento no período
	// (Receipt e Sale não excluídos; Sale não CANCELED).
	const receiptsInPeriod = `
		FROM pagamentos_cliente p
		JOIN vendas v ON v.id = p.venda_id
		WHERE p.status = 'recebido'
			AND p.deleted_at IS NULL
			AND p.data_pagamento >= ? AND p.data_pagamento <= ?
			AND v.deleted_at IS NULL
			AND v.delivery_status <> 'CANCELED'`
	if err := db.Raw(`SELECT ROUND(COALESCE(SUM(p.valor), 0), 2) `+receiptsInPeriod, from, to).
		Scan(&resp.Cash.Received).Error; err != nil {
		fail("cash.received", err)
		return
	}
	if err := db.Raw(`
		SELECT p.payment_method::text AS payment_method, ROUND(SUM(p.valor), 2) AS amount `+receiptsInPeriod+`
		GROUP BY p.payment_method
		HAVING SUM(p.valor) <> 0
		ORDER BY amount DESC, p.payment_method ASC NULLS LAST
	`, from, to).Scan(&resp.Cash.ReceivedByMethod).Error; err != nil {
		fail("cash.received_by_method", err)
		return
	}

	// cash.paid_to_suppliers: Purchases não excluídas com paid_at no período (RN-06).
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(paid_amount), 0), 2)
		FROM purchases
		WHERE deleted_at IS NULL AND paid_at >= ? AND paid_at <= ?
	`, from, to).Scan(&resp.Cash.PaidToSuppliers).Error; err != nil {
		fail("cash.paid_to_suppliers", err)
		return
	}

	// balances: saldos em aberto HOJE (ignoram o período); Sales CANCELED saem.
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(vr.receivable), 0), 2)
		FROM vw_vendas_resumo vr WHERE vr.delivery_status <> 'CANCELED'
	`).Scan(&resp.Balances.Receivable).Error; err != nil {
		fail("balances.receivable", err)
		return
	}
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(pr.to_pay), 0), 2) FROM vw_purchases_resumo pr
	`).Scan(&resp.Balances.PayableToSuppliers).Error; err != nil {
		fail("balances.payable_to_suppliers", err)
		return
	}
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(vr.freight_payable), 0), 2)
		FROM vw_vendas_resumo vr WHERE vr.delivery_status <> 'CANCELED'
	`).Scan(&resp.Balances.FreightPayable).Error; err != nil {
		fail("balances.freight_payable", err)
		return
	}
	if err := db.Raw(`
		SELECT ROUND(COALESCE(SUM(vr.comissao_valor), 0), 2)
		FROM vw_vendas_resumo vr
		WHERE vr.delivery_status <> 'CANCELED' AND vr.comissao_valor > 0 AND vr.comissao_paga = FALSE
	`).Scan(&resp.Balances.CommissionPayable).Error; err != nil {
		fail("balances.commission_payable", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
