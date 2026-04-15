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

type FornecedoresHandler struct {
	db *gorm.DB
}

func NewFornecedoresHandler(db *gorm.DB) *FornecedoresHandler {
	return &FornecedoresHandler{db: db}
}

func (h *FornecedoresHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /fornecedores", h.list)
	mux.HandleFunc("POST /fornecedores", h.create)
	mux.HandleFunc("GET /fornecedores/{id}", h.get)
	mux.HandleFunc("PUT /fornecedores/{id}", h.update)
	mux.HandleFunc("DELETE /fornecedores/{id}", h.delete)
}

type fornecedorRequest struct {
	Nome        string  `json:"nome"`
	Telefone    *string `json:"telefone"`
	Cidade      *string `json:"cidade"`
	Observacoes *string `json:"observacoes"`
}

func (h *FornecedoresHandler) list(w http.ResponseWriter, r *http.Request) {
	var fornecedores []models.Fornecedor
	if err := h.db.WithContext(r.Context()).Order("nome asc").Find(&fornecedores).Error; err != nil {
		log.Printf("ERROR ListFornecedores: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if fornecedores == nil {
		fornecedores = []models.Fornecedor{}
	}
	writeJSON(w, http.StatusOK, fornecedores)
}

func (h *FornecedoresHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var f models.Fornecedor
	if err := h.db.WithContext(r.Context()).First(&f, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR GetFornecedor: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, f)
}

func (h *FornecedoresHandler) create(w http.ResponseWriter, r *http.Request) {
	var req fornecedorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	nome, ok := validateNome(w, req.Nome)
	if !ok {
		return
	}

	f := models.Fornecedor{
		Nome:        nome,
		Telefone:    req.Telefone,
		Cidade:      req.Cidade,
		Observacoes: req.Observacoes,
	}

	if err := h.db.WithContext(r.Context()).Create(&f).Error; err != nil {
		log.Printf("ERROR CreateFornecedor: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusCreated, f)
}

func (h *FornecedoresHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req fornecedorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	nome, ok := validateNome(w, req.Nome)
	if !ok {
		return
	}

	var f models.Fornecedor
	if err := h.db.WithContext(r.Context()).First(&f, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR UpdateFornecedor find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	f.Nome = nome
	f.Telefone = req.Telefone
	f.Cidade = req.Cidade
	f.Observacoes = req.Observacoes

	if err := h.db.WithContext(r.Context()).Save(&f).Error; err != nil {
		log.Printf("ERROR UpdateFornecedor save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, f)
}

func (h *FornecedoresHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	if err := h.db.WithContext(r.Context()).Delete(&models.Fornecedor{}, "id = ?", id).Error; err != nil {
		log.Printf("ERROR SoftDeleteFornecedor: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
