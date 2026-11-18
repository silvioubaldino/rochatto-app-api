package handlers

// Testes de integração do AYD-007 §4 / SPEC-008 — movimentações financeiras (dinheiro executado):
// validações, correção, anulação, filtros e totais. Só rodam com TEST_DATABASE_URL.
import (
	"fmt"
	"math/rand"
	"net/http"
	"testing"
)

func (c apiClient) movement(body map[string]any) map[string]any {
	c.t.Helper()
	code, res := c.do("POST", "/financial-movements", body)
	if code != http.StatusCreated {
		c.t.Fatalf("POST /financial-movements %v = %d %v", body, code, res)
	}
	return res
}

func TestIntegration_FinancialMovementCreateValidation(t *testing.T) {
	e := newFinanceEnv(t)
	m := e.api.movement(map[string]any{"occurred_on": "2027-03-01", "amount": -317.87, "category": "VEHICLE", "payment_method": "PIX",
		"counterparty_name": "Posto Shell", "description": "Combustível"})
	if num(m, "amount") != -317.87 || m["category"] != "VEHICLE" || m["occurred_on"] != "2027-03-01" || m["payment_method"] != "PIX" ||
		m["counterparty_name"] != "Posto Shell" || m["description"] != "Combustível" || num(m, "unallocated") != 317.87 ||
		len(m["settlements"].([]any)) != 0 || m["corrected_at"] != nil || m["created_at"] == nil {
		t.Errorf("movimentação avulsa = %v", m)
	}
	income := e.api.movement(map[string]any{"occurred_on": "2027-03-02", "amount": 2100, "category": "OTHER_INCOME"})
	if income["payment_method"] != nil || income["counterparty_name"] != nil || num(income, "amount") != 2100 {
		t.Errorf("OTHER_INCOME = %v", income)
	}

	for name, body := range map[string]map[string]any{
		"amount zero":                {"occurred_on": "2027-03-01", "amount": 0, "category": "VEHICLE"},
		"saída com valor positivo":   {"occurred_on": "2027-03-01", "amount": 10, "category": "VEHICLE"},
		"entrada com valor negativo": {"occurred_on": "2027-03-01", "amount": -10, "category": "OTHER_INCOME"},
		"SALE_RECEIPT negativo":      {"occurred_on": "2027-03-01", "amount": -10, "category": "SALE_RECEIPT"},
		"categoria inválida":         {"occurred_on": "2027-03-01", "amount": -10, "category": "LAZER"},
		"sem categoria":              {"occurred_on": "2027-03-01", "amount": -10},
		"sem occurred_on":            {"amount": -10, "category": "VEHICLE"},
		"occurred_on inválida":       {"occurred_on": "01/03/2027", "amount": -10, "category": "VEHICLE"},
		"payment_method inválido":    {"occurred_on": "2027-03-01", "amount": -10, "category": "VEHICLE", "payment_method": "CHEQUE"},
		"3 casas":                    {"occurred_on": "2027-03-01", "amount": -10.005, "category": "VEHICLE"},
	} {
		if code, res := e.api.do("POST", "/financial-movements", body); code != 400 {
			t.Errorf("%s = %d %v, want 400", name, code, res)
		}
	}
}

