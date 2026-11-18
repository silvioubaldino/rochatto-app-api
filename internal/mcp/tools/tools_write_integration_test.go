package tools_test

// Testes de integração das ferramentas de ESCRITA (SPEC-007 / AYD-006 §4): cada ferramenta
// roda pelo pipeline real (scope, validação, dedupe, auditoria), pelo Gateway em processo,
// sobre os handlers REST de verdade, num Postgres descartável (TEST_DATABASE_URL).
import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/silvioubaldino/sales-backend/internal/mcp"
)

// writeFixture: um cliente, um fornecedor, dois produtos e uma Sale (venda rápida) de 1 item.
type writeFixture struct {
	customer, supplier, product, product2 string
	sale, item                            string
}

func (e *env) writeFixture() writeFixture {
	var f writeFixture
	f.customer = obj(e.ok("create_customer", map[string]any{"name": "Cliente Escrita", "city": "Vitória"})["customer"])["id"].(string)
	f.supplier = e.mustREST("POST", "/fornecedores", map[string]any{"nome": "Pedras Escrita"}, 201)["id"].(string)
	f.product = e.mustREST("POST", "/produtos", map[string]any{"nome": "Granito Escrita", "unidade": "m2"}, 201)["id"].(string)
	f.product2 = e.mustREST("POST", "/produtos", map[string]any{"nome": "Mármore Escrita", "unidade": "m2"}, 201)["id"].(string)
	out := e.ok("create_quick_sale", map[string]any{"customer_id": f.customer, "items": []map[string]any{
		{"product_id": f.product, "product_name": "Granito Escrita", "quantity": 4, "unit_price": 500, "unit_cost": 300}}})
	sale := obj(out["sale"])
	f.sale = sale["id"].(string)
	f.item = obj(sale["items"].([]any)[0])["id"].(string)
	return f
}

// receiptCount conta os "recebimentos" da Sale como get_sale os mostra: quitações da conta a
// receber + parcelas ainda não cobertas (AYD-007).
func (e *env) receiptCount(saleID string) int {
	e.t.Helper()
	list := e.mustREST("GET", "/obligations?origin_type=SALE&origin_id="+saleID+"&direction=RECEIVABLE", nil, 200)["items"].([]any)
	if len(list) == 0 {
		return 0
	}
	d := e.mustREST("GET", "/obligations/"+obj(list[0])["id"].(string), nil, 200)
	n := len(d["settlements"].([]any))
	if d["open"].(float64) > 0 {
		for _, i := range d["installments"].([]any) {
			if obj(i)["status"] != "COVERED" {
				n++
			}
		}
	}
	return n
}

func (e *env) toolCalls(tool string) (n int64) {
	e.db.Raw(`SELECT COUNT(*) FROM mcp_tool_calls WHERE tool = ?`, tool).Scan(&n)
	return
}

