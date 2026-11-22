package obligations

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type movementRow struct {
	ID               uuid.UUID  `gorm:"column:id"`
	OccurredOn       time.Time  `gorm:"column:occurred_on"`
	Amount           float64    `gorm:"column:amount"`
	Category         string     `gorm:"column:category"`
	PaymentMethod    *string    `gorm:"column:payment_method"`
	CounterpartyName *string    `gorm:"column:counterparty_name"`
	Description      *string    `gorm:"column:description"`
	Unallocated      float64    `gorm:"column:unallocated"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	CorrectedAt      *time.Time `gorm:"column:corrected_at"`
}

const movementSelect = `SELECT m.id, m.occurred_on, m.amount, m.category::text AS category,
	m.payment_method::text AS payment_method,
	COALESCE(m.counterparty_name, (
		SELECT string_agg(DISTINCT vo.counterparty_name, ', ' ORDER BY vo.counterparty_name)
		FROM settlements s JOIN vw_obligations vo ON vo.id = s.obligation_id
		WHERE s.movement_id = m.id AND s.deleted_at IS NULL AND vo.counterparty_name IS NOT NULL
	)) AS counterparty_name,
	m.description, a.unallocated, m.created_at, m.corrected_at
	FROM financial_movements m JOIN vw_movement_allocation a ON a.movement_id = m.id`

func toMovements(db *gorm.DB, rows []movementRow) ([]Movement, error) {
	out := make([]Movement, 0, len(rows))
	byID := make(map[uuid.UUID]int, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for i, r := range rows {
		byID[r.ID] = i
		ids = append(ids, r.ID)
		out = append(out, Movement{
			ID: r.ID, OccurredOn: r.OccurredOn.Format(dateFmt), Amount: Round2(r.Amount), Category: r.Category,
			PaymentMethod: r.PaymentMethod, CounterpartyName: r.CounterpartyName, Description: r.Description,
			Settlements: []MovementSettlement{}, Unallocated: Round2(r.Unallocated), CreatedAt: r.CreatedAt, CorrectedAt: r.CorrectedAt,
		})
	}
	if len(ids) == 0 {
		return out, nil
	}
	var sets []struct {
		ID           uuid.UUID `gorm:"column:id"`
		MovementID   uuid.UUID `gorm:"column:movement_id"`
		ObligationID uuid.UUID `gorm:"column:obligation_id"`
		Amount       float64   `gorm:"column:amount"`
	}
	if err := db.Raw(`SELECT id, movement_id, obligation_id, amount FROM settlements
		WHERE movement_id IN ? AND deleted_at IS NULL ORDER BY created_at, id`, ids).Scan(&sets).Error; err != nil {
		return nil, err
	}
	for _, s := range sets {
		i := byID[s.MovementID]
		out[i].Settlements = append(out[i].Settlements, MovementSettlement{ID: s.ID, ObligationID: s.ObligationID, Amount: Round2(s.Amount)})
	}
	return out, nil
}

func GetMovement(db *gorm.DB, id uuid.UUID) (*Movement, error) {
	var rows []movementRow
	if err := db.Raw(movementSelect+` WHERE m.id = ? AND m.deleted_at IS NULL`, id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, newErr(404, "movimentação não encontrada")
	}
	ms, err := toMovements(db, rows)
	if err != nil {
		return nil, err
	}
	return &ms[0], nil
}

type MovementFilter struct {
	From, To         *time.Time
	Category         string
	Direction        string
	CounterpartyType string
	CounterpartyID   *uuid.UUID
	UnallocatedOnly  bool
	Limit, Offset    int
}

func (f MovementFilter) where() (string, []any) {
	conds := []string{"m.deleted_at IS NULL"}
	var args []any
	add := func(c string, a ...any) {
		conds = append(conds, c)
		args = append(args, a...)
	}
	if f.From != nil {
		add("m.occurred_on >= ?", *f.From)
	}
	if f.To != nil {
		add("m.occurred_on <= ?", *f.To)
	}
	if f.Category != "" {
		add("m.category::text = ?", f.Category)
	}
	switch f.Direction {
	case "IN":
		add("m.amount > 0")
	case "OUT":
		add("m.amount < 0")
	}
	if f.CounterpartyType != "" || f.CounterpartyID != nil {
		c := "EXISTS (SELECT 1 FROM settlements s JOIN obligations o ON o.id = s.obligation_id WHERE s.movement_id = m.id AND s.deleted_at IS NULL"
		if f.CounterpartyType != "" {
			c += " AND o.counterparty_type = ?"
			args = append(args, f.CounterpartyType)
		}
		if f.CounterpartyID != nil {
			c += " AND o.counterparty_id = ?"
			args = append(args, *f.CounterpartyID)
		}
		conds = append(conds, c+")")
	}
	if f.UnallocatedOnly {
		add("a.unallocated > 0")
	}
	return strings.Join(conds, " AND "), args
}

func ListMovements(db *gorm.DB, f MovementFilter) (*MovementList, error) {
	where, args := f.where()
	if f.Limit <= 0 {
		f.Limit = 50
	}
	var rows []movementRow
	if err := db.Raw(movementSelect+" WHERE "+where+" ORDER BY m.occurred_on DESC, m.created_at DESC, m.id LIMIT ? OFFSET ?",
		append(append([]any{}, args...), f.Limit, f.Offset)...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	var tot struct {
		Count   int64   `gorm:"column:count"`
		Inflow  float64 `gorm:"column:inflow"`
		Outflow float64 `gorm:"column:outflow"`
	}
	if err := db.Raw(`SELECT COUNT(*) AS count,
			COALESCE(SUM(m.amount) FILTER (WHERE m.amount > 0), 0) AS inflow,
			COALESCE(-SUM(m.amount) FILTER (WHERE m.amount < 0), 0) AS outflow
		FROM financial_movements m JOIN vw_movement_allocation a ON a.movement_id = m.id WHERE `+where, args...).Scan(&tot).Error; err != nil {
		return nil, err
	}
	items, err := toMovements(db, rows)
	if err != nil {
		return nil, err
	}
	return &MovementList{
		Items:   items,
		Totals:  MovementTotals{Inflow: Round2(tot.Inflow), Outflow: Round2(tot.Outflow), Net: Round2(tot.Inflow - tot.Outflow)},
		Total:   tot.Count,
		Offset:  f.Offset,
		HasMore: int64(f.Offset+len(rows)) < tot.Count,
	}, nil
}

type MovementWrite struct {
	OccurredOn       *time.Time
	Amount           float64
	Category         string
	PaymentMethod    *string
	CounterpartyName *string
	Description      *string
}

func (w MovementWrite) validate() error {
	if w.OccurredOn == nil {
		return newErr(400, "campo 'occurred_on' é obrigatório")
	}
	if err := CheckSign(w.Category, w.Amount); err != nil {
		return err
	}
	if w.PaymentMethod != nil && !paymentMethods[*w.PaymentMethod] {
		return newErr(400, "payment_method inválido (PIX | CASH | CARD | BOLETO | TRANSFER)")
	}
	return nil
}

func CreateMovement(db *gorm.DB, w MovementWrite) (*Movement, error) {
	if err := w.validate(); err != nil {
		return nil, err
	}
	var id string
	if err := db.Raw(`INSERT INTO financial_movements (occurred_on, amount, category, payment_method, counterparty_name, description)
		VALUES (?, ?, CAST(? AS movement_category), CAST(? AS payment_method), ?, ?) RETURNING id::text`,
		*w.OccurredOn, w.Amount, w.Category, w.PaymentMethod, trimPtr(w.CounterpartyName), trimPtr(w.Description)).Scan(&id).Error; err != nil {
		return nil, err
	}
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return GetMovement(db, uid)
}

func UpdateMovement(db *gorm.DB, id uuid.UUID, w MovementWrite) (*Movement, error) {
	if err := w.validate(); err != nil {
		return nil, err
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var cur struct {
			Amount    float64 `gorm:"column:amount"`
			Allocated float64 `gorm:"column:allocated"`
		}
		res := tx.Raw(`SELECT m.amount, a.allocated FROM financial_movements m
			JOIN vw_movement_allocation a ON a.movement_id = m.id
			WHERE m.id = ? FOR UPDATE OF m`, id).Scan(&cur)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return newErr(404, "movimentação não encontrada")
		}
		if Cents(cur.Allocated) > 0 {
			if (cur.Amount > 0) != (w.Amount > 0) {
				return newErr(409, "a correção inverteria o sinal de uma movimentação que já quita contas")
			}
			if Cents(cur.Allocated) > Cents(abs(w.Amount)) {
				return newErr(409, "valor menor que o total já quitado (%.2f)", Round2(cur.Allocated))
			}
		}
		return tx.Exec(`UPDATE financial_movements SET occurred_on = ?, amount = ?, category = CAST(? AS movement_category),
			payment_method = CAST(? AS payment_method), counterparty_name = ?, description = ?, corrected_at = NOW()
			WHERE id = ?`, *w.OccurredOn, w.Amount, w.Category, w.PaymentMethod, trimPtr(w.CounterpartyName), trimPtr(w.Description), id).Error
	})
	if err != nil {
		return nil, err
	}
	return GetMovement(db, id)
}

func DeleteMovement(db *gorm.DB, id uuid.UUID) error {
	return db.Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`UPDATE financial_movements SET deleted_at = NOW() WHERE id = ? AND deleted_at IS NULL`, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return newErr(404, "movimentação não encontrada")
		}
		return tx.Exec(`UPDATE settlements SET deleted_at = NOW() WHERE movement_id = ? AND deleted_at IS NULL`, id).Error
	})
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
