package handlers

// Testes de integração dos serviços na Sale (AYD-010 / SPEC-011). Só rodam com TEST_DATABASE_URL.

import (
	"strings"
	"testing"
)

func sItem(name string, qty, price, cost float64, serviceID string) map[string]any {
	m := map[string]any{"kind": "SERVICE", "product_name": name, "quantity": qty, "unit_price": price, "unit_cost": cost}
	if serviceID != "" {
		m["service_id"] = serviceID
	}
	return m
}

func (e fxEnv) service(name string) string {
	e.t.Helper()
	res := e.post("/services", map[string]any{"name": name, "unit": "m²", "reference_price": 300, "reference_cost": 240}, 201)
	return res["id"].(string)
}

// itemByKind devolve o primeiro Item da Sale do tipo dado.
func itemByKind(t *testing.T, sale map[string]any, kind string) map[string]any {
	t.Helper()
	for _, it := range sale["itens"].([]any) {
		if m := it.(map[string]any); m["kind"] == kind {
			return m
		}
	}
	t.Fatalf("Sale sem Item %s: %v", kind, sale["itens"])
	return nil
}

func TestIntegration_ServicesCatalogCRUD(t *testing.T) {
	e := newFinanceEnv(t)
	name := uniq("Instalação")
	s := e.post("/services", map[string]any{"name": " " + name + " ", "unit": "m²", "reference_price": 300, "reference_cost": 240, "notes": "por m²"}, 201)
	id := s["id"].(string)
	if s["name"] != name || s["unit"] != "m²" || num(s, "reference_price") != 300 || num(s, "reference_cost") != 240 || s["notes"] != "por m²" || s["created_at"] == nil {
		t.Fatalf("POST = %v", s)
	}
	// Opcionais ausentes viram null.
	min := e.post("/services", map[string]any{"name": uniq("Consultoria")}, 201)
	if min["unit"] != nil || min["reference_price"] != nil || min["reference_cost"] != nil || min["notes"] != nil {
		t.Errorf("campos opcionais = %v", min)
	}

	if code, _ := e.api.do("POST", "/services", map[string]any{"name": "  "}); code != 400 {
		t.Errorf("name vazio = %d, want 400", code)
	}
	if code, _ := e.api.do("POST", "/services", map[string]any{"name": "X", "reference_price": -1}); code != 400 {
		t.Errorf("preço negativo = %d, want 400", code)
	}
	if code, res := e.api.do("POST", "/services", map[string]any{"name": strings.ToUpper(name)}); code != 409 {
		t.Errorf("nome duplicado (caixa) = %d %v, want 409", code, res)
	}

	// Busca sem acento nem caixa.
	code, rows := e.api.list("/services?q=" + strings.ReplaceAll(strings.ToLower(strings.Replace(name, "ç", "c", 1)), " ", "%20"))
	if code != 200 || len(rows) != 1 || rows[0]["id"] != id {
		t.Errorf("busca sem acento = %d %v", code, rows)
	}
	if code, rows := e.api.list("/services?limit=1"); code != 200 || len(rows) != 1 {
		t.Errorf("limit=1 = %d %d linhas", code, len(rows))
	}
	if code, _ := e.api.list("/services?limit=abc"); code != 400 {
		t.Errorf("limit inválido = %d, want 400", code)
	}
	if code, g := e.api.do("GET", "/services/"+id, nil); code != 200 || g["name"] != name {
		t.Errorf("GET = %d %v", code, g)
	}
	if code, _ := e.api.do("GET", "/services/00000000-0000-0000-0000-000000000000", nil); code != 404 {
		t.Errorf("GET inexistente = %d, want 404", code)
	}

	// PUT: mantém o próprio nome; nome de outro serviço ativo = 409.
	if code, res := e.api.do("PUT", "/services/"+id, map[string]any{"name": name, "unit": "hora", "reference_price": 90}); code != 200 || res["unit"] != "hora" || res["reference_cost"] != nil {
		t.Errorf("PUT = %d %v", code, res)
	}
	if code, _ := e.api.do("PUT", "/services/"+id, map[string]any{"name": min["name"]}); code != 409 {
		t.Errorf("PUT nome de outro = %d, want 409", code)
	}

	// Soft-delete: some das buscas; o nome volta a poder ser usado; o Item já vendido mantém o vínculo.
	sale, _ := e.sale(qItem("Pedra", 1, 100, 60, ""), sItem("Instalação", 1, 50, 0, id))
	if code, _ := e.api.do("DELETE", "/services/"+id, nil); code != 204 {
		t.Fatalf("DELETE = %d", code)
	}
	if code, _ := e.api.do("DELETE", "/services/"+id, nil); code != 404 {
		t.Errorf("DELETE repetido = %d, want 404", code)
	}
	if code, _ := e.api.do("GET", "/services/"+id, nil); code != 404 {
		t.Errorf("GET excluído = %d, want 404", code)
	}
	e.post("/services", map[string]any{"name": name}, 201)
	_, d := e.api.do("GET", "/vendas/"+sale, nil)
	if it := itemByKind(t, d, "SERVICE"); it["service_id"] != id {
		t.Errorf("vínculo do Item = %v, want %s", it["service_id"], id)
	}
	// Serviço excluído não pode ser vinculado a Item novo (404).
	if code, _ := e.api.do("POST", "/vendas/"+sale+"/itens", map[string]any{"kind": "SERVICE", "service_id": id, "produto_nome": "x", "quantidade": 1, "preco_venda_unit": 1}); code != 404 {
		t.Errorf("service_id excluído em Item novo = %d, want 404", code)
	}
}

