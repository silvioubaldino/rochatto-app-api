package handlers

// Testes de integração do AYD-007 §5 / SPEC-008 — quitação (pagar/receber): um Pix para várias
// contas, categoria padrão, limites 400/409, MIXED_COUNTERPARTIES, movimentação já lançada e
// concorrência. Só rodam com TEST_DATABASE_URL.
import (
	"net/http"
	"sync"
	"testing"
)

// purchaseAccount cria uma Purchase de reposição de custo `cost` e devolve (purchaseID, contaID).
func (e fxEnv) purchaseAccount(sup string, cost float64) (string, string) {
	e.t.Helper()
	code, p := e.api.do("POST", "/purchases", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": sup,
		"items": []map[string]any{{"product_id": e.product(), "quantity": 1, "unit_cost": cost}}})
	if code != 201 {
		e.t.Fatalf("POST purchase = %d %v", code, p)
	}
	id := p["id"].(string)
	return id, e.api.payableOf("PURCHASE", id, sup)
}

func alloc(obligationID string, amount ...float64) map[string]any {
	a := map[string]any{"obligation_id": obligationID}
	if len(amount) > 0 {
		a["amount"] = amount[0]
	}
	return a
}

func mvIn(day string) map[string]any {
	return map[string]any{"occurred_on": day, "payment_method": "PIX"}
}

// Um Pix quita várias contas (dois pedidos ao mesmo fornecedor): 1 movimentação (−Σ) e N quitações.
func TestIntegration_SettlementOnePixManyAccounts(t *testing.T) {
	e := newFinanceEnv(t)
	sup := e.supplier()
	_, a := e.purchaseAccount(sup, 100)
	_, b := e.purchaseAccount(sup, 60)

	code, res := e.api.do("POST", "/settlements", map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(a), alloc(b)}})
	if code != 201 {
		t.Fatalf("POST /settlements = %d %v", code, res)
	}
	mv := res["movement"].(map[string]any)
	if num(mv, "amount") != -160 || mv["category"] != "PURCHASE_PAYMENT" || mv["payment_method"] != "PIX" || num(mv, "unallocated") != 0 || len(mv["settlements"].([]any)) != 2 {
		t.Errorf("movimentação = %v", mv)
	}
	obs := res["obligations"].([]any)
	if len(obs) != 2 {
		t.Fatalf("obligations = %v", obs)
	}
	for _, o := range obs {
		m := o.(map[string]any)
		if m["status"] != "SETTLED" || num(m, "open") != 0 {
			t.Errorf("conta após quitar = %v", m)
		}
	}
	if ws, ok := res["warnings"].([]any); !ok || len(ws) != 0 {
		t.Errorf("warnings = %v, want []", res["warnings"])
	}

	// Valores explícitos: quitação parcial de uma conta.
	_, c := e.purchaseAccount(sup, 500)
	code, res = e.api.do("POST", "/settlements", map[string]any{"movement": mvIn("2027-03-02"), "allocations": []map[string]any{alloc(c, 120.5)}})
	if mv = res["movement"].(map[string]any); code != 201 || num(mv, "amount") != -120.5 {
		t.Fatalf("parcial = %d %v", code, res)
	}
	if o := res["obligations"].([]any)[0].(map[string]any); o["status"] != "PARTIAL" || num(o, "open") != 379.5 {
		t.Errorf("conta parcial = %v", o)
	}
	// Desfazer a quitação não anula a movimentação.
	sid := mv["settlements"].([]any)[0].(map[string]any)["id"].(string)
	if code, _ := e.api.do("DELETE", "/settlements/"+sid, nil); code != 204 {
		t.Fatalf("DELETE settlement = %d", code)
	}
	_, list := e.api.do("GET", "/financial-movements?from=2027-03-02&to=2027-03-02&unallocated_only=true", nil)
	found := false
	for _, it := range list["items"].([]any) {
		if it.(map[string]any)["id"] == mv["id"] && num(it.(map[string]any), "unallocated") == 120.5 {
			found = true
		}
	}
	if !found {
		t.Errorf("movimentação deveria permanecer, sem alocação: %v", list)
	}
}

