package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/finance"
	"github.com/silvioubaldino/sales-backend/internal/fulfillment"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
)

var validPagamentoStatuses = map[string]bool{
	"pendente": true,
	"recebido": true,
}

// --- Handler ---

type PagamentosHandler struct {
	db *gorm.DB
}

func NewPagamentosHandler(db *gorm.DB) *PagamentosHandler {
	return &PagamentosHandler{db: db}
}

func (h *PagamentosHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /vendas/{id}/pagamentos", h.list)
	mux.HandleFunc("POST /vendas/{id}/pagamentos", h.create)
	mux.HandleFunc("PUT /vendas/{id}/pagamentos/{pagId}", h.update)
	mux.HandleFunc("POST /vendas/{id}/pagamentos/{pagId}/confirmar", h.confirmar)
	mux.HandleFunc("DELETE /vendas/{id}/pagamentos/{pagId}", h.delete)
}

// errVendaCancelada: Receipts de Sale CANCELED ficam bloqueados para escrita (AYD-004;
// estorno é fora do MVP — AYD-002 questão 3).
const errVendaCancelada = "venda cancelada: recebimentos bloqueados"

// --- Request types ---

type pagamentoCreateRequest struct {
	Valor         float64 `json:"valor"`
	DataPagamento *string `json:"data_pagamento"`
	DataAgendada  *string `json:"data_agendada"`
	PaymentMethod *string `json:"payment_method"`
	Observacoes   *string `json:"observacoes"`
}

type pagamentoUpdateRequest struct {
	Valor         float64 `json:"valor"`
	DataPagamento *string `json:"data_pagamento"`
	DataAgendada  *string `json:"data_agendada"`
	Status        string  `json:"status"`
	PaymentMethod *string `json:"payment_method"`
	Observacoes   *string `json:"observacoes"`
}

type confirmarRequest struct {
	DataPagamento *string `json:"data_pagamento"`
	PaymentMethod *string `json:"payment_method"`
}

// --- Response type (also used by vendas detail) ---

type pagamentoResponse struct {
	ID            uuid.UUID `json:"id"`
	VendaID       uuid.UUID `json:"venda_id"`
	Valor         float64   `json:"valor"`
	DataPagamento *string   `json:"data_pagamento"`
	DataAgendada  *string   `json:"data_agendada"`
	Status        string    `json:"status"`
	PaymentMethod *string   `json:"payment_method"` // null só em Receipts legados ou pendentes (AYD-004)
	Observacoes   *string   `json:"observacoes"`
	CreatedAt     time.Time `json:"created_at"`
}

func pagamentoToResponse(p models.PagamentoCliente) pagamentoResponse {
	resp := pagamentoResponse{
		ID:            p.ID,
		VendaID:       p.VendaID,
		Valor:         p.Valor,
		Status:        p.Status,
		PaymentMethod: p.PaymentMethod,
		Observacoes:   p.Observacoes,
		CreatedAt:     p.CreatedAt,
	}
	if p.DataPagamento != nil {
		s := p.DataPagamento.Format("2006-01-02")
		resp.DataPagamento = &s
	}
	if p.DataAgendada != nil {
		s := p.DataAgendada.Format("2006-01-02")
		resp.DataAgendada = &s
	}
	return resp
}

// --- Helpers ---

func (h *PagamentosHandler) checkVendaExists(r *http.Request, w http.ResponseWriter, vendaID uuid.UUID) bool {
	var venda models.Venda
	if err := h.db.WithContext(r.Context()).First(&venda, "id = ?", vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "venda não encontrada")
			return false
		}
		log.Printf("ERROR Pagamentos check venda: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return false
	}
	return true
}