func TestIntegration_WriteQuoteToSaleFlow(t *testing.T) {
	e := newEnv(t)
	cust := obj(e.ok("create_customer", map[string]any{"name": "João Novo", "phone": "27 9999", "lead_source": "INSTAGRAM"})["customer"])
	if cust["name"] != "João Novo" || cust["phone"] != "27 9999" || cust["lead_source"] != "INSTAGRAM" {
		t.Fatalf("customer EN = %v", cust)
	}
	if out := e.ok("create_customer", map[string]any{"name": "Sem Warnings"}); len(out["warnings"].([]any)) != 0 {
		t.Errorf("warnings deve ser [] = %v", out)
	}
	e.fail("create_customer", map[string]any{"name": "Parceiro", "lead_source": "PARTNER"}, "INVALID_INPUT", "referrer_id")
	e.fail("create_customer", map[string]any{"name": "x", "extra": 1}, "INVALID_INPUT", "")

	prod := e.mustREST("POST", "/produtos", map[string]any{"nome": "Granito Fluxo", "unidade": "m2"}, 201)["id"].(string)
	qItems := []map[string]any{
		{"product_id": prod, "product_name": "Granito Fluxo", "quantity": 2.5, "unit_price": 520.5, "unit_cost": 300},
		{"product_name": "Item avulso", "quantity": 1, "unit_price": 100, "unit_cost": 60},
	}
	cid := cust["id"].(string)
	q := obj(e.ok("create_quote", map[string]any{"customer_id": cid, "notes": "orçamento", "items": qItems})["quote"])
	if q["status"] != "OPEN" || q["customer_name"] != "João Novo" || q["total_quote"] != 1401.25 || len(q["items"].([]any)) != 2 || q["notes"] != "orçamento" {
		t.Fatalf("quote = %v", q)
	}
	e.fail("create_quote", map[string]any{"customer_id": cid, "items": []any{}}, "INVALID_INPUT", "")
	e.fail("create_quote", map[string]any{"customer_id": uuidNew(), "items": qItems}, "INVALID_INPUT", "customer não encontrado")
	e.fail("create_quote", map[string]any{"customer_id": cid, "items": []map[string]any{{"product_name": "x", "quantity": 1, "unit_price": 1.005, "unit_cost": 0}}}, "INVALID_INPUT", "2 casas")
	e.fail("create_quote", map[string]any{"customer_id": cid, "items": []map[string]any{{"product_name": "x", "quantity": 0, "unit_price": 1, "unit_cost": 0}}}, "INVALID_INPUT", "")

	qid := q["id"].(string)
	lost := e.ok("set_quote_status", map[string]any{"quote_id": qid, "status": "LOST", "lost_reason": "preço"})
	if lost["status"] != "LOST" || lost["lost_reason"] != "preço" || lost["quote_id"] != qid {
		t.Errorf("LOST = %v", lost)
	}
	if reopened := e.ok("set_quote_status", map[string]any{"quote_id": qid, "status": "OPEN"}); reopened["status"] != "OPEN" || reopened["lost_reason"] != nil {
		t.Errorf("OPEN = %v", reopened)
	}
	e.fail("set_quote_status", map[string]any{"quote_id": qid, "status": "WON"}, "INVALID_INPUT", "")
	e.fail("set_quote_status", map[string]any{"quote_id": uuidNew(), "status": "LOST"}, "NOT_FOUND", "quote não encontrado")

	conv := e.ok("convert_quote_to_sale", map[string]any{"quote_id": qid})
	sale := obj(conv["sale"])
	if conv["quote_id"] != qid || sale["quote_id"] != qid || sale["customer_name"] != "João Novo" || sale["total"] != 1401.25 ||
		sale["delivery_status"] != "TO_DELIVER" || len(sale["items"].([]any)) != 2 || len(sale["receipts"].([]any)) != 0 {
		t.Fatalf("sale = %v", conv)
	}
	e.fail("convert_quote_to_sale", map[string]any{"quote_id": qid}, "", "") // já WON
	e.fail("convert_quote_to_sale", map[string]any{"quote_id": uuidNew()}, "NOT_FOUND", "")

	qs := e.ok("create_quick_sale", map[string]any{"customer_id": cid, "items": qItems[:1]})
	if obj(qs["sale"])["total"] != 1301.25 || qs["quote_id"] == nil || obj(qs["sale"])["quote_id"] != qs["quote_id"] {
		t.Errorf("quick sale = %v", qs)
	}
	e.fail("create_quick_sale", map[string]any{"customer_id": cid, "items": []any{}}, "INVALID_INPUT", "")
}

