package handlers

// Testes de integração da composição da venda (AYD-009 / SPEC-010). Só rodam com TEST_DATABASE_URL.

import (
	"testing"
)

func (e fxEnv) post(path string, body map[string]any, want int) map[string]any {
	e.t.Helper()
	code, res := e.api.do("POST", path, body)
	if code != want {
		e.t.Fatalf("POST %s %v = %d %v, want %d", path, body, code, res, want)
	}
	return res
}

func TestIntegration_QuoteChargesCarriedToSale(t *testing.T) {
	e := newFinanceEnv(t)
	body := map[string]any{"customer_id": e.cid, "items": []map[string]any{qItem("Rock face", 18, 260, 150, "")},
		"charges": []map[string]any{{"kind": "FREIGHT", "amount": 100, "description": "rota sul"}}}
	q := e.post("/quotes", body, 201)
	e.wantNum("quote", q, "total_quote", 4780)
	e.wantNum("quote", q, "items_total", 4680)
	e.wantNum("quote", q, "charges_total", 100)
	charges := q["charges"].([]any)
	if len(charges) != 1 || charges[0].(map[string]any)["kind"] != "FREIGHT" {
		t.Fatalf("charges = %v", charges)
	}
	chargeID := charges[0].(map[string]any)["id"].(string)

	// PUT: ausente mantém; lista substitui (id mantém a linha; sem id cria; o que sumiu é excluído).
	if code, res := e.api.do("PUT", "/quotes/"+q["id"].(string), map[string]any{"notes": "n"}); code != 200 || len(res["charges"].([]any)) != 1 {
		t.Fatalf("PUT sem charges = %d %v", code, res)
	}
	code, res := e.api.do("PUT", "/quotes/"+q["id"].(string), map[string]any{"charges": []map[string]any{
		{"id": chargeID, "kind": "FREIGHT", "amount": 120}, {"kind": "OTHER", "amount": 30}}})
	if code != 200 {
		t.Fatalf("PUT charges = %d %v", code, res)
	}
	e.wantNum("quote editado", res, "total_quote", 4830)
	if code, res := e.api.do("PUT", "/quotes/"+q["id"].(string), map[string]any{"charges": []map[string]any{{"kind": "OTHER", "amount": 0}}}); code != 400 {
		t.Errorf("amount 0 = %d %v, want 400", code, res)
	}
	if code, res := e.api.do("PUT", "/quotes/"+q["id"].(string), map[string]any{"charges": []map[string]any{{"kind": "X", "amount": 5}}}); code != 400 {
		t.Errorf("kind inválido = %d %v, want 400", code, res)
	}

	// Conversão copia as cobranças; a conta a receber já inclui o frete cobrado.
	conv := e.post("/quotes/"+q["id"].(string)+"/convert", nil, 201)
	_, sale := e.api.do("GET", "/vendas/"+conv["sale_id"].(string), nil)
	e.wantNum("venda", sale, "total_venda", 4830)
	comp := sale["composition"].(map[string]any)
	if len(comp["charges"].([]any)) != 2 {
		t.Fatalf("composition.charges = %v", comp["charges"])
	}
	e.wantNum("composition", comp, "items_total", 4680)
	e.wantNum("composition", comp, "charges_total", 150)
	e.wantNum("composition", comp, "price_total", 4830)
	recv := e.api.obligation(e.api.receivable(conv["sale_id"].(string)))
	e.wantNum("conta a receber", recv, "amount", 4830)
	ct := componentTypes(recv)
	if ct["SALE_ITEMS"] != 4680 || ct["SALE_CHARGE"] != 150 {
		t.Errorf("componentes = %v", ct)
	}

	// Venda rápida com charges.
	qs := e.post("/quotes/quick-sale", body, 201)
	_, sale2 := e.api.do("GET", "/vendas/"+qs["sale_id"].(string), nil)
	e.wantNum("venda rápida", sale2, "total_venda", 4780)
}

