package obligations

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/finance"
	"gorm.io/gorm"
)

var paymentMethods = finance.PaymentMethods

type obligationRow struct {
	ID                  uuid.UUID  `gorm:"column:id"`
	Direction           string     `gorm:"column:direction"`
	OriginType          string     `gorm:"column:origin_type"`
	OriginID            uuid.UUID  `gorm:"column:origin_id"`
	OriginLabel         string     `gorm:"column:origin_label"`
	CounterpartyType    *string    `gorm:"column:counterparty_type"`
	CounterpartyID      *uuid.UUID `gorm:"column:counterparty_id"`
	CounterpartyName    *string    `gorm:"column:counterparty_name"`
	SaleCanceled        *bool      `gorm:"column:sale_canceled"`
	Amount              float64    `gorm:"column:amount"`
	Settled             float64    `gorm:"column:settled"`
	Open                float64    `gorm:"column:open"`
	Status              string     `gorm:"column:status"`
	DueDate             *time.Time `gorm:"column:due_date"`
	Overdue             bool       `gorm:"column:overdue"`
	NextInstallmentDue  *time.Time `gorm:"column:next_installment_due"`
	NextInstallmentOpen *float64   `gorm:"column:next_installment_open"`
	Notes               *string    `gorm:"column:notes"`
}

func (r obligationRow) toObligation() Obligation {
	o := Obligation{
		ID:        r.ID,
		Direction: r.Direction,
		Origin:    Origin{Type: r.OriginType, ID: r.OriginID, Label: r.OriginLabel},
		Amount:    Round2(r.Amount),
		Settled:   Round2(r.Settled),
		Open:      Round2(r.Open),
		Status:    r.Status,
		DueDate:   fmtDate(r.DueDate),
		Overdue:   r.Overdue,
		Warnings:  []string{},
		Notes:     r.Notes,
	}
	if r.CounterpartyType != nil && r.CounterpartyID != nil {
		name := ""
		if r.CounterpartyName != nil {
			name = *r.CounterpartyName
		}
		o.Counterparty = &Counterparty{Type: *r.CounterpartyType, ID: *r.CounterpartyID, Name: name}
	}
	if r.NextInstallmentDue != nil && r.NextInstallmentOpen != nil {
		o.NextInstallment = &NextInstallment{DueDate: r.NextInstallmentDue.Format(dateFmt), AmountOpen: Round2(*r.NextInstallmentOpen)}
	}
	if Cents(r.Settled) > Cents(r.Amount) {
		o.Warnings = append(o.Warnings, WarningOversettled)
	}
	return o
}

// ListFilter são os filtros de GET /obligations (já validados pelo handler).
type ListFilter struct {
	Direction        string
	Statuses         []string
	CounterpartyType string
	CounterpartyID   *uuid.UUID
	OriginType       string
	OriginID         *uuid.UUID
	Overdue          *bool
	DueTo            *time.Time
	// Filtros internos (não expostos em GET /obligations) — pendências do dia (AYD-011 C10):
	// NoPlan = sem parcela a vencer nem due_date; NoCounterparty = credor "a definir".
	NoPlan         bool
	NoCounterparty bool
	Limit          int
	Offset         int
}

func (f ListFilter) where() (string, []any) {
	conds := []string{"1=1"}
	var args []any
	add := func(cond string, a ...any) {
		conds = append(conds, cond)
		args = append(args, a...)
	}
	if f.Direction != "" {
		add("o.direction = ?", f.Direction)
	}
	if len(f.Statuses) > 0 {
		add("o.status IN ?", f.Statuses)
	}
	if f.CounterpartyType != "" {
		add("o.counterparty_type = ?", f.CounterpartyType)
	}
	if f.CounterpartyID != nil {
		add("o.counterparty_id = ?", *f.CounterpartyID)
	}
	if f.OriginType != "" {
		add("o.origin_type = ?", f.OriginType)
	}
	if f.OriginID != nil {
		add("o.origin_id = ?", *f.OriginID)
	}
	if f.Overdue != nil {
		if *f.Overdue {
			add("o.overdue")
		} else {
			add("NOT o.overdue")
		}
	}
	if f.DueTo != nil {
		add("o.effective_due <= ?", *f.DueTo)
	}
	if f.NoPlan {
		add("o.effective_due IS NULL")
	}
	if f.NoCounterparty {
		add("o.counterparty_id IS NULL")
	}
	return strings.Join(conds, " AND "), args
}