// Limites da deduplicação (120 s, mesma Connection, JSON canônico) exercitados em record_expense.
func TestIntegration_WriteDedupeLimits(t *testing.T) {
	e := newEnv(t)
	args := map[string]any{"category": "VEHICLE", "amount": 500, "occurred_on": day(0), "payment_method": "PIX"}
	r1 := e.ok("record_expense", args)
	mv := obj(r1["movement"])
	if r1["deduplicated"] != nil || e.movementCount("VEHICLE") != 1 {
		t.Fatalf("record_expense = %v", r1)
	}
	if r2 := e.ok("record_expense", args); r2["deduplicated"] != true || obj(r2["movement"])["id"] != mv["id"] || e.movementCount("VEHICLE") != 1 {
		t.Fatalf("2ª chamada deveria deduplicar: %v", r2)
	}
	// allow_duplicate força outra movimentação (e não entra no hash); a seguinte volta a deduplicar.
	args3 := map[string]any{"category": "VEHICLE", "amount": 500, "occurred_on": day(0), "payment_method": "PIX", "allow_duplicate": true}
	if r3 := e.ok("record_expense", args3); r3["deduplicated"] != nil || obj(r3["movement"])["id"] == mv["id"] || e.movementCount("VEHICLE") != 2 {
		t.Fatalf("allow_duplicate = %v", r3)
	}
	if r4 := e.ok("record_expense", args); r4["deduplicated"] != true || e.movementCount("VEHICLE") != 2 {
		t.Errorf("4ª = %v", r4)
	}

	t.Run("limites", func(t *testing.T) {
		// Ordem diferente das chaves = argumentos idênticos (JSON canônico).
		_, _ = e.callRaw(e.pr, "record_expense", `{"category":"VEHICLE","amount":10,"payment_method":"CASH","occurred_on":"`+day(0)+`"}`)
		out, isErr := e.callRaw(e.pr, "record_expense", `{"occurred_on":"`+day(0)+`","payment_method":"CASH","amount":10,"category":"VEHICLE"}`)
		if isErr || out["deduplicated"] != true || e.movementCount("VEHICLE") != 3 {
			t.Errorf("ordem das chaves: %v (movimentações=%d)", out, e.movementCount("VEHICLE"))
		}
		// Argumentos diferentes → cria.
		e.ok("record_expense", map[string]any{"category": "VEHICLE", "amount": 11, "occurred_on": day(0), "payment_method": "CASH"})
		if e.movementCount("VEHICLE") != 4 {
			t.Error("argumentos diferentes devem criar")
		}
		// Outra Connection → cria (a chave inclui a Connection).
		other := e.newConnection("read", "write")
		if out, _ := e.callRaw(other, "record_expense", `{"category":"VEHICLE","amount":11,"occurred_on":"`+day(0)+`","payment_method":"CASH"}`); out["deduplicated"] != nil || e.movementCount("VEHICLE") != 5 {
			t.Errorf("outra Connection: %v", out)
		}
		// Mais de 120 s depois → cria.
		e.skew = 121 * time.Second
		defer func() { e.skew = 0 }()
		if out := e.ok("record_expense", args); out["deduplicated"] != nil || e.movementCount("VEHICLE") != 6 {
			t.Errorf("após 120 s: %v", out)
		}
	})

	t.Run("erro anterior não deduplica", func(t *testing.T) {
		bad := map[string]any{"category": "LAZER", "amount": 50}
		e.fail("record_expense", bad, "INVALID_INPUT", "")
		e.fail("record_expense", bad, "INVALID_INPUT", "") // continua erro, nunca "deduplicated"
		var withResult int64
		e.db.Raw(`SELECT COUNT(*) FROM mcp_tool_calls WHERE tool='record_expense' AND outcome='ERROR' AND result IS NOT NULL`).Scan(&withResult)
		if withResult != 0 {
			t.Errorf("chamadas com erro não guardam result (%d)", withResult)
		}
		before := e.movementCount("OTHER_EXPENSE")
		bad["category"] = "OTHER_EXPENSE"
		if out := e.ok("record_expense", bad); out["deduplicated"] != nil || e.movementCount("OTHER_EXPENSE") != before+1 {
			t.Errorf("corrigido deve criar: %v", out)
		}
	})
}

