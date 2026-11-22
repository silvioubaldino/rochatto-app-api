package handlers

// C9 (AYD-011): busca global.

import (
	"net/url"
	"testing"
)

type searchResult struct {
	groups []map[string]any
}

func (e fxEnv) search(query string) searchResult {
	e.t.Helper()
	res := e.must("GET", "/search?"+query, nil, 200)
	var out searchResult
	for _, g := range res["groups"].([]any) {
		out.groups = append(out.groups, g.(map[string]any))
	}
	return out
}

func (r searchResult) types() []string {
	var out []string
	for _, g := range r.groups {
		out = append(out, g["type"].(string))
	}
	return out
}

func (r searchResult) items(typ string) []map[string]any {
	for _, g := range r.groups {
		if g["type"] == typ {
			var out []map[string]any
			for _, it := range g["items"].([]any) {
				out = append(out, it.(map[string]any))
			}
			return out
		}
	}
	return nil
}

func TestJourney_GlobalSearch(t *testing.T) {
	e := newJourneyEnv(t)
	maria := e.customer("María Ação")
	e.db.Exec("UPDATE clientes SET cidade = 'Niterói', telefone = '(21) 99999-0000' WHERE id = ?", maria)
	e.customer("Ana Silva")
	e.customer("Joana Silva")
	deleted := e.customer("Mariana Apagada")
	e.db.Exec("UPDATE clientes SET deleted_at = NOW() WHERE id = ?", deleted)

	prod := e.must("POST", "/produtos", map[string]any{"nome": "Mármore Carrara", "unidade": "m2"}, 201)
	svc := e.must("POST", "/services", map[string]any{"name": "Instalação de Mármore", "unit": "m2"}, 201)
	sup := e.must("POST", "/fornecedores", map[string]any{"nome": "Marmoraria Maria", "kind": "STONE", "telefone": "21 3333-4444"}, 201)
	partner := e.must("POST", "/partners", map[string]any{"name": "Arq. Mariana", "type": "ARCHITECT"}, 201)

	q1 := e.must("POST", "/quotes", map[string]any{"customer_id": maria, "items": []map[string]any{qItem("X", 2, 100, 10, "")}}, 201)
	e.must("POST", "/quotes", map[string]any{"customer_id": e.cid, "items": []map[string]any{qItem("Y", 1, 10, 1, "")}}, 201)
	conv := e.must("POST", "/quotes/"+q1["id"].(string)+"/convert", nil, 201)
	pu := e.must("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup["id"]}, 201)

	// Sem acento e sem caixa; todos os tipos na ordem do enum; soft-deletado fora.
	r := e.search("q=" + url.QueryEscape("MARIA"))
	want := []string{"CUSTOMER", "QUOTE", "SALE", "PURCHASE", "PRODUCT", "SERVICE", "SUPPLIER", "PARTNER"}
	got := r.types()
	// PRODUCT e SERVICE têm "Mármore" mas não "maria": ficam fora.
	if len(got) != 6 || got[0] != "CUSTOMER" || got[1] != "QUOTE" || got[2] != "SALE" || got[3] != "PURCHASE" || got[4] != "SUPPLIER" || got[5] != "PARTNER" {
		t.Fatalf("tipos = %v (de %v)", got, want)
	}
	cust := r.items("CUSTOMER")
	if len(cust) != 1 || cust[0]["id"] != maria || cust[0]["title"] != "María Ação" || cust[0]["city"] != "Niterói" || cust[0]["number"] != nil {
		t.Errorf("customer = %v", cust)
	}
	qi := r.items("QUOTE")[0]
	if qi["id"] != q1["id"] || num(qi, "number") != 1 || qi["title"] != "María Ação" || num(qi, "amount") != 200 || qi["status"] != "WON" || qi["city"] != "Niterói" || qi["date"] == nil {
		t.Errorf("quote = %v", qi)
	}
	si := r.items("SALE")[0]
	if si["id"] != conv["sale_id"] || num(si, "number") != 1 || si["status"] != "TO_DELIVER" || num(si, "amount") != 200 {
		t.Errorf("sale = %v", si)
	}
	pi := r.items("PURCHASE")[0]
	if pi["id"] != pu["id"] || pi["title"] != "Marmoraria Maria" || num(pi, "number") != 1 || pi["status"] != "OPEN" || pi["date"] == nil {
		t.Errorf("purchase = %v", pi)
	}
	if k := r.items("SUPPLIER")[0]; k["id"] != sup["id"] || k["kind"] != "STONE" {
		t.Errorf("supplier = %v", k)
	}
	if k := r.items("PARTNER")[0]; k["id"] != partner["id"] || k["kind"] != "ARCHITECT" {
		t.Errorf("partner = %v", k)
	}

	// Produto/serviço: kind = unidade.
	r = e.search("q=marmore&types=SERVICE,PRODUCT")
	if g := r.types(); len(g) != 2 || g[0] != "SERVICE" || g[1] != "PRODUCT" {
		t.Errorf("ordem de types = %v", g)
	}
	if it := r.items("PRODUCT")[0]; it["id"] != prod["id"] || it["kind"] != "m2" {
		t.Errorf("product = %v", it)
	}
	if it := r.items("SERVICE")[0]; it["id"] != svc["id"] {
		t.Errorf("service = %v", it)
	}

	// Número exato (QUOTE/SALE/PURCHASE) e "#n"; telefone (com e sem máscara).
	for _, q := range []string{"%231", "%2301"} { // "#1" e "#01" (q tem ao menos 2 caracteres)
		r = e.search("q=" + q + "&types=QUOTE,SALE,PURCHASE")
		if g := r.types(); len(g) != 3 {
			t.Errorf("q=%s tipos = %v", q, g)
		}
	}
	r = e.search("q=%232&types=QUOTE,SALE,PURCHASE")
	if g := r.types(); len(g) != 1 || g[0] != "QUOTE" || num(r.items("QUOTE")[0], "number") != 2 {
		t.Errorf("q=#2 = %v", r.groups)
	}
	for _, q := range []string{"99999-0000", "999990000", url.QueryEscape("(21) 9999")} {
		if r = e.search("q=" + q + "&types=CUSTOMER"); len(r.items("CUSTOMER")) != 1 {
			t.Errorf("telefone q=%s = %v", q, r.groups)
		}
	}
	if r = e.search("q=3333&types=SUPPLIER"); len(r.items("SUPPLIER")) != 1 {
		t.Errorf("telefone do supplier = %v", r.groups)
	}
	if r = e.search("q=zzzz"); len(r.groups) != 0 {
		t.Errorf("sem resultado deveria vir sem grupos: %v", r.groups)
	}
}

func TestJourney_SearchRankingLimitAndValidation(t *testing.T) {
	e := newJourneyEnv(t)
	e.customer("Joana Silva")
	e.customer("Ana Silva")
	e.customer("Mariana Ana")
	e.customer("100% Pedra")
	r := e.search("q=ana&types=CUSTOMER")
	titles := []string{}
	for _, it := range r.items("CUSTOMER") {
		titles = append(titles, it["title"].(string))
	}
	if len(titles) != 3 || titles[0] != "Ana Silva" {
		t.Errorf("ordem (começa com primeiro) = %v", titles)
	}
	if r = e.search("q=ana&types=CUSTOMER&limit=1"); len(r.items("CUSTOMER")) != 1 {
		t.Errorf("limit=1 = %v", r.groups)
	}
	// Curinga do LIKE é literal.
	if r = e.search("q=" + url.QueryEscape("100%")); len(r.items("CUSTOMER")) != 1 {
		t.Errorf("q=100%% = %v", r.groups)
	}
	if r = e.search("q=" + url.QueryEscape("%%")); len(r.groups) != 0 {
		t.Errorf("q=%%%% não é curinga: %v", r.groups)
	}
	// Número exato primeiro: Quote nº 1 antes de quotes cujo cliente contém "1".
	for _, bad := range []string{"", "q=a", "q=%20a%20", "q=ana&types=FOO", "q=ana&limit=0", "q=ana&limit=11", "q=ana&limit=x"} {
		path := "/search"
		if bad != "" {
			path += "?" + bad
		}
		if code, res := e.api.do("GET", path, nil); code != 400 || res["error"] == nil {
			t.Errorf("GET %s = %d %v, want 400", path, code, res)
		}
	}
}
