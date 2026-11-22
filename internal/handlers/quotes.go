package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/closing"
	"github.com/silvioubaldino/sales-backend/internal/fulfillment"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/saleitem"
	"github.com/silvioubaldino/sales-backend/internal/search"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var validQuoteStatuses = map[string]bool{
	"OPEN": true,
	"WON":  true,
	"LOST": true,
}

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

type quoteItemRequest struct {
	ProductID   *uuid.UUID `json:"product_id"`
	ProductName string     `json:"product_name"`
	Quantity    float64    `json:"quantity"`
	UnitPrice   float64    `json:"unit_price"`
	UnitCost    float64    `json:"unit_cost"`
	Kind        *string    `json:"kind"`
	ServiceID   *uuid.UUID `json:"service_id"`
}

type quoteChargeRequest struct {
	ID          *uuid.UUID `json:"id"`
	Kind        string     `json:"kind"`
	Amount      float64    `json:"amount"`
	Description *string    `json:"description"`
}

type quoteChargeResponse struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	Amount      float64   `json:"amount"`
	Description *string   `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type quoteCreateRequest struct {
	CustomerID uuid.UUID            `json:"customer_id"`
	ReferrerID *uuid.UUID           `json:"referrer_id"`
	QuoteDate  string               `json:"quote_date"`
	Notes      *string              `json:"notes"`
	Items      []quoteItemRequest   `json:"items"`
	Charges    []quoteChargeRequest `json:"charges"`
	// Só vale em POST /quotes/quick-sale (fechamento da venda).
	Closing *closing.Request `json:"closing"`
}

type quoteUpdateRequest struct {
	ReferrerID *uuid.UUID            `json:"referrer_id"`
	Notes      *string               `json:"notes"`
	CustomerID *uuid.UUID            `json:"customer_id"`
	QuoteDate  *string               `json:"quote_date"`
	Charges    *[]quoteChargeRequest `json:"charges"`
}

type quoteStatusRequest struct {
	Status     string  `json:"status"`
	LostReason *string `json:"lost_reason"`
}

type quoteListResponse struct {
	ID            uuid.UUID  `json:"id"`
	Number        int        `json:"number"`
	CustomerID    uuid.UUID  `json:"customer_id"`
	CustomerName  string     `json:"customer_name"`
	ReferrerID    *uuid.UUID `json:"referrer_id"`
	ReferrerName  *string    `json:"referrer_name"`
	Status        string     `json:"status"`
	LostReason    *string    `json:"lost_reason"`
	QuoteDate     string     `json:"quote_date"`
	Notes         *string    `json:"notes"`
	QuickSale     bool       `json:"quick_sale"`
	TotalQuote    float64    `json:"total_quote"`
	ItemsTotal    float64    `json:"items_total"`
	ChargesTotal  float64    `json:"charges_total"`
	CreatedAt     time.Time  `json:"created_at"`
	SaleID        *uuid.UUID `json:"sale_id"`
	SaleNumber    *int       `json:"sale_number"`
	ItemsCount    int        `json:"items_count"`
	FirstItemName *string    `json:"first_item_name"`
}

type quoteDetailResponse struct {
	quoteListResponse
	Items   []quoteItemResponse   `json:"items"`
	Charges []quoteChargeResponse `json:"charges"`
}

type quoteItemResponse struct {
	ID          uuid.UUID  `json:"id"`
	ProductID   *uuid.UUID `json:"product_id"`
	ProductName string     `json:"product_name"`
	Quantity    float64    `json:"quantity"`
	UnitPrice   float64    `json:"unit_price"`
	UnitCost    float64    `json:"unit_cost"`
	Kind        string     `json:"kind"`
	ServiceID   *uuid.UUID `json:"service_id"`
	TotalPrice  float64    `json:"total_price"`
	CreatedAt   time.Time  `json:"created_at"`
}

type convertResponse struct {
	SaleID     uuid.UUID        `json:"sale_id"`
	QuoteID    uuid.UUID        `json:"quote_id"`
	SaleNumber int              `json:"sale_number"`
	Warnings   []closingWarning `json:"warnings"`
}

func saleNumber(db *gorm.DB, saleID uuid.UUID) (int, error) {
	var n int
	err := db.Raw(`SELECT number FROM vendas WHERE id = ?`, saleID).Scan(&n).Error
	return n, err
}

type quoteResumoRow struct {
	ID           uuid.UUID  `gorm:"column:id"`
	Number       int        `gorm:"column:number"`
	CustomerID   uuid.UUID  `gorm:"column:customer_id"`
	ReferrerID   *uuid.UUID `gorm:"column:referrer_id"`
	Status       string     `gorm:"column:status"`
	LostReason   *string    `gorm:"column:lost_reason"`
	QuoteDate    time.Time  `gorm:"column:quote_date"`
	Notes        *string    `gorm:"column:notes"`
	QuickSale    bool       `gorm:"column:quick_sale"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	TotalQuote   float64    `gorm:"column:total_quote"`
	ItemsTotal   float64    `gorm:"column:items_total"`
	ChargesTotal float64    `gorm:"column:charges_total"`
	CustomerName string     `gorm:"column:customer_name"`
	ReferrerName *string    `gorm:"column:referrer_name"`

	SaleID        *uuid.UUID `gorm:"column:sale_id"`
	SaleNumber    *int       `gorm:"column:sale_number"`
	ItemsCount    int        `gorm:"column:items_count"`
	FirstItemName *string    `gorm:"column:first_item_name"`
}

