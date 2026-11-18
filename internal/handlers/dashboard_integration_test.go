package handlers

// Testes de integração do AYD-005 (Dashboard): GET /dashboard/kpis, quick_sale no Quote e
// relatórios corrigidos. Mesmas condições dos outros *_integration_test: só com
// TEST_DATABASE_URL (banco descartável; dados não são limpos — períodos futuros únicos por
// execução isolam os totais e os globais são conferidos por diferença).
import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func newDashboardEnv(t *testing.T) fxEnv {
	t.Helper()
	e := newFinanceEnv(t) // inclui Dashboard e Finance
	return e
}

// uniquePeriod devolve um mês futuro único por execução: from, to e um dia dentro dele.
func uniquePeriod() (from, to, mid string) {
	year := 2100 + rand.Intn(7000)
	return fmt.Sprintf("%d-05-01", year), fmt.Sprintf("%d-05-31", year), fmt.Sprintf("%d-05-10", year)
}

func (e fxEnv) kpis(query string) map[string]any {
	e.t.Helper()
	code, res := e.api.do("GET", "/dashboard/kpis"+query, nil)
	if code != 200 {
		e.t.Fatalf("GET /dashboard/kpis%s = %d %v", query, code, res)
	}
	return res
}

func sub(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	return v
}

// quote cria um Quote normal (não venda rápida) com 1 Item, na data dada.
func (e fxEnv) quote(date string) string {
	e.t.Helper()
	code, q := e.api.do("POST", "/quotes", map[string]any{
		"customer_id": e.cid, "quote_date": date,
		"items": []map[string]any{qItem("Pedra", 1, 100, 60, "")},
	})
	if code != 201 {
		e.t.Fatalf("POST /quotes = %d %v", code, q)
	}
	return q["id"].(string)
}

