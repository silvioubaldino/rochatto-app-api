package handlers

// C5 (AYD-011): fechamento da venda na conversão e na venda rápida.

import (
	"testing"
)

func (e fxEnv) serviceItem(name string, qty, price, cost float64) map[string]any {
	return map[string]any{"product_name": name, "quantity": qty, "unit_price": price, "unit_cost": cost, "kind": "SERVICE"}
}

// quoteOf cria um Quote OPEN e devolve (id, ids dos Items na ordem).
func (e fxEnv) quoteOf(items ...map[string]any) (string, []string) {
	e.t.Helper()
	q := e.must("POST", "/quotes", map[string]any{"customer_id": e.cid, "items": items}, 201)
	ids := []string{}
	for _, it := range q["items"].([]any) {
		ids = append(ids, it.(map[string]any)["id"].(string))
	}
	return q["id"].(string), ids
}

func (e fxEnv) count(query string, args ...any) int64 {
	e.t.Helper()
	var n int64
	if err := e.db.Raw(query, args...).Scan(&n).Error; err != nil {
		e.t.Fatal(err)
	}
	return n
}

func itemByName(t *testing.T, sale map[string]any, name string) map[string]any {
	t.Helper()
	for _, it := range sale["itens"].([]any) {
		if m := it.(map[string]any); m["produto_nome"] == name {
			return m
		}
	}
	t.Fatalf("Item %q não encontrado em %v", name, sale["itens"])
	return nil
}

func TestJourney_ClosingOnConvert(t *testing.T) {
	e := newJourneyEnv(t)
	prod, stockProd := e.product(), e.product()
	sup, installer, partner := e.supplier(), e.supplier(), e.namedPartner("Arq. Closing")
	e.must("POST", "/stock/adjustments", map[string]any{"product_id": stockProd, "quantity": 5, "unit_cost": 40, "reason": "INITIAL", "adjusted_at": "2026-01-01"}, 201)

	// Itens: encomenda (1000), estoque 8 > saldo 5 (400), serviço (300) = 1700.
	qid, ids := e.quoteOf(
		qItem("Encomenda", 10, 100, 60, prod),
		qItem("Do estoque", 8, 50, 10, stockProd),
		e.serviceItem("Instalação", 1, 300, 120),
	)
	conv := e.must("POST", "/quotes/"+qid+"/convert", map[string]any{"closing": map[string]any{
		"delivery_due_date": "2026-12-20",
		"items": []map[string]any{
			{"quote_item_id": ids[0], "origin": "MADE_TO_ORDER", "supplier_id": sup},
			{"quote_item_id": ids[1], "origin": "FROM_STOCK"},
			{"quote_item_id": ids[2], "labor_payee_id": installer},
		},
		"costs": []map[string]any{
			{"kind": "FREIGHT", "amount": 80, "payee_id": installer, "description": "frete"},
			{"kind": "COMMISSION", "amount": 50, "payee_id": partner},
		},
		"receivable_plan": map[string]any{"installments": []map[string]any{
			{"due_date": "2026-12-10", "amount": 600, "payment_method": "PIX"},
			{"due_date": "2027-01-10", "amount": 800},
		}},
		"down_payment": map[string]any{"amount": 300, "occurred_on": "2026-11-22", "payment_method": "PIX"},
	}}, 201)
	saleID := conv["sale_id"].(string)
	if num(conv, "sale_number") != 1 || conv["quote_id"] != qid {
		t.Errorf("resposta = %v", conv)
	}
	ws := conv["warnings"].([]any)
	if len(ws) != 1 || ws[0].(map[string]any)["code"] != "INSUFFICIENT_STOCK" || ws[0].(map[string]any)["quote_item_id"] != ids[1] {
		t.Errorf("warnings = %v", ws)
	}

	sale := e.must("GET", "/vendas/"+saleID, nil, 200)
	if sale["data_entrega_prevista"] != "2026-12-20" || sale["delivery_status"] != "TO_DELIVER" {
		t.Errorf("entrega = %v %v", sale["data_entrega_prevista"], sale["delivery_status"])
	}
	enc, est, svc := itemByName(t, sale, "Encomenda"), itemByName(t, sale, "Do estoque"), itemByName(t, sale, "Instalação")
	if enc["origin"] != "MADE_TO_ORDER" || enc["fornecedor_id"] != sup || est["origin"] != "FROM_STOCK" || svc["origin"] != nil {
		t.Errorf("itens = %v / %v / %v", enc, est, svc)
	}
	if num(est, "custo_unit") != 40 || num(est, "stock_balance") != -3 {
		t.Errorf("FROM_STOCK: custo=%v saldo=%v, want 40 / -3", est["custo_unit"], est["stock_balance"])
	}
	// Custos: LABOR do serviço com o credor do closing + os 2 do closing.
	costs := sale["composition"].(map[string]any)["costs"].([]any)
	if len(costs) != 3 {
		t.Fatalf("custos = %v", costs)
	}
	payees := map[string]string{}
	for _, c := range costs {
		c := c.(map[string]any)
		if p, ok := c["payee"].(map[string]any); ok {
			payees[c["kind"].(string)] = p["type"].(string)
		}
	}
	if payees["LABOR"] != "SUPPLIER" || payees["FREIGHT"] != "SUPPLIER" || payees["COMMISSION"] != "PARTNER" {
		t.Errorf("credores = %v", payees)
	}
	// Conta a receber: plano e entrada (SALE_RECEIPT) cobrindo a 1ª parcela.
	recv := e.api.obligation(e.api.receivable(saleID))
	e.wantNum("receber", recv, "amount", 1700)
	e.wantNum("receber", recv, "settled", 300)
	if insts := recv["installments"].([]any); len(insts) != 2 || num(insts[0].(map[string]any), "covered") != 300 {
		t.Errorf("parcelas = %v", recv["installments"])
	}
	var category string
	e.db.Raw(`SELECT m.category::text FROM financial_movements m JOIN settlements s ON s.movement_id = m.id
		WHERE s.obligation_id = ?`, e.api.receivable(saleID)).Scan(&category)
	if category != "SALE_RECEIPT" {
		t.Errorf("categoria da entrada = %q", category)
	}
	quote := e.must("GET", "/quotes/"+qid, nil, 200)
	if quote["status"] != "WON" || quote["sale_id"] != saleID {
		t.Errorf("quote = %v", quote["status"])
	}
}

