package dbmigrate

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestIntegration_Migration006LegacyMapping(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dbName := fmt.Sprintf("mig006_%d", time.Now().UnixNano())
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
	if err := m.M.Migrate(5); err != nil {
		t.Fatalf("migrate 5: %v", err)
	}

	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", strings.SplitN(q, "\n", 2)[0], err)
		}
	}

	const (
		cust = "00000000-0000-0000-0000-0000000000c1"
		supA = "00000000-0000-0000-0000-0000000000f1"
		supB = "00000000-0000-0000-0000-0000000000f2"
		prod = "00000000-0000-0000-0000-0000000000a1"
	)
	exec(`INSERT INTO clientes (id, nome) VALUES ($1, 'C')`, cust)
	exec(`INSERT INTO fornecedores (id, nome) VALUES ($1, 'A'), ($2, 'B')`, supA, supB)
	exec(`INSERT INTO produtos (id, nome) VALUES ($1, 'P')`, prod)
	statuses := []string{"A_ENTREGAR", "A_ENTREGAR_DATA", "A_RETIRAR", "ENTREGUE_PARCIAL", "ENTREGUE"}
	sale := func(i int) string { return fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i+1) }
	for i, s := range statuses {
		exec(`INSERT INTO vendas (id, cliente_id, status) VALUES ($1, $2, $3)`, sale(i), cust, s)
	}
	exec(`INSERT INTO vendas (id, cliente_id, status, deleted_at) VALUES ('00000000-0000-0000-0000-000000000009', $1, 'ENTREGUE', NOW())`, cust)
	item := func(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-0000000001a%d", n) }
	exec(`INSERT INTO itens_venda (id, venda_id, produto_nome, fornecedor_id, quantidade, preco_venda_unit, custo_unit, pago_fornecedor) VALUES
		($1,  $7, 'x', $12, 1, 200, 100, 50),  -- A_ENTREGAR, Supplier A, pago parcial → Purchase A
		($2,  $7, 'x', $12, 1, 200, 100, 0),   -- A_ENTREGAR, Supplier A, sem pagamento → fila
		($3,  $8, 'x', $12, 2, 200, 100, 0),   -- ENTREGUE, Supplier A → Purchase A
		($4,  $8, 'x', $13, 3, 200, 100, 300), -- ENTREGUE, Supplier B, pago total → Purchase B PAID
		($5,  $8, 'x', NULL, 1, 200, 100, 0),  -- sem Supplier → fila
		($6,  $9, 'x', NULL, 1, 200, 100, 10), -- sem Supplier com pago → fora de Purchase
		($10, $11, 'x', $12, 1, 200, 100, 80)  -- Sale excluída → fora de Purchase`,
		item(1), item(2), item(3), item(4), item(5), item(6), sale(0), sale(4), sale(1), item(7),
		"00000000-0000-0000-0000-000000000009", supA, supB)
	exec(`INSERT INTO estoque (produto_id, produto_nome, quantidade_entrada, custo_unit, data_entrada, observacoes) VALUES
		($1, 'P', 10, 55, '2026-01-10', 'carga'), (NULL, 'Sem produto', 5, 40, '2026-01-11', NULL)`, prod)

	if err := m.M.Migrate(6); err != nil {
		t.Fatalf("migrate 6: %v", err)
	}

	for i, s := range statuses {
		var got string
		db.QueryRow(`SELECT delivery_status FROM vendas WHERE id = $1`, sale(i)).Scan(&got)
		want := "TO_DELIVER"
		if s == "ENTREGUE" {
			want = "DELIVERED"
		}
		if got != want {
			t.Errorf("%s → %s, want %s", s, got, want)
		}
	}
	type pur struct {
		status, payment string
		paid, total     float64
		toPay           float64
		lines           int
		notes           string
	}
	purchases := map[string]pur{}
	rows, err := db.Query(`SELECT supplier_id, status, payment_status, paid_amount, total_cost, to_pay, line_count, notes FROM vw_purchases_resumo`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var sup string
		var p pur
		if err := rows.Scan(&sup, &p.status, &p.payment, &p.paid, &p.total, &p.toPay, &p.lines, &p.notes); err != nil {
			t.Fatal(err)
		}
		purchases[sup] = p
	}
	rows.Close()
	if len(purchases) != 2 {
		t.Fatalf("purchases legadas = %d, want 2 (%v)", len(purchases), purchases)
	}
	if a := purchases[supA]; a.status != "RECEIVED" || a.payment != "PENDING" || a.paid != 50 || a.total != 300 || a.toPay != 250 || a.lines != 2 ||
		a.notes != "Migração: itens anteriores ao módulo de compras" {
		t.Errorf("Purchase A = %+v", a)
	}
	if b := purchases[supB]; b.status != "RECEIVED" || b.payment != "PAID" || b.paid != 300 || b.toPay != 0 || b.lines != 1 {
		t.Errorf("Purchase B = %+v", b)
	}
	inPurchase := map[string]bool{item(1): true, item(3): true, item(4): true}
	for n := 1; n <= 7; n++ {
		var linked bool
		var origin string
		db.QueryRow(`SELECT purchase_id IS NOT NULL, origin FROM itens_venda WHERE id = $1`, item(n)).Scan(&linked, &origin)
		if linked != inPurchase[item(n)] || origin != "MADE_TO_ORDER" {
			t.Errorf("item %d: em Purchase=%v origin=%s, want %v/MADE_TO_ORDER", n, linked, origin, inPurchase[item(n)])
		}
	}
	var n int
	var qty, cost float64
	var reason, adjustedAt string
	db.QueryRow(`SELECT COUNT(*) FROM stock_adjustments`).Scan(&n)
	db.QueryRow(`SELECT quantity, unit_cost, reason, adjusted_at::text FROM stock_adjustments`).Scan(&qty, &cost, &reason, &adjustedAt)
	if n != 1 || qty != 10 || cost != 55 || reason != "INITIAL" || adjustedAt != "2026-01-10" {
		t.Errorf("ajustes = %d (%v %v %s %s)", n, qty, cost, reason, adjustedAt)
	}
	var balance, lastCost float64
	db.QueryRow(`SELECT balance, last_inflow_cost FROM vw_stock_balance WHERE product_id = $1`, prod).Scan(&balance, &lastCost)
	if balance != 10 || lastCost != 55 {
		t.Errorf("balance = %v custo = %v, want 10/55", balance, lastCost)
	}
	for _, q := range []string{
		`SELECT 1 FROM information_schema.columns WHERE table_name = 'itens_venda' AND column_name = 'pago_fornecedor'`,
		`SELECT 1 FROM information_schema.columns WHERE table_name = 'vendas' AND column_name = 'status'`,
		`SELECT 1 FROM information_schema.tables WHERE table_name = 'estoque'`,
		`SELECT 1 FROM information_schema.columns WHERE table_name = 'vw_vendas_resumo' AND column_name = 'a_pagar_fornecedor'`,
	} {
		var one int
		if err := db.QueryRow(q).Scan(&one); err != sql.ErrNoRows {
			t.Errorf("legado ainda existe: %s", q)
		}
	}

	if err := m.M.Migrate(5); err != nil {
		t.Fatalf("down para 5: %v", err)
	}
	var st string
	var pago float64
	db.QueryRow(`SELECT status FROM vendas WHERE id = $1`, sale(4)).Scan(&st)
	db.QueryRow(`SELECT pago_fornecedor FROM itens_venda WHERE id = $1`, item(4)).Scan(&pago)
	db.QueryRow(`SELECT COUNT(*) FROM estoque`).Scan(&n)
	if st != "ENTREGUE" || pago != 300 || n != 1 {
		t.Errorf("down: status=%s pago=%v estoque=%d", st, pago, n)
	}
	var aPagar float64
	if err := db.QueryRow(`SELECT a_pagar_fornecedor FROM vw_vendas_resumo WHERE id = $1`, sale(4)).Scan(&aPagar); err != nil {
		t.Errorf("vw_vendas_resumo da 005 após down: %v", err)
	}
	if err := m.M.Migrate(6); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
}
