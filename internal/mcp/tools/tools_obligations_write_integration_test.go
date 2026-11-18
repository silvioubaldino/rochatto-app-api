package tools_test

// Testes de integração das 3 ferramentas de escrita novas do catálogo 1.0.0 (AYD-007 §8 /
// SPEC-008): settle_obligations, record_expense e set_installments, pelo pipeline real, sobre os
// handlers REST de verdade, num Postgres descartável (TEST_DATABASE_URL).
import (
	"testing"
	"time"
)

// purchaseAccount cria uma Purchase de reposição (custo cost) e devolve o id da conta do Supplier.
func (e *env) purchaseAccount(supplier, product string, cost float64) string {
	e.t.Helper()
	p := e.mustREST("POST", "/purchases", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": supplier,
		"items": []map[string]any{{"product_id": product, "quantity": 1, "unit_cost": cost}}}, 201)
	list := e.mustREST("GET", "/obligations?origin_type=PURCHASE&origin_id="+p["id"].(string), nil, 200)["items"].([]any)
	return obj(list[0])["id"].(string)
}

func (e *env) movementCount(category string) (n int64) {
	e.db.Raw(`SELECT COUNT(*) FROM financial_movements WHERE category::text = ? AND deleted_at IS NULL`, category).Scan(&n)
	return
}

func TestIntegration_WriteSettleObligations(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture()
	a := e.purchaseAccount(f.supplier, f.product, 100)
	b := e.purchaseAccount(f.supplier, f.product2, 60)
	today := time.Now().In(sp()).Format("2006-01-02")

	// Um Pix quita duas contas (sem valor: o que falta); data padrão = hoje; resposta com movimentação e contas.
	out := e.ok("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": a}, {"obligation_id": b}}, "payment_method": "PIX", "description": "Pix do dia"})
	mv := obj(out["movement"])
	if mv["amount"] != float64(-160) || mv["category"] != "PURCHASE_PAYMENT" || mv["payment_method"] != "PIX" || mv["occurred_on"] != today ||
		mv["description"] != "Pix do dia" || mv["unallocated"] != float64(0) || len(mv["settlements"].([]any)) != 2 {
		t.Fatalf("movimentação = %v", mv)
	}
	obs := out["obligations"].([]any)
	if len(obs) != 2 || obj(obs[0])["status"] != "SETTLED" || obj(obs[0])["open"] != float64(0) || obj(obs[1])["status"] != "SETTLED" {
		t.Errorf("obligations = %v", obs)
	}
	if w := out["warnings"].([]any); len(w) != 0 || out["deduplicated"] != nil {
		t.Errorf("warnings=%v deduplicated=%v", w, out["deduplicated"])
	}

	// Deduplicação (120 s) e allow_duplicate; valor parcial explícito e data informada.
	c := e.purchaseAccount(f.supplier, f.product, 500)
	args := map[string]any{"allocations": []map[string]any{{"obligation_id": c, "amount": 10}}, "occurred_on": "2026-11-10", "payment_method": "CASH"}
	first := e.ok("settle_obligations", args)
	if m := obj(first["movement"]); m["amount"] != float64(-10) || m["occurred_on"] != "2026-11-10" || m["payment_method"] != "CASH" {
		t.Fatalf("1ª = %v", first)
	}
	dup := e.ok("settle_obligations", args)
	if dup["deduplicated"] != true || obj(dup["movement"])["id"] != obj(first["movement"])["id"] || e.movementCount("PURCHASE_PAYMENT") != 2 {
		t.Fatalf("2ª deveria deduplicar: %v (movimentações %d)", dup, e.movementCount("PURCHASE_PAYMENT"))
	}
	args["allow_duplicate"] = true
	if forced := e.ok("settle_obligations", args); forced["deduplicated"] != nil || e.movementCount("PURCHASE_PAYMENT") != 3 {
		t.Errorf("allow_duplicate = %v", forced)
	}
	if o := e.mustREST("GET", "/obligations/"+c, nil, 200); o["settled"] != float64(20) || o["status"] != "PARTIAL" {
		t.Errorf("conta c = %v", o)
	}

	// Credores diferentes: permitido, com aviso no corpo.
	other := e.mustREST("POST", "/fornecedores", map[string]any{"nome": "Outro Credor"}, 201)["id"].(string)
	d := e.purchaseAccount(other, f.product, 30)
	mixed := e.ok("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": c, "amount": 5}, {"obligation_id": d}}})
	if w := mixed["warnings"].([]any); len(w) != 1 || w[0] != "MIXED_COUNTERPARTIES" {
		t.Errorf("warnings = %v", mixed["warnings"])
	}

	// Recebimento do cliente pela mesma ferramenta (RECEIVABLE → SALE_RECEIPT).
	recv := e.mustREST("GET", "/obligations?origin_type=SALE&origin_id="+f.sale+"&direction=RECEIVABLE", nil, 200)["items"].([]any)
	rec := e.ok("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": obj(recv[0])["id"], "amount": 500}}, "payment_method": "TRANSFER"})
	if m := obj(rec["movement"]); m["category"] != "SALE_RECEIPT" || m["amount"] != float64(500) {
		t.Errorf("recebimento = %v", rec)
	}

	// Erros: schema (≥ 1 alocação, enum, casas decimais), regras da API (409/404/400 → códigos do conector).
	e.fail("settle_obligations", map[string]any{"allocations": []any{}}, "INVALID_INPUT", "")
	e.fail("settle_obligations", map[string]any{}, "INVALID_INPUT", "")
	e.fail("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": a, "amount": 0}}}, "INVALID_INPUT", "")
	e.fail("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": a, "amount": 10.001}}}, "INVALID_INPUT", "2 casas")
	e.fail("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": a}}, "payment_method": "CHEQUE"}, "INVALID_INPUT", "")
	e.fail("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": a}}}, "CONFLICT", "valor acima do devido") // já quitada
	e.fail("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": uuidNew()}}}, "NOT_FOUND", "")
	e.fail("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": obj(recv[0])["id"], "amount": 1}, {"obligation_id": c, "amount": 1}}}, "INVALID_INPUT", "")
	e.fail("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": c}}, "extra": 1}, "INVALID_INPUT", "")
	e.ok("cancel_sale", map[string]any{"sale_id": f.sale})
	e.fail("settle_obligations", map[string]any{"allocations": []map[string]any{{"obligation_id": obj(recv[0])["id"], "amount": 1}}}, "CONFLICT", "venda cancelada: recebimentos bloqueados")
}

