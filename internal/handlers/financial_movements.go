package handlers

import (
	"net/http"
	"strings"

	"github.com/silvioubaldino/sales-backend/internal/finance"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"gorm.io/gorm"
)

type FinancialMovementsHandler struct {
	db *gorm.DB
}

func NewFinancialMovementsHandler(db *gorm.DB) *FinancialMovementsHandler {
	return &FinancialMovementsHandler{db: db}
}

func (h *FinancialMovementsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /financial-movements", h.list)
	mux.HandleFunc("POST /financial-movements", h.create)
	mux.HandleFunc("PUT /financial-movements/{id}", h.update)
	mux.HandleFunc("DELETE /financial-movements/{id}", h.delete)
}

func (h *FinancialMovementsHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := obligations.MovementFilter{}
	if v := q.Get("from"); v != "" {
		d, err := parseDate(v)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "from inválida (formato: YYYY-MM-DD)")
			return
		}
		f.From = &d
	}
	if v := q.Get("to"); v != "" {
		d, err := parseDate(v)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "to inválida (formato: YYYY-MM-DD)")
			return
		}
		f.To = &d
	}
	if v := q.Get("category"); v != "" {
		if !obligations.Categories[v] {
			models.WriteError(w, http.StatusBadRequest, "category inválida")
			return
		}
		f.Category = v
	}
	if v := q.Get("direction"); v != "" {
		if v != "IN" && v != "OUT" {
			models.WriteError(w, http.StatusBadRequest, "direction inválido (IN | OUT)")
			return
		}
		f.Direction = v
	}
	if v := q.Get("counterparty_type"); v != "" {
		if !obligations.CounterpartyType[v] {
			models.WriteError(w, http.StatusBadRequest, "counterparty_type inválido (SUPPLIER | PARTNER | CUSTOMER)")
			return
		}
		f.CounterpartyType = v
	}
	if v := q.Get("counterparty_id"); v != "" {
		id, ok := parseUUIDParam(w, "counterparty_id", v)
		if !ok {
			return
		}
		f.CounterpartyID = &id
	}
	f.UnallocatedOnly = q.Get("unallocated_only") == "true"
	var msg string
	if f.Limit, f.Offset, msg = parseLimitOffset(q, 200); msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	out, err := obligations.ListMovements(h.db.WithContext(r.Context()), f)
	if err != nil {
		writeObligationsError(w, "ListFinancialMovements", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type movementRequest struct {
	OccurredOn       *string `json:"occurred_on"`
	Amount           float64 `json:"amount"`
	Category         string  `json:"category"`
	PaymentMethod    *string `json:"payment_method"`
	CounterpartyName *string `json:"counterparty_name"`
	Description      *string `json:"description"`
}

func (m movementRequest) toWrite() (obligations.MovementWrite, error) {
	on, err := parseDateField("occurred_on", m.OccurredOn)
	if err != nil {
		return obligations.MovementWrite{}, err
	}
	method, err := finance.NormalizeMethod(m.PaymentMethod)
	if err != nil {
		return obligations.MovementWrite{}, errStatus(http.StatusBadRequest, err.Error())
	}
	return obligations.MovementWrite{
		OccurredOn: on, Amount: m.Amount, Category: strings.TrimSpace(m.Category), PaymentMethod: method,
		CounterpartyName: m.CounterpartyName, Description: m.Description,
	}, nil
}

func (h *FinancialMovementsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req movementRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toWrite()
	if err != nil {
		writeTxError(w, "CreateFinancialMovement", err)
		return
	}
	mv, err := obligations.CreateMovement(h.db.WithContext(r.Context()), in)
	if err != nil {
		writeObligationsError(w, "CreateFinancialMovement", err)
		return
	}
	writeJSON(w, http.StatusCreated, mv)
}

func (h *FinancialMovementsHandler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, "id", r.PathValue("id"))
	if !ok {
		return
	}
	var req movementRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toWrite()
	if err != nil {
		writeTxError(w, "UpdateFinancialMovement", err)
		return
	}
	mv, err := obligations.UpdateMovement(h.db.WithContext(r.Context()), id, in)
	if err != nil {
		writeObligationsError(w, "UpdateFinancialMovement", err)
		return
	}
	writeJSON(w, http.StatusOK, mv)
}

func (h *FinancialMovementsHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, "id", r.PathValue("id"))
	if !ok {
		return
	}
	if err := obligations.DeleteMovement(h.db.WithContext(r.Context()), id); err != nil {
		writeObligationsError(w, "DeleteFinancialMovement", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
