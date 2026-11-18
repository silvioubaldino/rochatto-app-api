package handlers

// Testes de integração do AYD-003 (Partner, lead source do Customer, categoria de
// Product) contra um Postgres real. Rodam só com TEST_DATABASE_URL apontando para um banco
// descartável com as migrations 001–005 aplicadas (os testes criam dados com nomes únicos e
// não limpam o banco).
import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newRegistriesEnv(t *testing.T) (apiClient, *gorm.DB) {
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
	NewPartnersHandler(db).Register(mux)
	NewClientesHandler(db).Register(mux)
	NewProductCategoriesHandler(db).Register(mux)
	NewProdutosHandler(db).Register(mux)
	NewQuotesHandler(db).Register(mux)
	return apiClient{t, mux}, db
}

// list faz um GET que devolve um array JSON.
func (c apiClient) list(path string) (int, []map[string]any) {
	c.t.Helper()
	rec := httptest.NewRecorder()
	c.mux.ServeHTTP(rec, httptest.NewRequest("GET", path, bytes.NewReader(nil)))
	var out []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func uniq(prefix string) string { return prefix + " " + uuid.NewString()[:8] }

func names(rows []map[string]any, key string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r[key].(string))
	}
	return out
}

func TestIntegration_Partners(t *testing.T) {
	api, db := newRegistriesEnv(t)
	name := uniq("Ana Arquiteta")

	// Criação (happy path) — contrato em inglês.
	code, p := api.do("POST", "/partners", map[string]any{"name": name, "phone": "11 9999", "type": "ARCHITECT", "notes": "obs"})
	if code != 201 || p["name"] != name || p["type"] != "ARCHITECT" || p["phone"] != "11 9999" || p["notes"] != "obs" || p["created_at"] == nil {
		t.Fatalf("create = %d %v", code, p)
	}
	if _, legacy := p["nome"]; legacy {
		t.Errorf("payload não deve expor campos legados PT: %v", p)
	}
	id := p["id"].(string)

	// Validações → 400.
	for _, body := range []map[string]any{
		{"type": "OTHER"},
		{"name": " ", "type": "OTHER"},
		{"name": "X"},
		{"name": "X", "type": "DESIGNER"},
	} {
		if code, res := api.do("POST", "/partners", body); code != 400 || res["error"] == nil {
			t.Errorf("POST %v = %d %v, want 400 + error", body, code, res)
		}
	}
	if code, _ := api.do("PUT", "/partners/"+id, map[string]any{"name": name}); code != 400 {
		t.Errorf("PUT sem type = %d, want 400", code)
	}

	// Filtros q e type.
	_, rows := api.list("/partners?type=ARCHITECT&q=" + name[len(name)-8:])
	if len(rows) != 1 || rows[0]["id"] != id {
		t.Errorf("filtro type+q = %v", rows)
	}
	if _, rows := api.list("/partners?type=ENGINEER&q=" + name[len(name)-8:]); len(rows) != 0 {
		t.Errorf("filtro type=ENGINEER deveria vir vazio: %v", rows)
	}
	if code, _ := api.list("/partners?type=x"); code != 400 {
		t.Errorf("type inválido no filtro = %d, want 400", code)
	}

	// Get / update.
	if code, g := api.do("GET", "/partners/"+id, nil); code != 200 || g["name"] != name {
		t.Errorf("get = %d %v", code, g)
	}
	code, u := api.do("PUT", "/partners/"+id, map[string]any{"name": name, "type": "BUILDER"})
	if code != 200 || u["type"] != "BUILDER" || u["phone"] != nil {
		t.Errorf("put = %d %v", code, u)
	}
	if code, _ := api.do("GET", "/partners/not-a-uuid", nil); code != 400 {
		t.Errorf("id inválido = %d, want 400", code)
	}
	missing := uuid.NewString()
	if code, _ := api.do("GET", "/partners/"+missing, nil); code != 404 {
		t.Errorf("get inexistente = %d, want 404", code)
	}
	if code, _ := api.do("PUT", "/partners/"+missing, map[string]any{"name": "X", "type": "OTHER"}); code != 404 {
		t.Errorf("put inexistente = %d, want 404", code)
	}

	// Referência existente continua válida (quotes.referrer_id → mesmo registro).
	var cid string
	db.Raw("INSERT INTO clientes(nome) VALUES ('Cliente AYD3') RETURNING id").Scan(&cid)
	if code, q := api.do("POST", "/quotes", map[string]any{"customer_id": cid, "referrer_id": id}); code != 201 || q["referrer_name"] != name {
		t.Errorf("quote com partner = %d %v", code, q)
	}

	// Soft-delete.
	if code, _ := api.do("DELETE", "/partners/"+id, nil); code != 204 {
		t.Errorf("delete = %d, want 204", code)
	}
	if code, _ := api.do("GET", "/partners/"+id, nil); code != 404 {
		t.Errorf("get após delete = %d, want 404", code)
	}
	if code, _ := api.do("DELETE", "/partners/"+id, nil); code != 404 {
		t.Errorf("delete repetido = %d, want 404", code)
	}
	var deleted int64
	db.Raw("SELECT count(*) FROM vendedores_externos WHERE id = ? AND deleted_at IS NOT NULL", id).Scan(&deleted)
	if deleted != 1 {
		t.Errorf("registro deveria continuar na tabela com deleted_at")
	}
}

