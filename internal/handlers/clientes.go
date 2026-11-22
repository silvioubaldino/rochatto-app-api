package handlers

import (
	"context"
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
	Nome        string     `json:"nome"`
	Telefone    *string    `json:"telefone"`
	Email       *string    `json:"email"`
	Endereco    *string    `json:"endereco"`
	Cidade      *string    `json:"cidade"`
	Observacoes *string    `json:"observacoes"`
	LeadSource  *string    `json:"lead_source"`
	ReferrerID  *uuid.UUID `json:"referrer_id"`
}

var validLeadSources = map[string]bool{
	"REFERRAL":  true,
	"INSTAGRAM": true,
	"PARTNER":   true,
	"OTHER":     true,
}

// validateLeadSource normaliza lead_source (trim; "" → null) e aplica a regra: referrer_id é obrigatório se lead_source = PARTNER e proibido caso contrário (inclusive com lead_source null).
func validateLeadSource(req *clienteRequest) string {
	req.LeadSource = nullIfBlank(req.LeadSource)
	if req.LeadSource != nil && !validLeadSources[*req.LeadSource] {
		return "campo 'lead_source' inválido (REFERRAL, INSTAGRAM, PARTNER ou OTHER)"
	}
	isPartner := req.LeadSource != nil && *req.LeadSource == "PARTNER"
	if isPartner && req.ReferrerID == nil {
		return "campo 'referrer_id' é obrigatório quando lead_source = PARTNER"
	}
	if !isPartner && req.ReferrerID != nil {
		return "campo 'referrer_id' só pode ser preenchido quando lead_source = PARTNER"
	}
	return ""
}

func (h *ClientesHandler) baseQuery(ctx context.Context) *gorm.DB {
	return h.db.WithContext(ctx).
		Model(&models.Cliente{}).
		Select("clientes.*, ve.nome AS referrer_name").
		Joins("LEFT JOIN vendedores_externos ve ON ve.id = clientes.referrer_id")
}

func (h *ClientesHandler) fetch(ctx context.Context, id uuid.UUID) (models.Cliente, error) {
	var c models.Cliente
	err := h.baseQuery(ctx).Where("clientes.id = ?", id).Take(&c).Error
	return c, err
}

// Um referrer_id novo precisa apontar para um Partner existente e não excluído; manter o mesmo referrer_id de um Partner já excluído é permitido (o vínculo histórico é preservado).
func (h *ClientesHandler) checkReferrer(w http.ResponseWriter, ctx context.Context, req *clienteRequest, current *uuid.UUID) bool {
	if msg := validateLeadSource(req); msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return false
	}
	if req.ReferrerID == nil || (current != nil && *current == *req.ReferrerID) {
		return true
	}
	var p models.Partner
	if err := h.db.WithContext(ctx).First(&p, "id = ?", *req.ReferrerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusBadRequest, "referrer não encontrado")
			return false
		}
		log.Printf("ERROR checkReferrer: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return false
	}
	return true
}

func (h *ClientesHandler) list(w http.ResponseWriter, r *http.Request) {
	query := h.baseQuery(r.Context())
	if ls := r.URL.Query().Get("lead_source"); ls != "" {
		if !validLeadSources[ls] {
			models.WriteError(w, http.StatusBadRequest, "lead_source inválido")
			return
		}
		query = query.Where("clientes.lead_source = ?", ls)
	}
	if ref := r.URL.Query().Get("referrer_id"); ref != "" {
		refID, err := uuid.Parse(ref)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "referrer_id inválido")
			return
		}
		query = query.Where("clientes.referrer_id = ?", refID)
	}

	var clientes []models.Cliente
	if err := query.Order("clientes.nome asc").Find(&clientes).Error; err != nil {
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

	c, err := h.fetch(r.Context(), id)
	if err != nil {
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
	if !h.checkReferrer(w, r.Context(), &req, nil) {
		return
	}

	c := models.Cliente{
		Nome:        nome,
		Telefone:    req.Telefone,
		Email:       req.Email,
		Endereco:    req.Endereco,
		Cidade:      req.Cidade,
		Observacoes: req.Observacoes,
		LeadSource:  req.LeadSource,
		ReferrerID:  req.ReferrerID,
	}

	if err := h.db.WithContext(r.Context()).Create(&c).Error; err != nil {
		log.Printf("ERROR CreateCliente: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	h.respond(w, r.Context(), http.StatusCreated, c.ID)
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
	if !h.checkReferrer(w, r.Context(), &req, c.ReferrerID) {
		return
	}

	c.Nome = nome
	c.Telefone = req.Telefone
	c.Email = req.Email
	c.Endereco = req.Endereco
	c.Cidade = req.Cidade
	c.Observacoes = req.Observacoes
	c.LeadSource = req.LeadSource
	c.ReferrerID = req.ReferrerID

	if err := h.db.WithContext(r.Context()).Save(&c).Error; err != nil {
		log.Printf("ERROR UpdateCliente save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	h.respond(w, r.Context(), http.StatusOK, c.ID)
}

func (h *ClientesHandler) respond(w http.ResponseWriter, ctx context.Context, status int, id uuid.UUID) {
	c, err := h.fetch(ctx, id)
	if err != nil {
		log.Printf("ERROR fetch cliente after write: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, status, c)
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
	if err := h.baseQuery(r.Context()).
		Where("clientes.nome ILIKE ?", "%"+q+"%").
		Order("clientes.nome asc").
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
