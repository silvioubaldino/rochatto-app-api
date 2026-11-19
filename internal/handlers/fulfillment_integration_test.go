package handlers

// Testes de integração do AYD-002 (atendimento da Sale: origem do Item, Purchase, Stock,
// status da Sale) contra um Postgres real. Mesmas condições dos outros *_integration_test:
// só rodam com TEST_DATABASE_URL (banco descartável com as migrations aplicadas; os dados
// criados não são limpos — totais globais são conferidos por diferença).
import (
	"net/http"
	"testing"

	"gorm.io/gorm"
)

type fxEnv struct {
	t   *testing.T
	api apiClient
	db  *gorm.DB
	cid string
}

func newFulfillmentEnv(t *testing.T) fxEnv {
	t.Helper()
	_, db, cid := newIntegrationEnv(t)
	mux := http.NewServeMux()
	NewQuotesHandler(db).Register(mux)
	NewVendasHandler(db).Register(mux)
	NewObligationsHandler(db).Register(mux)
	NewSettlementsHandler(db).Register(mux)
	NewFinancialMovementsHandler(db).Register(mux)
	NewPurchasesHandler(db).Register(mux)
	NewStockHandler(db).Register(mux)
	NewDashboardHandler(db).Register(mux)
	NewServicesHandler(db).Register(mux)
	return fxEnv{t, apiClient{t, mux}, db, cid}
}

func (e fxEnv) supplier() string {
	e.t.Helper()
	var id string
	if err := e.db.Raw("INSERT INTO fornecedores (nome) VALUES (?) RETURNING id", uniq("Fornecedor")).Scan(&id).Error; err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e fxEnv) product() string {
	e.t.Helper()
	var id string
	if err := e.db.Raw("INSERT INTO produtos (nome, unidade) VALUES (?, 'm2') RETURNING id", uniq("Pedra")).Scan(&id).Error; err != nil {
		e.t.Fatal(err)
	}
	return id
}

// sale cria uma Sale por venda rápida com os Items dados e devolve (saleID, itemIDs).
func (e fxEnv) sale(items ...map[string]any) (string, []string) {
	e.t.Helper()
	code, res := e.api.do("POST", "/quotes/quick-sale", map[string]any{"customer_id": e.cid, "items": items})
	if code != 201 {
		e.t.Fatalf("quick-sale = %d %v", code, res)
	}
	saleID := res["sale_id"].(string)
	_, sale := e.api.do("GET", "/vendas/"+saleID, nil)
	ids := []string{}
	for _, it := range sale["itens"].([]any) {
		ids = append(ids, it.(map[string]any)["id"].(string))
	}
	return saleID, ids
}

func qItem(name string, qty, price, cost float64, productID string) map[string]any {
	m := map[string]any{"product_name": name, "quantity": qty, "unit_price": price, "unit_cost": cost}
	if productID != "" {
		m["product_id"] = productID
	}
	return m
}

// putItem faz PUT do Item com o payload legado + campos novos.
func (e fxEnv) putItem(saleID, itemID string, body map[string]any) (int, map[string]any) {
	e.t.Helper()
	return e.api.do("PUT", "/vendas/"+saleID+"/itens/"+itemID, body)
}

func (e fxEnv) balance(productID string) map[string]any {
	e.t.Helper()
	code, b := e.api.do("GET", "/stock/balance/"+productID, nil)
	if code != 200 {
		e.t.Fatalf("GET balance = %d %v", code, b)
	}
	return b
}

func num(m map[string]any, k string) float64 {
	v, _ := m[k].(float64)
	return v
}

func hasWarning(res map[string]any, w string) bool {
	ws, _ := res["warnings"].([]any)
	for _, x := range ws {
		if x == w {
			return true
		}
	}
	return false
}

