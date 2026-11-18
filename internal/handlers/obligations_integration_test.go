package handlers

// Testes de integração do AYD-007 / SPEC-008 — contas (Obligation): manutenção automática a partir
// dos componentes (§2, §7a), leitura, prazo e cancelamento. Só rodam com TEST_DATABASE_URL (banco
// descartável com as migrations aplicadas; os dados não são limpos — os testes filtram por origem).
import (
	"testing"
)

func (c apiClient) obligation(id string) map[string]any {
	c.t.Helper()
	code, res := c.do("GET", "/obligations/"+id, nil)
	if code != 200 {
		c.t.Fatalf("GET /obligations/%s = %d %v", id, code, res)
	}
	return res
}

func componentTypes(o map[string]any) map[string]float64 {
	out := map[string]float64{}
	for _, c := range o["components"].([]any) {
		m := c.(map[string]any)
		out[m["type"].(string)] = num(m, "amount")
	}
	return out
}

// Sale com Items 1000, frete 150 e comissão 80 (Partner) → 3 contas; Purchase de 17995 → 1 conta.
func TestIntegration_ObligationsMaintainedFromComponents(t *testing.T) {
	e := newFinanceEnv(t)
	partner := e.partner()
	sale, items := e.sale(qItem("Pedra", 2, 500, 200, ""))
	if code, res := e.api.do("PUT", "/vendas/"+sale, map[string]any{"frete_valor": 150, "comissao_valor": 80, "vendedor_externo_id": partner}); code != 200 {
		t.Fatalf("PUT venda = %d %v", code, res)
	}

	list := e.api.obligationsOf("SALE", sale)
	if len(list) != 3 {
		t.Fatalf("contas da venda = %d, want 3: %v", len(list), list)
	}
	recv := e.api.obligation(e.api.receivable(sale))
	cp := recv["counterparty"].(map[string]any)
	if recv["direction"] != "RECEIVABLE" || num(recv, "amount") != 1000 || num(recv, "open") != 1000 || recv["status"] != "OPEN" ||
		cp["type"] != "CUSTOMER" || cp["id"] != e.cid || componentTypes(recv)["SALE_ITEMS"] != 1000 {
		t.Errorf("conta a receber = %v", recv)
	}
	if origin := recv["origin"].(map[string]any); origin["type"] != "SALE" || origin["id"] != sale || origin["label"] == nil || origin["label"] == "" {
		t.Errorf("origin = %v", origin)
	}
	freight := e.api.obligation(e.api.payableOf("SALE", sale, ""))
	if num(freight, "amount") != 150 || freight["counterparty"] != nil || componentTypes(freight)["LEGACY_FREIGHT"] != 150 {
		t.Errorf("conta do frete = %v", freight)
	}
	comm := e.api.obligation(e.api.payableOf("SALE", sale, partner))
	if num(comm, "amount") != 80 || comm["counterparty"].(map[string]any)["type"] != "PARTNER" || componentTypes(comm)["LEGACY_COMMISSION"] != 80 {
		t.Errorf("conta da comissão = %v", comm)
	}

	// Editar frete/itens recalcula sem ação manual; sem Partner a comissão cai na conta "a definir"
	// junto com o frete (única por origem + credor).
	e.api.do("PUT", "/vendas/"+sale, map[string]any{"frete_valor": 200, "comissao_valor": 80})
	merged := e.api.obligation(e.api.payableOf("SALE", sale, ""))
	if ct := componentTypes(merged); num(merged, "amount") != 280 || ct["LEGACY_FREIGHT"] != 200 || ct["LEGACY_COMMISSION"] != 80 || len(ct) != 2 {
		t.Errorf("conta sem credor após editar = %v", merged)
	}
	if n := len(e.api.obligationsOf("SALE", sale, "counterparty_type=PARTNER")); n != 0 {
		t.Errorf("conta do Partner deveria sumir (sem componentes e sem quitações): %d", n)
	}
	if code, res := e.putItem(sale, items[0], map[string]any{"produto_nome": "Pedra", "quantidade": 3, "preco_venda_unit": 500, "custo_unit": 200}); code != 200 {
		t.Fatalf("PUT item = %d %v", code, res)
	}
	if got := e.api.obligation(e.api.receivable(sale)); num(got, "amount") != 1500 {
		t.Errorf("conta a receber após editar o Item = %v, want 1500", got["amount"])
	}
	// Item novo e remoção também recalculam.
	e.api.do("POST", "/vendas/"+sale+"/itens", map[string]any{"produto_nome": "Extra", "quantidade": 1, "preco_venda_unit": 100, "custo_unit": 50})
	if got := e.api.obligation(e.api.receivable(sale)); num(got, "amount") != 1600 {
		t.Errorf("conta a receber após novo Item = %v, want 1600", got["amount"])
	}
	// Frete e comissão a 0: a conta sem componentes e sem quitações some.
	e.api.do("PUT", "/vendas/"+sale, map[string]any{"frete_valor": 0, "comissao_valor": 0})
	if n := len(e.api.obligationsOf("SALE", sale, "direction=PAYABLE")); n != 0 {
		t.Errorf("contas a pagar sem componentes deveriam sumir: %d", n)
	}

	// Purchase de 17995 (reposição de Stock) → 1 conta a pagar do Supplier.
	sup, pid := e.supplier(), e.product()
	code, p := e.api.do("POST", "/purchases", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": sup,
		"items": []map[string]any{{"product_id": pid, "quantity": 5, "unit_cost": 3000}, {"product_id": pid, "quantity": 1, "unit_cost": 2995}}})
	if code != 201 || num(p, "total_cost") != 17995 {
		t.Fatalf("POST purchase = %d %v", code, p)
	}
	purchase := p["id"].(string)
	if pl := e.api.obligationsOf("PURCHASE", purchase); len(pl) != 1 {
		t.Fatalf("contas da compra = %d, want 1", len(pl))
	}
	po := e.api.obligation(e.api.payableOf("PURCHASE", purchase, sup))
	if num(po, "amount") != 17995 || po["counterparty"].(map[string]any)["type"] != "SUPPLIER" || componentTypes(po)["PURCHASE_ITEMS"] != 17995 {
		t.Errorf("conta da compra = %v", po)
	}
	if lbl := po["origin"].(map[string]any)["label"].(string); len(lbl) < 8 || lbl[:6] != "Compra" {
		t.Errorf("label da compra = %q", lbl)
	}
	// Editar as linhas recalcula; a compra reflete pago/a pagar das contas.
	lines := p["items"].([]any)
	e.api.do("DELETE", "/purchases/"+purchase+"/items/"+lines[1].(map[string]any)["id"].(string), nil)
	if got := e.api.obligation(po["id"].(string)); num(got, "amount") != 15000 {
		t.Errorf("conta da compra após remover a linha = %v, want 15000", got["amount"])
	}
	_, pr := e.api.do("GET", "/purchases/"+purchase, nil)
	if num(pr, "payable") != 15000 || num(pr, "paid") != 0 || pr["payment_status"] != "PENDING" {
		t.Errorf("compra = %v", pr)
	}
}

