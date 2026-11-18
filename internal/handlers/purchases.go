package handlers

// Purchase (AYD-002@context): compra consolidada por Supplier — Items de Sale made-to-order
// (kind MADE_TO_ORDER) ou linhas de reposição de Stock (kind STOCK_REPLENISHMENT). Custo e
// um único pagamento ao Supplier por Purchase (RN-06 / PDR-001). Totais vêm de
// vw_purchases_resumo (nunca persistidos).

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/fulfillment"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PurchasesHandler struct {
	db *gorm.DB
}

func NewPurchasesHandler(db *gorm.DB) *PurchasesHandler {
	return &PurchasesHandler{db: db}
}

func (h *PurchasesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /purchases", h.list)
	mux.HandleFunc("POST /purchases", h.create)
	mux.HandleFunc("GET /purchases/pending-sale-items", h.pendingSaleItems)
	mux.HandleFunc("GET /purchases/{id}", h.get)
	mux.HandleFunc("PUT /purchases/{id}", h.update)
	mux.HandleFunc("DELETE /purchases/{id}", h.delete)

	mux.HandleFunc("POST /purchases/{id}/sale-items", h.attachSaleItems)
	mux.HandleFunc("DELETE /purchases/{id}/sale-items/{saleItemId}", h.detachSaleItem)

	mux.HandleFunc("POST /purchases/{id}/items", h.createLine)
	mux.HandleFunc("PUT /purchases/{id}/items/{itemId}", h.updateLine)
	mux.HandleFunc("DELETE /purchases/{id}/items/{itemId}", h.deleteLine)

	mux.HandleFunc("PATCH /purchases/{id}/status", h.updateStatus)
	mux.HandleFunc("PATCH /purchases/{id}/payment", h.updatePayment)
}

// --- Request types ---

type purchaseLineRequest struct {
	ProductID *uuid.UUID `json:"product_id"`
	Quantity  float64    `json:"quantity"`
	UnitCost  float64    `json:"unit_cost"`
}

type purchaseCreateRequest struct {
	Kind         string                `json:"kind"`
	SupplierID   *uuid.UUID            `json:"supplier_id"`
	ExpectedDate *string               `json:"expected_date"`
	Notes        *string               `json:"notes"`
	SaleItemIDs  []uuid.UUID           `json:"sale_item_ids"`
	Items        []purchaseLineRequest `json:"items"`
}

type purchaseUpdateRequest struct {
	ExpectedDate *string `json:"expected_date"`
	Notes        *string `json:"notes"`
}

type saleItemIDsRequest struct {
	SaleItemIDs []uuid.UUID `json:"sale_item_ids"`
}

type purchaseStatusRequest struct {
	Status string `json:"status"`
}

type purchasePaymentRequest struct {
	PaymentStatus string   `json:"payment_status"`
	PaidAmount    *float64 `json:"paid_amount"`
	PaidAt        *string  `json:"paid_at"`
}

// --- Response types ---

