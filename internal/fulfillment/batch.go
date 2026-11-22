package fulfillment

import (
	"fmt"

	"github.com/google/uuid"
)

// MaxBatchItems é o limite de Items por chamada do atendimento em lote (AYD-011 C6).
const MaxBatchItems = 50

// BatchItem é o atendimento pedido para um Item: origem e fornecedor (nil = sem Supplier).
type BatchItem struct {
	ID         uuid.UUID
	Origin     string
	SupplierID *uuid.UUID
}

// CheckBatch valida a forma do lote (400): 1..50 Items, ids presentes e únicos, origem válida.
func CheckBatch(items []BatchItem) error {
	if len(items) == 0 || len(items) > MaxBatchItems {
		return fmt.Errorf("items deve ter de 1 a %d Items", MaxBatchItems)
	}
	seen := map[uuid.UUID]bool{}
	for i, it := range items {
		if it.ID == uuid.Nil {
			return fmt.Errorf("items[%d].id é obrigatório", i)
		}
		if seen[it.ID] {
			return fmt.Errorf("items[%d].id repetido", i)
		}
		seen[it.ID] = true
		if !ItemOrigins[it.Origin] {
			return fmt.Errorf("items[%d].origin inválido (MADE_TO_ORDER | FROM_STOCK)", i)
		}
	}
	return nil
}
