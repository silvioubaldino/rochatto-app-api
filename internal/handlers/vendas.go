package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

// --- Valid status_venda values ---

var validStatuses = map[string]bool{
	"A_ENTREGAR":       true,
	"A_ENTREGAR_DATA":  true,
	"A_RETIRAR":        true,
	"ENTREGUE_PARCIAL": true,
	"ENTREGUE":         true,
}

// --- Handler ---

type VendasHandler struct {
	db *gorm.DB
}

func NewVendasHandler(db *gorm.DB) *VendasHandler {
	return &VendasHandler{db: db}
}

func (h *VendasHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /vendas", h.list)
	mux.HandleFunc("POST /vendas", h.create)
	mux.HandleFunc("GET /vendas/{id}", h.get)
	mux.HandleFunc("PUT /vendas/{id}", h.update)
	mux.HandleFunc("DELETE /vendas/{id}", h.delete)

	mux.HandleFunc("GET /vendas/{id}/itens", h.listItens)
	mux.HandleFunc("POST /vendas/{id}/itens", h.createItem)
	mux.HandleFunc("PUT /vendas/{id}/itens/{itemId}", h.updateItem)
	mux.HandleFunc("DELETE /vendas/{id}/itens/{itemId}", h.deleteItem)
}

// --- Request types ---

type vendaCreateRequest struct {
	ClienteID           uuid.UUID     `json:"cliente_id"`
	VendedorExternoID   *uuid.UUID    `json:"vendedor_externo_id"`
	Status              string        `json:"status"`
	DataVenda           string        `json:"data_venda"`
	DataEntregaPrevista *string       `json:"data_entrega_prevista"`
	FreteValor          float64       `json:"frete_valor"`
	FretePago           float64       `json:"frete_pago"`
	ComissaoValor       float64       `json:"comissao_valor"`
	ComissaoPaga        bool          `json:"comissao_paga"`
	Observacoes         *string       `json:"observacoes"`
	Itens               []itemRequest `json:"itens"`
}

type vendaUpdateRequest struct {
	VendedorExternoID   *uuid.UUID `json:"vendedor_externo_id"`
	Status              string     `json:"status"`
	DataEntregaPrevista *string    `json:"data_entrega_prevista"`
	FreteValor          float64    `json:"frete_valor"`
	FretePago           float64    `json:"frete_pago"`
	ComissaoValor       float64    `json:"comissao_valor"`
	ComissaoPaga        bool       `json:"comissao_paga"`
	Observacoes         *string    `json:"observacoes"`
}

type itemRequest struct {
	ProdutoID      *uuid.UUID `json:"produto_id"`
	ProdutoNome    string     `json:"produto_nome"`
	FornecedorID   *uuid.UUID `json:"fornecedor_id"`
	FornecedorNome *string    `json:"fornecedor_nome"`
	Quantidade     float64    `json:"quantidade"`
	PrecoVendaUnit float64    `json:"preco_venda_unit"`
	CustoUnit      float64    `json:"custo_unit"`
	PagoFornecedor float64    `json:"pago_fornecedor"`
}

// --- Response types ---

type vendaListResponse struct {
	ID                  uuid.UUID  `json:"id"`
	ClienteID           uuid.UUID  `json:"cliente_id"`
	ClienteNome         string     `json:"cliente_nome"`
	VendedorExternoID   *uuid.UUID `json:"vendedor_externo_id"`
	VendedorExternoNome *string    `json:"vendedor_externo_nome"`
	Status              string     `json:"status"`
	DataVenda           string     `json:"data_venda"`
	DataEntregaPrevista *string    `json:"data_entrega_prevista"`
	FreteValor          float64    `json:"frete_valor"`
	FretePago           float64    `json:"frete_pago"`
	ComissaoValor       float64    `json:"comissao_valor"`
	ComissaoPaga        bool       `json:"comissao_paga"`
	Observacoes         *string    `json:"observacoes"`
	TotalVenda          float64    `json:"total_venda"`
	TotalCusto          float64    `json:"total_custo"`
	TotalRecebido       float64    `json:"total_recebido"`
	AReceber            float64    `json:"a_receber"`
	Lucro               float64    `json:"lucro"`
	CreatedAt           time.Time  `json:"created_at"`
}