func TestIntegration_ServiceItemValidationsOnQuote(t *testing.T) {
	e := newFinanceEnv(t)
	svc := e.service(uniq("Instalação"))
	prod := e.product()
	post := func(item map[string]any) int {
		code, _ := e.api.do("POST", "/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{item}})
		return code
	}
	bad := sItem("Inst", 1, 10, 0, "")
	bad["product_id"] = prod
	if c := post(bad); c != 400 {
		t.Errorf("SERVICE com product_id = %d, want 400", c)
	}
	if c := post(qItem("Pedra", 1, 10, 5, "")); c != 201 {
		t.Errorf("PRODUCT sem kind (default) = %d, want 201", c)
	}
	withSvc := qItem("Pedra", 1, 10, 5, "")
	withSvc["service_id"] = svc
	if c := post(withSvc); c != 400 {
		t.Errorf("PRODUCT com service_id = %d, want 400", c)
	}
	if c := post(sItem("Inst", 1, 10, 0, "00000000-0000-0000-0000-000000000000")); c != 404 {
		t.Errorf("service_id inexistente = %d, want 404", c)
	}
	invalid := sItem("Inst", 1, 10, 0, "")
	invalid["kind"] = "OTHER"
	if c := post(invalid); c != 400 {
		t.Errorf("kind inválido = %d, want 400", c)
	}

	// Serviço do catálogo sem nome assume o nome do catálogo; kind e service_id voltam no Quote.
	q := e.post("/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{
		{"kind": "SERVICE", "service_id": svc, "quantity": 12, "unit_price": 300, "unit_cost": 240},
		qItem("Rock face", 12, 400, 250, "")}}, 201)
	items := q["items"].([]any)
	s0, s1 := items[0].(map[string]any), items[1].(map[string]any)
	if s0["kind"] != "SERVICE" || s0["service_id"] != svc || !strings.HasPrefix(s0["product_name"].(string), "Instalação") || s0["product_id"] != nil {
		t.Errorf("Item de serviço = %v", s0)
	}
	if s1["kind"] != "PRODUCT" || s1["service_id"] != nil {
		t.Errorf("Item de produto = %v", s1)
	}
	e.wantNum("quote", q, "total_quote", 12*300+12*400)

	// Items do Quote: POST/PUT/DELETE; mudar kind = 409.
	qid := q["id"].(string)
	added := e.post("/quotes/"+qid+"/items", sItem("Consultoria", 1, 2100, 0, ""), 201)
	if added["kind"] != "SERVICE" || added["service_id"] != nil {
		t.Errorf("POST item = %v", added)
	}
	itemPath := "/quotes/" + qid + "/items/" + added["id"].(string)
	if code, res := e.api.do("PUT", itemPath, map[string]any{"product_name": "Consultoria 2", "quantity": 2, "unit_price": 1000, "unit_cost": 0}); code != 200 || res["kind"] != "SERVICE" {
		t.Errorf("PUT mantendo o tipo = %d %v", code, res)
	}
	if code, _ := e.api.do("PUT", itemPath, map[string]any{"kind": "PRODUCT", "product_name": "Consultoria 2", "quantity": 2, "unit_price": 1000, "unit_cost": 0}); code != 409 {
		t.Errorf("PUT mudando kind = %d, want 409", code)
	}
	if code, _ := e.api.do("PUT", itemPath, map[string]any{"product_name": "X", "quantity": 1, "unit_price": 1, "unit_cost": 0, "product_id": prod}); code != 400 {
		t.Errorf("PUT SERVICE com product_id = %d, want 400", code)
	}
	if code, _ := e.api.do("DELETE", itemPath, nil); code != 204 {
		t.Errorf("DELETE item = %d", code)
	}
}

