package tools_test

// Testes de integração das ferramentas de leitura (AYD-006 §4) contra um Postgres real:
// cada ferramenta é executada pelo pipeline real (scope, validação, auditoria), pelo
// Gateway em processo, sobre os handlers REST de verdade — e a saída é validada contra o
// outputSchema. Rodam só com TEST_DATABASE_URL.
import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/silvioubaldino/sales-backend/internal/handlers"
	"github.com/silvioubaldino/sales-backend/internal/mcp"
	"github.com/silvioubaldino/sales-backend/internal/mcp/gateway"
	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
	"github.com/silvioubaldino/sales-backend/internal/oauth"
	"github.com/silvioubaldino/sales-backend/internal/testdb"
	"gorm.io/gorm"
)

type env struct {
	t      *testing.T
	db     *gorm.DB
	mux    *http.ServeMux
	pipe   *mcp.Pipeline
	pr     *oauth.Principal
	byName map[string]toolkit.Tool
	audit  *mcp.DBAuditor
	skew   time.Duration // adianta o relógio da deduplicação (simula "depois de 120 s")
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := testdb.New(t)
	mux := http.NewServeMux()
	handlers.NewClientesHandler(db).Register(mux)
	handlers.NewProdutosHandler(db).Register(mux)
	handlers.NewFornecedoresHandler(db).Register(mux)
	handlers.NewPartnersHandler(db).Register(mux)
	handlers.NewProductCategoriesHandler(db).Register(mux)
	handlers.NewVendasHandler(db).Register(mux)
	handlers.NewQuotesHandler(db).Register(mux)
	handlers.NewObligationsHandler(db).Register(mux)
	handlers.NewSettlementsHandler(db).Register(mux)
	handlers.NewFinancialMovementsHandler(db).Register(mux)
	handlers.NewPurchasesHandler(db).Register(mux)
	handlers.NewStockHandler(db).Register(mux)
	handlers.NewDashboardHandler(db).Register(mux)
	handlers.NewFinanceHandler(db).Register(mux)

	audit := mcp.NewDBAuditor(db)
	e := &env{t: t, db: db, mux: mux, byName: map[string]toolkit.Tool{}, audit: audit}
	deduper := mcp.NewDBDeduper(db, func() time.Time { return time.Now().Add(e.skew) })
	pipe, err := mcp.NewPipeline(mcp.PipelineOptions{Gateway: gateway.New(mux), Audit: audit, Deduper: deduper, ValidateOutput: true}, mcp.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	e.pipe = pipe
	for _, tool := range mcp.Catalog() {
		e.byName[tool.Meta.Name] = tool
	}
	// A Connection precisa existir (FK da auditoria).
	db.Exec(`INSERT INTO oauth_clients(client_id, redirect_uris) VALUES ('c', '{}')`)
	e.pr = e.newConnection("read", "write")
	return e
}

// newConnection cria uma Connection (FK da auditoria) e devolve o Principal correspondente.
func (e *env) newConnection(scopes ...string) *oauth.Principal {
	e.t.Helper()
	var connID string
	if err := e.db.Raw(`INSERT INTO oauth_connections(client_id, owner_uid, scopes, resource) VALUES ('c', 'u', ?, 'r') RETURNING id::text`,
		"{"+strings.Join(scopes, ",")+"}").Scan(&connID).Error; err != nil {
		e.t.Fatal(err)
	}
	return &oauth.Principal{ConnectionID: uuid.MustParse(connID), Scopes: scopes}
}

// rest chama a rota REST diretamente (para semear e para comparar paridade).
func (e *env) rest(method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewReader(b)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *env) mustREST(method, path string, body any, want int) map[string]any {
	e.t.Helper()
	code, out := e.rest(method, path, body)
	if code != want {
		e.t.Fatalf("%s %s = %d %v", method, path, code, out)
	}
	return out
}

// call executa a ferramenta pelo pipeline e devolve (structuredContent, isError).
func (e *env) call(name string, args any) (map[string]any, bool) {
	e.t.Helper()
	tool, ok := e.byName[name]
	if !ok {
		e.t.Fatalf("ferramenta %s não existe", name)
	}
	raw, _ := json.Marshal(args)
	res := e.pipe.Call(context.Background(), tool, e.pr, raw)
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		e.t.Fatalf("structuredContent de %s: %s", name, b)
	}
	return out, res.IsError
}

// callRaw executa com os argumentos como texto JSON (controla a ordem das chaves) e como a
// Connection pr.
func (e *env) callRaw(pr *oauth.Principal, name, args string) (map[string]any, bool) {
	e.t.Helper()
	res := e.pipe.Call(context.Background(), e.byName[name], pr, json.RawMessage(args))
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, res.IsError
}

func (e *env) ok(name string, args any) map[string]any {
	e.t.Helper()
	out, isErr := e.call(name, args)
	if isErr {
		e.t.Fatalf("%s(%v) falhou: %v", name, args, out)
	}
	return out
}

func (e *env) fail(name string, args any, code, msg string) {
	e.t.Helper()
	out, isErr := e.call(name, args)
	er, _ := out["error"].(map[string]any)
	if !isErr || (code != "" && er["code"] != code) || (msg != "" && !strings.Contains(er["message"].(string), msg)) {
		e.t.Fatalf("%s(%v) = %v, quero erro %s %q", name, args, out, code, msg)
	}
}

func items(out map[string]any) []any { return out["items"].([]any) }
func obj(v any) map[string]any       { return v.(map[string]any) }

func day(offset int) string { return time.Now().AddDate(0, 0, offset).Format("2006-01-02") }

