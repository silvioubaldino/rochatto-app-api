package obligations

import (
	"time"

	"github.com/google/uuid"
)

const dateFmt = "2006-01-02"

func fmtDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(dateFmt)
	return &s
}

// --- Obligation (conta) ---

// Origin é a compra ou venda da conta.
type Origin struct {
	Type  string    `json:"type"`
	ID    uuid.UUID `json:"id"`
	Label string    `json:"label"`
}

// Counterparty é o credor (ou o devedor, no preço da venda).
type Counterparty struct {
	Type string    `json:"type"`
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// NextInstallment é a próxima parcela não coberta.
type NextInstallment struct {
	DueDate    string  `json:"due_date"`
	AmountOpen float64 `json:"amount_open"`
}

// Obligation é a conta a pagar/receber (resumo — listagens e respostas de quitação).
type Obligation struct {
	ID              uuid.UUID        `json:"id"`
	Direction       string           `json:"direction"`
	Origin          Origin           `json:"origin"`
	Counterparty    *Counterparty    `json:"counterparty"`
	Amount          float64          `json:"amount"`
	Settled         float64          `json:"settled"`
	Open            float64          `json:"open"`
	Status          string           `json:"status"`
	DueDate         *string          `json:"due_date"`
	Overdue         bool             `json:"overdue"`
	NextInstallment *NextInstallment `json:"next_installment"`
	Warnings        []string         `json:"warnings"`
	Notes           *string          `json:"notes"`
}

// Component é um componente do valor da conta.
type Component struct {
	Type   string    `json:"type"`
	ID     uuid.UUID `json:"id"`
	Label  string    `json:"label"`
	Amount float64   `json:"amount"`
}

// Installment é uma parcela (plano) com a situação de cobertura.
type Installment struct {
	ID            uuid.UUID `json:"id"`
	DueDate       string    `json:"due_date"`
	Amount        float64   `json:"amount"`
	PaymentMethod *string   `json:"payment_method"`
	Notes         *string   `json:"notes"`
	Covered       float64   `json:"covered"`
	Status        string    `json:"status"`
	Overdue       bool      `json:"overdue"`
}

// SettlementRef é uma quitação da conta, com o resumo da movimentação.
type SettlementRef struct {
	ID       uuid.UUID       `json:"id"`
	Amount   float64         `json:"amount"`
	Movement MovementSummary `json:"movement"`
}

// ObligationDetail é a conta com componentes, parcelas e quitações.
type ObligationDetail struct {
	Obligation
	Components   []Component     `json:"components"`
	Installments []Installment   `json:"installments"`
	Settlements  []SettlementRef `json:"settlements"`
}

// Totals são os totais do conjunto filtrado (sem paginação).
type Totals struct {
	Amount  float64 `json:"amount"`
	Settled float64 `json:"settled"`
	Open    float64 `json:"open"`
}

// ObligationList é a resposta de GET /obligations.
type ObligationList struct {
	Items   []Obligation `json:"items"`
	Totals  Totals       `json:"totals"`
	Total   int64        `json:"total"`
	Offset  int          `json:"offset"`
	HasMore bool         `json:"has_more"`
}

// --- Financial movement ---

// MovementSummary é a movimentação resumida (dentro da conta).
type MovementSummary struct {
	ID               uuid.UUID `json:"id"`
	OccurredOn       string    `json:"occurred_on"`
	Amount           float64   `json:"amount"`
	Category         string    `json:"category"`
	PaymentMethod    *string   `json:"payment_method"`
	CounterpartyName *string   `json:"counterparty_name"`
	Description      *string   `json:"description"`
}

// MovementSettlement é uma quitação vista da movimentação.
type MovementSettlement struct {
	ID           uuid.UUID `json:"id"`
	ObligationID uuid.UUID `json:"obligation_id"`
	Amount       float64   `json:"amount"`
}

// Movement é o dinheiro executado (+ entrada / − saída).
type Movement struct {
	ID               uuid.UUID            `json:"id"`
	OccurredOn       string               `json:"occurred_on"`
	Amount           float64              `json:"amount"`
	Category         string               `json:"category"`
	PaymentMethod    *string              `json:"payment_method"`
	CounterpartyName *string              `json:"counterparty_name"`
	Description      *string              `json:"description"`
	Settlements      []MovementSettlement `json:"settlements"`
	Unallocated      float64              `json:"unallocated"`
	CreatedAt        time.Time            `json:"created_at"`
	CorrectedAt      *time.Time           `json:"corrected_at"`
}

// MovementTotals: outflow é a soma das saídas em valor positivo; net = inflow − outflow.
type MovementTotals struct {
	Inflow  float64 `json:"inflow"`
	Outflow float64 `json:"outflow"`
	Net     float64 `json:"net"`
}

// MovementList é a resposta de GET /financial-movements.
type MovementList struct {
	Items   []Movement     `json:"items"`
	Totals  MovementTotals `json:"totals"`
	Total   int64          `json:"total"`
	Offset  int            `json:"offset"`
	HasMore bool           `json:"has_more"`
}

// SettleResult é a resposta de POST /settlements e de POST /obligations/{id}/settle.
type SettleResult struct {
	Movement    Movement     `json:"movement"`
	Obligations []Obligation `json:"obligations"`
	Warnings    []string     `json:"warnings"`
}
