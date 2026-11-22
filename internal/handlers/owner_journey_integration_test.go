package handlers

// Testes de integração do AYD-011 / SPEC-012 (jornada do dono). Cada teste usa um banco
// descartável (testdb) — pulam sem TEST_DATABASE_URL.

import (
	"net/http"
	"testing"

	"github.com/silvioubaldino/sales-backend/internal/testdb"
	"gorm.io/gorm"
)

func newJourneyEnv(t *testing.T) fxEnv {
	t.Helper()
	db := testdb.New(t)
	mux := http.NewServeMux()
	NewClientesHandler(db).Register(mux)
	NewProdutosHandler(db).Register(mux)
	NewFornecedoresHandler(db).Register(mux)
	NewPartnersHandler(db).Register(mux)
	NewServicesHandler(db).Register(mux)
	NewVendasHandler(db).Register(mux)
	NewQuotesHandler(db).Register(mux)
	NewObligationsHandler(db).Register(mux)
	NewSettlementsHandler(db).Register(mux)
	NewFinancialMovementsHandler(db).Register(mux)
	NewPurchasesHandler(db).Register(mux)
	NewStockHandler(db).Register(mux)
	NewDashboardHandler(db).Register(mux)
	NewFinanceHandler(db).Register(mux)
	registerJourneyHandlers(db, mux)
	var cid string
	if err := db.Raw("INSERT INTO clientes(nome, cidade) VALUES ('Alejandra', 'Niterói') RETURNING id").Scan(&cid).Error; err != nil {
		t.Fatal(err)
	}
	return fxEnv{t, apiClient{t, mux}, db, cid}
}

func (e fxEnv) customer(name string) string {
	e.t.Helper()
	var id string
	if err := e.db.Raw("INSERT INTO clientes(nome) VALUES (?) RETURNING id", name).Scan(&id).Error; err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e fxEnv) namedPartner(name string) string {
	e.t.Helper()
	var id string
	if err := e.db.Raw("INSERT INTO vendedores_externos(nome) VALUES (?) RETURNING id", name).Scan(&id).Error; err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e fxEnv) must(method, path string, body any, want int) map[string]any {
	e.t.Helper()
	code, res := e.api.do(method, path, body)
	if code != want {
		e.t.Fatalf("%s %s = %d %v, want %d", method, path, code, res, want)
	}
	return res
}

