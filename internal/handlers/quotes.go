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
	"gorm.io/gorm/clause"
)

// --- Valid quote_status values (AYD-001) ---

var validQuoteStatuses = map[string]bool{
	"OPEN": true,
	"WON":  true,
	"LOST": true,
}

// --- Handler ---

type QuotesHandler struct {
	db *gorm.DB
}

func NewQuotesHandler(db *gorm.DB) *QuotesHandler {
	return &QuotesHandler{db: db}
}

func (h *QuotesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /quotes", h.list)
	mux.HandleFunc("POST /quotes", h.create)
	mux.HandleFunc("GET /quotes/{id}", h.get)
	mux.HandleFunc("PUT /quotes/{id}", h.update)
	mux.HandleFunc("DELETE /quotes/{id}", h.delete)

	mux.HandleFunc("GET /quotes/{id}/items", h.listItems)
	mux.HandleFunc("POST /quotes/{id}/items", h.createItem)
	mux.HandleFunc("PUT /quotes/{id}/items/{itemId}", h.updateItem)
	mux.HandleFunc("DELETE /quotes/{id}/items/{itemId}", h.deleteItem)

	mux.HandleFunc("PATCH /quotes/{id}/status", h.updateStatus)
	mux.HandleFunc("POST /quotes/{id}/convert", h.convert)
	mux.HandleFunc("POST /quotes/quick-sale", h.quickSale)
}

// --- Request types ---

type quoteItemRequest struct {
	ProductID   *uuid.UUID `json:"product_id"`
	ProductName string     `json:"product_name"`
	Quantity    float64    `json:"quantity"`
	UnitPrice   float64    `json:"unit_price"`
	UnitCost    float64    `json:"unit_cost"`
}

type quoteCreateRequest struct {
	CustomerID uuid.UUID          `json:"customer_id"`
	ReferrerID *uuid.UUID         `json:"referrer_id"`
	QuoteDate  string             `json:"quote_date"`
	Notes      *string            `json:"notes"`
	Items      []quoteItemRequest `json:"items"`
}

type quoteUpdateRequest struct {
	ReferrerID *uuid.UUID `json:"referrer_id"`
	Notes      *string    `json:"notes"`
}

type quoteStatusRequest struct {
	Status     string  `json:"status"`
	LostReason *string `json:"lost_reason"`
}

// --- Response types ---

type quoteListResponse struct {
	ID           uuid.UUID  `json:"id"`
	CustomerID   uuid.UUID  `json:"customer_id"`
	CustomerName string     `json:"customer_name"`
	ReferrerID   *uuid.UUID `json:"referrer_id"`
	ReferrerName *string    `json:"referrer_name"`
	Status       string     `json:"status"`
	LostReason   *string    `json:"lost_reason"`
	QuoteDate    string     `json:"quote_date"`
	Notes        *string    `json:"notes"`
	TotalQuote   float64    `json:"total_quote"`
	CreatedAt    time.Time  `json:"created_at"`
}

type quoteDetailResponse struct {
	quoteListResponse
	Items []quoteItemResponse `json:"items"`
}

type quoteItemResponse struct {
	ID          uuid.UUID  `json:"id"`
	ProductID   *uuid.UUID `json:"product_id"`
	ProductName string     `json:"product_name"`
	Quantity    float64    `json:"quantity"`
	UnitPrice   float64    `json:"unit_price"`
	UnitCost    float64    `json:"unit_cost"`
	TotalPrice  float64    `json:"total_price"`
	CreatedAt   time.Time  `json:"created_at"`
}

type convertResponse struct {
	SaleID  uuid.UUID `json:"sale_id"`
	QuoteID uuid.UUID `json:"quote_id"`
}

// --- Internal row type for scanning vw_quotes_resumo ---

