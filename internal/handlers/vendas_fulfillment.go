package handlers

// Atendimento em lote dos Items da Sale (AYD-011 C6 / SPEC-012): PATCH /vendas/{id}/itens/fulfillment.
// As mesmas regras do PUT /vendas/{id}/itens/{itemId} (origem e fornecedor; custo do FROM_STOCK
// pela regra RN-16; trava de Item em Purchase) aplicadas a vários Items numa transação — tudo ou nada.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/fulfillment"
	"github.com/silvioubaldino/sales-backend/internal/models"
	"github.com/silvioubaldino/sales-backend/internal/saleitem"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type batchFulfillmentItem struct {
	ID           uuid.UUID  `json:"id"`
	Origin       string     `json:"origin"`
	FornecedorID *uuid.UUID `json:"fornecedor_id"`
}

type batchFulfillmentRequest struct {
	Items []batchFulfillmentItem `json:"items"`
}

type batchWarning struct {
	ItemID uuid.UUID `json:"item_id"`
	Code   string    `json:"code"`
}

type batchFulfillmentResponse struct {
	Itens    []itemResponse `json:"itens"`
	Warnings []batchWarning `json:"warnings"`
}

// itemError é um erro de regra que aponta o Item (400/409 → { error, item_id }).
type itemError struct {
	httpError
	itemID uuid.UUID
}

func errItem(status int, itemID uuid.UUID, msg string) error {
	return &itemError{httpError{status, msg}, itemID}
}

func writeBatchError(w http.ResponseWriter, err error) {
	var ie *itemError
	if errors.As(err, &ie) {
		writeJSON(w, ie.status, map[string]string{"error": ie.msg, "item_id": ie.itemID.String()})
		return
	}
	writeTxError(w, "BatchFulfillment", err)
}

func (h *VendasHandler) batchFulfillment(w http.ResponseWriter, r *http.Request) {
	saleID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		models.WriteError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var req batchFulfillmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	batch := make([]fulfillment.BatchItem, len(req.Items))
	for i, it := range req.Items {
		batch[i] = fulfillment.BatchItem{ID: it.ID, Origin: it.Origin, SupplierID: it.FornecedorID}
	}
	if err := fulfillment.CheckBatch(batch); err != nil {
		models.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	var resp batchFulfillmentResponse
	err = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) (err error) {
		resp, err = applyBatchFulfillment(tx, saleID, batch)
		return err
	})
	if err != nil {
		writeBatchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func applyBatchFulfillment(tx *gorm.DB, saleID uuid.UUID, batch []fulfillment.BatchItem) (batchFulfillmentResponse, error) {
	if _, err := lockOpenSale(tx, saleID); err != nil {
		return batchFulfillmentResponse{}, err
	}
	saved := make([]models.ItemVenda, 0, len(batch))
	for _, b := range batch {
		item, err := applyFulfillment(tx, saleID, b)
		if err != nil {
			return batchFulfillmentResponse{}, err
		}
		saved = append(saved, *item)
	}

	// Avisos e resposta com o estado final (o Balance considera o lote inteiro).
	warnings := []batchWarning{}
	for _, item := range saved {
		ws, err := stockWarnings(tx, item)
		if err != nil {
			return batchFulfillmentResponse{}, err
		}
		for _, code := range ws {
			warnings = append(warnings, batchWarning{ItemID: item.ID, Code: code})
		}
	}
	ids := make([]uuid.UUID, len(saved))
	for i, item := range saved {
		ids[i] = item.ID
	}
	rows, err := fetchItemRows(tx, "itens_venda.id IN ?", ids)
	if err != nil {
		return batchFulfillmentResponse{}, err
	}
	byID := make(map[uuid.UUID]itemResponse, len(rows))
	for _, row := range rows {
		byID[row.ID] = row.toResponse()
	}
	itens := make([]itemResponse, 0, len(ids))
	for _, id := range ids {
		itens = append(itens, byID[id])
	}
	return batchFulfillmentResponse{Itens: itens, Warnings: warnings}, nil
}

// applyFulfillment aplica origem e fornecedor a um Item (regras do PUT por Item).
func applyFulfillment(tx *gorm.DB, saleID uuid.UUID, b fulfillment.BatchItem) (*models.ItemVenda, error) {
	var item models.ItemVenda
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&item, "id = ? AND venda_id = ?", b.ID, saleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errItem(http.StatusBadRequest, b.ID, "item não pertence à venda")
		}
		return nil, err
	}
	if item.Kind == saleitem.KindService {
		return nil, errItem(http.StatusBadRequest, b.ID, "item SERVICE não tem origem nem fornecedor")
	}
	if b.Origin == fulfillment.OriginFromStock && item.ProdutoID == nil {
		return nil, errItem(http.StatusBadRequest, b.ID, "item FROM_STOCK exige 'produto_id'")
	}
	if _, err := checkItemRefs(tx, itemRequest{FornecedorID: b.SupplierID}); err != nil {
		return nil, asItemError(err, b.ID)
	}
	cost, err := resolveItemCost(tx, itemRequest{ProdutoID: item.ProdutoID}, b.Origin, item.CustoUnit)
	if err != nil {
		return nil, err
	}

	if purchaseStatus, err := purchaseStatusOf(tx, item); err != nil {
		return nil, err
	} else if purchaseStatus != "" {
		before := fulfillment.ItemLockFields{Origin: derefString(item.Origin), SupplierID: supplierKey(item.FornecedorID), Quantity: item.Quantidade, UnitCost: item.CustoUnit}
		after := fulfillment.ItemLockFields{Origin: b.Origin, SupplierID: supplierKey(b.SupplierID), Quantity: item.Quantidade, UnitCost: cost}
		if fulfillment.LockedFieldsChanged(before, after) {
			return nil, asItemError(itemInPurchaseError(purchaseStatus), b.ID)
		}
	}

	origin := b.Origin
	item.Origin = &origin
	item.FornecedorID = b.SupplierID
	item.FornecedorNome = nil
	item.CustoUnit = cost
	if err := tx.Save(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// asItemError aponta o Item num httpError de regra (outros erros passam como estão).
func asItemError(err error, itemID uuid.UUID) error {
	if he := asHTTPError(err); he != nil {
		return errItem(he.status, itemID, he.msg)
	}
	return err
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