type seed struct {
	catID, partnerID, joao, joaoPereira, maria       string
	pedraID, mármoreID, supplierID                   string
	quoteOpen, quoteLost, quoteWon                   string
	saleQuick, saleConv                              string
	saleQuickItem, saleConvItem1, saleConvItem2      string
	receiptRecv, receiptPending, saleQuickReceivable string
	purchaseID                                       string
}

func (e *env) seed() seed {
	var s seed
	s.catID = e.mustREST("POST", "/product-categories", map[string]any{"name": "Granito"}, 201)["id"].(string)
	s.partnerID = e.mustREST("POST", "/partners", map[string]any{"name": "Arq. Ana", "type": "ARCHITECT", "phone": "11 9999"}, 201)["id"].(string)
	s.joao = e.mustREST("POST", "/clientes", map[string]any{"nome": "João Silva", "telefone": "11 1111", "cidade": "Campinas",
		"lead_source": "PARTNER", "referrer_id": s.partnerID, "observacoes": "obra no centro"}, 201)["id"].(string)
	s.joaoPereira = e.mustREST("POST", "/clientes", map[string]any{"nome": "Joao Pereira", "lead_source": "INSTAGRAM"}, 201)["id"].(string)
	s.maria = e.mustREST("POST", "/clientes", map[string]any{"nome": "Maria Souza"}, 201)["id"].(string)
	s.pedraID = e.mustREST("POST", "/produtos", map[string]any{"nome": "Granito Preto", "unidade": "m2",
		"preco_referencia": 500, "custo_referencia": 300, "category_id": s.catID}, 201)["id"].(string)
	s.mármoreID = e.mustREST("POST", "/produtos", map[string]any{"nome": "Mármore Carrara", "unidade": "m2"}, 201)["id"].(string)
	s.supplierID = e.mustREST("POST", "/fornecedores", map[string]any{"nome": "Pedras São José", "cidade": "Cachoeiro"}, 201)["id"].(string)
	e.mustREST("POST", "/fornecedores", map[string]any{"nome": "Mineração Zé"}, 201)

	// Quotes: aberto, perdido, e uma venda convertida.
	qItems := func(pid string, qty, price, cost float64) []map[string]any {
		return []map[string]any{{"product_id": pid, "product_name": "Granito Preto", "quantity": qty, "unit_price": price, "unit_cost": cost}}
	}
	s.quoteOpen = e.mustREST("POST", "/quotes", map[string]any{"customer_id": s.joao, "referrer_id": s.partnerID, "notes": "primeiro", "items": qItems(s.pedraID, 10, 500, 300)}, 201)["id"].(string)
	s.quoteLost = e.mustREST("POST", "/quotes", map[string]any{"customer_id": s.maria, "items": qItems(s.pedraID, 1, 100, 60)}, 201)["id"].(string)
	e.mustREST("PATCH", "/quotes/"+s.quoteLost+"/status", map[string]any{"status": "LOST", "lost_reason": "preço"}, 200)
	s.quoteWon = e.mustREST("POST", "/quotes", map[string]any{"customer_id": s.maria, "items": []map[string]any{
		{"product_name": "Granito sob encomenda", "quantity": 2, "unit_price": 1000, "unit_cost": 600},
		{"product_name": "Mármore do estoque", "product_id": s.mármoreID, "quantity": 1, "unit_price": 800, "unit_cost": 500},
	}}, 201)["id"].(string)
	s.saleConv = e.mustREST("POST", "/quotes/"+s.quoteWon+"/convert", nil, 201)["sale_id"].(string)

	qs := e.mustREST("POST", "/quotes/quick-sale", map[string]any{"customer_id": s.joao, "items": []map[string]any{
		{"product_id": s.pedraID, "product_name": "Granito Preto", "quantity": 4, "unit_price": 500, "unit_cost": 300}}}, 201)
	s.saleQuick = qs["sale_id"].(string)

	sale := e.mustREST("GET", "/vendas/"+s.saleQuick, nil, 200)
	s.saleQuickItem = obj(sale["itens"].([]any)[0])["id"].(string)
	conv := e.mustREST("GET", "/vendas/"+s.saleConv, nil, 200)
	s.saleConvItem1 = obj(conv["itens"].([]any)[0])["id"].(string)
	s.saleConvItem2 = obj(conv["itens"].([]any)[1])["id"].(string)

	// Item 1 da venda convertida: sob encomenda com Supplier (fila de compra); item 2: do estoque.
	put := func(saleID, itemID string, body map[string]any) {
		e.mustREST("PUT", "/vendas/"+saleID+"/itens/"+itemID, body, 200)
	}
	put(s.saleConv, s.saleConvItem1, map[string]any{"produto_nome": "Granito sob encomenda", "quantidade": 2, "preco_venda_unit": 1000, "custo_unit": 600,
		"origin": "MADE_TO_ORDER", "fornecedor_id": s.supplierID})
	e.mustREST("POST", "/stock/adjustments", map[string]any{"product_id": s.mármoreID, "quantity": 5, "unit_cost": 450, "reason": "INITIAL", "adjusted_at": "2026-01-01"}, 201)
	put(s.saleConv, s.saleConvItem2, map[string]any{"produto_id": s.mármoreID, "produto_nome": "Mármore do estoque", "quantidade": 1, "preco_venda_unit": 800, "origin": "FROM_STOCK"})
	put(s.saleQuick, s.saleQuickItem, map[string]any{"produto_id": s.pedraID, "produto_nome": "Granito Preto", "quantidade": 4, "preco_venda_unit": 500, "custo_unit": 300,
		"origin": "MADE_TO_ORDER", "fornecedor_id": s.supplierID})

	// Conta a receber da venda rápida: uma parcela de 700 vencida (gera alerta OVERDUE) e um
	// recebimento de 500 (PIX) que cobre a parcela pela metade (o quitado cobre as mais antigas).
	s.saleQuickReceivable = e.mustREST("GET", "/obligations?origin_type=SALE&origin_id="+s.saleQuick+"&direction=RECEIVABLE", nil, 200)["items"].([]any)[0].(map[string]any)["id"].(string)
	plan := e.mustREST("PUT", "/obligations/"+s.saleQuickReceivable+"/installments",
		map[string]any{"installments": []map[string]any{{"due_date": day(-3), "amount": 700}}}, 200)
	s.receiptPending = obj(plan["installments"].([]any)[0])["id"].(string)
	paid := e.mustREST("POST", "/obligations/"+s.saleQuickReceivable+"/settle",
		map[string]any{"amount": 500, "movement": map[string]any{"occurred_on": day(0), "payment_method": "PIX"}}, 201)
	s.receiptRecv = obj(obj(paid["movement"])["settlements"].([]any)[0])["id"].(string)

	// Purchase consolidada com o item sob encomenda da venda rápida.
	s.purchaseID = e.mustREST("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": s.supplierID,
		"expected_date": "2026-12-01", "sale_item_ids": []string{s.saleQuickItem}}, 201)["id"].(string)
	return s
}