// AYD-010 §2 e §3: conversão (LABOR ligado ao Item, custo_unit 0), Profit sem custo duplicado e o
// serviço fora da fila de compra, do Stock e do ranking de Products.
func TestIntegration_ServiceConversionCostAndExclusions(t *testing.T) {
	e := newFinanceEnv(t)
	svc := e.service(uniq("Instalação"))
	prod := e.product()
	q := e.post("/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{
		qItem("Rock face", 12, 400, 250, prod),
		sItem("Instalação", 12, 300, 240, svc), // custo estimado 12 × 240 = 2880
		sItem("Consultoria", 1, 2100, 0, ""),   // custo 0: nenhum custo da venda
	}}, 201)
	conv := e.post("/quotes/"+q["id"].(string)+"/convert", nil, 201)
	sale := conv["sale_id"].(string)

	_, d := e.api.do("GET", "/vendas/"+sale, nil)
	inst, cons, stone := itemByKind(t, d, "SERVICE"), map[string]any(nil), itemByKind(t, d, "PRODUCT")
	for _, it := range d["itens"].([]any) {
		if m := it.(map[string]any); m["produto_nome"] == "Consultoria" {
			cons = m
		}
	}
	if inst["produto_nome"] == "Consultoria" {
		inst = map[string]any{}
		for _, it := range d["itens"].([]any) {
			if m := it.(map[string]any); m["produto_nome"] == "Instalação" {
				inst = m
			}
		}
	}
	if inst["origin"] != nil || inst["purchase_id"] != nil || inst["fornecedor_id"] != nil || inst["produto_id"] != nil || num(inst, "custo_unit") != 0 || inst["service_id"] != svc {
		t.Errorf("Item de serviço na Sale = %v", inst)
	}
	if stone["origin"] != "MADE_TO_ORDER" || num(stone, "custo_unit") != 250 {
		t.Errorf("Item de pedra = %v", stone)
	}
	if cons == nil || cons["kind"] != "SERVICE" || cons["service_id"] != nil {
		t.Errorf("serviço em texto livre = %v", cons)
	}

	// Um único custo LABOR (o de custo estimado 0 não gera): 12 × 240, sem credor, ligado ao Item.
	costs := d["composition"].(map[string]any)["costs"].([]any)
	if len(costs) != 1 {
		t.Fatalf("custos = %v, want 1 LABOR", costs)
	}
	c := costs[0].(map[string]any)
	if c["kind"] != "LABOR" || num(c, "amount") != 2880 || c["payee"] != nil || c["sale_item_id"] != inst["id"] {
		t.Errorf("custo LABOR = %v", c)
	}
	// Preço = 4800 + 3600 + 2100; Profit = preço − pedra (3000) − LABOR (2880): o custo conta uma vez.
	e.wantNum("venda", d, "total_venda", 4800+3600+2100)
	e.wantNum("venda", d, "total_custo", 3000)
	e.wantNum("venda", d, "costs_total", 2880)
	e.wantNum("venda", d, "lucro", 4800+3600+2100-3000-2880)
	e.wantNum("venda", d, "receivable", 4800+3600+2100)
	if n := len(d["obligations"].([]any)); n != 2 { // a receber + a pagar (credor a definir)
		t.Errorf("obligations = %d, want 2", n)
	}
	// O dono escolhe o instalador (AYD-009): o custo ligado segue editável.
	installer := e.supplier()
	if code, res := e.api.do("PUT", "/vendas/"+sale+"/costs/"+c["id"].(string), map[string]any{
		"kind": "LABOR", "amount": 2880, "payee_supplier_id": installer, "sale_item_id": inst["id"]}); code != 200 {
		t.Fatalf("PUT custo = %d %v", code, res)
	}

	// Fila de compra: só a pedra (sem Supplier). Serviço nunca aparece.
	code, groups := e.api.list("/purchases/pending-sale-items")
	if code != 200 {
		t.Fatalf("pending = %d", code)
	}
	for _, g := range groups {
		for _, x := range g["items"].([]any) {
			if id := x.(map[string]any)["id"]; id == inst["id"] || id == cons["id"] {
				t.Errorf("Item de serviço na fila de compra: %v", x)
			}
		}
	}
	// Purchase com Item SERVICE → 409 (criação e anexação). Mesmo com o credor do instalador.
	if code, res := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": installer, "sale_item_ids": []string{inst["id"].(string)}}); code != 409 {
		t.Errorf("POST /purchases com serviço = %d %v, want 409", code, res)
	}
	_, p := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": installer})
	if code, res := e.api.do("POST", "/purchases/"+p["id"].(string)+"/sale-items", map[string]any{"sale_item_ids": []string{inst["id"].(string)}}); code != 409 {
		t.Errorf("sale-items com serviço = %d %v, want 409", code, res)
	}

	// Stock: serviço não gera Outflow nem aparece no Balance (o Product continua com saldo 0).
	if b := e.balance(prod); num(b, "balance") != 0 {
		t.Errorf("balance = %v", b)
	}
	var outflows int
	e.db.Raw(`SELECT COUNT(*) FROM itens_venda WHERE venda_id = ? AND kind = 'SERVICE' AND origin = 'FROM_STOCK'`, sale).Scan(&outflows)
	if outflows != 0 {
		t.Errorf("serviço FROM_STOCK = %d", outflows)
	}
}