type purchaseSummaryResponse struct {
	ID            uuid.UUID `json:"id"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	SupplierID    uuid.UUID `json:"supplier_id"`
	SupplierName  string    `json:"supplier_name"`
	ExpectedDate  *string   `json:"expected_date"`
	Notes         *string   `json:"notes"`
	OrderedAt     *string   `json:"ordered_at"`
	ReceivedAt    *string   `json:"received_at"`
	PaymentStatus string    `json:"payment_status"`
	PaidAmount    *float64  `json:"paid_amount"`
	PaidAt        *string   `json:"paid_at"`
	TotalCost     float64   `json:"total_cost"`
	ToPay         float64   `json:"to_pay"`
	LineCount     int64     `json:"line_count"`
	CreatedAt     time.Time `json:"created_at"`
}

type purchaseSaleItemResponse struct {
	ID           uuid.UUID `json:"id"`
	SaleID       uuid.UUID `json:"sale_id"`
	CustomerName string    `json:"customer_name"`
	ProdutoNome  string    `json:"produto_nome"`
	Quantidade   float64   `json:"quantidade"`
	CustoUnit    float64   `json:"custo_unit"`
}

type purchaseLineResponse struct {
	ID          uuid.UUID `json:"id"`
	ProductID   uuid.UUID `json:"product_id"`
	ProductName string    `json:"product_name"`
	Quantity    float64   `json:"quantity"`
	UnitCost    float64   `json:"unit_cost"`
}

type purchaseDetailResponse struct {
	purchaseSummaryResponse
	SaleItems []purchaseSaleItemResponse `json:"sale_items"`
	Items     []purchaseLineResponse     `json:"items"`
}

type purchaseStatusResponse struct {
	ID         uuid.UUID `json:"id"`
	Status     string    `json:"status"`
	OrderedAt  *string   `json:"ordered_at"`
	ReceivedAt *string   `json:"received_at"`
}

type purchasePaymentResponse struct {
	ID            uuid.UUID `json:"id"`
	PaymentStatus string    `json:"payment_status"`
	PaidAmount    *float64  `json:"paid_amount"`
	PaidAt        *string   `json:"paid_at"`
	ToPay         float64   `json:"to_pay"`
}

type pendingSaleItemResponse struct {
	ID           uuid.UUID  `json:"id"`
	SaleID       uuid.UUID  `json:"sale_id"`
	CustomerName string     `json:"customer_name"`
	ProdutoID    *uuid.UUID `json:"produto_id"`
	ProdutoNome  string     `json:"produto_nome"`
	Quantidade   float64    `json:"quantidade"`
	CustoUnit    float64    `json:"custo_unit"`
	SaleDate     string     `json:"sale_date"`
}

type pendingSupplierGroup struct {
	SupplierID   *uuid.UUID                `json:"supplier_id"`
	SupplierName *string                   `json:"supplier_name"`
	Items        []pendingSaleItemResponse `json:"items"`
}

// --- Rows ---

type purchaseResumoRow struct {
	ID            uuid.UUID  `gorm:"column:id"`
	Kind          string     `gorm:"column:kind"`
	Status        string     `gorm:"column:status"`
	SupplierID    uuid.UUID  `gorm:"column:supplier_id"`
	SupplierName  string     `gorm:"column:supplier_name"`
	ExpectedDate  *time.Time `gorm:"column:expected_date"`
	Notes         *string    `gorm:"column:notes"`
	OrderedAt     *time.Time `gorm:"column:ordered_at"`
	ReceivedAt    *time.Time `gorm:"column:received_at"`
	PaymentStatus string     `gorm:"column:payment_status"`
	PaidAmount    *float64   `gorm:"column:paid_amount"`
	PaidAt        *time.Time `gorm:"column:paid_at"`
	TotalCost     float64    `gorm:"column:total_cost"`
	ToPay         float64    `gorm:"column:to_pay"`
	LineCount     int64      `gorm:"column:line_count"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
}

func (row purchaseResumoRow) toSummary() purchaseSummaryResponse {
	return purchaseSummaryResponse{
		ID:            row.ID,
		Kind:          row.Kind,
		Status:        row.Status,
		SupplierID:    row.SupplierID,
		SupplierName:  row.SupplierName,
		ExpectedDate:  formatDate(row.ExpectedDate),
		Notes:         row.Notes,
		OrderedAt:     formatDate(row.OrderedAt),
		ReceivedAt:    formatDate(row.ReceivedAt),
		PaymentStatus: row.PaymentStatus,
		PaidAmount:    row.PaidAmount,
		PaidAt:        formatDate(row.PaidAt),
		TotalCost:     row.TotalCost,
		ToPay:         row.ToPay,
		LineCount:     row.LineCount,
		CreatedAt:     row.CreatedAt,
	}
}

// --- Helpers ---

func purchaseResumoQuery(db *gorm.DB) *gorm.DB {
	return db.Table("vw_purchases_resumo pr").
		Select("pr.*, f.nome AS supplier_name").
		Joins("JOIN fornecedores f ON f.id = pr.supplier_id")
}

func fetchPurchaseDetail(db *gorm.DB, id uuid.UUID) (*purchaseDetailResponse, error) {
	var row purchaseResumoRow
	res := purchaseResumoQuery(db).Where("pr.id = ?", id).Scan(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, errStatus(http.StatusNotFound, "compra não encontrada")
	}

	saleItems := []purchaseSaleItemResponse{}
	if err := db.Raw(`
		SELECT i.id, i.venda_id AS sale_id, c.nome AS customer_name, i.produto_nome, i.quantidade, i.custo_unit
		FROM itens_venda i
		JOIN vendas v ON v.id = i.venda_id
		JOIN clientes c ON c.id = v.cliente_id
		WHERE i.purchase_id = ?
		ORDER BY v.data_venda, i.created_at`, id).Scan(&saleItems).Error; err != nil {
		return nil, err
	}
	lines := []purchaseLineResponse{}
	if err := db.Raw(`
		SELECT pi.id, pi.product_id, p.nome AS product_name, pi.quantity, pi.unit_cost
		FROM purchase_items pi
		JOIN produtos p ON p.id = pi.product_id
		WHERE pi.purchase_id = ?
		ORDER BY pi.created_at`, id).Scan(&lines).Error; err != nil {
		return nil, err
	}
	return &purchaseDetailResponse{purchaseSummaryResponse: row.toSummary(), SaleItems: saleItems, Items: lines}, nil
}