func TestIntegration_LegacyPartnerDefaultsToOther(t *testing.T) {
	api, db := newRegistriesEnv(t)
	var id string
	db.Raw("INSERT INTO vendedores_externos(nome, telefone, observacoes) VALUES ('Legado', '1', 'o') RETURNING id").Scan(&id)
	code, p := api.do("GET", "/partners/"+id, nil)
	if code != 200 || p["type"] != "OTHER" || p["name"] != "Legado" || p["phone"] != "1" || p["notes"] != "o" {
		t.Errorf("legado = %d %v", code, p)
	}
}

func TestIntegration_CustomerLeadSource(t *testing.T) {
	api, db := newRegistriesEnv(t)
	pname := uniq("Construtora")
	_, partner := api.do("POST", "/partners", map[string]any{"name": pname, "type": "BUILDER"})
	pid := partner["id"].(string)
	_, gone := api.do("POST", "/partners", map[string]any{"name": uniq("Excluído"), "type": "OTHER"})
	goneID := gone["id"].(string)
	api.do("DELETE", "/partners/"+goneID, nil)

	// Happy path: PARTNER + referrer_id → referrer_name na resposta.
	cname := uniq("Cliente Indicado")
	code, c := api.do("POST", "/clientes", map[string]any{"nome": cname, "lead_source": "PARTNER", "referrer_id": pid})
	if code != 201 || c["lead_source"] != "PARTNER" || c["referrer_id"] != pid || c["referrer_name"] != pname || c["nome"] != cname {
		t.Fatalf("create = %d %v", code, c)
	}
	cid := c["id"].(string)

	// Sem lead source (Customers antigos) e com lead source sem Partner.
	if code, c2 := api.do("POST", "/clientes", map[string]any{"nome": uniq("Sem origem")}); code != 201 || c2["lead_source"] != nil || c2["referrer_id"] != nil || c2["referrer_name"] != nil {
		t.Errorf("sem lead_source = %d %v", code, c2)
	}
	if code, _ := api.do("POST", "/clientes", map[string]any{"nome": uniq("Insta"), "lead_source": "INSTAGRAM"}); code != 201 {
		t.Errorf("INSTAGRAM = %d, want 201", code)
	}

	// Erros do AYD-003 → 400.
	cases := map[string]map[string]any{
		"PARTNER sem referrer_id":         {"nome": "X", "lead_source": "PARTNER"},
		"referrer_id com INSTAGRAM":       {"nome": "X", "lead_source": "INSTAGRAM", "referrer_id": pid},
		"referrer_id com lead null":       {"nome": "X", "referrer_id": pid},
		"referrer_id inexistente":         {"nome": "X", "lead_source": "PARTNER", "referrer_id": uuid.NewString()},
		"referrer_id de Partner excluído": {"nome": "X", "lead_source": "PARTNER", "referrer_id": goneID},
		"lead_source inválido":            {"nome": "X", "lead_source": "TIKTOK"},
	}
	for name, body := range cases {
		if code, res := api.do("POST", "/clientes", body); code != 400 || res["error"] == nil {
			t.Errorf("%s: POST = %d %v, want 400", name, code, res)
		}
	}
	if code, _ := api.do("PUT", "/clientes/"+cid, map[string]any{"nome": cname, "lead_source": "PARTNER"}); code != 400 {
		t.Errorf("PUT PARTNER sem referrer = %d, want 400", code)
	}

	// Filtros.
	_, rows := api.list("/clientes?lead_source=PARTNER&referrer_id=" + pid)
	if len(rows) != 1 || rows[0]["id"] != cid || rows[0]["referrer_name"] != pname {
		t.Errorf("filtro lead_source+referrer_id = %v", rows)
	}
	if _, rows := api.list("/clientes?lead_source=INSTAGRAM&referrer_id=" + pid); len(rows) != 0 {
		t.Errorf("filtro combinado deveria vir vazio: %v", rows)
	}
	if code, _ := api.list("/clientes?lead_source=x"); code != 400 {
		t.Errorf("lead_source inválido no filtro = %d, want 400", code)
	}
	if code, _ := api.list("/clientes?referrer_id=x"); code != 400 {
		t.Errorf("referrer_id inválido no filtro = %d, want 400", code)
	}
	if _, rows := api.list("/clientes/search?q=" + cname[len(cname)-8:]); len(rows) != 1 || rows[0]["referrer_name"] != pname {
		t.Errorf("search = %v", rows)
	}

	// Partner excluído depois: o Customer mantém o vínculo e o nome; PUT mantendo o mesmo
	// referrer_id continua válido.
	api.do("DELETE", "/partners/"+pid, nil)
	if code, g := api.do("GET", "/clientes/"+cid, nil); code != 200 || g["referrer_name"] != pname {
		t.Errorf("get com partner excluído = %d %v", code, g)
	}
	if code, u := api.do("PUT", "/clientes/"+cid, map[string]any{"nome": cname, "cidade": "Curitiba", "lead_source": "PARTNER", "referrer_id": pid}); code != 200 || u["cidade"] != "Curitiba" || u["referrer_name"] != pname {
		t.Errorf("PUT mantendo partner excluído = %d %v", code, u)
	}

	// Trocar para outra origem limpa o Partner.
	code, u := api.do("PUT", "/clientes/"+cid, map[string]any{"nome": cname, "lead_source": "REFERRAL"})
	if code != 200 || u["lead_source"] != "REFERRAL" || u["referrer_id"] != nil || u["referrer_name"] != nil {
		t.Errorf("PUT REFERRAL = %d %v", code, u)
	}

	// O CHECK do banco garante a regra nas duas direções.
	if err := db.Exec("INSERT INTO clientes(nome, referrer_id) VALUES ('x', ?)", pid).Error; err == nil {
		t.Errorf("CHECK deveria rejeitar referrer_id com lead_source NULL")
	}
	if err := db.Exec("INSERT INTO clientes(nome, lead_source) VALUES ('x', 'PARTNER')").Error; err == nil {
		t.Errorf("CHECK deveria rejeitar PARTNER sem referrer_id")
	}
}