// RN-16: FROM_STOCK preenche o custo com a última entrada; sem saldo avisa e deixa o
// Balance negativo; cancelar a Sale tira o Outflow.
func TestIntegration_FromStockCostWarningAndBalance(t *testing.T) {
	e := newFulfillmentEnv(t)
	pid := e.product()

	for _, adj := range []map[string]any{
		{"product_id": pid, "quantity": 5, "unit_cost": 40, "reason": "INITIAL", "adjusted_at": "2026-01-01"},
		{"product_id": pid, "quantity": 2, "unit_cost": 45, "reason": "COUNT", "adjusted_at": "2026-02-01"},
		{"product_id": pid, "quantity": -1, "unit_cost": 99, "reason": "OTHER", "adjusted_at": "2026-03-01"}, // OTHER não define custo
	} {
		if code, res := e.api.do("POST", "/stock/adjustments", adj); code != 201 {
			t.Fatalf("POST adjustment = %d %v", code, res)
		}
	}
	b := e.balance(pid)
	if num(b, "balance") != 6 || num(b, "adjustment_qty") != 6 || num(b, "last_inflow_cost") != 45 || b["last_inflow_date"] != "2026-02-01" {
		t.Errorf("balance inicial = %v", b)
	}

	saleID, items := e.sale(qItem("Pedra", 10, 100, 99, pid))

	// PUT origin FROM_STOCK sem custo_unit → custo 45 e aviso (6 − 10 < 0).
	code, res := e.putItem(saleID, items[0], map[string]any{
		"produto_id": pid, "produto_nome": "Pedra", "quantidade": 10, "preco_venda_unit": 100, "origin": "FROM_STOCK"})
	if code != 200 || num(res, "custo_unit") != 45 || res["origin"] != "FROM_STOCK" || !hasWarning(res, "INSUFFICIENT_STOCK") {
		t.Fatalf("PUT FROM_STOCK = %d %v", code, res)
	}
	b = e.balance(pid)
	if num(b, "balance") != -4 || num(b, "outflow_qty") != 10 {
		t.Errorf("balance após Outflow = %v, want -4", b)
	}
	_, neg := e.api.list("/stock/balance?only_negative=true")
	found := false
	for _, r := range neg {
		if r["product_id"] == pid {
			found = true
		}
		if r["balance"].(float64) >= 0 {
			t.Errorf("only_negative trouxe balance %v", r["balance"])
		}
	}
	if !found {
		t.Error("only_negative não trouxe o Product negativo")
	}

	// Extrato: quantity com sinal (OUTFLOW negativo, reference_id = Sale; ajustes com o próprio sinal).
	var outflow, otherAdj map[string]any
	for _, m := range b["movements"].([]any) {
		mv := m.(map[string]any)
		if mv["kind"] == "OUTFLOW" {
			outflow = mv
		}
		if mv["kind"] == "ADJUSTMENT" && mv["date"] == "2026-03-01" {
			otherAdj = mv
		}
	}
	if outflow == nil || num(outflow, "quantity") != -10 || outflow["reference_id"] != saleID {
		t.Errorf("movimento OUTFLOW = %v", outflow)
	}
	if otherAdj == nil || num(otherAdj, "quantity") != -1 {
		t.Errorf("movimento ADJUSTMENT = %v", otherAdj)
	}
	if mv := b["movements"].([]any); len(mv) != 4 || mv[0].(map[string]any)["kind"] != "OUTFLOW" {
		t.Errorf("extrato (ordem desc por data) = %v", mv)
	}

	// custo_unit explícito é respeitado; com saldo suficiente não há aviso.
	code, res = e.putItem(saleID, items[0], map[string]any{
		"produto_id": pid, "produto_nome": "Pedra", "quantidade": 1, "preco_venda_unit": 100, "custo_unit": 50, "origin": "FROM_STOCK"})
	if code != 200 || num(res, "custo_unit") != 50 || len(res["warnings"].([]any)) != 0 {
		t.Errorf("PUT custo explícito = %d %v", code, res)
	}

	// FROM_STOCK sem produto_id → 400.
	if code, _ := e.api.do("POST", "/vendas/"+saleID+"/itens", map[string]any{
		"produto_nome": "X", "quantidade": 1, "preco_venda_unit": 1, "origin": "FROM_STOCK"}); code != 400 {
		t.Errorf("FROM_STOCK sem produto = %d, want 400", code)
	}
	// POST FROM_STOCK sem custo → última entrada.
	code, res = e.api.do("POST", "/vendas/"+saleID+"/itens", map[string]any{
		"produto_id": pid, "produto_nome": "Pedra", "quantidade": 8, "preco_venda_unit": 100, "origin": "FROM_STOCK"})
	if code != 201 || num(res, "custo_unit") != 45 || !hasWarning(res, "INSUFFICIENT_STOCK") {
		t.Errorf("POST FROM_STOCK = %d %v", code, res)
	}

	// Cancelar a Sale tira o Outflow do Balance.
	if code, _ := e.api.do("PATCH", "/vendas/"+saleID+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatalf("cancel = %d", code)
	}
	if b := e.balance(pid); num(b, "balance") != 6 || num(b, "outflow_qty") != 0 {
		t.Errorf("balance após cancelar = %v, want 6", b)
	}
}