func TestIntegration_CatalogReadTools(t *testing.T) {
	e := newEnv(t)
	s := e.seed()

	t.Run("search_customers: sem acento/caixa, filtros e paginação", func(t *testing.T) {
		out := e.ok("search_customers", map[string]any{"query": "JOAO"})
		if out["total"] != float64(2) || len(items(out)) != 2 || out["has_more"] != false || out["offset"] != float64(0) {
			t.Fatalf("query 'JOAO' = %v", out)
		}
		first := obj(items(out)[0])
		if first["name"] != "Joao Pereira" && first["name"] != "João Silva" {
			t.Errorf("nomes = %v", first)
		}
		out = e.ok("search_customers", map[string]any{"query": "silva"})
		c := obj(items(out)[0])
		if c["id"] != s.joao || c["phone"] != "11 1111" || c["city"] != "Campinas" || c["lead_source"] != "PARTNER" ||
			c["referrer_id"] != s.partnerID || c["referrer_name"] != "Arq. Ana" || c["notes"] != "obra no centro" {
			t.Errorf("customer EN = %v", c)
		}
		if _, has := c["email"]; has {
			t.Error("campo nulo deve ser omitido, não null")
		}
		out = e.ok("search_customers", map[string]any{"lead_source": "INSTAGRAM"})
		if out["total"] != float64(1) || obj(items(out)[0])["name"] != "Joao Pereira" {
			t.Errorf("filtro lead_source = %v", out)
		}
		out = e.ok("search_customers", map[string]any{"referrer_id": s.partnerID})
		if out["total"] != float64(1) {
			t.Errorf("filtro referrer_id = %v", out)
		}
		// Paginação (§3.4).
		out = e.ok("search_customers", map[string]any{"limit": 2})
		if len(items(out)) != 2 || out["total"] != float64(3) || out["has_more"] != true {
			t.Errorf("limit 2 = %v", out)
		}
		out = e.ok("search_customers", map[string]any{"limit": 2, "offset": 2})
		if len(items(out)) != 1 || out["has_more"] != false || out["offset"] != float64(2) {
			t.Errorf("offset 2 = %v", out)
		}
		e.fail("search_customers", map[string]any{"limit": 101}, "INVALID_INPUT", "argumentos inválidos")
		e.fail("search_customers", map[string]any{"limit": 0}, "INVALID_INPUT", "")
		e.fail("search_customers", map[string]any{"lead_source": "TV"}, "INVALID_INPUT", "")
		e.fail("search_customers", map[string]any{"nome": "x"}, "INVALID_INPUT", "")
	})

	t.Run("get_customer: dados e 10 vendas mais recentes", func(t *testing.T) {
		out := e.ok("get_customer", map[string]any{"customer_id": s.joao})
		if obj(out["customer"])["name"] != "João Silva" {
			t.Errorf("customer = %v", out["customer"])
		}
		sales := out["recent_sales"].([]any)
		if len(sales) != 1 || obj(sales[0])["id"] != s.saleQuick {
			t.Errorf("recent_sales = %v", sales)
		}
		e.fail("get_customer", map[string]any{"customer_id": uuid.NewString()}, "NOT_FOUND", "registro não encontrado")
		e.fail("get_customer", map[string]any{"customer_id": "não-é-uuid"}, "", "") // 400 da API ou do schema
	})

	t.Run("search_products e search_suppliers e search_partners", func(t *testing.T) {
		out := e.ok("search_products", map[string]any{"query": "marmore"})
		p := obj(items(out)[0])
		if out["total"] != float64(1) || p["name"] != "Mármore Carrara" || p["unit"] != "m2" {
			t.Errorf("search_products = %v", out)
		}
		out = e.ok("search_products", map[string]any{"category_id": s.catID})
		p = obj(items(out)[0])
		if out["total"] != float64(1) || p["category_name"] != "Granito" || p["reference_price"] != float64(500) || p["reference_cost"] != float64(300) {
			t.Errorf("categoria = %v", out)
		}
		out = e.ok("search_suppliers", map[string]any{"query": "sao jose"})
		sp := obj(items(out)[0])
		if out["total"] != float64(1) || sp["name"] != "Pedras São José" || sp["city"] != "Cachoeiro" {
			t.Errorf("search_suppliers = %v", out)
		}
		if out := e.ok("search_suppliers", map[string]any{}); out["total"] != float64(2) {
			t.Errorf("todos os suppliers = %v", out)
		}
		// AYD-007: kind (STONE por padrão) na saída e como filtro.
		if sp["kind"] != "STONE" {
			t.Errorf("kind do fornecedor = %v, quero STONE", sp["kind"])
		}
		e.mustREST("POST", "/fornecedores", map[string]any{"nome": "Transportes Rápido", "kind": "TRANSPORT"}, 201)
		if out := e.ok("search_suppliers", map[string]any{"kind": "TRANSPORT"}); out["total"] != float64(1) || obj(items(out)[0])["kind"] != "TRANSPORT" {
			t.Errorf("search_suppliers kind=TRANSPORT = %v", out)
		}
		e.fail("search_suppliers", map[string]any{"kind": "NOPE"}, "INVALID_INPUT", "")
		out = e.ok("search_partners", map[string]any{"query": "ana", "type": "ARCHITECT"})
		pp := obj(items(out)[0])
		if out["total"] != float64(1) || pp["name"] != "Arq. Ana" || pp["type"] != "ARCHITECT" || pp["phone"] != "11 9999" {
			t.Errorf("search_partners = %v", out)
		}
		if out := e.ok("search_partners", map[string]any{"type": "BUILDER"}); out["total"] != float64(0) || len(items(out)) != 0 {
			t.Errorf("tipo sem resultado = %v", out)
		}
	})

	t.Run("quotes", func(t *testing.T) {
		out := e.ok("list_quotes", map[string]any{"status": "OPEN"})
		if out["total"] != float64(1) {
			t.Fatalf("list_quotes OPEN = %v", out)
		}
		q := obj(items(out)[0])
		if q["id"] != s.quoteOpen || q["customer_name"] != "João Silva" || q["referrer_name"] != "Arq. Ana" || q["total_quote"] != float64(5000) ||
			q["status"] != "OPEN" || q["quick_sale"] != false {
			t.Errorf("quote summary = %v", q)
		}
		if _, has := q["items"]; has {
			t.Error("list_quotes não traz items")
		}
		out = e.ok("list_quotes", map[string]any{"status": "LOST"})
		if l := obj(items(out)[0]); l["lost_reason"] != "preço" {
			t.Errorf("lost_reason = %v", l)
		}
		if out := e.ok("list_quotes", map[string]any{"customer_id": s.maria}); out["total"] != float64(2) {
			t.Errorf("por cliente = %v", out)
		}
		if out := e.ok("list_quotes", map[string]any{"query": "primeiro"}); out["total"] != float64(1) {
			t.Errorf("por texto = %v", out)
		}
		if out := e.ok("list_quotes", map[string]any{"from": "2999-01-01"}); out["total"] != float64(0) {
			t.Errorf("por período = %v", out)
		}
		qd := obj(e.ok("get_quote", map[string]any{"quote_id": s.quoteOpen})["quote"])
		it := obj(qd["items"].([]any)[0])
		if qd["id"] != s.quoteOpen || it["product_name"] != "Granito Preto" || it["quantity"] != float64(10) || it["unit_price"] != float64(500) ||
			it["unit_cost"] != float64(300) || it["total_price"] != float64(5000) || it["product_id"] != s.pedraID {
			t.Errorf("get_quote = %v", qd)
		}
		e.fail("get_quote", map[string]any{"quote_id": uuid.NewString()}, "NOT_FOUND", "quote não encontrado")
	})
}

