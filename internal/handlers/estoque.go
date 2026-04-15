package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

type EstoqueHandler struct {
	db *gorm.DB
}

func NewEstoqueHandler(db *gorm.DB) *EstoqueHandler {
	return &EstoqueHandler{db: db}
}

func (h *EstoqueHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /estoque/resumo", h.resumo)
	mux.HandleFunc("GET /estoque", h.list)
	mux.HandleFunc("POST /estoque", h.create)
	mux.HandleFunc("GET /estoque/{id}", h.get)
	mux.HandleFunc("PUT /estoque/{id}", h.update)
	mux.HandleFunc("DELETE /estoque/{id}", h.delete)
}

// --- Request / Response types ---

type estoqueRequest struct {
	ProdutoID         *uuid.UUID `json:"produto_id"`
	ProdutoNome       string     `json:"produto_nome"`
	FornecedorID      *uuid.UUID `json:"fornecedor_id"`
	QuantidadeEntrada float64    `json:"quantidade_entrada"`
	CustoUnit         float64    `json:"custo_unit"`
	DataEntrada       string     `json:"data_entrada"`
	Observacoes       *string    `json:"observacoes"`
}

type estoqueListResponse struct {
	ID                   uuid.UUID  `json:"id"`
	ProdutoID            *uuid.UUID `json:"produto_id"`
	ProdutoNome          string     `json:"produto_nome"`
	ProdutoCatalogoNome  *string    `json:"produto_catalogo_nome"`
	FornecedorID         *uuid.UUID `json:"fornecedor_id"`
	FornecedorNomeCatalogo *string  `json:"fornecedor_nome_catalogo"`
	QuantidadeEntrada    float64    `json:"quantidade_entrada"`
	CustoUnit            float64    `json:"custo_unit"`
	DataEntrada          string     `json:"data_entrada"`
	Observacoes          *string    `json:"observacoes"`
	CreatedAt            time.Time  `json:"created_at"`
}

type estoqueListRow struct {
	models.Estoque
	ProdutoCatalogoNome    *string `gorm:"column:produto_catalogo_nome"`
	FornecedorNomeCatalogo *string `gorm:"column:fornecedor_nome_catalogo"`
}

func (row estoqueListRow) toResponse() estoqueListResponse {
	return estoqueListResponse{
		ID:                     row.ID,
		ProdutoID:              row.ProdutoID,
		ProdutoNome:            row.ProdutoNome,
		ProdutoCatalogoNome:    row.ProdutoCatalogoNome,
		FornecedorID:           row.FornecedorID,
		FornecedorNomeCatalogo: row.FornecedorNomeCatalogo,
		QuantidadeEntrada:      row.QuantidadeEntrada,
		CustoUnit:              row.CustoUnit,
		DataEntrada:            row.DataEntrada.Format("2006-01-02"),
		Observacoes:            row.Observacoes,
		CreatedAt:              row.CreatedAt,
	}
}

type resumoEstoqueResponse struct {
	ProdutoNome        string     `json:"produto_nome"`
	ProdutoID          *uuid.UUID `json:"produto_id"`
	TotalEntradas      int64      `json:"total_entradas"`
	QuantidadeTotal    float64    `json:"quantidade_total"`
	CustoMedio         float64    `json:"custo_medio"`
	ValorTotalEstoque  float64    `json:"valor_total_estoque"`
	UltimaEntrada      string     `json:"ultima_entrada"`
}

type resumoEstoqueRow struct {
	ProdutoNome       string     `gorm:"column:produto_nome"`
	ProdutoID         *uuid.UUID `gorm:"column:produto_id"`
	TotalEntradas     int64      `gorm:"column:total_entradas"`
	QuantidadeTotal   float64    `gorm:"column:quantidade_total"`
	CustoMedio        float64    `gorm:"column:custo_medio"`
	ValorTotalEstoque float64    `gorm:"column:valor_total_estoque"`
	UltimaEntrada     time.Time  `gorm:"column:ultima_entrada"`
}

// --- Validation ---

func validateEstoqueFields(req estoqueRequest) string {
	if strings.TrimSpace(req.ProdutoNome) == "" {
		return "campo 'produto_nome' é obrigatório"
	}
	if req.QuantidadeEntrada <= 0 {
		return "campo 'quantidade_entrada' deve ser maior que zero"
	}
	if req.CustoUnit < 0 {
		return "campo 'custo_unit' não pode ser negativo"
	}
	if strings.TrimSpace(req.DataEntrada) == "" {
		return "campo 'data_entrada' é obrigatório"
	}
	return ""
}

// --- Handlers ---

