package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

var validPartnerTypes = map[string]bool{
	"ARCHITECT": true,
	"ENGINEER":  true,
	"BUILDER":   true,
	"OTHER":     true,
}

type PartnersHandler struct {
	db *gorm.DB
}

func NewPartnersHandler(db *gorm.DB) *PartnersHandler {
	return &PartnersHandler{db: db}
}

func (h *PartnersHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /partners", h.list)
	mux.HandleFunc("POST /partners", h.create)
	mux.HandleFunc("GET /partners/{id}", h.get)
	mux.HandleFunc("PUT /partners/{id}", h.update)
	mux.HandleFunc("DELETE /partners/{id}", h.delete)
}

type partnerRequest struct {
	Name  string  `json:"name"`
	Phone *string `json:"phone"`
	Type  string  `json:"type"`
	Notes *string `json:"notes"`
}

func validatePartnerRequest(req *partnerRequest) string {
	name, msg := normalizeName("name", req.Name)
	if msg != "" {
		return msg
	}
	req.Name = name
	req.Type = strings.TrimSpace(req.Type)
	if req.Type == "" {
		return "campo 'type' é obrigatório"
	}
	if !validPartnerTypes[req.Type] {
		return "campo 'type' inválido (ARCHITECT, ENGINEER, BUILDER ou OTHER)"
	}
	req.Phone = nullIfBlank(req.Phone)
	req.Notes = nullIfBlank(req.Notes)
	return ""
}

func (h *PartnersHandler) list(w http.ResponseWriter, r *http.Request) {
	query := h.db.WithContext(r.Context()).Model(&models.Partner{})

	if t := r.URL.Query().Get("type"); t != "" {
		if !validPartnerTypes[t] {
			models.WriteError(w, http.StatusBadRequest, "type inválido")
			return
		}
		query = query.Where("type = ?", t)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		query = query.Where("nome ILIKE ?", "%"+q+"%")
	}

	partners := []models.Partner{}
	if err := query.Order("nome asc").Find(&partners).Error; err != nil {
		log.Printf("ERROR ListPartners: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, partners)
}

func (h *PartnersHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var p models.Partner
	if err := h.db.WithContext(r.Context()).First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "partner não encontrado")
			return
		}
		log.Printf("ERROR GetPartner: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *PartnersHandler) create(w http.ResponseWriter, r *http.Request) {
	var req partnerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if msg := validatePartnerRequest(&req); msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	p := models.Partner{Name: req.Name, Phone: req.Phone, Type: req.Type, Notes: req.Notes}
	if err := h.db.WithContext(r.Context()).Create(&p).Error; err != nil {
		log.Printf("ERROR CreatePartner: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *PartnersHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req partnerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if msg := validatePartnerRequest(&req); msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}

	var p models.Partner
	if err := h.db.WithContext(r.Context()).First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "partner não encontrado")
			return
		}
		log.Printf("ERROR UpdatePartner find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	p.Name = req.Name
	p.Phone = req.Phone
	p.Type = req.Type
	p.Notes = req.Notes

	if err := h.db.WithContext(r.Context()).Save(&p).Error; err != nil {
		log.Printf("ERROR UpdatePartner save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *PartnersHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	res := h.db.WithContext(r.Context()).Delete(&models.Partner{}, "id = ?", id)
	if res.Error != nil {
		log.Printf("ERROR SoftDeletePartner: %v", res.Error)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if res.RowsAffected == 0 {
		models.WriteError(w, http.StatusNotFound, "partner não encontrado")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