// PUT é correção (carimba corrected_at) com o mesmo corpo do POST; 409 se deixaria Σ quitações
// maior que |amount| ou inverteria o sinal de uma movimentação que já quita contas.
func TestIntegration_FinancialMovementCorrection(t *testing.T) {
	e := newFinanceEnv(t)
	m := e.api.movement(map[string]any{"occurred_on": "2027-03-01", "amount": -100, "category": "VEHICLE", "payment_method": "PIX"})
	id := m["id"].(string)

	code, got := e.api.do("PUT", "/financial-movements/"+id, map[string]any{
		"occurred_on": "2027-03-05", "amount": -120.5, "category": "FIXED_COSTS", "payment_method": "CASH", "counterparty_name": "Aluguel", "description": "corrigido"})
	if code != 200 || num(got, "amount") != -120.5 || got["category"] != "FIXED_COSTS" || got["occurred_on"] != "2027-03-05" ||
		got["payment_method"] != "CASH" || got["description"] != "corrigido" || got["corrected_at"] == nil {
		t.Fatalf("PUT = %d %v", code, got)
	}
	// Corpo = o do POST: campo omitido é limpo (substituição completa).
	_, got = e.api.do("PUT", "/financial-movements/"+id, map[string]any{"occurred_on": "2027-03-05", "amount": -120.5, "category": "FIXED_COSTS"})
	if got["payment_method"] != nil || got["description"] != nil {
		t.Errorf("PUT deveria substituir: %v", got)
	}
	if code, _ := e.api.do("PUT", "/financial-movements/"+id, map[string]any{"occurred_on": "2027-03-05", "amount": 5, "category": "FIXED_COSTS"}); code != 400 {
		t.Errorf("PUT com sinal incoerente = %d, want 400", code)
	}
	if code, _ := e.api.do("PUT", "/financial-movements/00000000-0000-0000-0000-000000000000", map[string]any{"occurred_on": "2027-03-05", "amount": -1, "category": "VEHICLE"}); code != 404 {
		t.Errorf("PUT inexistente = %d, want 404", code)
	}

	// Movimentação que quita conta: correção não pode passar do quitado nem inverter o sinal.
	sale := e.simpleSale(1, 1000, 400)
	_, res := e.api.receive(sale, 400, "2027-03-01", "PIX")
	mv := res["movement"].(map[string]any)
	mid := mv["id"].(string)
	put := func(amount float64, category string) int {
		code, _ := e.api.do("PUT", "/financial-movements/"+mid, map[string]any{"occurred_on": "2027-03-01", "amount": amount, "category": category})
		return code
	}
	if code := put(399.99, "SALE_RECEIPT"); code != 409 {
		t.Errorf("valor abaixo do quitado = %d, want 409", code)
	}
	if code := put(-400, "VEHICLE"); code != 409 {
		t.Errorf("inverter o sinal com quitação = %d, want 409", code)
	}
	if code := put(450, "SALE_RECEIPT"); code != 200 {
		t.Errorf("aumentar o valor = %d, want 200", code)
	}
	_, d := e.api.do("GET", "/obligations/"+e.api.receivable(sale), nil)
	if num(d, "settled") != 400 {
		t.Errorf("correção não altera a quitação: settled = %v", d["settled"])
	}
	_, mm := e.api.do("GET", "/financial-movements?from=2027-03-01&to=2027-03-01&category=SALE_RECEIPT", nil)
	found := false
	for _, it := range mm["items"].([]any) {
		if m := it.(map[string]any); m["id"] == mid {
			found = num(m, "unallocated") == 50 && num(m, "amount") == 450
		}
	}
	if !found {
		t.Errorf("movimentação corrigida deveria ter unallocated 50: %v", mm)
	}
}

// DELETE anula a movimentação e desfaz as suas quitações (soft-delete de ambos).
func TestIntegration_FinancialMovementDeleteUndoesSettlements(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 400)
	_, res := e.api.receive(sale, 1000, "2027-03-01", "PIX")
	mid := res["movement"].(map[string]any)["id"].(string)
	acc := e.api.receivable(sale)
	if _, d := e.api.do("GET", "/obligations/"+acc, nil); d["status"] != "SETTLED" {
		t.Fatalf("antes: %v", d["status"])
	}

	if code, _ := e.api.do("DELETE", "/financial-movements/"+mid, nil); code != 204 {
		t.Fatalf("DELETE = %d, want 204", code)
	}
	_, d := e.api.do("GET", "/obligations/"+acc, nil)
	if d["status"] != "OPEN" || num(d, "settled") != 0 || len(d["settlements"].([]any)) != 0 {
		t.Errorf("conta após anular = %v", d)
	}
	_, s := e.api.do("GET", "/vendas/"+sale, nil)
	if s["payment_status"] != "PENDING" || num(s, "total_recebido") != 0 {
		t.Errorf("venda após anular = %v", s)
	}
	var movementGone, settlementGone bool
	e.db.Raw(`SELECT deleted_at IS NOT NULL FROM financial_movements WHERE id = ?`, mid).Scan(&movementGone)
	e.db.Raw(`SELECT bool_and(deleted_at IS NOT NULL) FROM settlements WHERE movement_id = ?`, mid).Scan(&settlementGone)
	if !movementGone || !settlementGone {
		t.Errorf("soft-delete: movimentação=%v quitações=%v", movementGone, settlementGone)
	}
	if code, _ := e.api.do("DELETE", "/financial-movements/"+mid, nil); code != 404 {
		t.Errorf("DELETE repetido = %d, want 404", code)
	}
	if code, _ := e.api.do("PUT", "/financial-movements/"+mid, map[string]any{"occurred_on": "2027-03-01", "amount": 1, "category": "SALE_RECEIPT"}); code != 404 {
		t.Errorf("PUT em anulada = %d, want 404", code)
	}
	// Dá para receber de novo: a conta reabriu.
	if code, _ := e.api.receive(sale, 1000, "2027-03-02", "PIX"); code != 201 {
		t.Errorf("receber de novo = %d", code)
	}
}

