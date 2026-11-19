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
	"github.com/silvioubaldino/sales-backend/internal/fulfillment"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"github.com/silvioubaldino/sales-backend/internal/saleitem"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// --- Handler ---

type VendasHandler struct {
	db *gorm.DB
}

func NewVendasHandler(db *gorm.DB) *VendasHandler {
	return &VendasHandler{db: db}
}

// Register: a Sale só nasce de um Quote (POST /quotes/{id}/convert ou /quotes/quick-sale —
// RN-01); POST /vendas não existe mais (AYD-002).
func (h *VendasHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /vendas", h.list)
	mux.HandleFunc("GET /vendas/{id}", h.get)
	mux.HandleFunc("PUT /vendas/{id}", h.update)
	mux.HandleFunc("DELETE /vendas/{id}", h.delete)
	mux.HandleFunc("PATCH /vendas/{id}/delivery-status", h.updateDeliveryStatus)

	mux.HandleFunc("GET /vendas/{id}/itens", h.listItens)
	mux.HandleFunc("POST /vendas/{id}/itens", h.createItem)
	mux.HandleFunc("PUT /vendas/{id}/itens/{itemId}", h.updateItem)
	mux.HandleFunc("DELETE /vendas/{id}/itens/{itemId}", h.deleteItem)

	// AYD-009: cobranças ao cliente (preço) e custos da venda (com credor).
	mux.HandleFunc("GET /vendas/{id}/charges", h.listCharges)
	mux.HandleFunc("POST /vendas/{id}/charges", h.createCharge)
	mux.HandleFunc("PUT /vendas/{id}/charges/{chargeId}", h.updateCharge)
	mux.HandleFunc("DELETE /vendas/{id}/charges/{chargeId}", h.deleteCharge)
	mux.HandleFunc("GET /vendas/{id}/costs", h.listCosts)
	mux.HandleFunc("POST /vendas/{id}/costs", h.createCost)
	mux.HandleFunc("PUT /vendas/{id}/costs/{costId}", h.updateCost)
	mux.HandleFunc("DELETE /vendas/{id}/costs/{costId}", h.deleteCost)
}

// --- Request types ---

// vendaUpdateRequest: status de entrega não é mais aceito aqui (só via PATCH delivery-status).
// Status só existe para detectar o campo legado e responder 400.
type vendaUpdateRequest struct {
	VendedorExternoID   *uuid.UUID      `json:"vendedor_externo_id"`
	Status              json.RawMessage `json:"status"`
	DeliveryStatus      json.RawMessage `json:"delivery_status"`
	DataEntregaPrevista *string         `json:"data_entrega_prevista"`
	Observacoes         *string         `json:"observacoes"`
	// Campos removidos (AYD-007/009): só detectados para responder 400 — frete e comissão são
	// custos da venda (POST /vendas/{id}/costs) e pagá-los é quitar a conta (POST /settlements).
	FreteValor    json.RawMessage `json:"frete_valor"`
	ComissaoValor json.RawMessage `json:"comissao_valor"`
	FretePago     json.RawMessage `json:"frete_pago"`
	ComissaoPaga  json.RawMessage `json:"comissao_paga"`
}

type deliveryStatusRequest struct {
	DeliveryStatus string `json:"delivery_status"`
}

type deliveryStatusResponse struct {
	ID             uuid.UUID `json:"id"`
	DeliveryStatus string    `json:"delivery_status"`
}

// itemRequest: custo_unit é ponteiro para distinguir "não veio" (FROM_STOCK → custo da
// última entrada — RN-16). pago_fornecedor saiu do contrato (AYD-002).
type itemRequest struct {
	ProdutoID      *uuid.UUID `json:"produto_id"`
	ProdutoNome    string     `json:"produto_nome"`
	FornecedorID   *uuid.UUID `json:"fornecedor_id"`
	FornecedorNome *string    `json:"fornecedor_nome"`
	Quantidade     float64    `json:"quantidade"`
	PrecoVendaUnit float64    `json:"preco_venda_unit"`
	CustoUnit      *float64   `json:"custo_unit"`
	Origin         *string    `json:"origin"`
	// AYD-010: tipo do Item (default PRODUCT) e serviço do catálogo (só SERVICE; opcional).
	Kind      *string    `json:"kind"`
	ServiceID *uuid.UUID `json:"service_id"`
}

// --- Response types ---