// loadVendaForWrite exige Sale existente (não excluída) e não CANCELED para escrever Receipts:
// 404 / 409 / 500 já respondidos quando devolve false.
func (h *PagamentosHandler) loadVendaForWrite(r *http.Request, w http.ResponseWriter, vendaID uuid.UUID) bool {
	var venda models.Venda
	if err := h.db.WithContext(r.Context()).First(&venda, "id = ?", vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "venda não encontrada")
			return false
		}
		log.Printf("ERROR Pagamentos check venda: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return false
	}
	if venda.DeliveryStatus == fulfillment.DeliveryCanceled {
		models.WriteError(w, http.StatusConflict, errVendaCancelada)
		return false
	}
	return true
}

func isNonEmptyDateStr(s *string) bool {
	return s != nil && strings.TrimSpace(*s) != ""
}

// --- Endpoints ---

func (h *PagamentosHandler) list(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	if !h.checkVendaExists(r, w, vendaID) {
		return
	}

	var pagamentos []models.PagamentoCliente
	if err := h.db.WithContext(r.Context()).
		Where("venda_id = ?", vendaID).
		Order("CASE WHEN status = 'pendente' THEN 0 ELSE 1 END, COALESCE(data_agendada, data_pagamento) ASC").
		Find(&pagamentos).Error; err != nil {
		log.Printf("ERROR ListPagamentos: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	resp := make([]pagamentoResponse, 0, len(pagamentos))
	for _, p := range pagamentos {
		resp = append(resp, pagamentoToResponse(p))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *PagamentosHandler) create(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	if !h.loadVendaForWrite(r, w, vendaID) {
		return
	}

	var req pagamentoCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	method, err := finance.NormalizeMethod(req.PaymentMethod)
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate valor > 0.
	if req.Valor <= 0 {
		models.WriteError(w, http.StatusBadRequest, "campo 'valor' deve ser maior que zero")
		return
	}

	// Cannot have both dates.
	hasDataPag := isNonEmptyDateStr(req.DataPagamento)
	hasDataAgend := isNonEmptyDateStr(req.DataAgendada)

	if hasDataPag && hasDataAgend {
		models.WriteError(w, http.StatusBadRequest, "não é permitido informar data_pagamento e data_agendada ao mesmo tempo")
		return
	}

	// Infer status and parse dates.
	var dataPagamento *time.Time
	var dataAgendada *time.Time
	var status string

	switch {
	case hasDataAgend && !hasDataPag:
		// Scheduled payment.
		t, err := parseDate(*req.DataAgendada)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_agendada inválida (formato: YYYY-MM-DD)")
			return
		}
		dataAgendada = &t
		status = "pendente"

	case hasDataPag:
		// Already received.
		t, err := parseDate(*req.DataPagamento)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_pagamento inválida (formato: YYYY-MM-DD)")
			return
		}
		dataPagamento = &t
		status = "recebido"

	default:
		// Sem datas → pendente (AYD-004: só data_pagamento faz nascer "recebido").
		status = finance.ReceiptPending
	}

	if err := finance.CheckMethodForStatus(status, method); err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	pag := models.PagamentoCliente{
		VendaID:       vendaID,
		Valor:         req.Valor,
		DataPagamento: dataPagamento,
		DataAgendada:  dataAgendada,
		Status:        status,
		PaymentMethod: method,
		Observacoes:   req.Observacoes,
	}

	if err := h.db.WithContext(r.Context()).Create(&pag).Error; err != nil {
		log.Printf("ERROR CreatePagamento: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusCreated, pagamentoToResponse(pag))
}

func (h *PagamentosHandler) update(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id da venda inválido")
		return
	}

	pagID, err := uuid.Parse(r.PathValue("pagId"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do pagamento inválido")
		return
	}

	if !h.loadVendaForWrite(r, w, vendaID) {
		return
	}

	var req pagamentoUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	method, err := finance.NormalizeMethod(req.PaymentMethod)
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Valor <= 0 {
		models.WriteError(w, http.StatusBadRequest, "campo 'valor' deve ser maior que zero")
		return
	}
	if !validPagamentoStatuses[req.Status] {
		models.WriteError(w, http.StatusBadRequest, "status inválido (pendente ou recebido)")
		return
	}

	if err := finance.CheckMethodForStatus(req.Status, method); err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	hasDataPag := isNonEmptyDateStr(req.DataPagamento)
	hasDataAgend := isNonEmptyDateStr(req.DataAgendada)

	if hasDataPag && hasDataAgend {
		models.WriteError(w, http.StatusBadRequest, "não é permitido informar data_pagamento e data_agendada ao mesmo tempo")
		return
	}

	var dataPagamento *time.Time
	var dataAgendada *time.Time

	if hasDataPag {
		t, err := parseDate(*req.DataPagamento)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_pagamento inválida (formato: YYYY-MM-DD)")
			return
		}
		dataPagamento = &t
	}
	if hasDataAgend {
		t, err := parseDate(*req.DataAgendada)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_agendada inválida (formato: YYYY-MM-DD)")
			return
		}
		dataAgendada = &t
	}

	var pag models.PagamentoCliente
	if err := h.db.WithContext(r.Context()).First(&pag, "id = ? AND venda_id = ?", pagID, vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "pagamento não encontrado")
			return
		}
		log.Printf("ERROR UpdatePagamento find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	pag.Valor = req.Valor
	pag.DataPagamento = dataPagamento
	pag.DataAgendada = dataAgendada
	pag.Status = req.Status
	pag.PaymentMethod = method
	pag.Observacoes = req.Observacoes

	if err := h.db.WithContext(r.Context()).Save(&pag).Error; err != nil {
		log.Printf("ERROR UpdatePagamento save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, pagamentoToResponse(pag))
}

func (h *PagamentosHandler) confirmar(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id da venda inválido")
		return
	}

	pagID, err := uuid.Parse(r.PathValue("pagId"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do pagamento inválido")
		return
	}

	if !h.loadVendaForWrite(r, w, vendaID) {
		return
	}

	var req confirmarRequest
	// Body is optional.
	_ = json.NewDecoder(r.Body).Decode(&req)
	method, err := finance.NormalizeMethod(req.PaymentMethod)
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	var pag models.PagamentoCliente
	if err := h.db.WithContext(r.Context()).First(&pag, "id = ? AND venda_id = ?", pagID, vendaID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "pagamento não encontrado")
			return
		}
		log.Printf("ERROR ConfirmarPagamento find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	if pag.Status == "recebido" {
		models.WriteError(w, http.StatusBadRequest, "pagamento já confirmado")
		return
	}

	// payment_method obrigatório, salvo se o Receipt pendente já tiver um (é mantido).
	if method == nil {
		method = pag.PaymentMethod
	}
	if err := finance.CheckMethodForStatus(finance.ReceiptReceived, method); err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Determine data_pagamento.
	var dataPag time.Time
	if isNonEmptyDateStr(req.DataPagamento) {
		t, err := parseDate(*req.DataPagamento)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "data_pagamento inválida (formato: YYYY-MM-DD)")
			return
		}
		dataPag = t
	} else {
		dataPag = todayDate()
	}

	pag.Status = finance.ReceiptReceived
	pag.PaymentMethod = method
	pag.DataPagamento = &dataPag

	if err := h.db.WithContext(r.Context()).Save(&pag).Error; err != nil {
		log.Printf("ERROR ConfirmarPagamento save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, pagamentoToResponse(pag))
}

func (h *PagamentosHandler) delete(w http.ResponseWriter, r *http.Request) {
	vendaID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id da venda inválido")
		return
	}

	pagID, err := uuid.Parse(r.PathValue("pagId"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do pagamento inválido")
		return
	}

	if !h.loadVendaForWrite(r, w, vendaID) {
		return
	}

	// Soft-delete (RN-08): gorm.DeletedAt transforma o Delete em UPDATE deleted_at.
	result := h.db.WithContext(r.Context()).
		Where("id = ? AND venda_id = ?", pagID, vendaID).
		Delete(&models.PagamentoCliente{})
	if result.Error != nil {
		log.Printf("ERROR DeletePagamento: %v", result.Error)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if result.RowsAffected == 0 {
		models.WriteError(w, http.StatusNotFound, "pagamento não encontrado")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
