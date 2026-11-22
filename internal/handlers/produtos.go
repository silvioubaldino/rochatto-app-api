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
	Nome              string       `json:"nome"`
	Descricao         *string      `json:"descricao"`
	PrecoReferencia   *float64     `json:"preco_referencia"`
	CustoReferencia   *float64     `json:"custo_referencia"`
	Unidade           *string      `json:"unidade"`
	CategoryID        *uuid.UUID   `json:"category_id"`
	DefaultSupplierID optionalUUID `json:"default_supplier_id"`
}

func (h *ProdutosHandler) baseQuery(ctx context.Context) *gorm.DB {
	return h.db.WithContext(ctx).
		Model(&models.Produto{}).
		Select("produtos.*, pc.name AS category_name, fs.nome AS default_supplier_name").
		Joins("LEFT JOIN product_categories pc ON pc.id = produtos.category_id").
		Joins("LEFT JOIN fornecedores fs ON fs.id = produtos.default_supplier_id")
}

func (h *ProdutosHandler) fetch(ctx context.Context, id uuid.UUID) (models.Produto, error) {
	var p models.Produto
	err := h.baseQuery(ctx).Where("produtos.id = ?", id).Take(&p).Error
	return p, err
}

func (h *ProdutosHandler) respond(w http.ResponseWriter, ctx context.Context, status int, id uuid.UUID) {
	p, err := h.fetch(ctx, id)
	if err != nil {
		log.Printf("ERROR fetch produto after write: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, status, p)
}

// Uma categoria nova precisa existir e não estar excluída (400); manter a mesma categoria já excluída é permitido (o Product mantém o vínculo.
func (h *ProdutosHandler) checkCategory(w http.ResponseWriter, ctx context.Context, categoryID, current *uuid.UUID) bool {
	if categoryID == nil || (current != nil && *current == *categoryID) {
		return true
	}
	var c models.ProductCategory
	if err := h.db.WithContext(ctx).First(&c, "id = ?", *categoryID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusBadRequest, "categoria não encontrada")
			return false
		}
		log.Printf("ERROR checkCategory: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return false
	}
	return true
}

func (h *ProdutosHandler) checkDefaultSupplier(w http.ResponseWriter, ctx context.Context, id, current *uuid.UUID) bool {
	if id == nil || (current != nil && *current == *id) {
		return true
	}
	var n int64
	if err := h.db.WithContext(ctx).Model(&models.Fornecedor{}).Where("id = ?", *id).Count(&n).Error; err != nil {
		log.Printf("ERROR checkDefaultSupplier: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return false
	}
	if n == 0 {
		models.WriteError(w, http.StatusBadRequest, "fornecedor habitual não encontrado")
		return false
	}
	return true
}

func (h *ProdutosHandler) list(w http.ResponseWriter, r *http.Request) {
	query := h.baseQuery(r.Context())
	if cat := r.URL.Query().Get("category_id"); cat != "" {
		catID, err := uuid.Parse(cat)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "category_id inválido")
			return
		}
		query = query.Where("produtos.category_id = ?", catID)
	}

	var produtos []models.Produto
	if err := query.Order("produtos.nome asc").Find(&produtos).Error; err != nil {
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

	p, err := h.fetch(r.Context(), id)
	if err != nil {
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
	if !h.checkCategory(w, r.Context(), req.CategoryID, nil) {
		return
	}
	if !h.checkDefaultSupplier(w, r.Context(), req.DefaultSupplierID.Value, nil) {
		return
	}

	p := models.Produto{
		Nome:            nome,
		Descricao:       req.Descricao,
		PrecoReferencia: req.PrecoReferencia,
		CustoReferencia: req.CustoReferencia,
		Unidade:         req.Unidade,
		CategoryID:      req.CategoryID,

		DefaultSupplierID: req.DefaultSupplierID.Value,
	}

	if err := h.db.WithContext(r.Context()).Create(&p).Error; err != nil {
		log.Printf("ERROR CreateProduto: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	h.respond(w, r.Context(), http.StatusCreated, p.ID)
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
	if !h.checkCategory(w, r.Context(), req.CategoryID, p.CategoryID) {
		return
	}
	if req.DefaultSupplierID.Set {
		if !h.checkDefaultSupplier(w, r.Context(), req.DefaultSupplierID.Value, p.DefaultSupplierID) {
			return
		}
		p.DefaultSupplierID = req.DefaultSupplierID.Value
	}

	p.Nome = nome
	p.Descricao = req.Descricao
	p.PrecoReferencia = req.PrecoReferencia
	p.CustoReferencia = req.CustoReferencia
	p.Unidade = req.Unidade
	p.CategoryID = req.CategoryID

	if err := h.db.WithContext(r.Context()).Save(&p).Error; err != nil {
		log.Printf("ERROR UpdateProduto save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	h.respond(w, r.Context(), http.StatusOK, p.ID)
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
	if err := h.baseQuery(r.Context()).
		Where("produtos.nome ILIKE ?", "%"+q+"%").
		Order("produtos.nome asc").
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