func TestJourney_ClosingOnQuickSaleByIndex(t *testing.T) {
	e := newJourneyEnv(t)
	prod, sup := e.product(), e.supplier()
	res := e.must("POST", "/quotes/quick-sale", map[string]any{
		"customer_id": e.cid,
		"items":       []map[string]any{qItem("A", 1, 100, 50, prod), qItem("B", 2, 50, 10, "")},
		"closing": map[string]any{
			"items":        []map[string]any{{"item_index": 1, "supplier_id": sup}},
			"down_payment": map[string]any{"amount": 200, "occurred_on": "2026-11-22", "payment_method": "CASH"},
		},
	}, 201)
	sale := e.must("GET", "/vendas/"+res["sale_id"].(string), nil, 200)
	if itemByName(t, sale, "B")["fornecedor_id"] != sup || itemByName(t, sale, "A")["fornecedor_id"] != nil {
		t.Errorf("fornecedores = %v", sale["itens"])
	}
	if sale["payment_status"] != "PAID" || len(res["warnings"].([]any)) != 0 {
		t.Errorf("payment_status=%v warnings=%v", sale["payment_status"], res["warnings"])
	}
	// FROM_STOCK sem saldo por índice: aviso com item_index.
	res = e.must("POST", "/quotes/quick-sale", map[string]any{
		"customer_id": e.cid,
		"items":       []map[string]any{qItem("Fora", 1, 10, 1, ""), qItem("Estoque", 1, 10, 1, prod)},
		"closing":     map[string]any{"items": []map[string]any{{"item_index": 1, "origin": "FROM_STOCK"}}},
	}, 201)
	ws := res["warnings"].([]any)
	if len(ws) != 1 || num(ws[0].(map[string]any), "item_index") != 1 || ws[0].(map[string]any)["quote_item_id"] != nil {
		t.Errorf("warnings = %v", ws)
	}
}

