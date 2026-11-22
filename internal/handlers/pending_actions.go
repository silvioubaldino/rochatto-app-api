package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/fulfillment"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	pa "github.com/silvioubaldino/sales-backend/internal/pendingactions"
	"gorm.io/gorm"
)

type PendingActionsHandler struct {
	db *gorm.DB
}

func NewPendingActionsHandler(db *gorm.DB) *PendingActionsHandler {
	return &PendingActionsHandler{db: db}
}

func (h *PendingActionsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /pending-actions", h.list)
}

const pendingFetchLimit = 5000

type pendingCtx struct {
	db      *gorm.DB
	today   time.Time
	horizon int
}

func (c pendingCtx) windowEnd() time.Time { return c.today.AddDate(0, 0, c.horizon) }

func (h *PendingActionsHandler) list(w http.ResponseWriter, r *http.Request) {
	horizon, err := pa.ParseHorizon(r.URL.Query().Get("horizon_days"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	c := pendingCtx{db: h.db.WithContext(r.Context()), today: todayDate(), horizon: horizon}
	groups, err := c.collect()
	if err != nil {
		log.Printf("ERROR PendingActions: %v", err)
		models.WriteError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, pa.Assemble(c.today, horizon, groups))
}

func (c pendingCtx) collect() (map[pa.Kind]*pa.Group, error) {
	builders := []struct {
		kind  pa.Kind
		build func() ([]pa.Item, error)
	}{
		{pa.ReceivableDue, func() ([]pa.Item, error) { return c.obligationsDue(obligations.Receivable) }},
		{pa.PayableDue, func() ([]pa.Item, error) { return c.obligationsDue(obligations.Payable) }},
		{pa.DeliveryDue, c.deliveriesDue},
		{pa.ItemsWithoutSupplier, c.itemsWithoutSupplier},
		{pa.ItemsToPurchase, c.itemsToPurchase},
		{pa.PurchaseToOrder, c.purchasesToOrder},
		{pa.PurchaseToReceive, c.purchasesToReceive},
		{pa.ReceivableWithoutPlan, c.receivablesWithoutPlan},
		{pa.CostWithoutPayee, c.costsWithoutPayee},
		{pa.QuoteStale, c.staleQuotes},
		{pa.NegativeStock, c.negativeStock},
	}
	groups := make(map[pa.Kind]*pa.Group, len(builders))
	for _, b := range builders {
		items, err := b.build()
		if err != nil {
			return nil, err
		}
		groups[b.kind] = pa.BuildGroup(b.kind, items)
	}
	return groups, nil
}

func dateText(t *time.Time) *string { return formatDate(t) }

func f64(v float64) *float64 { v = obligations.Round2(v); return &v }

func intp(v int) *int { return &v }

func (c pendingCtx) openObligations(f obligations.ListFilter) ([]obligations.Obligation, error) {
	f.Statuses = []string{"OPEN", "PARTIAL"}
	f.Limit = pendingFetchLimit
	out, err := obligations.List(c.db, f)
	if err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c pendingCtx) originNumbers(obs []obligations.Obligation) (map[uuid.UUID]int, error) {
	var sales, purchases []uuid.UUID
	for _, o := range obs {
		if o.Origin.Type == "SALE" {
			sales = append(sales, o.Origin.ID)
		} else {
			purchases = append(purchases, o.Origin.ID)
		}
	}
	out := map[uuid.UUID]int{}
	for table, ids := range map[string][]uuid.UUID{"vendas": sales, "purchases": purchases} {
		if err := c.numbersOf(table, ids, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c pendingCtx) numbersOf(table string, ids []uuid.UUID, out map[uuid.UUID]int) error {
	if len(ids) == 0 {
		return nil
	}
	var rows []struct {
		ID     uuid.UUID `gorm:"column:id"`
		Number int       `gorm:"column:number"`
	}
	if err := c.db.Raw("SELECT id, number FROM "+table+" WHERE id IN ?", ids).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.ID] = r.Number
	}
	return nil
}

func obligationTitle(o obligations.Obligation) string {
	if o.Counterparty == nil || o.Counterparty.Name == "" {
		return "A definir"
	}
	return o.Counterparty.Name
}

func obligationItem(o obligations.Obligation, numbers map[uuid.UUID]int, date *string, amount float64) pa.Item {
	it := pa.Item{
		Entity: pa.Entity{Type: pa.EntityObligation, ID: o.ID}, Title: obligationTitle(o),
		Date: date, Overdue: o.Overdue, Amount: f64(amount),
	}
	if n, ok := numbers[o.Origin.ID]; ok {
		it.Number = intp(n)
	}
	return it
}

func (c pendingCtx) obligationsDue(direction string) ([]pa.Item, error) {
	end := c.windowEnd()
	obs, err := c.openObligations(obligations.ListFilter{Direction: direction, DueTo: &end})
	if err != nil {
		return nil, err
	}
	numbers, err := c.originNumbers(obs)
	if err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(obs))
	for _, o := range obs {
		date, amount := o.DueDate, o.Open
		if o.NextInstallment != nil {
			date, amount = &o.NextInstallment.DueDate, o.NextInstallment.AmountOpen
		}
		items = append(items, obligationItem(o, numbers, date, amount))
	}
	return items, nil
}

func (c pendingCtx) receivablesWithoutPlan() ([]pa.Item, error) {
	obs, err := c.openObligations(obligations.ListFilter{Direction: obligations.Receivable, NoPlan: true})
	if err != nil {
		return nil, err
	}
	numbers, err := c.originNumbers(obs)
	if err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(obs))
	for _, o := range obs {
		if o.Open > 0 {
			items = append(items, obligationItem(o, numbers, nil, o.Open))
		}
	}
	return items, nil
}