func TestIntegration_SaleServiceItemRoutes(t *testing.T) {
	e := newFinanceEnv(t)
	svc := e.service(uniq("Instalação"))
	prod := e.product()
	sup := e.supplier()
	sale, ids := e.sale(qItem("Pedra", 2, 1000, 600, ""))
	base := "/vendas/" + sale + "/itens"
	ok := map[string]any{"kind": "SERVICE", "service_id": svc, "produto_nome": "Instalação", "quantidade": 4, "preco_venda_unit": 300}

	it := e.post(base, ok, 201)
	if it["kind"] != "SERVICE" || it["origin"] != nil || num(it, "custo_unit") != 0 || it["service_id"] != svc || len(it["warnings"].([]any)) != 0 {
		t.Fatalf("POST serviço = %v", it)
	}
	// Sem nome, assume o do catálogo.
	named := e.post(base, map[string]any{"kind": "SERVICE", "service_id": svc, "quantidade": 1, "preco_venda_unit": 50}, 201)
	if !strings.HasPrefix(named["produto_nome"].(string), "Instalação") {
		t.Errorf("nome do catálogo = %v", named["produto_nome"])
	}
	_, d := e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("venda", d, "total_venda", 2000+1200+50)
	e.wantNum("venda", d, "lucro", 2000+1200+50-1200) // serviço sem custo da venda: só a pedra

	for name, body := range map[string]map[string]any{
		"produto_id":    {"produto_id": prod},
		"origin":        {"origin": "MADE_TO_ORDER"},
		"fornecedor_id": {"fornecedor_id": sup},
		"custo_unit":    {"custo_unit": 10},
	} {
		b := map[string]any{"kind": "SERVICE", "produto_nome": "Inst", "quantidade": 1, "preco_venda_unit": 10}
		for k, v := range body {
			b[k] = v
		}
		if code, res := e.api.do("POST", base, b); code != 400 {
			t.Errorf("SERVICE com %s = %d %v, want 400", name, code, res)
		}
	}
	if code, _ := e.api.do("POST", base, map[string]any{"produto_nome": "Pedra", "quantidade": 1, "preco_venda_unit": 10, "service_id": svc}); code != 400 {
		t.Errorf("PRODUCT com service_id = %d, want 400", code)
	}
	if code, _ := e.api.do("POST", base, map[string]any{"kind": "SERVICE", "service_id": "00000000-0000-0000-0000-000000000000", "produto_nome": "x", "quantidade": 1, "preco_venda_unit": 1}); code != 404 {
		t.Errorf("service_id inexistente = %d, want 404", code)
	}
	if code, _ := e.api.do("POST", base, map[string]any{"kind": "X", "produto_nome": "x", "quantidade": 1, "preco_venda_unit": 1}); code != 400 {
		t.Errorf("kind inválido = %d, want 400", code)
	}

	// PUT: ajusta quantidade/preço (kind igual é aceito); custo_unit 0 aceito; mudar kind = 409.
	itemPath := base + "/" + it["id"].(string)
	if code, res := e.putItem(sale, it["id"].(string), map[string]any{"kind": "SERVICE", "service_id": svc, "produto_nome": "Instalação", "quantidade": 5, "preco_venda_unit": 320, "custo_unit": 0, "origin": nil}); code != 200 || num(res, "preco_venda_unit") != 320 || res["origin"] != nil {
		t.Errorf("PUT serviço = %d %v", code, res)
	}
	if code, _ := e.api.do("PUT", itemPath, map[string]any{"kind": "PRODUCT", "produto_nome": "Instalação", "quantidade": 5, "preco_venda_unit": 320}); code != 409 {
		t.Errorf("PUT PRODUCT sobre SERVICE = %d, want 409", code)
	}
	if code, _ := e.api.do("PUT", base+"/"+ids[0], map[string]any{"kind": "SERVICE", "produto_nome": "Pedra", "quantidade": 2, "preco_venda_unit": 1000, "custo_unit": 600}); code != 409 {
		t.Errorf("PUT SERVICE sobre PRODUCT = %d, want 409", code)
	}
	for name, body := range map[string]map[string]any{
		"origin": {"origin": "FROM_STOCK"}, "fornecedor_id": {"fornecedor_id": sup}, "produto_id": {"produto_id": prod}, "custo_unit": {"custo_unit": 5},
	} {
		b := map[string]any{"produto_nome": "Instalação", "quantidade": 5, "preco_venda_unit": 320}
		for k, v := range body {
			b[k] = v
		}
		if code, res := e.api.do("PUT", itemPath, b); code != 400 {
			t.Errorf("PUT SERVICE com %s = %d %v, want 400", name, code, res)
		}
	}
	// PUT legado de PRODUCT segue funcionando sem kind.
	if code, res := e.putItem(sale, ids[0], map[string]any{"produto_nome": "Pedra", "quantidade": 3, "preco_venda_unit": 1000, "custo_unit": 600, "origin": "FROM_STOCK", "produto_id": prod}); code != 200 || res["kind"] != "PRODUCT" {
		t.Errorf("PUT PRODUCT legado = %d %v", code, res)
	}
}

