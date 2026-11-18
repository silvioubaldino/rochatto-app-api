package handlers

import (
	"math"
	"testing"

	"github.com/silvioubaldino/sales-backend/internal/purchasing"
)

func near2(a, b float64) bool { return math.Abs(a-b) < 0.0051 }

func (e fxEnv) productWithUnit(unit string) string {
	e.t.Helper()
	var id string
	if err := e.db.Raw("INSERT INTO produtos (nome, unidade) VALUES (?, ?) RETURNING id", uniq("Item"), unit).Scan(&id).Error; err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e fxEnv) replenishment(supplier string, extra map[string]any, lines ...map[string]any) map[string]any {
	e.t.Helper()
	body := map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": supplier, "items": lines}
	for k, v := range extra {
		body[k] = v
	}
	code, p := e.api.do("POST", "/purchases", body)
	if code != 201 {
		e.t.Fatalf("POST /purchases = %d %v", code, p)
	}
	return p
}

func line(product string, qty, cost float64) map[string]any {
	return map[string]any{"product_id": product, "quantity": qty, "unit_cost": cost}
}

func findLine(t *testing.T, p map[string]any, lineID string) map[string]any {
	t.Helper()
	for _, l := range p["lines"].([]any) {
		if m := l.(map[string]any); m["line_id"] == lineID {
			return m
		}
	}
	t.Fatalf("linha %s não encontrada em %v", lineID, p["lines"])
	return nil
}

func (e fxEnv) purchase(id string) map[string]any {
	e.t.Helper()
	code, p := e.api.do("GET", "/purchases/"+id, nil)
	if code != 200 {
		e.t.Fatalf("GET /purchases/%s = %d %v", id, code, p)
	}
	return p
}

func obligationOf(t *testing.T, p map[string]any, payee string) map[string]any {
	t.Helper()
	for _, o := range p["obligations"].([]any) {
		m := o.(map[string]any)
		cp, _ := m["counterparty"].(map[string]any)
		if (payee == "" && cp == nil) || (cp != nil && cp["id"] == payee) {
			return m
		}
	}
	t.Fatalf("conta de %q não encontrada em %v", payee, p["obligations"])
	return nil
}

