package handlers

// Testes de integração do AYD-004 (Financeiro): payment method e soft-delete do Receipt,
// receivable/freight_payable e filtros da Sale, GET /finance/summary e geração de alertas.
// Mesmas condições dos outros *_integration_test: só rodam com TEST_DATABASE_URL (banco
// descartável com as migrations aplicadas; dados não são limpos — saldos globais são
// conferidos por diferença e o painel usa um período único por execução).
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

// simpleSale: Sale com 1 Item (qty × price, custo cost) — receita qty*price.
func (e fxEnv) simpleSale(qty, price, cost float64) string {
	e.t.Helper()
	id, _ := e.sale(qItem("Pedra", qty, price, cost, ""))
	return id
}

func (e fxEnv) receipt(saleID string, body map[string]any) (int, map[string]any) {
	e.t.Helper()
	return e.api.do("POST", "/vendas/"+saleID+"/pagamentos", body)
}

func dayStr(offset int) string { return todayDate().AddDate(0, 0, offset).Format("2006-01-02") }

// RF-11/AYD-004: payment_method obrigatório ao receber; opcional em pendente.
func TestIntegration_ReceiptPaymentMethodRules(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)

	// POST com data_pagamento sem método → 400; método inválido → 400.
	if code, res := e.receipt(sale, map[string]any{"valor": 100, "data_pagamento": "2026-11-01"}); code != 400 {
		t.Fatalf("recebido sem método = %d %v", code, res)
	}
	if code, _ := e.receipt(sale, map[string]any{"valor": 100, "data_pagamento": "2026-11-01", "payment_method": "CHEQUE"}); code != 400 {
		t.Errorf("método inválido deveria ser 400, got %d", code)
	}

	// POST recebido com método → 201 e devolve o método.
	code, rec := e.receipt(sale, map[string]any{"valor": 100, "data_pagamento": "2026-11-01", "payment_method": "PIX"})
	if code != 201 || rec["status"] != "recebido" || rec["payment_method"] != "PIX" {
		t.Fatalf("recebido com método = %d %v", code, rec)
	}

	// POST pendente sem método → 201 com payment_method null; sem datas também nasce pendente.
	code, pend := e.receipt(sale, map[string]any{"valor": 200, "data_agendada": dayStr(30)})
	if code != 201 || pend["status"] != "pendente" || pend["payment_method"] != nil {
		t.Fatalf("pendente = %d %v", code, pend)
	}
	if code, nodate := e.receipt(sale, map[string]any{"valor": 50}); code != 201 || nodate["status"] != "pendente" {
		t.Errorf("sem datas deveria nascer pendente: %d %v", code, nodate)
	}
	pendID := pend["id"].(string)
	url := "/vendas/" + sale + "/pagamentos/" + pendID

	// confirmar sem método → 400; método inválido → 400; com método → 200.
	if code, _ := e.api.do("POST", url+"/confirmar", map[string]any{"data_pagamento": "2026-11-05"}); code != 400 {
		t.Errorf("confirmar sem método = %d, want 400", code)
	}
	if code, _ := e.api.do("POST", url+"/confirmar", map[string]any{"payment_method": "x"}); code != 400 {
		t.Errorf("confirmar com método inválido = %d, want 400", code)
	}
	code, conf := e.api.do("POST", url+"/confirmar", map[string]any{"data_pagamento": "2026-11-05", "payment_method": "BOLETO"})
	if code != 200 || conf["status"] != "recebido" || conf["payment_method"] != "BOLETO" || conf["data_pagamento"] != "2026-11-05" {
		t.Fatalf("confirmar = %d %v", code, conf)
	}

	// Pendente com método já combinado: confirmar sem body mantém o método.
	_, agreed := e.receipt(sale, map[string]any{"valor": 70, "data_agendada": dayStr(10), "payment_method": "TRANSFER"})
	code, conf = e.api.do("POST", "/vendas/"+sale+"/pagamentos/"+agreed["id"].(string)+"/confirmar", nil)
	if code != 200 || conf["payment_method"] != "TRANSFER" || conf["data_pagamento"] != dayStr(0) {
		t.Errorf("confirmar mantendo método = %d %v", code, conf)
	}

	// PUT (substituição completa): resultado "recebido" exige método; "pendente" não.
	putURL := "/vendas/" + sale + "/pagamentos/" + rec["id"].(string)
	if code, _ := e.api.do("PUT", putURL, map[string]any{"valor": 100, "data_pagamento": "2026-11-01", "status": "recebido"}); code != 400 {
		t.Errorf("PUT recebido sem método = %d, want 400", code)
	}
	if code, res := e.api.do("PUT", putURL, map[string]any{"valor": 110, "data_pagamento": "2026-11-01", "status": "recebido", "payment_method": "CARD"}); code != 200 || res["payment_method"] != "CARD" || num(res, "valor") != 110 {
		t.Errorf("PUT recebido com método = %d %v", code, res)
	}
	if code, res := e.api.do("PUT", putURL, map[string]any{"valor": 110, "data_agendada": dayStr(3), "status": "pendente"}); code != 200 || res["status"] != "pendente" {
		t.Errorf("PUT pendente sem método = %d %v", code, res)
	}

	// Detalhe da Sale traz o método nos Receipts embutidos.
	_, detail := e.api.do("GET", "/vendas/"+sale, nil)
	found := false
	for _, p := range detail["pagamentos"].([]any) {
		if p.(map[string]any)["payment_method"] == "BOLETO" {
			found = true
		}
	}
	if !found {
		t.Errorf("detalhe sem payment_method: %v", detail["pagamentos"])
	}
}

