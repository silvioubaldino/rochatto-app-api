package handlers

import (
	"testing"
)

type pendingResult struct {
	t      *testing.T
	raw    map[string]any
	groups map[string]map[string]any
	order  []string
}

func (e fxEnv) pending(query string) pendingResult {
	e.t.Helper()
	path := "/pending-actions"
	if query != "" {
		path += "?" + query
	}
	raw := e.must("GET", path, nil, 200)
	r := pendingResult{t: e.t, raw: raw, groups: map[string]map[string]any{}}
	for _, g := range raw["groups"].([]any) {
		g := g.(map[string]any)
		kind := g["kind"].(string)
		r.groups[kind] = g
		r.order = append(r.order, kind)
	}
	return r
}

func (r pendingResult) group(kind string, count, overdue int) []map[string]any {
	r.t.Helper()
	g, ok := r.groups[kind]
	if !ok {
		if count != 0 {
			r.t.Errorf("%s: grupo ausente, want count=%d", kind, count)
		}
		return nil
	}
	if int(num(g, "count")) != count || int(num(g, "overdue_count")) != overdue {
		r.t.Errorf("%s: count=%v overdue=%v, want %d/%d", kind, g["count"], g["overdue_count"], count, overdue)
	}
	var items []map[string]any
	for _, it := range g["items"].([]any) {
		items = append(items, it.(map[string]any))
	}
	return items
}

func entityID(it map[string]any) string { return it["entity"].(map[string]any)["id"].(string) }
func entityType(it map[string]any) string {
	return it["entity"].(map[string]any)["type"].(string)
}