func TestIntegration_SalesProcurementStockReadTools(t *testing.T) {
	e := newEnv(t)
	s := e.seed()

	t.Run("list_sales e get_sale", func(t *testing.T) {
		out := e.ok("list_sales", map[string]any{})
		if out["total"] != float64(2) {
			t.Fatalf("list_sales = %v", out)
		}
		out = e.ok("list_sales", map[string]any{"customer_id": s.joao})
		sale := obj(items(out)[0])
		want := map[string]any{
			"id": s.saleQuick, "customer_id": s.joao, "customer_name": "João Silva", "delivery_status": "TO_DELIVER", "payment_status": "PARTIAL",
			"total": float64(2000), "total_cost": float64(1200), "total_received": float64(500), "receivable": float64(1500), "profit": float64(800),
			"freight_amount": float64(0), "freight_payable": float64(0), "commission_amount": float64(0), "commission_paid": false,
		}
		for k, v := range want {
			if sale[k] != v {
				t.Errorf("sale.%s = %v, quero %v", k, sale[k], v)
			}
		}
		if _, has := sale["a_receber"]; has {
			t.Error("campo legado a_receber não pode ser exposto")
		}
		if out := e.ok("list_sales", map[string]any{"payment_status": []string{"PENDING"}}); out["total"] != float64(1) || obj(items(out)[0])["id"] != s.saleConv {
			t.Errorf("payment_status PENDING = %v", out)
		}
		if out := e.ok("list_sales", map[string]any{"payment_status": []string{"PENDING", "PARTIAL"}, "delivery_status": []string{"TO_DELIVER"}}); out["total"] != float64(2) {
			t.Errorf("filtros múltiplos = %v", out)
		}
		if out := e.ok("list_sales", map[string]any{"delivery_status": []string{"CANCELED"}}); out["total"] != float64(0) {
			t.Errorf("CANCELED = %v", out)
		}
		if out := e.ok("list_sales", map[string]any{"from": day(1)}); out["total"] != float64(0) {
			t.Errorf("from no futuro = %v", out)
		}
		if out := e.ok("list_sales", map[string]any{"to": day(0), "from": day(0)}); out["total"] != float64(2) {
			t.Errorf("hoje = %v", out)
		}
		e.fail("list_sales", map[string]any{"delivery_status": []string{"X"}}, "INVALID_INPUT", "")

		d := obj(e.ok("get_sale", map[string]any{"sale_id": s.saleQuick})["sale"])
		if d["id"] != s.saleQuick || d["quote_id"] == nil {
			t.Errorf("get_sale = %v", d)
		}
		itm := obj(d["items"].([]any)[0])
		if itm["origin"] != "MADE_TO_ORDER" || itm["supplier_id"] != s.supplierID || itm["supplier_name"] != nil && itm["supplier_name"] == "" ||
			itm["purchase_id"] != s.purchaseID || itm["product_name"] != "Granito Preto" || itm["quantity"] != float64(4) || itm["unit_price"] != float64(500) {
			t.Errorf("item = %v", itm)
		}
		recs := d["receipts"].([]any)
		if len(recs) != 2 {
			t.Fatalf("receipts = %v", recs)
		}
		byID := map[string]map[string]any{}
		for _, r := range recs {
			byID[obj(r)["id"].(string)] = obj(r)
		}
		rr, rp := byID[s.receiptRecv], byID[s.receiptPending]
		if rr["status"] != "RECEIVED" || rr["amount"] != float64(500) || rr["payment_method"] != "PIX" || rr["paid_at"] != day(0) {
			t.Errorf("receipt recebido = %v", rr)
		}
		// A parcela de 700 está coberta em 500 pelo recebimento: pendente pelo que falta.
		if rp["status"] != "PENDING" || rp["scheduled_date"] != day(-3) || rp["amount"] != float64(200) {
			t.Errorf("receipt pendente = %v", rp)
		}
		if _, has := rp["payment_method"]; has {
			t.Error("payment_method nulo deve ser omitido")
		}
		e.fail("get_sale", map[string]any{"sale_id": uuid.NewString()}, "NOT_FOUND", "venda não encontrada")
	})

	t.Run("procurement", func(t *testing.T) {
		out := e.ok("list_pending_purchase_items", map[string]any{})
		groups := out["groups"].([]any)
		if len(groups) != 1 {
			t.Fatalf("grupos = %v", out)
		}
		g := obj(groups[0])
		// O item da venda convertida (sob encomenda COM Supplier) é o único pendente.
		pi := obj(g["items"].([]any)[0])
		if g["supplier_id"] != s.supplierID || pi["id"] != s.saleConvItem1 || pi["customer_name"] != "Maria Souza" || pi["product_name"] != "Granito sob encomenda" ||
			pi["quantity"] != float64(2) || pi["unit_cost"] != float64(600) || pi["sale_id"] != s.saleConv {
			t.Errorf("pending = %v", g)
		}
		if out := e.ok("list_pending_purchase_items", map[string]any{"supplier_id": uuid.NewString()}); len(out["groups"].([]any)) != 0 {
			t.Errorf("filtro por outro supplier = %v", out)
		}
		if out := e.ok("list_pending_purchase_items", map[string]any{"supplier_id": s.supplierID}); len(out["groups"].([]any)) != 1 {
			t.Errorf("filtro por supplier = %v", out)
		}

		lp := e.ok("list_purchases", map[string]any{})
		p := obj(items(lp)[0])
		if lp["total"] != float64(1) || p["id"] != s.purchaseID || p["kind"] != "MADE_TO_ORDER" || p["status"] != "OPEN" || p["supplier_name"] != "Pedras São José" ||
			p["payment_status"] != "PENDING" || p["total_cost"] != float64(1200) || p["to_pay"] != float64(1200) || p["expected_date"] != "2026-12-01" {
			t.Errorf("purchase = %v", p)
		}
		if _, has := p["sale_items"]; has {
			t.Error("list_purchases não traz linhas")
		}
		for name, args := range map[string]map[string]any{
			"status único":    {"status": []string{"OPEN"}},
			"status múltiplo": {"status": []string{"OPEN", "ORDERED"}},
			"supplier":        {"supplier_id": s.supplierID},
			"kind":            {"kind": "MADE_TO_ORDER"},
			"pagamento":       {"payment_status": "PENDING"},
		} {
			if out := e.ok("list_purchases", args); out["total"] != float64(1) {
				t.Errorf("list_purchases %s = %v", name, out)
			}
		}
		for name, args := range map[string]map[string]any{
			"outro status": {"status": []string{"RECEIVED", "ORDERED"}},
			"PAID":         {"payment_status": "PAID"},
			"reposição":    {"kind": "STOCK_REPLENISHMENT"},
		} {
			if out := e.ok("list_purchases", args); out["total"] != float64(0) {
				t.Errorf("list_purchases %s = %v", name, out)
			}
		}
		d := obj(e.ok("get_purchase", map[string]any{"purchase_id": s.purchaseID})["purchase"])
		si := obj(d["sale_items"].([]any)[0])
		if d["id"] != s.purchaseID || si["id"] != s.saleQuickItem || si["customer_name"] != "João Silva" || si["quantity"] != float64(4) || si["product_name"] == nil || si["product_id"] == nil || si["unit_cost"] == nil || si["quantidade"] != nil || si["produto_nome"] != nil || si["custo_unit"] != nil || len(d["items"].([]any)) != 0 {
			t.Errorf("get_purchase = %v", d)
		}
		e.fail("get_purchase", map[string]any{"purchase_id": uuid.NewString()}, "NOT_FOUND", "")
	})

	t.Run("stock", func(t *testing.T) {
		out := e.ok("get_stock_balance", map[string]any{})
		if out["balance"] != nil || out["total"] != float64(2) || out["has_more"] != false || out["offset"] != float64(0) {
			t.Fatalf("get_stock_balance = %v", out)
		}
		var row map[string]any
		for _, it := range items(out) {
			if obj(it)["product_id"] == s.mármoreID {
				row = obj(it)
			}
		}
		// 5 de ajuste inicial − 1 do item FROM_STOCK da venda convertida.
		if row["product_id"] != s.mármoreID || row["balance"] != float64(4) || row["outflow_qty"] != float64(1) || row["adjustment_qty"] != float64(5) || row["last_inflow_cost"] != float64(450) {
			t.Errorf("saldo = %v", row)
		}
		if out := e.ok("get_stock_balance", map[string]any{"only_negative": true}); out["total"] != float64(0) || len(items(out)) != 0 {
			t.Errorf("only_negative = %v", out)
		}
		if out := e.ok("get_stock_balance", map[string]any{"query": "granito"}); out["total"] != float64(1) {
			t.Errorf("query = %v", out)
		}
		one := e.ok("get_stock_balance", map[string]any{"product_id": s.mármoreID})
		b := obj(one["balance"])
		if _, has := one["items"]; has || b["balance"] != float64(4) {
			t.Fatalf("com product_id = %v", one)
		}
		kinds := map[string]bool{}
		for _, m := range b["movements"].([]any) {
			kinds[obj(m)["kind"].(string)] = true
		}
		if !kinds["ADJUSTMENT"] || !kinds["OUTFLOW"] {
			t.Errorf("movements = %v", b["movements"])
		}
		e.fail("get_stock_balance", map[string]any{"product_id": uuid.NewString()}, "NOT_FOUND", "produto não encontrado")
	})
}

