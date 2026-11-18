package handlers

// Custos adicionais da compra (AYD-008@context): frete de compra, descarga, imposto, outros —
// cada um com credor editável. Os custos entram nas contas da compra (AYD-007, por triggers) e
// são rateados entre as linhas (vw_purchase_line_costs). Podem ser lançados em QUALQUER status
// da compra (o frete costuma vir depois do pedido). Rateio e custo de entrada nunca são gravados.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/purchasing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// purchaseChargeRequest: POST/PUT (PUT é de substituição completa). payee_supplier_id ausente =
// fornecedor da compra (padrão); null explícito = credor "a definir".
type purchaseChargeRequest struct {
	Kind            string          `json:"kind"`
	Amount          float64         `json:"amount"`
	Description     *string         `json:"description"`
	PayeeSupplierID json.RawMessage `json:"payee_supplier_id"`
}

type purchaseChargeResponse struct {
	ID              uuid.UUID  `json:"id"`
	PurchaseID      uuid.UUID  `json:"purchase_id"`
	Kind            string     `json:"kind"`
	Amount          float64    `json:"amount"`
	Description     *string    `json:"description"`
	PayeeSupplierID *uuid.UUID `json:"payee_supplier_id"`
	PayeeName       *string    `json:"payee_name"`
	ObligationID    *uuid.UUID `json:"obligation_id"` // conta onde o custo caiu (AYD-007)
	CreatedAt       time.Time  `json:"created_at"`
}

// purchaseChargeMutationResponse: o recurso no corpo + warnings (AYD-007; vazio = []).
type purchaseChargeMutationResponse struct {
	purchaseChargeResponse
	Warnings []string `json:"warnings"`
}

const purchaseChargeSelect = `
	SELECT c.id, c.purchase_id, c.kind::text AS kind, c.amount, c.description, c.payee_supplier_id,
	       f.nome AS payee_name, o.id AS obligation_id, c.created_at
	FROM purchase_charges c
	LEFT JOIN fornecedores f ON f.id = c.payee_supplier_id
	LEFT JOIN obligations o ON o.deleted_at IS NULL AND o.purchase_id = c.purchase_id
	     AND o.direction = 'PAYABLE' AND o.counterparty_id IS NOT DISTINCT FROM c.payee_supplier_id
	WHERE c.deleted_at IS NULL `

func fetchPurchaseCharges(db *gorm.DB, purchaseID uuid.UUID) ([]purchaseChargeResponse, error) {
	out := []purchaseChargeResponse{}
	err := db.Raw(purchaseChargeSelect+`AND c.purchase_id = ? ORDER BY c.created_at, c.id`, purchaseID).Scan(&out).Error
	return out, err
}

func fetchPurchaseCharge(db *gorm.DB, id uuid.UUID) (*purchaseChargeResponse, error) {
	var out purchaseChargeResponse
	res := db.Raw(purchaseChargeSelect+`AND c.id = ?`, id).Scan(&out)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, errStatus(http.StatusNotFound, "custo adicional não encontrado")
	}
	return &out, nil
}

// lockPurchaseForCharge trava a Purchase: inexistente → 404; soft-deletada → 409.
func lockPurchaseForCharge(tx *gorm.DB, id uuid.UUID) (*models.Purchase, error) {
	var p models.Purchase
	if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errStatus(http.StatusNotFound, "compra não encontrada")
		}
		return nil, err
	}
	if p.DeletedAt.Valid {
		return nil, errStatus(http.StatusConflict, "compra excluída")
	}
	return &p, nil
}