// Inflow só de reposição RECEIVED; custo da última entrada passa a ser o da Purchase recebida.
func TestIntegration_ReplenishmentInflow(t *testing.T) {
	e := newFulfillmentEnv(t)
	pid, sid := e.product(), e.supplier()
	e.api.do("POST", "/stock/adjustments", map[string]any{"product_id": pid, "quantity": 3, "unit_cost": 40, "reason": "INITIAL", "adjusted_at": "2026-01-01"})

	// MADE_TO_ORDER não aceita linhas; STOCK_REPLENISHMENT não aceita Items de Sale.
	if code, _ := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sid,
		"items": []map[string]any{{"product_id": pid, "quantity": 1, "unit_cost": 1}}}); code != 400 {
		t.Errorf("MTO com items = %d, want 400", code)
	}
	code, p := e.api.do("POST", "/purchases", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": sid,
		"expected_date": "2026-12-01", "items": []map[string]any{{"product_id": pid, "quantity": 10, "unit_cost": 60}}})
	if code != 201 || num(p, "total_cost") != 600 || p["status"] != "OPEN" || len(p["items"].([]any)) != 1 || p["expected_date"] != "2026-12-01" {
		t.Fatalf("POST reposição = %d %v", code, p)
	}
	id := p["id"].(string)

	// Linha extra, editar e remover (só OPEN).
	code, line := e.api.do("POST", "/purchases/"+id+"/items", map[string]any{"product_id": pid, "quantity": 2, "unit_cost": 70})
	if code != 201 || line["product_name"] == nil {
		t.Fatalf("POST linha = %d %v", code, line)
	}
	if code, l := e.api.do("PUT", "/purchases/"+id+"/items/"+line["id"].(string), map[string]any{"product_id": pid, "quantity": 5, "unit_cost": 70}); code != 200 || num(l, "quantity") != 5 {
		t.Errorf("PUT linha = %d %v", code, l)
	}
	if code, _ := e.api.do("DELETE", "/purchases/"+id+"/items/"+line["id"].(string), nil); code != 204 {
		t.Errorf("DELETE linha = %d", code)
	}

	// OPEN / ORDERED não geram Inflow.
	e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "ORDERED"})
	if b := e.balance(pid); num(b, "inflow_qty") != 0 || num(b, "balance") != 3 {
		t.Errorf("ORDERED gerou inflow: %v", b)
	}
	if code, _ := e.api.do("POST", "/purchases/"+id+"/items", map[string]any{"product_id": pid, "quantity": 1, "unit_cost": 1}); code != 409 {
		t.Errorf("linha em ORDERED = %d, want 409", code)
	}
	code, st := e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "RECEIVED"})
	if code != 200 || st["received_at"] == nil {
		t.Fatalf("RECEIVED = %d %v", code, st)
	}
	b := e.balance(pid)
	if num(b, "inflow_qty") != 10 || num(b, "balance") != 13 || num(b, "last_inflow_cost") != 60 || b["last_inflow_date"] != st["received_at"] {
		t.Errorf("balance após RECEIVED = %v", b)
	}
	inflow := false
	for _, m := range b["movements"].([]any) {
		mv := m.(map[string]any)
		if mv["kind"] == "INFLOW" && mv["reference_id"] == id && num(mv, "quantity") == 10 && num(mv, "unit_cost") == 60 {
			inflow = true
		}
	}
	if !inflow {
		t.Errorf("extrato sem INFLOW da Purchase: %v", b["movements"])
	}
	// RECEIVED é terminal; não pode excluir.
	if code, _ := e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "ORDERED"}); code != 409 {
		t.Errorf("RECEIVED→ORDERED = %d, want 409", code)
	}
	if code, _ := e.api.do("DELETE", "/purchases/"+id, nil); code != 409 {
		t.Errorf("DELETE RECEIVED = %d, want 409", code)
	}
}