type vendaDetailResponse struct {
	vendaListResponse
	APagarFornecedor float64             `json:"a_pagar_fornecedor"`
	APagarFrete      float64             `json:"a_pagar_frete"`
	Itens            []itemResponse      `json:"itens"`
	Pagamentos       []pagamentoResponse `json:"pagamentos"`
}

type itemResponse struct {
	ID               uuid.UUID  `json:"id"`
	ProdutoID        *uuid.UUID `json:"produto_id"`
	ProdutoNome      string     `json:"produto_nome"`
	FornecedorID     *uuid.UUID `json:"fornecedor_id"`
	FornecedorNome   *string    `json:"fornecedor_nome"`
	Quantidade       float64    `json:"quantidade"`
	PrecoVendaUnit   float64    `json:"preco_venda_unit"`
	CustoUnit        float64    `json:"custo_unit"`
	PagoFornecedor   float64    `json:"pago_fornecedor"`
	TotalVenda       float64    `json:"total_venda"`
	TotalCusto       float64    `json:"total_custo"`
	APagarFornecedor float64    `json:"a_pagar_fornecedor"`
	CreatedAt        time.Time  `json:"created_at"`
}

// --- Internal row type for scanning vw_vendas_resumo ---

type vendaResumoRow struct {
	ID                  uuid.UUID  `gorm:"column:id"`
	ClienteID           uuid.UUID  `gorm:"column:cliente_id"`
	VendedorExternoID   *uuid.UUID `gorm:"column:vendedor_externo_id"`
	Status              string     `gorm:"column:status"`
	DataVenda           time.Time  `gorm:"column:data_venda"`
	DataEntregaPrevista *time.Time `gorm:"column:data_entrega_prevista"`
	FreteValor          float64    `gorm:"column:frete_valor"`
	FretePago           float64    `gorm:"column:frete_pago"`
	ComissaoValor       float64    `gorm:"column:comissao_valor"`
	ComissaoPaga        bool       `gorm:"column:comissao_paga"`
	Observacoes         *string    `gorm:"column:observacoes"`
	CreatedAt           time.Time  `gorm:"column:created_at"`
	TotalVenda          float64    `gorm:"column:total_venda"`
	TotalCusto          float64    `gorm:"column:total_custo"`
	TotalRecebido       float64    `gorm:"column:total_recebido"`
	APagarFornecedor    float64    `gorm:"column:a_pagar_fornecedor"`
	Lucro               float64    `gorm:"column:lucro"`
	ClienteNome         string     `gorm:"column:cliente_nome"`
	VendedorExternoNome *string    `gorm:"column:vendedor_externo_nome"`
}

func (row vendaResumoRow) toListResponse() vendaListResponse {
	resp := vendaListResponse{
		ID:                  row.ID,
		ClienteID:           row.ClienteID,
		ClienteNome:         row.ClienteNome,
		VendedorExternoID:   row.VendedorExternoID,
		VendedorExternoNome: row.VendedorExternoNome,
		Status:              row.Status,
		DataVenda:           row.DataVenda.Format("2006-01-02"),
		FreteValor:          row.FreteValor,
		FretePago:           row.FretePago,
		ComissaoValor:       row.ComissaoValor,
		ComissaoPaga:        row.ComissaoPaga,
		Observacoes:         row.Observacoes,
		TotalVenda:          row.TotalVenda,
		TotalCusto:          row.TotalCusto,
		TotalRecebido:       row.TotalRecebido,
		AReceber:            row.TotalVenda - row.TotalRecebido,
		Lucro:               row.Lucro,
		CreatedAt:           row.CreatedAt,
	}
	if row.DataEntregaPrevista != nil {
		s := row.DataEntregaPrevista.Format("2006-01-02")
		resp.DataEntregaPrevista = &s
	}
	return resp
}