func TestJourney_PendingActions(t *testing.T) {
	e := newJourneyEnv(t)
	sup, sup2 := e.supplier(), e.supplier()
	stockProd := e.product()
	closeWith := func(items []map[string]any, closing map[string]any) map[string]any {
		return e.must("POST", "/quotes/quick-sale", map[string]any{"customer_id": e.cid, "items": items, "closing": closing}, 201)
	}

	s1 := closeWith([]map[string]any{qItem("P1", 10, 100, 60, "")}, map[string]any{
		"delivery_due_date": dayStr(-2),
		"items":             []map[string]any{{"item_index": 0, "supplier_id": sup}},
		"receivable_plan": map[string]any{"installments": []map[string]any{
			{"due_date": dayStr(-3), "amount": 400}, {"due_date": dayStr(3), "amount": 600}}},
	})
	s2 := e.must("POST", "/quotes/quick-sale", map[string]any{"customer_id": e.cid, "items": []map[string]any{qItem("P2", 5, 100, 10, "")}}, 201)
	s3 := closeWith([]map[string]any{qItem("Estoque", 2, 100, 0, stockProd)}, map[string]any{
		"delivery_due_date": dayStr(30),
		"items":             []map[string]any{{"item_index": 0, "origin": "FROM_STOCK"}},
		"costs": []map[string]any{
			{"kind": "LABOR", "amount": 100, "payee_id": nil},
			{"kind": "FREIGHT", "amount": 50, "payee_id": sup},
		},
		"receivable_plan": map[string]any{"installments": []map[string]any{{"due_date": dayStr(30), "amount": 200}}},
	})
	freight := ""
	for _, o := range e.api.obligationsOf("SALE", s3["sale_id"].(string), "direction=PAYABLE", "counterparty_id="+sup) {
		freight = o.(map[string]any)["id"].(string)
	}
	e.must("PATCH", "/obligations/"+freight, map[string]any{"due_date": dayStr(2)}, 200)
	s4 := closeWith([]map[string]any{qItem("A", 1, 100, 10, ""), qItem("B", 1, 100, 10, "")}, map[string]any{
		"items": []map[string]any{{"item_index": 0, "supplier_id": sup}, {"item_index": 1, "supplier_id": sup2}}})
	_, s4sale := e.api.do("GET", "/vendas/"+s4["sale_id"].(string), nil)
	s4items := s4sale["itens"].([]any)
	itemOf := func(name string) string {
		for _, it := range s4items {
			if it.(map[string]any)["produto_nome"] == name {
				return it.(map[string]any)["id"].(string)
			}
		}
		return ""
	}
	p1 := e.must("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup, "sale_item_ids": []string{itemOf("A")}}, 201)
	p2 := e.must("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup2, "sale_item_ids": []string{itemOf("B")}}, 201)
	e.must("PUT", "/purchases/"+p2["id"].(string), map[string]any{"expected_date": dayStr(-1)}, 200)
	e.must("PATCH", "/purchases/"+p2["id"].(string)+"/status", map[string]any{"status": "ORDERED"}, 200)
	s5 := closeWith([]map[string]any{qItem("P5", 1, 100, 10, "")}, map[string]any{"delivery_due_date": dayStr(-5)})
	e.must("PATCH", "/vendas/"+s5["sale_id"].(string)+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}, 200)
	stale := e.must("POST", "/quotes", map[string]any{"customer_id": e.cid, "quote_date": dayStr(-10), "items": []map[string]any{qItem("Q", 1, 50, 5, "")}}, 201)
	e.must("POST", "/quotes", map[string]any{"customer_id": e.cid, "quote_date": dayStr(-7), "items": []map[string]any{qItem("Q", 1, 50, 5, "")}}, 201)
	e.db.Exec(`UPDATE quotes SET quote_date = ? WHERE id = ?`, dayStr(-20), s2["quote_id"])

	r := e.pending("")
	if r.raw["today"] != dayStr(0) || num(r.raw, "horizon_days") != 7 {
		t.Errorf("today/horizon = %v / %v", r.raw["today"], r.raw["horizon_days"])
	}
	wantOrder := []string{"RECEIVABLE_DUE", "PAYABLE_DUE", "DELIVERY_DUE", "ITEMS_WITHOUT_SUPPLIER", "ITEMS_TO_PURCHASE",
		"PURCHASE_TO_ORDER", "PURCHASE_TO_RECEIVE", "RECEIVABLE_WITHOUT_PLAN", "COST_WITHOUT_PAYEE", "QUOTE_STALE", "NEGATIVE_STOCK"}
	if len(r.order) != len(wantOrder) {
		t.Fatalf("grupos = %v, want %v", r.order, wantOrder)
	}
	for i := range wantOrder {
		if r.order[i] != wantOrder[i] {
			t.Fatalf("ordem = %v, want %v", r.order, wantOrder)
		}
	}
	if num(r.raw, "total_overdue") != 4 {
		t.Errorf("total_overdue = %v, want 4", r.raw["total_overdue"])
	}

	it := r.group("RECEIVABLE_DUE", 1, 1)[0]
	if entityType(it) != "OBLIGATION" || num(it, "number") != num(s1, "sale_number") || it["title"] != "Alejandra" ||
		it["date"] != dayStr(-3) || it["overdue"] != true || num(it, "amount") != 400 {
		t.Errorf("RECEIVABLE_DUE = %v", it)
	}
	if g := r.groups["RECEIVABLE_DUE"]; num(g, "amount") != 400 {
		t.Errorf("RECEIVABLE_DUE.amount = %v", g["amount"])
	}
	it = r.group("PAYABLE_DUE", 1, 0)[0]
	if entityID(it) != freight || it["date"] != dayStr(2) || num(it, "amount") != 50 || num(it, "number") != num(s3, "sale_number") {
		t.Errorf("PAYABLE_DUE = %v", it)
	}
	items := r.group("DELIVERY_DUE", 3, 1)
	if entityType(items[0]) != "SALE" || entityID(items[0]) != s1["sale_id"] || items[0]["overdue"] != true || items[0]["date"] != dayStr(-2) || num(items[0], "amount") != 1000 {
		t.Errorf("DELIVERY_DUE[0] = %v", items[0])
	}
	for _, it := range items[1:] {
		if it["date"] != nil || it["overdue"] != false {
			t.Errorf("DELIVERY_DUE sem prazo = %v", it)
		}
	}
	it = r.group("ITEMS_WITHOUT_SUPPLIER", 1, 0)[0]
	if entityType(it) != "SALE" || entityID(it) != s2["sale_id"] || num(it, "quantity") != 1 || num(it, "number") != num(s2, "sale_number") {
		t.Errorf("ITEMS_WITHOUT_SUPPLIER = %v", it)
	}
	it = r.group("ITEMS_TO_PURCHASE", 1, 1)[0]
	if entityType(it) != "SUPPLIER" || entityID(it) != sup || num(it, "amount") != 600 || num(it, "quantity") != 1 || it["date"] != dayStr(-2) {
		t.Errorf("ITEMS_TO_PURCHASE = %v", it)
	}
	it = r.group("PURCHASE_TO_ORDER", 1, 0)[0]
	if entityType(it) != "PURCHASE" || entityID(it) != p1["id"] || num(it, "number") != num(p1, "number") {
		t.Errorf("PURCHASE_TO_ORDER = %v", it)
	}
	it = r.group("PURCHASE_TO_RECEIVE", 1, 1)[0]
	if entityID(it) != p2["id"] || it["date"] != dayStr(-1) || it["overdue"] != true {
		t.Errorf("PURCHASE_TO_RECEIVE = %v", it)
	}
	items = r.group("RECEIVABLE_WITHOUT_PLAN", 2, 0)
	if len(items) != 2 || items[0]["date"] != nil || num(items[0], "amount")+num(items[1], "amount") != 700 {
		t.Errorf("RECEIVABLE_WITHOUT_PLAN = %v", items)
	}
	it = r.group("COST_WITHOUT_PAYEE", 1, 0)[0]
	if it["title"] != "A definir" || num(it, "amount") != 100 || num(it, "number") != num(s3, "sale_number") {
		t.Errorf("COST_WITHOUT_PAYEE = %v", it)
	}
	it = r.group("QUOTE_STALE", 1, 0)[0]
	if entityType(it) != "QUOTE" || entityID(it) != stale["id"] || it["date"] != dayStr(-10) || num(it, "number") != num(stale, "number") || num(it, "amount") != 50 {
		t.Errorf("QUOTE_STALE = %v", it)
	}
	it = r.group("NEGATIVE_STOCK", 1, 0)[0]
	if entityType(it) != "PRODUCT" || entityID(it) != stockProd || num(it, "quantity") != -2 {
		t.Errorf("NEGATIVE_STOCK = %v", it)
	}

	wide := e.pending("horizon_days=60")
	wide.group("RECEIVABLE_DUE", 2, 1)
	wide.group("DELIVERY_DUE", 4, 1)
	narrow := e.pending("horizon_days=0")
	narrow.group("PAYABLE_DUE", 0, 0)
	narrow.group("RECEIVABLE_DUE", 1, 1)

	e.api.receive(s1["sale_id"].(string), 400, dayStr(0), "PIX")
	after := e.pending("horizon_days=0")
	after.group("RECEIVABLE_DUE", 0, 0)
	if e.pending("").groups["RECEIVABLE_DUE"]["count"] != float64(1) {
		t.Errorf("a próxima parcela deveria aparecer no horizonte 7")
	}

	for _, q := range []string{"horizon_days=-1", "horizon_days=61", "horizon_days=x"} {
		if code, res := e.api.do("GET", "/pending-actions?"+q, nil); code != 400 || res["error"] == nil {
			t.Errorf("GET ?%s = %d %v, want 400", q, code, res)
		}
	}
}

func TestJourney_PendingActionsEmpty(t *testing.T) {
	e := newJourneyEnv(t)
	r := e.pending("")
	if len(r.order) != 0 || num(r.raw, "total_overdue") != 0 || r.raw["groups"] == nil {
		t.Errorf("sem pendências: %v", r.raw)
	}
}