// RF-31: fila agrupada por Supplier; anexar Items com as travas de 409.
func TestIntegration_PendingQueueAndAttach(t *testing.T) {
	e := newFulfillmentEnv(t)
	supA, supB, pid := e.supplier(), e.supplier(), e.product()

	saleID, it := e.sale(qItem("A1", 1, 100, 60, ""), qItem("B1", 2, 100, 50, ""), qItem("S1", 1, 100, 40, pid), qItem("N1", 1, 100, 30, ""))
	set := func(itemID string, body map[string]any) {
		t.Helper()
		if code, res := e.putItem(saleID, itemID, body); code != 200 {
			t.Fatalf("PUT item = %d %v", code, res)
		}
	}
	set(it[0], map[string]any{"produto_nome": "A1", "quantidade": 1, "preco_venda_unit": 100, "custo_unit": 60, "fornecedor_id": supA})
	set(it[1], map[string]any{"produto_nome": "B1", "quantidade": 2, "preco_venda_unit": 100, "custo_unit": 50, "fornecedor_id": supB})
	set(it[2], map[string]any{"produto_id": pid, "produto_nome": "S1", "quantidade": 1, "preco_venda_unit": 100, "custo_unit": 40, "origin": "FROM_STOCK", "fornecedor_id": supA})

	canceledSale, cit := e.sale(qItem("C1", 1, 100, 60, ""))
	if code, res := e.putItem(canceledSale, cit[0], map[string]any{"produto_nome": "C1", "quantidade": 1, "preco_venda_unit": 100, "custo_unit": 60, "fornecedor_id": supA}); code != 200 {
		t.Fatalf("PUT = %d %v", code, res)
	}
	if code, _ := e.api.do("PATCH", "/vendas/"+canceledSale+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatalf("cancel = %d", code)
	}

	queue := func() map[string][]string { // supplier_id ("" = sem Supplier) → item ids
		t.Helper()
		code, groups := e.api.list("/purchases/pending-sale-items")
		if code != 200 {
			t.Fatalf("pending = %d", code)
		}
		out := map[string][]string{}
		for i, g := range groups {
			key := ""
			if g["supplier_id"] != nil {
				key = g["supplier_id"].(string)
				if g["supplier_name"] == nil {
					t.Errorf("grupo %s sem supplier_name", key)
				}
			} else if i != len(groups)-1 {
				t.Errorf("grupo sem Supplier não está por último (pos %d de %d)", i, len(groups))
			}
			for _, x := range g["items"].([]any) {
				m := x.(map[string]any)
				if m["sale_date"] == nil || m["customer_name"] == nil || m["sale_id"] == nil {
					t.Errorf("item da fila incompleto: %v", m)
				}
				out[key] = append(out[key], m["id"].(string))
			}
		}
		return out
	}
	contains := func(xs []string, x string) bool {
		for _, y := range xs {
			if y == x {
				return true
			}
		}
		return false
	}
	q := queue()
	if !contains(q[supA], it[0]) || !contains(q[supB], it[1]) || !contains(q[""], it[3]) {
		t.Errorf("fila sem os Items esperados: %v", q)
	}
	for _, k := range []string{supA, supB, ""} {
		if contains(q[k], it[2]) || contains(q[k], cit[0]) {
			t.Errorf("fila contém Item FROM_STOCK ou de Sale cancelada (grupo %q)", k)
		}
	}

	// Gera a Purchase do Supplier A com A1.
	code, p := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": supA, "sale_item_ids": []string{it[0]}})
	if code != 201 || num(p, "total_cost") != 60 || num(p, "payable") != 60 || len(p["sale_items"].([]any)) != 1 || p["supplier_name"] == nil {
		t.Fatalf("POST purchase = %d %v", code, p)
	}
	pidA := p["id"].(string)
	si := p["sale_items"].([]any)[0].(map[string]any)
	if si["sale_id"] != saleID || si["customer_name"] == nil || num(si, "custo_unit") != 60 {
		t.Errorf("sale_items[0] = %v", si)
	}
	if contains(queue()[supA], it[0]) {
		t.Error("Item comprado continua na fila")
	}

	// 409: outro Supplier, FROM_STOCK, Sale cancelada, já em outra Purchase.
	attach := func(purchaseID, itemID string) int {
		code, _ := e.api.do("POST", "/purchases/"+purchaseID+"/sale-items", map[string]any{"sale_item_ids": []string{itemID}})
		return code
	}
	for name, itemID := range map[string]string{"outro Supplier": it[1], "FROM_STOCK": it[2], "Sale cancelada": cit[0], "sem Supplier": it[3]} {
		if code := attach(pidA, itemID); code != 409 {
			t.Errorf("attach %s = %d, want 409", name, code)
		}
	}
	_, p2 := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": supA})
	if code := attach(p2["id"].(string), it[0]); code != 409 {
		t.Errorf("attach já em outra Purchase = %d, want 409", code)
	}
	if code := attach(pidA, "00000000-0000-0000-0000-000000000000"); code != 404 {
		t.Errorf("attach inexistente = %d, want 404", code)
	}
	_, repl := e.api.do("POST", "/purchases", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": supA})
	if code := attach(repl["id"].(string), it[0]); code != 409 {
		t.Errorf("attach em reposição = %d, want 409", code)
	}
	if code, _ := e.api.do("POST", "/purchases/"+pidA+"/items", map[string]any{"product_id": pid, "quantity": 1, "unit_cost": 1}); code != 409 {
		t.Errorf("linha de reposição em MTO = %d, want 409", code)
	}

	// Item em Purchase OPEN: mudar quantidade/custo/Supplier/origem → 409; mudar preço → 200; DELETE → 409.
	locked := []map[string]any{
		{"produto_nome": "A1", "quantidade": 3, "preco_venda_unit": 100, "custo_unit": 60, "fornecedor_id": supA},
		{"produto_nome": "A1", "quantidade": 1, "preco_venda_unit": 100, "custo_unit": 61, "fornecedor_id": supA},
		{"produto_nome": "A1", "quantidade": 1, "preco_venda_unit": 100, "custo_unit": 60, "fornecedor_id": supB},
		{"produto_id": pid, "produto_nome": "A1", "quantidade": 1, "preco_venda_unit": 100, "custo_unit": 60, "fornecedor_id": supA, "origin": "FROM_STOCK"},
	}
	for i, body := range locked {
		if code, _ := e.putItem(saleID, it[0], body); code != 409 {
			t.Errorf("PUT travado #%d = %d, want 409", i, code)
		}
	}
	if code, res := e.putItem(saleID, it[0], map[string]any{"produto_nome": "A1", "quantidade": 1, "preco_venda_unit": 150, "custo_unit": 60, "fornecedor_id": supA}); code != 200 || res["purchase_id"] != pidA || res["purchase_status"] != "OPEN" {
		t.Errorf("PUT só preço = %d %v", code, res)
	}
	if code, _ := e.api.do("DELETE", "/vendas/"+saleID+"/itens/"+it[0], nil); code != 409 {
		t.Errorf("DELETE item em Purchase = %d, want 409", code)
	}
	_, sale := e.api.do("GET", "/vendas/"+saleID, nil)
	for _, x := range sale["itens"].([]any) {
		m := x.(map[string]any)
		if m["id"] == it[0] && (m["purchase_status"] != "OPEN" || m["purchase_id"] != pidA) {
			t.Errorf("GET item em Purchase = %v", m)
		}
	}

	// Desanexar devolve à fila.
	if code, _ := e.api.do("DELETE", "/purchases/"+pidA+"/sale-items/"+it[0], nil); code != 204 {
		t.Errorf("detach = %d", code)
	}
	if !contains(queue()[supA], it[0]) {
		t.Error("Item desanexado não voltou à fila")
	}
	if code, _ := e.api.do("DELETE", "/purchases/"+pidA+"/sale-items/"+it[0], nil); code != 404 {
		t.Errorf("detach de novo = %d, want 404", code)
	}

	// Excluir Purchase OPEN libera os Items.
	attach(pidA, it[0])
	if code, _ := e.api.do("DELETE", "/purchases/"+pidA, nil); code != 204 {
		t.Errorf("DELETE purchase OPEN = %d", code)
	}
	if code, _ := e.api.do("GET", "/purchases/"+pidA, nil); code != 404 {
		t.Errorf("GET purchase excluída = %d, want 404", code)
	}
	if !contains(queue()[supA], it[0]) {
		t.Error("Item de Purchase excluída não voltou à fila")
	}

	// Items de Sale cancelada não podem ser criados/alterados.
	if code, _ := e.putItem(canceledSale, cit[0], map[string]any{"produto_nome": "C1", "quantidade": 1, "preco_venda_unit": 1}); code != 409 {
		t.Errorf("PUT item de Sale cancelada = %d, want 409", code)
	}
	if code, _ := e.api.do("POST", "/vendas/"+canceledSale+"/itens", map[string]any{"produto_nome": "C2", "quantidade": 1, "preco_venda_unit": 1}); code != 409 {
		t.Errorf("POST item em Sale cancelada = %d, want 409", code)
	}
	if code, _ := e.api.do("DELETE", "/vendas/"+canceledSale+"/itens/"+cit[0], nil); code != 409 {
		t.Errorf("DELETE item de Sale cancelada = %d, want 409", code)
	}
}

// Transições de status (incl. 422 vazia), carimbos, pagamento e travas pós-pedido.
func TestIntegration_PurchaseStatusPaymentAndCancel(t *testing.T) {
	e := newFulfillmentEnv(t)
	sup := e.supplier()
	var day string
	e.db.Raw("SELECT (NOW() AT TIME ZONE 'America/Sao_Paulo')::date::text").Scan(&day)

	saleID, it := e.sale(qItem("P1", 2, 200, 100, ""), qItem("P2", 1, 300, 100, ""))
	for i, name := range []string{"P1", "P2"} {
		qty := []float64{2, 1}[i]
		if code, res := e.putItem(saleID, it[i], map[string]any{"produto_nome": name, "quantidade": qty, "preco_venda_unit": 200, "custo_unit": 100, "fornecedor_id": sup}); code != 200 {
			t.Fatalf("PUT = %d %v", code, res)
		}
	}

	_, empty := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup})
	emptyID := empty["id"].(string)
	if code, _ := e.api.do("PATCH", "/purchases/"+emptyID+"/status", map[string]any{"status": "ORDERED"}); code != 422 {
		t.Errorf("ORDERED vazia = %d, want 422", code)
	}
	if code, _ := e.api.do("PATCH", "/purchases/"+emptyID+"/status", map[string]any{"status": "RECEIVED"}); code != 409 {
		t.Errorf("OPEN→RECEIVED = %d, want 409", code)
	}
	if code, _ := e.api.do("PATCH", "/purchases/"+emptyID+"/status", map[string]any{"status": "X"}); code != 400 {
		t.Errorf("status inválido = %d, want 400", code)
	}

	// Purchase com P1; P2 fica em outra Purchase OPEN.
	_, p := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup, "sale_item_ids": []string{it[0]}})
	id := p["id"].(string)
	_, pOpen := e.api.do("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup, "sale_item_ids": []string{it[1]}})

	code, st := e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "ORDERED"})
	if code != 200 || st["ordered_at"] != day || st["received_at"] != nil {
		t.Errorf("OPEN→ORDERED = %d %v (hoje %s)", code, st, day)
	}
	code, st = e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "OPEN"})
	if code != 200 || st["ordered_at"] != nil {
		t.Errorf("ORDERED→OPEN = %d %v", code, st)
	}
	e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "ORDERED"})

	// Purchase pedida: Item travado, attach/delete recusados, cancelar a Sale → 409.
	if code, _ := e.putItem(saleID, it[0], map[string]any{"produto_nome": "P1", "quantidade": 2, "preco_venda_unit": 200, "custo_unit": 90, "fornecedor_id": sup}); code != 409 {
		t.Errorf("PUT custo com Purchase ORDERED = %d, want 409", code)
	}
	if code, _ := e.api.do("DELETE", "/purchases/"+id+"/sale-items/"+it[0], nil); code != 409 {
		t.Errorf("detach em ORDERED = %d, want 409", code)
	}
	if code, _ := e.api.do("DELETE", "/purchases/"+id, nil); code != 409 {
		t.Errorf("DELETE ORDERED = %d, want 409", code)
	}
	if code, _ := e.api.do("PATCH", "/vendas/"+saleID+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 409 {
		t.Errorf("cancelar com Item em Purchase ORDERED = %d, want 409", code)
	}

	// Pagamento (AYD-007): quitar a conta do Supplier da compra; Σ a pagar no painel acompanha (RN-06).
	resumo := func() float64 {
		t.Helper()
		_, r := e.api.do("GET", "/dashboard/resumo", nil)
		return num(r, "total_a_pagar_fornecedor")
	}
	purchase := func() map[string]any {
		t.Helper()
		_, r := e.api.do("GET", "/purchases/"+id, nil)
		return r
	}
	before := resumo()
	supObl := e.api.payableOf("PURCHASE", id, sup)
	code, pay := e.api.do("POST", "/obligations/"+supObl+"/settle", map[string]any{"movement": map[string]any{"occurred_on": day, "payment_method": "PIX"}})
	mv, _ := pay["movement"].(map[string]any)
	if code != 201 || num(mv, "amount") != -200 || mv["category"] != "PURCHASE_PAYMENT" {
		t.Fatalf("pagar a conta = %d %v", code, pay)
	}
	if pr := purchase(); num(pr, "paid") != 200 || num(pr, "payable") != 0 || pr["payment_status"] != "PAID" {
		t.Errorf("compra paga = %v", pr)
	}
	if d := resumo() - before; !approx(d, -200) {
		t.Errorf("a pagar delta após quitar = %v, want -200", d)
	}
	// Desfazer a quitação reabre a conta; pagar a menos deixa PENDING com saldo.
	settlementID := mv["settlements"].([]any)[0].(map[string]any)["id"].(string)
	if code, _ := e.api.do("DELETE", "/settlements/"+settlementID, nil); code != 204 {
		t.Errorf("DELETE settlement = %d, want 204", code)
	}
	if pr := purchase(); num(pr, "paid") != 0 || num(pr, "payable") != 200 || pr["payment_status"] != "PENDING" {
		t.Errorf("compra após desfazer = %v", pr)
	}
	if code, _ := e.api.do("POST", "/obligations/"+supObl+"/settle", map[string]any{"amount": 150, "movement": map[string]any{"occurred_on": "2026-03-01", "payment_method": "PIX"}}); code != 201 {
		t.Errorf("pagar 150 = %d", code)
	}
	if pr := purchase(); num(pr, "paid") != 150 || num(pr, "payable") != 50 || pr["payment_status"] != "PENDING" {
		t.Errorf("compra parcial = %v", pr)
	}
	if code, _ := e.api.do("POST", "/obligations/"+supObl+"/settle", map[string]any{"amount": 60, "movement": map[string]any{"occurred_on": day}}); code != 409 {
		t.Errorf("pagar acima do devido = %d, want 409", code)
	}
	if code, _ := e.api.do("PATCH", "/purchases/"+id+"/payment", map[string]any{"payment_status": "PAID"}); code != 404 && code != 405 {
		t.Errorf("rota legada de pagamento = %d, want 404/405", code)
	}
	e.api.do("POST", "/obligations/"+supObl+"/settle", map[string]any{"movement": map[string]any{"occurred_on": day, "payment_method": "PIX"}})

	// Pedida → recebida; continua impedindo o cancelamento.
	code, st = e.api.do("PATCH", "/purchases/"+id+"/status", map[string]any{"status": "RECEIVED"})
	if code != 200 || st["received_at"] != day || st["ordered_at"] != day {
		t.Errorf("ORDERED→RECEIVED = %d %v", code, st)
	}
	if code, _ := e.api.do("PATCH", "/vendas/"+saleID+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 409 {
		t.Errorf("cancelar com Item em Purchase RECEIVED = %d, want 409", code)
	}

	// Lista com filtros.
	code, list := e.api.list("/purchases?supplier_id=" + sup + "&status=RECEIVED&payment_status=PAID&kind=MADE_TO_ORDER&from=" + day + "&to=" + day)
	if code != 200 || len(list) != 1 || list[0]["id"] != id {
		t.Errorf("GET /purchases filtrado = %d %v", code, list)
	}
	if code, _ := e.api.list("/purchases?status=X"); code != 400 {
		t.Errorf("filtro inválido = %d, want 400", code)
	}

	// Outra Sale: Item em Purchase OPEN é liberado ao cancelar.
	sale2, it2 := e.sale(qItem("Q1", 1, 500, 300, ""))
	e.putItem(sale2, it2[0], map[string]any{"produto_nome": "Q1", "quantidade": 1, "preco_venda_unit": 500, "custo_unit": 300, "fornecedor_id": sup})
	pOpenID := pOpen["id"].(string)
	if code, _ := e.api.do("POST", "/purchases/"+pOpenID+"/sale-items", map[string]any{"sale_item_ids": []string{it2[0]}}); code != 200 {
		t.Fatalf("attach = %d", code)
	}
	code, res := e.api.do("PATCH", "/vendas/"+sale2+"/delivery-status", map[string]any{"delivery_status": "CANCELED"})
	if code != 200 || res["delivery_status"] != "CANCELED" || res["id"] != sale2 {
		t.Fatalf("cancel = %d %v", code, res)
	}
	_, po := e.api.do("GET", "/purchases/"+pOpenID, nil)
	if num(po, "total_cost") != 100 || len(po["sale_items"].([]any)) != 1 {
		t.Errorf("Purchase OPEN após cancelamento = %v (Item da Sale cancelada deveria sair)", po)
	}
	var stillLinked int64
	e.db.Raw("SELECT COUNT(*) FROM itens_venda WHERE id = ? AND purchase_id IS NOT NULL", it2[0]).Scan(&stillLinked)
	if stillLinked != 0 {
		t.Error("Item da Sale cancelada continua na Purchase")
	}
	// CANCELED é terminal.
	if code, _ := e.api.do("PATCH", "/vendas/"+sale2+"/delivery-status", map[string]any{"delivery_status": "TO_DELIVER"}); code != 409 {
		t.Errorf("reabrir cancelada = %d, want 409", code)
	}
	if code, _ := e.api.do("PATCH", "/vendas/"+sale2+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 409 {
		t.Errorf("cancelar de novo = %d, want 409", code)
	}
}

// Delivery status manual, payment_status derivado dos Receipts e filtros da listagem.
func TestIntegration_SaleStatuses(t *testing.T) {
	e := newFulfillmentEnv(t)
	saleID, _ := e.sale(qItem("R1", 1, 1000, 500, ""))

	_, s := e.api.do("GET", "/vendas/"+saleID, nil)
	if s["delivery_status"] != "TO_DELIVER" || s["payment_status"] != "PENDING" || s["quote_id"] == nil {
		t.Errorf("Sale nova = %v", s)
	}
	for _, legacy := range []string{"status", "a_pagar_fornecedor", "total_pago_fornecedor"} {
		if _, ok := s[legacy]; ok {
			t.Errorf("campo legado %q ainda na resposta", legacy)
		}
	}
	item := s["itens"].([]any)[0].(map[string]any)
	if item["origin"] != "MADE_TO_ORDER" || item["purchase_id"] != nil {
		t.Errorf("Item convertido = %v", item)
	}
	if _, ok := item["pago_fornecedor"]; ok {
		t.Error("pago_fornecedor ainda no Item")
	}

	status := func() string {
		_, s := e.api.do("GET", "/vendas/"+saleID, nil)
		return s["payment_status"].(string)
	}
	e.api.receive(saleID, 400, "2026-11-01", "PIX")
	if got := status(); got != "PARTIAL" {
		t.Errorf("após 400/1000 = %s, want PARTIAL", got)
	}
	e.api.receive(saleID, 600, "2026-11-02", "PIX")
	if got := status(); got != "PAID" {
		t.Errorf("após 1000/1000 = %s, want PAID", got)
	}

	if code, r := e.api.do("PATCH", "/vendas/"+saleID+"/delivery-status", map[string]any{"delivery_status": "DELIVERED"}); code != 200 || r["delivery_status"] != "DELIVERED" {
		t.Errorf("DELIVERED = %d %v", code, r)
	}
	if code, _ := e.api.do("PATCH", "/vendas/"+saleID+"/delivery-status", map[string]any{"delivery_status": "ENTREGUE"}); code != 400 {
		t.Errorf("status legado = %d, want 400", code)
	}
	if code, _ := e.api.do("PATCH", "/vendas/00000000-0000-0000-0000-000000000000/delivery-status", map[string]any{"delivery_status": "DELIVERED"}); code != 404 {
		t.Errorf("Sale inexistente = %d, want 404", code)
	}

	_, list := e.api.list("/vendas?cliente_id=" + e.cid + "&delivery_status=DELIVERED&payment_status=PAID")
	if len(list) != 1 || list[0]["id"] != saleID {
		t.Errorf("filtro delivery/payment = %v", list)
	}
	if _, list := e.api.list("/vendas?cliente_id=" + e.cid + "&payment_status=PENDING,PARTIAL"); len(list) != 0 {
		t.Errorf("filtro PENDING,PARTIAL trouxe %d", len(list))
	}
	if _, list := e.api.list("/vendas?cliente_id=" + e.cid + "&payment_status=PARTIAL,PAID&delivery_status=TO_DELIVER,DELIVERED"); len(list) != 1 {
		t.Errorf("filtro com vírgulas trouxe %d, want 1", len(list))
	}
	if code, _ := e.api.list("/vendas?payment_status=PAID,X"); code != 400 {
		t.Errorf("lista com valor inválido = %d, want 400", code)
	}
	if code, _ := e.api.list("/vendas?delivery_status=ENTREGUE"); code != 400 {
		t.Errorf("filtro inválido = %d, want 400", code)
	}

	// PUT não aceita status; POST /vendas não existe mais.
	if code, _ := e.api.do("PUT", "/vendas/"+saleID, map[string]any{"status": "ENTREGUE"}); code != 400 {
		t.Errorf("PUT com status = %d, want 400", code)
	}
	if code, r := e.api.do("PUT", "/vendas/"+saleID, map[string]any{"observacoes": "ok"}); code != 200 || r["delivery_status"] != "DELIVERED" || r["observacoes"] != "ok" {
		t.Errorf("PUT = %d %v", code, r)
	}
	if code, _ := e.api.do("POST", "/vendas", map[string]any{"cliente_id": e.cid}); code == 201 {
		t.Error("POST /vendas ainda cria Sale")
	}
}

// Sales canceladas saem de todos os totais do painel/dashboard e dos relatórios.
func TestIntegration_DashboardExcludesCanceled(t *testing.T) {
	e := newFulfillmentEnv(t)
	var day, month string
	e.db.Raw("SELECT CURRENT_DATE::text").Scan(&day)
	e.db.Raw("SELECT DATE_TRUNC('month', CURRENT_DATE)::date::text").Scan(&month)
	pid := e.product()

	saleID, _ := e.sale(qItem("D1", 2, 500, 200, pid))
	mustExec(t, e.db, `UPDATE vendas SET data_venda = CURRENT_DATE, vendedor_externo_id = ? WHERE id = ?`, e.partner(), saleID)
	setLegacyCosts(t, e.db, saleID, 100, 50)
	e.api.receive(saleID, 300, day, "PIX")

	rankQty := func() float64 {
		_, rank := e.api.do("GET", "/relatorios/produtos?data_inicio="+day+"&data_fim="+day, nil)
		for _, d := range rank["dados"].([]any) {
			m := d.(map[string]any)
			if m["produto_id"] == pid {
				return num(m, "total_quantidade")
			}
		}
		return 0
	}
	resumo := func() map[string]any {
		_, r := e.api.do("GET", "/dashboard/resumo?data_inicio="+day+"&data_fim="+day, nil)
		return r
	}
	before, beforeSnap, beforeRank := resumo(), takeSnapshot(t, e.api, day, month), rankQty()
	if beforeRank != 2 {
		t.Fatalf("ranking antes = %v, want 2", beforeRank)
	}

	if code, _ := e.api.do("PATCH", "/vendas/"+saleID+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}); code != 200 {
		t.Fatalf("cancel = %d", code)
	}
	after, afterSnap := resumo(), takeSnapshot(t, e.api, day, month)

	checks := []struct {
		name      string
		got, want float64
	}{
		{"a receber", afterSnap.aReceber - beforeSnap.aReceber, -700},
		{"lucro", afterSnap.lucro - beforeSnap.lucro, -(1000 - 400 - 100 - 50)},
		{"vendido", afterSnap.vendido - beforeSnap.vendido, -1000},
		{"série mensal: total", afterSnap.mesTotal - beforeSnap.mesTotal, -1000},
		{"série mensal: lucro", afterSnap.mesLucro - beforeSnap.mesLucro, -450},
		{"comissões pendentes", num(after, "total_comissoes_pendentes") - num(before, "total_comissoes_pendentes"), -50},
		{"vendas abertas", num(after, "vendas_abertas") - num(before, "vendas_abertas"), -1},
		{"mais vendidos", rankQty(), 0},
	}
	for _, c := range checks {
		if !approx(c.got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, c.got, c.want)
		}
	}
	// "a receber" da listagem (pendente=true) também exclui a cancelada.
	_, list := e.api.list("/vendas?cliente_id=" + e.cid + "&pendente=true")
	for _, s := range list {
		if s["id"] == saleID {
			t.Error("Sale cancelada listada como pendente")
		}
	}
}

