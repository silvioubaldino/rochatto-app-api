package handlers

import (
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"testing"
)

func newFinanceEnv(t *testing.T) fxEnv {
	t.Helper()
	e := newFulfillmentEnv(t)
	NewFinanceHandler(e.db).Register(e.api.mux)
	return e
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func (e fxEnv) wantNum(label string, m map[string]any, key string, want float64) {
	e.t.Helper()
	if got := num(m, key); !near(got, want) {
		e.t.Errorf("%s.%s = %v, want %v", label, key, got, want)
	}
}

func (e fxEnv) simpleSale(qty, price, cost float64) string {
	e.t.Helper()
	id, _ := e.sale(qItem("Pedra", qty, price, cost, ""))
	return id
}

func dayStr(offset int) string { return todayDate().AddDate(0, 0, offset).Format("2006-01-02") }

func TestIntegration_ReceiveSettlesReceivableAccount(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)
	acc := e.api.receivable(sale)

	if code, res := e.api.do("POST", "/obligations/"+acc+"/settle", map[string]any{"amount": 100, "movement": map[string]any{"occurred_on": "2026-11-01", "payment_method": "CHEQUE"}}); code != 400 {
		t.Fatalf("método inválido = %d %v, want 400", code, res)
	}
	if code, _ := e.api.do("POST", "/obligations/"+acc+"/settle", map[string]any{"amount": 1500, "movement": map[string]any{"occurred_on": "2026-11-01", "payment_method": "PIX"}}); code != http.StatusConflict {
		t.Errorf("receber acima do devido = %d, want 409", code)
	}
	code, res := e.api.receive(sale, 100, "2026-11-01", "PIX")
	mv, _ := res["movement"].(map[string]any)
	if code != 201 || mv["category"] != "SALE_RECEIPT" || num(mv, "amount") != 100 || mv["payment_method"] != "PIX" {
		t.Fatalf("receber = %d %v", code, res)
	}
	_, s := e.api.do("GET", "/vendas/"+sale, nil)
	if s["payment_status"] != "PARTIAL" || num(s, "total_recebido") != 100 || num(s, "receivable") != 900 {
		t.Errorf("venda após receber = %v", s)
	}
	if _, ok := s["pagamentos"]; ok {
		t.Error("detalhe da venda ainda traz pagamentos (Receipt)")
	}

	settlementID := mv["settlements"].([]any)[0].(map[string]any)["id"].(string)
	if code, _ := e.api.do("DELETE", "/settlements/"+settlementID, nil); code != 204 {
		t.Fatalf("DELETE settlement = %d", code)
	}
	if code, _ := e.api.do("DELETE", "/settlements/"+settlementID, nil); code != 404 {
		t.Errorf("DELETE repetido = %d, want 404", code)
	}
	_, s = e.api.do("GET", "/vendas/"+sale, nil)
	if s["payment_status"] != "PENDING" || num(s, "total_recebido") != 0 || num(s, "receivable") != 1000 {
		t.Errorf("venda após desfazer = %v", s)
	}
	_, m := e.api.do("GET", "/financial-movements?unallocated_only=true&category=SALE_RECEIPT&from=2026-11-01&to=2026-11-01", nil)
	found := false
	for _, it := range m["items"].([]any) {
		if it.(map[string]any)["id"] == mv["id"] && num(it.(map[string]any), "unallocated") == 100 {
			found = true
		}
	}
	if !found {
		t.Errorf("movimentação sem alocação não listada: %v", m)
	}

	for _, c := range [][2]string{{"GET", "/vendas/" + sale + "/pagamentos"}, {"POST", "/vendas/" + sale + "/pagamentos"},
		{"PUT", "/vendas/" + sale + "/pagamentos/" + settlementID}, {"DELETE", "/vendas/" + sale + "/pagamentos/" + settlementID},
		{"POST", "/vendas/" + sale + "/pagamentos/" + settlementID + "/confirmar"}} {
		if code, _ := e.api.do(c[0], c[1], map[string]any{"valor": 1}); code != 404 {
			t.Errorf("%s %s = %d, want 404 (rota removida)", c[0], c[1], code)
		}
	}
}

