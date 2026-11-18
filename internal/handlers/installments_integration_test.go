package handlers

// Testes de integração do AYD-007 §3 / SPEC-008 — parcelas (plano), geração e cobertura pelo
// quitado da conta. Só rodam com TEST_DATABASE_URL.
import "testing"

func installmentsOf(d map[string]any) []map[string]any {
	var out []map[string]any
	for _, i := range d["installments"].([]any) {
		out = append(out, i.(map[string]any))
	}
	return out
}

func TestIntegration_InstallmentsPutReplacesPlanAndValidates(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 400)
	acc := e.api.receivable(sale)

	code, d := e.api.plan(acc, [2]any{"2027-01-10", 300.0}, [2]any{"2027-02-10", 300.0})
	ins := installmentsOf(d)
	if code != 200 || len(ins) != 2 || ins[0]["due_date"] != "2027-01-10" || ins[0]["status"] != "OPEN" || num(ins[0], "covered") != 0 || ins[0]["overdue"] != false {
		t.Fatalf("PUT plano = %d %v", code, d)
	}
	// Substitui o plano inteiro (Σ ≤ amount; menor que o valor da conta é válido).
	code, d = e.api.plan(acc, [2]any{"2027-03-01", 1000.0})
	if ins = installmentsOf(d); code != 200 || len(ins) != 1 || num(ins[0], "amount") != 1000 {
		t.Fatalf("PUT substituindo = %d %v", code, d)
	}
	// Plano vazio limpa.
	if code, d = e.api.plan(acc); code != 200 || len(installmentsOf(d)) != 0 {
		t.Errorf("plano vazio = %d %v", code, d)
	}
	// payment_method e notes por parcela.
	code, d = e.api.do("PUT", "/obligations/"+acc+"/installments", map[string]any{"installments": []map[string]any{
		{"due_date": "2027-04-01", "amount": 500, "payment_method": "BOLETO", "notes": "1ª"}}})
	if ins = installmentsOf(d); code != 200 || ins[0]["payment_method"] != "BOLETO" || ins[0]["notes"] != "1ª" {
		t.Errorf("método/notes = %d %v", code, d)
	}

	bad := map[string][]map[string]any{
		"Σ > amount":        {{"due_date": "2027-01-01", "amount": 600}, {"due_date": "2027-02-01", "amount": 400.01}},
		"amount zero":       {{"due_date": "2027-01-01", "amount": 0}},
		"amount negativo":   {{"due_date": "2027-01-01", "amount": -5}},
		"sem due_date":      {{"amount": 10}},
		"due_date inválida": {{"due_date": "01/02/2027", "amount": 10}},
		"método inválido":   {{"due_date": "2027-01-01", "amount": 10, "payment_method": "CHEQUE"}},
		"3 casas":           {{"due_date": "2027-01-01", "amount": 10.005}},
	}
	for name, plan := range bad {
		if code, res := e.api.do("PUT", "/obligations/"+acc+"/installments", map[string]any{"installments": plan}); code != 400 {
			t.Errorf("%s = %d %v, want 400", name, code, res)
		}
	}
	// Um plano recusado não altera o anterior.
	_, cur := e.api.do("GET", "/obligations/"+acc, nil)
	if got := installmentsOf(cur); len(got) != 1 || got[0]["due_date"] != "2027-04-01" {
		t.Errorf("plano após recusas = %v", got)
	}
	// Soft-delete: o plano antigo fica no banco com deleted_at.
	var n int
	e.db.Raw(`SELECT COUNT(*) FROM installments WHERE obligation_id = ? AND deleted_at IS NOT NULL`, acc).Scan(&n)
	if n < 3 {
		t.Errorf("parcelas substituídas deveriam estar com deleted_at: %d", n)
	}
}