func TestIntegration_FinancialMovementListFiltersAndTotals(t *testing.T) {
	e := newFinanceEnv(t)
	year := 2100 + rand.Intn(7000)
	d := func(m, day int) string { return fmt.Sprintf("%d-%02d-%02d", year, m, day) }
	from, to := d(8, 1), d(8, 31)

	sup := e.supplier()
	pid := e.product()
	_, p := e.api.do("POST", "/purchases", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": sup, "items": []map[string]any{{"product_id": pid, "quantity": 1, "unit_cost": 500}}})
	pacc := e.api.payableOf("PURCHASE", p["id"].(string), sup)
	e.api.do("POST", "/obligations/"+pacc+"/settle", map[string]any{"amount": 200, "movement": map[string]any{"occurred_on": d(8, 10), "payment_method": "PIX"}})
	e.api.movement(map[string]any{"occurred_on": d(8, 12), "amount": -50, "category": "VEHICLE"})
	e.api.movement(map[string]any{"occurred_on": d(8, 20), "amount": 700, "category": "OTHER_INCOME"})
	e.api.movement(map[string]any{"occurred_on": d(9, 1), "amount": -999, "category": "TAXES"}) // fora do período

	get := func(q string) map[string]any {
		t.Helper()
		code, res := e.api.do("GET", "/financial-movements?"+q, nil)
		if code != 200 {
			t.Fatalf("GET ?%s = %d %v", q, code, res)
		}
		return res
	}
	all := get("from=" + from + "&to=" + to)
	if num(all, "total") != 3 || all["has_more"] != false {
		t.Errorf("período = %v", all)
	}
	tot := all["totals"].(map[string]any)
	e.wantNum("totals", tot, "inflow", 700)
	e.wantNum("totals", tot, "outflow", 250)
	e.wantNum("totals", tot, "net", 450)
	// Mais recentes primeiro.
	if first := all["items"].([]any)[0].(map[string]any); first["occurred_on"] != d(8, 20) {
		t.Errorf("ordem = %v", first["occurred_on"])
	}

	if r := get("from=" + from + "&to=" + to + "&direction=IN"); num(r, "total") != 1 {
		t.Errorf("direction=IN = %v", r)
	}
	if r := get("from=" + from + "&to=" + to + "&direction=OUT"); num(r, "total") != 2 {
		t.Errorf("direction=OUT = %v", r)
	}
	if r := get("from=" + from + "&to=" + to + "&category=VEHICLE"); num(r, "total") != 1 {
		t.Errorf("category = %v", r)
	}
	// counterparty: movimentações que quitam contas daquele credor; o nome vem da conta.
	r := get("from=" + from + "&to=" + to + "&counterparty_type=SUPPLIER&counterparty_id=" + sup)
	if num(r, "total") != 1 {
		t.Fatalf("counterparty = %v", r)
	}
	if m := r["items"].([]any)[0].(map[string]any); m["counterparty_name"] == nil || num(m, "unallocated") != 0 || len(m["settlements"].([]any)) != 1 {
		t.Errorf("movimentação do credor = %v", m)
	}
	// unallocated_only: as duas avulsas (sem quitação).
	if r := get("from=" + from + "&to=" + to + "&unallocated_only=true"); num(r, "total") != 2 {
		t.Errorf("unallocated_only = %v", r)
	}
	// paginação.
	pg := get("from=" + from + "&to=" + to + "&limit=2")
	if len(pg["items"].([]any)) != 2 || pg["has_more"] != true {
		t.Errorf("limit=2 = %v", pg)
	}
	pg = get("from=" + from + "&to=" + to + "&limit=2&offset=2")
	if len(pg["items"].([]any)) != 1 || pg["has_more"] != false {
		t.Errorf("offset=2 = %v", pg)
	}
	for _, q := range []string{"from=ontem", "to=2027-13-01", "category=X", "direction=SIDEWAYS", "counterparty_type=X", "counterparty_id=abc", "limit=0"} {
		if code, _ := e.api.do("GET", "/financial-movements?"+q, nil); code != 400 {
			t.Errorf("?%s = %d, want 400", q, code)
		}
	}
}
