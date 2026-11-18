package handlers

// Stock (AYD-002@context): Balance por Product sempre calculado (vw_stock_balance) =
// Inflows (reposição recebida) − Outflows (Items FROM_STOCK de Sales não canceladas) ±
// ajustes. Ajustes são imutáveis (sem PUT/DELETE). Substitui as rotas legadas /estoque.

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/fulfillment"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"gorm.io/gorm"
)

type StockHandler struct {
	db *gorm.DB
}

func NewStockHandler(db *gorm.DB) *StockHandler {
	return &StockHandler{db: db}
}

func (h *StockHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /stock/balance", h.listBalance)
	mux.HandleFunc("GET /stock/balance/{productId}", h.getBalance)
	mux.HandleFunc("POST /stock/adjustments", h.createAdjustment)
	mux.HandleFunc("GET /stock/adjustments", h.listAdjustments)
}

// --- Types ---

type stockBalanceRow struct {
	ProductID      uuid.UUID  `gorm:"column:product_id"`
	ProductName    string     `gorm:"column:product_name"`
	Unit           *string    `gorm:"column:unit"`
	InflowQty      float64    `gorm:"column:inflow_qty"`
	OutflowQty     float64    `gorm:"column:outflow_qty"`
	AdjustmentQty  float64    `gorm:"column:adjustment_qty"`
	Balance        float64    `gorm:"column:balance"`
	LastInflowCost *float64   `gorm:"column:last_inflow_cost"`
	LastInflowDate *time.Time `gorm:"column:last_inflow_date"`
	StockValue     float64    `gorm:"column:stock_value"`
}

type stockBalanceResponse struct {
	ProductID      uuid.UUID `json:"product_id"`
	ProductName    string    `json:"product_name"`
	Unit           *string   `json:"unit"`
	InflowQty      float64   `json:"inflow_qty"`
	OutflowQty     float64   `json:"outflow_qty"`
	AdjustmentQty  float64   `json:"adjustment_qty"`
	Balance        float64   `json:"balance"`
	LastInflowCost *float64  `json:"last_inflow_cost"`
	LastInflowDate *string   `json:"last_inflow_date"`
	StockValue     float64   `json:"stock_value"` // GREATEST(balance, 0) × last_inflow_cost (custo de entrada — AYD-008)
}

func round2Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := obligations.Round2(*v)
	return &r
}

func (r stockBalanceRow) toResponse() stockBalanceResponse {
	return stockBalanceResponse{
		ProductID:      r.ProductID,
		ProductName:    r.ProductName,
		Unit:           r.Unit,
		InflowQty:      r.InflowQty,
		OutflowQty:     r.OutflowQty,
		AdjustmentQty:  r.AdjustmentQty,
		Balance:        r.Balance,
		LastInflowCost: round2Ptr(r.LastInflowCost),
		LastInflowDate: formatDate(r.LastInflowDate),
		StockValue:     obligations.Round2(r.StockValue),
	}
}

// stockMovement: entrada do extrato, quantity com sinal (efeito no Balance): INFLOW
// positivo (reference_id = Purchase), OUTFLOW negativo (reference_id = Sale), ADJUSTMENT
// com o próprio sinal (reference_id = ajuste).
type stockMovement struct {
	Kind        string   `json:"kind"`
	Date        string   `json:"date"`
	Quantity    float64  `json:"quantity"`
	UnitCost    *float64 `json:"unit_cost"`
	ReferenceID string   `json:"reference_id"`
	Description string   `json:"description"`
}

type stockBalanceDetailResponse struct {
	stockBalanceResponse
	Movements []stockMovement `json:"movements"`
}

type adjustmentRequest struct {
	ProductID  *uuid.UUID `json:"product_id"`
	Quantity   float64    `json:"quantity"`
	UnitCost   *float64   `json:"unit_cost"`
	Reason     string     `json:"reason"`
	AdjustedAt *string    `json:"adjusted_at"`
	Notes      *string    `json:"notes"`
}