func (row quoteResumoRow) toListResponse() quoteListResponse {
	return quoteListResponse{
		ID:            row.ID,
		Number:        row.Number,
		SaleID:        row.SaleID,
		SaleNumber:    row.SaleNumber,
		ItemsCount:    row.ItemsCount,
		FirstItemName: row.FirstItemName,
		CustomerID:    row.CustomerID,
		CustomerName:  row.CustomerName,
		ReferrerID:    row.ReferrerID,
		ReferrerName:  row.ReferrerName,
		Status:        row.Status,
		LostReason:    row.LostReason,
		QuoteDate:     row.QuoteDate.Format("2006-01-02"),
		Notes:         row.Notes,
		QuickSale:     row.QuickSale,
		TotalQuote:    row.TotalQuote,
		ItemsTotal:    row.ItemsTotal,
		ChargesTotal:  row.ChargesTotal,
		CreatedAt:     row.CreatedAt,
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
		Kind:        item.Kind,
		ServiceID:   item.ServiceID,
		TotalPrice:  item.Quantity * item.UnitPrice,
		CreatedAt:   item.CreatedAt,
	}
}

// quoteResumoQuery lê vw_quotes_resumo com cliente, indicador, número humano (C1), a Sale criada (C2) e o resumo dos Items (C3 — subconsultas escalares, sem fan-out).
func quoteResumoQuery(db *gorm.DB) *gorm.DB {
	return db.Table("vw_quotes_resumo qr").
		Select(`qr.*, qn.number AS number, c.nome AS customer_name, ve.nome AS referrer_name,
			sv.id AS sale_id, sv.number AS sale_number,
			(SELECT COUNT(*) FROM quote_items qi WHERE qi.quote_id = qr.id) AS items_count,
			(SELECT qi.product_name FROM quote_items qi WHERE qi.quote_id = qr.id
			  ORDER BY qi.created_at, qi.id LIMIT 1) AS first_item_name`).
		Joins("JOIN quotes qn ON qn.id = qr.id").
		Joins("JOIN clientes c ON c.id = qr.customer_id").
		Joins("LEFT JOIN vendedores_externos ve ON ve.id = qr.referrer_id").
		Joins("LEFT JOIN vendas sv ON sv.quote_id = qr.id AND sv.deleted_at IS NULL")
}

