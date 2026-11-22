package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

type ServicesHandler struct {
	db *gorm.DB
}

func NewServicesHandler(db *gorm.DB) *ServicesHandler { return &ServicesHandler{db: db} }

func (h *ServicesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /services", h.list)
	mux.HandleFunc("POST /services", h.create)
	mux.HandleFunc("GET /services/{id}", h.get)
	mux.HandleFunc("PUT /services/{id}", h.update)
	mux.HandleFunc("DELETE /services/{id}", h.delete)
}

type serviceRequest struct {
	Name           string   `json:"name"`
	Unit           *string  `json:"unit"`
	ReferencePrice *float64 `json:"reference_price"`
	ReferenceCost  *float64 `json:"reference_cost"`
	Notes          *string  `json:"notes"`
}

const duplicateServiceMsg = "já existe um serviço com esse nome"

const foldSQL = `translate(lower(%s), 'áàâãäéèêëíìîïóòôõöúùûüç', 'aaaaaeeeeiiiiooooouuuuc')`

func (r serviceRequest) validated() (*models.Service, error) {
	name, msg := normalizeName("name", r.Name)
	if msg != "" {
		return nil, errStatus(http.StatusBadRequest, msg)
	}
	if r.ReferencePrice != nil && (*r.ReferencePrice < 0 || *r.ReferencePrice > 9999999999.99) {
		return nil, errStatus(http.StatusBadRequest, "campo 'reference_price' deve ser >= 0")
	}
	if r.ReferenceCost != nil && (*r.ReferenceCost < 0 || *r.ReferenceCost > 9999999999.99) {
		return nil, errStatus(http.StatusBadRequest, "campo 'reference_cost' deve ser >= 0")
	}
	unit := nullIfBlank(r.Unit)
	if unit != nil && len([]rune(*unit)) > 50 {
		return nil, errStatus(http.StatusBadRequest, "campo 'unit' deve ter no máximo 50 caracteres")
	}
	return &models.Service{
		Name: name, Unit: unit, ReferencePrice: r.ReferencePrice, ReferenceCost: r.ReferenceCost, Notes: nullIfBlank(r.Notes),
	}, nil
}

func (h *ServicesHandler) nameTaken(db *gorm.DB, name string, exceptID *uuid.UUID) (bool, error) {
	q := db.Model(&models.Service{}).Where("lower(name) = lower(?)", name)
	if exceptID != nil {
		q = q.Where("id <> ?", *exceptID)
	}
	var n int64
	err := q.Count(&n).Error
	return n > 0, err
}

func (h *ServicesHandler) list(w http.ResponseWriter, r *http.Request) {
	q := h.db.WithContext(r.Context()).Model(&models.Service{})
	if term := strings.TrimSpace(r.URL.Query().Get("q")); term != "" {
		col := strings.Replace(foldSQL, "%s", "name", 1)
		q = q.Where("strpos("+col+", "+strings.Replace(foldSQL, "%s", "?", 1)+") > 0", term)
	}
	for _, f := range []struct {
		param string
		apply func(n int)
	}{
		{"limit", func(n int) { q = q.Limit(n) }},
		{"offset", func(n int) { q = q.Offset(n) }},
	} {
		raw := r.URL.Query().Get(f.param)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || (f.param == "limit" && n == 0) {
			models.WriteError(w, http.StatusBadRequest, f.param+" inválido")
			return
		}
		f.apply(n)
	}
	out := []models.Service{}
	if err := q.Order("lower(name) asc, id asc").Find(&out).Error; err != nil {
		log.Printf("ERROR ListServices: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *ServicesHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var s models.Service
	if err := h.db.WithContext(r.Context()).First(&s, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "serviço não encontrado")
			return
		}
		log.Printf("ERROR GetService: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *ServicesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req serviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	s, err := req.validated()
	if err != nil {
		writeTxError(w, "CreateService", err)
		return
	}
	db := h.db.WithContext(r.Context())
	taken, err := h.nameTaken(db, s.Name, nil)
	if err != nil {
		writeTxError(w, "CreateService check", err)
		return
	}
	if taken {
		models.WriteError(w, http.StatusConflict, duplicateServiceMsg)
		return
	}
	if err := db.Create(s).Error; err != nil {
		if isUniqueViolation(err) {
			models.WriteError(w, http.StatusConflict, duplicateServiceMsg)
			return
		}
		writeTxError(w, "CreateService", err)
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

func (h *ServicesHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var req serviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	in, err := req.validated()
	if err != nil {
		writeTxError(w, "UpdateService", err)
		return
	}
	db := h.db.WithContext(r.Context())
	var s models.Service
	if err := db.First(&s, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "serviço não encontrado")
			return
		}
		writeTxError(w, "UpdateService find", err)
		return
	}
	taken, err := h.nameTaken(db, in.Name, &s.ID)
	if err != nil {
		writeTxError(w, "UpdateService check", err)
		return
	}
	if taken {
		models.WriteError(w, http.StatusConflict, duplicateServiceMsg)
		return
	}
	s.Name, s.Unit, s.ReferencePrice, s.ReferenceCost, s.Notes = in.Name, in.Unit, in.ReferencePrice, in.ReferenceCost, in.Notes
	if err := db.Save(&s).Error; err != nil {
		if isUniqueViolation(err) {
			models.WriteError(w, http.StatusConflict, duplicateServiceMsg)
			return
		}
		writeTxError(w, "UpdateService save", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *ServicesHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}
	res := h.db.WithContext(r.Context()).Delete(&models.Service{}, "id = ?", id)
	if res.Error != nil {
		writeTxError(w, "SoftDeleteService", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		models.WriteError(w, http.StatusNotFound, "serviço não encontrado")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