func itemToResponse(item models.ItemVenda) itemResponse {
	totalVenda := item.Quantidade * item.PrecoVendaUnit
	totalCusto := item.Quantidade * item.CustoUnit
	return itemResponse{
		ID:               item.ID,
		ProdutoID:        item.ProdutoID,
		ProdutoNome:      item.ProdutoNome,
		FornecedorID:     item.FornecedorID,
		FornecedorNome:   item.FornecedorNome,
		Quantidade:       item.Quantidade,
		PrecoVendaUnit:   item.PrecoVendaUnit,
		CustoUnit:        item.CustoUnit,
		PagoFornecedor:   item.PagoFornecedor,
		TotalVenda:       totalVenda,
		TotalCusto:       totalCusto,
		APagarFornecedor: totalCusto - item.PagoFornecedor,
		CreatedAt:        item.CreatedAt,
	}
}

// --- Helper methods ---

func (h *VendasHandler) fetchVendaResumo(ctx context.Context, id uuid.UUID) (*vendaResumoRow, error) {
	var row vendaResumoRow
	result := h.db.WithContext(ctx).
		Table("vw_vendas_resumo vr").
		Select("vr.*, c.nome AS cliente_nome, ve.nome AS vendedor_externo_nome").
		Joins("JOIN clientes c ON c.id = vr.cliente_id").
		Joins("LEFT JOIN vendedores_externos ve ON ve.id = vr.vendedor_externo_id").
		Where("vr.id = ?", id).
		Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func (h *VendasHandler) fetchItens(ctx context.Context, vendaID uuid.UUID) ([]models.ItemVenda, error) {
	var itens []models.ItemVenda
	err := h.db.WithContext(ctx).
		Where("venda_id = ?", vendaID).
		Order("created_at ASC").
		Find(&itens).Error
	return itens, err
}

func (h *VendasHandler) buildDetailResponse(ctx context.Context, vendaID uuid.UUID) (*vendaDetailResponse, error) {
	row, err := h.fetchVendaResumo(ctx, vendaID)
	if err != nil {
		return nil, err
	}

	itens, err := h.fetchItens(ctx, vendaID)
	if err != nil {
		return nil, fmt.Errorf("fetch itens: %w", err)
	}

	var pagamentos []models.PagamentoCliente
	if err := h.db.WithContext(ctx).
		Where("venda_id = ?", vendaID).
		Order("CASE WHEN status = 'pendente' THEN 0 ELSE 1 END, COALESCE(data_agendada, data_pagamento) ASC").
		Find(&pagamentos).Error; err != nil {
		return nil, fmt.Errorf("fetch pagamentos: %w", err)
	}

	itensResp := make([]itemResponse, 0, len(itens))
	for _, item := range itens {
		itensResp = append(itensResp, itemToResponse(item))
	}

	pagResp := make([]pagamentoResponse, 0, len(pagamentos))
	for _, p := range pagamentos {
		pagResp = append(pagResp, pagamentoToResponse(p))
	}

	return &vendaDetailResponse{
		vendaListResponse: row.toListResponse(),
		APagarFornecedor:  row.APagarFornecedor,
		APagarFrete:       row.FreteValor - row.FretePago,
		Itens:             itensResp,
		Pagamentos:        pagResp,
	}, nil
}

func validateItemFields(req itemRequest) string {
	if strings.TrimSpace(req.ProdutoNome) == "" {
		return "campo 'produto_nome' é obrigatório"
	}
	if req.Quantidade <= 0 {
		return "campo 'quantidade' deve ser maior que zero"
	}
	if req.PrecoVendaUnit < 0 {
		return "campo 'preco_venda_unit' não pode ser negativo"
	}
	if req.CustoUnit < 0 {
		return "campo 'custo_unit' não pode ser negativo"
	}
	if req.PagoFornecedor < 0 {
		return "campo 'pago_fornecedor' não pode ser negativo"
	}
	return ""
}

func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(s))
}

// ============================================================
// Vendas CRUD
// ============================================================

