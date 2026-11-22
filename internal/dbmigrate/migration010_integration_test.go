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

type saleState struct {
	received, receivable, profit float64
	paymentStatus                string
}

func TestIntegration_Migration010ObligationsAndMovements(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dbName := fmt.Sprintf("mig010_%d", time.Now().UnixNano())
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
	if err := m.M.Migrate(9); err != nil {
		t.Fatalf("migrate 9: %v", err)
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
	scanF := func(q string, args ...any) (v float64) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatalf("%v: %v", q, err)
		}
		return
	}
	scanS := func(q string, args ...any) (v sql.NullString) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(&v); err != nil && err != sql.ErrNoRows {
			t.Fatalf("%v: %v", q, err)
		}
		return
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.005 }

	const (
		cust    = "00000000-0000-0000-0000-0000000000c1"
		partner = "00000000-0000-0000-0000-0000000000a1"
		supA    = "00000000-0000-0000-0000-0000000000f1"
		supB    = "00000000-0000-0000-0000-0000000000f2"
		saleA   = "00000000-0000-0000-0000-000000000001"
		saleB   = "00000000-0000-0000-0000-000000000002"
		saleC   = "00000000-0000-0000-0000-000000000003"
		saleD   = "00000000-0000-0000-0000-000000000004"
		rA1     = "00000000-0000-0000-0000-0000000000b1"
		rA2     = "00000000-0000-0000-0000-0000000000b2"
		rA3     = "00000000-0000-0000-0000-0000000000b3"
		rA4     = "00000000-0000-0000-0000-0000000000b4"
		rB1     = "00000000-0000-0000-0000-0000000000b5"
		rC1     = "00000000-0000-0000-0000-0000000000b6"
		rD1     = "00000000-0000-0000-0000-0000000000b7"
		purA    = "00000000-0000-0000-0000-0000000000d1"
		purB    = "00000000-0000-0000-0000-0000000000d2"
		purC    = "00000000-0000-0000-0000-0000000000d3"
		prod    = "00000000-0000-0000-0000-0000000000e1"
	)
	exec(`INSERT INTO clientes (id, nome) VALUES ($1, 'Cliente')`, cust)
	exec(`INSERT INTO vendedores_externos (id, nome) VALUES ($1, 'Arquiteta')`, partner)
	exec(`INSERT INTO fornecedores (id, nome) VALUES ($1, 'Matheus RJ'), ($2, 'Edmilson')`, supA, supB)
	exec(`INSERT INTO produtos (id, nome) VALUES ($1, 'Granito')`, prod)
	exec(`INSERT INTO vendas (id, cliente_id, vendedor_externo_id, data_venda, frete_valor, frete_pago, comissao_valor, comissao_paga) VALUES ($1, $2, $3, '2026-10-01', 150, 50, 80, TRUE)`, saleA, cust, partner)
	exec(`INSERT INTO vendas (id, cliente_id, data_venda, comissao_valor) VALUES ($1, $2, '2026-10-05', 20)`, saleB, cust)
	exec(`INSERT INTO vendas (id, cliente_id, data_venda, delivery_status) VALUES ($1, $2, '2026-10-06', 'CANCELED')`, saleC, cust)
	exec(`INSERT INTO vendas (id, cliente_id, data_venda) VALUES ($1, $2, '2026-10-07')`, saleD, cust)
	exec(`INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit) VALUES ($1, 'g', 2, 500, 200), ($2, 'm', 1, 500, 200), ($3, 'q', 1, 300, 100)`, saleA, saleB, saleC)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_pagamento, payment_method, created_at) VALUES ($1, $2, 400, 'recebido', '2026-10-02', 'PIX', '2026-10-02 12:00-03')`, rA1, saleA)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_agendada, created_at) VALUES ($1, $2, 300, 'pendente', '2026-12-15', '2026-10-03 12:00-03')`, rA2, saleA)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_agendada, created_at) VALUES ($1, $2, 100, 'recebido', '2026-10-20', '2026-10-04 12:00-03')`, rA3, saleA)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_pagamento, payment_method, deleted_at) VALUES ($1, $2, 999, 'recebido', '2026-10-09', 'PIX', now())`, rA4, saleA)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_pagamento, payment_method) VALUES ($1, $2, 500, 'recebido', '2026-10-06', 'CASH')`, rB1, saleB)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_pagamento, payment_method) VALUES ($1, $2, 100, 'recebido', '2026-10-06', 'PIX')`, rC1, saleC)
	exec(`INSERT INTO pagamentos_cliente (id, venda_id, valor, status, data_agendada) VALUES ($1, $2, 50, 'pendente', '2026-11-30')`, rD1, saleD)
	exec(`INSERT INTO purchases (id, kind, supplier_id, status, payment_status, paid_amount, paid_at) VALUES ($1, 'STOCK_REPLENISHMENT', $2, 'RECEIVED', 'PENDING', 70, '2026-10-10')`, purA, supA)
	exec(`INSERT INTO purchases (id, kind, supplier_id, status, payment_status, paid_amount, paid_at) VALUES ($1, 'STOCK_REPLENISHMENT', $2, 'RECEIVED', 'PAID', 80, '2026-10-11')`, purB, supB)
	exec(`INSERT INTO purchases (id, kind, supplier_id) VALUES ($1, 'STOCK_REPLENISHMENT', $2)`, purC, supA)
	exec(`INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost) VALUES ($1, $4, 2, 50), ($2, $4, 1, 80), ($3, $4, 1, 40)`, purA, purB, purC, prod)
	exec(`INSERT INTO notificacoes (tipo, venda_id, pagamento_id, cliente_nome, valor, data_ref, dias_restantes, lida) VALUES ('pagamento_vencendo', $1, $2, 'Cliente', 300, '2026-12-15', 3, TRUE)`, saleA, rA2)

	snapshot := func() (map[string]saleState, map[string]float64) {
		sales := map[string]saleState{}
		for _, id := range []string{saleA, saleB, saleC, saleD} {
			var s saleState
			if err := db.QueryRow(`SELECT total_recebido, receivable, lucro, payment_status FROM vw_vendas_resumo WHERE id = $1`, id).
				Scan(&s.received, &s.receivable, &s.profit, &s.paymentStatus); err != nil {
				t.Fatal(err)
			}
			sales[id] = s
		}
		return sales, nil
	}
	salesBefore, _ := snapshot()
	toPayBefore := map[string]float64{}
	for _, id := range []string{purA, purB, purC} {
		toPayBefore[id] = scanF(`SELECT to_pay FROM vw_purchases_resumo WHERE id = $1`, id)
	}
	if salesBefore[saleA].received != 500 || toPayBefore[purA] != 30 || toPayBefore[purB] != 0 || toPayBefore[purC] != 40 {
		t.Fatalf("fixture legada inesperada: %+v %v", salesBefore, toPayBefore)
	}

	if err := m.M.Migrate(10); err != nil {
		t.Fatalf("up 010: %v", err)
	}

	salesAfter, _ := snapshot()
	for id, before := range salesBefore {
		after := salesAfter[id]
		wantReceivable := before.receivable
		if id == saleC {
			wantReceivable = 0
		}
		if !near(after.received, before.received) || !near(after.receivable, wantReceivable) || after.paymentStatus != before.paymentStatus || !near(after.profit, before.profit) {
			t.Errorf("venda %s: antes %+v depois %+v (receivable esperado %v)", id, before, after, wantReceivable)
		}
	}
	for id, was := range toPayBefore {
		if got := scanF(`SELECT payable FROM vw_purchases_resumo WHERE id = $1`, id); !near(got, was) {
			t.Errorf("a pagar da compra %s: antes %v depois %v", id, was, got)
		}
	}
	if st := scanS(`SELECT payment_status FROM vw_purchases_resumo WHERE id = $1`, purB); st.String != "PAID" {
		t.Errorf("payment_status derivado da compra quitada = %v", st)
	}

	for id, want := range map[string]string{rA1: "2026-10-02", rA2: "2026-12-15", rA3: "2026-10-20", rB1: "2026-10-06", rD1: "2026-11-30"} {
		if got := scanS(`SELECT due_date::text FROM installments WHERE id = $1`, id); got.String != want {
			t.Errorf("parcela %s due_date = %q, want %s", id, got.String, want)
		}
	}
	if n := scanF(`SELECT COUNT(*) FROM installments WHERE id = $1`, rA4); n != 0 {
		t.Error("Receipt soft-deletado não deveria migrar")
	}
	if got := scanS(`SELECT payment_method::text FROM installments WHERE id = $1`, rA1); got.String != "PIX" {
		t.Errorf("payment_method da parcela = %v", got)
	}
	if got := scanS(`SELECT occurred_on::text FROM financial_movements WHERE id = $1 AND category = 'SALE_RECEIPT' AND amount = 400`, rA1); got.String != "2026-10-02" {
		t.Errorf("movimentação do Receipt recebido = %v", got)
	}
	if got := scanS(`SELECT occurred_on::text FROM financial_movements WHERE id = $1`, rA3); got.String != "2026-10-20" {
		t.Errorf("recebido sem data_pagamento = %v, want a data agendada", got)
	}
	if got := scanS(`SELECT payment_method::text FROM financial_movements WHERE id = $1`, rA3); got.Valid {
		t.Errorf("recebido legado sem método deveria ficar null: %v", got)
	}
	if n := scanF(`SELECT COUNT(*) FROM financial_movements WHERE id = $1`, rA2); n != 0 {
		t.Error("Receipt pendente não gera movimentação")
	}
	if n := scanF(`SELECT COUNT(*) FROM settlements s JOIN obligations o ON o.id = s.obligation_id
		JOIN financial_movements m ON m.id = s.movement_id
		WHERE o.purchase_id = $1 AND o.supplier_id = $2 AND m.category = 'PURCHASE_PAYMENT' AND m.amount = -70 AND m.occurred_on = '2026-10-10'`, purA, supA); n != 1 {
		t.Errorf("pagamento da compra A migrado %v vezes, want 1", n)
	}
	if n := scanF(`SELECT COUNT(*) FROM settlements s JOIN obligations o ON o.id = s.obligation_id AND o.sale_id = $1 AND o.counterparty_id IS NULL
		JOIN financial_movements m ON m.id = s.movement_id
		WHERE m.category = 'FREIGHT' AND m.amount = -50 AND m.occurred_on = '2026-10-01'`, saleA); n != 1 {
		t.Errorf("frete pago migrado %v vezes, want 1", n)
	}
	if n := scanF(`SELECT COUNT(*) FROM settlements s JOIN obligations o ON o.id = s.obligation_id AND o.sale_id = $1 AND o.partner_id = $2
		JOIN financial_movements m ON m.id = s.movement_id
		WHERE m.category = 'COMMISSION' AND m.amount = -80 AND m.occurred_on = '2026-10-01'`, saleA, partner); n != 1 {
		t.Errorf("comissão paga migrada %v vezes, want 1", n)
	}
	if got := scanF(`SELECT freight_payable FROM vw_vendas_resumo WHERE id = $1`, saleA); got != 100 {
		t.Errorf("freight_payable da venda A = %v, want 100", got)
	}
	if got := scanF(`SELECT commission_payable FROM vw_vendas_resumo WHERE id = $1`, saleA); got != 0 {
		t.Errorf("commission_payable da venda A (paga) = %v, want 0", got)
	}
	if got := scanF(`SELECT commission_payable FROM vw_vendas_resumo WHERE id = $1`, saleB); got != 20 {
		t.Errorf("commission_payable da venda B (sem Partner, não paga) = %v, want 20", got)
	}
	if got := scanF(`SELECT COALESCE(SUM(amount) FILTER (WHERE amount > 0), 0) FROM financial_movements`); got != 400+100+500+100 {
		t.Errorf("Σ entradas = %v, want 1100", got)
	}
	if got := scanF(`SELECT COALESCE(-SUM(amount) FILTER (WHERE amount < 0), 0) FROM financial_movements`); got != 70+80+50+80 {
		t.Errorf("Σ saídas = %v, want 280", got)
	}
	if n := scanF(`SELECT COUNT(*) FROM fornecedores WHERE kind <> 'STONE'`); n != 0 {
		t.Errorf("fornecedores não-STONE após a migração: %v", n)
	}
	var lida bool
	var oblID, instID sql.NullString
	if err := db.QueryRow(`SELECT lida, obligation_id::text, installment_id::text FROM notificacoes WHERE installment_id = $1`, rA2).Scan(&lida, &oblID, &instID); err != nil || !lida || !oblID.Valid {
		t.Errorf("alerta migrado: lida=%v obl=%v err=%v", lida, oblID, err)
	}
	for _, q := range []string{
		`SELECT 1 FROM information_schema.tables WHERE table_name = 'pagamentos_cliente'`,
		`SELECT 1 FROM information_schema.columns WHERE table_name = 'purchases' AND column_name IN ('payment_status','paid_amount','paid_at')`,
		`SELECT 1 FROM information_schema.columns WHERE table_name = 'vendas' AND column_name IN ('frete_pago','comissao_paga')`,
	} {
		var one int
		if err := db.QueryRow(q).Scan(&one); err != sql.ErrNoRows {
			t.Errorf("legado deveria ter sido removido: %s", q)
		}
	}

	// Triggers: depois da migração, editar a Sale recalcula a conta (sem ação manual).
	exec(`UPDATE vendas SET frete_valor = 200 WHERE id = $1`, saleA)
	if got := scanF(`SELECT amount FROM vw_obligations WHERE sale_id = $1 AND direction = 'PAYABLE' AND counterparty_id IS NULL`, saleA); got != 200 {
		t.Errorf("conta do frete após editar a Sale = %v, want 200", got)
	}
	exec(`UPDATE vendas SET frete_valor = 150 WHERE id = $1`, saleA)

	if err := m.M.Migrate(9); err != nil {
		t.Fatalf("down 010: %v", err)
	}
	salesDown, _ := snapshot()
	for id, before := range salesBefore {
		if id == saleC {
			continue
		}
		if after := salesDown[id]; !near(after.received, before.received) || after.paymentStatus != before.paymentStatus || !near(after.profit, before.profit) {
			t.Errorf("após o down, venda %s: antes %+v depois %+v", id, before, after)
		}
	}
	if got := scanF(`SELECT frete_pago FROM vendas WHERE id = $1`, saleA); got != 50 {
		t.Errorf("frete_pago após o down = %v, want 50", got)
	}
	if got := scanS(`SELECT comissao_paga::text FROM vendas WHERE id = $1`, saleA); got.String != "true" {
		t.Errorf("comissao_paga após o down = %v", got)
	}
	for _, id := range []string{purA, purB, purC} {
		if got := scanF(`SELECT to_pay FROM vw_purchases_resumo WHERE id = $1`, id); !near(got, toPayBefore[id]) {
			t.Errorf("to_pay da compra %s após o down = %v, want %v", id, got, toPayBefore[id])
		}
	}
	if err := m.M.Migrate(10); err != nil {
		t.Fatalf("up 010 de novo: %v", err)
	}
	salesAgain, _ := snapshot()
	for id := range salesBefore {
		if a, b := salesAfter[id], salesAgain[id]; !near(a.received, b.received) || !near(a.receivable, b.receivable) || a.paymentStatus != b.paymentStatus {
			t.Errorf("up após down, venda %s: %+v vs %+v", id, a, b)
		}
	}
}