// Troca de Partner: o valor muda de conta; se a antiga já tinha quitação maior que o novo valor,
// OVERSETTLED. Cancelar a Sale zera as contas, mas as quitações continuam visíveis.
func TestIntegration_ObligationPartnerSwapAndCancel(t *testing.T) {
	e := newFinanceEnv(t)
	oldPartner, newPartner := e.partner(), e.partner()
	sale := e.simpleSale(1, 1000, 400)
	if code, res := e.api.do("PUT", "/vendas/"+sale, map[string]any{"comissao_valor": 80, "vendedor_externo_id": oldPartner}); code != 200 {
		t.Fatalf("PUT = %d %v", code, res)
	}
	oldAcc := e.api.payableOf("SALE", sale, oldPartner)
	if code, res := e.api.do("POST", "/obligations/"+oldAcc+"/settle", map[string]any{"movement": map[string]any{"occurred_on": "2026-11-01", "payment_method": "PIX"}}); code != 201 {
		t.Fatalf("pagar comissão = %d %v", code, res)
	}

	code, res := e.api.do("PUT", "/vendas/"+sale, map[string]any{"comissao_valor": 80, "vendedor_externo_id": newPartner})
	if code != 200 || !hasWarning(res, "OVERSETTLED") {
		t.Fatalf("trocar Partner = %d %v, want 200 com warnings OVERSETTLED", code, res)
	}
	old := e.api.obligation(oldAcc)
	if num(old, "amount") != 0 || num(old, "settled") != 80 || num(old, "open") != 0 || !hasWarning(old, "OVERSETTLED") || len(old["settlements"].([]any)) != 1 {
		t.Errorf("conta antiga = %v", old)
	}
	fresh := e.api.obligation(e.api.payableOf("SALE", sale, newPartner))
	if num(fresh, "amount") != 80 || num(fresh, "open") != 80 || hasWarning(fresh, "OVERSETTLED") {
		t.Errorf("conta nova = %v", fresh)
	}

	// Cancelar: amount 0 em todas as contas; quitações seguem visíveis; sai dos saldos.
	e.api.receive(sale, 300, "2026-11-02", "PIX")
	before := e.balances()
	if code, _ := e.api.do("PATCH", "/vendas/"+sale+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatal("cancelar")
	}
	for _, it := range e.api.obligationsOf("SALE", sale) {
		o := it.(map[string]any)
		if num(o, "amount") != 0 || num(o, "open") != 0 {
			t.Errorf("conta de venda cancelada = %v, want amount 0", o)
		}
	}
	recv := e.api.obligation(e.api.receivable(sale))
	if num(recv, "settled") != 300 || len(recv["settlements"].([]any)) != 1 {
		t.Errorf("quitação da venda cancelada deveria seguir visível: %v", recv)
	}
	after := e.balances()
	if d := after.receivable - before.receivable; !near(d, -700) {
		t.Errorf("Δ receivable ao cancelar = %v, want -700", d)
	}
	if d := after.payable - before.payable; !near(d, -80) {
		t.Errorf("Δ payable ao cancelar = %v, want -80", d)
	}
}

