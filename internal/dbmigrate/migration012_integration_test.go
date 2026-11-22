package dbmigrate

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

func TestIntegration_Migration012SaleComposition(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dbName := fmt.Sprintf("mig012_%d", time.Now().UnixNano())
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
	if err := m.M.Migrate(11); err != nil {
		t.Fatalf("migrate 11: %v", err)
	}
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
		cust    = "00000000-0000-0000-0000-0000000000c1"
		partner = "00000000-0000-0000-0000-0000000000a1"
		supA    = "00000000-0000-0000-0000-0000000000f1"
		saleA   = "00000000-0000-0000-0000-000000000001"
		saleB   = "00000000-0000-0000-0000-000000000002"
	)
	exec(`INSERT INTO clientes (id, nome) VALUES ($1, 'Cliente')`, cust)
	exec(`INSERT INTO vendedores_externos (id, nome) VALUES ($1, 'Arq')`, partner)
	exec(`INSERT INTO fornecedores (id, nome) VALUES ($1, 'Motorista')`, supA)
	exec(`INSERT INTO vendas (id, cliente_id, vendedor_externo_id, data_venda, frete_valor, comissao_valor) VALUES ($1, $2, $3, '2026-10-01', 150, 80)`, saleA, cust, partner)
	exec(`INSERT INTO vendas (id, cliente_id, data_venda, comissao_valor, delivery_status) VALUES ($1, $2, '2026-10-05', 20, 'CANCELED')`, saleB, cust)
	exec(`INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit) VALUES ($1, 'g', 2, 1000, 300), ($2, 'g', 1, 500, 100)`, saleA, saleB)
	exec(`INSERT INTO financial_movements (id, occurred_on, amount, category) VALUES ('00000000-0000-0000-0000-0000000000b1', '2026-10-02', -50, 'FREIGHT')`)
	exec(`INSERT INTO settlements (obligation_id, movement_id, amount)
	      SELECT o.id, '00000000-0000-0000-0000-0000000000b1', 50 FROM obligations o
	      WHERE o.sale_id = $1 AND o.direction = 'PAYABLE' AND o.counterparty_id IS NULL AND o.deleted_at IS NULL`, saleA)

	profitBefore := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, saleA)
	totalBefore := scanF(`SELECT total_venda FROM vw_vendas_resumo WHERE id = $1`, saleA)
	obBefore := scanF(`SELECT COUNT(*) FROM obligations WHERE sale_id = $1 AND deleted_at IS NULL`, saleA)

	if err := m.M.Migrate(12); err != nil {
		t.Fatalf("up 012: %v", err)
	}

	if got := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, saleA); !near(got, profitBefore) || !near(got, 2000-600-150-80) {
		t.Errorf("lucro = %v, want %v", got, profitBefore)
	}
	if got := scanF(`SELECT total_venda FROM vw_vendas_resumo WHERE id = $1`, saleA); !near(got, totalBefore) {
		t.Errorf("total_venda = %v, want %v", got, totalBefore)
	}
	if got := scanF(`SELECT COUNT(*) FROM obligations WHERE sale_id = $1 AND deleted_at IS NULL`, saleA); got != obBefore {
		t.Errorf("contas = %v, want %v", got, obBefore)
	}
	if got := scanF(`SELECT amount FROM sale_costs WHERE sale_id = $1 AND kind = 'FREIGHT' AND payee_supplier_id IS NULL`, saleA); !near(got, 150) {
		t.Errorf("custo FREIGHT = %v, want 150", got)
	}
	if got := scanF(`SELECT COUNT(*) FROM sale_costs WHERE sale_id = $1 AND kind = 'COMMISSION' AND payee_partner_id = $2 AND amount = 80`, saleA, partner); got != 1 {
		t.Errorf("custo COMMISSION do Partner = %v, want 1", got)
	}
	if got := scanF(`SELECT settled FROM vw_obligations WHERE sale_id = $1 AND direction = 'PAYABLE' AND counterparty_id IS NULL`, saleA); !near(got, 50) {
		t.Errorf("quitação do frete preservada = %v, want 50", got)
	}
	if got := scanF(`SELECT costs_open FROM vw_vendas_resumo WHERE id = $1`, saleA); !near(got, 180) {
		t.Errorf("costs_open = %v, want 180", got)
	}
	if got := scanF(`SELECT COUNT(*) FROM sale_costs WHERE sale_id = $1 AND kind = 'COMMISSION' AND payee_partner_id IS NULL`, saleB); got != 1 {
		t.Errorf("comissão sem Partner = %v, want 1 (a definir)", got)
	}
	if got := scanF(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'vendas' AND column_name IN ('frete_valor', 'comissao_valor')`); got != 0 {
		t.Errorf("colunas legadas = %v, want 0", got)
	}

	exec(`INSERT INTO sale_charges (sale_id, kind, amount) VALUES ($1, 'FREIGHT', 100)`, saleA)
	if got := scanF(`SELECT total_venda FROM vw_vendas_resumo WHERE id = $1`, saleA); !near(got, 2100) {
		t.Errorf("total_venda com cobrança = %v, want 2100", got)
	}
	if got := scanF(`SELECT amount FROM vw_obligations WHERE sale_id = $1 AND direction = 'RECEIVABLE'`, saleA); !near(got, 2100) {
		t.Errorf("conta a receber = %v, want 2100", got)
	}
	if got := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, saleA); !near(got, profitBefore+100) {
		t.Errorf("lucro com cobrança = %v, want %v", got, profitBefore+100)
	}
	exec(`UPDATE vendas SET vendedor_externo_id = NULL WHERE id = $1`, saleA)
	if got := scanF(`SELECT COUNT(*) FROM sale_costs WHERE sale_id = $1 AND kind = 'COMMISSION' AND payee_partner_id IS NULL AND deleted_at IS NULL`, saleA); got != 1 {
		t.Errorf("comissão após remover o Partner = %v, want a definir", got)
	}

	if err := m.M.Migrate(11); err != nil {
		t.Fatalf("down 012: %v", err)
	}
	if got := scanF(`SELECT frete_valor FROM vendas WHERE id = $1`, saleA); !near(got, 150) {
		t.Errorf("frete_valor após o down = %v, want 150", got)
	}
	if got := scanF(`SELECT comissao_valor FROM vendas WHERE id = $1`, saleA); !near(got, 80) {
		t.Errorf("comissao_valor após o down = %v, want 80", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pg_class WHERE relname IN ('sale_costs', 'sale_charges', 'quote_charges')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("estruturas da 012 após o down: %d (%v)", n, err)
	}
	if err := m.M.Migrate(12); err != nil {
		t.Fatalf("up 012 de novo: %v", err)
	}
}