func TestIntegration_FinanceAndAnalyticsReadTools(t *testing.T) {
	e := newEnv(t)
	s := e.seed()
	_ = s

	t.Run("get_finance_summary espelha /finance/summary", func(t *testing.T) {
		out := e.ok("get_finance_summary", map[string]any{})
		_, rest := e.rest("GET", "/finance/summary", nil)
		sales, rs := obj(out["sales"]), obj(rest["sales"])
		for _, k := range []string{"count", "revenue", "cost", "freight", "commission", "profit", "received", "receivable"} {
			if sales[k] != rs[k] {
				t.Errorf("sales.%s = %v, REST %v", k, sales[k], rs[k])
			}
		}
		for _, k := range []string{"receivable", "payable"} {
			if obj(out["balances"])[k] != obj(rest["balances"])[k] {
				t.Errorf("balances.%s = %v, REST %v", k, obj(out["balances"])[k], obj(rest["balances"])[k])
			}
		}
		if _, ok := obj(out["balances"])["payable_by_counterparty_kind"].([]any); !ok {
			t.Errorf("payable_by_counterparty_kind = %v", obj(out["balances"]))
		}
		cash := obj(out["cash"])
		if cash["inflow"] != float64(500) || cash["outflow"] != float64(0) || cash["net"] != float64(500) {
			t.Errorf("cash = %v", cash)
		}
		if cats := cash["by_category"].([]any); len(cats) != 1 || obj(cats[0])["category"] != "SALE_RECEIPT" || obj(cats[0])["amount"] != float64(500) {
			t.Errorf("cash.by_category = %v", cash["by_category"])
		}
		if exp := obj(out["expenses"]); exp["total"] != float64(0) || len(exp["by_category"].([]any)) != 0 {
			t.Errorf("expenses = %v", out["expenses"])
		}
		if res := obj(out["result"]); res["sales_profit"] != obj(rest["result"])["sales_profit"] || res["net_result"] != obj(rest["result"])["net_result"] {
			t.Errorf("result = %v, REST %v", res, rest["result"])
		}
		by := cash["received_by_method"].([]any)
		if len(by) != 1 || obj(by[0])["payment_method"] != "PIX" || obj(by[0])["amount"] != float64(500) {
			t.Errorf("received_by_method = %v", by)
		}
		if obj(out["period"])["to"] != day(0) {
			t.Errorf("period = %v", out["period"])
		}
		e.fail("get_finance_summary", map[string]any{"from": "2026-12-01", "to": "2026-01-01"}, "INVALID_INPUT", "")
		ranged := e.ok("get_finance_summary", map[string]any{"from": "2000-01-01", "to": "2000-01-31"})
		if obj(ranged["sales"])["count"] != float64(0) {
			t.Errorf("período vazio = %v", ranged["sales"])
		}
	})

	t.Run("get_dashboard_kpis espelha /dashboard/kpis", func(t *testing.T) {
		out := e.ok("get_dashboard_kpis", map[string]any{})
		_, rest := e.rest("GET", "/dashboard/kpis", nil)
		if obj(out["sales"])["revenue"] != obj(rest["sales"])["revenue"] || out["open_deliveries"] != rest["open_deliveries"] ||
			obj(out["quotes"])["created"] != obj(rest["quotes"])["created"] || obj(out["balances"])["receivable"] != obj(rest["balances"])["receivable"] {
			t.Errorf("kpis = %v REST %v", out, rest)
		}
		if obj(out["sales"])["average_ticket"] == nil || out["open_deliveries"] != float64(2) {
			t.Errorf("kpis = %v", out)
		}
		// Sem Quotes no período a taxa vem ausente (a rota devolve null).
		empty := e.ok("get_dashboard_kpis", map[string]any{"from": "2000-01-01", "to": "2000-01-31"})
		if _, has := obj(empty["quotes"])["conversion_rate"]; has || obj(empty["quotes"])["created"] != float64(0) {
			t.Errorf("sem quotes = %v", empty["quotes"])
		}
	})

	t.Run("get_top_products e get_monthly_sales", func(t *testing.T) {
		out := e.ok("get_top_products", map[string]any{})
		list := items(out)
		if len(list) < 2 {
			t.Fatalf("top products = %v", out)
		}
		top := obj(list[0])
		if top["product_name"] != "Granito sob encomenda" && top["product_name"] != "Granito Preto" {
			t.Errorf("top = %v", top)
		}
		var pretoRev float64
		for _, it := range list {
			p := obj(it)
			if p["product_name"] == "Granito Preto" {
				pretoRev = p["revenue"].(float64)
				if p["quantity"] != float64(4) || p["sales_count"] != float64(1) || p["average_price"] != float64(500) || p["average_cost"] != float64(300) ||
					p["average_margin_percent"] != float64(40) || p["unit"] != "m2" || p["product_id"] != s.pedraID {
					t.Errorf("Granito Preto = %v", p)
				}
			}
		}
		if pretoRev != 2000 {
			t.Errorf("receita Granito Preto = %v", pretoRev)
		}
		if out := e.ok("get_top_products", map[string]any{"from": "2000-01-01", "to": "2000-12-31"}); len(items(out)) != 0 {
			t.Errorf("período vazio = %v", out)
		}

		m := e.ok("get_monthly_sales", map[string]any{"months": 3})
		ms := items(m)
		cur := obj(ms[2])
		if len(ms) != 3 || cur["month"] != time.Now().Format("2006-01") || cur["sales_count"] != float64(2) || cur["revenue"] != float64(4800) {
			t.Errorf("monthly = %v", ms)
		}
		if obj(ms[0])["sales_count"] != float64(0) || obj(ms[0])["revenue"] != float64(0) {
			t.Errorf("mês sem vendas deve vir zerado: %v", ms[0])
		}
		if out := e.ok("get_monthly_sales", map[string]any{}); len(items(out)) != 12 {
			t.Errorf("default = 12 meses: %d", len(items(out)))
		}
		e.fail("get_monthly_sales", map[string]any{"months": 61}, "INVALID_INPUT", "")
		e.fail("get_monthly_sales", map[string]any{"months": 0}, "INVALID_INPUT", "")
	})

	t.Run("list_payment_alerts", func(t *testing.T) {
		out := e.ok("list_payment_alerts", map[string]any{})
		list := items(out)
		if len(list) != 1 {
			t.Fatalf("alertas = %v", out)
		}
		a := obj(list[0])
		if a["kind"] != "OVERDUE" || a["direction"] != "RECEIVABLE" || a["sale_id"] != s.saleQuick || a["obligation_id"] != s.saleQuickReceivable ||
			a["installment_id"] != s.receiptPending || a["customer_name"] != "João Silva" || a["counterparty_name"] != "João Silva" ||
			a["amount"] != float64(200) || a["due_date"] != day(-3) || a["read"] != false {
			t.Errorf("alerta = %v", a)
		}
		if _, has := a["receipt_id"]; has {
			t.Error("receipt_id foi substituído por obligation_id/installment_id")
		}
		// Marca como lido (REST): some do padrão, volta com only_unread=false.
		e.mustREST("POST", "/dashboard/notificacoes/"+a["id"].(string)+"/lida", nil, 204)
		if out := e.ok("list_payment_alerts", map[string]any{}); len(items(out)) != 0 {
			t.Errorf("lidos não aparecem por padrão: %v", out)
		}
		if out := e.ok("list_payment_alerts", map[string]any{"only_unread": true}); len(items(out)) != 0 {
			t.Errorf("only_unread true: %v", out)
		}
		all := e.ok("list_payment_alerts", map[string]any{"only_unread": false})
		if len(items(all)) != 1 || obj(items(all)[0])["read"] != true {
			t.Errorf("only_unread false = %v", all)
		}
	})
}