func TestIntegration_ProductCategories(t *testing.T) {
	api, _ := newRegistriesEnv(t)
	suffix := uuid.NewString()[:8]
	nameB := "zz Mármore " + suffix
	nameA := "zz granito " + suffix

	code, b := api.do("POST", "/product-categories", map[string]any{"name": nameB})
	if code != 201 || b["name"] != nameB || b["id"] == nil || b["created_at"] == nil {
		t.Fatalf("create = %d %v", code, b)
	}
	code, a := api.do("POST", "/product-categories", map[string]any{"name": "  " + nameA + "  "})
	if code != 201 || a["name"] != nameA {
		t.Fatalf("create (trim) = %d %v", code, a)
	}
	aID, bID := a["id"].(string), b["id"].(string)

	// Duplicado case-insensitive → 409; vazio → 400.
	if code, res := api.do("POST", "/product-categories", map[string]any{"name": "ZZ GRANITO " + suffix}); code != 409 || res["error"] == nil {
		t.Errorf("duplicado = %d %v, want 409", code, res)
	}
	if code, _ := api.do("POST", "/product-categories", map[string]any{"name": " "}); code != 400 {
		t.Errorf("nome vazio = %d, want 400", code)
	}

	// Lista em ordem alfabética (ignorando caixa).
	_, rows := api.list("/product-categories")
	var mine []string
	for _, n := range names(rows, "name") {
		if n == nameA || n == nameB {
			mine = append(mine, n)
		}
	}
	if len(mine) != 2 || mine[0] != nameA || mine[1] != nameB {
		t.Errorf("ordem = %v, want [%s %s]", mine, nameA, nameB)
	}

	// PUT: renomear (inclusive só a caixa), duplicado → 409, inexistente → 404.
	if code, u := api.do("PUT", "/product-categories/"+aID, map[string]any{"name": "ZZ Granito " + suffix}); code != 200 || u["name"] != "ZZ Granito "+suffix {
		t.Errorf("rename caixa = %d %v", code, u)
	}
	if code, _ := api.do("PUT", "/product-categories/"+aID, map[string]any{"name": nameB}); code != 409 {
		t.Errorf("rename duplicado = %d, want 409", code)
	}
	if code, _ := api.do("PUT", "/product-categories/"+uuid.NewString(), map[string]any{"name": uniq("x")}); code != 404 {
		t.Errorf("put inexistente = %d, want 404", code)
	}
	if code, _ := api.do("PUT", "/product-categories/"+aID, map[string]any{"name": ""}); code != 400 {
		t.Errorf("put nome vazio = %d, want 400", code)
	}

	// Soft-delete: some da lista, libera o nome, 404 ao repetir.
	if code, _ := api.do("DELETE", "/product-categories/"+bID, nil); code != 204 {
		t.Errorf("delete = %d, want 204", code)
	}
	if code, _ := api.do("DELETE", "/product-categories/"+bID, nil); code != 404 {
		t.Errorf("delete repetido = %d, want 404", code)
	}
	if code, _ := api.do("PUT", "/product-categories/"+bID, map[string]any{"name": uniq("y")}); code != 404 {
		t.Errorf("put em excluída = %d, want 404", code)
	}
	_, rows = api.list("/product-categories")
	for _, r := range rows {
		if r["id"] == bID {
			t.Errorf("categoria excluída na lista")
		}
	}
	if code, _ := api.do("POST", "/product-categories", map[string]any{"name": nameB}); code != 201 {
		t.Errorf("recriar nome de categoria excluída = %d, want 201", code)
	}
}