func TestIntegration_ReceiveBlockedOnCanceledSale(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)
	acc := e.api.receivable(sale)
	if code, res := e.api.receive(sale, 100, "2026-11-01", "PIX"); code != 201 {
		t.Fatalf("receber = %d %v", code, res)
	}
	if code, res := e.api.do("PATCH", "/vendas/"+sale+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatalf("cancelar = %d %v", code, res)
	}
	code, res := e.api.do("POST", "/settlements", map[string]any{
		"movement":    map[string]any{"occurred_on": "2026-11-02", "payment_method": "PIX"},
		"allocations": []map[string]any{{"obligation_id": acc, "amount": 10}},
	})
	if code != http.StatusConflict || res["error"] != "venda cancelada: recebimentos bloqueados" {
		t.Errorf("receber em venda cancelada = %d %v, want 409 venda cancelada", code, res)
	}
	code, d := e.api.do("GET", "/obligations/"+acc, nil)
	if code != 200 || num(d, "amount") != 0 || num(d, "settled") != 100 || len(d["settlements"].([]any)) != 1 {
		t.Errorf("conta da venda cancelada = %d %v, want amount 0 e quitação visível", code, d)
	}
}

func TestIntegration_SaleBalancesAndFilters(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(2, 100, 60)
	_, d := e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("novo", d, "receivable", 200)
	e.wantNum("novo", d, "costs_total", 0)

	for _, c := range []map[string]any{{"kind": "FREIGHT", "amount": 50}, {"kind": "COMMISSION", "amount": 10}} {
		if code, res := e.api.do("POST", "/vendas/"+sale+"/costs", c); code != 201 {
			t.Fatalf("POST costs = %d %v", code, res)
		}
	}
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("custos", d, "costs_total", 60)
	e.wantNum("custos", d, "costs_open", 60)
	e.wantNum("custos", d, "costs_paid", 0)
	if _, has := d["freight_payable"]; has {
		t.Error("freight_payable saiu da Sale (AYD-009)")
	}

	for _, body := range []map[string]any{{"frete_valor": 50}, {"comissao_valor": 10}, {"frete_pago": 20}, {"comissao_paga": true}} {
		if code, res := e.api.do("PUT", "/vendas/"+sale, body); code != 400 {
			t.Errorf("PUT %v = %d %v, want 400", body, code, res)
		}
	}

	payable := e.api.payableOf("SALE", sale, "")
	pay := func(amount float64, category string) {
		t.Helper()
		if code, res := e.api.do("POST", "/obligations/"+payable+"/settle", map[string]any{"amount": amount,
			"movement": map[string]any{"occurred_on": "2026-11-01", "payment_method": "PIX", "category": category}}); code != 201 {
			t.Fatalf("pagar %v %s = %d %v", amount, category, code, res)
		}
	}
	pay(20, "FREIGHT")
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("frete parcial", d, "costs_paid", 20)
	e.wantNum("frete parcial", d, "costs_open", 40)

	e.api.receive(sale, 80, "2026-11-01", "PIX")
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("parcial", d, "receivable", 120)
	e.api.receive(sale, 120, "2026-11-02", "PIX")
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("quitada", d, "receivable", 0)
	_, list := e.api.list("/vendas?cliente_id=" + e.cid)
	if !containsID(list, sale) || list[0]["costs_open"] == nil || list[0]["receivable"] == nil {
		t.Errorf("lista deveria expor receivable/costs_open: %v", list)
	}

	has := func(q string) bool {
		t.Helper()
		code, l := e.api.list("/vendas?" + q)
		if code != 200 {
			t.Fatalf("GET /vendas?%s = %d", q, code)
		}
		return containsID(l, sale)
	}
	if !has("cliente_id=" + e.cid) {
		t.Error("filtro por cliente")
	}

	e.db.Exec("UPDATE vendas SET data_venda = '2090-06-15' WHERE id = ?", sale)
	for q, want := range map[string]bool{
		"from=2090-06-15&to=2090-06-15": true,
		"from=2090-06-16":               false,
		"to=2090-06-14":                 false,
		"from=2090-06-01&to=2090-06-30": true,
	} {
		if got := has(q); got != want {
			t.Errorf("filtro %s: got %v, want %v", q, got, want)
		}
	}
	for _, q := range []string{"from=2090-13-01", "to=ontem"} {
		if code, _ := e.api.list("/vendas?" + q); code != 400 {
			t.Errorf("%s = %d, want 400", q, code)
		}
	}
}