func (h *EstoqueHandler) list(w http.ResponseWriter, r *http.Request) {
	query := h.db.WithContext(r.Context()).
		Table("estoque e").
		Select("e.*, p.nome AS produto_catalogo_nome, f.nome AS fornecedor_nome_catalogo").
		Joins("LEFT JOIN produtos p ON p.id = e.produto_id").
		Joins("LEFT JOIN fornecedores f ON f.id = e.fornecedor_id")

	if produtoID := r.URL.Query().Get("produto_id"); produtoID != "" {
		if _, err := uuid.Parse(produtoID); err != nil {
			models.WriteError(w, http.StatusBadRequest, "produto_id inválido")
			return
		}
		query = query.Where("e.produto_id = ?", produtoID)
	}
	if dataInicio := r.URL.Query().Get("data_inicio"); dataInicio != "" {
		query = query.Where("e.data_entrada >= ?", dataInicio)
	}
	if dataFim := r.URL.Query().Get("data_fim"); dataFim != "" {
		query = query.Where("e.data_entrada <= ?", dataFim)
	}

	query = query.Order("e.data_entrada DESC, e.created_at DESC")

	var rows []estoqueListRow
	if err := query.Scan(&rows).Error; err != nil {
		log.Printf("ERROR ListEstoque: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	resp := make([]estoqueListResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, row.toResponse())
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *EstoqueHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var e models.Estoque
	if err := h.db.WithContext(r.Context()).First(&e, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR GetEstoque: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, e)
}

func (h *EstoqueHandler) create(w http.ResponseWriter, r *http.Request) {
	var req estoqueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if errMsg := validateEstoqueFields(req); errMsg != "" {
		models.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}

	dataEntrada, err := parseDate(req.DataEntrada)
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "campo 'data_entrada' inválido (formato: YYYY-MM-DD)")
		return
	}

	e := models.Estoque{
		ProdutoID:         req.ProdutoID,
		ProdutoNome:       strings.TrimSpace(req.ProdutoNome),
		FornecedorID:      req.FornecedorID,
		QuantidadeEntrada: req.QuantidadeEntrada,
		CustoUnit:         req.CustoUnit,
		DataEntrada:       dataEntrada,
		Observacoes:       req.Observacoes,
	}

	if err := h.db.WithContext(r.Context()).Create(&e).Error; err != nil {
		log.Printf("ERROR CreateEstoque: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusCreated, e)
}

func (h *EstoqueHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req estoqueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if errMsg := validateEstoqueFields(req); errMsg != "" {
		models.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}

	dataEntrada, err := parseDate(req.DataEntrada)
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "campo 'data_entrada' inválido (formato: YYYY-MM-DD)")
		return
	}

	var e models.Estoque
	if err := h.db.WithContext(r.Context()).First(&e, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "registro não encontrado")
			return
		}
		log.Printf("ERROR UpdateEstoque find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	e.ProdutoID = req.ProdutoID
	e.ProdutoNome = strings.TrimSpace(req.ProdutoNome)
	e.FornecedorID = req.FornecedorID
	e.QuantidadeEntrada = req.QuantidadeEntrada
	e.CustoUnit = req.CustoUnit
	e.DataEntrada = dataEntrada
	e.Observacoes = req.Observacoes

	if err := h.db.WithContext(r.Context()).Save(&e).Error; err != nil {
		log.Printf("ERROR UpdateEstoque save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, e)
}

func (h *EstoqueHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	result := h.db.WithContext(r.Context()).Unscoped().Delete(&models.Estoque{}, "id = ?", id)
	if result.Error != nil {
		log.Printf("ERROR DeleteEstoque: %v", result.Error)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *EstoqueHandler) resumo(w http.ResponseWriter, r *http.Request) {
	query := h.db.WithContext(r.Context()).
		Table("estoque").
		Select(`
			produto_nome,
			produto_id,
			COUNT(*)                                AS total_entradas,
			SUM(quantidade_entrada)                 AS quantidade_total,
			AVG(custo_unit)                         AS custo_medio,
			SUM(quantidade_entrada * custo_unit)     AS valor_total_estoque,
			MAX(data_entrada)                       AS ultima_entrada
		`).
		Group("produto_nome, produto_id").
		Order("quantidade_total DESC")

	if produtoID := r.URL.Query().Get("produto_id"); produtoID != "" {
		if _, err := uuid.Parse(produtoID); err != nil {
			models.WriteError(w, http.StatusBadRequest, "produto_id inválido")
			return
		}
		query = query.Where("produto_id = ?", produtoID)
	}

	var rows []resumoEstoqueRow
	if err := query.Scan(&rows).Error; err != nil {
		log.Printf("ERROR GetResumoEstoque: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	resp := make([]resumoEstoqueResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, resumoEstoqueResponse{
			ProdutoNome:       row.ProdutoNome,
			ProdutoID:         row.ProdutoID,
			TotalEntradas:     row.TotalEntradas,
			QuantidadeTotal:   row.QuantidadeTotal,
			CustoMedio:        row.CustoMedio,
			ValorTotalEstoque: row.ValorTotalEstoque,
			UltimaEntrada:     row.UltimaEntrada.Format("2006-01-02"),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