type quoteResumoRow struct {
	ID           uuid.UUID  `gorm:"column:id"`
	CustomerID   uuid.UUID  `gorm:"column:customer_id"`
	ReferrerID   *uuid.UUID `gorm:"column:referrer_id"`
	Status       string     `gorm:"column:status"`
	LostReason   *string    `gorm:"column:lost_reason"`
	QuoteDate    time.Time  `gorm:"column:quote_date"`
	Notes        *string    `gorm:"column:notes"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	TotalQuote   float64    `gorm:"column:total_quote"`
	CustomerName string     `gorm:"column:customer_name"`
	ReferrerName *string    `gorm:"column:referrer_name"`
}

func (row quoteResumoRow) toListResponse() quoteListResponse {
	return quoteListResponse{
		ID:           row.ID,
		CustomerID:   row.CustomerID,
		CustomerName: row.CustomerName,
		ReferrerID:   row.ReferrerID,
		ReferrerName: row.ReferrerName,
		Status:       row.Status,
		LostReason:   row.LostReason,
		QuoteDate:    row.QuoteDate.Format("2006-01-02"),
		Notes:        row.Notes,
		TotalQuote:   row.TotalQuote,
		CreatedAt:    row.CreatedAt,
	}
}

func quoteItemToResponse(item models.QuoteItem) quoteItemResponse {
	return quoteItemResponse{
		ID:          item.ID,
		ProductID:   item.ProductID,
		ProductName: item.ProductName,
		Quantity:    item.Quantity,
		UnitPrice:   item.UnitPrice,
		UnitCost:    item.UnitCost,
		TotalPrice:  item.Quantity * item.UnitPrice,
		CreatedAt:   item.CreatedAt,
	}
}

// --- Helper methods ---

func (h *QuotesHandler) fetchQuoteResumo(ctx context.Context, id uuid.UUID) (*quoteResumoRow, error) {
	var row quoteResumoRow
	result := h.db.WithContext(ctx).
		Table("vw_quotes_resumo qr").
		Select("qr.*, c.nome AS customer_name, ve.nome AS referrer_name").
		Joins("JOIN clientes c ON c.id = qr.customer_id").
		Joins("LEFT JOIN vendedores_externos ve ON ve.id = qr.referrer_id").
		Where("qr.id = ?", id).
		Scan(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func (h *QuotesHandler) fetchItems(ctx context.Context, quoteID uuid.UUID) ([]models.QuoteItem, error) {
	var items []models.QuoteItem
	err := h.db.WithContext(ctx).
		Where("quote_id = ?", quoteID).
		Order("created_at ASC").
		Find(&items).Error
	return items, err
}

func (h *QuotesHandler) buildDetailResponse(ctx context.Context, quoteID uuid.UUID) (*quoteDetailResponse, error) {
	row, err := h.fetchQuoteResumo(ctx, quoteID)
	if err != nil {
		return nil, err
	}

	items, err := h.fetchItems(ctx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("fetch items: %w", err)
	}

	itemsResp := make([]quoteItemResponse, 0, len(items))
	for _, item := range items {
		itemsResp = append(itemsResp, quoteItemToResponse(item))
	}

	return &quoteDetailResponse{
		quoteListResponse: row.toListResponse(),
		Items:             itemsResp,
	}, nil
}

func validateQuoteItemFields(req quoteItemRequest) string {
	if strings.TrimSpace(req.ProductName) == "" {
		return "campo 'product_name' é obrigatório"
	}
	if req.Quantity <= 0 {
		return "campo 'quantity' deve ser maior que zero"
	}
	if req.UnitPrice < 0 {
		return "campo 'unit_price' não pode ser negativo"
	}
	if req.UnitCost < 0 {
		return "campo 'unit_cost' não pode ser negativo"
	}
	return ""
}

// fetchQuoteForWrite loads the quote and ensures it is OPEN, writing the
// appropriate error response otherwise. Returns ok=false if a response was
// already written.
func (h *QuotesHandler) fetchQuoteForWrite(w http.ResponseWriter, ctx context.Context, id uuid.UUID) (*models.Quote, bool) {
	var quote models.Quote
	if err := h.db.WithContext(ctx).First(&quote, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "quote não encontrado")
			return nil, false
		}
		log.Printf("ERROR fetchQuoteForWrite: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return nil, false
	}
	if quote.Status != "OPEN" {
		models.WriteError(w, http.StatusConflict, "quote só pode ser editado enquanto estiver 'OPEN'")
		return nil, false
	}
	return &quote, true
}

// ============================================================
// Quotes CRUD
// ============================================================

func (h *QuotesHandler) list(w http.ResponseWriter, r *http.Request) {
	query := h.db.WithContext(r.Context()).
		Table("vw_quotes_resumo qr").
		Select("qr.*, c.nome AS customer_name, ve.nome AS referrer_name").
		Joins("JOIN clientes c ON c.id = qr.customer_id").
		Joins("LEFT JOIN vendedores_externos ve ON ve.id = qr.referrer_id")

	if status := r.URL.Query().Get("status"); status != "" {
		if !validQuoteStatuses[status] {
			models.WriteError(w, http.StatusBadRequest, "status inválido")
			return
		}
		query = query.Where("qr.status = ?", status)
	}
	if from := r.URL.Query().Get("from"); from != "" {
		if _, err := parseDate(from); err != nil {
			models.WriteError(w, http.StatusBadRequest, "from inválida (formato: YYYY-MM-DD)")
			return
		}
		query = query.Where("qr.quote_date >= ?", from)
	}
	if to := r.URL.Query().Get("to"); to != "" {
		if _, err := parseDate(to); err != nil {
			models.WriteError(w, http.StatusBadRequest, "to inválida (formato: YYYY-MM-DD)")
			return
		}
		query = query.Where("qr.quote_date <= ?", to)
	}
	if customerID := r.URL.Query().Get("customer_id"); customerID != "" {
		if _, err := uuid.Parse(customerID); err != nil {
			models.WriteError(w, http.StatusBadRequest, "customer_id inválido")
			return
		}
		query = query.Where("qr.customer_id = ?", customerID)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		like := "%" + q + "%"
		query = query.Where("c.nome ILIKE ? OR qr.notes ILIKE ?", like, like)
	}

	query = query.Order("qr.quote_date DESC, qr.created_at DESC")

	var rows []quoteResumoRow
	if err := query.Scan(&rows).Error; err != nil {
		log.Printf("ERROR ListQuotes: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	quotes := make([]quoteListResponse, 0, len(rows))
	for _, row := range rows {
		quotes = append(quotes, row.toListResponse())
	}
	writeJSON(w, http.StatusOK, quotes)
}

// validateQuoteRequest valida o payload de criação (compartilhado por create e quick-sale).
// Escreve a resposta de erro e devolve ok=false quando inválido.
func (h *QuotesHandler) validateQuoteRequest(w http.ResponseWriter, ctx context.Context, req quoteCreateRequest) (time.Time, bool) {
	if req.CustomerID == uuid.Nil {
		models.WriteError(w, http.StatusBadRequest, "campo 'customer_id' é obrigatório")
		return time.Time{}, false
	}
	var cliente models.Cliente
	if err := h.db.WithContext(ctx).First(&cliente, "id = ?", req.CustomerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusBadRequest, "customer não encontrado")
			return time.Time{}, false
		}
		log.Printf("ERROR validateQuoteRequest check customer: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return time.Time{}, false
	}
	if req.ReferrerID != nil {
		var partner models.Partner
		if err := h.db.WithContext(ctx).First(&partner, "id = ?", *req.ReferrerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				models.WriteError(w, http.StatusBadRequest, "referrer não encontrado")
				return time.Time{}, false
			}
			log.Printf("ERROR validateQuoteRequest check referrer: %v", err)
			models.WriteError(w, http.StatusInternalServerError, "erro interno")
			return time.Time{}, false
		}
	}

	quoteDate := today()
	if strings.TrimSpace(req.QuoteDate) != "" {
		t, err := parseDate(req.QuoteDate)
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, "quote_date inválida (formato: YYYY-MM-DD)")
			return time.Time{}, false
		}
		quoteDate = t
	}

	for i, itemReq := range req.Items {
		if errMsg := validateQuoteItemFields(itemReq); errMsg != "" {
			models.WriteError(w, http.StatusBadRequest, fmt.Sprintf("items[%d]: %s", i, errMsg))
			return time.Time{}, false
		}
	}
	return quoteDate, true
}

// insertQuote grava o Quote e seus Items dentro da transação recebida.
func insertQuote(tx *gorm.DB, req quoteCreateRequest, quoteDate time.Time) (*models.Quote, error) {
	quote := models.Quote{
		CustomerID: req.CustomerID,
		ReferrerID: req.ReferrerID,
		Status:     "OPEN",
		QuoteDate:  quoteDate,
		Notes:      req.Notes,
	}
	if err := tx.Create(&quote).Error; err != nil {
		return nil, fmt.Errorf("insert quote: %w", err)
	}
	for _, itemReq := range req.Items {
		item := models.QuoteItem{
			QuoteID:     quote.ID,
			ProductID:   itemReq.ProductID,
			ProductName: strings.TrimSpace(itemReq.ProductName),
			Quantity:    itemReq.Quantity,
			UnitPrice:   itemReq.UnitPrice,
			UnitCost:    itemReq.UnitCost,
		}
		if err := tx.Create(&item).Error; err != nil {
			return nil, fmt.Errorf("insert quote item: %w", err)
		}
	}
	return &quote, nil
}

func (h *QuotesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req quoteCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	quoteDate, ok := h.validateQuoteRequest(w, r.Context(), req)
	if !ok {
		return
	}

	tx := h.db.WithContext(r.Context()).Begin()
	defer tx.Rollback()

	quote, err := insertQuote(tx, req, quoteDate)
	if err != nil {
		log.Printf("ERROR CreateQuote: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if err := tx.Commit().Error; err != nil {
		log.Printf("ERROR CreateQuote commit: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	resp, err := h.buildDetailResponse(r.Context(), quote.ID)
	if err != nil {
		log.Printf("ERROR CreateQuote fetch detail: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *QuotesHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	resp, err := h.buildDetailResponse(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "quote não encontrado")
			return
		}
		log.Printf("ERROR GetQuote: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *QuotesHandler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req quoteUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	quote, ok := h.fetchQuoteForWrite(w, r.Context(), id)
	if !ok {
		return
	}

	quote.ReferrerID = req.ReferrerID
	quote.Notes = req.Notes

	if err := h.db.WithContext(r.Context()).Save(quote).Error; err != nil {
		log.Printf("ERROR UpdateQuote save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	resp, err := h.buildDetailResponse(r.Context(), id)
	if err != nil {
		log.Printf("ERROR UpdateQuote fetch detail: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *QuotesHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var quote models.Quote
	if err := h.db.WithContext(r.Context()).First(&quote, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "quote não encontrado")
			return
		}
		log.Printf("ERROR SoftDeleteQuote find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	// RN-01: a Sale mantém referência ao Quote de origem — Quote convertido não se apaga.
	if quote.Status == "WON" {
		models.WriteError(w, http.StatusConflict, "quote convertido em Sale não pode ser excluído")
		return
	}

	if err := h.db.WithContext(r.Context()).Delete(&quote).Error; err != nil {
		log.Printf("ERROR SoftDeleteQuote: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Quote Items CRUD (só enquanto OPEN)
// ============================================================

func (h *QuotesHandler) listItems(w http.ResponseWriter, r *http.Request) {
	quoteID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var quote models.Quote
	if err := h.db.WithContext(r.Context()).First(&quote, "id = ?", quoteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "quote não encontrado")
			return
		}
		log.Printf("ERROR ListQuoteItems check quote: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	items, err := h.fetchItems(r.Context(), quoteID)
	if err != nil {
		log.Printf("ERROR ListQuoteItems: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	resp := make([]quoteItemResponse, 0, len(items))
	for _, item := range items {
		resp = append(resp, quoteItemToResponse(item))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *QuotesHandler) createItem(w http.ResponseWriter, r *http.Request) {
	quoteID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req quoteItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if _, ok := h.fetchQuoteForWrite(w, r.Context(), quoteID); !ok {
		return
	}

	if errMsg := validateQuoteItemFields(req); errMsg != "" {
		models.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}

	item := models.QuoteItem{
		QuoteID:     quoteID,
		ProductID:   req.ProductID,
		ProductName: strings.TrimSpace(req.ProductName),
		Quantity:    req.Quantity,
		UnitPrice:   req.UnitPrice,
		UnitCost:    req.UnitCost,
	}

	if err := h.db.WithContext(r.Context()).Create(&item).Error; err != nil {
		log.Printf("ERROR CreateQuoteItem: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusCreated, quoteItemToResponse(item))
}

func (h *QuotesHandler) updateItem(w http.ResponseWriter, r *http.Request) {
	quoteID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do quote inválido")
		return
	}

	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do item inválido")
		return
	}

	var req quoteItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if _, ok := h.fetchQuoteForWrite(w, r.Context(), quoteID); !ok {
		return
	}

	if errMsg := validateQuoteItemFields(req); errMsg != "" {
		models.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}

	var item models.QuoteItem
	if err := h.db.WithContext(r.Context()).First(&item, "id = ? AND quote_id = ?", itemID, quoteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "item não encontrado")
			return
		}
		log.Printf("ERROR UpdateQuoteItem find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	item.ProductID = req.ProductID
	item.ProductName = strings.TrimSpace(req.ProductName)
	item.Quantity = req.Quantity
	item.UnitPrice = req.UnitPrice
	item.UnitCost = req.UnitCost

	if err := h.db.WithContext(r.Context()).Save(&item).Error; err != nil {
		log.Printf("ERROR UpdateQuoteItem save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, quoteItemToResponse(item))
}

func (h *QuotesHandler) deleteItem(w http.ResponseWriter, r *http.Request) {
	quoteID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do quote inválido")
		return
	}

	itemID, err := uuid.Parse(r.PathValue("itemId"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id do item inválido")
		return
	}

	if _, ok := h.fetchQuoteForWrite(w, r.Context(), quoteID); !ok {
		return
	}

	var item models.QuoteItem
	if err := h.db.WithContext(r.Context()).First(&item, "id = ? AND quote_id = ?", itemID, quoteID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "item não encontrado")
			return
		}
		log.Printf("ERROR DeleteQuoteItem find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	if err := h.db.WithContext(r.Context()).Delete(&item).Error; err != nil {
		log.Printf("ERROR DeleteQuoteItem: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Status transition (OPEN <-> LOST; WON só via /convert)
// ============================================================

func (h *QuotesHandler) updateStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	var req quoteStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if !validQuoteStatuses[req.Status] {
		models.WriteError(w, http.StatusBadRequest, "status inválido")
		return
	}
	if req.Status == "WON" {
		models.WriteError(w, http.StatusConflict, "WON só é alcançado via POST /quotes/{id}/convert")
		return
	}

	var quote models.Quote
	if err := h.db.WithContext(r.Context()).First(&quote, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			models.WriteError(w, http.StatusNotFound, "quote não encontrado")
			return
		}
		log.Printf("ERROR UpdateQuoteStatus find: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if quote.Status == "WON" {
		models.WriteError(w, http.StatusConflict, "quote já convertido em Sale ('WON' é terminal)")
		return
	}

	quote.Status = req.Status
	if req.Status == "LOST" {
		quote.LostReason = req.LostReason
	} else {
		quote.LostReason = nil
	}

	if err := h.db.WithContext(r.Context()).Save(&quote).Error; err != nil {
		log.Printf("ERROR UpdateQuoteStatus save: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          quote.ID,
		"status":      quote.Status,
		"lost_reason": quote.LostReason,
	})
}

// ============================================================
// Convert: Quote OPEN -> Sale (transacional, RF-07 / RN-01)
// ============================================================

// convertError carrega o status HTTP de uma falha de regra de negócio na conversão.
type convertError struct {
	status int
	msg    string
}

func (e *convertError) Error() string { return e.msg }

// convertQuote converte o Quote em Sale dentro da transação recebida. O Quote é lido com
// lock de linha (FOR UPDATE): conversões concorrentes do mesmo Quote são serializadas e as
// seguintes recebem 409 em vez de falhar na constraint UNIQUE de vendas.quote_id.
func convertQuote(tx *gorm.DB, id uuid.UUID) (*models.Quote, *models.Venda, error) {
	var quote models.Quote
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&quote, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, &convertError{http.StatusNotFound, "quote não encontrado"}
		}
		return nil, nil, err
	}
	if quote.Status != "OPEN" {
		return nil, nil, &convertError{http.StatusConflict, "apenas quotes 'OPEN' podem ser convertidos"}
	}

	var items []models.QuoteItem
	if err := tx.Where("quote_id = ?", id).Find(&items).Error; err != nil {
		return nil, nil, err
	}
	if len(items) == 0 {
		return nil, nil, &convertError{http.StatusUnprocessableEntity, "quote sem items não pode ser convertido"}
	}

	quoteID := quote.ID
	venda := models.Venda{
		ClienteID:         quote.CustomerID,
		VendedorExternoID: quote.ReferrerID,
		QuoteID:           &quoteID,
		Status:            "A_ENTREGAR",
		DataVenda:         today(),
		Observacoes:       quote.Notes,
	}
	if err := tx.Create(&venda).Error; err != nil {
		return nil, nil, err
	}
	// Supplier e origem do Item são definidos na Sale (PDR-001 / AYD-002), não no Quote.
	for _, quoteItem := range items {
		item := models.ItemVenda{
			VendaID:        venda.ID,
			ProdutoID:      quoteItem.ProductID,
			ProdutoNome:    quoteItem.ProductName,
			Quantidade:     quoteItem.Quantity,
			PrecoVendaUnit: quoteItem.UnitPrice,
			CustoUnit:      quoteItem.UnitCost,
		}
		if err := tx.Create(&item).Error; err != nil {
			return nil, nil, err
		}
	}

	quote.Status = "WON"
	if err := tx.Save(&quote).Error; err != nil {
		return nil, nil, err
	}
	return &quote, &venda, nil
}

func writeConvertError(w http.ResponseWriter, op string, err error) {
	var ce *convertError
	if errors.As(err, &ce) {
		models.WriteError(w, ce.status, ce.msg)
		return
	}
	log.Printf("ERROR %s: %v", op, err)
	models.WriteError(w, http.StatusInternalServerError, "erro interno")
}

func (h *QuotesHandler) convert(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}

	tx := h.db.WithContext(r.Context()).Begin()
	defer tx.Rollback()

	quote, venda, err := convertQuote(tx, id)
	if err != nil {
		writeConvertError(w, "ConvertQuote", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		log.Printf("ERROR ConvertQuote commit: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusCreated, convertResponse{SaleID: venda.ID, QuoteID: quote.ID})
}

// quickSale cria o Quote e o converte em Sale na mesma transação (venda rápida — AYD-001).
func (h *QuotesHandler) quickSale(w http.ResponseWriter, r *http.Request) {
	var req quoteCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if len(req.Items) == 0 {
		models.WriteError(w, http.StatusUnprocessableEntity, "venda rápida exige ao menos 1 item")
		return
	}
	quoteDate, ok := h.validateQuoteRequest(w, r.Context(), req)
	if !ok {
		return
	}

	tx := h.db.WithContext(r.Context()).Begin()
	defer tx.Rollback()

	created, err := insertQuote(tx, req, quoteDate)
	if err != nil {
		log.Printf("ERROR QuickSale: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	quote, venda, err := convertQuote(tx, created.ID)
	if err != nil {
		writeConvertError(w, "QuickSale convert", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		log.Printf("ERROR QuickSale commit: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusCreated, convertResponse{SaleID: venda.ID, QuoteID: quote.ID})
}