// List devolve as contas filtradas (mais próximas do vencimento primeiro) e os totais do
// conjunto filtrado.
func List(db *gorm.DB, f ListFilter) (*ObligationList, error) {
	where, args := f.where()
	if f.Limit <= 0 {
		f.Limit = 50
	}
	var rows []obligationRow
	if err := db.Raw("SELECT o.* FROM vw_obligations o WHERE "+where+
		" ORDER BY o.effective_due ASC NULLS LAST, o.created_at DESC, o.id LIMIT ? OFFSET ?",
		append(append([]any{}, args...), f.Limit, f.Offset)...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	var tot struct {
		Count   int64   `gorm:"column:count"`
		Amount  float64 `gorm:"column:amount"`
		Settled float64 `gorm:"column:settled"`
		Open    float64 `gorm:"column:open"`
	}
	if err := db.Raw("SELECT COUNT(*) AS count, COALESCE(SUM(o.amount),0) AS amount, COALESCE(SUM(o.settled),0) AS settled, "+
		"COALESCE(SUM(o.open),0) AS open FROM vw_obligations o WHERE "+where, args...).Scan(&tot).Error; err != nil {
		return nil, err
	}
	out := &ObligationList{
		Items:   make([]Obligation, 0, len(rows)),
		Totals:  Totals{Amount: Round2(tot.Amount), Settled: Round2(tot.Settled), Open: Round2(tot.Open)},
		Total:   tot.Count,
		Offset:  f.Offset,
		HasMore: int64(f.Offset+len(rows)) < tot.Count,
	}
	for _, r := range rows {
		out.Items = append(out.Items, r.toObligation())
	}
	return out, nil
}

func fetchRows(db *gorm.DB, ids []uuid.UUID) (map[uuid.UUID]obligationRow, error) {
	var rows []obligationRow
	if len(ids) > 0 {
		if err := db.Raw("SELECT * FROM vw_obligations WHERE id IN ?", ids).Scan(&rows).Error; err != nil {
			return nil, err
		}
	}
	m := make(map[uuid.UUID]obligationRow, len(rows))
	for _, r := range rows {
		m[r.ID] = r
	}
	return m, nil
}

// Summaries devolve as contas (resumo) na ordem dos ids pedidos; ausentes são omitidas.
func Summaries(db *gorm.DB, ids []uuid.UUID) ([]Obligation, error) {
	rows, err := fetchRows(db, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Obligation, 0, len(ids))
	for _, id := range ids {
		if r, ok := rows[id]; ok {
			out = append(out, r.toObligation())
		}
	}
	return out, nil
}

// Get devolve a conta com componentes, parcelas e quitações.
func Get(db *gorm.DB, id uuid.UUID) (*ObligationDetail, error) {
	rows, err := fetchRows(db, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	r, ok := rows[id]
	if !ok {
		return nil, newErr(404, "conta não encontrada")
	}
	d := &ObligationDetail{
		Obligation:   r.toObligation(),
		Components:   []Component{},
		Installments: []Installment{},
		Settlements:  []SettlementRef{},
	}
	var comps []struct {
		Type   string    `gorm:"column:type"`
		ID     uuid.UUID `gorm:"column:component_id"`
		Label  string    `gorm:"column:label"`
		Amount float64   `gorm:"column:amount"`
	}
	if err := db.Raw(`SELECT type, component_id, label, amount FROM vw_obligation_components
		WHERE origin_type = ? AND origin_id = ? AND direction = ? AND counterparty_id IS NOT DISTINCT FROM ?
		ORDER BY type`, r.OriginType, r.OriginID, r.Direction, r.CounterpartyID).Scan(&comps).Error; err != nil {
		return nil, err
	}
	for _, c := range comps {
		d.Components = append(d.Components, Component{Type: c.Type, ID: c.ID, Label: c.Label, Amount: Round2(c.Amount)})
	}
	var inst []struct {
		ID            uuid.UUID `gorm:"column:id"`
		DueDate       time.Time `gorm:"column:due_date"`
		Amount        float64   `gorm:"column:amount"`
		PaymentMethod *string   `gorm:"column:payment_method"`
		Notes         *string   `gorm:"column:notes"`
		Covered       float64   `gorm:"column:covered"`
		Status        string    `gorm:"column:status"`
		Overdue       bool      `gorm:"column:overdue"`
	}
	if err := db.Raw(`SELECT id, due_date, amount, payment_method::text AS payment_method, notes, covered, status, overdue
		FROM vw_installment_coverage WHERE obligation_id = ? ORDER BY due_date, created_at, id`, id).Scan(&inst).Error; err != nil {
		return nil, err
	}
	for _, i := range inst {
		d.Installments = append(d.Installments, Installment{
			ID: i.ID, DueDate: i.DueDate.Format(dateFmt), Amount: Round2(i.Amount), PaymentMethod: i.PaymentMethod,
			Notes: i.Notes, Covered: Round2(i.Covered), Status: i.Status, Overdue: i.Overdue,
		})
	}
	var sets []struct {
		ID               uuid.UUID `gorm:"column:id"`
		Amount           float64   `gorm:"column:amount"`
		MovementID       uuid.UUID `gorm:"column:movement_id"`
		OccurredOn       time.Time `gorm:"column:occurred_on"`
		MovementAmount   float64   `gorm:"column:movement_amount"`
		Category         string    `gorm:"column:category"`
		PaymentMethod    *string   `gorm:"column:payment_method"`
		CounterpartyName *string   `gorm:"column:counterparty_name"`
		Description      *string   `gorm:"column:description"`
	}
	if err := db.Raw(`SELECT s.id, s.amount, m.id AS movement_id, m.occurred_on, m.amount AS movement_amount,
			m.category::text AS category, m.payment_method::text AS payment_method, m.counterparty_name, m.description
		FROM settlements s JOIN financial_movements m ON m.id = s.movement_id
		WHERE s.obligation_id = ? AND s.deleted_at IS NULL AND m.deleted_at IS NULL
		ORDER BY m.occurred_on, s.created_at, s.id`, id).Scan(&sets).Error; err != nil {
		return nil, err
	}
	for _, s := range sets {
		d.Settlements = append(d.Settlements, SettlementRef{ID: s.ID, Amount: Round2(s.Amount), Movement: MovementSummary{
			ID: s.MovementID, OccurredOn: s.OccurredOn.Format(dateFmt), Amount: Round2(s.MovementAmount), Category: s.Category,
			PaymentMethod: s.PaymentMethod, CounterpartyName: s.CounterpartyName, Description: s.Description,
		}})
	}
	return d, nil
}

// Optional distingue campo omitido, null explícito e valor (PATCH parcial).
type Optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// PatchInput é o corpo de PATCH /obligations/{id}.
type PatchInput struct {
	DueDate Optional[time.Time]
	Notes   Optional[string]
}

// Patch altera só o prazo final e as notas da conta.
func Patch(db *gorm.DB, id uuid.UUID, in PatchInput) (*ObligationDetail, error) {
	upd := map[string]any{}
	if in.DueDate.Set {
		if in.DueDate.Null {
			upd["due_date"] = nil
		} else {
			upd["due_date"] = in.DueDate.Value
		}
	}
	if in.Notes.Set {
		if in.Notes.Null || strings.TrimSpace(in.Notes.Value) == "" {
			upd["notes"] = nil
		} else {
			upd["notes"] = strings.TrimSpace(in.Notes.Value)
		}
	}
	if len(upd) == 0 {
		return Get(db, id)
	}
	res := db.Table("obligations").Where("id = ? AND deleted_at IS NULL", id).Updates(upd)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, newErr(404, "conta não encontrada")
	}
	return Get(db, id)
}

// lockObligation trava a conta (FOR UPDATE) e devolve o seu estado; 404 se não existe.
func lockObligation(tx *gorm.DB, id uuid.UUID) (*obligationRow, error) {
	var locked []string
	if err := tx.Raw(`SELECT id::text FROM obligations WHERE id = ? AND deleted_at IS NULL FOR UPDATE`, id).Scan(&locked).Error; err != nil {
		return nil, err
	}
	if len(locked) == 0 {
		return nil, newErr(404, "conta não encontrada")
	}
	rows, err := fetchRows(tx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	r := rows[id]
	return &r, nil
}

// --- parcelas ---

// ReplaceInstallments substitui o plano inteiro (Σ ≤ amount da conta). As parcelas antigas
// ficam com deleted_at (soft-delete).
func ReplaceInstallments(db *gorm.DB, id uuid.UUID, plan []InstallmentInput) (*ObligationDetail, error) {
	err := db.Transaction(func(tx *gorm.DB) error {
		r, err := lockObligation(tx, id)
		if err != nil {
			return err
		}
		return replacePlan(tx, r, plan)
	})
	if err != nil {
		return nil, err
	}
	return Get(db, id)
}

func replacePlan(tx *gorm.DB, r *obligationRow, plan []InstallmentInput) error {
	if err := ValidatePlan(plan, r.Amount); err != nil {
		return err
	}
	if err := tx.Exec(`UPDATE installments SET deleted_at = NOW() WHERE obligation_id = ? AND deleted_at IS NULL`, r.ID).Error; err != nil {
		return err
	}
	for _, p := range plan {
		if err := tx.Exec(`INSERT INTO installments (obligation_id, due_date, amount, payment_method, notes)
			VALUES (?, ?, ?, CAST(? AS payment_method), ?)`, r.ID, p.DueDate, p.Amount, p.PaymentMethod, p.Notes).Error; err != nil {
			return err
		}
	}
	return nil
}

// GenerateInput é o corpo de POST /obligations/{id}/installments/generate.
type GenerateInput struct {
	Count          int
	FirstDueDate   time.Time
	IntervalMonths int      // 0 = 1
	Amount         *float64 // nil = o aberto da conta
}

// Generate gera parcelas iguais (centavos na última) e substitui o plano.
func Generate(db *gorm.DB, id uuid.UUID, in GenerateInput) (*ObligationDetail, error) {
	err := db.Transaction(func(tx *gorm.DB) error {
		r, err := lockObligation(tx, id)
		if err != nil {
			return err
		}
		amount := r.Open
		if in.Amount != nil {
			amount = *in.Amount
		}
		if Cents(amount) > Cents(r.Amount) {
			return newErr(400, "valor a parcelar maior que o valor da conta")
		}
		interval := in.IntervalMonths
		if interval == 0 {
			interval = 1
		}
		plan, err := GenerateInstallments(in.Count, in.FirstDueDate, interval, amount)
		if err != nil {
			return err
		}
		return replacePlan(tx, r, plan)
	})
	if err != nil {
		return nil, err
	}
	return Get(db, id)
}

// --- quitação ---

// MovementInput é a movimentação criada junto com a quitação (POST /settlements).
type MovementInput struct {
	OccurredOn    *time.Time
	PaymentMethod *string
	Category      *string
	Description   *string
}

// SettleInput é o corpo de POST /settlements.
type SettleInput struct {
	MovementID  *uuid.UUID
	Movement    *MovementInput
	Allocations []AllocationInput
}

// Settle paga/recebe: uma movimentação (nova ou já lançada) quita N contas. Transação única,
// com as contas e a movimentação travadas (FOR UPDATE) — duas quitações simultâneas não passam
// do aberto.
func Settle(db *gorm.DB, in SettleInput) (*SettleResult, error) {
	if in.MovementID == nil && in.Movement == nil {
		return nil, newErr(400, "informe movement_id ou movement")
	}
	if in.MovementID != nil && in.Movement != nil {
		return nil, newErr(400, "informe movement_id ou movement, não os dois")
	}
	if len(in.Allocations) == 0 {
		return nil, newErr(400, "allocations não pode ser vazio")
	}
	ids := make([]uuid.UUID, 0, len(in.Allocations))
	for _, a := range in.Allocations {
		id, err := uuid.Parse(a.ObligationID)
		if err != nil {
			return nil, newErr(400, "obligation_id inválido: %s", a.ObligationID)
		}
		ids = append(ids, id)
	}
	if in.Movement != nil {
		if in.Movement.OccurredOn == nil {
			return nil, newErr(400, "campo 'movement.occurred_on' é obrigatório")
		}
		if in.Movement.PaymentMethod != nil && !paymentMethods[*in.Movement.PaymentMethod] {
			return nil, newErr(400, "payment_method inválido (PIX | CASH | CARD | BOLETO | TRANSFER)")
		}
		if in.Movement.Category != nil && !Categories[*in.Movement.Category] {
			return nil, newErr(400, "category inválida")
		}
	}

	var movementID uuid.UUID
	var warnings []string
	err := db.Transaction(func(tx *gorm.DB) error {
		// Trava as contas em ordem estável (sem deadlock entre quitações concorrentes).
		sorted := append([]uuid.UUID(nil), ids...)
		sortUUIDs(sorted)
		var locked []string
		if err := tx.Raw(`SELECT id::text FROM obligations WHERE id IN ? AND deleted_at IS NULL ORDER BY id FOR UPDATE`, sorted).Scan(&locked).Error; err != nil {
			return err
		}
		have := map[string]bool{}
		for _, l := range locked {
			have[l] = true
		}
		for _, id := range ids {
			if !have[id.String()] {
				return newErr(404, "conta não encontrada: %s", id)
			}
		}
		rows, err := fetchRows(tx, ids)
		if err != nil {
			return err
		}
		states := make(map[string]AccountState, len(rows))
		for id, r := range rows {
			cp := ""
			if r.CounterpartyType != nil && r.CounterpartyID != nil {
				cp = *r.CounterpartyType + ":" + r.CounterpartyID.String()
			}
			states[id.String()] = AccountState{
				ID: id.String(), Direction: r.Direction, Open: r.Open, SaleCanceled: r.SaleCanceled != nil && *r.SaleCanceled, Counterparty: cp,
			}
		}
		// Normaliza os ids (maiúsculas/minúsculas) para casar com o mapa.
		req := make([]AllocationInput, len(in.Allocations))
		for i, a := range in.Allocations {
			req[i] = AllocationInput{ObligationID: ids[i].String(), Amount: a.Amount}
		}
		allocs, direction, warns, err := Allocate(req, states)
		if err != nil {
			return err
		}
		warnings = warns
		total := Sum(allocs)

		if in.MovementID != nil {
			var mv struct {
				Amount      float64 `gorm:"column:amount"`
				Unallocated float64 `gorm:"column:unallocated"`
			}
			if err := tx.Exec(`SELECT 1 FROM financial_movements WHERE id = ? AND deleted_at IS NULL FOR UPDATE`, *in.MovementID).Error; err != nil {
				return err
			}
			res := tx.Raw(`SELECT m.amount, a.unallocated FROM financial_movements m
				JOIN vw_movement_allocation a ON a.movement_id = m.id WHERE m.id = ?`, *in.MovementID).Scan(&mv)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return newErr(404, "movimentação não encontrada")
			}
			if (mv.Amount > 0) != (direction == Receivable) {
				return newErr(400, "a movimentação é de %s e as contas são %s", dirWord(mv.Amount), direction)
			}
			if Cents(total) > Cents(mv.Unallocated) {
				return newErr(409, "valor acima do saldo livre da movimentação")
			}
			movementID = *in.MovementID
		} else {
			category := ""
			if in.Movement.Category != nil {
				category = *in.Movement.Category
			} else {
				kinds, err := accountKinds(tx, ids, rows)
				if err != nil {
					return err
				}
				category = DefaultCategory(kinds)
			}
			amount := SignFor(direction) * total
			if err := CheckSign(category, amount); err != nil {
				return err
			}
			var newID string
			if err := tx.Raw(`INSERT INTO financial_movements (occurred_on, amount, category, payment_method, description)
				VALUES (?, ?, CAST(? AS movement_category), CAST(? AS payment_method), ?) RETURNING id::text`,
				*in.Movement.OccurredOn, amount, category, in.Movement.PaymentMethod, trimPtr(in.Movement.Description)).Scan(&newID).Error; err != nil {
				return err
			}
			if movementID, err = uuid.Parse(newID); err != nil {
				return err
			}
		}
		for _, a := range allocs {
			if err := tx.Exec(`INSERT INTO settlements (obligation_id, movement_id, amount) VALUES (?, ?, ?)`,
				a.ObligationID, movementID, a.Amount).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	mv, err := GetMovement(db, movementID)
	if err != nil {
		return nil, err
	}
	obs, err := Summaries(db, ids)
	if err != nil {
		return nil, err
	}
	if warnings == nil {
		warnings = []string{}
	}
	for _, o := range obs { // OVERSETTLED etc. também sobem para a resposta
		for _, w := range o.Warnings {
			if !contains(warnings, w) {
				warnings = append(warnings, w)
			}
		}
	}
	return &SettleResult{Movement: *mv, Obligations: obs, Warnings: warnings}, nil
}

// SettleOne é o atalho de 1 conta: POST /obligations/{id}/settle { amount?, movement }.
func SettleOne(db *gorm.DB, id uuid.UUID, amount *float64, mv MovementInput) (*SettleResult, error) {
	return Settle(db, SettleInput{Movement: &mv, Allocations: []AllocationInput{{ObligationID: id.String(), Amount: amount}}})
}

// DeleteSettlement desfaz a quitação (soft-delete); a movimentação permanece.
func DeleteSettlement(db *gorm.DB, id uuid.UUID) error {
	res := db.Exec(`UPDATE settlements SET deleted_at = NOW() WHERE id = ? AND deleted_at IS NULL`, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return newErr(404, "quitação não encontrada")
	}
	return nil
}

// accountKinds descreve as contas (direção, origem, tipos de componente) para a categoria padrão.
func accountKinds(tx *gorm.DB, ids []uuid.UUID, rows map[uuid.UUID]obligationRow) ([]AccountKind, error) {
	var comps []struct {
		ObligationID uuid.UUID `gorm:"column:obligation_id"`
		Type         string    `gorm:"column:type"`
	}
	if err := tx.Raw(`SELECT o.id AS obligation_id,
			CASE WHEN c.type = 'SALE_COST' THEN 'SALE_COST_' || sc.kind::text ELSE c.type END AS type
		FROM vw_obligations o
		JOIN vw_obligation_components c ON c.origin_type = o.origin_type AND c.origin_id = o.origin_id
		  AND c.direction = o.direction AND c.counterparty_id IS NOT DISTINCT FROM o.counterparty_id
		LEFT JOIN sale_costs sc ON c.type = 'SALE_COST' AND sc.id = c.component_id
		WHERE o.id IN ?`, ids).Scan(&comps).Error; err != nil {
		return nil, err
	}
	types := map[uuid.UUID][]string{}
	for _, c := range comps {
		types[c.ObligationID] = append(types[c.ObligationID], c.Type)
	}
	out := make([]AccountKind, 0, len(ids))
	for _, id := range ids {
		r := rows[id]
		out = append(out, AccountKind{Direction: r.Direction, OriginType: r.OriginType, ComponentTypes: types[id]})
	}
	return out, nil
}

func dirWord(amount float64) string {
	if amount > 0 {
		return "entrada"
	}
	return "saída"
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

func sortUUIDs(ids []uuid.UUID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j].String() < ids[j-1].String(); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
