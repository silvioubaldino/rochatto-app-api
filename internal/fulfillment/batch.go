package fulfillment

import (
	"fmt"

	"github.com/google/uuid"
)

const MaxBatchItems = 50

type BatchItem struct {
	ID         uuid.UUID
	Origin     string
	SupplierID *uuid.UUID
}

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