type vendaListResponse struct {
	ID                  uuid.UUID  `json:"id"`
	ClienteID           uuid.UUID  `json:"cliente_id"`
	ClienteNome         string     `json:"cliente_nome"`
	VendedorExternoID   *uuid.UUID `json:"vendedor_externo_id"`
	VendedorExternoNome *string    `json:"vendedor_externo_nome"`
	QuoteID             *uuid.UUID `json:"quote_id"`
	DeliveryStatus      string     `json:"delivery_status"`
	PaymentStatus       string     `json:"payment_status"`
	DataVenda           string     `json:"data_venda"`
	DataEntregaPrevista *string    `json:"data_entrega_prevista"`
	Observacoes         *string    `json:"observacoes"`
	TotalVenda          float64    `json:"total_venda"` // preço ao cliente: Items + cobranças (AYD-009)
	ChargesTotal        float64    `json:"charges_total"`
	TotalCusto          float64    `json:"total_custo"`    // pedras (+ rateio dos custos adicionais da compra)
	CostsTotal          float64    `json:"costs_total"`    // custos da venda previstos
	CostsPaid           float64    `json:"costs_paid"`     // Σ quitado das contas a pagar da venda
	CostsOpen           float64    `json:"costs_open"`     // Σ em aberto das contas a pagar da venda
	TotalRecebido       float64    `json:"total_recebido"` // Σ quitações da conta RECEIVABLE (AYD-007)
	Received            float64    `json:"received"`       // = total_recebido
	AReceber            float64    `json:"a_receber"`
	Receivable          float64    `json:"receivable"` // RN-05: aberto da conta RECEIVABLE
	Lucro               float64    `json:"lucro"`
	CreatedAt           time.Time  `json:"created_at"`
}

type vendaDetailResponse struct {
	vendaListResponse
	Itens       []itemResponse           `json:"itens"`
	Composition saleComposition          `json:"composition"`
	Obligations []obligations.Obligation `json:"obligations"`
	// Warnings: OVERSETTLED quando alguma conta da Sale ficou com quitado > valor (ex.: troca de
	// Partner depois de quitar a comissão) — o dono ajusta a quitação (AYD-007 §2).
	Warnings []string `json:"warnings"`
}

type itemResponse struct {
	ID             uuid.UUID  `json:"id"`
	ProdutoID      *uuid.UUID `json:"produto_id"`
	ProdutoNome    string     `json:"produto_nome"`
	FornecedorID   *uuid.UUID `json:"fornecedor_id"`
	FornecedorNome *string    `json:"fornecedor_nome"`
	Kind           string     `json:"kind"`
	ServiceID      *uuid.UUID `json:"service_id"`
	Origin         *string    `json:"origin"` // null em Item de serviço
	PurchaseID     *uuid.UUID `json:"purchase_id"`
	PurchaseStatus *string    `json:"purchase_status"` // só leitura: status da Purchase do Item (null se não está em Purchase)
	Quantidade     float64    `json:"quantidade"`
	PrecoVendaUnit float64    `json:"preco_venda_unit"`
	CustoUnit      float64    `json:"custo_unit"`
	TotalVenda     float64    `json:"total_venda"`
	TotalCusto     float64    `json:"total_custo"`
	CreatedAt      time.Time  `json:"created_at"`
}

// itemMutationResponse: resposta de POST/PUT de Item — sempre traz warnings (pode ser []).
type itemMutationResponse struct {
	itemResponse
	Warnings []string `json:"warnings"`
}

// --- Internal row types ---

type vendaResumoRow struct {
	ID                  uuid.UUID  `gorm:"column:id"`
	ClienteID           uuid.UUID  `gorm:"column:cliente_id"`
	VendedorExternoID   *uuid.UUID `gorm:"column:vendedor_externo_id"`
	DeliveryStatus      string     `gorm:"column:delivery_status"`
	PaymentStatus       string     `gorm:"column:payment_status"`
	DataVenda           time.Time  `gorm:"column:data_venda"`
	DataEntregaPrevista *time.Time `gorm:"column:data_entrega_prevista"`
	Observacoes         *string    `gorm:"column:observacoes"`
	CreatedAt           time.Time  `gorm:"column:created_at"`
	TotalVenda          float64    `gorm:"column:total_venda"`
	ItemsTotal          float64    `gorm:"column:items_total"`
	ChargesTotal        float64    `gorm:"column:charges_total"`
	TotalCusto          float64    `gorm:"column:total_custo"`
	CostsTotal          float64    `gorm:"column:costs_total"`
	CostsPaid           float64    `gorm:"column:costs_paid"`
	CostsOpen           float64    `gorm:"column:costs_open"`
	TotalRecebido       float64    `gorm:"column:total_recebido"`
	Lucro               float64    `gorm:"column:lucro"`
	Receivable          float64    `gorm:"column:receivable"`
	ClienteNome         string     `gorm:"column:cliente_nome"`
	VendedorExternoNome *string    `gorm:"column:vendedor_externo_nome"`
	QuoteID             *uuid.UUID `gorm:"column:quote_id"`
}

