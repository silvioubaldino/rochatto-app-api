package dbmigrate

// Teste de integração da migração 007 (AYD-004@context): colunas novas do Receipt,
// vw_vendas_resumo com receivable/freight_payable, função de alertas versionada, e o
// ciclo down → up. Roda só com TEST_DATABASE_URL e precisa de CREATE DATABASE (banco
// descartável próprio; senão é pulado).
import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestIntegration_Migration007Finance(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dbName := fmt.Sprintf("mig007_%d", time.Now().UnixNano())
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
	if err := m.M.Migrate(6); err != nil {
		t.Fatalf("migrate 6: %v", err)
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
	exists := func(q string) bool {
		t.Helper()
		var one int
		err := db.QueryRow(q).Scan(&one)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		return err == nil
	}

	// Arrange: Sale 006 com Receipts existentes (recebido e pendente vencido).
	const (
		cust = "00000000-0000-0000-0000-0000000000c1"
		sale = "00000000-0000-0000-0000-000000000001"
		recv = "00000000-0000-0000-0000-0000000000b1"
		pend = "00000000-0000-0000-0000-0000000000b2"
	)
	exec(`INSERT INTO clientes (id, nome) VALUES ($1, 'C')`, cust)
	exec(`INSERT INTO vendas (id, cliente_id, frete_valor, frete_pago) VALUES ($1, $2, 30, 10)`, sale, cust)
	exec(`INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit) VALUES ($1, 'x', 1, 100, 60)`, sale)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_pagamento) VALUES ($1, $2, 40, 'recebido', '2026-01-01')`, recv, sale)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_agendada) VALUES ($1, $2, 60, 'pendente', CURRENT_DATE - 1)`, pend, sale)

	// Act: up.
	if err := m.M.Migrate(7); err != nil {
		t.Fatalf("migrate 7: %v", err)
	}

	// Assert: Receipts existentes ficam com payment_method e deleted_at nulos (sem conversão).
	var method sql.NullString
	var deleted sql.NullTime
	if err := db.QueryRow(`SELECT payment_method::text, deleted_at FROM pagamentos_cliente WHERE id = $1`, recv).Scan(&method, &deleted); err != nil {
		t.Fatal(err)
	}
	if method.Valid || deleted.Valid {
		t.Errorf("legado deveria ficar nulo: %v %v", method, deleted)
	}
	// View: colunas antigas preservadas + novas.
	var total, recebido, lucro, receivable, freightPayable float64
	var status string
	if err := db.QueryRow(`SELECT total_venda, total_recebido, lucro, payment_status, receivable, freight_payable
		FROM vw_vendas_resumo WHERE id = $1`, sale).Scan(&total, &recebido, &lucro, &status, &receivable, &freightPayable); err != nil {
		t.Fatal(err)
	}
	if total != 100 || recebido != 40 || lucro != 10 || status != "PARTIAL" || receivable != 60 || freightPayable != 20 {
		t.Errorf("view = %v %v %v %s %v %v", total, recebido, lucro, status, receivable, freightPayable)
	}
	// Soft-delete tira o Receipt de total_recebido.
	exec(`UPDATE pagamentos_cliente SET deleted_at = NOW() WHERE id = $1`, recv)
	db.QueryRow(`SELECT total_recebido, payment_status FROM vw_vendas_resumo WHERE id = $1`, sale).Scan(&recebido, &status)
	if recebido != 0 || status != "PENDING" {
		t.Errorf("com Receipt excluído: recebido=%v status=%s", recebido, status)
	}
	exec(`UPDATE pagamentos_cliente SET deleted_at = NULL WHERE id = $1`, recv)

	// Função de alertas versionada: gera o vencido e mantém `lida` na segunda execução.
	exec(`SELECT gerar_notificacoes_pagamentos()`)
	var tipo string
	if err := db.QueryRow(`SELECT tipo FROM notificacoes WHERE pagamento_id = $1`, pend).Scan(&tipo); err != nil || tipo != "pagamento_vencido" {
		t.Fatalf("alerta = %q %v", tipo, err)
	}
	exec(`UPDATE notificacoes SET lida = TRUE WHERE pagamento_id = $1`, pend)
	exec(`SELECT gerar_notificacoes_pagamentos()`)
	var lida bool
	db.QueryRow(`SELECT lida FROM notificacoes WHERE pagamento_id = $1`, pend).Scan(&lida)
	if !lida {
		t.Error("lida deveria ser preservada")
	}

	// Down: volta ao schema 006 (colunas/tipo/view nova somem; função antiga funciona).
	if err := m.M.Migrate(6); err != nil {
		t.Fatalf("down para 6: %v", err)
	}
	if exists(`SELECT 1 FROM information_schema.columns WHERE table_name = 'pagamentos_cliente' AND column_name IN ('payment_method','deleted_at')`) ||
		exists(`SELECT 1 FROM pg_type WHERE typname = 'payment_method'`) ||
		exists(`SELECT 1 FROM information_schema.columns WHERE table_name = 'vw_vendas_resumo' AND column_name IN ('receivable','freight_payable')`) {
		t.Error("down deveria remover colunas, tipo e colunas novas da view")
	}
	if err := db.QueryRow(`SELECT total_recebido FROM vw_vendas_resumo WHERE id = $1`, sale).Scan(&recebido); err != nil || recebido != 40 {
		t.Errorf("view 006 após down: %v %v", recebido, err)
	}
	exec(`SELECT gerar_notificacoes_pagamentos()`)

	// Up de novo funciona.
	if err := m.M.Migrate(7); err != nil {
		t.Fatalf("up de novo: %v", err)
	}
	if !exists(`SELECT 1 FROM information_schema.columns WHERE table_name = 'pagamentos_cliente' AND column_name = 'payment_method'`) {
		t.Error("payment_method deveria existir após re-up")
	}
}
