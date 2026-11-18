package handlers

// Testes de integração do fluxo Quote → Sale contra um Postgres real.
// Rodam só com TEST_DATABASE_URL apontando para um banco com as migrations 001–003
// aplicadas (o teste cria seus próprios dados e não limpa o banco — use um banco descartável):
//
//	TEST_DATABASE_URL="host=localhost user=sales password=sales dbname=salesdb sslmode=disable" go test ./internal/handlers
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type apiClient struct {
	t   *testing.T
	mux *http.ServeMux
}

func (c apiClient) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	rec := httptest.NewRecorder()
	c.mux.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewReader(b)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func newIntegrationEnv(t *testing.T) (apiClient, *gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewQuotesHandler(db).Register(mux)
	NewVendasHandler(db).Register(mux)
	var customerID string
	if err := db.Raw("INSERT INTO clientes(nome) VALUES ('Cliente teste') RETURNING id").Scan(&customerID).Error; err != nil {
		t.Fatal(err)
	}
	return apiClient{t, mux}, db, customerID
}

func twoItems() []map[string]any {
	return []map[string]any{
		{"product_name": "Granito", "quantity": 2, "unit_price": 100, "unit_cost": 60},
		{"product_name": "Mármore", "quantity": 1.5, "unit_price": 200, "unit_cost": 120},
	}
}

func TestIntegration_QuoteLifecycleAndConvert(t *testing.T) {
	api, db, cid := newIntegrationEnv(t)

	code, q := api.do("POST", "/quotes", map[string]any{"customer_id": cid, "items": twoItems()})
	if code != 201 || q["total_quote"].(float64) != 500 {
		t.Fatalf("create = %d total=%v, want 201/500", code, q["total_quote"])
	}
	id := q["id"].(string)

	if code, _ := api.do("PATCH", "/quotes/"+id+"/status", map[string]any{"status": "WON"}); code != 409 {
		t.Errorf("PATCH WON = %d, want 409", code)
	}
	if code, _ := api.do("PATCH", "/quotes/"+id+"/status", map[string]any{"status": "LOST", "lost_reason": "preço"}); code != 200 {
		t.Errorf("PATCH LOST = %d, want 200", code)
	}
	if code, _ := api.do("POST", "/quotes/"+id+"/convert", nil); code != 409 {
		t.Errorf("convert LOST = %d, want 409", code)
	}
	if code, _ := api.do("PATCH", "/quotes/"+id+"/status", map[string]any{"status": "OPEN"}); code != 200 {
		t.Errorf("reopen = %d, want 200", code)
	}

	code, conv := api.do("POST", "/quotes/"+id+"/convert", nil)
	if code != 201 {
		t.Fatalf("convert = %d, want 201", code)
	}
	if code, _ := api.do("POST", "/quotes/"+id+"/convert", nil); code != 409 {
		t.Errorf("convert again = %d, want 409", code)
	}

	// Sale: valores copiados, origem do Quote exposta, sem Supplier (PDR-001).
	code, sale := api.do("GET", "/vendas/"+conv["sale_id"].(string), nil)
	if code != 200 || sale["quote_id"] != id || sale["total_venda"].(float64) != 500 {
		t.Errorf("sale = %d quote_id=%v total=%v", code, sale["quote_id"], sale["total_venda"])
	}
	var suppliers int64
	db.Raw("SELECT count(*) FROM itens_venda WHERE venda_id = ? AND fornecedor_id IS NOT NULL", conv["sale_id"]).Scan(&suppliers)
	if suppliers != 0 {
		t.Errorf("itens com fornecedor após conversão = %d, want 0", suppliers)
	}

	// RN-01: Quote convertido não pode ser excluído.
	if code, _ := api.do("DELETE", "/quotes/"+id, nil); code != 409 {
		t.Errorf("delete WON = %d, want 409", code)
	}
}