// Categoria padrão: RECEIVABLE → SALE_RECEIPT; compra → PURCHASE_PAYMENT; custo da venda → o tipo
// do custo (misto → OTHER_EXPENSE); categoria explícita precisa combinar com o sinal.
func TestIntegration_SettlementDefaultCategory(t *testing.T) {
	e := newFinanceEnv(t)
	partner := e.partner()
	sale := e.simpleSale(1, 1000, 400)
	e.api.do("PUT", "/vendas/"+sale, map[string]any{"frete_valor": 100, "comissao_valor": 50, "vendedor_externo_id": partner})
	freight := e.api.payableOf("SALE", sale, "")
	commission := e.api.payableOf("SALE", sale, partner)

	catOf := func(allocs ...map[string]any) string {
		t.Helper()
		code, res := e.api.do("POST", "/settlements", map[string]any{"movement": mvIn("2027-03-01"), "allocations": allocs})
		if code != 201 {
			t.Fatalf("POST /settlements = %d %v", code, res)
		}
		return res["movement"].(map[string]any)["category"].(string)
	}
	if got := catOf(alloc(e.api.receivable(sale), 10)); got != "SALE_RECEIPT" {
		t.Errorf("receber = %s", got)
	}
	if got := catOf(alloc(freight, 10)); got != "FREIGHT" {
		t.Errorf("frete = %s", got)
	}
	if got := catOf(alloc(commission, 10)); got != "COMMISSION" {
		t.Errorf("comissão = %s", got)
	}
	if got := catOf(alloc(freight, 5), alloc(commission, 5)); got != "OTHER_EXPENSE" {
		t.Errorf("frete + comissão = %s, want OTHER_EXPENSE (misto)", got)
	}
	// Frete e comissão SEM Partner na mesma conta → misto.
	sale2 := e.simpleSale(1, 1000, 400)
	e.api.do("PUT", "/vendas/"+sale2, map[string]any{"frete_valor": 100, "comissao_valor": 50})
	if got := catOf(alloc(e.api.payableOf("SALE", sale2, ""), 10)); got != "OTHER_EXPENSE" {
		t.Errorf("conta sem credor com frete e comissão = %s, want OTHER_EXPENSE", got)
	}
	_, pacc := e.purchaseAccount(e.supplier(), 100)
	if got := catOf(alloc(pacc, 10)); got != "PURCHASE_PAYMENT" {
		t.Errorf("compra = %s", got)
	}
	// Categoria explícita: vale se combinar com o sinal.
	code, res := e.api.do("POST", "/settlements", map[string]any{"movement": map[string]any{"occurred_on": "2027-03-01", "category": "LABOR"}, "allocations": []map[string]any{alloc(freight, 5)}})
	if code != 201 || res["movement"].(map[string]any)["category"] != "LABOR" {
		t.Errorf("categoria explícita = %d %v", code, res)
	}
	for name, body := range map[string]map[string]any{
		"saída com categoria de entrada": {"movement": map[string]any{"occurred_on": "2027-03-01", "category": "OTHER_INCOME"}, "allocations": []map[string]any{alloc(freight, 5)}},
		"entrada com categoria de saída": {"movement": map[string]any{"occurred_on": "2027-03-01", "category": "VEHICLE"}, "allocations": []map[string]any{alloc(e.api.receivable(sale), 5)}},
		"categoria inválida":             {"movement": map[string]any{"occurred_on": "2027-03-01", "category": "X"}, "allocations": []map[string]any{alloc(freight, 5)}},
	} {
		if code, res := e.api.do("POST", "/settlements", body); code != 400 {
			t.Errorf("%s = %d %v, want 400", name, code, res)
		}
	}
}