func TestIntegration_PurchaseComposition(t *testing.T) {
	e := newFulfillmentEnv(t)
	sup, driver := e.supplier(), e.supplier()
	prodA, prodB := e.product(), e.product()

	p := e.replenishment(sup, nil, line(prodA, 100, 27), line(prodB, 200, 90))
	id := p["id"].(string)
	if p["allocation_method"] != "BY_QUANTITY" || num(p["composition"].(map[string]any), "total") != 20700 {
		t.Fatalf("composição inicial = %v", p["composition"])
	}
	if ws, ok := p["warnings"].([]any); !ok || len(ws) != 0 {
		t.Errorf("warnings = %v, want []", p["warnings"])
	}

	for _, bad := range []map[string]any{
		{"kind": "FREIGHT", "amount": 0}, {"kind": "FREIGHT", "amount": -5}, {"kind": "XX", "amount": 5},
	} {
		if code, _ := e.api.do("POST", "/purchases/"+id+"/charges", bad); code != 400 {
			t.Errorf("POST %v = %d, want 400", bad, code)
		}
	}
	if code, _ := e.api.do("POST", "/purchases/"+id+"/charges", map[string]any{"kind": "FREIGHT", "amount": 10, "payee_supplier_id": uuidZero}); code != 404 {
		t.Errorf("credor inexistente = %d, want 404", code)
	}
	if code, _ := e.api.do("POST", "/purchases/"+uuidZero+"/charges", map[string]any{"kind": "FREIGHT", "amount": 10}); code != 404 {
		t.Errorf("compra inexistente = %d, want 404", code)
	}

	code, freight := e.api.do("POST", "/purchases/"+id+"/charges", map[string]any{"kind": "FREIGHT", "amount": 600})
	if code != 201 || freight["payee_supplier_id"] != sup || freight["obligation_id"] == nil || freight["payee_name"] == nil {
		t.Fatalf("POST frete = %d %v", code, freight)
	}
	if ws, ok := freight["warnings"].([]any); !ok || len(ws) != 0 {
		t.Errorf("warnings = %v, want []", freight["warnings"])
	}
	code, unload := e.api.do("POST", "/purchases/"+id+"/charges", map[string]any{"kind": "UNLOADING", "amount": 150, "description": "Jairo", "payee_supplier_id": driver})
	if code != 201 || unload["payee_supplier_id"] != driver {
		t.Fatalf("POST descarga = %d %v", code, unload)
	}
	code, tax := e.api.do("POST", "/purchases/"+id+"/charges", map[string]any{"kind": "TAX", "amount": 30, "payee_supplier_id": nil})
	if code != 201 || tax["payee_supplier_id"] != nil || tax["obligation_id"] == nil {
		t.Fatalf("POST imposto a definir = %d %v", code, tax)
	}

	p = e.purchase(id)
	comp := p["composition"].(map[string]any)
	if num(comp, "goods_total") != 20700 || num(comp, "charges_total") != 780 || num(comp, "total") != 21480 || len(comp["charges"].([]any)) != 3 {
		t.Errorf("composition = %v", comp)
	}
	if got := len(p["obligations"].([]any)); got != 3 {
		t.Fatalf("contas = %d, want 3: %v", got, p["obligations"])
	}
	if num(obligationOf(t, p, sup), "amount") != 21300 || num(obligationOf(t, p, driver), "amount") != 150 || num(obligationOf(t, p, ""), "amount") != 30 {
		t.Errorf("contas = %v", p["obligations"])
	}
	if num(p, "paid") != 0 || num(p, "payable") != 21480 || num(p, "open") != 21480 {
		t.Errorf("paid/payable/open = %v/%v/%v", p["paid"], p["payable"], p["open"])
	}
	_, ob := e.api.do("GET", "/obligations/"+obligationOf(t, p, sup)["id"].(string), nil)
	types := map[string]string{}
	for _, c := range ob["components"].([]any) {
		m := c.(map[string]any)
		types[m["type"].(string)] = m["id"].(string)
	}
	if types["PURCHASE_ITEMS"] != id || types["PURCHASE_CHARGE"] != freight["id"] {
		t.Errorf("componentes = %v", ob["components"])
	}

	items := p["items"].([]any)
	wantAlloc, _ := purchasing.Allocate(purchasing.ByQuantity, 780, []purchasing.Line{{Unit: "m2", Quantity: 100, UnitCost: 27}, {Unit: "m2", Quantity: 200, UnitCost: 90}})
	for i, it := range items {
		l := findLine(t, p, it.(map[string]any)["id"].(string))
		if l["line_type"] != "STOCK_ITEM" || !near2(num(l, "allocated_charges"), wantAlloc[i]) {
			t.Errorf("linha %d = %v, rateio esperado %.2f", i, l, wantAlloc[i])
		}
		base := num(l, "base_cost")
		if !near2(num(l, "landed_total"), base+wantAlloc[i]) || !near2(num(l, "landed_unit_cost")*num(l, "quantity"), base+wantAlloc[i]) {
			t.Errorf("custo de entrada = %v", l)
		}
	}
	landedA := 27 + 780.0/300 // 29.60

	if b := e.balance(prodA); num(b, "balance") != 0 {
		t.Errorf("antes de receber = %v", b)
	}
	e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "ORDERED"})
	if code, _ := e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "RECEIVED"}); code != 200 {
		t.Fatal("RECEIVED")
	}
	b := e.balance(prodA)
	if num(b, "balance") != 100 || !near2(num(b, "last_inflow_cost"), landedA) || !near2(num(b, "stock_value"), 100*landedA) {
		t.Errorf("balance A = %v (landed %.2f)", b, landedA)
	}
	mv := b["movements"].([]any)[0].(map[string]any)
	if mv["kind"] != "INFLOW" || !near2(num(mv, "unit_cost"), landedA) {
		t.Errorf("extrato INFLOW = %v", mv)
	}

	code, late := e.api.do("POST", "/purchases/"+id+"/charges", map[string]any{"kind": "OTHER", "amount": 300})
	if code != 201 {
		t.Fatalf("custo adicional em RECEIVED = %d %v", code, late)
	}
	landedA = 27 + 1080.0/300
	if b := e.balance(prodA); !near2(num(b, "last_inflow_cost"), landedA) {
		t.Errorf("custo de entrada após novo custo = %v, want %.2f", b["last_inflow_cost"], landedA)
	}
	_, list := e.api.list("/stock/balance?q=" + "Pedra")
	var sum float64
	for _, r := range list {
		sum += num(r, "stock_value")
	}
	if sum <= 0 {
		t.Errorf("Σ stock_value = %v", sum)
	}

	supOb := obligationOf(t, e.purchase(id), sup)["id"].(string)
	if code, res := e.api.do("POST", "/obligations/"+supOb+"/settle", map[string]any{
		"amount": 21300, "movement": map[string]any{"occurred_on": "2027-03-01", "payment_method": "PIX"}}); code != 201 {
		t.Fatalf("settle = %d %v", code, res)
	}
	code, upd := e.api.do("PUT", "/purchases/"+id+"/charges/"+freight["id"].(string), map[string]any{"kind": "FREIGHT", "amount": 100})
	if code != 200 || !hasWarning(upd, "OVERSETTLED") || num(upd, "amount") != 100 {
		t.Errorf("PUT frete menor = %d %v, want OVERSETTLED", code, upd)
	}
	if ws := e.purchase(id)["warnings"].([]any); len(ws) != 1 || ws[0] != "OVERSETTLED" {
		t.Errorf("GET warnings = %v", ws)
	}
	code, _ = e.api.do("PUT", "/purchases/"+id+"/charges/"+tax["id"].(string), map[string]any{"kind": "TAX", "amount": 30, "payee_supplier_id": driver})
	if code != 200 {
		t.Fatalf("PUT imposto = %d", code)
	}
	p = e.purchase(id)
	if len(p["obligations"].([]any)) != 2 || num(obligationOf(t, p, driver), "amount") != 180 {
		t.Errorf("após trocar credor: %v", p["obligations"])
	}
	if code, _ := e.api.do("DELETE", "/purchases/"+id+"/charges/"+unload["id"].(string), nil); code != 204 {
		t.Errorf("DELETE custo = %d", code)
	}
	if code, _ := e.api.do("DELETE", "/purchases/"+id+"/charges/"+unload["id"].(string), nil); code != 404 {
		t.Errorf("DELETE repetido = %d, want 404", code)
	}
	if n := len(e.purchase(id)["composition"].(map[string]any)["charges"].([]any)); n != 3 {
		t.Errorf("charges após delete = %d, want 3", n)
	}
	var live int64
	e.db.Raw(`SELECT COUNT(*) FROM purchase_charges WHERE purchase_id = ? AND deleted_at IS NOT NULL`, id).Scan(&live)
	if live != 1 {
		t.Errorf("soft-delete: %d linhas com deleted_at, want 1", live)
	}

	_, chargesList := e.api.list("/purchases/" + id + "/charges")
	if len(chargesList) != 3 {
		t.Errorf("GET charges = %d, want 3", len(chargesList))
	}
	_, open := e.api.list("/purchases?open_only=true&supplier_id=" + sup)
	if len(open) != 1 || open[0]["id"] != id || num(open[0], "open") <= 0 {
		t.Errorf("open_only = %v", open)
	}
	_, overdue := e.api.list("/purchases?overdue=true&supplier_id=" + sup)
	if len(overdue) != 0 {
		t.Errorf("overdue sem prazo vencido = %v", overdue)
	}
	obID := obligationOf(t, e.purchase(id), driver)["id"].(string)
	if code, res := e.api.do("PATCH", "/obligations/"+obID, map[string]any{"due_date": "2020-01-01"}); code != 200 {
		t.Fatalf("PATCH due_date = %d %v", code, res)
	}
	if _, overdue = e.api.list("/purchases?overdue=true&supplier_id=" + sup); len(overdue) != 1 {
		t.Errorf("overdue = %v, want 1", overdue)
	}

	p2 := e.replenishment(sup, nil, line(prodA, 1, 1))
	if code, _ := e.api.do("DELETE", "/purchases/"+p2["id"].(string), nil); code != 204 {
		t.Fatalf("DELETE purchase = %d", code)
	}
	if code, _ := e.api.do("POST", "/purchases/"+p2["id"].(string)+"/charges", map[string]any{"kind": "FREIGHT", "amount": 5}); code != 409 {
		t.Errorf("custo em compra excluída = %d, want 409", code)
	}
}

