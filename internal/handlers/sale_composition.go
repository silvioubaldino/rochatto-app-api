package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/obligations"
	"github.com/silvioubaldino/sales-backend/internal/salecomp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type saleChargeRequest struct {
	Kind        string  `json:"kind"`
	Amount      float64 `json:"amount"`
	Description *string `json:"description"`
}

type saleChargeResponse struct {
	ID           uuid.UUID  `json:"id"`
	SaleID       uuid.UUID  `json:"sale_id"`
	Kind         string     `json:"kind"`
	Amount       float64    `json:"amount"`
	Description  *string    `json:"description"`
	ObligationID *uuid.UUID `json:"obligation_id"`
	CreatedAt    time.Time  `json:"created_at"`
}

type saleChargeMutationResponse struct {
	saleChargeResponse
	Warnings []string `json:"warnings"`
}

type salePayee struct {
	Type string    `json:"type"`
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type saleCostRequest struct {
	Kind            string          `json:"kind"`
	Amount          float64         `json:"amount"`
	Description     *string         `json:"description"`
	PayeeSupplierID json.RawMessage `json:"payee_supplier_id"`
	PayeePartnerID  json.RawMessage `json:"payee_partner_id"`
	SaleItemID      *uuid.UUID      `json:"sale_item_id"`
}

type saleCostResponse struct {
	ID           uuid.UUID  `json:"id"`
	SaleID       uuid.UUID  `json:"sale_id"`
	Kind         string     `json:"kind"`
	Amount       float64    `json:"amount"`
	Description  *string    `json:"description"`
	Payee        *salePayee `json:"payee"`
	SaleItemID   *uuid.UUID `json:"sale_item_id"`
	ObligationID *uuid.UUID `json:"obligation_id"`
	CreatedAt    time.Time  `json:"created_at"`
}

type saleCostRow struct {
	ID           uuid.UUID  `gorm:"column:id"`
	SaleID       uuid.UUID  `gorm:"column:sale_id"`
	Kind         string     `gorm:"column:kind"`
	Amount       float64    `gorm:"column:amount"`
	Description  *string    `gorm:"column:description"`
	PayeeType    *string    `gorm:"column:payee_type"`
	PayeeID      *uuid.UUID `gorm:"column:payee_id"`
	PayeeName    *string    `gorm:"column:payee_name"`
	SaleItemID   *uuid.UUID `gorm:"column:sale_item_id"`
	ObligationID *uuid.UUID `gorm:"column:obligation_id"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
}

func (r saleCostRow) toResponse() saleCostResponse {
	out := saleCostResponse{
		ID: r.ID, SaleID: r.SaleID, Kind: r.Kind, Amount: r.Amount, Description: r.Description,
		SaleItemID: r.SaleItemID, ObligationID: r.ObligationID, CreatedAt: r.CreatedAt,
	}
	if r.PayeeType != nil && r.PayeeID != nil {
		name := ""
		if r.PayeeName != nil {
			name = *r.PayeeName
		}
		out.Payee = &salePayee{Type: *r.PayeeType, ID: *r.PayeeID, Name: name}
	}
	return out
}

type saleCostMutationResponse struct {
	saleCostResponse
	Warnings []string `json:"warnings"`
}

const saleChargeSelect = `
	SELECT c.id, c.sale_id, c.kind::text AS kind, c.amount, c.description, o.id AS obligation_id, c.created_at
	FROM sale_charges c
	LEFT JOIN obligations o ON o.deleted_at IS NULL AND o.sale_id = c.sale_id AND o.direction = 'RECEIVABLE'
	WHERE c.deleted_at IS NULL `

const saleCostSelect = `
	SELECT c.id, c.sale_id, c.kind::text AS kind, c.amount, c.description,
	       CASE WHEN c.payee_supplier_id IS NOT NULL THEN 'SUPPLIER' WHEN c.payee_partner_id IS NOT NULL THEN 'PARTNER' END AS payee_type,
	       COALESCE(c.payee_supplier_id, c.payee_partner_id) AS payee_id,
	       COALESCE(f.nome, p.nome) AS payee_name,
	       c.sale_item_id, o.id AS obligation_id, c.created_at
	FROM sale_costs c
	LEFT JOIN fornecedores f ON f.id = c.payee_supplier_id
	LEFT JOIN vendedores_externos p ON p.id = c.payee_partner_id
	LEFT JOIN obligations o ON o.deleted_at IS NULL AND o.sale_id = c.sale_id AND o.direction = 'PAYABLE'
	     AND o.counterparty_id IS NOT DISTINCT FROM COALESCE(c.payee_supplier_id, c.payee_partner_id)
	WHERE c.deleted_at IS NULL `

func fetchSaleCharges(db *gorm.DB, saleID uuid.UUID) ([]saleChargeResponse, error) {
	out := []saleChargeResponse{}
	err := db.Raw(saleChargeSelect+`AND c.sale_id = ? ORDER BY c.created_at, c.id`, saleID).Scan(&out).Error
	return out, err
}

func fetchSaleCosts(db *gorm.DB, saleID uuid.UUID) ([]saleCostResponse, error) {
	var rows []saleCostRow
	if err := db.Raw(saleCostSelect+`AND c.sale_id = ? ORDER BY c.created_at, c.id`, saleID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]saleCostResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toResponse())
	}
	return out, nil
}

func fetchSaleCost(db *gorm.DB, id uuid.UUID) (*saleCostResponse, error) {
	var row saleCostRow
	res := db.Raw(saleCostSelect+`AND c.id = ?`, id).Scan(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, errStatus(http.StatusNotFound, "custo da venda não encontrado")
	}
	out := row.toResponse()
	return &out, nil
}

func saleWarnings(db *gorm.DB, saleID uuid.UUID) ([]string, error) {
	var n int64
	if err := db.Raw(`SELECT COUNT(*) FROM vw_obligations WHERE sale_id = ? AND settled > amount`, saleID).Scan(&n).Error; err != nil {
		return nil, err
	}
	if n > 0 {
		return []string{salecomp.WarningOversettled}, nil
	}
	return []string{}, nil
}

// lockSale trava a Sale (FOR UPDATE); inexistente/excluída → 404. allowCanceled=false → 409 se CANCELED.
func lockSale(tx *gorm.DB, id uuid.UUID, allowCanceled bool) (*models.Venda, error) {
	var venda models.Venda
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&venda, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errStatus(http.StatusNotFound, "venda não encontrada")
		}
		return nil, err
	}
	if !allowCanceled && venda.DeliveryStatus == "CANCELED" {
		return nil, errStatus(http.StatusConflict, "venda cancelada não aceita cobranças")
	}
	return &venda, nil
}

func validateChargeFields(kind string, amount float64, description *string) (*string, error) {
	if !salecomp.ChargeKinds[kind] {
		return nil, errStatus(http.StatusBadRequest, "kind inválido (FREIGHT | OTHER)")
	}
	if amount <= 0 {
		return nil, errStatus(http.StatusBadRequest, "campo 'amount' deve ser maior que zero")
	}
	if amount > salecomp.MaxAmount {
		return nil, errStatus(http.StatusBadRequest, "campo 'amount' acima do limite")
	}
	desc := nullIfBlank(description)
	if desc != nil && len([]rune(*desc)) > 500 {
		return nil, errStatus(http.StatusBadRequest, "campo 'description' deve ter no máximo 500 caracteres")
	}
	return desc, nil
}

func resolveCostPayee(tx *gorm.DB, req saleCostRequest, sale *models.Venda) (supplier, partner *uuid.UUID, err error) {
	wantPartner := salecomp.PayeeType(req.Kind) == salecomp.PayeePartner
	matching, other := bytes.TrimSpace(req.PayeeSupplierID), bytes.TrimSpace(req.PayeePartnerID)
	if wantPartner {
		matching, other = other, matching
	}
	if len(other) > 0 && string(other) != "null" {
		return nil, nil, errStatus(http.StatusBadRequest, "credor de tipo incompatível: "+req.Kind+" aceita "+
			map[bool]string{true: "payee_partner_id", false: "payee_supplier_id"}[wantPartner])
	}
	var id *uuid.UUID
	switch {
	case len(matching) == 0:
		if wantPartner {
			id = sale.VendedorExternoID
		}
	case string(matching) == "null":
		id = nil
	default:
		var s string
		if json.Unmarshal(matching, &s) != nil {
			return nil, nil, errStatus(http.StatusBadRequest, "credor inválido")
		}
		parsed, perr := uuid.Parse(strings.TrimSpace(s))
		if perr != nil {
			return nil, nil, errStatus(http.StatusBadRequest, "credor inválido")
		}
		id = &parsed
	}
	if id == nil {
		return nil, nil, nil
	}
	var n int64
	if wantPartner {
		err = tx.Model(&models.Partner{}).Where("id = ?", *id).Count(&n).Error
	} else {
		err = tx.Model(&models.Fornecedor{}).Where("id = ?", *id).Count(&n).Error
	}
	if err != nil {
		return nil, nil, err
	}
	if n == 0 {
		return nil, nil, errStatus(http.StatusNotFound, "credor não encontrado")
	}
	if wantPartner {
		return nil, id, nil
	}
	return id, nil, nil
}

type validatedCost struct {
	desc     *string
	supplier *uuid.UUID
	partner  *uuid.UUID
}

func (req saleCostRequest) validated(tx *gorm.DB, sale *models.Venda) (*validatedCost, error) {
	if !salecomp.CostKinds[req.Kind] {
		return nil, errStatus(http.StatusBadRequest, "kind inválido (FREIGHT | LABOR | COMMISSION | OTHER)")
	}
	if req.Amount < 0 {
		return nil, errStatus(http.StatusBadRequest, "campo 'amount' não pode ser negativo")
	}
	if req.Amount > salecomp.MaxAmount {
		return nil, errStatus(http.StatusBadRequest, "campo 'amount' acima do limite")
	}
	desc := nullIfBlank(req.Description)
	if desc != nil && len([]rune(*desc)) > 500 {
		return nil, errStatus(http.StatusBadRequest, "campo 'description' deve ter no máximo 500 caracteres")
	}
	supplier, partner, err := resolveCostPayee(tx, req, sale)
	if err != nil {
		return nil, err
	}
	if req.SaleItemID != nil {
		var n int64
		if err := tx.Table("itens_venda").Where("id = ? AND venda_id = ?", *req.SaleItemID, sale.ID).Count(&n).Error; err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, errStatus(http.StatusBadRequest, "sale_item_id não pertence à venda")
		}
	}
	return &validatedCost{desc: desc, supplier: supplier, partner: partner}, nil
}

func (h *VendasHandler) listCharges(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	db := h.db.WithContext(r.Context())
	var n int64
	if err := db.Model(&models.Venda{}).Where("id = ?", ids[0]).Count(&n).Error; err != nil {
		writeTxError(w, "ListSaleCharges", err)
		return
	}
	if n == 0 {
		models.WriteError(w, http.StatusNotFound, "venda não encontrada")
		return
	}
	out, err := fetchSaleCharges(db, ids[0])
	if err != nil {
		writeTxError(w, "ListSaleCharges", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func saleChargeMutation(tx *gorm.DB, saleID, chargeID uuid.UUID) (*saleChargeMutationResponse, error) {
	var c saleChargeResponse
	res := tx.Raw(saleChargeSelect+`AND c.id = ?`, chargeID).Scan(&c)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, errStatus(http.StatusNotFound, "cobrança não encontrada")
	}
	w, err := saleWarnings(tx, saleID)
	if err != nil {
		return nil, err
	}
	return &saleChargeMutationResponse{saleChargeResponse: c, Warnings: w}, nil
}

func (h *VendasHandler) createCharge(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	var req saleChargeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var resp *saleChargeMutationResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		sale, err := lockSale(tx, ids[0], false)
		if err != nil {
			return err
		}
		desc, err := validateChargeFields(req.Kind, req.Amount, req.Description)
		if err != nil {
			return err
		}
		c := models.SaleCharge{SaleID: sale.ID, Kind: req.Kind, Amount: req.Amount, Description: desc}
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		resp, err = saleChargeMutation(tx, sale.ID, c.ID)
		return err
	})
	if err != nil {
		writeTxError(w, "CreateSaleCharge", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *VendasHandler) updateCharge(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "chargeId")
	if !ok {
		return
	}
	var req saleChargeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var resp *saleChargeMutationResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		sale, err := lockSale(tx, ids[0], false)
		if err != nil {
			return err
		}
		desc, err := validateChargeFields(req.Kind, req.Amount, req.Description)
		if err != nil {
			return err
		}
		res := tx.Model(&models.SaleCharge{}).Where("id = ? AND sale_id = ?", ids[1], sale.ID).
			Updates(map[string]any{"kind": req.Kind, "amount": req.Amount, "description": desc})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "cobrança não encontrada")
		}
		resp, err = saleChargeMutation(tx, sale.ID, ids[1])
		return err
	})
	if err != nil {
		writeTxError(w, "UpdateSaleCharge", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *VendasHandler) deleteCharge(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "chargeId")
	if !ok {
		return
	}
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		sale, err := lockSale(tx, ids[0], false)
		if err != nil {
			return err
		}
		res := tx.Where("id = ? AND sale_id = ?", ids[1], sale.ID).Delete(&models.SaleCharge{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "cobrança não encontrada")
		}
		return nil
	})
	if err != nil {
		writeTxError(w, "DeleteSaleCharge", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *VendasHandler) listCosts(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	db := h.db.WithContext(r.Context())
	var n int64
	if err := db.Model(&models.Venda{}).Where("id = ?", ids[0]).Count(&n).Error; err != nil {
		writeTxError(w, "ListSaleCosts", err)
		return
	}
	if n == 0 {
		models.WriteError(w, http.StatusNotFound, "venda não encontrada")
		return
	}
	out, err := fetchSaleCosts(db, ids[0])
	if err != nil {
		writeTxError(w, "ListSaleCosts", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func saleCostMutation(tx *gorm.DB, saleID, costID uuid.UUID) (*saleCostMutationResponse, error) {
	c, err := fetchSaleCost(tx, costID)
	if err != nil {
		return nil, err
	}
	w, err := saleWarnings(tx, saleID)
	if err != nil {
		return nil, err
	}
	return &saleCostMutationResponse{saleCostResponse: *c, Warnings: w}, nil
}

func createSaleCost(tx *gorm.DB, saleID uuid.UUID, req saleCostRequest) (*saleCostMutationResponse, error) {
	sale, err := lockSale(tx, saleID, true)
	if err != nil {
		return nil, err
	}
	v, err := req.validated(tx, sale)
	if err != nil {
		return nil, err
	}
	c := models.SaleCost{
		SaleID: sale.ID, Kind: req.Kind, Amount: req.Amount, Description: v.desc,
		PayeeSupplierID: v.supplier, PayeePartnerID: v.partner, SaleItemID: req.SaleItemID,
	}
	if err := tx.Create(&c).Error; err != nil {
		return nil, err
	}
	return saleCostMutation(tx, sale.ID, c.ID)
}

func (h *VendasHandler) createCost(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	var req saleCostRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var resp *saleCostMutationResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) (err error) {
		resp, err = createSaleCost(tx, ids[0], req)
		return err
	})
	if err != nil {
		writeTxError(w, "CreateSaleCost", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *VendasHandler) updateCost(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "costId")
	if !ok {
		return
	}
	var req saleCostRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var resp *saleCostMutationResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		sale, err := lockSale(tx, ids[0], true)
		if err != nil {
			return err
		}
		v, err := req.validated(tx, sale)
		if err != nil {
			return err
		}
		res := tx.Model(&models.SaleCost{}).Where("id = ? AND sale_id = ?", ids[1], sale.ID).
			Updates(map[string]any{
				"kind": req.Kind, "amount": req.Amount, "description": v.desc,
				"payee_supplier_id": v.supplier, "payee_partner_id": v.partner, "sale_item_id": req.SaleItemID,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "custo da venda não encontrado")
		}
		resp, err = saleCostMutation(tx, sale.ID, ids[1])
		return err
	})
	if err != nil {
		writeTxError(w, "UpdateSaleCost", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *VendasHandler) deleteCost(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "costId")
	if !ok {
		return
	}
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		sale, err := lockSale(tx, ids[0], true)
		if err != nil {
			return err
		}
		res := tx.Where("id = ? AND sale_id = ?", ids[1], sale.ID).Delete(&models.SaleCost{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "custo da venda não encontrado")
		}
		return nil
	})
	if err != nil {
		writeTxError(w, "DeleteSaleCost", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type saleComposition struct {
	ItemsTotal   float64              `json:"items_total"`
	Charges      []saleChargeResponse `json:"charges"`
	ChargesTotal float64              `json:"charges_total"`
	PriceTotal   float64              `json:"price_total"`
	Costs        []saleCostResponse   `json:"costs"`
	CostsTotal   float64              `json:"costs_total"`
}

func buildSaleComposition(db *gorm.DB, saleID uuid.UUID, itemsTotal, chargesTotal, costsTotal float64) (saleComposition, []obligations.Obligation, error) {
	charges, err := fetchSaleCharges(db, saleID)
	if err != nil {
		return saleComposition{}, nil, err
	}
	costs, err := fetchSaleCosts(db, saleID)
	if err != nil {
		return saleComposition{}, nil, err
	}
	obs, err := obligations.List(db, obligations.ListFilter{OriginType: "SALE", OriginID: &saleID, Limit: 200})
	if err != nil {
		return saleComposition{}, nil, err
	}
	items, charged, costed := obligations.Round2(itemsTotal), obligations.Round2(chargesTotal), obligations.Round2(costsTotal)
	return saleComposition{
		ItemsTotal: items, Charges: charges, ChargesTotal: charged,
		PriceTotal: obligations.Round2(items + charged), Costs: costs, CostsTotal: costed,
	}, obs.Items, nil
}