func TestIntegration_WriteSalePartialUpdate(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture()
	e.mustREST("PUT", "/vendas/"+f.sale, map[string]any{"frete_valor": 300, "observacoes": "x", "data_entrega_prevista": "2026-12-20", "comissao_valor": 40}, 200)

	// Critério de aceite: só o frete pago muda — vira quitação da conta a pagar do frete (AYD-007).
	out := e.ok("update_sale", map[string]any{"sale_id": f.sale, "freight_paid": 300})
	s := obj(out["sale"])
	if s["freight_paid"] != float64(300) || s["freight_amount"] != float64(300) || s["notes"] != "x" || s["expected_delivery_date"] != "2026-12-20" ||
		s["commission_amount"] != float64(40) || s["freight_payable"] != float64(0) || s["commission_paid"] != false {
		t.Fatalf("update_sale = %v", s)
	}
	got := e.mustREST("GET", "/vendas/"+f.sale, nil, 200)
	if got["freight_payable"] != float64(0) || got["commission_payable"] != float64(40) || got["frete_valor"] != float64(300) || got["observacoes"] != "x" {
		t.Errorf("REST = %v", got)
	}
	if _, has := got["frete_pago"]; has {
		t.Error("frete_pago não existe mais na API")
	}
	var freightMovements int64
	e.db.Raw(`SELECT COUNT(*) FROM financial_movements m JOIN settlements s ON s.movement_id = m.id AND s.deleted_at IS NULL
		JOIN obligations o ON o.id = s.obligation_id WHERE o.sale_id = ? AND m.category = 'FREIGHT' AND m.amount = -300`, f.sale).Scan(&freightMovements)
	if freightMovements != 1 {
		t.Errorf("o frete pago deveria ser 1 movimentação FREIGHT de −300 quitando a conta: %d", freightMovements)
	}
	// Repetir o mesmo valor não paga de novo (idempotente).
	e.ok("update_sale", map[string]any{"sale_id": f.sale, "freight_paid": 300})
	e.db.Raw(`SELECT COUNT(*) FROM financial_movements m JOIN settlements s ON s.movement_id = m.id JOIN obligations o ON o.id = s.obligation_id WHERE o.sale_id = ? AND m.category = 'FREIGHT'`, f.sale).Scan(&freightMovements)
	if freightMovements != 1 {
		t.Errorf("update_sale deveria ser idempotente: %d movimentações de frete", freightMovements)
	}

	// commission_paid true quita a comissão (movimentação COMMISSION); false não desfaz pagamento.
	if c := obj(e.ok("update_sale", map[string]any{"sale_id": f.sale, "commission_paid": true})["sale"]); c["commission_paid"] != true || c["freight_paid"] != float64(300) {
		t.Errorf("commission_paid = %v", c)
	}
	if got = e.mustREST("GET", "/vendas/"+f.sale, nil, 200); got["commission_payable"] != float64(0) || got["freight_payable"] != float64(0) {
		t.Errorf("commission_paid REST = %v", got)
	}
	e.fail("update_sale", map[string]any{"sale_id": f.sale, "commission_paid": false}, "INVALID_INPUT", "desfazer")
	e.fail("update_sale", map[string]any{"sale_id": f.sale, "freight_paid": 100}, "INVALID_INPUT", "reduzir")
	e.ok("update_sale", map[string]any{"sale_id": f.sale, "freight_amount": 450})
	if got = e.mustREST("GET", "/vendas/"+f.sale, nil, 200); got["frete_valor"] != float64(450) || got["freight_payable"] != float64(150) {
		t.Errorf("freight_amount = %v", got)
	}
	e.ok("update_sale", map[string]any{"sale_id": f.sale, "expected_delivery_date": nil, "notes": nil})
	if got = e.mustREST("GET", "/vendas/"+f.sale, nil, 200); got["data_entrega_prevista"] != nil || got["observacoes"] != nil || got["frete_valor"] != float64(450) {
		t.Errorf("null limpa = %v", got)
	}
	e.ok("update_sale", map[string]any{"sale_id": f.sale, "expected_delivery_date": "2027-01-10", "notes": "novo"})
	if got = e.mustREST("GET", "/vendas/"+f.sale, nil, 200); got["data_entrega_prevista"] != "2027-01-10" || got["observacoes"] != "novo" {
		t.Errorf("novos = %v", got)
	}

	// Validações: ≥ 1 campo; regra da API (frete pago > frete); campo fora do schema; null onde não pode.
	e.fail("update_sale", map[string]any{"sale_id": f.sale}, "INVALID_INPUT", "ao menos um campo")
	e.fail("update_sale", map[string]any{"sale_id": f.sale, "freight_paid": 9999}, "INVALID_INPUT", "frete_pago não pode ser maior")
	e.fail("update_sale", map[string]any{"sale_id": f.sale, "freight_amount": nil}, "INVALID_INPUT", "")
	e.fail("update_sale", map[string]any{"sale_id": f.sale, "freight_amount": -1}, "INVALID_INPUT", "")
	e.fail("update_sale", map[string]any{"sale_id": f.sale, "customer_id": uuidNew()}, "INVALID_INPUT", "")
	e.fail("update_sale", map[string]any{"sale_id": uuidNew(), "notes": "x"}, "NOT_FOUND", "venda não encontrada")
}