func (row vendaResumoRow) toListResponse() vendaListResponse {
	return vendaListResponse{
		ID:                  row.ID,
		ClienteID:           row.ClienteID,
		ClienteNome:         row.ClienteNome,
		VendedorExternoID:   row.VendedorExternoID,
		VendedorExternoNome: row.VendedorExternoNome,
		QuoteID:             row.QuoteID,
		DeliveryStatus:      row.DeliveryStatus,
		PaymentStatus:       row.PaymentStatus,
		DataVenda:           row.DataVenda.Format("2006-01-02"),
		DataEntregaPrevista: formatDate(row.DataEntregaPrevista),
		Observacoes:         row.Observacoes,
		TotalVenda:          row.TotalVenda,
		ChargesTotal:        row.ChargesTotal,
		TotalCusto:          row.TotalCusto,
		CostsTotal:          row.CostsTotal,
		CostsPaid:           obligations.Round2(row.CostsPaid),
		CostsOpen:           obligations.Round2(row.CostsOpen),
		TotalRecebido:       row.TotalRecebido,
		Received:            row.TotalRecebido,
		AReceber:            row.TotalVenda - row.TotalRecebido,
		Receivable:          row.Receivable,
		Lucro:               row.Lucro,
		CreatedAt:           row.CreatedAt,
	}
}

// itemRow: Item de Sale + status da Purchase (LEFT JOIN).
type itemRow struct {
	models.ItemVenda
	PurchaseStatus *string `gorm:"column:purchase_status"`
}

func itemToResponse(item models.ItemVenda, purchaseStatus *string) itemResponse {
	return itemResponse{
		ID:             item.ID,
		ProdutoID:      item.ProdutoID,
		ProdutoNome:    item.ProdutoNome,
		FornecedorID:   item.FornecedorID,
		FornecedorNome: item.FornecedorNome,
		Kind:           item.Kind,
		ServiceID:      item.ServiceID,
		Origin:         item.Origin,
		PurchaseID:     item.PurchaseID,
		PurchaseStatus: purchaseStatus,
		Quantidade:     item.Quantidade,
		PrecoVendaUnit: item.PrecoVendaUnit,
		CustoUnit:      item.CustoUnit,
		TotalVenda:     item.Quantidade * item.PrecoVendaUnit,
		TotalCusto:     item.Quantidade * item.CustoUnit,
		CreatedAt:      item.CreatedAt,
	}
}

// --- Helper methods ---

func (h *VendasHandler) resumoQuery(ctx context.Context) *gorm.DB {
	return h.db.WithContext(ctx).
		Table("vw_vendas_resumo vr").
		Select("vr.*, c.nome AS cliente_nome, ve.nome AS vendedor_externo_nome"). // vr.* inclui quote_id, delivery_status, payment_status
		Joins("JOIN clientes c ON c.id = vr.cliente_id").
		Joins("LEFT JOIN vendedores_externos ve ON ve.id = vr.vendedor_externo_id")
}

func (h *VendasHandler) fetchVendaResumo(ctx context.Context, id uuid.UUID) (*vendaResumoRow, error) {
	var row vendaResumoRow
	result := h.resumoQuery(ctx).Where("vr.id = ?", id).Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func fetchItemRows(db *gorm.DB, where string, args ...any) ([]itemRow, error) {
	var rows []itemRow
	err := db.Table("itens_venda").
		Select("itens_venda.*, p.status AS purchase_status").
		Joins("LEFT JOIN purchases p ON p.id = itens_venda.purchase_id").
		Where(where, args...).
		Order("itens_venda.created_at ASC").
		Scan(&rows).Error
	return rows, err
}

func (h *VendasHandler) fetchItens(ctx context.Context, vendaID uuid.UUID) ([]itemResponse, error) {
	rows, err := fetchItemRows(h.db.WithContext(ctx), "itens_venda.venda_id = ?", vendaID)
	if err != nil {
		return nil, err
	}
	out := make([]itemResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, itemToResponse(r.ItemVenda, r.PurchaseStatus))
	}
	return out, nil
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

	var overs []string
	if err := h.db.WithContext(ctx).Raw(`SELECT DISTINCT 'OVERSETTLED' FROM vw_obligations
		WHERE sale_id = ? AND settled > amount`, vendaID).Scan(&overs).Error; err != nil {
		return nil, fmt.Errorf("fetch warnings: %w", err)
	}
	if overs == nil {
		overs = []string{}
	}

	comp, obs, err := buildSaleComposition(h.db.WithContext(ctx), vendaID, row.ItemsTotal, row.ChargesTotal, row.CostsTotal)
	if err != nil {
		return nil, fmt.Errorf("fetch composition: %w", err)
	}

	return &vendaDetailResponse{
		vendaListResponse: row.toListResponse(),
		Itens:             itens,
		Composition:       comp,
		Obligations:       obs,
		Warnings:          overs,
	}, nil
}

