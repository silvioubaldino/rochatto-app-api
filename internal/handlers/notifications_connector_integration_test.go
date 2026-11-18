package handlers

// SPEC-006: duas adições compatíveis em GET /dashboard/notificacoes usadas pelo Assistant
// connector — `pagamento_id` (Receipt do alerta) e `only_unread=false` (inclui os lidos).
// Roda só com TEST_DATABASE_URL.
import "testing"

func TestIntegration_NotificationsExposeReceiptAndReadFilter(t *testing.T) {
	e := newFinanceEnv(t)
	sale := e.simpleSale(1, 1000, 500)
	_, rec := e.receipt(sale, map[string]any{"valor": 100, "data_agendada": dayStr(-1)})
	recID := rec["id"].(string)

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
	if n == nil || n["pagamento_id"] != recID || n["lida"] != false {
		t.Fatalf("alerta = %v, quero pagamento_id %s", n, recID)
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
