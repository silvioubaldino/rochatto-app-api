package obligations

import (
	"time"

	"gorm.io/gorm"
)

// CategoryAmount é um total por categoria de movimentação.
type CategoryAmount struct {
	Category string  `json:"category" gorm:"column:category"`
	Amount   float64 `json:"amount" gorm:"column:amount"`
}

// MethodAmount é um total por forma de pagamento (null = "não informado").
type MethodAmount struct {
	PaymentMethod *string `json:"payment_method" gorm:"column:payment_method"`
	Amount        float64 `json:"amount" gorm:"column:amount"`
}

// Cash é o dinheiro executado no período (AYD-007 §6). by_category traz o valor com sinal
// (entradas +, saídas −); outflow é positivo.
type Cash struct {
	Inflow           float64          `json:"inflow"`
	Outflow          float64          `json:"outflow"`
	Net              float64          `json:"net"`
	ByCategory       []CategoryAmount `json:"by_category"`
	ReceivedByMethod []MethodAmount   `json:"received_by_method"`
}

// Expenses são as despesas do período: saídas de categoria de despesa SEM quitação.
type Expenses struct {
	Total      float64          `json:"total"`
	ByCategory []CategoryAmount `json:"by_category"`
}

// KindOpen é o a pagar em aberto por tipo de credor: o kind do Supplier; PARTNER (comissões) ou
// UNASSIGNED (conta sem credor, "a definir").
type KindOpen struct {
	Kind string  `json:"kind" gorm:"column:kind"`
	Open float64 `json:"open" gorm:"column:open"`
}

// Balances são os saldos em aberto de hoje (Σ open das contas; Sales canceladas já têm amount 0).
type Balances struct {
	Receivable                float64    `json:"receivable"`
	Payable                   float64    `json:"payable"`
	PayableByCounterpartyKind []KindOpen `json:"payable_by_counterparty_kind"`
}

// CashSummary agrega as movimentações do período.
func CashSummary(db *gorm.DB, from, to time.Time) (Cash, error) {
	out := Cash{ByCategory: []CategoryAmount{}, ReceivedByMethod: []MethodAmount{}}
	var tot struct {
		Inflow  float64 `gorm:"column:inflow"`
		Outflow float64 `gorm:"column:outflow"`
	}
	if err := db.Raw(`SELECT ROUND(COALESCE(SUM(amount) FILTER (WHERE amount > 0), 0), 2) AS inflow,
			ROUND(COALESCE(-SUM(amount) FILTER (WHERE amount < 0), 0), 2) AS outflow
		FROM financial_movements WHERE deleted_at IS NULL AND occurred_on >= ? AND occurred_on <= ?`, from, to).Scan(&tot).Error; err != nil {
		return out, err
	}
	out.Inflow, out.Outflow, out.Net = tot.Inflow, tot.Outflow, Round2(tot.Inflow-tot.Outflow)
	if err := db.Raw(`SELECT category::text AS category, ROUND(SUM(amount), 2) AS amount
		FROM financial_movements WHERE deleted_at IS NULL AND occurred_on >= ? AND occurred_on <= ?
		GROUP BY category ORDER BY ABS(SUM(amount)) DESC, category`, from, to).Scan(&out.ByCategory).Error; err != nil {
		return out, err
	}
	if err := db.Raw(`SELECT payment_method::text AS payment_method, ROUND(SUM(amount), 2) AS amount
		FROM financial_movements
		WHERE deleted_at IS NULL AND category = 'SALE_RECEIPT' AND occurred_on >= ? AND occurred_on <= ?
		GROUP BY payment_method HAVING SUM(amount) <> 0
		ORDER BY amount DESC, payment_method ASC NULLS LAST`, from, to).Scan(&out.ReceivedByMethod).Error; err != nil {
		return out, err
	}
	return out, nil
}

// ExpensesSummary agrega as despesas do período (movimentação sem quitação, categoria de despesa).
func ExpensesSummary(db *gorm.DB, from, to time.Time) (Expenses, error) {
	out := Expenses{ByCategory: []CategoryAmount{}}
	if err := db.Raw(`SELECT m.category::text AS category, ROUND(-SUM(m.amount), 2) AS amount
		FROM financial_movements m
		WHERE m.deleted_at IS NULL AND m.occurred_on >= ? AND m.occurred_on <= ?
		  AND m.category::text IN ?
		  AND NOT EXISTS (SELECT 1 FROM settlements s WHERE s.movement_id = m.id AND s.deleted_at IS NULL)
		GROUP BY m.category ORDER BY SUM(m.amount) ASC, m.category`, from, to, ExpenseCategories()).Scan(&out.ByCategory).Error; err != nil {
		return out, err
	}
	var c int64
	for _, r := range out.ByCategory {
		c += Cents(r.Amount)
	}
	out.Total = FromCents(c)
	return out, nil
}

// BalancesSummary devolve os saldos em aberto de hoje.
func BalancesSummary(db *gorm.DB) (Balances, error) {
	out := Balances{PayableByCounterpartyKind: []KindOpen{}}
	var tot struct {
		Receivable float64 `gorm:"column:receivable"`
		Payable    float64 `gorm:"column:payable"`
	}
	if err := db.Raw(`SELECT ROUND(COALESCE(SUM(open) FILTER (WHERE direction = 'RECEIVABLE'), 0), 2) AS receivable,
			ROUND(COALESCE(SUM(open) FILTER (WHERE direction = 'PAYABLE'), 0), 2) AS payable
		FROM vw_obligations`).Scan(&tot).Error; err != nil {
		return out, err
	}
	out.Receivable, out.Payable = tot.Receivable, tot.Payable
	if err := db.Raw(`SELECT CASE WHEN o.counterparty_type = 'SUPPLIER' THEN f.kind::text
			WHEN o.counterparty_type = 'PARTNER' THEN 'PARTNER' ELSE 'UNASSIGNED' END AS kind,
			ROUND(SUM(o.open), 2) AS open
		FROM vw_obligations o
		LEFT JOIN fornecedores f ON f.id = o.counterparty_id AND o.counterparty_type = 'SUPPLIER'
		WHERE o.direction = 'PAYABLE'
		GROUP BY 1 HAVING SUM(o.open) > 0 ORDER BY open DESC, kind`).Scan(&out.PayableByCounterpartyKind).Error; err != nil {
		return out, err
	}
	return out, nil
}