func (e fxEnv) listIDs(path string) []string {
	e.t.Helper()
	code, rows := e.api.list(path)
	if code != 200 {
		e.t.Fatalf("GET %s = %d", path, code)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r["id"].(string))
	}
	return ids
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// C1 + C2 + C3: número humano, Quote ↔ Sale, cabeçalho editável e resumo dos Items.
func TestJourney_NumbersQuoteSaleLinkAndHeader(t *testing.T) {
	e := newJourneyEnv(t)
	q1 := e.must("POST", "/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{
		qItem("Granito Preto", 2, 100, 60, ""), qItem("Mármore", 1, 50, 20, "")}}, 201)
	q2 := e.must("POST", "/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{qItem("Quartzito", 1, 10, 5, "")}}, 201)
	if num(q1, "number") != 1 || num(q2, "number") != 2 {
		t.Fatalf("numbers = %v %v, want 1 2", q1["number"], q2["number"])
	}
	if q1["sale_id"] != nil || q1["sale_number"] != nil {
		t.Errorf("Quote OPEN sem Sale: %v %v", q1["sale_id"], q1["sale_number"])
	}
	if num(q1, "items_count") != 2 || q1["first_item_name"] != "Granito Preto" {
		t.Errorf("resumo = %v %v", q1["items_count"], q1["first_item_name"])
	}

	// Venda rápida consome 1 número de Quote e 1 de Sale.
	qs := e.must("POST", "/quotes/quick-sale", map[string]any{"customer_id": e.cid, "items": []map[string]any{qItem("Rápida", 1, 10, 5, "")}}, 201)
	if num(qs, "sale_number") != 1 {
		t.Errorf("sale_number da rápida = %v, want 1", qs["sale_number"])
	}
	conv := e.must("POST", "/quotes/"+q1["id"].(string)+"/convert", nil, 201)
	if num(conv, "sale_number") != 2 {
		t.Errorf("sale_number = %v, want 2", conv["sale_number"])
	}
	quote := e.must("GET", "/quotes/"+q1["id"].(string), nil, 200)
	if quote["sale_id"] != conv["sale_id"] || num(quote, "sale_number") != 2 {
		t.Errorf("quote ↔ sale = %v %v", quote["sale_id"], quote["sale_number"])
	}
	sale := e.must("GET", "/vendas/"+conv["sale_id"].(string), nil, 200)
	if num(sale, "number") != 2 || num(sale, "items_count") != 2 || sale["first_item_name"] != "Granito Preto" {
		t.Errorf("venda = %v %v %v", sale["number"], sale["items_count"], sale["first_item_name"])
	}
	// Obligation label usa o número.
	recv := e.api.obligation(e.api.receivable(conv["sale_id"].(string)))
	if lbl := recv["origin"].(map[string]any)["label"]; lbl != "Venda nº 2 · Alejandra" {
		t.Errorf("label = %v", lbl)
	}

	// Compra: número e label.
	sup := e.supplier()
	p := e.must("POST", "/purchases", map[string]any{"supplier_id": sup, "kind": "MADE_TO_ORDER"}, 201)
	if num(p, "number") != 1 {
		t.Errorf("purchase number = %v", p["number"])
	}

	// C2: cabeçalho editável só OPEN.
	other := e.customer("Outro Cliente")
	upd := e.must("PUT", "/quotes/"+q2["id"].(string), map[string]any{"customer_id": other, "quote_date": "2026-01-15"}, 200)
	if upd["customer_id"] != other || upd["quote_date"] != "2026-01-15" || upd["customer_name"] != "Outro Cliente" {
		t.Errorf("PUT cabeçalho = %v", upd)
	}
	keep := e.must("PUT", "/quotes/"+q2["id"].(string), map[string]any{"notes": "só notas"}, 200)
	if keep["customer_id"] != other || keep["quote_date"] != "2026-01-15" {
		t.Errorf("ausente deveria manter: %v", keep)
	}
	e.must("PUT", "/quotes/"+q2["id"].(string), map[string]any{"customer_id": "00000000-0000-0000-0000-000000000001"}, 400)
	e.must("PUT", "/quotes/"+q2["id"].(string), map[string]any{"quote_date": "15/01/2026"}, 400)
	e.db.Exec("UPDATE clientes SET deleted_at = NOW() WHERE id = ?", other)
	e.must("PUT", "/quotes/"+q1["id"].(string), map[string]any{"customer_id": other}, 409) // WON
	q3 := e.must("POST", "/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{qItem("X", 1, 1, 1, "")}}, 201)
	e.must("PUT", "/quotes/"+q3["id"].(string), map[string]any{"customer_id": other}, 400) // excluído
}

// C4: filtros de lista.
func TestJourney_ListFilters(t *testing.T) {
	e := newJourneyEnv(t)
	ref := e.namedPartner("Arq. Beatriz")
	maria := e.customer("María Ação")
	e.must("POST", "/quotes", map[string]any{"customer_id": maria, "referrer_id": ref, "items": []map[string]any{qItem("Mármore Carrara", 1, 10, 5, "")}}, 201)
	qOther := e.must("POST", "/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{qItem("Granito", 1, 10, 5, "")}}, 201)

	if ids := e.listIDs("/quotes?referrer_id=" + ref); len(ids) != 1 {
		t.Errorf("quotes referrer = %v", ids)
	}
	e.must("GET", "/quotes?referrer_id=x", nil, 400)
	if ids := e.listIDs("/quotes?q=" + "2"); !contains(ids, qOther["id"].(string)) || len(ids) != 1 {
		t.Errorf("quotes q=nº2 = %v", ids)
	}

	s1 := e.must("POST", "/quotes/quick-sale", map[string]any{"customer_id": maria, "referrer_id": ref, "items": []map[string]any{qItem("Mármore Carrara", 1, 10, 5, "")}}, 201)
	s2 := e.must("POST", "/quotes/quick-sale", map[string]any{"customer_id": e.cid, "items": []map[string]any{qItem("Granito", 1, 10, 5, "")}}, 201)
	id1, id2 := s1["sale_id"].(string), s2["sale_id"].(string)

	cases := []struct {
		query string
		want  []string
	}{
		{"q=maria", []string{id1}}, {"q=MARIA%20ACAO", []string{id1}}, {"q=carrara", []string{id1}},
		{"q=granito", []string{id2}}, {"q=2", []string{id2}}, {"q=%232", []string{id2}}, {"q=zzz", nil},
		{"referrer_id=" + ref, []string{id1}},
	}
	for _, c := range cases {
		got := e.listIDs("/vendas?" + c.query)
		if len(got) != len(c.want) || (len(got) == 1 && got[0] != c.want[0]) {
			t.Errorf("GET /vendas?%s = %v, want %v", c.query, got, c.want)
		}
	}
	e.must("GET", "/vendas?referrer_id=x", nil, 400)
}

// C7 + C8: saldo no Item de estoque e fornecedor habitual.
func TestJourney_StockBalanceAndDefaultSupplier(t *testing.T) {
	e := newJourneyEnv(t)
	prod := e.product()
	saleID, ids := e.sale(qItem("Estoque", 3, 100, 0, prod), qItem("Encomenda", 1, 10, 5, ""))
	e.must("PUT", "/vendas/"+saleID+"/itens/"+ids[0], map[string]any{"produto_id": prod, "produto_nome": "Estoque", "quantidade": 3, "preco_venda_unit": 100, "origin": "FROM_STOCK"}, 200)
	sale := e.must("GET", "/vendas/"+saleID, nil, 200)
	for _, it := range sale["itens"].([]any) {
		it := it.(map[string]any)
		switch it["origin"] {
		case "FROM_STOCK":
			if num(it, "stock_balance") != -3 {
				t.Errorf("stock_balance = %v, want -3", it["stock_balance"])
			}
		default:
			if it["stock_balance"] != nil {
				t.Errorf("stock_balance de encomenda = %v, want null", it["stock_balance"])
			}
		}
	}

	// C8
	sup, sup2 := e.supplier(), e.supplier()
	p := e.must("POST", "/produtos", map[string]any{"nome": uniq("P"), "default_supplier_id": sup}, 201)
	if p["default_supplier_id"] != sup || p["default_supplier_name"] == nil {
		t.Fatalf("create = %v", p)
	}
	id := p["id"].(string)
	// PUT sem o campo mantém.
	kept := e.must("PUT", "/produtos/"+id, map[string]any{"nome": "Renomeado"}, 200)
	if kept["default_supplier_id"] != sup || kept["nome"] != "Renomeado" {
		t.Errorf("ausente deveria manter: %v", kept)
	}
	chg := e.must("PUT", "/produtos/"+id, map[string]any{"nome": "Renomeado", "default_supplier_id": sup2}, 200)
	if chg["default_supplier_id"] != sup2 {
		t.Errorf("troca = %v", chg)
	}
	cleared := e.must("PUT", "/produtos/"+id, map[string]any{"nome": "Renomeado", "default_supplier_id": nil}, 200)
	if cleared["default_supplier_id"] != nil || cleared["default_supplier_name"] != nil {
		t.Errorf("null deveria limpar: %v", cleared)
	}
	e.must("PUT", "/produtos/"+id, map[string]any{"nome": "R", "default_supplier_id": "00000000-0000-0000-0000-000000000001"}, 400)
	e.must("POST", "/produtos", map[string]any{"nome": uniq("P"), "default_supplier_id": "00000000-0000-0000-0000-000000000001"}, 400)
	// Fornecedor excluído: 400 ao definir; manter o já gravado é permitido.
	e.must("PUT", "/produtos/"+id, map[string]any{"nome": "R", "default_supplier_id": sup}, 200)
	e.db.Exec("UPDATE fornecedores SET deleted_at = NOW() WHERE id = ?", sup)
	e.must("PUT", "/produtos/"+id, map[string]any{"nome": "R2", "default_supplier_id": sup}, 200)
	e.must("PUT", "/produtos/"+id, map[string]any{"nome": "R3"}, 200)
	other := e.must("POST", "/produtos", map[string]any{"nome": uniq("P")}, 201)
	e.must("PUT", "/produtos/"+other["id"].(string), map[string]any{"nome": "R", "default_supplier_id": sup}, 400)
}

// registerJourneyHandlers registra os handlers novos do AYD-011 (acrescentados por contrato).
func registerJourneyHandlers(db *gorm.DB, mux *http.ServeMux) {}