// AYD-007 §8: list_obligations e list_financial_movements (leitura) pelo pipeline real.
func TestIntegration_ObligationAndMovementReadTools(t *testing.T) {
	e := newEnv(t)
	s := e.seed()

	t.Run("list_obligations: padrão em aberto, filtros, totais e paginação", func(t *testing.T) {
		out := e.ok("list_obligations", map[string]any{})
		// Em aberto: a receber das 2 vendas, a pagar da compra (1200) — a venda convertida ainda não tem compra.
		if out["total"] != float64(3) || len(items(out)) != 3 || out["has_more"] != false || out["offset"] != float64(0) {
			t.Fatalf("list_obligations = %v", out)
		}
		tot := obj(out["totals"])
		if tot["amount"] != float64(2000+2800+1200) || tot["settled"] != float64(500) || tot["open"] != float64(5500) {
			t.Errorf("totals = %v", tot)
		}
		// Mais próxima do vencimento primeiro: a conta com a parcela vencida (venda rápida).
		first := obj(items(out)[0])
		if first["id"] != s.saleQuickReceivable || first["overdue"] != true || first["status"] != "PARTIAL" || first["direction"] != "RECEIVABLE" ||
			first["amount"] != float64(2000) || first["settled"] != float64(500) || first["open"] != float64(1500) {
			t.Errorf("1ª conta = %v", first)
		}
		if ni := obj(first["next_installment"]); ni["due_date"] != day(-3) || ni["amount_open"] != float64(200) {
			t.Errorf("next_installment = %v", ni)
		}
		if cp := obj(first["counterparty"]); cp["type"] != "CUSTOMER" || cp["id"] != s.joao || cp["name"] != "João Silva" {
			t.Errorf("counterparty = %v", cp)
		}
		if org := obj(first["origin"]); org["type"] != "SALE" || org["id"] != s.saleQuick || org["label"] == "" {
			t.Errorf("origin = %v", org)
		}
		if _, has := first["notes"]; has {
			t.Error("campo nulo deve ser omitido")
		}
		pay := e.ok("list_obligations", map[string]any{"direction": "PAYABLE"})
		if pay["total"] != float64(1) {
			t.Fatalf("PAYABLE = %v", pay)
		}
		po := obj(items(pay)[0])
		if cp := obj(po["counterparty"]); cp["type"] != "SUPPLIER" || cp["id"] != s.supplierID || po["amount"] != float64(1200) || obj(po["origin"])["type"] != "PURCHASE" {
			t.Errorf("conta da compra = %v", po)
		}
		for name, args := range map[string]map[string]any{
			"origem":    {"origin_type": "PURCHASE", "origin_id": s.purchaseID},
			"credor":    {"counterparty_type": "SUPPLIER", "counterparty_id": s.supplierID},
			"status":    {"status": []string{"OPEN"}, "direction": "PAYABLE"},
			"due_to":    {"due_to": day(0), "direction": "RECEIVABLE"},
			"vencidas":  {"overdue": true},
			"pago/tudo": {"status": []string{"OPEN", "PARTIAL", "SETTLED"}, "direction": "PAYABLE"},
		} {
			if out := e.ok("list_obligations", args); out["total"] != float64(1) {
				t.Errorf("list_obligations %s = %v", name, out)
			}
		}
		if out := e.ok("list_obligations", map[string]any{"status": []string{"SETTLED"}}); out["total"] != float64(0) || len(items(out)) != 0 {
			t.Errorf("SETTLED = %v", out)
		}
		page := e.ok("list_obligations", map[string]any{"limit": 2})
		if len(items(page)) != 2 || page["has_more"] != true || page["total"] != float64(3) {
			t.Errorf("limit=2 = %v", page)
		}
		page = e.ok("list_obligations", map[string]any{"limit": 2, "offset": 2})
		if len(items(page)) != 1 || page["has_more"] != false || page["offset"] != float64(2) {
			t.Errorf("offset=2 = %v", page)
		}
		e.fail("list_obligations", map[string]any{"direction": "X"}, "INVALID_INPUT", "")
	})

	t.Run("list_financial_movements", func(t *testing.T) {
		e.mustREST("POST", "/financial-movements", map[string]any{"occurred_on": day(0), "amount": -317.87, "category": "VEHICLE", "counterparty_name": "Posto"}, 201)
		out := e.ok("list_financial_movements", map[string]any{"from": day(0), "to": day(0)})
		if out["total"] != float64(2) || len(items(out)) != 2 {
			t.Fatalf("movimentações = %v", out)
		}
		tot := obj(out["totals"])
		if tot["inflow"] != float64(500) || tot["outflow"] != 317.87 || tot["net"] != 182.13 {
			t.Errorf("totals = %v", tot)
		}
		var receipt, expense map[string]any
		for _, it := range items(out) {
			if obj(it)["category"] == "SALE_RECEIPT" {
				receipt = obj(it)
			} else {
				expense = obj(it)
			}
		}
		if receipt["amount"] != float64(500) || receipt["payment_method"] != "PIX" || receipt["unallocated"] != float64(0) || len(receipt["settlements"].([]any)) != 1 ||
			obj(receipt["settlements"].([]any)[0])["obligation_id"] != s.saleQuickReceivable {
			t.Errorf("recebimento = %v", receipt)
		}
		if expense["amount"] != -317.87 || expense["counterparty_name"] != "Posto" || expense["unallocated"] != 317.87 || len(expense["settlements"].([]any)) != 0 {
			t.Errorf("despesa = %v", expense)
		}
		if _, has := expense["payment_method"]; has {
			t.Error("payment_method nulo deve ser omitido")
		}
		for name, args := range map[string]map[string]any{
			"saídas":       {"direction": "OUT", "from": day(0), "to": day(0)},
			"categoria":    {"category": "VEHICLE", "from": day(0), "to": day(0)},
			"sem quitação": {"unallocated_only": true, "from": day(0), "to": day(0)},
			"credor":       {"counterparty_type": "CUSTOMER", "counterparty_id": s.joao, "from": day(0), "to": day(0)},
		} {
			if got := e.ok("list_financial_movements", args); got["total"] != float64(1) {
				t.Errorf("list_financial_movements %s = %v", name, got)
			}
		}
		if got := e.ok("list_financial_movements", map[string]any{"from": "2000-01-01", "to": "2000-01-31"}); got["total"] != float64(0) || len(items(got)) != 0 {
			t.Errorf("período vazio = %v", got)
		}
		e.fail("list_financial_movements", map[string]any{"category": "X"}, "INVALID_INPUT", "")
	})
}