func TestIntegration_ProductCategoryOnProdutos(t *testing.T) {
	api, _ := newRegistriesEnv(t)
	catName := uniq("Quartzito")
	_, cat := api.do("POST", "/product-categories", map[string]any{"name": catName})
	catID := cat["id"].(string)
	_, other := api.do("POST", "/product-categories", map[string]any{"name": uniq("Outra")})
	otherID := other["id"].(string)

	// Happy path: category_id + category_name; unidade segue texto livre.
	pname := uniq("Taj Mahal")
	code, p := api.do("POST", "/produtos", map[string]any{"nome": pname, "unidade": "m²", "category_id": catID})
	if code != 201 || p["category_id"] != catID || p["category_name"] != catName || p["unidade"] != "m²" {
		t.Fatalf("create = %d %v", code, p)
	}
	pid := p["id"].(string)
	if code, p2 := api.do("POST", "/produtos", map[string]any{"nome": uniq("Sem categoria")}); code != 201 || p2["category_id"] != nil || p2["category_name"] != nil {
		t.Errorf("sem categoria = %d %v", code, p2)
	}

	// category_id inexistente → 400.
	if code, _ := api.do("POST", "/produtos", map[string]any{"nome": "X", "category_id": uuid.NewString()}); code != 400 {
		t.Errorf("categoria inexistente = %d, want 400", code)
	}

	// Filtro category_id.
	_, rows := api.list("/produtos?category_id=" + catID)
	if len(rows) != 1 || rows[0]["id"] != pid || rows[0]["category_name"] != catName {
		t.Errorf("filtro category_id = %v", rows)
	}
	if code, _ := api.list("/produtos?category_id=x"); code != 400 {
		t.Errorf("category_id inválido no filtro = %d, want 400", code)
	}

	// Categoria excluída: Product mantém vínculo e nome; não pode ser atribuída de novo.
	api.do("DELETE", "/product-categories/"+catID, nil)
	if code, g := api.do("GET", "/produtos/"+pid, nil); code != 200 || g["category_id"] != catID || g["category_name"] != catName {
		t.Errorf("get com categoria excluída = %d %v", code, g)
	}
	if code, _ := api.do("POST", "/produtos", map[string]any{"nome": "X", "category_id": catID}); code != 400 {
		t.Errorf("create com categoria excluída = %d, want 400", code)
	}
	if code, u := api.do("PUT", "/produtos/"+pid, map[string]any{"nome": pname, "unidade": "chapa", "category_id": catID}); code != 200 || u["unidade"] != "chapa" || u["category_name"] != catName {
		t.Errorf("PUT mantendo categoria excluída = %d %v", code, u)
	}
	if code, u := api.do("PUT", "/produtos/"+pid, map[string]any{"nome": pname, "category_id": otherID}); code != 200 || u["category_id"] != otherID {
		t.Errorf("PUT trocando categoria = %d %v", code, u)
	}
	if code, _ := api.do("PUT", "/produtos/"+pid, map[string]any{"nome": pname, "category_id": catID}); code != 400 {
		t.Errorf("PUT voltando para categoria excluída = %d, want 400", code)
	}
	if code, _ := api.do("PUT", "/produtos/"+pid, map[string]any{"nome": pname, "category_id": uuid.NewString()}); code != 400 {
		t.Errorf("PUT categoria inexistente = %d, want 400", code)
	}
}