// lockPurchase carrega a Purchase (não excluída) com FOR UPDATE.
func lockPurchase(tx *gorm.DB, id uuid.UUID) (*models.Purchase, error) {
	var p models.Purchase
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errStatus(http.StatusNotFound, "compra não encontrada")
		}
		return nil, err
	}
	return &p, nil
}

// requireOpen: Purchase ≠ OPEN → 409 (linhas e Items só mudam com a Purchase aberta).
func requireOpen(p *models.Purchase, kind string) error {
	if p.Status != fulfillment.PurchaseOpen {
		return errStatus(http.StatusConflict, "compra não está aberta (OPEN)")
	}
	if kind != "" && p.Kind != kind {
		return errStatus(http.StatusConflict, "operação não permitida para compras do tipo "+p.Kind)
	}
	return nil
}

// attachToPurchase anexa Items de Sale à Purchase (já travada, OPEN e MADE_TO_ORDER).
// 409: Item de outro Supplier, em outra Purchase, FROM_STOCK ou de Sale cancelada/excluída.
func attachToPurchase(tx *gorm.DB, p *models.Purchase, ids []uuid.UUID) error {
	type row struct {
		ID             uuid.UUID  `gorm:"column:id"`
		FornecedorID   *uuid.UUID `gorm:"column:fornecedor_id"`
		Origin         string     `gorm:"column:origin"`
		PurchaseID     *uuid.UUID `gorm:"column:purchase_id"`
		DeliveryStatus string     `gorm:"column:delivery_status"`
		SaleDeleted    bool       `gorm:"column:sale_deleted"`
	}
	var rows []row
	if err := tx.Raw(`
		SELECT i.id, i.fornecedor_id, i.origin, i.purchase_id, v.delivery_status,
		       v.deleted_at IS NOT NULL AS sale_deleted
		FROM itens_venda i
		JOIN vendas v ON v.id = i.venda_id
		WHERE i.id IN ?
		FOR UPDATE OF i`, ids).Scan(&rows).Error; err != nil {
		return err
	}
	found := make(map[uuid.UUID]row, len(rows))
	for _, r := range rows {
		found[r.ID] = r
	}
	for _, id := range ids {
		r, ok := found[id]
		switch {
		case !ok:
			return errStatus(http.StatusNotFound, "item de venda não encontrado: "+id.String())
		case r.SaleDeleted || r.DeliveryStatus == fulfillment.DeliveryCanceled:
			return errStatus(http.StatusConflict, "item de venda cancelada: "+id.String())
		case r.Origin == fulfillment.OriginFromStock:
			return errStatus(http.StatusConflict, "item FROM_STOCK não entra em compra: "+id.String())
		case r.FornecedorID == nil || *r.FornecedorID != p.SupplierID:
			return errStatus(http.StatusConflict, "item de outro fornecedor (ou sem fornecedor): "+id.String())
		case r.PurchaseID != nil && *r.PurchaseID != p.ID:
			return errStatus(http.StatusConflict, "item já está em outra compra: "+id.String())
		}
	}
	return tx.Exec(`UPDATE itens_venda SET purchase_id = ? WHERE id IN ?`, p.ID, ids).Error
}

func validateLine(req purchaseLineRequest) string {
	if req.ProductID == nil || *req.ProductID == uuid.Nil {
		return "campo 'product_id' é obrigatório"
	}
	if req.Quantity <= 0 {
		return "campo 'quantity' deve ser maior que zero"
	}
	if req.UnitCost < 0 {
		return "campo 'unit_cost' não pode ser negativo"
	}
	return ""
}