// RN-08: DELETE é soft-delete; some de lista, detalhe, total_recebido e payment_status.
func TestIntegration_ReceiptSoftDelete(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)
	_, r1 := e.receipt(sale, map[string]any{"valor": 400, "data_pagamento": "2026-11-01", "payment_method": "PIX"})
	_, r2 := e.receipt(sale, map[string]any{"valor": 600, "data_pagamento": "2026-11-02", "payment_method": "CASH"})

	_, s := e.api.do("GET", "/vendas/"+sale, nil)
	if s["payment_status"] != "PAID" || num(s, "total_recebido") != 1000 || num(s, "receivable") != 0 {
		t.Fatalf("antes = %v", s)
	}

	id2 := r2["id"].(string)
	code, _ := e.api.do("DELETE", "/vendas/"+sale+"/pagamentos/"+id2, nil)
	if code != 204 {
		t.Fatalf("DELETE = %d, want 204", code)
	}

	_, s = e.api.do("GET", "/vendas/"+sale, nil)
	if s["payment_status"] != "PARTIAL" || num(s, "total_recebido") != 400 || num(s, "receivable") != 600 {
		t.Errorf("depois = %v", s)
	}
	if n := len(s["pagamentos"].([]any)); n != 1 {
		t.Errorf("detalhe deveria ter 1 Receipt, got %d", n)
	}
	if _, list := e.api.list("/vendas/" + sale + "/pagamentos"); len(list) != 1 || list[0]["id"] != r1["id"] {
		t.Errorf("lista = %v", list)
	}
	// A linha continua no banco (soft-delete), com deleted_at.
	var soft bool
	e.db.Raw("SELECT deleted_at IS NOT NULL FROM pagamentos_cliente WHERE id = ?", id2).Scan(&soft)
	if !soft {
		t.Error("Receipt deveria estar soft-deletado (linha preservada)")
	}
	// Excluído conta como inexistente.
	if code, _ := e.api.do("DELETE", "/vendas/"+sale+"/pagamentos/"+id2, nil); code != 404 {
		t.Errorf("DELETE repetido = %d, want 404", code)
	}
	if code, _ := e.api.do("PUT", "/vendas/"+sale+"/pagamentos/"+id2, map[string]any{"valor": 1, "status": "pendente"}); code != 404 {
		t.Errorf("PUT em excluído = %d, want 404", code)
	}
	if code, _ := e.api.do("POST", "/vendas/"+sale+"/pagamentos/"+id2+"/confirmar", nil); code != 404 {
		t.Errorf("confirmar excluído = %d, want 404", code)
	}
}

