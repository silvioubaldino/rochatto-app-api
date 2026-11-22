package handlers

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func newSummaryEnv(t *testing.T) (apiClient, *gorm.DB, string) {
	t.Helper()
	_, db, customerID := newIntegrationEnv(t)
	mux := http.NewServeMux()
	NewVendasHandler(db).Register(mux)
	NewDashboardHandler(db).Register(mux)
	return apiClient{t, mux}, db, customerID
}

func approx(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func mustExec(t *testing.T, db *gorm.DB, sql string, args ...any) {
	t.Helper()
	if err := db.Exec(sql, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func insertReceived(t *testing.T, db *gorm.DB, saleID string, amount float64) {
	t.Helper()
	var movement string
	if err := db.Raw(`INSERT INTO financial_movements (occurred_on, amount, category) VALUES (CURRENT_DATE, ?, 'SALE_RECEIPT') RETURNING id::text`, amount).Scan(&movement).Error; err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO settlements (obligation_id, movement_id, amount)
		SELECT o.id, ?, ? FROM obligations o WHERE o.sale_id = ? AND o.direction = 'RECEIVABLE' AND o.deleted_at IS NULL`,
		movement, amount, saleID)
}

func insertPlanned(t *testing.T, db *gorm.DB, saleID string, amount float64) {
	t.Helper()
	mustExec(t, db, `INSERT INTO installments (obligation_id, due_date, amount)
		SELECT o.id, CURRENT_DATE + 30, ? FROM obligations o WHERE o.sale_id = ? AND o.direction = 'RECEIVABLE' AND o.deleted_at IS NULL`,
		amount, saleID)
}

func insertSale(t *testing.T, db *gorm.DB, customerID string, freight, commission float64) string {
	t.Helper()
	var id string
	if err := db.Raw(`INSERT INTO vendas (cliente_id) VALUES (?) RETURNING id`, customerID).Scan(&id).Error; err != nil {
		t.Fatal(err)
	}
	setLegacyCosts(t, db, id, freight, commission)
	return id
}

type dashSnapshot struct {
	aReceber, lucro, vendido float64
	mesTotal, mesLucro       float64
}

func takeSnapshot(t *testing.T, api apiClient, day, month string) dashSnapshot {
	t.Helper()
	code, res := api.do("GET", "/dashboard/resumo?data_inicio="+day+"&data_fim="+day, nil)
	if code != 200 {
		t.Fatalf("resumo = %d", code)
	}
	s := dashSnapshot{
		aReceber: res["total_a_receber"].(float64),
		lucro:    res["lucro_periodo"].(float64),
		vendido:  res["total_vendido_periodo"].(float64),
	}
	code, rel := api.do("GET", "/relatorios/vendas?meses=1", nil)
	if code != 200 {
		t.Fatalf("relatorios/vendas = %d", code)
	}
	for _, d := range rel["dados"].([]any) {
		m := d.(map[string]any)
		if m["mes"] == month {
			s.mesTotal, s.mesLucro = m["total_venda"].(float64), m["lucro"].(float64)
		}
	}
	return s
}

func TestIntegration_SaleSummaryNoFanOut(t *testing.T) {
	api, db, cid := newSummaryEnv(t)

	saleID := insertSale(t, db, cid, 400, 100)
	mustExec(t, db, `INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit)
		VALUES (?, 'Granito', 2, 1000, 600), (?, 'Mármore', 1.5, 2000, 1200)`, saleID, saleID)
	insertReceived(t, db, saleID, 1000)
	insertReceived(t, db, saleID, 1500)
	insertPlanned(t, db, saleID, 800)

	code, sale := api.do("GET", "/vendas/"+saleID, nil)
	if code != 200 {
		t.Fatalf("GET venda = %d", code)
	}
	want := map[string]float64{
		"total_venda":    5000,
		"total_custo":    3000,
		"total_recebido": 2500,
		"a_receber":      2500,
		"lucro":          1500,
	}
	for k, v := range want {
		if got, _ := sale[k].(float64); !approx(got, v) {
			t.Errorf("%s = %v, want %v", k, sale[k], v)
		}
	}
	if _, ok := sale["quote_id"]; !ok {
		t.Errorf("quote_id ausente na resposta (deve vir da view)")
	}

	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/vendas?cliente_id="+cid+"&pendente=true", nil))
	var list []map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
		t.Fatalf("GET /vendas = %d %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, m := range list {
		if m["id"] == saleID {
			found = true
			if !approx(m["total_venda"].(float64), 5000) || !approx(m["lucro"].(float64), 1500) {
				t.Errorf("listagem: total=%v lucro=%v, want 5000/1500", m["total_venda"], m["lucro"])
			}
		}
	}
	if !found {
		t.Errorf("venda pendente não listada com pendente=true")
	}
}

func TestIntegration_DashboardNumbers(t *testing.T) {
	api, db, cid := newSummaryEnv(t)

	var day, month string
	db.Raw("SELECT CURRENT_DATE::text").Scan(&day)
	db.Raw("SELECT DATE_TRUNC('month', CURRENT_DATE)::date::text").Scan(&month)
	before := takeSnapshot(t, api, day, month)

	var productID string
	productName := "Quartzito teste " + uuid.NewString()
	if err := db.Raw("INSERT INTO produtos (nome) VALUES (?) RETURNING id", productName).Scan(&productID).Error; err != nil {
		t.Fatal(err)
	}
	legacyName := "Legado " + productID

	s1 := insertSale(t, db, cid, 400, 100)
	mustExec(t, db, `INSERT INTO itens_venda (venda_id, produto_id, produto_nome, quantidade, preco_venda_unit, custo_unit)
		VALUES (?, ?, 'Quartzito nome antigo', 2, 1000, 600)`, s1, productID)
	mustExec(t, db, `INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit)
		VALUES (?, ?, 1.5, 2000, 1200)`, s1, legacyName)
	insertReceived(t, db, s1, 1000)
	insertReceived(t, db, s1, 1500)
	insertPlanned(t, db, s1, 800)
	s2 := insertSale(t, db, cid, 0, 0)
	mustExec(t, db, `INSERT INTO itens_venda (venda_id, produto_id, produto_nome, quantidade, preco_venda_unit, custo_unit)
		VALUES (?, ?, ?, 8, 500, 300)`, s2, productID, productName)

	after := takeSnapshot(t, api, day, month)

	checks := []struct {
		name      string
		got, want float64
	}{
		{"a receber", after.aReceber - before.aReceber, 2500 + 4000},
		{"lucro do período", after.lucro - before.lucro, 1500 + 1600},
		{"total vendido no período", after.vendido - before.vendido, 5000 + 4000},
		{"série mensal: total", after.mesTotal - before.mesTotal, 9000},
		{"série mensal: lucro (Freight/Commission 1× por Sale)", after.mesLucro - before.mesLucro, 3100},
	}
	for _, c := range checks {
		if !approx(c.got, c.want) {
			t.Errorf("%s: delta = %v, want %v", c.name, c.got, c.want)
		}
	}

	code, rank := api.do("GET", "/relatorios/produtos?data_inicio="+day+"&data_fim="+day, nil)
	if code != 200 {
		t.Fatalf("relatorios/produtos = %d", code)
	}
	var byID, legacy map[string]any
	for _, d := range rank["dados"].([]any) {
		m := d.(map[string]any)
		if m["produto_id"] == productID {
			if byID != nil {
				t.Errorf("Product %s aparece em mais de uma linha", productID)
			}
			byID = m
		}
		if m["produto_nome"] == legacyName {
			legacy = m
		}
	}
	if byID == nil || legacy == nil {
		t.Fatalf("ranking sem as linhas do teste (byID=%v legacy=%v)", byID != nil, legacy != nil)
	}
	wantRank := map[string]float64{
		"total_vendas":         2,
		"total_quantidade":     10,
		"total_receita":        6000,
		"preco_medio":          600,
		"custo_medio":          360,
		"margem_media_percent": 40,
	}
	for k, v := range wantRank {
		if got, _ := byID[k].(float64); !approx(got, v) {
			t.Errorf("ranking %s = %v, want %v", k, byID[k], v)
		}
	}
	if byID["produto_nome"] != productName {
		t.Errorf("produto_nome = %v, want nome do cadastro %q", byID["produto_nome"], productName)
	}
	if legacy["produto_id"] != nil || !approx(legacy["preco_medio"].(float64), 2000) {
		t.Errorf("linha legada: produto_id=%v preco_medio=%v, want nil/2000", legacy["produto_id"], legacy["preco_medio"])
	}
}

func TestIntegration_DashboardAReceberIncludesDelivered(t *testing.T) {
	api, db, cid := newSummaryEnv(t)
	var day, month string
	db.Raw("SELECT CURRENT_DATE::text").Scan(&day)
	db.Raw("SELECT DATE_TRUNC('month', CURRENT_DATE)::date::text").Scan(&month)
	before := takeSnapshot(t, api, day, month)

	delivered := insertSale(t, db, cid, 0, 0)
	mustExec(t, db, `UPDATE vendas SET delivery_status = 'DELIVERED' WHERE id = ?`, delivered)
	mustExec(t, db, `INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit)
		VALUES (?, 'Granito', 1, 1000, 500)`, delivered)
	insertReceived(t, db, delivered, 400)

	overpaid := insertSale(t, db, cid, 0, 0)
	mustExec(t, db, `INSERT INTO itens_venda (venda_id, produto_nome, quantidade, preco_venda_unit, custo_unit)
		VALUES (?, 'Mármore', 1, 300, 100)`, overpaid)
	insertReceived(t, db, overpaid, 500)

	after := takeSnapshot(t, api, day, month)
	if got := after.aReceber - before.aReceber; !approx(got, 600) {
		t.Errorf("a receber delta = %v, want 600 (entregue devendo 600; pago a maior conta 0)", got)
	}
}
