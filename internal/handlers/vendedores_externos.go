package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

type VendedoresExternosHandler struct {
	db *gorm.DB
}

func NewVendedoresExternosHandler(db *gorm.DB) *VendedoresExternosHandler {
	return &VendedoresExternosHandler{db: db}
}

func (h *VendedoresExternosHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /vendedores-externos", h.list)
	mux.HandleFunc("POST /vendedores-externos", h.create)
	mux.HandleFunc("GET /vendedores-externos/{id}", h.get)
	mux.HandleFunc("PUT /vendedores-externos/{id}", h.update)
	mux.HandleFunc("DELETE /vendedores-externos/{id}", h.delete)
}

type vendedorExternoRequest struct {
	Nome        string  `json:"nome"`
	Telefone    *string `json:"telefone"`
	Observacoes *string `json:"observacoes"`
}

func (h *VendedoresExternosHandler) list(w http.ResponseWriter, r *http.Request) {
	var vendedores []models.VendedorExterno
	if err := h.db.WithContext(r.Context()).Order("nome asc").Find(&vendedores).Error; err != nil {
		log.Printf("ERROR ListVendedoresExternos: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if vendedores == nil {
		vendedores = []models.VendedorExterno{}
	}
	writeJSON(w, http.StatusOK, vendedores)
}

func (h *VendedoresExternosHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var v models.VendedorExterno
	if err := h.db.WithContext(r.Context()).First(&v, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR GetVendedorExterno: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, v)
}

func (h *VendedoresExternosHandler) create(w http.ResponseWriter, r *http.Request) {
	var req vendedorExternoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	nome, ok := validateNome(w, req.Nome)
	if !ok {
		return
	}

	v := models.VendedorExterno{
		Nome:        nome,
		Telefone:    req.Telefone,
		Observacoes: req.Observacoes,
	}

	if err := h.db.WithContext(r.Context()).Create(&v).Error; err != nil {
		log.Printf("ERROR CreateVendedorExterno: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusCreated, v)
}

func (h *VendedoresExternosHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req vendedorExternoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	nome, ok := validateNome(w, req.Nome)
	if !ok {
		return
	}

	var v models.VendedorExterno
	if err := h.db.WithContext(r.Context()).First(&v, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR UpdateVendedorExterno find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	v.Nome = nome
	v.Telefone = req.Telefone
	v.Observacoes = req.Observacoes

	if err := h.db.WithContext(r.Context()).Save(&v).Error; err != nil {
		log.Printf("ERROR UpdateVendedorExterno save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, v)
}

func (h *VendedoresExternosHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	if err := h.db.WithContext(r.Context()).Delete(&models.VendedorExterno{}, "id = ?", id).Error; err != nil {
		log.Printf("ERROR SoftDeleteVendedorExterno: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