func TestIntegration_StockAdjustments(t *testing.T) {
	e := newFulfillmentEnv(t)
	pid := e.product()

	bad := []map[string]any{
		{"product_id": pid, "quantity": 0, "reason": "OTHER"},
		{"product_id": pid, "quantity": 1, "reason": "X"},
		{"product_id": "00000000-0000-0000-0000-000000000000", "quantity": 1, "reason": "OTHER"},
		{"quantity": 1, "reason": "OTHER"},
		{"product_id": pid, "quantity": 1, "reason": "OTHER", "adjusted_at": "18/11/2026"},
	}
	for i, body := range bad {
		if code, _ := e.api.do("POST", "/stock/adjustments", body); code != 400 {
			t.Errorf("ajuste inválido #%d = %d, want 400", i, code)
		}
	}
	code, a := e.api.do("POST", "/stock/adjustments", map[string]any{"product_id": pid, "quantity": -2.5, "reason": "COUNT", "notes": "quebra"})
	if code != 201 || num(a, "quantity") != -2.5 || a["unit_cost"] != nil || a["adjusted_at"] == nil || a["product_name"] == nil || a["notes"] != "quebra" {
		t.Errorf("POST ajuste = %d %v", code, a)
	}
	code, list := e.api.list("/stock/adjustments?product_id=" + pid)
	if code != 200 || len(list) != 1 || list[0]["id"] != a["id"] {
		t.Errorf("GET ajustes = %d %v", code, list)
	}
	if b := e.balance(pid); num(b, "balance") != -2.5 || b["last_inflow_cost"] != nil || b["unit"] != "m2" {
		t.Errorf("balance = %v", b)
	}
	// Imutáveis: não há PUT/DELETE.
	if code, _ := e.api.do("DELETE", "/stock/adjustments/"+a["id"].(string), nil); code != 404 && code != 405 {
		t.Errorf("DELETE ajuste = %d, want 404/405", code)
	}
	if code, _ := e.api.do("GET", "/stock/balance/00000000-0000-0000-0000-000000000000", nil); code != 404 {
		t.Errorf("balance de Product inexistente = %d, want 404", code)
	}
	_, found := e.api.list("/stock/balance?q=" + "Pedra")
	if len(found) == 0 {
		t.Error("busca q=Pedra vazia")
	}
}