func TestIntegration_ObligationsListFiltersTotalsAndPaging(t *testing.T) {
	e := newFinanceEnv(t)
	partner := e.partner()
	sale := e.simpleSale(1, 1000, 400)
	e.api.do("PUT", "/vendas/"+sale, map[string]any{"frete_valor": 100, "comissao_valor": 50, "vendedor_externo_id": partner})
	recv := e.api.receivable(sale)
	e.api.receive(sale, 250, "2026-11-01", "PIX")
	freight := e.api.payableOf("SALE", sale, "")
	e.api.do("PATCH", "/obligations/"+freight, map[string]any{"due_date": dayStr(-3)})

	get := func(q string) map[string]any {
		t.Helper()
		code, res := e.api.do("GET", "/obligations?origin_type=SALE&origin_id="+sale+"&"+q, nil)
		if code != 200 {
			t.Fatalf("GET ?%s = %d %v", q, code, res)
		}
		return res
	}
	ids := func(res map[string]any) map[string]bool {
		out := map[string]bool{}
		for _, it := range res["items"].([]any) {
			out[it.(map[string]any)["id"].(string)] = true
		}
		return out
	}

	all := get("")
	if num(all, "total") != 3 || all["has_more"] != false || num(all, "offset") != 0 {
		t.Errorf("paginação = %v", all)
	}
	tot := all["totals"].(map[string]any)
	e.wantNum("totals", tot, "amount", 1150)
	e.wantNum("totals", tot, "settled", 250)
	e.wantNum("totals", tot, "open", 900)

	if got := ids(get("direction=RECEIVABLE")); len(got) != 1 || !got[recv] {
		t.Errorf("direction = %v", got)
	}
	if got := ids(get("direction=PAYABLE")); len(got) != 2 {
		t.Errorf("direction=PAYABLE = %v", got)
	}
	if got := ids(get("status=PARTIAL")); len(got) != 1 || !got[recv] {
		t.Errorf("status=PARTIAL = %v", got)
	}
	if got := ids(get("status=OPEN,PARTIAL")); len(got) != 3 {
		t.Errorf("status=OPEN,PARTIAL = %v", got)
	}
	if got := ids(get("status=SETTLED")); len(got) != 0 {
		t.Errorf("status=SETTLED = %v", got)
	}
	if got := ids(get("counterparty_type=PARTNER&counterparty_id=" + partner)); len(got) != 1 {
		t.Errorf("counterparty = %v", got)
	}
	if got := ids(get("counterparty_type=CUSTOMER")); len(got) != 1 || !got[recv] {
		t.Errorf("counterparty_type=CUSTOMER = %v", got)
	}
	if got := ids(get("overdue=true")); len(got) != 1 || !got[freight] {
		t.Errorf("overdue=true = %v (a conta com prazo de 3 dias atrás)", got)
	}
	if got := ids(get("overdue=false")); len(got) != 2 {
		t.Errorf("overdue=false = %v", got)
	}
	if got := ids(get("due_to=" + dayStr(0))); len(got) != 1 || !got[freight] {
		t.Errorf("due_to = %v", got)
	}

	// limit/offset/has_more: a conta mais próxima do vencimento vem primeiro.
	page := get("limit=2")
	if len(page["items"].([]any)) != 2 || page["has_more"] != true || num(page, "total") != 3 {
		t.Errorf("limit=2 = %v", page)
	}
	if first := page["items"].([]any)[0].(map[string]any); first["id"] != freight {
		t.Errorf("ordem por vencimento: primeiro = %v", first["id"])
	}
	page = get("limit=2&offset=2")
	if len(page["items"].([]any)) != 1 || page["has_more"] != false || num(page, "offset") != 2 {
		t.Errorf("offset=2 = %v", page)
	}

	for _, q := range []string{"direction=X", "status=OPEN,NOPE", "counterparty_type=X", "counterparty_id=abc", "origin_type=X",
		"origin_id=abc", "overdue=talvez", "due_to=amanha", "limit=0", "limit=999", "offset=-1"} {
		if code, _ := e.api.do("GET", "/obligations?"+q, nil); code != 400 {
			t.Errorf("?%s = %d, want 400", q, code)
		}
	}
}

