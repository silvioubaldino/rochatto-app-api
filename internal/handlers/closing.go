package handlers

// Fechamento da venda (Sale closing — AYD-011 C5 / SPEC-012): a conversão do Quote (ou a venda
// rápida) já com o atendimento dos Items, prazo de entrega, custos da venda, plano de recebimento
// e entrada, numa única transação. Não há regra nova: reaproveita convertQuote, createSaleCost,
// obligations.ReplaceInstallments e obligations.SettleOne; as regras puras estão em
// internal/closing. Qualquer erro desfaz tudo (nada é gravado) e aponta o campo (`field`).

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

// closingWarning: aviso do fechamento (INSUFFICIENT_STOCK) apontando o Item pela mesma chave do
// pedido — quote_item_id na conversão, item_index na venda rápida.
type closingWarning struct {
	Code        string     `json:"code"`
	QuoteItemID *uuid.UUID `json:"quote_item_id,omitempty"`
	ItemIndex   *int       `json:"item_index,omitempty"`
}

// closingRun é o closing de uma requisição, já validado quanto à forma.
type closingRun struct {
	req       *closing.Request
	parsed    *closing.Parsed
	mode      closing.Mode
	ordered   []models.QuoteItem // venda rápida: Items na ordem de items[] (item_index)
	decisions map[uuid.UUID]closing.Decision
}

// newClosingRun valida a forma do closing; req nil devolve nil (conversão simples). O erro, se
// houver, é um *closing.FieldError.
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

// resolveItems aplica o atendimento dos Items (e confere no banco os fornecedores citados).
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

// closeSale converte e aplica o resto do fechamento; tudo dentro da transação de tx.
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

// finish aplica custos, plano e entrada e devolve os avisos (nunca nil).
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

// createCosts grava os custos da venda (mesmo corpo e regras de POST /vendas/{id}/costs).
func (r *closingRun) createCosts(tx *gorm.DB, saleID uuid.UUID) error {
	for i, c := range r.parsed.Costs {
		req := saleCostRequest{Kind: c.Kind, Amount: c.Amount, Description: c.Description}
		if salecomp.PayeeType(c.Kind) == salecomp.PayeePartner {
			req.PayeePartnerID = c.PayeeID
		} else {
			req.PayeeSupplierID = c.PayeeID
		}
		if _, err := createSaleCost(tx, saleID, req); err != nil {
			// Kind, valor e descrição já foram validados na forma: o que sobra é o credor
			// (inexistente ou de tipo errado para o kind → 400).
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

// planAndDownPayment aplica o plano de recebimento e a entrada na conta a receber criada pela
// conversão (preço ao cliente = valor da conta).
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

// stockWarnings avisa (sem bloquear — RN-16) os Items FROM_STOCK que deixaram o Balance negativo.
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

// writeFieldError responde { error, field } apontando a seção do closing.
func writeFieldError(w http.ResponseWriter, status int, msg, field string) {
	writeJSON(w, status, map[string]string{"error": msg, "field": field})
}