// AYD-004: Sale CANCELED bloqueia escrita de Receipts (409), mas a leitura segue.
func TestIntegration_ReceiptsBlockedOnCanceledSale(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)
	_, rec := e.receipt(sale, map[string]any{"valor": 100, "data_pagamento": "2026-11-01", "payment_method": "PIX"})
	_, pend := e.receipt(sale, map[string]any{"valor": 50, "data_agendada": dayStr(5)})
	if code, res := e.api.do("PATCH", "/vendas/"+sale+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatalf("cancelar = %d %v", code, res)
	}

	base := "/vendas/" + sale + "/pagamentos"
	for name, call := range map[string]func() (int, map[string]any){
		"POST": func() (int, map[string]any) {
			return e.api.do("POST", base, map[string]any{"valor": 10, "data_agendada": dayStr(1)})
		},
		"PUT": func() (int, map[string]any) {
			return e.api.do("PUT", base+"/"+rec["id"].(string), map[string]any{"valor": 1, "status": "pendente"})
		},
		"confirmar": func() (int, map[string]any) {
			return e.api.do("POST", base+"/"+pend["id"].(string)+"/confirmar", map[string]any{"payment_method": "PIX"})
		},
		"DELETE": func() (int, map[string]any) { return e.api.do("DELETE", base+"/"+rec["id"].(string), nil) },
	} {
		code, res := call()
		if code != http.StatusConflict || res["error"] != "venda cancelada: recebimentos bloqueados" {
			t.Errorf("%s = %d %v, want 409 venda cancelada", name, code, res)
		}
	}
	// Existentes ficam como estão; leitura permitida.
	if code, list := e.api.list(base); code != 200 || len(list) != 2 {
		t.Errorf("lista = %d %v", code, list)
	}
}