func TestIntegration_WriteRecordExpense(t *testing.T) {
	e := newEnv(t)
	today := time.Now().In(sp()).Format("2006-01-02")

	args := map[string]any{"category": "VEHICLE", "amount": 317.87, "counterparty_name": "Posto Shell", "description": "Combustível", "payment_method": "PIX"}
	out := e.ok("record_expense", args)
	mv := obj(out["movement"])
	if mv["amount"] != -317.87 || mv["category"] != "VEHICLE" || mv["occurred_on"] != today || mv["counterparty_name"] != "Posto Shell" ||
		mv["description"] != "Combustível" || mv["payment_method"] != "PIX" || mv["unallocated"] != 317.87 || len(mv["settlements"].([]any)) != 0 || len(out["warnings"].([]any)) != 0 {
		t.Fatalf("despesa = %v", out)
	}
	// Receita avulsa: o sinal vem da categoria.
	inc := obj(e.ok("record_expense", map[string]any{"category": "OTHER_INCOME", "amount": 2100, "occurred_on": "2026-11-05", "description": "Consultoria"})["movement"])
	if inc["amount"] != float64(2100) || inc["category"] != "OTHER_INCOME" || inc["occurred_on"] != "2026-11-05" {
		t.Errorf("receita avulsa = %v", inc)
	}
	// Mão de obra avulsa entra como despesa; sem quitação.
	if lab := obj(e.ok("record_expense", map[string]any{"category": "LABOR", "amount": 150})["movement"]); lab["amount"] != float64(-150) {
		t.Errorf("LABOR = %v", lab)
	}

	// Deduplicação (120 s) e allow_duplicate.
	if d := e.ok("record_expense", args); d["deduplicated"] != true || obj(d["movement"])["id"] != mv["id"] || e.movementCount("VEHICLE") != 1 {
		t.Fatalf("deveria deduplicar: %v (%d)", d, e.movementCount("VEHICLE"))
	}
	args["allow_duplicate"] = true
	if f := e.ok("record_expense", args); f["deduplicated"] != nil || e.movementCount("VEHICLE") != 2 {
		t.Errorf("allow_duplicate = %v", f)
	}

	e.fail("record_expense", map[string]any{"category": "LAZER", "amount": 10}, "INVALID_INPUT", "")
	e.fail("record_expense", map[string]any{"category": "PURCHASE_PAYMENT", "amount": 10}, "INVALID_INPUT", "") // pagamento de conta é settle_obligations
	e.fail("record_expense", map[string]any{"category": "VEHICLE", "amount": 0}, "INVALID_INPUT", "")
	e.fail("record_expense", map[string]any{"category": "VEHICLE", "amount": -5}, "INVALID_INPUT", "")
	e.fail("record_expense", map[string]any{"category": "VEHICLE", "amount": 10.005}, "INVALID_INPUT", "2 casas")
	e.fail("record_expense", map[string]any{"amount": 10}, "INVALID_INPUT", "")
	e.fail("record_expense", map[string]any{"category": "VEHICLE", "amount": 10, "occurred_on": "ontem"}, "INVALID_INPUT", "")
}