func (c pendingCtx) costsWithoutPayee() ([]pa.Item, error) {
	obs, err := c.openObligations(obligations.ListFilter{Direction: obligations.Payable, NoCounterparty: true})
	if err != nil {
		return nil, err
	}
	numbers, err := c.originNumbers(obs)
	if err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(obs))
	for _, o := range obs {
		if o.Amount <= 0 {
			continue
		}
		date := o.DueDate
		if o.NextInstallment != nil {
			date = &o.NextInstallment.DueDate
		}
		items = append(items, obligationItem(o, numbers, date, o.Amount))
	}
	return items, nil
}

func (c pendingCtx) deliveriesDue() ([]pa.Item, error) {
	end := c.windowEnd()
	var rows []struct {
		ID     uuid.UUID  `gorm:"column:id"`
		Number int        `gorm:"column:number"`
		Title  string     `gorm:"column:title"`
		Due    *time.Time `gorm:"column:due"`
		Amount float64    `gorm:"column:amount"`
	}
	err := c.db.Raw(`
		SELECT v.id, v.number, c.nome AS title, v.data_entrega_prevista AS due, vr.total_venda AS amount
		FROM vendas v
		JOIN vw_vendas_resumo vr ON vr.id = v.id
		JOIN clientes c ON c.id = v.cliente_id
		WHERE v.deleted_at IS NULL AND v.delivery_status = ?
		  AND (v.data_entrega_prevista IS NULL OR v.data_entrega_prevista <= ?)`,
		fulfillment.DeliveryToDeliver, end).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, pa.Item{
			Entity: pa.Entity{Type: pa.EntitySale, ID: r.ID}, Number: intp(r.Number), Title: r.Title,
			Date: dateText(r.Due), Overdue: pa.IsOverdue(r.Due, c.today), Amount: f64(r.Amount),
		})
	}
	return items, nil
}

func (c pendingCtx) itemsWithoutSupplier() ([]pa.Item, error) {
	rows, err := pendingSaleItemRows(c.db)
	if err != nil {
		return nil, err
	}
	bySale := map[uuid.UUID]*pa.Item{}
	var order []uuid.UUID
	var ids []uuid.UUID
	for _, r := range rows {
		if r.SupplierID != nil {
			continue
		}
		it, ok := bySale[r.SaleID]
		if !ok {
			it = &pa.Item{
				Entity: pa.Entity{Type: pa.EntitySale, ID: r.SaleID}, Title: r.CustomerName,
				Date: dateText(r.DueDate), Overdue: pa.IsOverdue(r.DueDate, c.today), Quantity: f64(0),
			}
			bySale[r.SaleID] = it
			order = append(order, r.SaleID)
			ids = append(ids, r.SaleID)
		}
		*it.Quantity++
	}
	numbers := map[uuid.UUID]int{}
	if err := c.numbersOf("vendas", ids, numbers); err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(order))
	for _, id := range order {
		it := bySale[id]
		if n, ok := numbers[id]; ok {
			it.Number = intp(n)
		}
		items = append(items, *it)
	}
	return items, nil
}