func (h *VendasHandler) list(w http.ResponseWriter, r *http.Request) {
	query := h.db.WithContext(r.Context()).
		Table("vw_vendas_resumo vr").
		Select("vr.*, c.nome AS cliente_nome, ve.nome AS vendedor_externo_nome").
		Joins("JOIN clientes c ON c.id = vr.cliente_id").
		Joins("LEFT JOIN vendedores_externos ve ON ve.id = vr.vendedor_externo_id")

	if status := r.URL.Query().Get("status"); status != "" {
		query = query.Where("vr.status = ?", status)
	}
	if dataInicio := r.URL.Query().Get("data_inicio"); dataInicio != "" {
		query = query.Where("vr.data_venda >= ?", dataInicio)
	}
	if dataFim := r.URL.Query().Get("data_fim"); dataFim != "" {
		query = query.Where("vr.data_venda <= ?", dataFim)
	}
	if clienteID := r.URL.Query().Get("cliente_id"); clienteID != "" {
		if _, err := uuid.Parse(clienteID); err != nil {
			models.WriteError(w, http.StatusBadRequest, "cliente_id inválido")
			return
		}
		query = query.Where("vr.cliente_id = ?", clienteID)
	}
	if r.URL.Query().Get("pendente") == "true" {
		query = query.Where(`
			(SELECT COALESCE(SUM(iv.quantidade * iv.preco_venda_unit), 0) FROM itens_venda iv WHERE iv.venda_id = vr.id)
			> (SELECT COALESCE(SUM(pc.valor), 0) FROM pagamentos_cliente pc WHERE pc.venda_id = vr.id AND pc.status = 'recebido')
		`)
	}

	query = query.Order("vr.data_venda DESC, vr.created_at DESC")

	var rows []vendaResumoRow
	if err := query.Scan(&rows).Error; err != nil {
		log.Printf("ERROR ListVendas: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	vendas := make([]vendaListResponse, 0, len(rows))
	for _, row := range rows {
		vendas = append(vendas, row.toListResponse())
	}
	writeJSON(w, http.StatusOK, vendas)
}

func (h *VendasHandler) create(w http.ResponseWriter, r *http.Request) {
	var req vendaCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	// Validate cliente_id exists.
	if req.ClienteID == uuid.Nil {
		models.WriteError(w, http.StatusBadRequest, "campo 'cliente_id' é obrigatório")
		return
	}
	var cliente models.Cliente
	if err := h.db.WithContext(r.Context()).First(&cliente, "id = ?", req.ClienteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusBadRequest, "cliente não encontrado")
			return
		}
		log.Printf("ERROR CreateVenda check cliente: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// Validate status.
	if !validStatuses[req.Status] {
		models.WriteError(w, http.StatusBadRequest, "status inválido")
		return
	}
	if req.Status == "A_ENTREGAR_DATA" && (req.DataEntregaPrevista == nil || strings.TrimSpace(*req.DataEntregaPrevista) == "") {
		models.WriteError(w, http.StatusBadRequest, "data_entrega_prevista é obrigatória para status A_ENTREGAR_DATA")
		return
	}

	// Validate data_venda.
	if strings.TrimSpace(req.DataVenda) == "" {
		models.WriteError(w, http.StatusBadRequest, "campo 'data_venda' é obrigatório")
		return
	}
	dataVenda, err := parseDate(req.DataVenda)
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "data_venda inválida (formato: YYYY-MM-DD)")
		return
	}

	// Parse data_entrega_prevista.
	var dataEntregaPrevista *time.Time
	if req.DataEntregaPrevista != nil && strings.TrimSpace(*req.DataEntregaPrevista) != "" {
		t, err := parseDate(*req.DataEntregaPrevista)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_entrega_prevista inválida (formato: YYYY-MM-DD)")
			return
		}
		dataEntregaPrevista = &t
	}

	// Validate monetary fields >= 0.
	if req.FreteValor < 0 || req.FretePago < 0 || req.ComissaoValor < 0 {
		models.WriteError(w, http.StatusBadRequest, "valores monetários não podem ser negativos")
		return
	}

	// Validate each item.
	for i, itemReq := range req.Itens {
		if errMsg := validateItemFields(itemReq); errMsg != "" {
			models.WriteError(w, http.StatusBadRequest, fmt.Sprintf("item[%d]: %s", i, errMsg))
			return
		}
	}

	// Transaction: create venda + items.
	tx := h.db.WithContext(r.Context()).Begin()
	defer tx.Rollback()

	venda := models.Venda{
		ClienteID:           req.ClienteID,
		VendedorExternoID:   req.VendedorExternoID,
		Status:              req.Status,
		DataVenda:           dataVenda,
		DataEntregaPrevista: dataEntregaPrevista,
		FreteValor:          req.FreteValor,
		FretePago:           req.FretePago,
		ComissaoValor:       req.ComissaoValor,
		ComissaoPaga:        req.ComissaoPaga,
		Observacoes:         req.Observacoes,
	}

	if err := tx.Create(&venda).Error; err != nil {
		log.Printf("ERROR CreateVenda insert: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	for _, itemReq := range req.Itens {
		item := models.ItemVenda{
			VendaID:        venda.ID,
			ProdutoID:      itemReq.ProdutoID,
			ProdutoNome:    strings.TrimSpace(itemReq.ProdutoNome),
			FornecedorID:   itemReq.FornecedorID,
			FornecedorNome: itemReq.FornecedorNome,
			Quantidade:     itemReq.Quantidade,
			PrecoVendaUnit: itemReq.PrecoVendaUnit,
			CustoUnit:      itemReq.CustoUnit,
		}
		if err := tx.Create(&item).Error; err != nil {
			log.Printf("ERROR CreateVenda insert item: %v", err)
			models.WriteError(w, http.StatusInternalServerError, "erro interno")
			return
		}
	}

	if err := tx.Commit().Error; err != nil {
		log.Printf("ERROR CreateVenda commit: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// Fetch complete venda with totals and items.
	resp, err := h.buildDetailResponse(r.Context(), venda.ID)
	if err != nil {
		log.Printf("ERROR CreateVenda fetch detail: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *VendasHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	resp, err := h.buildDetailResponse(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "venda não encontrada")
			return
		}
		log.Printf("ERROR GetVenda: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *VendasHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req vendaUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	// Validate status.
	if !validStatuses[req.Status] {
		models.WriteError(w, http.StatusBadRequest, "status inválido")
		return
	}
	if req.Status == "A_ENTREGAR_DATA" && (req.DataEntregaPrevista == nil || strings.TrimSpace(*req.DataEntregaPrevista) == "") {
		models.WriteError(w, http.StatusBadRequest, "data_entrega_prevista é obrigatória para status A_ENTREGAR_DATA")
		return
	}

	// Validate monetary fields.
	if req.FreteValor < 0 || req.FretePago < 0 || req.ComissaoValor < 0 {
		models.WriteError(w, http.StatusBadRequest, "valores monetários não podem ser negativos")
		return
	}

	// Parse data_entrega_prevista.
	var dataEntregaPrevista *time.Time
	if req.DataEntregaPrevista != nil && strings.TrimSpace(*req.DataEntregaPrevista) != "" {
		t, err := parseDate(*req.DataEntregaPrevista)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_entrega_prevista inválida (formato: YYYY-MM-DD)")
			return
		}
		dataEntregaPrevista = &t
	}

	// Find existing venda.
	var venda models.Venda
	if err := h.db.WithContext(r.Context()).First(&venda, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "venda não encontrada")
			return
		}
		log.Printf("ERROR UpdateVenda find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// Update allowed fields (not cliente_id or data_venda).
	venda.VendedorExternoID = req.VendedorExternoID
	venda.Status = req.Status
	venda.DataEntregaPrevista = dataEntregaPrevista
	venda.FreteValor = req.FreteValor
	venda.FretePago = req.FretePago
	venda.ComissaoValor = req.ComissaoValor
	venda.ComissaoPaga = req.ComissaoPaga
	venda.Observacoes = req.Observacoes

	if err := h.db.WithContext(r.Context()).Save(&venda).Error; err != nil {
		log.Printf("ERROR UpdateVenda save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	resp, err := h.buildDetailResponse(r.Context(), id)
	if err != nil {
		log.Printf("ERROR UpdateVenda fetch detail: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *VendasHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	if err := h.db.WithContext(r.Context()).Delete(&models.Venda{}, "id = ?", id).Error; err != nil {
		log.Printf("ERROR SoftDeleteVenda: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Itens de Venda CRUD
// ============================================================

func (h *VendasHandler) listItens(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var venda models.Venda
	if err := h.db.WithContext(r.Context()).First(&venda, "id = ?", vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "venda não encontrada")
			return
		}
		log.Printf("ERROR ListItens check venda: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	itens, err := h.fetchItens(r.Context(), vendaID)
	if err != nil {
		log.Printf("ERROR ListItens: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	resp := make([]itemResponse, 0, len(itens))
	for _, item := range itens {
		resp = append(resp, itemToResponse(item))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *VendasHandler) createItem(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var venda models.Venda
	if err := h.db.WithContext(r.Context()).First(&venda, "id = ?", vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "venda não encontrada")
			return
		}
		log.Printf("ERROR CreateItem check venda: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	var req itemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if errMsg := validateItemFields(req); errMsg != "" {
		models.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}

	item := models.ItemVenda{
		VendaID:        vendaID,
		ProdutoID:      req.ProdutoID,
		ProdutoNome:    strings.TrimSpace(req.ProdutoNome),
		FornecedorID:   req.FornecedorID,
		FornecedorNome: req.FornecedorNome,
		Quantidade:     req.Quantidade,
		PrecoVendaUnit: req.PrecoVendaUnit,
		CustoUnit:      req.CustoUnit,
	}

	if err := h.db.WithContext(r.Context()).Create(&item).Error; err != nil {
		log.Printf("ERROR CreateItem: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusCreated, itemToResponse(item))
}

func (h *VendasHandler) updateItem(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id da venda inválido")
		return
	}

	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do item inválido")
		return
	}

	var req itemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if errMsg := validateItemFields(req); errMsg != "" {
		models.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}

	var item models.ItemVenda
	if err := h.db.WithContext(r.Context()).First(&item, "id = ? AND venda_id = ?", itemID, vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "item não encontrado")
			return
		}
		log.Printf("ERROR UpdateItem find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	item.ProdutoID = req.ProdutoID
	item.ProdutoNome = strings.TrimSpace(req.ProdutoNome)
	item.FornecedorID = req.FornecedorID
	item.FornecedorNome = req.FornecedorNome
	item.Quantidade = req.Quantidade
	item.PrecoVendaUnit = req.PrecoVendaUnit
	item.CustoUnit = req.CustoUnit
	item.PagoFornecedor = req.PagoFornecedor

	if err := h.db.WithContext(r.Context()).Save(&item).Error; err != nil {
		log.Printf("ERROR UpdateItem save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, itemToResponse(item))
}

func (h *VendasHandler) deleteItem(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id da venda inválido")
		return
	}

	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do item inválido")
		return
	}

	// Verify item exists and belongs to venda.
	var item models.ItemVenda
	if err := h.db.WithContext(r.Context()).First(&item, "id = ? AND venda_id = ?", itemID, vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "item não encontrado")
			return
		}
		log.Printf("ERROR DeleteItem find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	// Venda must keep at least 1 item.
	var count int64
	if err := h.db.WithContext(r.Context()).Model(&models.ItemVenda{}).Where("venda_id = ?", vendaID).Count(&count).Error; err != nil {
		log.Printf("ERROR DeleteItem count: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if count <= 1 {
		models.WriteError(w, http.StatusBadRequest, "a venda deve ter ao menos 1 item")
		return
	}

	if err := h.db.WithContext(r.Context()).Delete(&item).Error; err != nil {
		log.Printf("ERROR DeleteItem: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