func TestIntegration_WriteSaleItemsDeliveryAndCancel(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture()

	// Parcial: só a quantidade muda; os demais campos permanecem.
	it := obj(e.ok("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "quantity": 6.5, "unit_price": 510})["item"])
	if it["quantity"] != 6.5 || it["unit_price"] != float64(510) || it["unit_cost"] != float64(300) || it["product_name"] != "Granito Escrita" || it["product_id"] != f.product {
		t.Fatalf("item = %v", it)
	}
	// Fornecedor: nome acompanha o id; null limpa.
	it = obj(e.ok("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "origin": "MADE_TO_ORDER", "supplier_id": f.supplier})["item"])
	if it["supplier_id"] != f.supplier || it["supplier_name"] != "Pedras Escrita" || it["origin"] != "MADE_TO_ORDER" {
		t.Fatalf("fornecedor = %v", it)
	}
	// FROM_STOCK sem saldo: permitido, com aviso; o custo vem da API (RN-16: sem entrada, mantém).
	out := e.ok("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "origin": "FROM_STOCK"})
	if w := out["warnings"].([]any); len(w) != 1 || w[0] != "INSUFFICIENT_STOCK" || obj(out["item"])["origin"] != "FROM_STOCK" {
		t.Errorf("FROM_STOCK = %v", out)
	}
	// Com entrada de estoque, FROM_STOCK passa a usar o custo da última entrada e some o aviso.
	e.ok("create_stock_adjustment", map[string]any{"product_id": f.product, "quantity": 100, "reason": "INITIAL", "unit_cost": 280})
	out = e.ok("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "origin": "MADE_TO_ORDER"})
	out = e.ok("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "origin": "FROM_STOCK"})
	if len(out["warnings"].([]any)) != 0 {
		t.Errorf("com saldo não deve avisar: %v", out)
	}
	if c := obj(out["item"])["unit_cost"]; c != float64(300) && c != float64(280) {
		t.Errorf("custo = %v", c)
	}
	e.ok("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "origin": "MADE_TO_ORDER", "supplier_id": nil})
	e.fail("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item}, "INVALID_INPUT", "ao menos um campo")
	e.fail("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": uuidNew(), "quantity": 1}, "NOT_FOUND", "item não encontrado")
	e.fail("update_sale_item", map[string]any{"sale_id": uuidNew(), "item_id": f.item, "quantity": 1}, "NOT_FOUND", "venda não encontrada")
	e.fail("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "supplier_id": uuidNew()}, "INVALID_INPUT", "fornecedor não encontrado")
	e.fail("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "quantity": 0}, "INVALID_INPUT", "")
	e.fail("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "quantity": 1.0005}, "INVALID_INPUT", "3 casas")

	// Item em Purchase pedida: campos travados → CONFLICT da API.
	e.ok("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "supplier_id": f.supplier})
	pid := obj(e.ok("create_purchase", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": f.supplier, "sale_item_ids": []string{f.item}})["purchase"])["id"].(string)
	e.ok("set_purchase_status", map[string]any{"purchase_id": pid, "status": "ORDERED"})
	e.fail("update_sale_item", map[string]any{"sale_id": f.sale, "item_id": f.item, "quantity": 99}, "CONFLICT", "")
	e.fail("cancel_sale", map[string]any{"sale_id": f.sale}, "CONFLICT", "compra pedida ou recebida")

	// Entrega.
	d := e.ok("set_sale_delivery_status", map[string]any{"sale_id": f.sale, "delivery_status": "DELIVERED"})
	if d["delivery_status"] != "DELIVERED" || d["sale_id"] != f.sale || len(d["warnings"].([]any)) != 0 {
		t.Errorf("delivered = %v", d)
	}
	if d = e.ok("set_sale_delivery_status", map[string]any{"sale_id": f.sale, "delivery_status": "TO_DELIVER"}); d["delivery_status"] != "TO_DELIVER" {
		t.Errorf("to_deliver = %v", d)
	}
	e.fail("set_sale_delivery_status", map[string]any{"sale_id": f.sale, "delivery_status": "CANCELED"}, "INVALID_INPUT", "")
	e.fail("set_sale_delivery_status", map[string]any{"sale_id": uuidNew(), "delivery_status": "DELIVERED"}, "NOT_FOUND", "venda não encontrada")

	// Cancelamento: libera Items de Purchase aberta e é terminal.
	g := e.writeFixture()
	e.ok("update_sale_item", map[string]any{"sale_id": g.sale, "item_id": g.item, "origin": "MADE_TO_ORDER", "supplier_id": g.supplier})
	openP := obj(e.ok("create_purchase", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": g.supplier, "sale_item_ids": []string{g.item}})["purchase"])["id"].(string)
	c := e.ok("cancel_sale", map[string]any{"sale_id": g.sale})
	if c["delivery_status"] != "CANCELED" || c["sale_id"] != g.sale {
		t.Errorf("cancel_sale = %v", c)
	}
	if got := e.mustREST("GET", "/vendas/"+g.sale, nil, 200); got["delivery_status"] != "CANCELED" || obj(got["itens"].([]any)[0])["purchase_id"] != nil {
		t.Errorf("Item deveria ser liberado da Purchase %s: %v", openP, got)
	}
	e.fail("set_sale_delivery_status", map[string]any{"sale_id": g.sale, "delivery_status": "DELIVERED"}, "CONFLICT", "cancelada")
	e.fail("update_sale_item", map[string]any{"sale_id": g.sale, "item_id": g.item, "quantity": 1}, "CONFLICT", "")
}

func TestIntegration_WriteProcurementAndStock(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture()
	g := e.writeFixture() // 2ª venda, mesmo fornecedor lógico (outro registro)
	for _, x := range []writeFixture{f, g} {
		e.ok("update_sale_item", map[string]any{"sale_id": x.sale, "item_id": x.item, "origin": "MADE_TO_ORDER", "supplier_id": f.supplier})
	}

	// Compra consolidada MADE_TO_ORDER (N Sales → 1 Purchase) + inclusão posterior de itens.
	cp := e.ok("create_purchase", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": f.supplier, "expected_date": "2026-12-10", "notes": "lote", "sale_item_ids": []string{f.item}})
	p := obj(cp["purchase"])
	pid := p["id"].(string)
	if p["status"] != "OPEN" || p["supplier_name"] != "Pedras Escrita" || p["total_cost"] != float64(1200) || p["expected_date"] != "2026-12-10" || p["notes"] != "lote" ||
		len(p["sale_items"].([]any)) != 1 || obj(p["sale_items"].([]any)[0])["product_name"] != "Granito Escrita" || obj(p["sale_items"].([]any)[0])["unit_cost"] != float64(300) {
		t.Fatalf("create_purchase = %v", p)
	}
	if dup := e.ok("create_purchase", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": f.supplier, "expected_date": "2026-12-10", "notes": "lote", "sale_item_ids": []string{f.item}}); dup["deduplicated"] != true || obj(dup["purchase"])["id"] != pid {
		t.Errorf("create_purchase deveria deduplicar: %v", dup)
	}
	add := obj(e.ok("add_sale_items_to_purchase", map[string]any{"purchase_id": pid, "sale_item_ids": []string{g.item}})["purchase"])
	if len(add["sale_items"].([]any)) != 2 || add["total_cost"] != float64(2400) {
		t.Fatalf("add_sale_items = %v", add)
	}
	e.fail("add_sale_items_to_purchase", map[string]any{"purchase_id": pid, "sale_item_ids": []string{}}, "INVALID_INPUT", "")
	e.fail("add_sale_items_to_purchase", map[string]any{"purchase_id": uuidNew(), "sale_item_ids": []string{g.item}}, "NOT_FOUND", "")
	e.fail("create_purchase", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": f.supplier, "sale_item_ids": []string{f.item}}, "INVALID_INPUT", "não aceita 'sale_item_ids'")
	e.fail("create_purchase", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": uuidNew(), "sale_item_ids": []string{f.item}}, "", "")

	// Status: OPEN → ORDERED → RECEIVED com as datas; made-to-order não movimenta Stock.
	o := e.ok("set_purchase_status", map[string]any{"purchase_id": pid, "status": "ORDERED"})
	if o["status"] != "ORDERED" || o["ordered_at"] == nil || o["purchase_id"] != pid {
		t.Errorf("ORDERED = %v", o)
	}
	e.fail("add_sale_items_to_purchase", map[string]any{"purchase_id": pid, "sale_item_ids": []string{f.item}}, "CONFLICT", "")
	rcv := e.ok("set_purchase_status", map[string]any{"purchase_id": pid, "status": "RECEIVED"})
	if rcv["status"] != "RECEIVED" || rcv["received_at"] == nil {
		t.Errorf("RECEIVED = %v", rcv)
	}
	e.fail("set_purchase_status", map[string]any{"purchase_id": uuidNew(), "status": "ORDERED"}, "NOT_FOUND", "")
	e.fail("set_purchase_status", map[string]any{"purchase_id": pid, "status": "EM_ROTA"}, "INVALID_INPUT", "")

	// Reposição de estoque: Purchase recebida gera Inflow; ajuste devolve o Balance.
	rp := obj(e.ok("create_purchase", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": f.supplier,
		"items": []map[string]any{{"product_id": f.product2, "quantity": 12.5, "unit_cost": 410.25}}})["purchase"])
	if rp["kind"] != "STOCK_REPLENISHMENT" || len(rp["items"].([]any)) != 1 || len(rp["sale_items"].([]any)) != 0 {
		t.Fatalf("reposição = %v", rp)
	}
	e.ok("set_purchase_status", map[string]any{"purchase_id": rp["id"], "status": "ORDERED"})
	e.ok("set_purchase_status", map[string]any{"purchase_id": rp["id"], "status": "RECEIVED"})
	bal := e.ok("get_stock_balance", map[string]any{"product_id": f.product2})
	if obj(bal["balance"])["balance"] != 12.5 || obj(bal["balance"])["inflow_qty"] != 12.5 {
		t.Errorf("inflow = %v", bal)
	}
	adj := e.ok("create_stock_adjustment", map[string]any{"product_id": f.product2, "quantity": -2.5, "reason": "COUNT", "notes": "contagem", "adjusted_at": "2026-11-01"})
	a, b := obj(adj["adjustment"]), obj(adj["balance"])
	if a["quantity"] != -2.5 || a["reason"] != "COUNT" || a["notes"] != "contagem" || a["adjusted_at"] != "2026-11-01" || a["product_id"] != f.product2 ||
		b["balance"] != float64(10) || b["adjustment_qty"] != -2.5 || b["product_name"] != "Mármore Escrita" {
		t.Errorf("create_stock_adjustment = %v", adj)
	}
	if dup := e.ok("create_stock_adjustment", map[string]any{"product_id": f.product2, "quantity": -2.5, "reason": "COUNT", "notes": "contagem", "adjusted_at": "2026-11-01"}); dup["deduplicated"] != true || obj(dup["adjustment"])["id"] != a["id"] {
		t.Errorf("ajuste deveria deduplicar: %v", dup)
	}
	if again := e.ok("get_stock_balance", map[string]any{"product_id": f.product2}); obj(again["balance"])["balance"] != float64(10) {
		t.Errorf("ajuste deduplicado não pode lançar de novo: %v", again)
	}
	e.ok("create_stock_adjustment", map[string]any{"product_id": f.product2, "quantity": -2.5, "reason": "COUNT", "notes": "contagem", "adjusted_at": "2026-11-01", "allow_duplicate": true})
	if again := e.ok("get_stock_balance", map[string]any{"product_id": f.product2}); obj(again["balance"])["balance"] != 7.5 {
		t.Errorf("allow_duplicate = %v", again)
	}
	e.fail("create_stock_adjustment", map[string]any{"product_id": f.product2, "quantity": 0, "reason": "COUNT"}, "INVALID_INPUT", "")
	e.fail("create_stock_adjustment", map[string]any{"product_id": f.product2, "quantity": 1, "reason": "PERDA"}, "INVALID_INPUT", "")
	e.fail("create_stock_adjustment", map[string]any{"product_id": uuidNew(), "quantity": 1, "reason": "COUNT"}, "", "")
}

func TestIntegration_WriteAudit(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture()
	exp := map[string]any{"category": "VEHICLE", "amount": 100, "occurred_on": day(0), "payment_method": "CASH"}
	r := e.ok("record_expense", exp)
	e.ok("record_expense", exp) // deduplicada
	e.ok("update_sale", map[string]any{"sale_id": f.sale, "notes": "audit"})
	e.fail("cancel_sale", map[string]any{"sale_id": uuidNew()}, "NOT_FOUND", "")

	type row struct {
		Tool, Outcome string
		DurationMS    int64
		HasResult     bool
		ErrorCode     *string
	}
	var rows []row
	e.db.Raw(`SELECT tool, outcome, duration_ms AS duration_ms, result IS NOT NULL AS has_result, error_code
		FROM mcp_tool_calls WHERE tool IN ('record_expense','update_sale','cancel_sale') ORDER BY created_at, call_id`).Scan(&rows)
	byTool := map[string][]row{}
	for _, x := range rows {
		byTool[x.Tool] = append(byTool[x.Tool], x)
	}
	rr := byTool["record_expense"]
	if len(rr) != 2 || rr[0].Outcome != "OK" || !rr[0].HasResult || rr[1].Outcome != "OK" || rr[1].HasResult {
		t.Errorf("record_expense: 1ª OK com result, deduplicada OK sem result: %+v", rr)
	}
	if u := byTool["update_sale"]; len(u) != 1 || u[0].Outcome != "OK" || u[0].HasResult {
		t.Errorf("update_sale (não dd) não guarda result: %+v", u)
	}
	if c := byTool["cancel_sale"]; len(c) != 1 || c[0].Outcome != "ERROR" || c[0].ErrorCode == nil || *c[0].ErrorCode != "NOT_FOUND" || c[0].HasResult {
		t.Errorf("cancel_sale com erro: %+v", c)
	}
	// O result guardado é a saída original (usada pela deduplicação).
	var stored string
	e.db.Raw(`SELECT result::text FROM mcp_tool_calls WHERE tool='record_expense' AND result IS NOT NULL`).Scan(&stored)
	var m map[string]any
	if json.Unmarshal([]byte(stored), &m) != nil || obj(m["movement"])["id"] != obj(r["movement"])["id"] {
		t.Errorf("result = %s", stored)
	}
}

// Connection só com read: nenhuma das escritas chega à API (também coberto no e2e via /mcp).
func TestIntegration_WriteRequiresWriteScope(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture()
	ro := e.newConnection("read")
	for _, tool := range mcp.Catalog() {
		if tool.Meta.ReadOnly {
			continue
		}
		out, isErr := e.callRaw(ro, tool.Meta.Name, `{"sale_id":"`+f.sale+`","amount":1,"quote_id":"`+f.sale+`"}`)
		er, _ := out["error"].(map[string]any)
		if !isErr || er["code"] != "FORBIDDEN" || !strings.Contains(er["message"].(string), "escrita") {
			t.Errorf("%s com scope read = %v", tool.Meta.Name, out)
		}
	}
	if e.receiptCount(f.sale) != 0 || e.movementCount("VEHICLE") != 0 {
		t.Error("nenhuma escrita deveria ter chegado à API")
	}
	var n int64
	e.db.Raw(`SELECT COUNT(*) FROM mcp_tool_calls WHERE connection_id = ? AND outcome = 'ERROR' AND error_code = 'FORBIDDEN'`, ro.ConnectionID).Scan(&n)
	if n != 17 {
		t.Errorf("auditoria FORBIDDEN = %d, quero 17", n)
	}
	_ = context.Background
}