func TestIntegration_ObligationPatchAndNotFound(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 400)
	acc := e.api.receivable(sale)

	code, d := e.api.do("PATCH", "/obligations/"+acc, map[string]any{"due_date": "2027-04-09", "notes": "combinado no WhatsApp"})
	if code != 200 || d["due_date"] != "2027-04-09" || d["notes"] != "combinado no WhatsApp" || num(d, "amount") != 1000 {
		t.Fatalf("PATCH = %d %v", code, d)
	}
	// Só os campos enviados mudam; null limpa.
	code, d = e.api.do("PATCH", "/obligations/"+acc, map[string]any{"notes": nil})
	if code != 200 || d["notes"] != nil || d["due_date"] != "2027-04-09" {
		t.Errorf("limpar notes = %d %v", code, d)
	}
	code, d = e.api.do("PATCH", "/obligations/"+acc, map[string]any{"due_date": nil})
	if code != 200 || d["due_date"] != nil {
		t.Errorf("limpar due_date = %d %v", code, d)
	}
	// Valor, direção e quitado não são editáveis por aqui.
	code, d = e.api.do("PATCH", "/obligations/"+acc, map[string]any{"amount": 1, "settled": 999, "direction": "PAYABLE"})
	if code != 200 || num(d, "amount") != 1000 || d["direction"] != "RECEIVABLE" || num(d, "settled") != 0 {
		t.Errorf("campos calculados mudaram: %d %v", code, d)
	}
	if code, _ := e.api.do("PATCH", "/obligations/"+acc, map[string]any{"due_date": "09/04/2027"}); code != 400 {
		t.Errorf("data inválida = %d, want 400", code)
	}
	if code, _ := e.api.do("PATCH", "/obligations/abc", map[string]any{}); code != 400 {
		t.Errorf("id inválido = %d, want 400", code)
	}
	missing := "00000000-0000-0000-0000-000000000000"
	if code, _ := e.api.do("PATCH", "/obligations/"+missing, map[string]any{"notes": "x"}); code != 404 {
		t.Errorf("PATCH inexistente = %d, want 404", code)
	}
	if code, _ := e.api.do("GET", "/obligations/"+missing, nil); code != 404 {
		t.Errorf("GET inexistente = %d, want 404", code)
	}
	for _, c := range []string{"PUT /obligations/" + missing + "/installments", "POST /obligations/" + missing + "/installments/generate", "POST /obligations/" + missing + "/settle"} {
		var method, path string
		for i := range c {
			if c[i] == ' ' {
				method, path = c[:i], c[i+1:]
			}
		}
		body := map[string]any{"installments": []any{}, "count": 2, "first_due_date": "2027-01-01", "movement": map[string]any{"occurred_on": "2027-01-01"}}
		if code, _ := e.api.do(method, path, body); code != 404 {
			t.Errorf("%s = %d, want 404", c, code)
		}
	}
}

