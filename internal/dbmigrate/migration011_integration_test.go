package dbmigrate

// Teste de integração da migração 011 (AYD-008@context / SPEC-009): semeia dados na versão 10
// (reposição recebida, venda sob encomenda, saldo de estoque), aplica a 011 e confere a PARIDADE
// (sem custos adicionais nada muda: a pagar da compra, Profit da Sale, custo da última entrada),
// o rateio com custos adicionais (contas por credor, Profit, custo de entrada, valor em estoque,
// unidades mistas), os triggers (inclusive troca de Supplier) e o ciclo down → up. Roda só com
// TEST_DATABASE_URL e precisa de CREATE DATABASE (banco descartável próprio; senão é pulado).
import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestIntegration_Migration011PurchaseComposition(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dbName := fmt.Sprintf("mig011_%d", time.Now().UnixNano())
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
	if err := m.M.Migrate(10); err != nil {
		t.Fatalf("migrate 10: %v", err)
	}
	// Protocolo simples: sem cache de statements preparados (as views mudam de tipo na migração).
	dbCfg := cfg.Copy()
	dbCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	dbConn := stdlib.RegisterConnConfig(dbCfg)
	defer stdlib.UnregisterConnConfig(dbConn)
	db, err := sql.Open("pgx", dbConn)
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
	scanF := func(q string, args ...any) (v float64) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatalf("%v: %v", q, err)
		}
		return
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.005 }

	const (
		cust  = "00000000-0000-0000-0000-0000000000c1"
		supA  = "00000000-0000-0000-0000-0000000000f1"
		supB  = "00000000-0000-0000-0000-0000000000f2"
		prodM = "00000000-0000-0000-0000-0000000000e1" // m2
		prodS = "00000000-0000-0000-0000-0000000000e2" // saco
		sale  = "00000000-0000-0000-0000-000000000001"
		purR  = "00000000-0000-0000-0000-0000000000d1" // reposição: 10 m2 × 100
		purM  = "00000000-0000-0000-0000-0000000000d2" // sob encomenda: 1 Item 4 × 50
	)
	exec(`INSERT INTO clientes (id, nome) VALUES ($1, 'Cliente')`, cust)
	exec(`INSERT INTO fornecedores (id, nome) VALUES ($1, 'Matheus RJ'), ($2, 'Jairo')`, supA, supB)
	exec(`INSERT INTO produtos (id, nome, unidade) VALUES ($1, 'Granito', 'm2'), ($2, 'Argamassa', 'saco')`, prodM, prodS)
	exec(`INSERT INTO vendas (id, cliente_id, data_venda) VALUES ($1, $2, '2026-10-01')`, sale, cust)
	exec(`INSERT INTO purchases (id, kind, supplier_id, status, received_at) VALUES ($1, 'STOCK_REPLENISHMENT', $2, 'RECEIVED', '2026-10-05')`, purR, supA)
	exec(`INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost) VALUES ($1, $2, 10, 100)`, purR, prodM)
	exec(`INSERT INTO purchases (id, kind, supplier_id) VALUES ($1, 'MADE_TO_ORDER', $2)`, purM, supA)
	exec(`INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit, origin, fornecedor_id, purchase_id)
	      VALUES ($1, 'g', 4, 200, 50, 'MADE_TO_ORDER', $2, $3)`, sale, supA, purM)

	payableBefore := scanF(`SELECT payable FROM vw_purchases_resumo WHERE id = $1`, purR)
	profitBefore := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, sale)
	lastCostBefore := scanF(`SELECT last_inflow_cost FROM vw_stock_balance WHERE product_id = $1`, prodM)

	if err := m.M.Migrate(11); err != nil {
		t.Fatalf("up 011: %v", err)
	}

	// Paridade: sem custos adicionais nada muda.
	if got := scanF(`SELECT payable FROM vw_purchases_resumo WHERE id = $1`, purR); !near(got, payableBefore) {
		t.Errorf("a pagar = %v, want %v", got, payableBefore)
	}
	if got := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, sale); !near(got, profitBefore) {
		t.Errorf("lucro = %v, want %v", got, profitBefore)
	}
	if got := scanF(`SELECT last_inflow_cost FROM vw_stock_balance WHERE product_id = $1`, prodM); !near(got, lastCostBefore) {
		t.Errorf("last_inflow_cost = %v, want %v", got, lastCostBefore)
	}
	var method string
	if err := db.QueryRow(`SELECT allocation_method::text FROM purchases WHERE id = $1`, purR).Scan(&method); err != nil || method != "BY_QUANTITY" {
		t.Errorf("allocation_method = %q (%v), want BY_QUANTITY", method, err)
	}
	if got := scanF(`SELECT stock_value FROM vw_stock_balance WHERE product_id = $1`, prodM); !near(got, 10*100) {
		t.Errorf("stock_value = %v, want 1000", got)
	}

	// Triggers: custo adicional (credor padrão e outro credor) → contas e rateio.
	exec(`INSERT INTO purchase_charges (purchase_id, kind, amount, payee_supplier_id) VALUES ($1, 'FREIGHT', 200, $2), ($1, 'TAX', 50, NULL), ($1, 'UNLOADING', 30, $3)`, purR, supA, supB)
	exec(`INSERT INTO purchase_charges (purchase_id, kind, amount, payee_supplier_id) VALUES ($1, 'FREIGHT', 40, $2)`, purM, supA)
	if got := scanF(`SELECT COUNT(*) FROM obligations WHERE purchase_id = $1 AND deleted_at IS NULL`, purR); got != 3 {
		t.Errorf("contas da compra = %v, want 3 (Supplier, Jairo, sem credor)", got)
	}
	if got := scanF(`SELECT amount FROM vw_obligations WHERE purchase_id = $1 AND counterparty_id = $2`, purR, supA); !near(got, 1200) {
		t.Errorf("conta do Supplier = %v, want 1200 (pedra 1000 + frete 200)", got)
	}
	if got := scanF(`SELECT payable FROM vw_purchases_resumo WHERE id = $1`, purR); !near(got, 1280) {
		t.Errorf("a pagar = %v, want 1280", got)
	}
	if got := scanF(`SELECT last_inflow_cost FROM vw_stock_balance WHERE product_id = $1`, prodM); !near(got, 100+28) {
		t.Errorf("custo de entrada = %v, want 128", got)
	}
	if got := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, sale); !near(got, profitBefore-40) {
		t.Errorf("lucro com custo da compra sob encomenda = %v, want %v", got, profitBefore-40)
	}
	exec(`UPDATE purchase_charges SET deleted_at = now() WHERE purchase_id = $1 AND kind = 'TAX'`, purR)
	if got := scanF(`SELECT COUNT(*) FROM obligations WHERE purchase_id = $1 AND deleted_at IS NULL AND counterparty_id IS NULL`, purR); got != 0 {
		t.Errorf("conta sem credor após excluir o custo = %v, want 0", got)
	}
	// Troca de Supplier da compra: custo com credor padrão acompanha.
	exec(`UPDATE purchases SET supplier_id = $2 WHERE id = $1`, purR, supB)
	if got := scanF(`SELECT COUNT(*) FROM purchase_charges WHERE purchase_id = $1 AND deleted_at IS NULL AND payee_supplier_id = $2`, purR, supB); got != 2 {
		t.Errorf("custos com o novo Supplier = %v, want 2", got)
	}
	if got := scanF(`SELECT COUNT(*) FROM obligations WHERE purchase_id = $1 AND deleted_at IS NULL`, purR); got != 1 {
		t.Errorf("contas após trocar o Supplier = %v, want 1", got)
	}
	// Unidades mistas: a view sinaliza.
	exec(`INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost) VALUES ($1, $2, 5, 20)`, purR, prodS)
	if got := scanF(`SELECT COUNT(*) FROM vw_purchase_line_costs WHERE purchase_id = $1 AND units_mixed`, purR); got != 2 {
		t.Errorf("units_mixed = %v linhas, want 2", got)
	}

	// ---- down → up ----
	if err := m.M.Migrate(10); err != nil {
		t.Fatalf("down 011: %v", err)
	}
	if got := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, sale); !near(got, profitBefore) {
		t.Errorf("lucro após o down = %v, want %v", got, profitBefore)
	}
	if got := scanF(`SELECT total_cost FROM vw_purchases_resumo WHERE id = $1`, purR); !near(got, 1100) {
		t.Errorf("total_cost após o down = %v, want 1100", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pg_class WHERE relname IN ('purchase_charges', 'vw_purchase_line_costs')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("estruturas da 011 após o down: %d (%v)", n, err)
	}
	if err := m.M.Migrate(11); err != nil {
		t.Fatalf("up 011 de novo: %v", err)
	}
}