// RN-05 / RF-10: receivable e freight_payable (nunca negativos); filtros novos de /vendas;
// validação de frete no PUT.
func TestIntegration_SaleBalancesAndFilters(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(2, 100, 60) // 200
	_, d := e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("novo", d, "receivable", 200)
	e.wantNum("novo", d, "freight_payable", 0)

	// PUT: frete 50 (pago 20), comissão 10 pendente.
	put := func(freight, paid, commission float64, commPaid bool) (int, map[string]any) {
		return e.api.do("PUT", "/vendas/"+sale, map[string]any{
			"frete_valor": freight, "frete_pago": paid, "comissao_valor": commission, "comissao_paga": commPaid})
	}
	code, d := put(50, 20, 10, false)
	if code != 200 {
		t.Fatalf("PUT = %d %v", code, d)
	}
	e.wantNum("put", d, "freight_payable", 30)
	e.wantNum("put", d, "a_pagar_frete", 30)

	// Validação nova do PUT.
	for _, c := range [][3]float64{{-1, 0, 0}, {10, -1, 0}, {10, 11, 0}} {
		if code, res := put(c[0], c[1], c[2], false); code != 400 {
			t.Errorf("PUT frete %v = %d %v, want 400", c, code, res)
		}
	}

	// Recebido parcial e a maior: receivable = max(total − recebido, 0).
	e.receipt(sale, map[string]any{"valor": 80, "data_pagamento": "2026-11-01", "payment_method": "PIX"})
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("parcial", d, "receivable", 120)
	e.receipt(sale, map[string]any{"valor": 500, "data_pagamento": "2026-11-02", "payment_method": "PIX"})
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("a maior", d, "receivable", 0)
	_, list := e.api.list("/vendas?cliente_id=" + e.cid + "&freight_pending=true")
	if !containsID(list, sale) || list[0]["freight_payable"] == nil || list[0]["receivable"] == nil {
		t.Errorf("lista deveria expor receivable/freight_payable: %v", list)
	}

	// freight_pending / commission_pending.
	has := func(q string) bool {
		t.Helper()
		code, l := e.api.list("/vendas?" + q)
		if code != 200 {
			t.Fatalf("GET /vendas?%s = %d", q, code)
		}
		return containsID(l, sale)
	}
	if !has("freight_pending=true") || !has("commission_pending=true") {
		t.Error("Sale com frete e comissão pendentes deveria aparecer nos filtros")
	}
	put(50, 50, 10, true) // frete quitado, comissão paga
	if has("freight_pending=true") || has("commission_pending=true") {
		t.Error("Sale quitada não deveria aparecer em freight_pending/commission_pending")
	}
	put(50, 50, 0, false) // comissão 0 não é pendente
	if has("commission_pending=true") {
		t.Error("comissão 0 não é pendente")
	}

	// from/to inclusivos por data_venda; data inválida → 400.
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

type finBalances struct{ receivable, payable, freight, commission float64 }

func (e fxEnv) balances() finBalances {
	e.t.Helper()
	code, res := e.api.do("GET", "/finance/summary", nil)
	if code != 200 {
		e.t.Fatalf("GET /finance/summary = %d %v", code, res)
	}
	b := res["balances"].(map[string]any)
	return finBalances{num(b, "receivable"), num(b, "payable_to_suppliers"), num(b, "freight_payable"), num(b, "commission_payable")}
}

// GET /finance/summary: números numa fixture pequena — inclui Sale cancelada e Receipt
// excluído (devem sair de tudo) e Receipt legado sem método.
func TestIntegration_FinanceSummary(t *testing.T) {
	e := newFinanceEnv(t)

	// Período único por execução (dados não são limpos entre execuções).
	year := 2100 + rand.Intn(7000)
	from, to := fmt.Sprintf("%d-05-01", year), fmt.Sprintf("%d-05-31", year)
	inP := fmt.Sprintf("%d-05-10", year)
	outP := fmt.Sprintf("%d-07-01", year)

	before := e.balances()

	// Sale A (ativa): 2×100 (custo 60), frete 10 (pago 4), comissão 5 pendente.
	saleA := e.simpleSale(2, 100, 60)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ?, frete_valor = 10, frete_pago = 4, comissao_valor = 5 WHERE id = ?`, inP, saleA)
	e.receipt(saleA, map[string]any{"valor": 80, "data_pagamento": inP, "payment_method": "PIX"})
	mustExec(t, e.db, `INSERT INTO pagamentos_cliente (venda_id, valor, status, data_pagamento) VALUES (?, 20, 'recebido', ?)`, saleA, inP) // legado: sem método
	_, del := e.receipt(saleA, map[string]any{"valor": 30, "data_pagamento": inP, "payment_method": "CARD"})
	if code, _ := e.api.do("DELETE", "/vendas/"+saleA+"/pagamentos/"+del["id"].(string), nil); code != 204 {
		t.Fatal("DELETE do Receipt")
	}
	e.receipt(saleA, map[string]any{"valor": 10, "data_pagamento": outP, "payment_method": "PIX"}) // fora do período (conta em sales.received)

	// Sale B (cancelada) no período: nada dela entra.
	saleB := e.simpleSale(1, 500, 100)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ?, frete_valor = 7, comissao_valor = 3 WHERE id = ?`, inP, saleB)
	mustExec(t, e.db, `INSERT INTO pagamentos_cliente (venda_id, valor, status, data_pagamento, payment_method) VALUES (?, 50, 'recebido', ?, 'PIX')`, saleB, inP)
	if code, _ := e.api.do("PATCH", "/vendas/"+saleB+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatal("cancelar Sale B")
	}

	// Sale C fora do período: não entra em sales.
	saleC := e.simpleSale(1, 999, 1)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, outP, saleC)

	// Purchases: uma paga no período (70) e uma de reposição pendente (2×50 = 100 a pagar).
	sup := e.supplier()
	mustExec(t, e.db, `INSERT INTO purchases (kind, supplier_id, status, payment_status, paid_amount, paid_at) VALUES ('STOCK_REPLENISHMENT', ?, 'RECEIVED', 'PAID', 70, ?)`, sup, inP)
	mustExec(t, e.db, `INSERT INTO purchases (kind, supplier_id, status, payment_status, paid_amount, paid_at) VALUES ('STOCK_REPLENISHMENT', ?, 'RECEIVED', 'PAID', 5, ?)`, sup, outP)                     // fora do período
	mustExec(t, e.db, `INSERT INTO purchases (kind, supplier_id, status, payment_status, deleted_at, paid_amount, paid_at) VALUES ('STOCK_REPLENISHMENT', ?, 'RECEIVED', 'PAID', NOW(), 900, ?)`, sup, inP) // excluída
	pid := e.product()
	var pendPurchase string
	e.db.Raw(`INSERT INTO purchases (kind, supplier_id) VALUES ('STOCK_REPLENISHMENT', ?) RETURNING id`, sup).Scan(&pendPurchase)
	mustExec(t, e.db, `INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost) VALUES (?, ?, 2, 50)`, pendPurchase, pid)

	// Act
	code, res := e.api.do("GET", "/finance/summary?from="+from+"&to="+to, nil)
	if code != 200 {
		t.Fatalf("summary = %d %v", code, res)
	}

	// Assert
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
	e.wantNum("sales", sales, "received", 110) // 80 + 20 + 10 (qualquer data; o excluído não conta)
	e.wantNum("sales", sales, "receivable", 90)

	cash := res["cash"].(map[string]any)
	e.wantNum("cash", cash, "received", 100)
	e.wantNum("cash", cash, "paid_to_suppliers", 70)
	by := cash["received_by_method"].([]any)
	if len(by) != 2 {
		t.Fatalf("received_by_method = %v", by)
	}
	first, second := by[0].(map[string]any), by[1].(map[string]any)
	if first["payment_method"] != "PIX" || !near(num(first, "amount"), 80) {
		t.Errorf("primeiro método = %v, want PIX 80 (amount desc)", first)
	}
	if v, present := second["payment_method"]; !present || v != nil || !near(num(second, "amount"), 20) {
		t.Errorf("segundo método = %v, want null 20", second)
	}

	// balances ignoram o período: conferidos por diferença (A entra; B cancelada e C... C conta).
	after := e.balances()
	if d := after.receivable - before.receivable; !near(d, 90+999) { // A: 90; C: 999 (sem Receipts)
		t.Errorf("Δ receivable = %v, want 1089", d)
	}
	if d := after.freight - before.freight; !near(d, 6) {
		t.Errorf("Δ freight_payable = %v, want 6", d)
	}
	if d := after.commission - before.commission; !near(d, 5) {
		t.Errorf("Δ commission_payable = %v, want 5", d)
	}
	if d := after.payable - before.payable; !near(d, 100) {
		t.Errorf("Δ payable_to_suppliers = %v, want 100", d)
	}
}