// validateItemFields valida o payload do Item (regras que não dependem do banco).
func validateItemFields(req itemRequest) string {
	// Item de serviço do catálogo pode vir sem nome: assume o nome do serviço (AYD-010).
	if strings.TrimSpace(req.ProdutoNome) == "" && req.ServiceID == nil {
		return "campo 'produto_nome' é obrigatório"
	}
	if req.Kind != nil {
		if _, ok := saleitem.Resolve(req.Kind); !ok {
			return "kind inválido (PRODUCT | SERVICE)"
		}
	}
	if req.Quantidade <= 0 {
		return "campo 'quantidade' deve ser maior que zero"
	}
	if req.PrecoVendaUnit < 0 {
		return "campo 'preco_venda_unit' não pode ser negativo"
	}
	if req.CustoUnit != nil && *req.CustoUnit < 0 {
		return "campo 'custo_unit' não pode ser negativo"
	}
	if req.Origin != nil && !fulfillment.ItemOrigins[*req.Origin] {
		return "origin inválido (MADE_TO_ORDER | FROM_STOCK)"
	}
	if req.Origin != nil && *req.Origin == fulfillment.OriginFromStock && req.ProdutoID == nil {
		return "item FROM_STOCK exige 'produto_id'"
	}
	return ""
}

// parseEnumList separa "A,B" e valida cada valor contra o enum.
func parseEnumList(raw string, valid map[string]bool) ([]string, bool) {
	var out []string
	for _, v := range strings.Split(raw, ",") {
		v = strings.TrimSpace(v)
		if !valid[v] {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(s))
}

// ============================================================
// Vendas
// ============================================================

func (h *VendasHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := h.resumoQuery(r.Context())

	// delivery_status / payment_status aceitam vários valores separados por vírgula
	// (ex.: payment_status=PENDING,PARTIAL).
	for _, f := range []struct {
		param string
		valid map[string]bool
	}{{"delivery_status", fulfillment.SaleDeliveryStatuses}, {"payment_status", fulfillment.SalePaymentStatuses}} {
		raw := q.Get(f.param)
		if raw == "" {
			continue
		}
		values, ok := parseEnumList(raw, f.valid)
		if !ok {
			models.WriteError(w, http.StatusBadRequest, f.param+" inválido")
			return
		}
		query = query.Where("vr."+f.param+"::text IN ?", values)
	}
	if dataInicio := q.Get("data_inicio"); dataInicio != "" {
		query = query.Where("vr.data_venda >= ?", dataInicio)
	}
	if dataFim := q.Get("data_fim"); dataFim != "" {
		query = query.Where("vr.data_venda <= ?", dataFim)
	}
	if clienteID := q.Get("cliente_id"); clienteID != "" {
		if _, err := uuid.Parse(clienteID); err != nil {
			models.WriteError(w, http.StatusBadRequest, "cliente_id inválido")
			return
		}
		query = query.Where("vr.cliente_id = ?", clienteID)
	}
	// AYD-004: período por data_venda (inclusivo). freight_pending/commission_pending saíram
	// (AYD-009): use GET /obligations?origin_type=SALE&counterparty_type=…
	for _, f := range []struct{ param, op string }{{"from", ">="}, {"to", "<="}} {
		raw := q.Get(f.param)
		if raw == "" {
			continue
		}
		d, err := parseDate(raw)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, f.param+" inválida (formato: YYYY-MM-DD)")
			return
		}
		query = query.Where("vr.data_venda "+f.op+" ?", d)
	}
	if q.Get("pendente") == "true" {
		// "a receber" exclui Sales canceladas (AYD-002).
		query = query.Where("vr.receivable > 0 AND vr.delivery_status <> 'CANCELED'")
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
	if req.Status != nil || req.DeliveryStatus != nil {
		models.WriteError(w, http.StatusBadRequest, "status de entrega só via PATCH /vendas/{id}/delivery-status")
		return
	}
	if req.FreteValor != nil || req.ComissaoValor != nil {
		models.WriteError(w, http.StatusBadRequest, "frete_valor e comissao_valor foram removidos: lance frete e comissão como custos da venda (POST /vendas/{id}/costs)")
		return
	}
	if req.FretePago != nil || req.ComissaoPaga != nil {
		models.WriteError(w, http.StatusBadRequest, "frete_pago e comissao_paga foram removidos: pague frete e comissão quitando a conta (POST /settlements)")
		return
	}
	dataEntregaPrevista, err := parseOptionalDate("data_entrega_prevista", req.DataEntregaPrevista)
	if err != nil {
		writeTxError(w, "UpdateVenda", err)
		return
	}

	// Atualiza só os campos editáveis (não cliente_id, data_venda nem delivery_status).
	result := h.db.WithContext(r.Context()).
		Model(&models.Venda{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"vendedor_externo_id":   req.VendedorExternoID,
			"data_entrega_prevista": dataEntregaPrevista,
			"observacoes":           req.Observacoes,
		})
	if result.Error != nil {
		log.Printf("ERROR UpdateVenda save: %v", result.Error)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if result.RowsAffected == 0 {
		models.WriteError(w, http.StatusNotFound, "venda não encontrada")
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

// updateDeliveryStatus: PATCH /vendas/{id}/delivery-status. Cancelar exige que nenhum Item
// esteja em Purchase ORDERED/RECEIVED; Items em Purchase OPEN são liberados na mesma
// transação (RN-15). CANCELED é terminal.
func (h *VendasHandler) updateDeliveryStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var req deliveryStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if !fulfillment.SaleDeliveryStatuses[req.DeliveryStatus] {
		models.WriteError(w, http.StatusBadRequest, "delivery_status inválido (TO_DELIVER | DELIVERED | CANCELED)")
		return
	}

	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		return changeDeliveryStatus(tx, id, req.DeliveryStatus)
	})
	if err != nil {
		writeTxError(w, "UpdateDeliveryStatus", err)
		return
	}
	writeJSON(w, http.StatusOK, deliveryStatusResponse{ID: id, DeliveryStatus: req.DeliveryStatus})
}