func TestIntegration_PurchaseAllocationMethodAndUnits(t *testing.T) {
	e := newFulfillmentEnv(t)
	sup := e.supplier()
	stone, bag := e.productWithUnit("m2"), e.productWithUnit("saco")

	p := e.replenishment(sup, nil, line(stone, 10, 100), line(bag, 5, 20))
	id := p["id"].(string)
	if code, res := e.api.do("POST", "/purchases/"+id+"/charges", map[string]any{"kind": "FREIGHT", "amount": 110}); code != 422 {
		t.Fatalf("BY_QUANTITY misto = %d %v, want 422", code, res)
	}
	if n := len(e.purchase(id)["composition"].(map[string]any)["charges"].([]any)); n != 0 {
		t.Errorf("422 deve desfazer o custo (tx): %d", n)
	}
	if code, _ := e.api.do("PUT", "/purchases/"+id, map[string]any{"allocation_method": "POR_PESO"}); code != 400 {
		t.Errorf("método inválido = %d, want 400", code)
	}
	if code, _ := e.api.do("PUT", "/purchases/"+id, map[string]any{"allocation_method": "BY_QUANTITY"}); code != 422 {
		t.Errorf("PUT BY_QUANTITY misto = %d, want 422", code)
	}
	code, upd := e.api.do("PUT", "/purchases/"+id, map[string]any{"allocation_method": "BY_VALUE"})
	if code != 200 || upd["allocation_method"] != "BY_VALUE" {
		t.Fatalf("PUT BY_VALUE = %d %v", code, upd)
	}
	if code, res := e.api.do("POST", "/purchases/"+id+"/charges", map[string]any{"kind": "FREIGHT", "amount": 110}); code != 201 {
		t.Fatalf("BY_VALUE misto = %d %v", code, res)
	}
	p = e.purchase(id)
	items := p["items"].([]any)
	la, lb := findLine(t, p, items[0].(map[string]any)["id"].(string)), findLine(t, p, items[1].(map[string]any)["id"].(string))
	if !near2(num(la, "allocated_charges"), 100) || !near2(num(lb, "allocated_charges"), 10) || !near2(num(lb, "landed_unit_cost"), 22) {
		t.Errorf("rateio BY_VALUE = %v | %v", la, lb)
	}
	if code, _ := e.api.do("PUT", "/purchases/"+id, map[string]any{"allocation_method": "BY_QUANTITY"}); code != 422 {
		t.Errorf("voltar a BY_QUANTITY misto = %d, want 422", code)
	}

	if code, _ := e.api.do("POST", "/purchases", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": sup, "allocation_method": "X"}); code != 400 {
		t.Errorf("POST método inválido = %d, want 400", code)
	}
	if p3 := e.replenishment(sup, map[string]any{"allocation_method": "BY_VALUE"}, line(stone, 1, 1)); p3["allocation_method"] != "BY_VALUE" {
		t.Errorf("POST BY_VALUE = %v", p3["allocation_method"])
	}

	z := e.replenishment(sup, map[string]any{"allocation_method": "BY_VALUE"}, line(stone, 1, 0), line(stone, 3, 0))
	zid := z["id"].(string)
	e.api.do("POST", "/purchases/"+zid+"/charges", map[string]any{"kind": "FREIGHT", "amount": 40})
	z = e.purchase(zid)
	allocs := []float64{}
	for _, l := range z["lines"].([]any) {
		allocs = append(allocs, num(l.(map[string]any), "allocated_charges"))
	}
	if !((near2(allocs[0], 10) && near2(allocs[1], 30)) || (near2(allocs[0], 30) && near2(allocs[1], 10))) {
		t.Errorf("fallback por quantidade = %v", allocs)
	}
}