// AYD-007 §1: Supplier ganha kind (STONE por padrão), validado e filtrável.
func TestIntegration_SupplierKind(t *testing.T) {
	e := newFinanceEnv(t)
	NewFornecedoresHandler(e.db).Register(e.api.mux)

	code, s := e.api.do("POST", "/fornecedores", map[string]any{"nome": uniq("Pedreira")})
	if code != 201 || s["kind"] != "STONE" {
		t.Fatalf("sem kind = %d %v, want STONE", code, s)
	}
	code, tr := e.api.do("POST", "/fornecedores", map[string]any{"nome": uniq("Transportadora"), "kind": "TRANSPORT"})
	if code != 201 || tr["kind"] != "TRANSPORT" {
		t.Fatalf("TRANSPORT = %d %v", code, tr)
	}
	if code, _ := e.api.do("POST", "/fornecedores", map[string]any{"nome": "X", "kind": "OUTRO"}); code != 400 {
		t.Errorf("kind inválido no POST = %d, want 400", code)
	}
	id := tr["id"].(string)
	if code, _ := e.api.do("PUT", "/fornecedores/"+id, map[string]any{"nome": "Y", "kind": "NOPE"}); code != 400 {
		t.Errorf("kind inválido no PUT = %d, want 400", code)
	}
	code, up := e.api.do("PUT", "/fornecedores/"+id, map[string]any{"nome": tr["nome"], "kind": "LABOR"})
	if code != 200 || up["kind"] != "LABOR" {
		t.Errorf("PUT kind = %d %v", code, up)
	}
	code, got := e.api.do("GET", "/fornecedores/"+id, nil)
	if code != 200 || got["kind"] != "LABOR" {
		t.Errorf("GET = %v", got)
	}
	code, list := e.api.list("/fornecedores?kind=LABOR")
	if code != 200 || !containsID(list, id) {
		t.Errorf("filtro kind=LABOR = %d %v", code, list)
	}
	for _, r := range list {
		if r["kind"] != "LABOR" {
			t.Errorf("filtro kind trouxe %v", r["kind"])
		}
	}
	if _, list := e.api.list("/fornecedores?kind=TAX_AUTHORITY"); containsID(list, id) {
		t.Error("filtro kind=TAX_AUTHORITY não deveria trazer o LABOR")
	}
	if code, _ := e.api.list("/fornecedores?kind=X"); code != 400 {
		t.Errorf("filtro inválido = %d, want 400", code)
	}
	// Fornecedor existente antes da migração (sem kind informado em SQL) fica STONE.
	var kind string
	e.db.Raw(`INSERT INTO fornecedores (nome) VALUES (?) RETURNING kind::text`, uniq("Legado")).Scan(&kind)
	if kind != "STONE" {
		t.Errorf("default do banco = %s, want STONE", kind)
	}
}