func TestIntegration_SettlementErrors(t *testing.T) {
	e := newFinanceEnv(t)
	sup := e.supplier()
	_, a := e.purchaseAccount(sup, 100)
	sale := e.simpleSale(1, 1000, 400)
	recv := e.api.receivable(sale)
	missing := "00000000-0000-0000-0000-000000000000"
	post := func(body map[string]any) (int, map[string]any) { return e.api.do("POST", "/settlements", body) }

	for name, body := range map[string]map[string]any{
		"allocations vazio":            {"movement": mvIn("2027-03-01"), "allocations": []map[string]any{}},
		"sem allocations":              {"movement": mvIn("2027-03-01")},
		"amount zero":                  {"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(a, 0)}},
		"amount negativo":              {"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(a, -1)}},
		"direções diferentes":          {"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(a), alloc(recv)}},
		"sem movement nem movement_id": {"allocations": []map[string]any{alloc(a)}},
		"movement e movement_id":       {"movement_id": missing, "movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(a)}},
		"sem occurred_on":              {"movement": map[string]any{"payment_method": "PIX"}, "allocations": []map[string]any{alloc(a)}},
		"payment_method inválido":      {"movement": map[string]any{"occurred_on": "2027-03-01", "payment_method": "CHEQUE"}, "allocations": []map[string]any{alloc(a)}},
		"obligation_id inválido":       {"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc("abc")}},
		"conta repetida":               {"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(a, 10), alloc(a, 10)}},
		"movement_id inválido":         {"movement_id": "abc", "allocations": []map[string]any{alloc(a)}},
	} {
		if code, res := post(body); code != http.StatusBadRequest {
			t.Errorf("%s = %d %v, want 400", name, code, res)
		}
	}
	if code, res := post(map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(missing)}}); code != 404 {
		t.Errorf("conta inexistente = %d %v, want 404", code, res)
	}
	if code, res := post(map[string]any{"movement_id": missing, "allocations": []map[string]any{alloc(a)}}); code != 404 {
		t.Errorf("movimentação inexistente = %d %v, want 404", code, res)
	}

	// 409: acima do devido (mensagem do AYD); uma alocação ruim não grava nenhuma das outras.
	code, res := post(map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(a, 100.01)}})
	if code != http.StatusConflict || res["error"] != "valor acima do devido" {
		t.Errorf("acima do devido = %d %v", code, res)
	}
	_, b := e.purchaseAccount(sup, 50)
	if code, _ := post(map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(b, 10), alloc(a, 500)}}); code != 409 {
		t.Errorf("uma alocação acima do devido = %d, want 409", code)
	}
	if o := e.api.obligation(b); num(o, "settled") != 0 {
		t.Errorf("a falha deveria desfazer tudo (transação): %v", o["settled"])
	}
	// Conta já quitada: nada a pagar (409).
	if code, _ := post(map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(b)}}); code != 201 {
		t.Fatalf("quitar b = %d", code)
	}
	if code, res := post(map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(b)}}); code != 409 {
		t.Errorf("quitar conta quitada = %d %v, want 409", code, res)
	}
	// Venda cancelada.
	e.api.do("PATCH", "/vendas/"+sale+"/delivery-status", map[string]any{"delivery_status": "CANCELED"})
	code, res = post(map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(recv, 10)}})
	if code != 409 || res["error"] != "venda cancelada: recebimentos bloqueados" {
		t.Errorf("venda cancelada = %d %v", code, res)
	}
	// Atalho de 1 conta.
	if code, _ := e.api.do("POST", "/obligations/"+a+"/settle", map[string]any{"amount": 10}); code != 400 {
		t.Errorf("settle sem movement = %d, want 400", code)
	}
	if code, _ := e.api.do("DELETE", "/settlements/"+missing, nil); code != 404 {
		t.Errorf("DELETE settlement inexistente = %d, want 404", code)
	}
	if code, _ := e.api.do("DELETE", "/settlements/abc", nil); code != 400 {
		t.Errorf("DELETE settlement id inválido = %d, want 400", code)
	}
}