// Sem closing (ou {}) a conversão é a de sempre; Items sob encomenda sem Supplier.
func TestJourney_ClosingAbsentIsPlainConversion(t *testing.T) {
	e := newJourneyEnv(t)
	for _, body := range []any{nil, map[string]any{}, map[string]any{"closing": map[string]any{}}} {
		qid, _ := e.quoteOf(qItem("X", 1, 100, 10, ""))
		conv := e.must("POST", "/quotes/"+qid+"/convert", body, 201)
		sale := e.must("GET", "/vendas/"+conv["sale_id"].(string), nil, 200)
		it := itemByName(t, sale, "X")
		if it["origin"] != "MADE_TO_ORDER" || it["fornecedor_id"] != nil || sale["data_entrega_prevista"] != nil || len(conv["warnings"].([]any)) != 0 {
			t.Errorf("body %v: %v", body, sale)
		}
		if e.count(`SELECT COUNT(*) FROM installments i JOIN obligations o ON o.id = i.obligation_id WHERE o.sale_id = ?`, conv["sale_id"]) != 0 {
			t.Errorf("sem closing não cria parcelas")
		}
	}
}

// Qualquer erro: nada é gravado e o campo é apontado.
func TestJourney_ClosingErrorsAreAtomic(t *testing.T) {
	e := newJourneyEnv(t)
	prod, sup := e.product(), e.supplier()
	ghost := "00000000-0000-0000-0000-000000000001"
	vendasBefore := e.count(`SELECT COUNT(*) FROM vendas`)
	movBefore := e.count(`SELECT COUNT(*) FROM financial_movements`)

	cases := []struct {
		name    string
		status  int
		field   string
		closing func(ids []string) map[string]any
	}{
		{"fornecedor inexistente", 400, "closing.items[1].supplier_id", func(ids []string) map[string]any {
			return map[string]any{"items": []map[string]any{{"quote_item_id": ids[0], "supplier_id": sup}, {"quote_item_id": ids[1], "supplier_id": ghost}}}
		}},
		{"origem em serviço", 400, "closing.items[0].origin", func(ids []string) map[string]any {
			return map[string]any{"items": []map[string]any{{"quote_item_id": ids[2], "origin": "MADE_TO_ORDER"}}}
		}},
		{"labor em produto", 400, "closing.items[0].labor_payee_id", func(ids []string) map[string]any {
			return map[string]any{"items": []map[string]any{{"quote_item_id": ids[0], "labor_payee_id": sup}}}
		}},
		{"FROM_STOCK sem product", 400, "closing.items[0].origin", func(ids []string) map[string]any {
			return map[string]any{"items": []map[string]any{{"quote_item_id": ids[1], "origin": "FROM_STOCK"}}}
		}},
		{"item de outro quote", 400, "closing.items[0].quote_item_id", func(ids []string) map[string]any {
			return map[string]any{"items": []map[string]any{{"quote_item_id": ghost}}}
		}},
		{"índice na conversão", 400, "closing.items[0].quote_item_id", func(ids []string) map[string]any {
			return map[string]any{"items": []map[string]any{{"item_index": 0}}}
		}},
		{"comissão com Supplier", 400, "closing.costs[1].payee_id", func(ids []string) map[string]any {
			return map[string]any{"costs": []map[string]any{{"kind": "LABOR", "amount": 10}, {"kind": "COMMISSION", "amount": 10, "payee_id": sup}}}
		}},
		{"frete com Partner", 400, "closing.costs[0].payee_id", func(ids []string) map[string]any {
			return map[string]any{"costs": []map[string]any{{"kind": "FREIGHT", "amount": 10, "payee_id": e.namedPartner("P")}}}
		}},
		{"entrada sem método", 400, "closing.down_payment.payment_method", func(ids []string) map[string]any {
			return map[string]any{"down_payment": map[string]any{"amount": 10, "occurred_on": "2026-11-22"}}
		}},
		{"parcela zero", 400, "closing.receivable_plan.installments[0].amount", func(ids []string) map[string]any {
			return map[string]any{"receivable_plan": map[string]any{"installments": []map[string]any{{"due_date": "2026-12-10", "amount": 0}}}}
		}},
		{"soma das parcelas", 409, "closing.receivable_plan.installments", func(ids []string) map[string]any {
			return map[string]any{"receivable_plan": map[string]any{"installments": []map[string]any{{"due_date": "2026-12-10", "amount": 700}, {"due_date": "2027-01-10", "amount": 700.01}}}}
		}},
		{"entrada acima do preço", 409, "closing.down_payment.amount", func(ids []string) map[string]any {
			return map[string]any{"down_payment": map[string]any{"amount": 1400.01, "occurred_on": "2026-11-22", "payment_method": "PIX"}}
		}},
		{"data de entrega", 400, "closing.delivery_due_date", func(ids []string) map[string]any {
			return map[string]any{"delivery_due_date": "amanhã"}
		}},
	}
	for _, c := range cases {
		qid, ids := e.quoteOf(qItem("P", 10, 100, 10, prod), qItem("Sem produto", 1, 100, 10, ""), e.serviceItem("Serv", 1, 300, 100))
		// preço = 1400
		code, res := e.api.do("POST", "/quotes/"+qid+"/convert", map[string]any{"closing": c.closing(ids)})
		if code != c.status || res["field"] != c.field {
			t.Errorf("%s: %d %v, want %d %s", c.name, code, res, c.status, c.field)
		}
		quote := e.must("GET", "/quotes/"+qid, nil, 200)
		if quote["status"] != "OPEN" || quote["sale_id"] != nil {
			t.Errorf("%s: quote %v / %v", c.name, quote["status"], quote["sale_id"])
		}
	}
	if got := e.count(`SELECT COUNT(*) FROM vendas`); got != vendasBefore {
		t.Errorf("vendas = %d, want %d (nada gravado)", got, vendasBefore)
	}
	if got := e.count(`SELECT COUNT(*) FROM financial_movements`); got != movBefore {
		t.Errorf("movimentações = %d, want %d", got, movBefore)
	}
	// Venda rápida: erro desfaz o Quote também.
	quotesBefore := e.count(`SELECT COUNT(*) FROM quotes`)
	code, res := e.api.do("POST", "/quotes/quick-sale", map[string]any{
		"customer_id": e.cid, "items": []map[string]any{qItem("R", 1, 100, 10, "")},
		"closing": map[string]any{"items": []map[string]any{{"item_index": 5}}}})
	if code != 400 || res["field"] != "closing.items[0].item_index" {
		t.Errorf("quick-sale índice fora = %d %v", code, res)
	}
	code, res = e.api.do("POST", "/quotes/quick-sale", map[string]any{
		"customer_id": e.cid, "items": []map[string]any{qItem("R", 1, 100, 10, "")},
		"closing": map[string]any{"down_payment": map[string]any{"amount": 101, "occurred_on": "2026-11-22", "payment_method": "PIX"}}})
	if code != 409 || res["field"] != "closing.down_payment.amount" {
		t.Errorf("quick-sale entrada = %d %v", code, res)
	}
	if got := e.count(`SELECT COUNT(*) FROM quotes`); got != quotesBefore {
		t.Errorf("quotes = %d, want %d", got, quotesBefore)
	}
	// Quote inexistente / já convertido seguem 404 / 409.
	e.must("POST", "/quotes/"+ghost+"/convert", map[string]any{"closing": map[string]any{}}, 404)
	qid, _ := e.quoteOf(qItem("Z", 1, 1, 1, ""))
	e.must("POST", "/quotes/"+qid+"/convert", nil, 201)
	e.must("POST", "/quotes/"+qid+"/convert", map[string]any{"closing": map[string]any{}}, 409)
}