func TestIntegration_SaleChargesRoutes(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(2, 100, 60) // 200
	base := "/vendas/" + sale + "/charges"

	c := e.post(base, map[string]any{"kind": "FREIGHT", "amount": 50}, 201)
	if c["obligation_id"] == nil || len(c["warnings"].([]any)) != 0 {
		t.Errorf("POST = %v", c)
	}
	_, d := e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("venda", d, "total_venda", 250)
	e.wantNum("venda", d, "receivable", 250)
	e.wantNum("venda", d, "lucro", 250-120)

	if code, res := e.api.do("POST", base, map[string]any{"kind": "FREIGHT", "amount": 0}); code != 400 {
		t.Errorf("amount 0 = %d %v", code, res)
	}
	if code, res := e.api.do("POST", base, map[string]any{"kind": "TAX", "amount": 5}); code != 400 {
		t.Errorf("kind = %d %v", code, res)
	}
	id := c["id"].(string)
	if code, res := e.api.do("PUT", base+"/"+id, map[string]any{"kind": "OTHER", "amount": 70, "description": "descarga"}); code != 200 || num(res, "amount") != 70 {
		t.Errorf("PUT = %d %v", code, res)
	}
	code, list := e.api.do("GET", base, nil)
	_ = list
	if code != 200 {
		t.Errorf("GET = %d", code)
	}

	// Recebido 250 e depois a cobrança diminui: OVERSETTLED.
	e.api.receive(sale, 250, "2026-11-01", "PIX")
	code, res := e.api.do("PUT", base+"/"+id, map[string]any{"kind": "OTHER", "amount": 20})
	if code != 200 || !hasWarning(res, "OVERSETTLED") {
		t.Errorf("OVERSETTLED = %d %v", code, res)
	}
	if code, _ := e.api.do("DELETE", base+"/"+id, nil); code != 204 {
		t.Errorf("DELETE = %d", code)
	}
	if code, _ := e.api.do("DELETE", base+"/"+id, nil); code != 404 {
		t.Errorf("DELETE repetido = %d", code)
	}

	// Venda cancelada não aceita cobranças (409); inexistente 404.
	if code, _ := e.api.do("PATCH", "/vendas/"+sale+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatal("cancelar")
	}
	if code, res := e.api.do("POST", base, map[string]any{"kind": "FREIGHT", "amount": 10}); code != 409 {
		t.Errorf("cancelada = %d %v, want 409", code, res)
	}
	if code, _ := e.api.do("POST", "/vendas/00000000-0000-0000-0000-000000000000/charges", map[string]any{"kind": "FREIGHT", "amount": 10}); code != 404 {
		t.Errorf("inexistente = %d, want 404", code)
	}
}

