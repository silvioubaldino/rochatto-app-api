package handlers

import (
	"testing"

	"gorm.io/gorm"
)

func setLegacyCosts(t *testing.T, db *gorm.DB, sale string, freight, commission float64) {
	t.Helper()
	mustExec(t, db, `UPDATE sale_costs SET deleted_at = NOW() WHERE sale_id = ? AND deleted_at IS NULL`, sale)
	if freight > 0 {
		mustExec(t, db, `INSERT INTO sale_costs (sale_id, kind, amount) VALUES (?, 'FREIGHT', ?)`, sale, freight)
	}
	if commission > 0 {
		mustExec(t, db, `INSERT INTO sale_costs (sale_id, kind, amount, payee_partner_id)
			SELECT id, 'COMMISSION', ?, vendedor_externo_id FROM vendas WHERE id = ?`, commission, sale)
	}
}
