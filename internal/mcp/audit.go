package mcp

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const AuditRetention = 90 * 24 * time.Hour

// Result só é preenchido para ferramentas dedupe — nunca para as demais.
type AuditEntry struct {
	CallID        uuid.UUID
	ConnectionID  uuid.UUID
	Tool          string
	Arguments     []byte
	ArgumentsHash string
	Outcome       string
	ErrorCode     string
	Result        []byte
	DurationMS    int64
	CreatedAt     time.Time
}

type Auditor interface {
	Record(ctx context.Context, e AuditEntry)
}

type DBAuditor struct {
	db          *gorm.DB
	now         func() time.Time
	lastCleanup atomic.Int64
}

func NewDBAuditor(db *gorm.DB) *DBAuditor { return &DBAuditor{db: db, now: time.Now} }

func (a *DBAuditor) Record(ctx context.Context, e AuditEntry) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var errCode, result any
	if e.ErrorCode != "" {
		errCode = e.ErrorCode
	}
	if e.Result != nil {
		result = string(e.Result)
	}
	args := string(e.Arguments)
	if args == "" {
		args = "{}"
	}
	if err := a.db.WithContext(ctx).Exec(`
		INSERT INTO mcp_tool_calls(call_id, connection_id, tool, arguments, arguments_hash, outcome, error_code, result, duration_ms, created_at)
		VALUES (?, ?, ?, ?::jsonb, ?, ?, ?, ?::jsonb, ?, ?)`,
		e.CallID, e.ConnectionID, e.Tool, args, e.ArgumentsHash, e.Outcome, errCode, result, e.DurationMS, e.CreatedAt).Error; err != nil {
		log.Printf("ERROR mcp audit (call_id=%s tool=%s): %v", e.CallID, e.Tool, err)
	}
	a.maybeCleanup(ctx)
}

func (a *DBAuditor) maybeCleanup(ctx context.Context) {
	now := a.now()
	last := a.lastCleanup.Load()
	if now.Unix()-last < 3600 || !a.lastCleanup.CompareAndSwap(last, now.Unix()) {
		return
	}
	if err := a.db.WithContext(ctx).Exec(`DELETE FROM mcp_tool_calls WHERE created_at < ?`, now.Add(-AuditRetention)).Error; err != nil {
		log.Printf("WARN mcp audit retention: %v", err)
	}
}
