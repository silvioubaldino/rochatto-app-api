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

type ClientesHandler struct {
	db *gorm.DB
}

func NewClientesHandler(db *gorm.DB) *ClientesHandler {
	return &ClientesHandler{db: db}
}

func (h *ClientesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /clientes/search", h.search)
	mux.HandleFunc("GET /clientes", h.list)
	mux.HandleFunc("POST /clientes", h.create)
	mux.HandleFunc("GET /clientes/{id}", h.get)
	mux.HandleFunc("PUT /clientes/{id}", h.update)
	mux.HandleFunc("DELETE /clientes/{id}", h.delete)
}

type clienteRequest struct {
	Nome        string  `json:"nome"`
	Telefone    *string `json:"telefone"`
	Email       *string `json:"email"`
	Endereco    *string `json:"endereco"`
	Cidade      *string `json:"cidade"`
	Observacoes *string `json:"observacoes"`
}

func (h *ClientesHandler) list(w http.ResponseWriter, r *http.Request) {
	var clientes []models.Cliente
	if err := h.db.WithContext(r.Context()).Order("nome asc").Find(&clientes).Error; err != nil {
		log.Printf("ERROR ListClientes: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if clientes == nil {
		clientes = []models.Cliente{}
	}
	writeJSON(w, http.StatusOK, clientes)
}

func (h *ClientesHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var c models.Cliente
	if err := h.db.WithContext(r.Context()).First(&c, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR GetCliente: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, c)
}

func (h *ClientesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req clienteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	nome, ok := validateNome(w, req.Nome)
	if !ok {
		return
	}

	c := models.Cliente{
		Nome:        nome,
		Telefone:    req.Telefone,
		Email:       req.Email,
		Endereco:    req.Endereco,
		Cidade:      req.Cidade,
		Observacoes: req.Observacoes,
	}

	if err := h.db.WithContext(r.Context()).Create(&c).Error; err != nil {
		log.Printf("ERROR CreateCliente: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusCreated, c)
}

func (h *ClientesHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req clienteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	nome, ok := validateNome(w, req.Nome)
	if !ok {
		return
	}

	var c models.Cliente
	if err := h.db.WithContext(r.Context()).First(&c, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR UpdateCliente find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	c.Nome = nome
	c.Telefone = req.Telefone
	c.Email = req.Email
	c.Endereco = req.Endereco
	c.Cidade = req.Cidade
	c.Observacoes = req.Observacoes

	if err := h.db.WithContext(r.Context()).Save(&c).Error; err != nil {
		log.Printf("ERROR UpdateCliente save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, c)
}

func (h *ClientesHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	if err := h.db.WithContext(r.Context()).Delete(&models.Cliente{}, "id = ?", id).Error; err != nil {
		log.Printf("ERROR SoftDeleteCliente: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ClientesHandler) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []models.Cliente{})
		return
	}

	var clientes []models.Cliente
	if err := h.db.WithContext(r.Context()).
		Where("nome ILIKE ?", "%"+q+"%").
		Order("nome asc").
		Limit(20).
		Find(&clientes).Error; err != nil {
		log.Printf("ERROR SearchClientes: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if clientes == nil {
		clientes = []models.Cliente{}
	}
	writeJSON(w, http.StatusOK, clientes)
}