func (c pendingCtx) itemsToPurchase() ([]pa.Item, error) {
	rows, err := pendingSaleItemRows(c.db)
	if err != nil {
		return nil, err
	}
	type acc struct {
		item pa.Item
		due  *time.Time
		cost float64
	}
	bySupplier := map[uuid.UUID]*acc{}
	var order []uuid.UUID
	for _, r := range rows {
		if r.SupplierID == nil {
			continue
		}
		a, ok := bySupplier[*r.SupplierID]
		if !ok {
			title := ""
			if r.SupplierName != nil {
				title = *r.SupplierName
			}
			a = &acc{item: pa.Item{Entity: pa.Entity{Type: pa.EntitySupplier, ID: *r.SupplierID}, Title: title, Quantity: f64(0)}}
			bySupplier[*r.SupplierID] = a
			order = append(order, *r.SupplierID)
		}
		*a.item.Quantity++
		a.cost += r.Quantidade * r.CustoUnit
		if r.DueDate != nil && (a.due == nil || r.DueDate.Before(*a.due)) {
			a.due = r.DueDate
		}
	}
	items := make([]pa.Item, 0, len(order))
	for _, id := range order {
		a := bySupplier[id]
		a.item.Date, a.item.Overdue, a.item.Amount = dateText(a.due), pa.IsOverdue(a.due, c.today), f64(a.cost)
		items = append(items, a.item)
	}
	return items, nil
}

func purchaseItem(r purchaseResumoRow, overdue bool) pa.Item {
	return pa.Item{
		Entity: pa.Entity{Type: pa.EntityPurchase, ID: r.ID}, Number: intp(r.Number), Title: r.SupplierName,
		Date: dateText(r.ExpectedDate), Overdue: overdue, Amount: f64(r.TotalCost),
	}
}

func (c pendingCtx) purchasesToOrder() ([]pa.Item, error) {
	var rows []purchaseResumoRow
	if err := purchaseResumoQuery(c.db).
		Where("pr.status = ? AND pr.line_count > 0", fulfillment.PurchaseOpen).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, purchaseItem(r, false))
	}
	return items, nil
}

func (c pendingCtx) purchasesToReceive() ([]pa.Item, error) {
	var rows []purchaseResumoRow
	if err := purchaseResumoQuery(c.db).
		Where("pr.status = ? AND (pr.expected_date IS NULL OR pr.expected_date <= ?)", fulfillment.PurchaseOrdered, c.windowEnd()).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, purchaseItem(r, pa.IsOverdue(r.ExpectedDate, c.today)))
	}
	return items, nil
}

// staleQuotes: Quote OPEN (não é venda rápida) com quote_date antes de hoje − 7 dias.
func (c pendingCtx) staleQuotes() ([]pa.Item, error) {
	cutoff := c.today.AddDate(0, 0, -pa.StaleQuoteDays)
	var rows []quoteResumoRow
	if err := quoteResumoQuery(c.db).
		Where("qr.status = 'OPEN' AND qr.quick_sale = FALSE AND qr.quote_date < ?", cutoff).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(rows))
	for _, r := range rows {
		date := r.QuoteDate
		items = append(items, pa.Item{
			Entity: pa.Entity{Type: pa.EntityQuote, ID: r.ID}, Number: intp(r.Number), Title: r.CustomerName,
			Date: dateText(&date), Amount: f64(r.TotalQuote),
		})
	}
	return items, nil
}

func (c pendingCtx) negativeStock() ([]pa.Item, error) {
	rows, err := fetchStockBalances(c.db, "", true)
	if err != nil {
		return nil, err
	}
	items := make([]pa.Item, 0, len(rows))
	for _, r := range rows {
		q := r.Balance
		items = append(items, pa.Item{Entity: pa.Entity{Type: pa.EntityProduct, ID: r.ProductID}, Title: r.ProductName, Quantity: &q})
	}
	return items, nil
}
