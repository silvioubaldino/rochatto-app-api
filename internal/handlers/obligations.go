package handlers

// Contas a pagar/receber (AYD-007@context / SPEC-008): leitura, prazo, parcelas e quitação.
// Handlers finos: decodificam, validam o formato e delegam a internal/obligations.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/finance"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"gorm.io/gorm"
)

type ObligationsHandler struct {
	db *gorm.DB
}

func NewObligationsHandler(db *gorm.DB) *ObligationsHandler { return &ObligationsHandler{db: db} }

func (h *ObligationsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /obligations", h.list)
	mux.HandleFunc("GET /obligations/{id}", h.get)
	mux.HandleFunc("PATCH /obligations/{id}", h.patch)
	mux.HandleFunc("PUT /obligations/{id}/installments", h.putInstallments)
	mux.HandleFunc("POST /obligations/{id}/installments/generate", h.generateInstallments)
	mux.HandleFunc("POST /obligations/{id}/settle", h.settle)
}

// writeObligationsError responde 400/404/409 das regras de internal/obligations ou 500.
func writeObligationsError(w http.ResponseWriter, op string, err error) {
	if e := obligations.AsError(err); e != nil {
		models.WriteError(w, e.Status, e.Msg)
		return
	}
	writeTxError(w, op, err)
}

// parseDateField converte um "YYYY-MM-DD" opcional (nil/vazio → nil; inválido → 400).
func parseDateField(field string, s *string) (*time.Time, error) {
	return parseOptionalDate(field, s)
}

func parseUUIDParam(w http.ResponseWriter, name, raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, name+" inválido")
		return uuid.Nil, false
	}
	return id, true
}

func parseLimitOffset(q map[string][]string, maxLimit int) (int, int, string) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	limit, offset := 50, 0
	if s := get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			return 0, 0, "limit inválido (1 a " + strconv.Itoa(maxLimit) + ")"
		}
		limit = n
	}
	if s := get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return 0, 0, "offset inválido"
		}
		offset = n
	}
	return limit, offset, ""
}