// Contas de credores diferentes na mesma movimentação: permitido (201) com warning no corpo.
func TestIntegration_SettlementMixedCounterparties(t *testing.T) {
	e := newFinanceEnv(t)
	_, a := e.purchaseAccount(e.supplier(), 100)
	_, b := e.purchaseAccount(e.supplier(), 60)
	code, res := e.api.do("POST", "/settlements", map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(a), alloc(b)}})
	if code != http.StatusCreated || !hasWarning(res, "MIXED_COUNTERPARTIES") {
		t.Fatalf("credores diferentes = %d %v, want 201 + MIXED_COUNTERPARTIES", code, res)
	}
	if mv := res["movement"].(map[string]any); num(mv, "amount") != -160 {
		t.Errorf("movimentação = %v", mv)
	}
	// Mesmo credor: sem warning.
	sup := e.supplier()
	_, c := e.purchaseAccount(sup, 10)
	_, d := e.purchaseAccount(sup, 10)
	if code, res = e.api.do("POST", "/settlements", map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(c), alloc(d)}}); code != 201 || hasWarning(res, "MIXED_COUNTERPARTIES") {
		t.Errorf("mesmo credor = %d %v", code, res)
	}
}

// movement_id: usa a movimentação já lançada, respeitando sinal/direção e o saldo livre.
func TestIntegration_SettlementExistingMovement(t *testing.T) {
	e := newFinanceEnv(t)
	sup := e.supplier()
	_, a := e.purchaseAccount(sup, 1000)
	mv := e.api.movement(map[string]any{"occurred_on": "2027-03-01", "amount": -500, "category": "PURCHASE_PAYMENT", "payment_method": "PIX"})
	mid := mv["id"].(string)
	post := func(allocs ...map[string]any) (int, map[string]any) {
		return e.api.do("POST", "/settlements", map[string]any{"movement_id": mid, "allocations": allocs})
	}

	code, res := post(alloc(a, 200))
	if m := res["movement"].(map[string]any); code != 201 || m["id"] != mid || num(m, "unallocated") != 300 || len(m["settlements"].([]any)) != 1 {
		t.Fatalf("usar movimentação = %d %v", code, res)
	}
	if code, res = post(alloc(a, 300.01)); code != 409 {
		t.Errorf("Σ acima do saldo livre = %d %v, want 409", code, res)
	}
	if code, res = post(alloc(a, 300)); code != 201 || num(res["movement"].(map[string]any), "unallocated") != 0 {
		t.Errorf("usar o resto = %d %v", code, res)
	}
	if code, _ = post(alloc(a, 1)); code != 409 {
		t.Errorf("movimentação sem saldo livre = %d, want 409", code)
	}
	// Direção incompatível: movimentação de saída para conta a receber → 400.
	recv := e.api.receivable(e.simpleSale(1, 100, 50))
	mv2 := e.api.movement(map[string]any{"occurred_on": "2027-03-01", "amount": -50, "category": "VEHICLE"})
	if code, res := e.api.do("POST", "/settlements", map[string]any{"movement_id": mv2["id"], "allocations": []map[string]any{alloc(recv, 10)}}); code != 400 {
		t.Errorf("saída quitando conta a receber = %d %v, want 400", code, res)
	}
	// Entrada avulsa (OTHER_INCOME) pode quitar conta a receber.
	mv3 := e.api.movement(map[string]any{"occurred_on": "2027-03-01", "amount": 100, "category": "OTHER_INCOME"})
	if code, res := e.api.do("POST", "/settlements", map[string]any{"movement_id": mv3["id"], "allocations": []map[string]any{alloc(recv)}}); code != 201 {
		t.Errorf("entrada quitando conta a receber = %d %v", code, res)
	}
}

// Duas quitações simultâneas da mesma conta nunca passam do `open` (contas travadas FOR UPDATE).
func TestIntegration_SettlementConcurrencyNeverExceedsOpen(t *testing.T) {
	e := newFinanceEnv(t)
	_, acc := e.purchaseAccount(e.supplier(), 100)

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = e.api.do("POST", "/settlements", map[string]any{"movement": mvIn("2027-03-01"), "allocations": []map[string]any{alloc(acc, 100)}})
		}(i)
	}
	wg.Wait()
	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case 201:
			created++
		case 409:
			conflicts++
		}
	}
	if created != 1 || conflicts != n-1 {
		t.Errorf("201=%d 409=%d (códigos %v), want 1 e %d", created, conflicts, codes, n-1)
	}
	if o := e.api.obligation(acc); num(o, "settled") != 100 || num(o, "open") != 0 || hasWarning(o, "OVERSETTLED") {
		t.Errorf("conta após a disputa = %v", o)
	}
}
