package dbmigrate

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestIntegration_Migration009AssistantConnector(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dbName := fmt.Sprintf("mig009_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + dbName); err != nil {
		t.Skipf("sem permissão para criar banco descartável: %v", err)
	}
	defer admin.Exec("DROP DATABASE IF EXISTS " + dbName + " WITH (FORCE)")

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = dbName
	connStr := stdlib.RegisterConnConfig(cfg)
	defer stdlib.UnregisterConnConfig(connStr)

	m, err := New(connStr)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.M.Migrate(8); err != nil {
		t.Fatalf("migrate 8: %v", err)
	}
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tables := func() (n int) {
		db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'
			AND table_name IN ('oauth_clients','oauth_authorization_requests','oauth_connections','oauth_tokens','mcp_tool_calls')`).Scan(&n)
		return
	}
	if tables() != 0 {
		t.Fatal("tabelas já existem antes da 009")
	}

	if err := m.M.Migrate(9); err != nil {
		t.Fatalf("up 009: %v", err)
	}
	if tables() != 5 {
		t.Fatalf("tabelas após a 009 = %d, quero 5", tables())
	}
	exec := func(q string, args ...any) error { _, err := db.Exec(q, args...); return err }
	if err := exec(`INSERT INTO oauth_clients(client_id, redirect_uris) VALUES ('c1', ARRAY['https://claude.ai/cb'])`); err != nil {
		t.Fatal(err)
	}
	var connID string
	if err := db.QueryRow(`INSERT INTO oauth_connections(client_id, owner_uid, scopes, resource) VALUES ('c1','u','{read}','r') RETURNING id`).Scan(&connID); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"revoked_reason": `UPDATE oauth_connections SET revoked_reason = 'OUTRO'`,
		"kind":           `INSERT INTO oauth_tokens(connection_id, kind, token_hash, expires_at) VALUES ('` + connID + `','X','h',now())`,
		"outcome":        `INSERT INTO mcp_tool_calls(call_id, tool, arguments_hash, outcome) VALUES (gen_random_uuid(),'t','h','TALVEZ')`,
		"status":         `INSERT INTO oauth_authorization_requests(client_id, redirect_uri, state, code_challenge, scopes, resource, status, expires_at) VALUES ('c1','u','s','c','{read}','r','X',now())`,
	} {
		if err := exec(q); err == nil {
			t.Errorf("constraint de %s não barrou valor inválido", name)
		}
	}
	tok := `INSERT INTO oauth_tokens(connection_id, kind, token_hash, expires_at) VALUES ('` + connID + `','ACCESS','dup',now())`
	if err := exec(tok); err != nil {
		t.Fatal(err)
	}
	if err := exec(tok); err == nil {
		t.Error("token_hash deveria ser único")
	}
	if err := exec(`INSERT INTO mcp_tool_calls(call_id, connection_id, tool, arguments_hash, outcome) VALUES (gen_random_uuid(),'` + connID + `','t','h','OK')`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`DELETE FROM oauth_connections WHERE id = '` + connID + `'`); err != nil {
		t.Fatal(err)
	}
	var tokens, calls int
	db.QueryRow(`SELECT COUNT(*) FROM oauth_tokens`).Scan(&tokens)
	db.QueryRow(`SELECT COUNT(*) FROM mcp_tool_calls WHERE connection_id IS NULL`).Scan(&calls)
	if tokens != 0 || calls != 1 {
		t.Errorf("tokens=%d auditoria órfã=%d, quero 0 e 1", tokens, calls)
	}

	if err := m.M.Migrate(8); err != nil {
		t.Fatalf("down 009: %v", err)
	}
	if tables() != 0 {
		t.Error("down deveria remover as tabelas")
	}
	if err := m.M.Migrate(9); err != nil {
		t.Fatalf("up 009 de novo: %v", err)
	}
	if tables() != 5 {
		t.Error("up após down deveria recriar as tabelas")
	}
}