func checkProductExists(tx *gorm.DB, id uuid.UUID) error {
	var n int64
	if err := tx.Model(&models.Produto{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return errStatus(http.StatusBadRequest, "produto não encontrado")
	}
	return nil
}

func parsePathIDs(w http.ResponseWriter, r *http.Request, names ...string) ([]uuid.UUID, bool) {
	out := make([]uuid.UUID, 0, len(names))
	for _, n := range names {
		id, err := uuid.Parse(r.PathValue(n))
		if err != nil {
			models.WriteError(w, http.StatusBadRequest, n+" inválido")
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		models.WriteError(w, http.StatusBadRequest, "JSON inválido")
		return false
	}
	return true
}

// ============================================================
// Purchase CRUD
// ============================================================

func (h *PurchasesHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := purchaseResumoQuery(h.db.WithContext(r.Context()))

	filters := []struct {
		param string
		valid map[string]bool
		col   string
	}{
		{"kind", fulfillment.PurchaseKinds, "pr.kind"},
		{"status", fulfillment.PurchaseStatuses, "pr.status"},
		{"payment_status", fulfillment.SupplierPaymentStatus, "pr.payment_status"},
	}
	for _, f := range filters {
		if v := q.Get(f.param); v != "" {
			if !f.valid[v] {
				models.WriteError(w, http.StatusBadRequest, f.param+" inválido")
				return
			}
			query = query.Where(f.col+" = ?", v)
		}
	}
	if v := q.Get("supplier_id"); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			models.WriteError(w, http.StatusBadRequest, "supplier_id inválido")
			return
		}
		query = query.Where("pr.supplier_id = ?", v)
	}
	// from/to filtram a data de criação da Purchase (fuso do negócio).
	for _, f := range []struct{ param, op string }{{"from", ">="}, {"to", "<="}} {
		if v := q.Get(f.param); v != "" {
			d, err := parseDate(v)
			if err != nil {
				models.WriteError(w, http.StatusBadRequest, f.param+" inválido (formato: YYYY-MM-DD)")
				return
			}
			query = query.Where("(pr.created_at AT TIME ZONE 'America/Sao_Paulo')::date "+f.op+" ?", d.Format("2006-01-02"))
		}
	}

	var rows []purchaseResumoRow
	if err := query.Order("pr.created_at DESC").Scan(&rows).Error; err != nil {
		log.Printf("ERROR ListPurchases: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	out := make([]purchaseSummaryResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toSummary())
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *PurchasesHandler) create(w http.ResponseWriter, r *http.Request) {
	var req purchaseCreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !fulfillment.PurchaseKinds[req.Kind] {
		models.WriteError(w, http.StatusBadRequest, "kind inválido (MADE_TO_ORDER | STOCK_REPLENISHMENT)")
		return
	}
	if req.SupplierID == nil || *req.SupplierID == uuid.Nil {
		models.WriteError(w, http.StatusBadRequest, "campo 'supplier_id' é obrigatório")
		return
	}
	if req.Kind == fulfillment.KindMadeToOrder && len(req.Items) > 0 {
		models.WriteError(w, http.StatusBadRequest, "compra MADE_TO_ORDER não aceita 'items' (use 'sale_item_ids')")
		return
	}
	if req.Kind == fulfillment.KindStockReplenishment && len(req.SaleItemIDs) > 0 {
		models.WriteError(w, http.StatusBadRequest, "compra STOCK_REPLENISHMENT não aceita 'sale_item_ids' (use 'items')")
		return
	}
	for _, line := range req.Items {
		if msg := validateLine(line); msg != "" {
			models.WriteError(w, http.StatusBadRequest, msg)
			return
		}
	}
	expected, err := parseOptionalDate("expected_date", req.ExpectedDate)
	if err != nil {
		writeTxError(w, "CreatePurchase", err)
		return
	}

	var resp *purchaseDetailResponse
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&models.Fornecedor{}).Where("id = ?", *req.SupplierID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return errStatus(http.StatusBadRequest, "fornecedor não encontrado")
		}
		p := models.Purchase{
			Kind:          req.Kind,
			SupplierID:    *req.SupplierID,
			Status:        fulfillment.PurchaseOpen,
			PaymentStatus: fulfillment.PaymentPending,
			ExpectedDate:  expected,
			Notes:         nullIfBlank(req.Notes),
		}
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		if len(req.SaleItemIDs) > 0 {
			if err := attachToPurchase(tx, &p, req.SaleItemIDs); err != nil {
				return err
			}
		}
		for _, line := range req.Items {
			if err := checkProductExists(tx, *line.ProductID); err != nil {
				return err
			}
			pi := models.PurchaseItem{PurchaseID: p.ID, ProductID: *line.ProductID, Quantity: line.Quantity, UnitCost: line.UnitCost}
			if err := tx.Create(&pi).Error; err != nil {
				return err
			}
		}
		var err error
		resp, err = fetchPurchaseDetail(tx, p.ID)
		return err
	})
	if err != nil {
		writeTxError(w, "CreatePurchase", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *PurchasesHandler) get(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	resp, err := fetchPurchaseDetail(h.db.WithContext(r.Context()), ids[0])
	if err != nil {
		writeTxError(w, "GetPurchase", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *PurchasesHandler) update(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	var req purchaseUpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	expected, err := parseOptionalDate("expected_date", req.ExpectedDate)
	if err != nil {
		writeTxError(w, "UpdatePurchase", err)
		return
	}
	res := h.db.WithContext(r.Context()).Model(&models.Purchase{}).Where("id = ?", ids[0]).
		Updates(map[string]any{"expected_date": expected, "notes": nullIfBlank(req.Notes)})
	if res.Error != nil {
		writeTxError(w, "UpdatePurchase", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		models.WriteError(w, http.StatusNotFound, "compra não encontrada")
		return
	}
	h.respondDetail(w, r.Context(), http.StatusOK, ids[0])
}

func (h *PurchasesHandler) respondDetail(w http.ResponseWriter, ctx context.Context, status int, id uuid.UUID) {
	resp, err := fetchPurchaseDetail(h.db.WithContext(ctx), id)
	if err != nil {
		writeTxError(w, "FetchPurchase", err)
		return
	}
	writeJSON(w, status, resp)
}

// delete: soft-delete, só OPEN; libera os Items de Sale (voltam à fila).
func (h *PurchasesHandler) delete(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchase(tx, ids[0])
		if err != nil {
			return err
		}
		if err := requireOpen(p, ""); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE itens_venda SET purchase_id = NULL WHERE purchase_id = ?`, p.ID).Error; err != nil {
			return err
		}
		return tx.Delete(p).Error
	})
	if err != nil {
		writeTxError(w, "DeletePurchase", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Fila de Items a comprar (RF-31)
// ============================================================

func (h *PurchasesHandler) pendingSaleItems(w http.ResponseWriter, r *http.Request) {
	type row struct {
		ID           uuid.UUID  `gorm:"column:id"`
		SaleID       uuid.UUID  `gorm:"column:sale_id"`
		CustomerName string     `gorm:"column:customer_name"`
		ProdutoID    *uuid.UUID `gorm:"column:produto_id"`
		ProdutoNome  string     `gorm:"column:produto_nome"`
		Quantidade   float64    `gorm:"column:quantidade"`
		CustoUnit    float64    `gorm:"column:custo_unit"`
		SaleDate     time.Time  `gorm:"column:sale_date"`
		SupplierID   *uuid.UUID `gorm:"column:supplier_id"`
		SupplierName *string    `gorm:"column:supplier_name"`
	}
	var rows []row
	err := h.db.WithContext(r.Context()).Raw(`
		SELECT i.id, i.venda_id AS sale_id, c.nome AS customer_name, i.produto_id, i.produto_nome,
		       i.quantidade, i.custo_unit, v.data_venda AS sale_date,
		       i.fornecedor_id AS supplier_id, COALESCE(f.nome, i.fornecedor_nome) AS supplier_name
		FROM itens_venda i
		JOIN vendas v ON v.id = i.venda_id
		JOIN clientes c ON c.id = v.cliente_id
		LEFT JOIN fornecedores f ON f.id = i.fornecedor_id
		WHERE i.origin = 'MADE_TO_ORDER'
		  AND i.purchase_id IS NULL
		  AND v.deleted_at IS NULL
		  AND v.delivery_status <> 'CANCELED'
		ORDER BY v.data_venda, i.created_at`).Scan(&rows).Error
	if err != nil {
		log.Printf("ERROR PendingSaleItems: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	groups := []*pendingSupplierGroup{}
	bySupplier := map[string]*pendingSupplierGroup{}
	for _, r := range rows {
		key := supplierKey(r.SupplierID)
		g, ok := bySupplier[key]
		if !ok {
			g = &pendingSupplierGroup{SupplierID: r.SupplierID, Items: []pendingSaleItemResponse{}}
			if r.SupplierID != nil {
				g.SupplierName = r.SupplierName
			}
			bySupplier[key] = g
			groups = append(groups, g)
		}
		g.Items = append(g.Items, pendingSaleItemResponse{
			ID: r.ID, SaleID: r.SaleID, CustomerName: r.CustomerName, ProdutoID: r.ProdutoID,
			ProdutoNome: r.ProdutoNome, Quantidade: r.Quantidade, CustoUnit: r.CustoUnit,
			SaleDate: r.SaleDate.Format("2006-01-02"),
		})
	}
	// Por nome do Supplier; o grupo sem Supplier (supplier_id null) por último.
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if (a.SupplierID == nil) != (b.SupplierID == nil) {
			return b.SupplierID == nil
		}
		if a.SupplierName == nil || b.SupplierName == nil {
			return false
		}
		return *a.SupplierName < *b.SupplierName
	})
	writeJSON(w, http.StatusOK, groups)
}

// ============================================================
// Items de Sale na Purchase (MADE_TO_ORDER)
// ============================================================

func (h *PurchasesHandler) attachSaleItems(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	var req saleItemIDsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.SaleItemIDs) == 0 {
		models.WriteError(w, http.StatusBadRequest, "campo 'sale_item_ids' é obrigatório")
		return
	}
	var resp *purchaseDetailResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchase(tx, ids[0])
		if err != nil {
			return err
		}
		if err := requireOpen(p, fulfillment.KindMadeToOrder); err != nil {
			return err
		}
		if err := attachToPurchase(tx, p, req.SaleItemIDs); err != nil {
			return err
		}
		resp, err = fetchPurchaseDetail(tx, p.ID)
		return err
	})
	if err != nil {
		writeTxError(w, "AttachSaleItems", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *PurchasesHandler) detachSaleItem(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "saleItemId")
	if !ok {
		return
	}
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchase(tx, ids[0])
		if err != nil {
			return err
		}
		if err := requireOpen(p, ""); err != nil {
			return err
		}
		res := tx.Exec(`UPDATE itens_venda SET purchase_id = NULL WHERE id = ? AND purchase_id = ?`, ids[1], p.ID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "item de venda não está nesta compra")
		}
		return nil
	})
	if err != nil {
		writeTxError(w, "DetachSaleItem", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Linhas de reposição (STOCK_REPLENISHMENT)
// ============================================================

func lineResponse(tx *gorm.DB, id uuid.UUID) (*purchaseLineResponse, error) {
	var line purchaseLineResponse
	err := tx.Raw(`
		SELECT pi.id, pi.product_id, p.nome AS product_name, pi.quantity, pi.unit_cost
		FROM purchase_items pi JOIN produtos p ON p.id = pi.product_id
		WHERE pi.id = ?`, id).Scan(&line).Error
	return &line, err
}

func (h *PurchasesHandler) createLine(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	var req purchaseLineRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := validateLine(req); msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	var resp *purchaseLineResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchase(tx, ids[0])
		if err != nil {
			return err
		}
		if err := requireOpen(p, fulfillment.KindStockReplenishment); err != nil {
			return err
		}
		if err := checkProductExists(tx, *req.ProductID); err != nil {
			return err
		}
		pi := models.PurchaseItem{PurchaseID: p.ID, ProductID: *req.ProductID, Quantity: req.Quantity, UnitCost: req.UnitCost}
		if err := tx.Create(&pi).Error; err != nil {
			return err
		}
		resp, err = lineResponse(tx, pi.ID)
		return err
	})
	if err != nil {
		writeTxError(w, "CreatePurchaseLine", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *PurchasesHandler) updateLine(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "itemId")
	if !ok {
		return
	}
	var req purchaseLineRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := validateLine(req); msg != "" {
		models.WriteError(w, http.StatusBadRequest, msg)
		return
	}
	var resp *purchaseLineResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchase(tx, ids[0])
		if err != nil {
			return err
		}
		if err := requireOpen(p, ""); err != nil {
			return err
		}
		if err := checkProductExists(tx, *req.ProductID); err != nil {
			return err
		}
		res := tx.Model(&models.PurchaseItem{}).Where("id = ? AND purchase_id = ?", ids[1], p.ID).
			Updates(map[string]any{"product_id": *req.ProductID, "quantity": req.Quantity, "unit_cost": req.UnitCost})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "linha não encontrada")
		}
		resp, err = lineResponse(tx, ids[1])
		return err
	})
	if err != nil {
		writeTxError(w, "UpdatePurchaseLine", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *PurchasesHandler) deleteLine(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "itemId")
	if !ok {
		return
	}
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchase(tx, ids[0])
		if err != nil {
			return err
		}
		if err := requireOpen(p, ""); err != nil {
			return err
		}
		res := tx.Where("id = ? AND purchase_id = ?", ids[1], p.ID).Delete(&models.PurchaseItem{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "linha não encontrada")
		}
		return nil
	})
	if err != nil {
		writeTxError(w, "DeletePurchaseLine", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Status e pagamento
// ============================================================

func (h *PurchasesHandler) updateStatus(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	var req purchaseStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !fulfillment.PurchaseStatuses[req.Status] {
		models.WriteError(w, http.StatusBadRequest, "status inválido (OPEN | ORDERED | RECEIVED)")
		return
	}
	var resp purchaseStatusResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchase(tx, ids[0])
		if err != nil {
			return err
		}
		var lines int64
		if err := tx.Raw(`SELECT line_count FROM vw_purchases_resumo WHERE id = ?`, p.ID).Scan(&lines).Error; err != nil {
			return err
		}
		switch err := fulfillment.CheckPurchaseTransition(p.Status, req.Status, lines); {
		case errors.Is(err, fulfillment.ErrIllegalTransition):
			return errStatus(http.StatusConflict, "transição ilegal: "+p.Status+" → "+req.Status)
		case errors.Is(err, fulfillment.ErrEmptyPurchase):
			return errStatus(http.StatusUnprocessableEntity, "compra sem linhas não pode ser pedida")
		case err != nil:
			return err
		}
		orderedAt, receivedAt := fulfillment.StampTransition(req.Status, p.OrderedAt, p.ReceivedAt, todayDate())
		if err := tx.Model(p).Updates(map[string]any{
			"status": req.Status, "ordered_at": orderedAt, "received_at": receivedAt,
		}).Error; err != nil {
			return err
		}
		resp = purchaseStatusResponse{ID: p.ID, Status: req.Status, OrderedAt: formatDate(orderedAt), ReceivedAt: formatDate(receivedAt)}
		return nil
	})
	if err != nil {
		writeTxError(w, "UpdatePurchaseStatus", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// updatePayment: um pagamento por Purchase (PDR-001). Independe do status da Purchase.
func (h *PurchasesHandler) updatePayment(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	var req purchasePaymentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !fulfillment.SupplierPaymentStatus[req.PaymentStatus] {
		models.WriteError(w, http.StatusBadRequest, "payment_status inválido (PENDING | PAID)")
		return
	}
	paidAt, err := parseOptionalDate("paid_at", req.PaidAt)
	if err != nil {
		writeTxError(w, "UpdatePurchasePayment", err)
		return
	}
	var resp purchasePaymentResponse
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchase(tx, ids[0])
		if err != nil {
			return err
		}
		var totalCost float64
		if err := tx.Raw(`SELECT total_cost FROM vw_purchases_resumo WHERE id = ?`, p.ID).Scan(&totalCost).Error; err != nil {
			return err
		}
		amount, at, err := fulfillment.ResolvePayment(req.PaymentStatus, req.PaidAmount, paidAt, totalCost, todayDate())
		if errors.Is(err, fulfillment.ErrNegativePaidAmount) {
			return errStatus(http.StatusBadRequest, err.Error())
		}
		if err != nil {
			return err
		}
		if err := tx.Model(p).Updates(map[string]any{
			"payment_status": req.PaymentStatus, "paid_amount": amount, "paid_at": at,
		}).Error; err != nil {
			return err
		}
		var toPay float64
		if err := tx.Raw(`SELECT to_pay FROM vw_purchases_resumo WHERE id = ?`, p.ID).Scan(&toPay).Error; err != nil {
			return err
		}
		resp = purchasePaymentResponse{ID: p.ID, PaymentStatus: req.PaymentStatus, PaidAmount: amount, PaidAt: formatDate(at), ToPay: toPay}
		return nil
	})
	if err != nil {
		writeTxError(w, "UpdatePurchasePayment", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