func changeDeliveryStatus(tx *gorm.DB, saleID uuid.UUID, next string) error {
	var venda models.Venda
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&venda, "id = ?", saleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errStatus(http.StatusNotFound, "venda não encontrada")
		}
		return err
	}

	ordered := false
	if next == fulfillment.DeliveryCanceled {
		var n int64
		if err := tx.Raw(`
			SELECT COUNT(*) FROM itens_venda i
			JOIN purchases p ON p.id = i.purchase_id
			WHERE i.venda_id = ? AND p.status IN ('ORDERED', 'RECEIVED')`, saleID).Scan(&n).Error; err != nil {
			return err
		}
		ordered = n > 0
	}
	switch err := fulfillment.CheckDeliveryChange(venda.DeliveryStatus, next, ordered); {
	case errors.Is(err, fulfillment.ErrSaleCanceled):
		return errStatus(http.StatusConflict, "venda cancelada não pode mudar de status")
	case errors.Is(err, fulfillment.ErrItemsInOrderedPurchase):
		return errStatus(http.StatusConflict, "venda com item em compra pedida ou recebida não pode ser cancelada")
	case err != nil:
		return err
	}

	if next == fulfillment.DeliveryCanceled {
		// Libera os Items das Purchases OPEN (os únicos que podem estar em Purchase aqui).
		if err := tx.Exec(`UPDATE itens_venda SET purchase_id = NULL WHERE venda_id = ? AND purchase_id IS NOT NULL`, saleID).Error; err != nil {
			return err
		}
	}
	return tx.Model(&models.Venda{}).Where("id = ?", saleID).Update("delivery_status", next).Error
}

// ============================================================
// Itens de Venda
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
	writeJSON(w, http.StatusOK, itens)
}

// lockOpenSale carrega a Sale (FOR UPDATE) e recusa Sale cancelada (409).
func lockOpenSale(tx *gorm.DB, vendaID uuid.UUID) (*models.Venda, error) {
	var venda models.Venda
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&venda, "id = ?", vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errStatus(http.StatusNotFound, "venda não encontrada")
		}
		return nil, err
	}
	if venda.DeliveryStatus == fulfillment.DeliveryCanceled {
		return nil, errStatus(http.StatusConflict, "itens de venda cancelada não podem ser alterados")
	}
	return &venda, nil
}