func (h *QuotesHandler) fetchQuoteResumo(ctx context.Context, id uuid.UUID) (*quoteResumoRow, error) {
	var row quoteResumoRow
	result := quoteResumoQuery(h.db.WithContext(ctx)).
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

	charges, err := fetchQuoteCharges(h.db.WithContext(ctx), quoteID)
	if err != nil {
		return nil, fmt.Errorf("fetch charges: %w", err)
	}

	return &quoteDetailResponse{
		quoteListResponse: row.toListResponse(),
		Items:             itemsResp,
		Charges:           charges,
	}, nil
}

func fetchQuoteCharges(db *gorm.DB, quoteID uuid.UUID) ([]quoteChargeResponse, error) {
	out := []quoteChargeResponse{}
	err := db.Raw(`SELECT id, kind::text AS kind, amount, description, created_at
		FROM quote_charges WHERE quote_id = ? AND deleted_at IS NULL ORDER BY created_at, id`, quoteID).Scan(&out).Error
	return out, err
}

func validateQuoteCharges(charges []quoteChargeRequest) error {
	for i, c := range charges {
		if _, err := validateChargeFields(c.Kind, c.Amount, c.Description); err != nil {
			var he *httpError
			if errors.As(err, &he) {
				return errStatus(he.status, fmt.Sprintf("charges[%d]: %s", i, he.msg))
			}
			return err
		}
	}
	return nil
}