func TestIntegration_RemovingServiceItemRemovesLinkedCost(t *testing.T) {
	e := newFinanceEnv(t)
	installer := e.supplier()
	q := e.post("/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{
		qItem("Pedra", 1, 1000, 600, ""), sItem("Instalação A", 10, 100, 40, ""), sItem("Instalação B", 5, 100, 40, "")}}, 201)
	sale := e.post("/quotes/"+q["id"].(string)+"/convert", nil, 201)["sale_id"].(string)
	_, d := e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("venda", d, "costs_total", 600) // 400 + 200
	itemOf := func(name string) string {
		for _, it := range d["itens"].([]any) {
			if m := it.(map[string]any); m["produto_nome"] == name {
				return m["id"].(string)
			}
		}
		t.Fatalf("Item %s não encontrado", name)
		return ""
	}
	a, b := itemOf("Instalação A"), itemOf("Instalação B")
	costOf := func(item string) string {
		for _, c := range d["composition"].(map[string]any)["costs"].([]any) {
			if m := c.(map[string]any); m["sale_item_id"] == item {
				return m["id"].(string)
			}
		}
		t.Fatalf("custo do Item %s não encontrado", item)
		return ""
	}
	costA, costB := costOf(a), costOf(b)
	// Mesmo credor: os dois custos viram uma conta; quitar parte dela trava a remoção dos dois.
	for _, id := range []string{costA, costB} {
		if code, res := e.api.do("PUT", "/vendas/"+sale+"/costs/"+id, map[string]any{"kind": "LABOR", "amount": map[string]float64{costA: 400, costB: 200}[id], "payee_supplier_id": installer, "sale_item_id": map[string]string{costA: a, costB: b}[id]}); code != 200 {
			t.Fatalf("PUT custo = %d %v", code, res)
		}
	}

	// Sem quitação: remover o Item remove o custo ligado e a Sale sai do custo dele.
	if code, res := e.api.do("DELETE", "/vendas/"+sale+"/itens/"+a, nil); code != 204 {
		t.Fatalf("DELETE sem quitação = %d %v", code, res)
	}
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("depois de remover A", d, "costs_total", 200)
	e.wantNum("depois de remover A", d, "total_venda", 1000+500)
	if n := len(d["composition"].(map[string]any)["costs"].([]any)); n != 1 {
		t.Errorf("custos = %d, want 1", n)
	}
	var linked int
	e.db.Raw(`SELECT COUNT(*) FROM sale_costs WHERE sale_item_id = ?`, a).Scan(&linked)
	if linked != 0 {
		t.Errorf("custo ainda ligado ao Item removido: %d", linked)
	}

	// Conta do credor com quitação: remover o Item de serviço → 409 e nada muda.
	acc := e.api.payableOf("SALE", sale, installer)
	if code, res := e.api.do("POST", "/obligations/"+acc+"/settle", map[string]any{"amount": 50,
		"movement": map[string]any{"occurred_on": "2026-11-01", "payment_method": "PIX"}}); code != 201 {
		t.Fatalf("pagar = %d %v", code, res)
	}
	if code, res := e.api.do("DELETE", "/vendas/"+sale+"/itens/"+b, nil); code != 409 {
		t.Errorf("DELETE com conta quitada = %d %v, want 409", code, res)
	}
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	e.wantNum("depois do 409", d, "costs_total", 200)
	if n := len(d["itens"].([]any)); n != 2 {
		t.Errorf("itens = %d, want 2", n)
	}

	// Remover um Item de pedra que tem custo ligado só desfaz o vínculo (não apaga o custo).
	pedra := itemByKind(t, d, "PRODUCT")["id"].(string)
	extra := e.post("/vendas/"+sale+"/costs", map[string]any{"kind": "OTHER", "amount": 10, "sale_item_id": pedra}, 201)
	if code, res := e.api.do("DELETE", "/vendas/"+sale+"/itens/"+pedra, nil); code != 204 {
		t.Fatalf("DELETE pedra = %d %v", code, res)
	}
	_, d = e.api.do("GET", "/vendas/"+sale, nil)
	found := false
	for _, c := range d["composition"].(map[string]any)["costs"].([]any) {
		if m := c.(map[string]any); m["id"] == extra["id"] {
			found = m["sale_item_id"] == nil
		}
	}
	if !found {
		t.Errorf("custo de pedra deveria permanecer sem vínculo: %v", d["composition"])
	}
}

