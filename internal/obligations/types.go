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

type Origin struct {
	Type  string    `json:"type"`
	ID    uuid.UUID `json:"id"`
	Label string    `json:"label"`
}

type Counterparty struct {
	Type string    `json:"type"`
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type NextInstallment struct {
	DueDate    string  `json:"due_date"`
	AmountOpen float64 `json:"amount_open"`
}

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

type Component struct {
	Type   string    `json:"type"`
	ID     uuid.UUID `json:"id"`
	Label  string    `json:"label"`
	Amount float64   `json:"amount"`
}

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

type SettlementRef struct {
	ID       uuid.UUID       `json:"id"`
	Amount   float64         `json:"amount"`
	Movement MovementSummary `json:"movement"`
}

type ObligationDetail struct {
	Obligation
	Components   []Component     `json:"components"`
	Installments []Installment   `json:"installments"`
	Settlements  []SettlementRef `json:"settlements"`
}

type Totals struct {
	Amount  float64 `json:"amount"`
	Settled float64 `json:"settled"`
	Open    float64 `json:"open"`
}

type ObligationList struct {
	Items   []Obligation `json:"items"`
	Totals  Totals       `json:"totals"`
	Total   int64        `json:"total"`
	Offset  int          `json:"offset"`
	HasMore bool         `json:"has_more"`
}

type MovementSummary struct {
	ID               uuid.UUID `json:"id"`
	OccurredOn       string    `json:"occurred_on"`
	Amount           float64   `json:"amount"`
	Category         string    `json:"category"`
	PaymentMethod    *string   `json:"payment_method"`
	CounterpartyName *string   `json:"counterparty_name"`
	Description      *string   `json:"description"`
}

type MovementSettlement struct {
	ID           uuid.UUID `json:"id"`
	ObligationID uuid.UUID `json:"obligation_id"`
	Amount       float64   `json:"amount"`
}

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

type MovementTotals struct {
	Inflow  float64 `json:"inflow"`
	Outflow float64 `json:"outflow"`
	Net     float64 `json:"net"`
}

type MovementList struct {
	Items   []Movement     `json:"items"`
	Totals  MovementTotals `json:"totals"`
	Total   int64          `json:"total"`
	Offset  int            `json:"offset"`
	HasMore bool           `json:"has_more"`
}

type SettleResult struct {
	Movement    Movement     `json:"movement"`
	Obligations []Obligation `json:"obligations"`
	Warnings    []string     `json:"warnings"`
}