func TestIntegration_ConvertEmptyAndValidation(t *testing.T) {
	api, _, cid := newIntegrationEnv(t)

	_, q := api.do("POST", "/quotes", map[string]any{"customer_id": cid})
	if code, _ := api.do("POST", "/quotes/"+q["id"].(string)+"/convert", nil); code != 422 {
		t.Errorf("convert sem items = %d, want 422", code)
	}
	if code, _ := api.do("DELETE", "/quotes/"+q["id"].(string), nil); code != 204 {
		t.Errorf("delete OPEN = %d, want 204", code)
	}
	if code, _ := api.do("POST", "/quotes", map[string]any{"customer_id": cid, "referrer_id": "11111111-1111-1111-1111-111111111111"}); code != 400 {
		t.Errorf("referrer inexistente = %d, want 400", code)
	}
	if code, _ := api.do("GET", "/quotes?from=lixo", nil); code != 400 {
		t.Errorf("from inválido = %d, want 400", code)
	}
	if code, _ := api.do("GET", "/quotes?to=2026-02-31x", nil); code != 400 {
		t.Errorf("to inválido = %d, want 400", code)
	}
}

func TestIntegration_ConcurrentConvertCreatesOneSale(t *testing.T) {
	api, db, cid := newIntegrationEnv(t)
	_, q := api.do("POST", "/quotes", map[string]any{"customer_id": cid, "items": twoItems()})
	id := q["id"].(string)

	codes := make([]int, 5)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = api.do("POST", "/quotes/"+id+"/convert", nil)
		}(i)
	}
	wg.Wait()

	created := 0
	for _, c := range codes {
		switch c {
		case 201:
			created++
		case 409:
		default:
			t.Errorf("status inesperado %d em conversão concorrente (%v)", c, codes)
		}
	}
	var sales int64
	db.Raw("SELECT count(*) FROM vendas WHERE quote_id = ?", id).Scan(&sales)
	if created != 1 || sales != 1 {
		t.Errorf("201s=%d vendas=%d, want 1/1 (%v)", created, sales, codes)
	}
}

func TestIntegration_QuickSale(t *testing.T) {
	api, _, cid := newIntegrationEnv(t)

	if code, _ := api.do("POST", "/quotes/quick-sale", map[string]any{"customer_id": cid}); code != 422 {
		t.Errorf("quick-sale sem items = %d, want 422", code)
	}
	code, res := api.do("POST", "/quotes/quick-sale", map[string]any{"customer_id": cid, "items": twoItems()})
	if code != 201 {
		t.Fatalf("quick-sale = %d, want 201", code)
	}
	_, quote := api.do("GET", "/quotes/"+res["quote_id"].(string), nil)
	_, sale := api.do("GET", "/vendas/"+res["sale_id"].(string), nil)
	if quote["status"] != "WON" || sale["quote_id"] != res["quote_id"] {
		t.Errorf("quote=%v sale.quote_id=%v", quote["status"], sale["quote_id"])
	}
}

func TestIntegration_EditItemKeepsProduct(t *testing.T) {
	api, db, cid := newIntegrationEnv(t)
	var pid string
	db.Raw("INSERT INTO produtos(nome) VALUES ('Granito T') RETURNING id").Scan(&pid)
	_, q := api.do("POST", "/quotes", map[string]any{"customer_id": cid, "items": []map[string]any{
		{"product_id": pid, "product_name": "Granito T", "quantity": 1, "unit_price": 10, "unit_cost": 5}}})
	id := q["id"].(string)
	item := q["items"].([]any)[0].(map[string]any)

	code, _ := api.do("PUT", "/quotes/"+id+"/items/"+item["id"].(string), map[string]any{
		"product_id": pid, "product_name": "Granito T", "quantity": 1, "unit_price": 12, "unit_cost": 5})
	if code != 200 {
		t.Fatalf("update item = %d", code)
	}
	var kept string
	db.Raw("SELECT product_id::text FROM quote_items WHERE id = ?", item["id"]).Scan(&kept)
	if kept != pid {
		t.Errorf("product_id após edição = %q, want %q", kept, pid)
	}
}