func TestIntegration_KPIsPeriodSalesQuotesAndBalances(t *testing.T) {
	e := newDashboardEnv(t)
	from, to, mid := uniquePeriod()

	// Sales: A (200, lucro 200-120-10-5 = 65) e D (100, custo 50, lucro 50) no período;
	// B cancelada no período (fora de tudo); C fora do período.
	saleA := e.simpleSale(2, 100, 60)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ?, frete_valor = 10, comissao_valor = 5 WHERE id = ?`, mid, saleA)
	saleD := e.simpleSale(1, 100, 50)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, mid, saleD)
	saleB := e.simpleSale(1, 500, 100)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, mid, saleB)
	if code, _ := e.api.do("PATCH", "/vendas/"+saleB+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatal("cancelar Sale B")
	}
	saleC := e.simpleSale(1, 999, 1)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, from[:4]+"-07-01", saleC)

	// Quotes (coorte, por quote_date): 1 aberto, 1 perdido, 1 ganho (convertido), 1 excluído,
	// 1 fora do período — e uma venda rápida (nasce WON) que NÃO entra na conversão.
	e.quote(mid) // aberto
	lost := e.quote(mid)
	if code, _ := e.api.do("PATCH", "/quotes/"+lost+"/status", map[string]any{"status": "LOST", "lost_reason": "preço"}); code != 200 {
		t.Fatal("LOST")
	}
	won := e.quote(mid)
	code, conv := e.api.do("POST", "/quotes/"+won+"/convert", nil)
	if code != 201 {
		t.Fatalf("convert = %d %v", code, conv)
	}
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, mid, must(t, conv, "sale_id"))
	del := e.quote(mid)
	if code, _ := e.api.do("DELETE", "/quotes/"+del, nil); code != 204 {
		t.Fatal("DELETE quote")
	}
	e.quote(from[:4] + "-08-01") // fora do período
	code, qs := e.api.do("POST", "/quotes/quick-sale", map[string]any{
		"customer_id": e.cid, "quote_date": mid, "items": []map[string]any{qItem("Pedra", 1, 100, 60, "")}})
	if code != 201 {
		t.Fatalf("quick-sale = %d %v", code, qs)
	}
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, mid, must(t, qs, "sale_id"))

	before := e.kpis("")
	res := e.kpis("?from=" + from + "&to=" + to)

	if p := sub(res, "period"); p["from"] != from || p["to"] != to {
		t.Errorf("period = %v", p)
	}
	sales := sub(res, "sales")
	e.wantNum("sales", sales, "count", 4) // A, D, convertida (100/60) e venda rápida (100/60)
	e.wantNum("sales", sales, "revenue", 500)
	e.wantNum("sales", sales, "profit", 65+50+40+40)
	e.wantNum("sales", sales, "average_ticket", 125)

	// Só os Quotes normais: aberto, perdido, ganho = 3 (quick sale, excluído e fora saem).
	quotes := sub(res, "quotes")
	e.wantNum("quotes", quotes, "created", 3)
	e.wantNum("quotes", quotes, "won", 1)
	e.wantNum("quotes", quotes, "lost", 1)
	e.wantNum("quotes", quotes, "open", 1)
	e.wantNum("quotes", quotes, "conversion_rate", 0.3333)

	// O Quote da venda rápida expõe quick_sale = true; os demais false.
	_, qd := e.api.do("GET", "/quotes/"+must(t, qs, "quote_id"), nil)
	if qd["quick_sale"] != true {
		t.Errorf("quote da venda rápida: quick_sale = %v", qd["quick_sale"])
	}
	_, qw := e.api.do("GET", "/quotes/"+won, nil)
	if qw["quick_sale"] != false {
		t.Errorf("quote convertido: quick_sale = %v", qw["quick_sale"])
	}

	// open_deliveries: todas as Sales TO_DELIVER (A, D, C, convertida, venda rápida; B cancelada fora).
	if num(res, "open_deliveries") < 5 {
		t.Errorf("open_deliveries = %v, want >= 5", res["open_deliveries"])
	}
	if got := num(before, "open_deliveries"); got != num(res, "open_deliveries") {
		t.Errorf("open_deliveries não depende do período: %v vs %v", got, res["open_deliveries"])
	}
}

func must(t *testing.T, m map[string]any, k string) string {
	t.Helper()
	s, ok := m[k].(string)
	if !ok {
		t.Fatalf("campo %q ausente em %v", k, m)
	}
	return s
}

func TestIntegration_KPIsMatchFinanceSummary(t *testing.T) {
	e := newDashboardEnv(t)
	from, to, mid := uniquePeriod()
	sale := e.simpleSale(3, 100, 40)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ?, frete_valor = 12, frete_pago = 2, comissao_valor = 7 WHERE id = ?`, mid, sale)
	e.receipt(sale, map[string]any{"valor": 100, "data_pagamento": mid, "payment_method": "PIX"})
	sup := e.supplier()
	pid := e.product()
	var pur string
	e.db.Raw(`INSERT INTO purchases (kind, supplier_id) VALUES ('STOCK_REPLENISHMENT', ?) RETURNING id`, sup).Scan(&pur)
	mustExec(t, e.db, `INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost) VALUES (?, ?, 2, 50)`, pur, pid)

	_, fin := e.api.do("GET", "/finance/summary?from="+from+"&to="+to, nil)
	k := e.kpis("?from=" + from + "&to=" + to)

	fs, ks := sub(fin, "sales"), sub(k, "sales")
	for _, key := range []string{"count", "revenue", "profit"} {
		if num(fs, key) != num(ks, key) {
			t.Errorf("sales.%s: finance=%v kpis=%v", key, fs[key], ks[key])
		}
	}
	fb, kb := sub(fin, "balances"), sub(k, "balances")
	for _, key := range []string{"receivable", "payable_to_suppliers", "commission_payable", "freight_payable"} {
		if _, ok := kb[key]; !ok {
			t.Errorf("balances.%s ausente", key)
		}
		if num(fb, key) != num(kb, key) {
			t.Errorf("balances.%s: finance=%v kpis=%v", key, fb[key], kb[key])
		}
	}
	e.wantNum("sales", ks, "average_ticket", 300)
}

func TestIntegration_KPIsDefaultPeriodAndNullConversion(t *testing.T) {
	e := newDashboardEnv(t)
	now := today()
	wantFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	res := e.kpis("")
	if p := sub(res, "period"); p["from"] != wantFrom || p["to"] != now.Format("2006-01-02") {
		t.Errorf("período default = %v, want %s..%s", p, wantFrom, now.Format("2006-01-02"))
	}

	// Período sem Quotes nem Sales: conversion_rate null, ticket 0, tudo zerado.
	from, to, _ := uniquePeriod()
	res = e.kpis("?from=" + from + "&to=" + to)
	quotes := sub(res, "quotes")
	if v, present := quotes["conversion_rate"]; !present || v != nil {
		t.Errorf("conversion_rate = %v (present=%v), want null", v, present)
	}
	e.wantNum("quotes", quotes, "created", 0)
	sales := sub(res, "sales")
	e.wantNum("sales", sales, "count", 0)
	e.wantNum("sales", sales, "average_ticket", 0)
	e.wantNum("sales", sales, "revenue", 0)
}