func TestIntegration_SaleCostsRoutes(t *testing.T) {
	e := newFinanceEnv(t)
	partner, other := e.partner(), e.partner()
	driver, installer := e.supplier(), e.supplier()
	sale := e.simpleSale(2, 1000, 400) // 2000, custo 800
	e.api.do("PUT", "/vendas/"+sale, map[string]any{"vendedor_externo_id": partner})
	base := "/vendas/" + sale + "/costs"

	// Frete pago ao motorista; instalador; comissão sem credor informado → Partner indicador.
	fr := e.post(base, map[string]any{"kind": "FREIGHT", "amount": 150, "payee_supplier_id": driver}, 201)
	e.post(base, map[string]any{"kind": "LABOR", "amount": 300, "payee_supplier_id": installer, "description": "instalação"}, 201)
	cm := e.post(base, map[string]any{"kind": "COMMISSION", "amount": 0}, 201) // 0: sem conta ainda
	if p := cm["payee"].(map[string]any); p["type"] != "PARTNER" || p["id"] != partner || cm["obligation_id"] != nil {
		t.Errorf("comissão = %v", cm)
	}
	if p := fr["payee"].(map[string]any); p["type"] != "SUPPLIER" || p["id"] != driver || p["name"] == "" || fr["obligation_id"] == nil {
		t.Errorf("frete = %v", fr)
	}

	_, d := e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("venda", d, "costs_total", 450)
	e.wantNum("venda", d, "costs_open", 450)
	e.wantNum("venda", d, "costs_paid", 0)
	e.wantNum("venda", d, "lucro", 2000-800-450)
	if n := len(d["obligations"].([]any)); n != 3 { // a receber + motorista + instalador
		t.Errorf("obligations = %d, want 3", n)
	}
	if n := len(d["composition"].(map[string]any)["costs"].([]any)); n != 3 {
		t.Errorf("composition.costs = %d, want 3", n)
	}

	// Pagar o frete: costs_paid sobe e o lucro previsto não muda (o pago não entra duas vezes).
	if code, res := e.api.do("POST", "/obligations/"+e.api.payableOf("SALE", sale, driver)+"/settle", map[string]any{
		"movement": map[string]any{"occurred_on": "2026-11-01", "payment_method": "PIX"}}); code != 201 {
		t.Fatalf("pagar = %d %v", code, res)
	}
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("pago", d, "costs_paid", 150)
	e.wantNum("pago", d, "costs_open", 300)
	e.wantNum("pago", d, "lucro", 2000-800-450)

	// Categoria padrão da quitação pelo tipo do custo (frete → FREIGHT).
	var cat string
	e.db.Raw(`SELECT m.category::text FROM financial_movements m JOIN settlements s ON s.movement_id = m.id
		JOIN obligations o ON o.id = s.obligation_id WHERE o.sale_id = ? AND o.supplier_id = ?`, sale, driver).Scan(&cat)
	if cat != "FREIGHT" {
		t.Errorf("categoria padrão = %q, want FREIGHT", cat)
	}

	// Editar valor abaixo do quitado → OVERSETTLED; trocar o credor leva o custo para outra conta.
	id := fr["id"].(string)
	code, res := e.api.do("PUT", base+"/"+id, map[string]any{"kind": "FREIGHT", "amount": 100, "payee_supplier_id": driver})
	if code != 200 || !hasWarning(res, "OVERSETTLED") {
		t.Errorf("OVERSETTLED = %d %v", code, res)
	}
	code, res = e.api.do("PUT", base+"/"+id, map[string]any{"kind": "FREIGHT", "amount": 150, "payee_supplier_id": nil})
	if code != 200 || res["payee"] != nil {
		t.Errorf("a definir = %d %v", code, res)
	}

	// Validações (400/404).
	for name, body := range map[string]map[string]any{
		"amount negativo":       {"kind": "FREIGHT", "amount": -1},
		"kind inválido":         {"kind": "TAX", "amount": 1},
		"comissão com Supplier": {"kind": "COMMISSION", "amount": 1, "payee_supplier_id": driver},
		"frete com Partner":     {"kind": "FREIGHT", "amount": 1, "payee_partner_id": partner},
		"dois credores":         {"kind": "FREIGHT", "amount": 1, "payee_supplier_id": driver, "payee_partner_id": partner},
		"item de outra venda":   {"kind": "LABOR", "amount": 1, "sale_item_id": "00000000-0000-0000-0000-000000000001"},
	} {
		if code, res := e.api.do("POST", base, body); code != 400 {
			t.Errorf("%s = %d %v, want 400", name, code, res)
		}
	}
	if code, _ := e.api.do("POST", base, map[string]any{"kind": "FREIGHT", "amount": 1, "payee_supplier_id": "00000000-0000-0000-0000-000000000001"}); code != 404 {
		t.Errorf("credor inexistente = %d, want 404", code)
	}

	// Trocar o Partner indicador leva a comissão junto; DELETE → 204; venda cancelada aceita custos.
	e.api.do("PUT", base+"/"+cm["id"].(string), map[string]any{"kind": "COMMISSION", "amount": 80})
	e.api.do("PUT", "/vendas/"+sale, map[string]any{"vendedor_externo_id": other})
	_, costs := e.api.list(base)
	for _, c := range costs {
		if c["kind"] == "COMMISSION" && c["payee"].(map[string]any)["id"] != other {
			t.Errorf("comissão deveria seguir o novo Partner: %v", c)
		}
	}
	if code, _ := e.api.do("DELETE", base+"/"+id, nil); code != 204 {
		t.Errorf("DELETE = %d", code)
	}
	if code, _ := e.api.do("PATCH", "/vendas/"+sale+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatal("cancelar")
	}
	e.post(base, map[string]any{"kind": "OTHER", "amount": 10}, 201)
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	if d["delivery_status"] != "CANCELED" {
		t.Errorf("venda = %v", d)
	}
}