func replaceQuoteCharges(tx *gorm.DB, quoteID uuid.UUID, charges []quoteChargeRequest) error {
	var existing []models.QuoteCharge
	if err := tx.Where("quote_id = ?", quoteID).Find(&existing).Error; err != nil {
		return err
	}
	keep := map[uuid.UUID]bool{}
	for _, c := range charges {
		desc := nullIfBlank(c.Description)
		if c.ID != nil {
			found := false
			for _, e := range existing {
				if e.ID == *c.ID {
					found = true
				}
			}
			if !found {
				return errStatus(http.StatusBadRequest, "charges: id não pertence ao quote")
			}
			if err := tx.Model(&models.QuoteCharge{}).Where("id = ?", *c.ID).
				Updates(map[string]any{"kind": c.Kind, "amount": c.Amount, "description": desc}).Error; err != nil {
				return err
			}
			keep[*c.ID] = true
			continue
		}
		row := models.QuoteCharge{QuoteID: quoteID, Kind: c.Kind, Amount: c.Amount, Description: desc}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		keep[row.ID] = true
	}
	for _, e := range existing {
		if !keep[e.ID] {
			if err := tx.Delete(&models.QuoteCharge{}, "id = ?", e.ID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func validateQuoteItemFields(req quoteItemRequest) string {
	if strings.TrimSpace(req.ProductName) == "" && req.ServiceID == nil {
		return "campo 'product_name' é obrigatório"
	}
	kind, ok := saleitem.Resolve(req.Kind)
	if !ok {
		return "kind inválido (PRODUCT | SERVICE)"
	}
	if msg := saleitem.Validate(saleitem.Fields{Kind: kind, HasProduct: req.ProductID != nil, HasService: req.ServiceID != nil}); msg != "" {
		return msg
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

func resolveQuoteItem(db *gorm.DB, req quoteItemRequest, current *uuid.UUID) (string, string, error) {
	kind, _ := saleitem.Resolve(req.Kind)
	name := strings.TrimSpace(req.ProductName)
	svc, err := checkServiceRef(db, req.ServiceID, current)
	if err != nil {
		return "", "", err
	}
	if name == "" && svc != nil {
		name = svc.Name
	}
	return kind, name, nil
}

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

func (h *QuotesHandler) list(w http.ResponseWriter, r *http.Request) {
	query := quoteResumoQuery(h.db.WithContext(r.Context()))

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
	if ref := r.URL.Query().Get("referrer_id"); ref != "" {
		if _, err := uuid.Parse(ref); err != nil {
			models.WriteError(w, http.StatusBadRequest, "referrer_id inválido")
			return
		}
		query = query.Where("qr.referrer_id = ?", ref)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		like := "%" + q + "%"
		if n, ok := search.AsNumber(q); ok {
			query = query.Where("c.nome ILIKE ? OR qr.notes ILIKE ? OR qn.number = ?", like, like, n)
		} else {
			query = query.Where("c.nome ILIKE ? OR qr.notes ILIKE ?", like, like)
		}
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
		if _, _, err := resolveQuoteItem(h.db.WithContext(ctx), itemReq, nil); err != nil {
			writeTxError(w, "validateQuoteRequest service", err)
			return time.Time{}, false
		}
	}
	if err := validateQuoteCharges(req.Charges); err != nil {
		var he *httpError
		if errors.As(err, &he) {
			models.WriteError(w, he.status, he.msg)
		}
		return time.Time{}, false
	}
	return quoteDate, true
}

func insertQuote(tx *gorm.DB, req quoteCreateRequest, quoteDate time.Time, quickSale bool) (*models.Quote, []models.QuoteItem, error) {
	quote := models.Quote{
		CustomerID: req.CustomerID,
		ReferrerID: req.ReferrerID,
		Status:     "OPEN",
		QuoteDate:  quoteDate,
		Notes:      req.Notes,
		QuickSale:  quickSale,
	}
	if err := tx.Create(&quote).Error; err != nil {
		return nil, nil, fmt.Errorf("insert quote: %w", err)
	}
	created := make([]models.QuoteItem, 0, len(req.Items))
	for _, itemReq := range req.Items {
		kind, name, err := resolveQuoteItem(tx, itemReq, nil)
		if err != nil {
			return nil, nil, err
		}
		item := models.QuoteItem{
			QuoteID:     quote.ID,
			ProductID:   itemReq.ProductID,
			ProductName: name,
			Quantity:    itemReq.Quantity,
			UnitPrice:   itemReq.UnitPrice,
			UnitCost:    itemReq.UnitCost,
			Kind:        kind,
			ServiceID:   itemReq.ServiceID,
		}
		if err := tx.Create(&item).Error; err != nil {
			return nil, nil, fmt.Errorf("insert quote item: %w", err)
		}
		created = append(created, item)
	}
	for _, c := range req.Charges {
		row := models.QuoteCharge{QuoteID: quote.ID, Kind: c.Kind, Amount: c.Amount, Description: nullIfBlank(c.Description)}
		if err := tx.Create(&row).Error; err != nil {
			return nil, nil, fmt.Errorf("insert quote charge: %w", err)
		}
	}
	return &quote, created, nil
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

	quote, _, err := insertQuote(tx, req, quoteDate, false)
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

	if err := h.applyQuoteHeader(r.Context(), quote, req); err != nil {
		writeTxError(w, "UpdateQuote header", err)
		return
	}
	quote.ReferrerID = req.ReferrerID
	quote.Notes = req.Notes

	if req.Charges != nil {
		if err := validateQuoteCharges(*req.Charges); err != nil {
			writeTxError(w, "UpdateQuote charges", err)
			return
		}
	}
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(quote).Error; err != nil {
			return err
		}
		if req.Charges != nil {
			return replaceQuoteCharges(tx, quote.ID, *req.Charges)
		}
		return nil
	})
	if err != nil {
		writeTxError(w, "UpdateQuote save", err)
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

func (h *QuotesHandler) applyQuoteHeader(ctx context.Context, quote *models.Quote, req quoteUpdateRequest) error {
	if req.CustomerID != nil && *req.CustomerID != quote.CustomerID {
		var n int64
		if err := h.db.WithContext(ctx).Model(&models.Cliente{}).Where("id = ?", *req.CustomerID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return errStatus(http.StatusBadRequest, "customer não encontrado")
		}
		quote.CustomerID = *req.CustomerID
	}
	if req.QuoteDate != nil {
		d, err := parseDate(*req.QuoteDate)
		if err != nil {
			return errStatus(http.StatusBadRequest, "quote_date inválida (formato: YYYY-MM-DD)")
		}
		quote.QuoteDate = d
	}
	return nil
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

	kind, name, err := resolveQuoteItem(h.db.WithContext(r.Context()), req, nil)
	if err != nil {
		writeTxError(w, "CreateQuoteItem service", err)
		return
	}
	item := models.QuoteItem{
		QuoteID:     quoteID,
		ProductID:   req.ProductID,
		ProductName: name,
		Quantity:    req.Quantity,
		UnitPrice:   req.UnitPrice,
		UnitCost:    req.UnitCost,
		Kind:        kind,
		ServiceID:   req.ServiceID,
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

	if req.Kind != nil {
		if k, _ := saleitem.Resolve(req.Kind); k != item.Kind {
			models.WriteError(w, http.StatusConflict, "o tipo (kind) do item não muda: remova o item e adicione outro")
			return
		}
	}
	req.Kind = &item.Kind
	if errMsg := validateQuoteItemFields(req); errMsg != "" {
		models.WriteError(w, http.StatusBadRequest, errMsg)
		return
	}
	_, name, err := resolveQuoteItem(h.db.WithContext(r.Context()), req, item.ServiceID)
	if err != nil {
		writeTxError(w, "UpdateQuoteItem service", err)
		return
	}
	item.ProductID = req.ProductID
	item.ProductName = name
	item.ServiceID = req.ServiceID
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

type convertError struct {
	status int
	msg    string
}

func (e *convertError) Error() string { return e.msg }

type convertedItem struct {
	quote models.QuoteItem
	sale  models.ItemVenda
}

type convertResult struct {
	quote *models.Quote
	sale  *models.Venda
	items []convertedItem
}

// O Quote é lido com lock de linha (FOR UPDATE): conversões concorrentes do mesmo Quote são serializadas e as seguintes recebem 409 em vez de falhar na constraint UNIQUE de vendas.quote_id.
func convertQuote(tx *gorm.DB, id uuid.UUID, run *closingRun) (*convertResult, error) {
	var quote models.Quote
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&quote, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &convertError{http.StatusNotFound, "quote não encontrado"}
		}
		return nil, err
	}
	if quote.Status != "OPEN" {
		return nil, &convertError{http.StatusConflict, "apenas quotes 'OPEN' podem ser convertidos"}
	}

	var items []models.QuoteItem
	if err := tx.Where("quote_id = ?", id).Order("created_at, id").Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, &convertError{http.StatusUnprocessableEntity, "quote sem items não pode ser convertido"}
	}
	decisions, err := run.resolveItems(tx, items)
	if err != nil {
		return nil, err
	}

	quoteID := quote.ID
	venda := models.Venda{
		ClienteID:           quote.CustomerID,
		VendedorExternoID:   quote.ReferrerID,
		QuoteID:             &quoteID,
		DeliveryStatus:      fulfillment.DeliveryToDeliver,
		DataVenda:           today(),
		DataEntregaPrevista: run.deliveryDue(),
		Observacoes:         quote.Notes,
	}
	if err := tx.Create(&venda).Error; err != nil {
		return nil, err
	}
	res := &convertResult{quote: &quote, sale: &venda}
	for _, quoteItem := range items {
		item, err := newSaleItem(tx, venda.ID, quoteItem, decisions[quoteItem.ID])
		if err != nil {
			return nil, err
		}
		if err := tx.Create(item).Error; err != nil {
			return nil, err
		}
		if quoteItem.Kind == saleitem.KindService {
			if amount := saleitem.LaborCostAmount(quoteItem.Quantity, quoteItem.UnitCost); amount > 0 {
				desc := quoteItem.ProductName
				cost := models.SaleCost{SaleID: venda.ID, Kind: "LABOR", Amount: amount, Description: &desc, SaleItemID: &item.ID,
					PayeeSupplierID: decisions[quoteItem.ID].LaborPayeeID}
				if err := tx.Create(&cost).Error; err != nil {
					return nil, err
				}
			}
		}
		res.items = append(res.items, convertedItem{quote: quoteItem, sale: *item})
	}

	var qcharges []models.QuoteCharge
	if err := tx.Where("quote_id = ?", id).Order("created_at, id").Find(&qcharges).Error; err != nil {
		return nil, err
	}
	for _, qc := range qcharges {
		sc := models.SaleCharge{SaleID: venda.ID, Kind: qc.Kind, Amount: qc.Amount, Description: qc.Description}
		if err := tx.Create(&sc).Error; err != nil {
			return nil, err
		}
	}

	quote.Status = "WON"
	if err := tx.Save(&quote).Error; err != nil {
		return nil, err
	}
	return res, nil
}

// FROM_STOCK usa o custo da última entrada (RN-16), mantendo o custo estimado do Quote se o Product nunca teve entrada.
func newSaleItem(tx *gorm.DB, saleID uuid.UUID, qi models.QuoteItem, d closing.Decision) (*models.ItemVenda, error) {
	origin := d.Origin
	if origin == "" {
		origin = fulfillment.OriginMadeToOrder
	}
	item := models.ItemVenda{
		VendaID:        saleID,
		ProdutoID:      qi.ProductID,
		ProdutoNome:    qi.ProductName,
		FornecedorID:   d.SupplierID,
		Quantidade:     qi.Quantity,
		PrecoVendaUnit: qi.UnitPrice,
		CustoUnit:      qi.UnitCost,
		Origin:         &origin,
		Kind:           qi.Kind,
		ServiceID:      qi.ServiceID,
	}
	switch {
	case qi.Kind == saleitem.KindService:
		item.Origin, item.CustoUnit = nil, 0
	case origin == fulfillment.OriginFromStock && qi.ProductID != nil:
		cost, err := lastInflowCost(tx, *qi.ProductID)
		if err != nil {
			return nil, err
		}
		if cost != nil {
			item.CustoUnit = *cost
		}
	}
	return &item, nil
}

func writeConvertError(w http.ResponseWriter, op string, err error) {
	var ce *convertError
	if errors.As(err, &ce) {
		models.WriteError(w, ce.status, ce.msg)
		return
	}
	var fe *closing.FieldError
	if errors.As(err, &fe) {
		writeFieldError(w, fe.Status, fe.Msg, fe.Field)
		return
	}
	var he *httpError
	if errors.As(err, &he) {
		models.WriteError(w, he.status, he.msg)
		return
	}
	log.Printf("ERROR %s: %v", op, err)
	models.WriteError(w, http.StatusInternalServerError, "erro interno")
}

func decodeOptionalBody(r *http.Request, v any) error {
	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (h *QuotesHandler) convert(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var body struct {
		Closing *closing.Request `json:"closing"`
	}
	if err := decodeOptionalBody(r, &body); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	run, err := newClosingRun(body.Closing, closing.ByQuoteItemID, nil)
	if err != nil {
		writeConvertError(w, "ConvertQuote closing", err)
		return
	}

	var resp convertResponse
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) (err error) {
		resp, err = closeSale(tx, func() (*convertResult, error) { return convertQuote(tx, id, run) }, run)
		return err
	})
	if err != nil {
		writeConvertError(w, "ConvertQuote", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

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
	run, err := newClosingRun(req.Closing, closing.ByItemIndex, nil)
	if err != nil {
		writeConvertError(w, "QuickSale closing", err)
		return
	}

	var resp convertResponse
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		created, createdItems, err := insertQuote(tx, req, quoteDate, true)
		if err != nil {
			return err
		}
		run.setCreatedOrder(createdItems)
		resp, err = closeSale(tx, func() (*convertResult, error) { return convertQuote(tx, created.ID, run) }, run)
		return err
	})
	if err != nil {
		writeConvertError(w, "QuickSale", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}