func TestIntegration_AuditAndErrorsThroughPipeline(t *testing.T) {
	e := newEnv(t)
	e.seed()

	e.ok("search_customers", map[string]any{"query": "maria"})
	e.fail("get_sale", map[string]any{"sale_id": uuid.NewString()}, "NOT_FOUND", "venda não encontrada")
	e.fail("get_sale", map[string]any{}, "INVALID_INPUT", "")

	// Toda chamada (leitura, erro de API e erro de schema) gera um registro de auditoria.
	var rows []struct {
		Tool, Outcome, ErrorCode string
		Result                   *string
		DurationMS               int64
		Hash                     string
	}
	if err := e.db.Raw(`SELECT tool, outcome, COALESCE(error_code,'') AS error_code, result::text AS result, duration_ms, arguments_hash AS hash
		FROM mcp_tool_calls ORDER BY created_at`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("auditoria = %+v", rows)
	}
	want := []struct{ tool, outcome, code string }{
		{"search_customers", "OK", ""}, {"get_sale", "ERROR", "NOT_FOUND"}, {"get_sale", "ERROR", "INVALID_INPUT"},
	}
	for i, w := range want {
		r := rows[i]
		if r.Tool != w.tool || r.Outcome != w.outcome || r.ErrorCode != w.code || r.Result != nil || len(r.Hash) != 64 {
			t.Errorf("auditoria[%d] = %+v, quero %+v (result só para dedupe)", i, r, w)
		}
	}
}

func TestIntegration_GatewayCannotReachOAuthRoutes(t *testing.T) {
	e := newEnv(t)
	gw := gateway.New(e.mux)
	if err := gw.Do(context.Background(), "GET", "/oauth/connections", nil, nil, nil); err == nil {
		t.Error("o Gateway não pode alcançar /oauth/*")
	}
}

func uuidNew() string { return uuid.NewString() }

func sp() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*60*60)
	}
	return loc
}