func (h *ObligationsHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := obligations.ListFilter{}
	if v := q.Get("direction"); v != "" {
		if !obligations.Directions[v] {
			models.WriteError(w, http.StatusBadRequest, "direction inválido (PAYABLE | RECEIVABLE)")
			return
		}
		f.Direction = v
	}
	if v := q.Get("status"); v != "" {
		values, ok := parseEnumList(v, obligations.Statuses)
		if !ok {
			models.WriteError(w, http.StatusBadRequest, "status inválido (OPEN | PARTIAL | SETTLED)")
			return
		}
		f.Statuses = values
	}
	if v := q.Get("counterparty_type"); v != "" {
		if !obligations.CounterpartyType[v] {
			models.WriteError(w, http.StatusBadRequest, "counterparty_type inválido (SUPPLIER | PARTNER | CUSTOMER)")
			return
		}
		f.CounterpartyType = v
	}
	if v := q.Get("origin_type"); v != "" {
		if !obligations.OriginTypes[v] {
			models.WriteError(w, http.StatusBadRequest, "origin_type inválido (SALE | PURCHASE)")
			return
		}
		f.OriginType = v
	}
	for _, p := range []struct {
		name string
		dst  **uuid.UUID
	}{{"counterparty_id", &f.CounterpartyID}, {"origin_id", &f.OriginID}} {
		if v := q.Get(p.name); v != "" {
			id, ok := parseUUIDParam(w, p.name, v)
			if !ok {
				return
			}
			*p.dst = &id
		}
	}
	if v := q.Get("overdue"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "overdue inválido (true | false)")
			return
		}
		f.Overdue = &b
	}
	if v := q.Get("due_to"); v != "" {
		d, err := parseDate(v)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "due_to inválida (formato: YYYY-MM-DD)")
			return
		}
		f.DueTo = &d
	}
	var msg string
	if f.Limit, f.Offset, msg = parseLimitOffset(q, 200); msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	out, err := obligations.List(h.db.WithContext(r.Context()), f)
	if err != nil {
		writeObligationsError(w, "ListObligations", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *ObligationsHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, "id", r.PathValue("id"))
	if !ok {
		return
	}
	d, err := obligations.Get(h.db.WithContext(r.Context()), id)
	if err != nil {
		writeObligationsError(w, "GetObligation", err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// patch: PATCH /obligations/{id} { due_date?: date|null, notes?: string|null }. Só esses campos.
func (h *ObligationsHandler) patch(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, "id", r.PathValue("id"))
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return
	}
	var in obligations.PatchInput
	if v, present := raw["due_date"]; present {
		in.DueDate.Set = true
		if string(v) == "null" {
			in.DueDate.Null = true
		} else {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				models.WriteError(w, http.StatusBadRequest, "due_date inválida (formato: YYYY-MM-DD)")
				return
			}
			d, err := parseDate(s)
			if err != nil {
				models.WriteError(w, http.StatusBadRequest, "due_date inválida (formato: YYYY-MM-DD)")
				return
			}
			in.DueDate.Value = d
		}
	}
	if v, present := raw["notes"]; present {
		in.Notes.Set = true
		if string(v) == "null" {
			in.Notes.Null = true
		} else if err := json.Unmarshal(v, &in.Notes.Value); err != nil {
			models.WriteError(w, http.StatusBadRequest, "notes inválido")
			return
		}
	}
	d, err := obligations.Patch(h.db.WithContext(r.Context()), id, in)
	if err != nil {
		writeObligationsError(w, "PatchObligation", err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type installmentRequest struct {
	DueDate       *string `json:"due_date"`
	Amount        float64 `json:"amount"`
	PaymentMethod *string `json:"payment_method"`
	Notes         *string `json:"notes"`
}

// putInstallments: PUT /obligations/{id}/installments — substitui o plano inteiro.
func (h *ObligationsHandler) putInstallments(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, "id", r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		Installments []installmentRequest `json:"installments"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	plan := make([]obligations.InstallmentInput, 0, len(req.Installments))
	for i, in := range req.Installments {
		due, err := parseDateField("installments.due_date", in.DueDate)
		if err != nil || due == nil {
			models.WriteError(w, http.StatusBadRequest, "installments["+strconv.Itoa(i)+"].due_date é obrigatória (formato: YYYY-MM-DD)")
			return
		}
		method, err := finance.NormalizeMethod(in.PaymentMethod)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		plan = append(plan, obligations.InstallmentInput{DueDate: *due, Amount: in.Amount, PaymentMethod: method, Notes: nullIfBlank(in.Notes)})
	}
	d, err := obligations.ReplaceInstallments(h.db.WithContext(r.Context()), id, plan)
	if err != nil {
		writeObligationsError(w, "PutInstallments", err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// generateInstallments: POST /obligations/{id}/installments/generate.
func (h *ObligationsHandler) generateInstallments(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, "id", r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		Count          int      `json:"count"`
		FirstDueDate   *string  `json:"first_due_date"`
		IntervalMonths int      `json:"interval_months"`
		Amount         *float64 `json:"amount"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	first, err := parseDateField("first_due_date", req.FirstDueDate)
	if err != nil || first == nil {
		models.WriteError(w, http.StatusBadRequest, "campo 'first_due_date' é obrigatório (formato: YYYY-MM-DD)")
		return
	}
	d, err := obligations.Generate(h.db.WithContext(r.Context()), id, obligations.GenerateInput{
		Count: req.Count, FirstDueDate: *first, IntervalMonths: req.IntervalMonths, Amount: req.Amount,
	})
	if err != nil {
		writeObligationsError(w, "GenerateInstallments", err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type movementInputRequest struct {
	OccurredOn    *string `json:"occurred_on"`
	PaymentMethod *string `json:"payment_method"`
	Category      *string `json:"category"`
	Description   *string `json:"description"`
}

func (m movementInputRequest) toInput() (*obligations.MovementInput, error) {
	on, err := parseDateField("occurred_on", m.OccurredOn)
	if err != nil {
		return nil, err
	}
	method, err := finance.NormalizeMethod(m.PaymentMethod)
	if err != nil {
		return nil, errStatus(http.StatusBadRequest, err.Error())
	}
	var category *string
	if m.Category != nil && strings.TrimSpace(*m.Category) != "" {
		c := strings.TrimSpace(*m.Category)
		category = &c
	}
	return &obligations.MovementInput{OccurredOn: on, PaymentMethod: method, Category: category, Description: m.Description}, nil
}

// settle: POST /obligations/{id}/settle { amount?, movement } — atalho de 1 conta.
func (h *ObligationsHandler) settle(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, "id", r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		Amount   *float64              `json:"amount"`
		Movement *movementInputRequest `json:"movement"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Movement == nil {
		models.WriteError(w, http.StatusBadRequest, "campo 'movement' é obrigatório")
		return
	}
	mv, err := req.Movement.toInput()
	if err != nil {
		writeTxError(w, "SettleObligation", err)
		return
	}
	res, err := obligations.SettleOne(h.db.WithContext(r.Context()), id, req.Amount, *mv)
	if err != nil {
		writeObligationsError(w, "SettleObligation", err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}
