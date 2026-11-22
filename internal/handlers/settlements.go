package handlers

import (
	"net/http"

	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"gorm.io/gorm"
)

type SettlementsHandler struct {
	db *gorm.DB
}

func NewSettlementsHandler(db *gorm.DB) *SettlementsHandler { return &SettlementsHandler{db: db} }

func (h *SettlementsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /settlements", h.create)
	mux.HandleFunc("DELETE /settlements/{id}", h.delete)
}

type allocationRequest struct {
	ObligationID string   `json:"obligation_id"`
	Amount       *float64 `json:"amount"`
}

func (h *SettlementsHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MovementID  *string               `json:"movement_id"`
		Movement    *movementInputRequest `json:"movement"`
		Allocations []allocationRequest   `json:"allocations"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	in := obligations.SettleInput{}
	if req.MovementID != nil && *req.MovementID != "" {
		id, ok := parseUUIDParam(w, "movement_id", *req.MovementID)
		if !ok {
			return
		}
		in.MovementID = &id
	}
	if req.Movement != nil {
		mv, err := req.Movement.toInput()
		if err != nil {
			writeTxError(w, "CreateSettlement", err)
			return
		}
		in.Movement = mv
	}
	for _, a := range req.Allocations {
		in.Allocations = append(in.Allocations, obligations.AllocationInput{ObligationID: a.ObligationID, Amount: a.Amount})
	}
	res, err := obligations.Settle(h.db.WithContext(r.Context()), in)
	if err != nil {
		writeObligationsError(w, "CreateSettlement", err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (h *SettlementsHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, "id", r.PathValue("id"))
	if !ok {
		return
	}
	if err := obligations.DeleteSettlement(h.db.WithContext(r.Context()), id); err != nil {
		writeObligationsError(w, "DeleteSettlement", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
