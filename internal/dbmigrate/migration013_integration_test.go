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

func TestIntegration_Migration013ServicesOnSale(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dbName := fmt.Sprintf("mig013_%d", time.Now().UnixNano())
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
	if err := m.M.Migrate(12); err != nil {
		t.Fatalf("migrate 12: %v", err)
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
		cust  = "00000000-0000-0000-0000-0000000000c1"
		sale  = "00000000-0000-0000-0000-000000000001"
		quote = "00000000-0000-0000-0000-0000000000d1"
	)
	exec(`INSERT INTO clientes (id, nome) VALUES ($1, 'Cliente')`, cust)
	exec(`INSERT INTO vendas (id, cliente_id, data_venda) VALUES ($1, $2, '2026-10-01')`, sale, cust)
	exec(`INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit) VALUES ($1, 'Granito', 2, 1000, 300)`, sale)
	exec(`INSERT INTO quotes (id, customer_id, quote_date) VALUES ($1, $2, '2026-10-01')`, quote, cust)
	exec(`INSERT INTO quote_items (quote_id, product_name, quantity, unit_price, unit_cost) VALUES ($1, 'Granito', 2, 1000, 300)`, quote)
	totalBefore := scanF(`SELECT total_venda FROM vw_vendas_resumo WHERE id = $1`, sale)
	profitBefore := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, sale)

	if err := m.M.Migrate(13); err != nil {
		t.Fatalf("up 013: %v", err)
	}

	// Items existentes viram PRODUCT; origem e views intactas.
	if got := scanF(`SELECT COUNT(*) FROM itens_venda WHERE kind = 'PRODUCT' AND service_id IS NULL AND origin = 'MADE_TO_ORDER'`); got != 1 {
		t.Errorf("itens_venda PRODUCT = %v, want 1", got)
	}
	if got := scanF(`SELECT COUNT(*) FROM quote_items WHERE kind = 'PRODUCT' AND service_id IS NULL`); got != 1 {
		t.Errorf("quote_items PRODUCT = %v, want 1", got)
	}
	if got := scanF(`SELECT total_venda FROM vw_vendas_resumo WHERE id = $1`, sale); !near(got, totalBefore) {
		t.Errorf("total_venda = %v, want %v", got, totalBefore)
	}

	// Catálogo: nome único entre ativos, sem diferenciar caixa; excluído libera o nome.
	exec(`INSERT INTO services (name, unit) VALUES ('Instalação', 'm²')`)
	if _, err := db.Exec(`INSERT INTO services (name) VALUES ('INSTALAÇÃO')`); err == nil {
		t.Error("nome duplicado entre ativos deveria falhar")
	}
	if _, err := db.Exec(`INSERT INTO services (name, reference_price) VALUES ('Outro', -1)`); err == nil {
		t.Error("preço de referência negativo deveria falhar")
	}
	exec(`UPDATE services SET deleted_at = NOW() WHERE name = 'Instalação'`)
	exec(`INSERT INTO services (name) VALUES ('Instalação')`)

	// CHECKs de coerência por tipo.
	exec(`INSERT INTO produtos (nome) VALUES ('P')`)
	var svc string
	if err := db.QueryRow(`SELECT id FROM services WHERE name = 'Instalação' AND deleted_at IS NULL`).Scan(&svc); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"SERVICE com origin":     `INSERT INTO itens_venda (venda_id, kind, produto_nome, quantidade, preco_venda_unit, custo_unit) VALUES ($1, 'SERVICE', 'i', 1, 10, 0)`,
		"SERVICE com custo_unit": `INSERT INTO itens_venda (venda_id, kind, produto_nome, quantidade, preco_venda_unit, custo_unit, origin) VALUES ($1, 'SERVICE', 'i', 1, 10, 5, NULL)`,
		"PRODUCT sem origin":     `INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit, origin) VALUES ($1, 'p', 1, 10, 5, NULL)`,
		"PRODUCT com service_id": `INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit, service_id) VALUES ($1, 'p', 1, 10, 5, '` + svc + `')`,
		"SERVICE com produto_id": `INSERT INTO itens_venda (venda_id, kind, produto_id, produto_nome, quantidade, preco_venda_unit, custo_unit, origin) VALUES ($1, 'SERVICE', (SELECT id FROM produtos LIMIT 1), 'i', 1, 10, 0, NULL)`,
	} {
		if _, err := db.Exec(q, sale); err == nil {
			t.Errorf("%s deveria violar o CHECK", name)
		}
	}
	if _, err := db.Exec(`INSERT INTO quote_items (quote_id, kind, product_id, product_name, quantity, unit_price, unit_cost) VALUES ($1, 'SERVICE', (SELECT id FROM produtos LIMIT 1), 'i', 1, 10, 0)`, quote); err == nil {
		t.Error("quote_item SERVICE com product_id deveria violar o CHECK")
	}

	// Item de serviço: entra no preço e no Profit; origin NULL fica fora do Stock e da fila.
	exec(`INSERT INTO itens_venda (venda_id, kind, service_id, produto_nome, quantidade, preco_venda_unit, custo_unit, origin) VALUES ($1, 'SERVICE', $2, 'Instalação', 12, 300, 0, NULL)`, sale, svc)
	if got := scanF(`SELECT total_venda FROM vw_vendas_resumo WHERE id = $1`, sale); !near(got, totalBefore+3600) {
		t.Errorf("total_venda com serviço = %v, want %v", got, totalBefore+3600)
	}
	if got := scanF(`SELECT lucro FROM vw_vendas_resumo WHERE id = $1`, sale); !near(got, profitBefore+3600) {
		t.Errorf("lucro com serviço = %v, want %v", got, profitBefore+3600)
	}
	if got := scanF(`SELECT amount FROM vw_obligations WHERE sale_id = $1 AND direction = 'RECEIVABLE'`, sale); !near(got, totalBefore+3600) {
		t.Errorf("conta a receber com serviço = %v, want %v", got, totalBefore+3600)
	}

	// Down (lossy): o Item de serviço vira Item sob encomenda — a receita continua.
	if err := m.M.Migrate(12); err != nil {
		t.Fatalf("down 013: %v", err)
	}
	if got := scanF(`SELECT COUNT(*) FROM itens_venda WHERE venda_id = $1 AND origin = 'MADE_TO_ORDER'`, sale); got != 2 {
		t.Errorf("itens após o down = %v, want 2", got)
	}
	if got := scanF(`SELECT total_venda FROM vw_vendas_resumo WHERE id = $1`, sale); !near(got, totalBefore+3600) {
		t.Errorf("total_venda após o down = %v, want %v", got, totalBefore+3600)
	}
	if got := scanF(`SELECT COUNT(*) FROM pg_class WHERE relname = 'services'`); got != 0 {
		t.Errorf("tabela services após o down: %v", got)
	}
	if err := m.M.Migrate(13); err != nil {
		t.Fatalf("up 013 de novo: %v", err)
	}
}