func TestIntegration_PurchaseChargesFlowToSaleProfitAndSnapshot(t *testing.T) {
	e := newFulfillmentEnv(t)
	sup, other := e.supplier(), e.supplier()
	prod := e.product()

	saleID, it := e.sale(qItem("MTO1", 10, 200, 100, ""), qItem("MTO2", 30, 200, 100, ""))
	for i, id := range it {
		name := []string{"MTO1", "MTO2"}[i]
		qty := []float64{10, 30}[i]
		if code, res := e.putItem(saleID, id, map[string]any{"produto_nome": name, "quantidade": qty, "preco_venda_unit": 200, "custo_unit": 100, "fornecedor_id": sup}); code != 200 {
			t.Fatalf("PUT item = %d %v", code, res)
		}
	}
	_, before := e.api.do("GET", "/vendas/"+saleID, nil)
	lucroBefore := num(before, "lucro")

	code, p := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup, "sale_item_ids": it})
	if code != 201 {
		t.Fatalf("POST MTO = %d %v", code, p)
	}
	id := p["id"].(string)
	otherSale, oit := e.sale(qItem("O1", 5, 200, 100, ""))
	e.putItem(otherSale, oit[0], map[string]any{"produto_nome": "O1", "quantidade": 5, "preco_venda_unit": 200, "custo_unit": 100, "fornecedor_id": other})
	_, otherBefore := e.api.do("GET", "/vendas/"+otherSale, nil)

	if code, res := e.api.do("POST", "/purchases/"+id+"/charges", map[string]any{"kind": "FREIGHT", "amount": 400}); code != 201 {
		t.Fatalf("POST custo MTO = %d %v", code, res)
	}
	_, after := e.api.do("GET", "/vendas/"+saleID, nil)
	if !near2(lucroBefore-num(after, "lucro"), 400) {
		t.Errorf("lucro %v → %v, queda esperada 400", lucroBefore, after["lucro"])
	}
	_, otherAfter := e.api.do("GET", "/vendas/"+otherSale, nil)
	if num(otherAfter, "lucro") != num(otherBefore, "lucro") {
		t.Errorf("Sale de outra compra mudou: %v → %v", otherBefore["lucro"], otherAfter["lucro"])
	}
	pd := e.purchase(id)
	for _, l := range pd["lines"].([]any) {
		m := l.(map[string]any)
		if m["line_type"] != "SALE_ITEM" || !near2(num(m, "allocated_charges"), 400*num(m, "quantity")/40) {
			t.Errorf("linha SALE_ITEM = %v", m)
		}
	}
	if num(pd["composition"].(map[string]any), "total") != 4400 {
		t.Errorf("composition = %v", pd["composition"])
	}
	var tc float64
	e.db.Raw(`SELECT total_custo FROM vw_vendas_resumo WHERE id = ?`, saleID).Scan(&tc)
	if !near2(tc, 4000+400) {
		t.Errorf("total_custo = %v, want 4400", tc)
	}

	r := e.replenishment(sup, nil, line(prod, 10, 50))
	rid := r["id"].(string)
	e.api.do("POST", "/purchases/"+rid+"/charges", map[string]any{"kind": "FREIGHT", "amount": 100})
	e.api.do("POST", "/purchases/"+rid+"/charges", map[string]any{"kind": "UNLOADING", "amount": 50, "payee_supplier_id": other})
	code, moved := e.api.do("PUT", "/purchases/"+rid, map[string]any{"supplier_id": other})
	if code != 200 || moved["supplier_id"] != other {
		t.Fatalf("PUT supplier = %d %v", code, moved)
	}
	if obs := moved["obligations"].([]any); len(obs) != 1 || num(obs[0].(map[string]any), "amount") != 650 {
		t.Errorf("após trocar Supplier: %v", moved["obligations"])
	}
	if code, _ := e.api.do("PUT", "/purchases/"+id, map[string]any{"supplier_id": other}); code != 409 {
		t.Errorf("trocar Supplier de compra com Items de venda = %d, want 409", code)
	}

	e.api.do("PATCH", "/purchases/"+rid+"/status", map[string]any{"status": "ORDERED"})
	e.api.do("PATCH", "/purchases/"+rid+"/status", map[string]any{"status": "RECEIVED"})
	landed := 50 + 150.0/10
	fsSale, fit := e.sale(qItem("FS", 1, 300, 1, ""))
	code, res := e.putItem(fsSale, fit[0], map[string]any{"produto_id": prod, "produto_nome": "FS", "quantidade": 1, "preco_venda_unit": 300, "origin": "FROM_STOCK"})
	if code != 200 || !near2(num(res, "custo_unit"), landed) {
		t.Fatalf("FROM_STOCK custo = %d %v, want %.2f", code, res, landed)
	}
	e.api.do("POST", "/purchases/"+rid+"/charges", map[string]any{"kind": "OTHER", "amount": 500, "payee_supplier_id": other})
	_, fsAfter := e.api.do("GET", "/vendas/"+fsSale, nil)
	if got := num(fsAfter["itens"].([]any)[0].(map[string]any), "custo_unit"); !near2(got, landed) {
		t.Errorf("snapshot mudou: %v, want %.2f", got, landed)
	}
}

const uuidZero = "00000000-0000-0000-0000-000000000000"
