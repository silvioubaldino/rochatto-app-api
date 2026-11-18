package handlers

// SPEC-006/SPEC-008: GET /dashboard/notificacoes usado pelo Assistant connector — traz
// `obligation_id` / `installment_id` (AYD-007; antes `pagamento_id`) e `only_unread=false` (inclui os lidos).
// Roda só com TEST_DATABASE_URL.
import "testing"

func TestIntegration_NotificationsExposeObligationAndReadFilter(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)
	acc := e.api.receivable(sale)
	_, plan := e.api.plan(acc, [2]any{dayStr(-1), 100.0})
	instID := plan["installments"].([]any)[0].(map[string]any)["id"].(string)

	find := func(query string) map[string]any {
		t.Helper()
		code, res := e.api.do("GET", "/dashboard/notificacoes"+query, nil)
		if code != 200 {
			t.Fatalf("GET notificacoes%s = %d %v", query, code, res)
		}
		for _, x := range res["notificacoes"].([]any) {
			if m := x.(map[string]any); m["venda_id"] == sale {
				return m
			}
		}
		return nil
	}

	n := find("")
	if n == nil || n["obligation_id"] != acc || n["installment_id"] != instID || n["lida"] != false {
		t.Fatalf("alerta = %v, quero obligation_id %s e installment_id %s", n, acc, instID)
	}
	if _, has := n["pagamento_id"]; has {
		t.Error("pagamento_id foi substituído por obligation_id/installment_id")
	}
	if code, _ := e.api.do("POST", "/dashboard/notificacoes/"+n["id"].(string)+"/lida", nil); code != 204 {
		t.Fatalf("marcar lida = %d", code)
	}
	if find("") != nil || find("?only_unread=true") != nil {
		t.Error("lido não aparece por padrão")
	}
	if l := find("?only_unread=false"); l == nil || l["lida"] != true {
		t.Errorf("only_unread=false deveria incluir o lido: %v", l)
	}
}
