package mcp

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DedupeWindow é a janela de deduplicação de criações (AYD-006 §3.7).
const DedupeWindow = 120 * time.Second

// DBDeduper implementa Deduper sobre mcp_tool_calls (tabela e índice
// (connection_id, tool, arguments_hash, created_at) da migração 009). A chave é
// (connection_id, tool, arguments_hash), em que o hash é do JSON canônico dos argumentos
// SEM allow_duplicate (ver canonicalArgs no pipeline). Só chamadas com outcome OK e result
// gravado contam: a auditoria só guarda result nas ferramentas dedupe, e a chamada que foi
// ela mesma deduplicada é auditada sem result (não prorroga a janela).
//
// Risco residual aceito na v1: duas chamadas idênticas SIMULTÂNEAS passam ambas pela
// consulta antes de qualquer uma ser auditada e criam dois registros (não há lock). O
// cenário alvo — o modelo repetindo a chamada depois de uma resposta — é serial.
type DBDeduper struct {
	db  *gorm.DB
	now func() time.Time
}

// NewDBDeduper cria o Deduper em Postgres. now nil usa time.Now (injetável nos testes).
func NewDBDeduper(db *gorm.DB, now func() time.Time) *DBDeduper {
	if now == nil {
		now = time.Now
	}
	return &DBDeduper{db: db, now: now}
}

// Lookup implements Deduper.
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