func TestIntegration_KPIsInvalidPeriod(t *testing.T) {
	e := newDashboardEnv(t)
	for _, q := range []string{"from=2026-13-01", "to=abc", "from=2026-05-02&to=2026-05-01"} {
		if code, res := e.api.do("GET", "/dashboard/kpis?"+q, nil); code != 400 {
			t.Errorf("kpis?%s = %d %v, want 400", q, code, res)
		}
	}
}

// quick_sale só é true na venda rápida; payloads de criação/edição não conseguem setá-lo.
func TestIntegration_QuoteQuickSaleReadOnly(t *testing.T) {
	e := newDashboardEnv(t)
	code, q := e.api.do("POST", "/quotes", map[string]any{
		"customer_id": e.cid, "quick_sale": true, "items": []map[string]any{qItem("Pedra", 1, 100, 60, "")}})
	if code != 201 || q["quick_sale"] != false {
		t.Fatalf("create com quick_sale no payload = %d %v", code, q["quick_sale"])
	}
	id := q["id"].(string)
	if code, res := e.api.do("PUT", "/quotes/"+id, map[string]any{"quick_sale": true, "notes": "x"}); code != 200 || res["quick_sale"] != false {
		t.Errorf("PUT com quick_sale = %d %v", code, res["quick_sale"])
	}
	_, list := e.api.list("/quotes")
	for _, r := range list {
		if r["id"] == id && r["quick_sale"] != false {
			t.Errorf("listagem: quick_sale = %v", r["quick_sale"])
		}
	}
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (e fxEnv) monthly(n int) []map[string]any {
	e.t.Helper()
	code, res := e.api.do("GET", fmt.Sprintf("/relatorios/vendas?meses=%d", n), nil)
	if code != 200 {
		e.t.Fatalf("GET /relatorios/vendas = %d %v", code, res)
	}
	var out []map[string]any
	for _, d := range res["dados"].([]any) {
		out = append(out, d.(map[string]any))
	}
	return out
}

func TestIntegration_MonthlyReportExactlyNMonthsZeroFilled(t *testing.T) {
	e := newDashboardEnv(t)
	const n = 6
	cur := monthStart(today())

	before := e.monthly(n)
	if len(before) != n {
		t.Fatalf("len = %d, want exactly %d", len(before), n)
	}
	for i, it := range before {
		want := cur.AddDate(0, -(n-1)+i, 0).Format("2006-01-02")
		if it["mes"] != want {
			t.Errorf("dados[%d].mes = %v, want %s (crescente, termina no mês corrente)", i, it["mes"], want)
		}
		for _, k := range []string{"total_venda", "lucro", "sales_count"} {
			if _, ok := it[k]; !ok {
				t.Errorf("dados[%d] sem %s", i, k)
			}
		}
	}

	// Sale (200, lucro 80) 2 meses atrás e Sale cancelada no mesmo mês (não conta).
	two := cur.AddDate(0, -2, 0).Format("2006-01-02")
	s1 := e.simpleSale(2, 100, 60)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, two, s1)
	s2 := e.simpleSale(1, 500, 1)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, two, s2)
	if code, _ := e.api.do("PATCH", "/vendas/"+s2+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatal("cancelar")
	}
	// Sale mais antiga que a janela: não aparece.
	s3 := e.simpleSale(1, 700, 1)
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, cur.AddDate(0, -n, 0).Format("2006-01-02"), s3)

	after := e.monthly(n)
	if len(after) != n {
		t.Fatalf("len = %d, want %d", len(after), n)
	}
	for i := range after {
		dRev := num(after[i], "total_venda") - num(before[i], "total_venda")
		dCnt := num(after[i], "sales_count") - num(before[i], "sales_count")
		if after[i]["mes"] == two {
			if !near(dRev, 200) || dCnt != 1 || !near(num(after[i], "lucro")-num(before[i], "lucro"), 80) {
				t.Errorf("mês %s: Δ receita=%v Δ count=%v", two, dRev, dCnt)
			}
		} else if dRev != 0 || dCnt != 0 {
			t.Errorf("mês %v mudou: Δ receita=%v Δ count=%v", after[i]["mes"], dRev, dCnt)
		}
	}

	// N=1 → só o mês corrente; limites e valores inválidos → 400.
	if got := e.monthly(1); len(got) != 1 || got[0]["mes"] != cur.Format("2006-01-02") {
		t.Errorf("meses=1 = %v", got)
	}
	if got := e.monthly(60); len(got) != 60 {
		t.Errorf("meses=60 len = %d", len(got))
	}
	for _, q := range []string{"0", "61", "abc"} {
		if code, _ := e.api.do("GET", "/relatorios/vendas?meses="+q, nil); code != 400 {
			t.Errorf("meses=%s = %d, want 400", q, code)
		}
	}
	// Default = 12.
	if code, res := e.api.do("GET", "/relatorios/vendas", nil); code != 200 || len(res["dados"].([]any)) != 12 {
		t.Errorf("default meses: %d %v", code, res)
	}
}

