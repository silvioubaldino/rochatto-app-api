package mcp

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const DedupeWindow = 120 * time.Second

// Risco residual aceito na v1: duas chamadas idênticas SIMULTÂNEAS passam ambas pela consulta antes de qualquer uma ser auditada e criam dois registros (não há lock).
type DBDeduper struct {
	db  *gorm.DB
	now func() time.Time
}

func NewDBDeduper(db *gorm.DB, now func() time.Time) *DBDeduper {
	if now == nil {
		now = time.Now
	}
	return &DBDeduper{db: db, now: now}
}

func (d *DBDeduper) Lookup(ctx context.Context, connectionID uuid.UUID, tool, argsHash string) ([]byte, bool, error) {
	var result sql.NullString
	res := d.db.WithContext(ctx).Raw(`
		SELECT result::text FROM mcp_tool_calls
		WHERE connection_id = ? AND tool = ? AND arguments_hash = ?
		  AND outcome = 'OK' AND result IS NOT NULL AND created_at >= ?
		ORDER BY created_at DESC LIMIT 1`,
		connectionID, tool, argsHash, d.now().Add(-DedupeWindow)).Row()
	if err := res.Scan(&result); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !result.Valid {
		return nil, false, nil
	}
	return []byte(result.String), true, nil
}