func containsID(list []map[string]any, id string) bool {
	for _, r := range list {
		if r["id"] == id {
			return true
		}
	}
	return false
}

type finBalances struct{ receivable, payable float64 }

func (e fxEnv) balances() finBalances {
	e.t.Helper()
	code, res := e.api.do("GET", "/finance/summary", nil)
	if code != 200 {
		e.t.Fatalf("GET /finance/summary = %d %v", code, res)
	}
	b := res["balances"].(map[string]any)
	return finBalances{num(b, "receivable"), num(b, "payable")}
}

func TestIntegration_FinanceSummary(t *testing.T) {
	e := newFinanceEnv(t)

	year := 2100 + rand.Intn(7000)
	from, to := fmt.Sprintf("%d-05-01", year), fmt.Sprintf("%d-05-31", year)
	inP := fmt.Sprintf("%d-05-10", year)
	outP := fmt.Sprintf("%d-07-01", year)

	before := e.balances()

	partner := e.partner()
	saleA := e.simpleSale(2, 100, 60)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ?, vendedor_externo_id = ? WHERE id = ?`, inP, partner, saleA)
	setLegacyCosts(t, e.db, saleA, 10, 5)
	e.api.receive(saleA, 80, inP, "PIX")
	e.api.receive(saleA, 20, inP, "")
	_, undone := e.api.receive(saleA, 30, inP, "CARD")
	if code, _ := e.api.do("DELETE", "/financial-movements/"+undone["movement"].(map[string]any)["id"].(string), nil); code != 204 {
		t.Fatal("DELETE da movimentação")
	}
	e.api.receive(saleA, 10, outP, "PIX")

	if code, res := e.api.do("POST", "/obligations/"+e.api.payableOf("SALE", saleA, "")+"/settle", map[string]any{"amount": 4,
		"movement": map[string]any{"occurred_on": inP, "payment_method": "PIX"}}); code != 201 {
		t.Fatalf("pagar frete = %d %v", code, res)
	}
	if code, res := e.api.do("POST", "/obligations/"+e.api.payableOf("SALE", saleA, partner)+"/settle", map[string]any{
		"movement": map[string]any{"occurred_on": inP, "payment_method": "PIX"}}); code != 201 {
		t.Fatalf("pagar comissão = %d %v", code, res)
	}

	saleB := e.simpleSale(1, 500, 100)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, inP, saleB)
	setLegacyCosts(t, e.db, saleB, 7, 3)
	e.api.receive(saleB, 50, inP, "PIX")
	if code, _ := e.api.do("PATCH", "/vendas/"+saleB+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatal("cancelar Sale B")
	}

	saleC := e.simpleSale(1, 999, 1)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, outP, saleC)

	sup := e.supplier()
	pid := e.product()
	var purchase string
	e.db.Raw(`INSERT INTO purchases (kind, supplier_id) VALUES ('STOCK_REPLENISHMENT', ?) RETURNING id`, sup).Scan(&purchase)
	mustExec(t, e.db, `INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost) VALUES (?, ?, 2, 50)`, purchase, pid)
	pacc := e.api.payableOf("PURCHASE", purchase, sup)
	for _, p := range []struct {
		amount float64
		day    string
	}{{70, inP}, {5, outP}} {
		if code, res := e.api.do("POST", "/obligations/"+pacc+"/settle", map[string]any{"amount": p.amount,
			"movement": map[string]any{"occurred_on": p.day, "payment_method": "PIX"}}); code != 201 {
			t.Fatalf("pagar compra = %d %v", code, res)
		}
	}

	for _, m := range []map[string]any{
		{"occurred_on": inP, "amount": -317.87, "category": "VEHICLE", "payment_method": "PIX"},
		{"occurred_on": inP, "amount": -40, "category": "VEHICLE"},
		{"occurred_on": inP, "amount": -100, "category": "LABOR", "counterparty_name": "Diarista"},
		{"occurred_on": outP, "amount": -999, "category": "TAXES"},
		{"occurred_on": inP, "amount": 2100, "category": "OTHER_INCOME", "payment_method": "TRANSFER"},
	} {
		if code, res := e.api.do("POST", "/financial-movements", m); code != 201 {
			t.Fatalf("POST movimentação = %d %v", code, res)
		}
	}

	code, res := e.api.do("GET", "/finance/summary?from="+from+"&to="+to, nil)
	if code != 200 {
		t.Fatalf("summary = %d %v", code, res)
	}

	if p := res["period"].(map[string]any); p["from"] != from || p["to"] != to {
		t.Errorf("period = %v", p)
	}
	sales := res["sales"].(map[string]any)
	e.wantNum("sales", sales, "count", 1)
	e.wantNum("sales", sales, "revenue", 200)
	e.wantNum("sales", sales, "cost", 120)
	e.wantNum("sales", sales, "freight", 10)
	e.wantNum("sales", sales, "commission", 5)
	e.wantNum("sales", sales, "profit", 65)
	e.wantNum("sales", sales, "received", 110)
	e.wantNum("sales", sales, "receivable", 90)

	cash := res["cash"].(map[string]any)
	e.wantNum("cash", cash, "inflow", 2250)
	e.wantNum("cash", cash, "outflow", 536.87)
	e.wantNum("cash", cash, "net", 1713.13)
	by := cash["received_by_method"].([]any)
	if len(by) != 2 {
		t.Fatalf("received_by_method = %v", by)
	}
	first, second := by[0].(map[string]any), by[1].(map[string]any)
	if first["payment_method"] != "PIX" || !near(num(first, "amount"), 130) {
		t.Errorf("primeiro método = %v, want PIX 130 (amount desc)", first)
	}
	if v, present := second["payment_method"]; !present || v != nil || !near(num(second, "amount"), 20) {
		t.Errorf("segundo método = %v, want null 20", second)
	}
	cats := map[string]float64{}
	for _, c := range cash["by_category"].([]any) {
		cats[c.(map[string]any)["category"].(string)] = num(c.(map[string]any), "amount")
	}
	if !near(cats["VEHICLE"], -357.87) || !near(cats["PURCHASE_PAYMENT"], -70) || !near(cats["OTHER_INCOME"], 2100) || !near(cats["COMMISSION"], -5) {
		t.Errorf("cash.by_category = %v", cats)
	}

	exp := res["expenses"].(map[string]any)
	e.wantNum("expenses", exp, "total", 457.87)
	ecats := map[string]float64{}
	for _, c := range exp["by_category"].([]any) {
		ecats[c.(map[string]any)["category"].(string)] = num(c.(map[string]any), "amount")
	}
	if len(ecats) != 2 || !near(ecats["VEHICLE"], 357.87) || !near(ecats["LABOR"], 100) {
		t.Errorf("expenses.by_category = %v", ecats)
	}
	result := res["result"].(map[string]any)
	e.wantNum("result", result, "sales_profit", 65)
	e.wantNum("result", result, "expenses", 457.87)
	e.wantNum("result", result, "net_result", -392.87)

	after := e.balances()
	if d := after.receivable - before.receivable; !near(d, 90+999) {
		t.Errorf("Δ receivable = %v, want 1089", d)
	}
	if d := after.payable - before.payable; !near(d, 6+25) {
		t.Errorf("Δ payable = %v, want 31", d)
	}
	bal := res["balances"].(map[string]any)
	if _, ok := bal["payable_by_counterparty_kind"].([]any); !ok {
		t.Errorf("payable_by_counterparty_kind deveria ser array: %v", bal)
	}
}

func TestIntegration_FinanceSummaryPeriodValidation(t *testing.T) {
	e := newFinanceEnv(t)
	for _, q := range []string{"from=2026-13-01", "to=abc", "from=2026-05-02&to=2026-05-01"} {
		if code, res := e.api.do("GET", "/finance/summary?"+q, nil); code != 400 {
			t.Errorf("%s = %d %v, want 400", q, code, res)
		}
	}
	code, res := e.api.do("GET", "/finance/summary", nil)
	if code != 200 {
		t.Fatalf("default = %d", code)
	}
	now := today()
	p := res["period"].(map[string]any)
	if p["from"] != now.Format("2006-01")+"-01" || p["to"] != now.Format("2006-01-02") {
		t.Errorf("period default = %v", p)
	}
	if _, ok := res["cash"].(map[string]any)["received_by_method"].([]any); !ok {
		t.Errorf("received_by_method deveria ser array: %v", res["cash"])
	}
	if _, ok := res["expenses"].(map[string]any)["by_category"].([]any); !ok {
		t.Errorf("expenses.by_category deveria ser array: %v", res["expenses"])
	}
	if code, _ := e.api.do("GET", "/finance/summary?from=2026-05-01&to=2026-05-01", nil); code != 200 {
		t.Errorf("from == to = %d, want 200", code)
	}
}

func TestIntegration_NotificationsGeneration(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)
	acc := e.api.receivable(sale)
	code, d := e.api.plan(acc, [2]any{dayStr(-2), 100.0}, [2]any{dayStr(40), 900.0})
	if code != 200 {
		t.Fatalf("plano = %d %v", code, d)
	}
	instID := d["installments"].([]any)[0].(map[string]any)["id"].(string)

	type notif struct {
		Tipo string
		Lida bool
		Dias *int
		Ref  string
		N    int
	}
	read := func() (n notif) {
		t.Helper()
		e.db.Raw(`SELECT COALESCE(MAX(tipo), ''), COALESCE(BOOL_OR(lida), FALSE), MAX(dias_restantes), COALESCE(MAX(data_ref::text), ''), COUNT(*)
			FROM notificacoes WHERE installment_id = ?`, instID).Row().Scan(&n.Tipo, &n.Lida, &n.Dias, &n.Ref, &n.N)
		return
	}
	list := func() []any {
		t.Helper()
		code, res := e.api.do("GET", "/dashboard/notificacoes", nil)
		if code != 200 {
			t.Fatalf("GET notificacoes = %d %v", code, res)
		}
		return res["notificacoes"].([]any)
	}
	listed := func() (found bool, id string) {
		for _, x := range list() {
			m := x.(map[string]any)
			if m["venda_id"] == sale {
				if m["obligation_id"] != acc || m["installment_id"] != instID {
					t.Errorf("alerta sem obligation_id/installment_id corretos: %v", m)
				}
				return true, m["id"].(string)
			}
		}
		return false, ""
	}

	ok, nid := listed()
	if n := read(); !ok || n.Tipo != "pagamento_vencido" || n.Dias == nil || *n.Dias != -2 || n.Lida {
		t.Fatalf("alerta vencido = %+v listado=%v", n, ok)
	}
	var total int
	e.db.Raw(`SELECT COUNT(*) FROM notificacoes WHERE venda_id = ?`, sale).Scan(&total)
	if total != 1 {
		t.Errorf("alertas da venda = %d, want 1 (a parcela de +40 dias fica de fora)", total)
	}

	// Marcar como lida some da listagem e sobrevive a nova geração (idempotente).
	if code, _ := e.api.do("POST", "/dashboard/notificacoes/"+nid+"/lida", nil); code != 204 {
		t.Fatalf("marcar lida = %d", code)
	}
	if found, _ := listed(); found {
		t.Error("alerta lido não deveria ser listado")
	}
	if n := read(); !n.Lida || n.N != 1 {
		t.Errorf("após reler: %+v", n)
	}
	if code, _ := e.api.do("POST", "/dashboard/gerar-notificacoes", nil); code != 204 {
		t.Errorf("gerar = %d, want 204", code)
	}

	mustExec(t, e.db, `UPDATE notificacoes SET dias_restantes = 99 WHERE installment_id = ?`, instID)
	listed()
	if n := read(); !n.Lida || n.Dias == nil || *n.Dias != -2 {
		t.Errorf("dias_restantes sozinho não reabre: %+v", n)
	}

	mustExec(t, e.db, `UPDATE installments SET due_date = ? WHERE id = ?`, dayStr(3), instID)
	if found, _ := listed(); !found {
		t.Error("alerta reaberto deveria ser listado")
	}
	if n := read(); n.Lida || n.Tipo != "pagamento_vencendo" || n.Ref != dayStr(3) {
		t.Errorf("após reagendar: %+v", n)
	}

	e.api.do("POST", "/dashboard/notificacoes/"+nid+"/lida", nil)
	mustExec(t, e.db, `UPDATE installments SET due_date = ? WHERE id = ?`, dayStr(4), instID)
	listed()
	if n := read(); n.Lida || n.Ref != dayStr(4) {
		t.Errorf("data_ref mudou, deveria reabrir: %+v", n)
	}

	mustExec(t, e.db, `UPDATE installments SET due_date = ? WHERE id = ?`, dayStr(10), instID)
	listed()
	if n := read(); n.N != 0 {
		t.Errorf("fora da janela deveria remover o alerta: %+v", n)
	}

	mustExec(t, e.db, `UPDATE installments SET due_date = ? WHERE id = ?`, dayStr(1), instID)
	listed()
	if n := read(); n.N != 1 {
		t.Fatalf("deveria gerar de novo: %+v", n)
	}
	e.api.receive(sale, 100, dayStr(0), "PIX")
	listed()
	if n := read(); n.N != 0 {
		t.Errorf("parcela coberta remove o alerta: %+v", n)
	}

	sale2 := e.simpleSale(1, 500, 100)
	acc2 := e.api.receivable(sale2)
	if code, _ := e.api.do("PATCH", "/obligations/"+acc2, map[string]any{"due_date": dayStr(0)}); code != 200 {
		t.Fatal("PATCH due_date")
	}
	count := func() (n int) {
		e.db.Raw(`SELECT COUNT(*) FROM notificacoes WHERE venda_id = ?`, sale2).Scan(&n)
		return
	}
	list()
	if count() != 1 {
		t.Fatalf("alerta de prazo da conta: %d, want 1", count())
	}
	e.api.do("PATCH", "/vendas/"+sale2+"/delivery-status", map[string]any{"delivery_status": "CANCELED"})
	list()
	if count() != 0 {
		t.Errorf("Sale cancelada remove os alertas, got %d", count())
	}
}

func TestIntegration_PayableNotifications(t *testing.T) {
	e := newFinanceEnv(t)
	sup := e.supplier()
	sale, items := e.sale(qItem("Pedra", 1, 800, 500, ""))
	if code, res := e.putItem(sale, items[0], map[string]any{"produto_nome": "Pedra", "quantidade": 1, "preco_venda_unit": 800, "custo_unit": 500, "fornecedor_id": sup}); code != 200 {
		t.Fatalf("PUT item = %d %v", code, res)
	}
	_, p := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup, "sale_item_ids": []string{items[0]}})
	purchase := p["id"].(string)
	acc := e.api.payableOf("PURCHASE", purchase, sup)

	kind := func() (tipo string, n int) {
		t.Helper()
		e.api.do("GET", "/dashboard/notificacoes", nil)
		e.db.Raw(`SELECT COALESCE(MAX(tipo), ''), COUNT(*) FROM notificacoes WHERE obligation_id = ?`, acc).Row().Scan(&tipo, &n)
		return
	}
	if _, n := kind(); n != 0 {
		t.Fatal("conta sem prazo não alerta")
	}
	e.api.do("PATCH", "/obligations/"+acc, map[string]any{"due_date": dayStr(3)})
	if tipo, n := kind(); n != 1 || tipo != "conta_pagar_vencendo" {
		t.Errorf("prazo em 3 dias = %s (%d)", tipo, n)
	}
	e.api.do("PATCH", "/obligations/"+acc, map[string]any{"due_date": dayStr(-1)})
	if tipo, n := kind(); n != 1 || tipo != "conta_pagar_vencida" {
		t.Errorf("prazo vencido = %s (%d)", tipo, n)
	}
	e.api.do("POST", "/obligations/"+acc+"/settle", map[string]any{"movement": map[string]any{"occurred_on": dayStr(0), "payment_method": "PIX"}})
	if _, n := kind(); n != 0 {
		t.Errorf("conta quitada remove o alerta (%d)", n)
	}
}