type adjustmentResponse struct {
	ID          uuid.UUID `json:"id"`
	ProductID   uuid.UUID `json:"product_id"`
	ProductName string    `json:"product_name"`
	Quantity    float64   `json:"quantity"`
	UnitCost    *float64  `json:"unit_cost"`
	Reason      string    `json:"reason"`
	AdjustedAt  string    `json:"adjusted_at"`
	Notes       *string   `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
}

type adjustmentRow struct {
	models.StockAdjustment
	ProductName string `gorm:"column:product_name"`
}

func (r adjustmentRow) toResponse() adjustmentResponse {
	return adjustmentResponse{
		ID:          r.ID,
		ProductID:   r.ProductID,
		ProductName: r.ProductName,
		Quantity:    r.Quantity,
		UnitCost:    r.UnitCost,
		Reason:      r.Reason,
		AdjustedAt:  r.AdjustedAt.Format("2006-01-02"),
		Notes:       r.Notes,
		CreatedAt:   r.CreatedAt,
	}
}

// validateAdjustment valida o payload do ajuste (regras sem banco).
func validateAdjustment(req adjustmentRequest) string {
	if req.ProductID == nil || *req.ProductID == uuid.Nil {
		return "campo 'product_id' é obrigatório"
	}
	if req.Quantity == 0 {
		return "campo 'quantity' não pode ser zero"
	}
	if req.UnitCost != nil && *req.UnitCost < 0 {
		return "campo 'unit_cost' não pode ser negativo"
	}
	if !fulfillment.StockAdjustmentReasons[req.Reason] {
		return "reason inválido (INITIAL | COUNT | OTHER)"
	}
	return ""
}

// --- Endpoints ---

func (h *StockHandler) listBalance(w http.ResponseWriter, r *http.Request) {
	query := h.db.WithContext(r.Context()).Table("vw_stock_balance")
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		query = query.Where("product_name ILIKE ?", "%"+q+"%")
	}
	if r.URL.Query().Get("only_negative") == "true" {
		query = query.Where("balance < 0")
	}
	var rows []stockBalanceRow
	if err := query.Order("product_name ASC").Scan(&rows).Error; err != nil {
		log.Printf("ERROR ListStockBalance: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	out := make([]stockBalanceResponse, 0, len(rows))
	var totalValue float64
	for _, row := range rows {
		out = append(out, row.toResponse())
		totalValue += row.StockValue
	}
	w.Header().Set("X-Total-Stock-Value", strconv.FormatFloat(obligations.Round2(totalValue), 'f', 2, 64))
	writeJSON(w, http.StatusOK, out)
}

func (h *StockHandler) getBalance(w http.ResponseWriter, r *http.Request) {
	productID, err := uuid.Parse(r.PathValue("productId"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "productId inválido")
		return
	}
	db := h.db.WithContext(r.Context())
	var row stockBalanceRow
	res := db.Table("vw_stock_balance").Where("product_id = ?", productID).Scan(&row)
	if res.Error != nil {
		log.Printf("ERROR GetStockBalance: %v", res.Error)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if res.RowsAffected == 0 {
		models.WriteError(w, http.StatusNotFound, "produto não encontrado")
		return
	}

	type movementRow struct {
		Kind        string    `gorm:"column:kind"`
		Date        time.Time `gorm:"column:date"`
		Quantity    float64   `gorm:"column:quantity"`
		UnitCost    *float64  `gorm:"column:unit_cost"`
		ReferenceID string    `gorm:"column:reference_id"`
		Description string    `gorm:"column:description"`
	}
	var mrows []movementRow
	err = db.Raw(`
		SELECT * FROM (
			SELECT 'INFLOW' AS kind, p.received_at AS date, pi.quantity, lc.landed_unit_cost AS unit_cost,
			       p.id::text AS reference_id, 'Compra — ' || f.nome AS description, pi.created_at
			FROM purchase_items pi
			JOIN purchases p ON p.id = pi.purchase_id
			JOIN vw_purchase_line_costs lc ON lc.line_type = 'STOCK_ITEM' AND lc.line_id = pi.id
			JOIN fornecedores f ON f.id = p.supplier_id
			WHERE pi.product_id = @pid AND p.deleted_at IS NULL
			  AND p.kind = 'STOCK_REPLENISHMENT' AND p.status = 'RECEIVED'
			UNION ALL
			SELECT 'OUTFLOW', v.data_venda, -i.quantidade, i.custo_unit,
			       v.id::text, 'Venda — ' || c.nome, i.created_at
			FROM itens_venda i
			JOIN vendas v ON v.id = i.venda_id
			JOIN clientes c ON c.id = v.cliente_id
			WHERE i.produto_id = @pid AND i.origin = 'FROM_STOCK'
			  AND v.deleted_at IS NULL AND v.delivery_status <> 'CANCELED'
			UNION ALL
			SELECT 'ADJUSTMENT', a.adjusted_at, a.quantity, a.unit_cost,
			       a.id::text, 'Ajuste ' || a.reason || COALESCE(' — ' || a.notes, ''), a.created_at
			FROM stock_adjustments a
			WHERE a.product_id = @pid
		) m
		ORDER BY date DESC, created_at DESC`, map[string]any{"pid": productID}).Scan(&mrows).Error
	if err != nil {
		log.Printf("ERROR GetStockBalance movements: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	movements := make([]stockMovement, 0, len(mrows))
	for _, m := range mrows {
		movements = append(movements, stockMovement{
			Kind: m.Kind, Date: m.Date.Format("2006-01-02"), Quantity: m.Quantity, UnitCost: round2Ptr(m.UnitCost),
			ReferenceID: m.ReferenceID, Description: m.Description,
		})
	}
	writeJSON(w, http.StatusOK, stockBalanceDetailResponse{stockBalanceResponse: row.toResponse(), Movements: movements})
}

func (h *StockHandler) createAdjustment(w http.ResponseWriter, r *http.Request) {
	var req adjustmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := validateAdjustment(req); msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	adjustedAt, err := parseOptionalDate("adjusted_at", req.AdjustedAt)
	if err != nil {
		writeTxError(w, "CreateStockAdjustment", err)
		return
	}
	if adjustedAt == nil {
		d := todayDate()
		adjustedAt = &d
	}
	db := h.db.WithContext(r.Context())
	if err := checkProductExists(db, *req.ProductID); err != nil {
		writeTxError(w, "CreateStockAdjustment", err)
		return
	}
	adj := models.StockAdjustment{
		ProductID:  *req.ProductID,
		Quantity:   req.Quantity,
		UnitCost:   req.UnitCost,
		Reason:     req.Reason,
		AdjustedAt: *adjustedAt,
		Notes:      nullIfBlank(req.Notes),
	}
	if err := db.Create(&adj).Error; err != nil {
		writeTxError(w, "CreateStockAdjustment", err)
		return
	}
	var row adjustmentRow
	if err := adjustmentQuery(db).Where("a.id = ?", adj.ID).Scan(&row).Error; err != nil {
		writeTxError(w, "CreateStockAdjustment fetch", err)
		return
	}
	writeJSON(w, http.StatusCreated, row.toResponse())
}

func adjustmentQuery(db *gorm.DB) *gorm.DB {
	return db.Table("stock_adjustments a").
		Select("a.*, p.nome AS product_name").
		Joins("JOIN produtos p ON p.id = a.product_id")
}

func (h *StockHandler) listAdjustments(w http.ResponseWriter, r *http.Request) {
	query := adjustmentQuery(h.db.WithContext(r.Context()))
	if v := r.URL.Query().Get("product_id"); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			models.WriteError(w, http.StatusBadRequest, "product_id inválido")
			return
		}
		query = query.Where("a.product_id = ?", v)
	}
	var rows []adjustmentRow
	if err := query.Order("a.adjusted_at DESC, a.created_at DESC").Scan(&rows).Error; err != nil {
		log.Printf("ERROR ListStockAdjustments: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	out := make([]adjustmentResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toResponse())
	}
	writeJSON(w, http.StatusOK, out)
}
