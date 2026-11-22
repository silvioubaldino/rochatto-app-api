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

func TestIntegration_Migration008QuoteQuickSale(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dbName := fmt.Sprintf("mig008_%d", time.Now().UnixNano())
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
	if err := m.M.Migrate(7); err != nil {
		t.Fatalf("migrate 7: %v", err)
	}
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%v: %v", q, err)
		}
	}
	colExists := func() bool {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_name IN ('quotes', 'vw_quotes_resumo') AND column_name = 'quick_sale'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}

	const cust = "00000000-0000-0000-0000-0000000000c1"
	exec(`INSERT INTO clientes (id, nome) VALUES ($1, 'C')`, cust)
	quote := func(id, status string) {
		exec(`INSERT INTO quotes (id, customer_id, status, quote_date, created_at)
			VALUES ($1, $2, $3::quote_status, '2026-03-10', '2026-03-10 12:00:00+00')`, id, cust, status)
	}
	sale := func(quoteID, dataVenda, createdAt string) {
		exec(`INSERT INTO vendas (quote_id, cliente_id, data_venda, created_at) VALUES ($1, $2, $3, $4)`,
			quoteID, cust, dataVenda, createdAt)
	}
	const (
		qQuick   = "00000000-0000-0000-0000-000000000001"
		qSlow    = "00000000-0000-0000-0000-000000000002"
		qOtherDt = "00000000-0000-0000-0000-000000000003"
		qOpen    = "00000000-0000-0000-0000-000000000004"
		qNoSale  = "00000000-0000-0000-0000-000000000005"
	)
	quote(qQuick, "WON")
	sale(qQuick, "2026-03-10", "2026-03-10 12:00:02+00")
	quote(qSlow, "WON")
	sale(qSlow, "2026-03-10", "2026-03-10 12:01:00+00")
	quote(qOtherDt, "WON")
	sale(qOtherDt, "2026-03-15", "2026-03-10 12:00:02+00")
	quote(qOpen, "OPEN")
	quote(qNoSale, "WON")

	if err := m.M.Migrate(8); err != nil {
		t.Fatalf("migrate 8: %v", err)
	}

	flag := func(id string) bool {
		t.Helper()
		var v bool
		if err := db.QueryRow(`SELECT quick_sale FROM vw_quotes_resumo WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !flag(qQuick) {
		t.Error("Quote da venda rápida (+2 s) deveria receber quick_sale = true")
	}
	for name, id := range map[string]string{"Sale 1 min depois": qSlow, "data_venda diferente": qOtherDt, "OPEN": qOpen, "WON sem Sale": qNoSale} {
		if flag(id) {
			t.Errorf("%s não deveria ser marcado quick_sale", name)
		}
	}
	exec(`INSERT INTO quotes (id, customer_id) VALUES ('00000000-0000-0000-0000-000000000009', $1)`, cust)
	if flag("00000000-0000-0000-0000-000000000009") {
		t.Error("DEFAULT deveria ser false")
	}

	if err := m.M.Migrate(7); err != nil {
		t.Fatalf("down para 7: %v", err)
	}
	if colExists() {
		t.Error("down deveria remover quick_sale da tabela e da view")
	}
	var total float64
	if err := db.QueryRow(`SELECT total_quote FROM vw_quotes_resumo WHERE id = $1`, qQuick).Scan(&total); err != nil {
		t.Errorf("view 007 após down: %v", err)
	}

	if err := m.M.Migrate(8); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
	if !flag(qQuick) || flag(qSlow) {
		t.Error("backfill deveria ser refeito no re-up")
	}
}
