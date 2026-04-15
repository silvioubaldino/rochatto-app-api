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

type ProdutosHandler struct {
	db *gorm.DB
}

func NewProdutosHandler(db *gorm.DB) *ProdutosHandler {
	return &ProdutosHandler{db: db}
}

func (h *ProdutosHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /produtos/search", h.search)
	mux.HandleFunc("GET /produtos", h.list)
	mux.HandleFunc("POST /produtos", h.create)
	mux.HandleFunc("GET /produtos/{id}", h.get)
	mux.HandleFunc("PUT /produtos/{id}", h.update)
	mux.HandleFunc("DELETE /produtos/{id}", h.delete)
}

type produtoRequest struct {
	Nome            string   `json:"nome"`
	Descricao       *string  `json:"descricao"`
	PrecoReferencia *float64 `json:"preco_referencia"`
	CustoReferencia *float64 `json:"custo_referencia"`
	Unidade         *string  `json:"unidade"`
}

func (h *ProdutosHandler) list(w http.ResponseWriter, r *http.Request) {
	var produtos []models.Produto
	if err := h.db.WithContext(r.Context()).Order("nome asc").Find(&produtos).Error; err != nil {
		log.Printf("ERROR ListProdutos: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if produtos == nil {
		produtos = []models.Produto{}
	}
	writeJSON(w, http.StatusOK, produtos)
}

func (h *ProdutosHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var p models.Produto
	if err := h.db.WithContext(r.Context()).First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR GetProduto: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, p)
}

func (h *ProdutosHandler) create(w http.ResponseWriter, r *http.Request) {
	var req produtoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	nome, ok := validateNome(w, req.Nome)
	if !ok {
		return
	}
	if req.PrecoReferencia != nil && *req.PrecoReferencia < 0 {
		models.WriteError(w, http.StatusBadRequest, "campo 'preco_referencia' deve ser >= 0")
		return
	}
	if req.CustoReferencia != nil && *req.CustoReferencia < 0 {
		models.WriteError(w, http.StatusBadRequest, "campo 'custo_referencia' deve ser >= 0")
		return
	}

	p := models.Produto{
		Nome:            nome,
		Descricao:       req.Descricao,
		PrecoReferencia: req.PrecoReferencia,
		CustoReferencia: req.CustoReferencia,
		Unidade:         req.Unidade,
	}

	if err := h.db.WithContext(r.Context()).Create(&p).Error; err != nil {
		log.Printf("ERROR CreateProduto: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusCreated, p)
}

func (h *ProdutosHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req produtoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	nome, ok := validateNome(w, req.Nome)
	if !ok {
		return
	}
	if req.PrecoReferencia != nil && *req.PrecoReferencia < 0 {
		models.WriteError(w, http.StatusBadRequest, "campo 'preco_referencia' deve ser >= 0")
		return
	}
	if req.CustoReferencia != nil && *req.CustoReferencia < 0 {
		models.WriteError(w, http.StatusBadRequest, "campo 'custo_referencia' deve ser >= 0")
		return
	}

	var p models.Produto
	if err := h.db.WithContext(r.Context()).First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR UpdateProduto find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	p.Nome = nome
	p.Descricao = req.Descricao
	p.PrecoReferencia = req.PrecoReferencia
	p.CustoReferencia = req.CustoReferencia
	p.Unidade = req.Unidade

	if err := h.db.WithContext(r.Context()).Save(&p).Error; err != nil {
		log.Printf("ERROR UpdateProduto save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, p)
}

func (h *ProdutosHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	if err := h.db.WithContext(r.Context()).Delete(&models.Produto{}, "id = ?", id).Error; err != nil {
		log.Printf("ERROR SoftDeleteProduto: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ProdutosHandler) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []models.Produto{})
		return
	}

	var produtos []models.Produto
	if err := h.db.WithContext(r.Context()).
		Where("nome ILIKE ?", "%"+q+"%").
		Order("nome asc").
		Limit(20).
		Find(&produtos).Error; err != nil {
		log.Printf("ERROR SearchProdutos: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if produtos == nil {
		produtos = []models.Produto{}
	}
	writeJSON(w, http.StatusOK, produtos)
}