// checkItemRefs confere que produto_id e fornecedor_id (se vierem) existem.
func checkItemRefs(tx *gorm.DB, req itemRequest) (*models.Produto, error) {
	var produto *models.Produto
	if req.ProdutoID != nil {
		var p models.Produto
		if err := tx.Unscoped().First(&p, "id = ?", *req.ProdutoID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, errStatus(http.StatusBadRequest, "produto não encontrado")
			}
			return nil, err
		}
		produto = &p
	}
	if req.FornecedorID != nil {
		var n int64
		if err := tx.Model(&models.Fornecedor{}).Unscoped().Where("id = ?", *req.FornecedorID).Count(&n).Error; err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, errStatus(http.StatusBadRequest, "fornecedor não encontrado")
		}
	}
	return produto, nil
}

// lastInflowCost: custo da última entrada do Product (vw_stock_balance), nil se nunca houve.
func lastInflowCost(tx *gorm.DB, productID uuid.UUID) (*float64, error) {
	var cost *float64
	err := tx.Raw(`SELECT last_inflow_cost FROM vw_stock_balance WHERE product_id = ?`, productID).Scan(&cost).Error
	return cost, err
}

// stockWarnings devolve ["INSUFFICIENT_STOCK"] se o Item FROM_STOCK deixou o Balance negativo.
func stockWarnings(tx *gorm.DB, item models.ItemVenda) ([]string, error) {
	warnings := []string{}
	if item.Origin == nil || *item.Origin != fulfillment.OriginFromStock || item.ProdutoID == nil {
		return warnings, nil
	}
	var balance float64
	if err := tx.Raw(`
		SELECT COALESCE((SELECT balance FROM vw_stock_balance WHERE product_id = ?), 0)`, *item.ProdutoID).Scan(&balance).Error; err != nil {
		return nil, err
	}
	if fulfillment.InsufficientStock(balance) {
		warnings = append(warnings, fulfillment.WarningInsufficientStock)
	}
	return warnings, nil
}

// resolveItemCost aplica RN-16: FROM_STOCK sem custo_unit no payload → custo da última
// entrada. Sem entrada registrada, usa fallback (custo atual do Item no PUT; custo de
// referência do Product no POST; senão 0).
func resolveItemCost(tx *gorm.DB, req itemRequest, origin string, fallback float64) (float64, error) {
	if req.CustoUnit != nil {
		return *req.CustoUnit, nil
	}
	if origin == fulfillment.OriginFromStock && req.ProdutoID != nil {
		cost, err := lastInflowCost(tx, *req.ProdutoID)
		if err != nil {
			return 0, err
		}
		if cost != nil {
			return *cost, nil
		}
	}
	return fallback, nil
}

// checkServiceRef confere que service_id existe (404). current é o service_id já gravado no Item:
// mantê-lo é permitido mesmo que o serviço tenha sido excluído depois (o vínculo se preserva).
func checkServiceRef(tx *gorm.DB, id, current *uuid.UUID) (*models.Service, error) {
	if id == nil {
		return nil, nil
	}
	q := tx
	if current != nil && *current == *id {
		q = tx.Unscoped()
	}
	var svc models.Service
	if err := q.First(&svc, "id = ?", *id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errStatus(http.StatusNotFound, "serviço não encontrado")
		}
		return nil, err
	}
	return &svc, nil
}

// itemKindError aplica a coerência do Item com o tipo (AYD-010 §2) e o custo do serviço (sempre 0).
func itemKindError(kind string, req itemRequest) error {
	if msg := saleitem.Validate(saleitem.Fields{
		Kind: kind, HasProduct: req.ProdutoID != nil, HasService: req.ServiceID != nil,
		HasOrigin: req.Origin != nil, HasSupplier: req.FornecedorID != nil,
	}); msg != "" {
		return errStatus(http.StatusBadRequest, msg)
	}
	if kind == saleitem.KindService && req.CustoUnit != nil && *req.CustoUnit != 0 {
		return errStatus(http.StatusBadRequest, "item SERVICE tem custo_unit 0: o custo do serviço é um custo da venda (POST /vendas/{id}/costs)")
	}
	return nil
}

