package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/closing"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"github.com/silvioubaldino/sales-backend/internal/salecomp"
	"gorm.io/gorm"
)

type closingWarning struct {
	Code        string     `json:"code"`
	QuoteItemID *uuid.UUID `json:"quote_item_id,omitempty"`
	ItemIndex   *int       `json:"item_index,omitempty"`
}

type closingRun struct {
	req       *closing.Request
	parsed    *closing.Parsed
	mode      closing.Mode
	ordered   []models.QuoteItem // venda rápida: Items na ordem de items[] (item_index)
	decisions map[uuid.UUID]closing.Decision
}

func newClosingRun(req *closing.Request, mode closing.Mode, ordered []models.QuoteItem) (*closingRun, error) {
	if req == nil {
		return nil, nil
	}
	parsed, ferr := closing.Parse(req)
	if ferr != nil {
		return nil, ferr
	}
	return &closingRun{req: req, parsed: parsed, mode: mode, ordered: ordered}, nil
}

func (r *closingRun) setCreatedOrder(items []models.QuoteItem) {
	if r != nil {
		r.ordered = items
	}
}

func (r *closingRun) deliveryDue() *time.Time {
	if r == nil {
		return nil
	}
	return r.parsed.DeliveryDue
}

func (r *closingRun) resolveItems(tx *gorm.DB, items []models.QuoteItem) (map[uuid.UUID]closing.Decision, error) {
	if r == nil {
		return nil, nil
	}
	list := items
	if r.mode == closing.ByItemIndex && r.ordered != nil {
		list = r.ordered
	}
	qis := make([]closing.QuoteItem, len(list))
	for i, it := range list {
		qis[i] = closing.QuoteItem{ID: it.ID, Kind: it.Kind, HasProduct: it.ProductID != nil}
	}
	decisions, ferr := closing.ResolveItems(r.req.Items, qis, r.mode)
	if ferr != nil {
		return nil, ferr
	}
	for _, ref := range closing.SupplierRefs(r.req.Items) {
		var n int64
		if err := tx.Model(&models.Fornecedor{}).Where("id = ?", ref.ID).Count(&n).Error; err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, closing.Wrap(ref.Field, http.StatusBadRequest, "fornecedor não encontrado")
		}
	}
	r.decisions = decisions
	return decisions, nil
}

func closeSale(tx *gorm.DB, convert func() (*convertResult, error), run *closingRun) (convertResponse, error) {
	res, err := convert()
	if err != nil {
		return convertResponse{}, err
	}
	warnings, err := run.finish(tx, res)
	if err != nil {
		return convertResponse{}, err
	}
	number, err := saleNumber(tx, res.sale.ID)
	if err != nil {
		return convertResponse{}, err
	}
	return convertResponse{SaleID: res.sale.ID, QuoteID: res.quote.ID, SaleNumber: number, Warnings: warnings}, nil
}

func (r *closingRun) finish(tx *gorm.DB, res *convertResult) ([]closingWarning, error) {
	if r == nil {
		return []closingWarning{}, nil
	}
	if err := r.createCosts(tx, res.sale.ID); err != nil {
		return nil, err
	}
	if err := r.planAndDownPayment(tx, res.sale.ID); err != nil {
		return nil, err
	}
	return r.stockWarnings(tx, res)
}

func (r *closingRun) createCosts(tx *gorm.DB, saleID uuid.UUID) error {
	for i, c := range r.parsed.Costs {
		req := saleCostRequest{Kind: c.Kind, Amount: c.Amount, Description: c.Description}
		if salecomp.PayeeType(c.Kind) == salecomp.PayeePartner {
			req.PayeePartnerID = c.PayeeID
		} else {
			req.PayeeSupplierID = c.PayeeID
		}
		if _, err := createSaleCost(tx, saleID, req); err != nil {
			if he := asHTTPError(err); he != nil {
				return closing.Wrap(fmt.Sprintf("closing.costs[%d].payee_id", i), http.StatusBadRequest, he.msg)
			}
			return err
		}
	}
	return nil
}

func asHTTPError(err error) *httpError {
	he, _ := err.(*httpError)
	return he
}

func (r *closingRun) planAndDownPayment(tx *gorm.DB, saleID uuid.UUID) error {
	p := r.parsed
	if len(p.Plan) == 0 && p.Down == nil {
		return nil
	}
	field := "closing.receivable_plan"
	if len(p.Plan) == 0 {
		field = "closing.down_payment"
	}
	list, err := obligations.List(tx, obligations.ListFilter{
		Direction: obligations.Receivable, OriginType: "SALE", OriginID: &saleID, Limit: 1,
	})
	if err != nil {
		return err
	}
	if len(list.Items) == 0 {
		return closing.Wrap(field, http.StatusConflict, "venda sem valor a receber: não há conta para parcelar ou quitar")
	}
	recv := list.Items[0]
	if ferr := closing.CheckTotals(&closing.Parsed{Plan: p.Plan, HasPlan: len(p.Plan) > 0, Down: p.Down}, recv.Amount); ferr != nil {
		return ferr
	}
	if len(p.Plan) > 0 {
		plan := make([]obligations.InstallmentInput, len(p.Plan))
		for i, in := range p.Plan {
			plan[i] = obligations.InstallmentInput{DueDate: in.DueDate, Amount: in.Amount, PaymentMethod: in.PaymentMethod}
		}
		if _, err := obligations.ReplaceInstallments(tx, recv.ID, plan); err != nil {
			return wrapObligationsError("closing.receivable_plan.installments", err)
		}
	}
	if p.Down != nil {
		method := p.Down.PaymentMethod
		on := p.Down.OccurredOn
		amount := p.Down.Amount
		if _, err := obligations.SettleOne(tx, recv.ID, &amount, obligations.MovementInput{OccurredOn: &on, PaymentMethod: &method}); err != nil {
			return wrapObligationsError("closing.down_payment", err)
		}
	}
	return nil
}

func wrapObligationsError(field string, err error) error {
	if e := obligations.AsError(err); e != nil {
		return closing.Wrap(field, e.Status, e.Msg)
	}
	return err
}

func (r *closingRun) stockWarnings(tx *gorm.DB, res *convertResult) ([]closingWarning, error) {
	warnings := []closingWarning{}
	for _, ci := range res.items {
		ws, err := stockWarnings(tx, ci.sale)
		if err != nil {
			return nil, err
		}
		for _, code := range ws {
			w := closingWarning{Code: code}
			if r.mode == closing.ByItemIndex {
				idx := r.decisions[ci.quote.ID].Index
				w.ItemIndex = &idx
			} else {
				id := ci.quote.ID
				w.QuoteItemID = &id
			}
			warnings = append(warnings, w)
		}
	}
	return warnings, nil
}

func writeFieldError(w http.ResponseWriter, status int, msg, field string) {
	writeJSON(w, status, map[string]string{"error": msg, "field": field})
}