func TestIntegration_FinanceSummaryPeriodValidation(t *testing.T) {
	e := newFinanceEnv(t)
	for _, q := range []string{"from=2026-13-01", "to=abc", "from=2026-05-02&to=2026-05-01"} {
		if code, res := e.api.do("GET", "/finance/summary?"+q, nil); code != 400 {
			t.Errorf("%s = %d %v, want 400", q, code, res)
		}
	}
	// Default: 1º dia do mês corrente até hoje; arrays e objetos sempre presentes.
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
	// from == to é válido.
	if code, _ := e.api.do("GET", "/finance/summary?from=2026-05-01&to=2026-05-01", nil); code != 200 {
		t.Errorf("from == to = %d, want 200", code)
	}
}

// RF-19: geração de alertas na leitura, preservando `lida`.
func TestIntegration_NotificationsGeneration(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)
	_, rec := e.receipt(sale, map[string]any{"valor": 100, "data_agendada": dayStr(-2)})
	recID := rec["id"].(string)

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
			FROM notificacoes WHERE pagamento_id = ?`, recID).Row().Scan(&n.Tipo, &n.Lida, &n.Dias, &n.Ref, &n.N)
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
				return true, m["id"].(string)
			}
		}
		return false, ""
	}

	// GET gera: vencido (agendado há 2 dias), com dias_restantes negativo.
	ok, nid := listed()
	if n := read(); !ok || n.Tipo != "pagamento_vencido" || n.Dias == nil || *n.Dias != -2 || n.Lida {
		t.Fatalf("alerta vencido = %+v listado=%v", n, ok)
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

	// Só dias_restantes mudou (simula a passagem do dia): lida preservada, dias atualizado.
	mustExec(t, e.db, `UPDATE notificacoes SET dias_restantes = 99 WHERE pagamento_id = ?`, recID)
	listed()
	if n := read(); !n.Lida || n.Dias == nil || *n.Dias != -2 {
		t.Errorf("dias_restantes sozinho não reabre: %+v", n)
	}

	// Tipo muda (reagendado para daqui a 3 dias → vencendo): reabre.
	mustExec(t, e.db, `UPDATE pagamentos_cliente SET data_agendada = ? WHERE id = ?`, dayStr(3), recID)
	if found, _ := listed(); !found {
		t.Error("alerta reaberto deveria ser listado")
	}
	if n := read(); n.Lida || n.Tipo != "pagamento_vencendo" || n.Ref != dayStr(3) {
		t.Errorf("após reagendar: %+v", n)
	}

	// data_ref muda com o mesmo tipo (3 → 4 dias): reabre também.
	e.api.do("POST", "/dashboard/notificacoes/"+nid+"/lida", nil)
	mustExec(t, e.db, `UPDATE pagamentos_cliente SET data_agendada = ? WHERE id = ?`, dayStr(4), recID)
	listed()
	if n := read(); n.Lida || n.Ref != dayStr(4) {
		t.Errorf("data_ref mudou, deveria reabrir: %+v", n)
	}

	// Fora da janela (+10 dias) → alerta removido.
	mustExec(t, e.db, `UPDATE pagamentos_cliente SET data_agendada = ? WHERE id = ?`, dayStr(10), recID)
	listed()
	if n := read(); n.N != 0 {
		t.Errorf("fora da janela deveria remover o alerta: %+v", n)
	}

	// Receipt volta à janela e depois é confirmado → alerta some.
	mustExec(t, e.db, `UPDATE pagamentos_cliente SET data_agendada = ? WHERE id = ?`, dayStr(1), recID)
	listed()
	if n := read(); n.N != 1 {
		t.Fatalf("deveria gerar de novo: %+v", n)
	}
	e.api.do("POST", "/vendas/"+sale+"/pagamentos/"+recID+"/confirmar", map[string]any{"payment_method": "PIX"})
	listed()
	if n := read(); n.N != 0 {
		t.Errorf("recebido remove o alerta: %+v", n)
	}

	// Excluído e Sale cancelada também removem.
	_, rec2 := e.receipt(sale, map[string]any{"valor": 10, "data_agendada": dayStr(0)})
	e.receipt(sale, map[string]any{"valor": 10, "data_agendada": dayStr(0)})
	listed()
	count := func() (n int) {
		e.db.Raw(`SELECT COUNT(*) FROM notificacoes WHERE venda_id = ?`, sale).Scan(&n)
		return
	}
	if count() != 2 {
		t.Fatalf("esperava 2 alertas, got %d", count())
	}
	e.api.do("DELETE", "/vendas/"+sale+"/pagamentos/"+rec2["id"].(string), nil)
	listed()
	if count() != 1 {
		t.Errorf("Receipt excluído remove o alerta, got %d", count())
	}
	e.api.do("PATCH", "/vendas/"+sale+"/delivery-status", map[string]any{"delivery_status": "CANCELED"})
	listed()
	if count() != 0 {
		t.Errorf("Sale cancelada remove os alertas, got %d", count())
	}
}