// validated devolve o credor resolvido e a descrição normalizada, ou um erro 400/404.
func (req purchaseChargeRequest) validated(tx *gorm.DB, p *models.Purchase) (payee *uuid.UUID, desc *string, err error) {
	if !purchasing.ChargeKinds[req.Kind] {
		return nil, nil, errStatus(http.StatusBadRequest, "kind inválido (FREIGHT | UNLOADING | TAX | OTHER)")
	}
	if req.Amount <= 0 {
		return nil, nil, errStatus(http.StatusBadRequest, "campo 'amount' deve ser maior que zero")
	}
	if req.Amount > purchasing.MaxAmount {
		return nil, nil, errStatus(http.StatusBadRequest, "campo 'amount' acima do limite")
	}
	desc = nullIfBlank(req.Description)
	if desc != nil && len([]rune(*desc)) > 500 {
		return nil, nil, errStatus(http.StatusBadRequest, "campo 'description' deve ter no máximo 500 caracteres")
	}
	raw := bytes.TrimSpace(req.PayeeSupplierID)
	switch {
	case len(raw) == 0: // ausente → credor padrão: o Supplier da compra
		id := p.SupplierID
		payee = &id
	case string(raw) == "null": // explícito → "a definir"
		payee = nil
	default:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, nil, errStatus(http.StatusBadRequest, "payee_supplier_id inválido")
		}
		id, perr := uuid.Parse(strings.TrimSpace(s))
		if perr != nil {
			return nil, nil, errStatus(http.StatusBadRequest, "payee_supplier_id inválido")
		}
		payee = &id
	}
	if payee != nil {
		var n int64
		if err := tx.Model(&models.Fornecedor{}).Where("id = ?", *payee).Count(&n).Error; err != nil {
			return nil, nil, err
		}
		if n == 0 {
			return nil, nil, errStatus(http.StatusNotFound, "fornecedor não encontrado")
		}
	}
	return payee, desc, nil
}

func chargeMutation(tx *gorm.DB, purchaseID, chargeID uuid.UUID) (*purchaseChargeMutationResponse, error) {
	c, err := fetchPurchaseCharge(tx, chargeID)
	if err != nil {
		return nil, err
	}
	w, err := purchaseWarnings(tx, purchaseID)
	if err != nil {
		return nil, err
	}
	return &purchaseChargeMutationResponse{purchaseChargeResponse: *c, Warnings: w}, nil
}

func (h *PurchasesHandler) listCharges(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	db := h.db.WithContext(r.Context())
	var n int64
	if err := db.Model(&models.Purchase{}).Where("id = ?", ids[0]).Count(&n).Error; err != nil {
		writeTxError(w, "ListPurchaseCharges", err)
		return
	}
	if n == 0 {
		models.WriteError(w, http.StatusNotFound, "compra não encontrada")
		return
	}
	out, err := fetchPurchaseCharges(db, ids[0])
	if err != nil {
		writeTxError(w, "ListPurchaseCharges", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *PurchasesHandler) createCharge(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id")
	if !ok {
		return
	}
	var req purchaseChargeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var resp *purchaseChargeMutationResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchaseForCharge(tx, ids[0])
		if err != nil {
			return err
		}
		payee, desc, err := req.validated(tx, p)
		if err != nil {
			return err
		}
		c := models.PurchaseCharge{PurchaseID: p.ID, Kind: req.Kind, Amount: req.Amount, Description: desc, PayeeSupplierID: payee}
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		if err := ensureUnitsCompatible(tx, p.ID, false); err != nil {
			return err
		}
		resp, err = chargeMutation(tx, p.ID, c.ID)
		return err
	})
	if err != nil {
		writeTxError(w, "CreatePurchaseCharge", err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *PurchasesHandler) updateCharge(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "chargeId")
	if !ok {
		return
	}
	var req purchaseChargeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var resp *purchaseChargeMutationResponse
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchaseForCharge(tx, ids[0])
		if err != nil {
			return err
		}
		payee, desc, err := req.validated(tx, p)
		if err != nil {
			return err
		}
		res := tx.Model(&models.PurchaseCharge{}).Where("id = ? AND purchase_id = ?", ids[1], p.ID).
			Updates(map[string]any{"kind": req.Kind, "amount": req.Amount, "description": desc, "payee_supplier_id": payee})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "custo adicional não encontrado")
		}
		resp, err = chargeMutation(tx, p.ID, ids[1])
		return err
	})
	if err != nil {
		writeTxError(w, "UpdatePurchaseCharge", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// deleteCharge: soft-delete. O OVERSETTLED eventual aparece em GET /purchases/{id}.warnings.
func (h *PurchasesHandler) deleteCharge(w http.ResponseWriter, r *http.Request) {
	ids, ok := parsePathIDs(w, r, "id", "chargeId")
	if !ok {
		return
	}
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		p, err := lockPurchaseForCharge(tx, ids[0])
		if err != nil {
			return err
		}
		res := tx.Where("id = ? AND purchase_id = ?", ids[1], p.ID).Delete(&models.PurchaseCharge{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errStatus(http.StatusNotFound, "custo adicional não encontrado")
		}
		return nil
	})
	if err != nil {
		writeTxError(w, "DeletePurchaseCharge", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