func TestIntegration_ServicesReportAndProductRankingExclusion(t *testing.T) {
	e := newDashboardEnv(t)
	from, to, mid := uniquePeriod()
	svc := e.service(uniq("Instalação"))
	prod := e.product()

	// Duas vendas no período: 12 m² a R$ 300 (custo 240) e 8 m² a R$ 350 (custo 200) do serviço do
	// catálogo; consultoria em texto livre (R$ 2.100, custo 0); mais uma pedra.
	mk := func(items ...map[string]any) string {
		id, _ := e.sale(items...)
		e.db.Exec(`UPDATE vendas SET data_venda = ? WHERE id = ?`, mid, id)
		return id
	}
	mk(qItem("Rock face", 12, 400, 250, prod), sItem("Instalação", 12, 300, 240, svc))
	mk(sItem("Instalação", 8, 350, 200, svc), sItem("Consultoria", 1, 2100, 0, ""))
	canceled := mk(sItem("Instalação", 100, 300, 240, svc))
	e.api.do("PATCH", "/vendas/"+canceled+"/delivery-status", map[string]any{"delivery_status": "CANCELED"})

	code, res := e.api.do("GET", "/relatorios/servicos?from="+from+"&to="+to, nil)
	if code != 200 {
		t.Fatalf("GET /relatorios/servicos = %d %v", code, res)
	}
	dados := res["dados"].([]any)
	if len(dados) != 2 {
		t.Fatalf("dados = %v, want 2 linhas (catálogo + texto livre; cancelada fora)", dados)
	}
	// Ordem por receita: Instalação 12×300 + 8×350 = 6400; Consultoria 2100.
	inst, cons := dados[0].(map[string]any), dados[1].(map[string]any)
	if inst["service_id"] != svc || !strings.HasPrefix(inst["service_name"].(string), "Instalação") || inst["unit"] != "m²" {
		t.Errorf("linha do catálogo = %v", inst)
	}
	e.wantNum("Instalação", inst, "total_vendas", 2)
	e.wantNum("Instalação", inst, "total_quantidade", 20)
	e.wantNum("Instalação", inst, "total_receita", 6400)
	e.wantNum("Instalação", inst, "preco_medio", 320)
	e.wantNum("Instalação", inst, "custo_medio", (12*240+8*200)/20.0)
	e.wantNum("Instalação", inst, "margem_media_percent", (6400-4480)/6400.0*100)
	if cons["service_id"] != nil || cons["service_name"] != "Consultoria" || cons["unit"] != nil {
		t.Errorf("linha em texto livre = %v", cons)
	}
	e.wantNum("Consultoria", cons, "total_receita", 2100)
	e.wantNum("Consultoria", cons, "custo_medio", 0)
	e.wantNum("Consultoria", cons, "margem_media_percent", 100)

	if code, _ := e.api.do("GET", "/relatorios/servicos?from=x", nil); code != 400 {
		t.Errorf("from inválido = %d, want 400", code)
	}
	if code, _ := e.api.do("GET", "/relatorios/servicos?from="+to+"&to="+from, nil); code != 400 {
		t.Errorf("from > to = %d, want 400", code)
	}
	if code, res := e.api.do("GET", "/relatorios/servicos?from=2001-01-01&to=2001-01-02", nil); code != 200 || len(res["dados"].([]any)) != 0 {
		t.Errorf("período vazio = %d %v", code, res)
	}

	// O ranking de Products (RF-16) só tem a pedra.
	_, rank := e.api.do("GET", "/relatorios/produtos?from="+from+"&to="+to, nil)
	pd := rank["dados"].([]any)
	if len(pd) != 1 || pd[0].(map[string]any)["produto_id"] != prod {
		t.Errorf("ranking de Products = %v, want só a pedra", pd)
	}

	// KPIs e resumo financeiro incluem o serviço (faturamento e Profit).
	k := e.kpis("?from=" + from + "&to=" + to)
	sales := k["sales"].(map[string]any)
	e.wantNum("kpis.sales", sales, "count", 2)
	e.wantNum("kpis.sales", sales, "revenue", 4800+3600+2800+2100)
}