func TestIntegration_InstallmentsGenerate(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 400)
	acc := e.api.receivable(sale)
	gen := func(body map[string]any) (int, map[string]any) {
		return e.api.do("POST", "/obligations/"+acc+"/installments/generate", body)
	}

	// Default: amount = open, intervalo 1 mês; centavos na ÚLTIMA; fim de mês preservado.
	code, d := gen(map[string]any{"count": 3, "first_due_date": "2027-01-31"})
	ins := installmentsOf(d)
	if code != 200 || len(ins) != 3 {
		t.Fatalf("generate = %d %v", code, d)
	}
	if num(ins[0], "amount") != 333.33 || num(ins[1], "amount") != 333.33 || num(ins[2], "amount") != 333.34 {
		t.Errorf("valores = %v %v %v", ins[0]["amount"], ins[1]["amount"], ins[2]["amount"])
	}
	if ins[0]["due_date"] != "2027-01-31" || ins[1]["due_date"] != "2027-02-28" || ins[2]["due_date"] != "2027-03-31" {
		t.Errorf("datas = %v %v %v", ins[0]["due_date"], ins[1]["due_date"], ins[2]["due_date"])
	}

	// amount e interval_months explícitos substituem o plano.
	code, d = gen(map[string]any{"count": 2, "first_due_date": "2027-05-10", "interval_months": 2, "amount": 600})
	ins = installmentsOf(d)
	if code != 200 || len(ins) != 2 || num(ins[0], "amount") != 300 || ins[1]["due_date"] != "2027-07-10" {
		t.Errorf("generate explícito = %d %v", code, d)
	}

	// Default = o aberto da conta (depois de receber 400, parcela os 600 restantes).
	e.api.receive(sale, 400, "2027-01-01", "PIX")
	code, d = gen(map[string]any{"count": 2, "first_due_date": "2027-06-01"})
	if ins = installmentsOf(d); code != 200 || num(ins[0], "amount") != 300 || num(ins[1], "amount") != 300 {
		t.Errorf("generate sobre o aberto = %d %v", code, d)
	}

	for name, body := range map[string]map[string]any{
		"count 1":                  {"count": 1, "first_due_date": "2027-01-01"},
		"count 25":                 {"count": 25, "first_due_date": "2027-01-01"},
		"sem count":                {"first_due_date": "2027-01-01"},
		"sem first_due_date":       {"count": 2},
		"first_due_date inválida":  {"count": 2, "first_due_date": "amanhã"},
		"amount acima da conta":    {"count": 2, "first_due_date": "2027-01-01", "amount": 1000.01},
		"amount zero":              {"count": 2, "first_due_date": "2027-01-01", "amount": 0},
		"interval_months inválido": {"count": 2, "first_due_date": "2027-01-01", "interval_months": 99},
	} {
		if code, res := gen(body); code != 400 {
			t.Errorf("%s = %d %v, want 400", name, code, res)
		}
	}
}

// §3: o quitado da conta cobre as parcelas na ordem dos vencimentos; overdue = parcela não
// coberta vencida (ou prazo da conta vencido com open > 0), em America/Sao_Paulo.
func TestIntegration_InstallmentCoverageAndOverdue(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 400)
	acc := e.api.receivable(sale)
	// Fora de ordem de propósito.
	e.api.plan(acc, [2]any{dayStr(30), 300.0}, [2]any{dayStr(-10), 300.0}, [2]any{dayStr(60), 400.0})

	_, d := e.api.do("GET", "/obligations/"+acc, nil)
	if d["overdue"] != true {
		t.Errorf("parcela vencida e não coberta → conta overdue: %v", d["overdue"])
	}
	ins := installmentsOf(d)
	if ins[0]["due_date"] != dayStr(-10) || ins[0]["overdue"] != true || ins[0]["status"] != "OPEN" {
		t.Errorf("ordenação/overdue das parcelas = %v", ins)
	}
	if ni := d["next_installment"].(map[string]any); ni["due_date"] != dayStr(-10) || num(ni, "amount_open") != 300 {
		t.Errorf("next_installment = %v", ni)
	}

	// Receber 450: cobre a mais antiga (300) e metade da seguinte (150).
	e.api.receive(sale, 450, dayStr(0), "PIX")
	_, d = e.api.do("GET", "/obligations/"+acc, nil)
	ins = installmentsOf(d)
	if ins[0]["status"] != "COVERED" || num(ins[0], "covered") != 300 || ins[0]["overdue"] != false {
		t.Errorf("1ª = %v", ins[0])
	}
	if ins[1]["status"] != "PARTIAL" || num(ins[1], "covered") != 150 || ins[2]["status"] != "OPEN" || num(ins[2], "covered") != 0 {
		t.Errorf("2ª/3ª = %v %v", ins[1], ins[2])
	}
	if d["overdue"] != false || d["status"] != "PARTIAL" {
		t.Errorf("conta após cobrir a vencida: overdue=%v status=%v", d["overdue"], d["status"])
	}
	if ni := d["next_installment"].(map[string]any); ni["due_date"] != dayStr(30) || num(ni, "amount_open") != 150 {
		t.Errorf("next_installment = %v", ni)
	}

	// Prazo da conta vencido com open > 0 também é overdue (conta sem parcelas).
	sale2 := e.simpleSale(1, 500, 100)
	acc2 := e.api.receivable(sale2)
	e.api.do("PATCH", "/obligations/"+acc2, map[string]any{"due_date": dayStr(-1)})
	if _, d2 := e.api.do("GET", "/obligations/"+acc2, nil); d2["overdue"] != true || d2["next_installment"] != nil {
		t.Errorf("prazo vencido: %v", d2)
	}
	e.api.receive(sale2, 500, dayStr(0), "PIX")
	if _, d2 := e.api.do("GET", "/obligations/"+acc2, nil); d2["overdue"] != false || d2["status"] != "SETTLED" {
		t.Errorf("conta quitada deixa de ser overdue: %v", d2)
	}
}