func TestIntegration_ProductsReportFromToLegacyAndUnit(t *testing.T) {
	e := newDashboardEnv(t)
	from, to, mid := uniquePeriod()
	outside := from[:4] + "-09-01"
	pid := e.product() // unidade 'm2'

	s1 := e.simpleSaleOf(qItem("Pedra", 2, 100, 60, pid))
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, mid, s1)
	s2 := e.simpleSaleOf(qItem("Pedra", 4, 130, 70, pid))
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, mid, s2)
	s3 := e.simpleSaleOf(qItem("Pedra", 1, 9999, 1, pid)) // fora do período
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, outside, s3)
	legacy := uniq("Legado")
	s4 := e.simpleSaleOf(qItem(legacy, 1, 50, 20, ""))
	mustExec(t, e.db, `UPDATE vendas SET data_venda = ? WHERE id = ?`, mid, s4)
	mustExec(t, e.db, `UPDATE itens_venda SET produto_id = NULL WHERE venda_id = ?`, s4)

	check := func(label, query string) {
		t.Helper()
		code, res := e.api.do("GET", "/relatorios/produtos?"+query, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", label, code, res)
		}
		dados := res["dados"].([]any)
		var prod, leg map[string]any
		for _, d := range dados {
			m := d.(map[string]any)
			if m["produto_id"] == pid {
				prod = m
			}
			if m["produto_nome"] == legacy {
				leg = m
			}
		}
		if prod == nil || leg == nil {
			t.Fatalf("%s: faltam linhas (produto=%v legado=%v) em %v", label, prod, leg, dados)
		}
		e.wantNum(label, prod, "total_vendas", 2)
		e.wantNum(label, prod, "total_quantidade", 6)
		e.wantNum(label, prod, "total_receita", 720)
		e.wantNum(label, prod, "preco_medio", 120)
		e.wantNum(label, prod, "custo_medio", 66.6667)
		if prod["unit"] != "m2" {
			t.Errorf("%s: unit = %v, want m2", label, prod["unit"])
		}
		if v, present := leg["unit"]; !present || v != nil {
			t.Errorf("%s: unit do legado = %v (present=%v), want null", label, v, present)
		}
		if leg["produto_id"] != nil {
			t.Errorf("%s: produto_id do legado = %v", label, leg["produto_id"])
		}
	}
	check("from/to", "from="+from+"&to="+to)
	check("legado", "data_inicio="+from+"&data_fim="+to)
	// from/to vencem os legados quando ambos são enviados (legado aponta para outro mês).
	check("from/to vencem", "from="+from+"&to="+to+"&data_inicio="+outside+"&data_fim="+outside)

	for _, q := range []string{"from=2026-13-01", "to=abc", "from=2026-05-02&to=2026-05-01", "data_inicio=x", "data_inicio=2026-05-02&data_fim=2026-05-01"} {
		if code, res := e.api.do("GET", "/relatorios/produtos?"+q, nil); code != 400 {
			t.Errorf("produtos?%s = %d %v, want 400", q, code, res)
		}
	}
}

// simpleSaleOf: Sale por venda rápida com os Items dados.
func (e fxEnv) simpleSaleOf(items ...map[string]any) string {
	e.t.Helper()
	id, _ := e.sale(items...)
	return id
}