func TestIntegration_WriteSetInstallments(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture() // venda de 2000
	acc := obj(e.mustREST("GET", "/obligations?origin_type=SALE&origin_id="+f.sale+"&direction=RECEIVABLE", nil, 200)["items"].([]any)[0])["id"].(string)

	// Lista completa: substitui o plano; devolve a conta com as parcelas.
	plan := []map[string]any{{"due_date": "2027-01-10", "amount": 800, "payment_method": "BOLETO", "notes": "entrada"}, {"due_date": "2027-02-10", "amount": 1200}}
	out := e.ok("set_installments", map[string]any{"obligation_id": acc, "installments": plan})
	ob := obj(out["obligation"])
	ins := ob["installments"].([]any)
	if ob["id"] != acc || len(ins) != 2 || obj(ins[0])["due_date"] != "2027-01-10" || obj(ins[0])["amount"] != float64(800) || obj(ins[0])["payment_method"] != "BOLETO" ||
		obj(ins[0])["status"] != "OPEN" || obj(ins[1])["covered"] != float64(0) || len(out["warnings"].([]any)) != 0 {
		t.Fatalf("plano = %v", out)
	}
	if ni := obj(ob["next_installment"]); ni["due_date"] != "2027-01-10" || ni["amount_open"] != float64(800) {
		t.Errorf("next_installment = %v", ni)
	}
	if _, has := obj(ins[1])["payment_method"]; has {
		t.Error("campo nulo deve ser omitido")
	}
	// Idempotente: mesmo pedido, mesmo plano.
	again := obj(e.ok("set_installments", map[string]any{"obligation_id": acc, "installments": plan})["obligation"])
	if len(again["installments"].([]any)) != 2 || obj(again["installments"].([]any)[1])["amount"] != float64(1200) {
		t.Errorf("repetir = %v", again)
	}

	// Gerar parcelas iguais (centavos na última) + prazo final; o padrão é o que falta na conta.
	e.ok("create_stock_adjustment", map[string]any{"product_id": f.product, "quantity": 1, "reason": "OTHER"}) // não interfere
	gen := obj(e.ok("set_installments", map[string]any{"obligation_id": acc, "count": 3, "first_due_date": "2027-03-31", "due_date": "2027-09-30"})["obligation"])
	g := gen["installments"].([]any)
	if len(g) != 3 || obj(g[0])["amount"] != 666.66 || obj(g[1])["amount"] != 666.66 || obj(g[2])["amount"] != 666.68 ||
		obj(g[1])["due_date"] != "2027-04-30" || gen["due_date"] != "2027-09-30" {
		t.Errorf("generate = %v", gen)
	}
	// Só o prazo (null limpa); o plano permanece.
	cleared := obj(e.ok("set_installments", map[string]any{"obligation_id": acc, "due_date": nil})["obligation"])
	if _, has := cleared["due_date"]; has || len(cleared["installments"].([]any)) != 3 {
		t.Errorf("due_date null = %v", cleared)
	}
	// Parcelar só parte do valor.
	half := obj(e.ok("set_installments", map[string]any{"obligation_id": acc, "count": 2, "first_due_date": "2027-05-01", "interval_months": 2, "amount": 1000})["obligation"])
	if h := half["installments"].([]any); len(h) != 2 || obj(h[0])["amount"] != float64(500) || obj(h[1])["due_date"] != "2027-07-01" {
		t.Errorf("amount/interval = %v", half)
	}

	e.fail("set_installments", map[string]any{"obligation_id": acc}, "INVALID_INPUT", "informe")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "installments": plan, "count": 2, "first_due_date": "2027-01-01"}, "INVALID_INPUT", "OU")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "count": 3}, "INVALID_INPUT", "first_due_date")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "first_due_date": "2027-01-01", "due_date": "2027-02-01"}, "INVALID_INPUT", "count")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "count": 1, "first_due_date": "2027-01-01"}, "INVALID_INPUT", "")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "count": 25, "first_due_date": "2027-01-01"}, "INVALID_INPUT", "")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "installments": []map[string]any{{"due_date": "2027-01-01", "amount": 2000.01}}}, "INVALID_INPUT", "soma das parcelas")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "installments": []map[string]any{{"due_date": "2027-01-01", "amount": 0}}}, "INVALID_INPUT", "")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "installments": []map[string]any{{"due_date": "2027-01-01", "amount": 1.005}}}, "INVALID_INPUT", "2 casas")
	e.fail("set_installments", map[string]any{"obligation_id": uuidNew(), "due_date": "2027-01-01"}, "NOT_FOUND", "")
	e.fail("set_installments", map[string]any{"obligation_id": acc, "due_date": "01/01/2027"}, "INVALID_INPUT", "")
	// Um plano recusado não altera o anterior.
	if o := e.mustREST("GET", "/obligations/"+acc, nil, 200); len(o["installments"].([]any)) != 2 {
		t.Errorf("plano após recusas = %v", o["installments"])
	}
}