// serviceItemName: nome do Item de serviço — o informado ou, se vazio, o do catálogo.
func serviceItemName(req itemRequest, svc *models.Service) string {
	if name := strings.TrimSpace(req.ProdutoNome); name != "" || svc == nil {
		return name
	}
	return svc.Name
}

func (h *VendasHandler) createItem(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
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

	var resp itemMutationResponse
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := lockOpenSale(tx, vendaID); err != nil {
			return err
		}
		kind, _ := saleitem.Resolve(req.Kind)
		if err := itemKindError(kind, req); err != nil {
			return err
		}
		if kind == saleitem.KindService {
			svc, err := checkServiceRef(tx, req.ServiceID, nil)
			if err != nil {
				return err
			}
			item := models.ItemVenda{
				VendaID: vendaID, ProdutoNome: serviceItemName(req, svc), Kind: kind, ServiceID: req.ServiceID,
				Quantidade: req.Quantidade, PrecoVendaUnit: req.PrecoVendaUnit,
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			resp = itemMutationResponse{itemResponse: itemToResponse(item, nil), Warnings: []string{}}
			return nil
		}
		produto, err := checkItemRefs(tx, req)
		if err != nil {
			return err
		}
		origin := fulfillment.OriginMadeToOrder
		if req.Origin != nil {
			origin = *req.Origin
		}
		fallback := 0.0
		if produto != nil && produto.CustoReferencia != nil {
			fallback = *produto.CustoReferencia
		}
		cost, err := resolveItemCost(tx, req, origin, fallback)
		if err != nil {
			return err
		}

		item := models.ItemVenda{
			VendaID:        vendaID,
			ProdutoID:      req.ProdutoID,
			ProdutoNome:    strings.TrimSpace(req.ProdutoNome),
			FornecedorID:   req.FornecedorID,
			FornecedorNome: req.FornecedorNome,
			Origin:         &origin,
			Kind:           saleitem.KindProduct,
			Quantidade:     req.Quantidade,
			PrecoVendaUnit: req.PrecoVendaUnit,
			CustoUnit:      cost,
		}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		warnings, err := stockWarnings(tx, item)
		if err != nil {
			return err
		}
		resp = itemMutationResponse{itemResponse: itemToResponse(item, nil), Warnings: warnings}
		return nil
	})
	if err != nil {
		writeTxError(w, "CreateItem", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// lockItemInPurchase devolve o status da Purchase do Item (ou "" se o Item não está em Purchase).
func purchaseStatusOf(tx *gorm.DB, item models.ItemVenda) (string, error) {
	if item.PurchaseID == nil {
		return "", nil
	}
	var status string
	err := tx.Raw(`SELECT status FROM purchases WHERE id = ?`, *item.PurchaseID).Scan(&status).Error
	return status, err
}

func itemInPurchaseError(purchaseStatus string) error {
	if purchaseStatus == fulfillment.PurchaseOpen {
		return errStatus(http.StatusConflict, "item está numa compra aberta: remova-o da compra antes de alterar origem, fornecedor, quantidade ou custo")
	}
	return errStatus(http.StatusConflict, "item está numa compra já pedida ou recebida: origem, fornecedor, quantidade e custo não podem mudar")
}

func supplierKey(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
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

	var resp itemMutationResponse
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := lockOpenSale(tx, vendaID); err != nil {
			return err
		}
		var item models.ItemVenda
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&item, "id = ? AND venda_id = ?", itemID, vendaID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errStatus(http.StatusNotFound, "item não encontrado")
			}
			return err
		}
		// Mudar o tipo de um Item existente é 409: remova e adicione outro (AYD-010).
		if req.Kind != nil {
			if k, _ := saleitem.Resolve(req.Kind); k != item.Kind {
				return errStatus(http.StatusConflict, "o tipo (kind) do item não muda: remova o item e adicione outro")
			}
		}
		if err := itemKindError(item.Kind, req); err != nil {
			return err
		}
		if item.Kind == saleitem.KindService {
			svc, err := checkServiceRef(tx, req.ServiceID, item.ServiceID)
			if err != nil {
				return err
			}
			item.ProdutoNome = serviceItemName(req, svc)
			item.ServiceID = req.ServiceID
			item.Quantidade = req.Quantidade
			item.PrecoVendaUnit = req.PrecoVendaUnit
			if err := tx.Save(&item).Error; err != nil {
				return err
			}
			resp = itemMutationResponse{itemResponse: itemToResponse(item, nil), Warnings: []string{}}
			return nil
		}
		if _, err := checkItemRefs(tx, req); err != nil {
			return err
		}

		// origin ausente no PUT mantém a atual.
		origin := ""
		if item.Origin != nil {
			origin = *item.Origin
		}
		if req.Origin != nil {
			origin = *req.Origin
		}
		if origin == fulfillment.OriginFromStock && req.ProdutoID == nil {
			return errStatus(http.StatusBadRequest, "item FROM_STOCK exige 'produto_id'")
		}
		cost, err := resolveItemCost(tx, req, origin, item.CustoUnit)
		if err != nil {
			return err
		}

		origin0 := ""
		if item.Origin != nil {
			origin0 = *item.Origin
		}
		purchaseStatus, err := purchaseStatusOf(tx, item)
		if err != nil {
			return err
		}
		if purchaseStatus != "" {
			before := fulfillment.ItemLockFields{Origin: origin0, SupplierID: supplierKey(item.FornecedorID), Quantity: item.Quantidade, UnitCost: item.CustoUnit}
			after := fulfillment.ItemLockFields{Origin: origin, SupplierID: supplierKey(req.FornecedorID), Quantity: req.Quantidade, UnitCost: cost}
			if fulfillment.LockedFieldsChanged(before, after) {
				return itemInPurchaseError(purchaseStatus)
			}
		}

		item.ProdutoID = req.ProdutoID
		item.ProdutoNome = strings.TrimSpace(req.ProdutoNome)
		item.FornecedorID = req.FornecedorID
		item.FornecedorNome = req.FornecedorNome
		item.Origin = &origin
		item.Quantidade = req.Quantidade
		item.PrecoVendaUnit = req.PrecoVendaUnit
		item.CustoUnit = cost
		if err := tx.Save(&item).Error; err != nil {
			return err
		}
		warnings, err := stockWarnings(tx, item)
		if err != nil {
			return err
		}
		var ps *string
		if purchaseStatus != "" {
			ps = &purchaseStatus
		}
		resp = itemMutationResponse{itemResponse: itemToResponse(item, ps), Warnings: warnings}
		return nil
	})
	if err != nil {
		writeTxError(w, "UpdateItem", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
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

	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := lockOpenSale(tx, vendaID); err != nil {
			return err
		}
		var item models.ItemVenda
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&item, "id = ? AND venda_id = ?", itemID, vendaID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errStatus(http.StatusNotFound, "item não encontrado")
			}
			return err
		}
		purchaseStatus, err := purchaseStatusOf(tx, item)
		if err != nil {
			return err
		}
		if purchaseStatus != "" {
			return itemInPurchaseError(purchaseStatus)
		}

		// A Sale mantém ao menos 1 Item.
		var count int64
		if err := tx.Model(&models.ItemVenda{}).Where("venda_id = ?", vendaID).Count(&count).Error; err != nil {
			return err
		}
		if count <= 1 {
			return errStatus(http.StatusBadRequest, "a venda deve ter ao menos 1 item")
		}
		if err := releaseItemCosts(tx, item); err != nil {
			return err
		}
		return tx.Delete(&item).Error
	})
	if err != nil {
		writeTxError(w, "DeleteItem", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// releaseItemCosts trata os custos da venda ligados ao Item (sale_costs.sale_item_id) antes de
// excluí-lo. Item de serviço leva o custo junto (soft-delete) — 409 se a conta do credor desse
// custo já tem quitação (AYD-010). Qualquer vínculo restante é desfeito (FK).
func releaseItemCosts(tx *gorm.DB, item models.ItemVenda) error {
	if item.Kind == saleitem.KindService {
		var settled int64
		if err := tx.Raw(`
			SELECT COUNT(*)
			FROM sale_costs c
			JOIN vw_obligations o ON o.sale_id = c.sale_id AND o.direction = 'PAYABLE'
			 AND o.counterparty_id IS NOT DISTINCT FROM COALESCE(c.payee_supplier_id, c.payee_partner_id)
			WHERE c.sale_item_id = ? AND c.deleted_at IS NULL AND o.settled > 0`, item.ID).Scan(&settled).Error; err != nil {
			return err
		}
		if settled > 0 {
			return errStatus(http.StatusConflict, "o custo deste serviço já tem quitação na conta do credor: estorne a quitação antes de remover o item")
		}
		if err := tx.Where("sale_item_id = ?", item.ID).Delete(&models.SaleCost{}).Error; err != nil {
			return err
		}
	}
	return tx.Exec(`UPDATE sale_costs SET sale_item_id = NULL WHERE sale_item_id = ?`, item.ID).Error
}
