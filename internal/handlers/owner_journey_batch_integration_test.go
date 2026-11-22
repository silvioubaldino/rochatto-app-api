package handlers

// C6 (AYD-011): atendimento em lote dos Items da Sale.

import (
	"testing"
)

func batchBody(items ...map[string]any) map[string]any { return map[string]any{"items": items} }

func TestJourney_BatchFulfillment(t *testing.T) {
	e := newJourneyEnv(t)
	p1, p2 := e.product(), e.product()
	sup := e.supplier()
	e.must("POST", "/stock/adjustments", map[string]any{"product_id": p2, "quantity": 10, "unit_cost": 33, "reason": "INITIAL", "adjusted_at": "2026-01-01"}, 201)
	saleID, ids := e.sale(qItem("A", 4, 100, 10, p1), qItem("B", 2, 50, 10, p2), qItem("C", 1, 10, 1, ""))
	path := "/vendas/" + saleID + "/itens/fulfillment"

	res := e.must("PATCH", path, batchBody(
		map[string]any{"id": ids[0], "origin": "FROM_STOCK", "fornecedor_id": nil}, // sem saldo → aviso
		map[string]any{"id": ids[1], "origin": "FROM_STOCK"},                       // com saldo
		map[string]any{"id": ids[2], "origin": "MADE_TO_ORDER", "fornecedor_id": sup},
	), 200)
	itens := res["itens"].([]any)
	if len(itens) != 3 {
		t.Fatalf("itens = %v", itens)
	}
	a, b, c := itens[0].(map[string]any), itens[1].(map[string]any), itens[2].(map[string]any)
	if a["origin"] != "FROM_STOCK" || num(a, "stock_balance") != -4 || b["origin"] != "FROM_STOCK" || num(b, "stock_balance") != 8 {
		t.Errorf("estoque: %v / %v", a, b)
	}
	if num(b, "custo_unit") != 33 || c["fornecedor_id"] != sup || c["stock_balance"] != nil {
		t.Errorf("custo/fornecedor: %v / %v", b, c)
	}
	ws := res["warnings"].([]any)
	if len(ws) != 1 || ws[0].(map[string]any)["item_id"] != ids[0] || ws[0].(map[string]any)["code"] != "INSUFFICIENT_STOCK" {
		t.Errorf("warnings = %v", ws)
	}
	e.wantNum("saldo p2", e.balance(p2), "balance", 8)
}

func TestJourney_BatchFulfillmentErrorsAreAtomic(t *testing.T) {
	e := newJourneyEnv(t)
	p1 := e.product()
	sup, sup2 := e.supplier(), e.supplier()
	saleID, ids := e.sale(qItem("A", 1, 100, 10, p1), qItem("B", 1, 50, 10, ""), e.serviceItem("Serv", 1, 10, 1))
	other, otherIDs := e.sale(qItem("Outra", 1, 10, 1, ""))
	_ = other
	path := "/vendas/" + saleID + "/itens/fulfillment"
	origin := func(id string) any {
		var o *string
		e.db.Raw(`SELECT origin::text FROM itens_venda WHERE id = ?`, id).Scan(&o)
		if o == nil {
			return nil
		}
		return *o
	}

	cases := []struct {
		name   string
		body   map[string]any
		status int
		itemID string
	}{
		{"FROM_STOCK sem product", batchBody(map[string]any{"id": ids[0], "origin": "FROM_STOCK"}, map[string]any{"id": ids[1], "origin": "FROM_STOCK"}), 400, ids[1]},
		{"serviço", batchBody(map[string]any{"id": ids[0], "origin": "FROM_STOCK"}, map[string]any{"id": ids[2], "origin": "MADE_TO_ORDER"}), 400, ids[2]},
		{"item de outra venda", batchBody(map[string]any{"id": ids[0], "origin": "FROM_STOCK"}, map[string]any{"id": otherIDs[0], "origin": "MADE_TO_ORDER"}), 400, otherIDs[0]},
		{"fornecedor inexistente", batchBody(map[string]any{"id": ids[0], "origin": "FROM_STOCK"}, map[string]any{"id": ids[1], "origin": "MADE_TO_ORDER", "fornecedor_id": "00000000-0000-0000-0000-000000000001"}), 400, ids[1]},
	}
	for _, c := range cases {
		code, res := e.api.do("PATCH", path, c.body)
		if code != c.status || res["item_id"] != c.itemID {
			t.Errorf("%s: %d %v, want %d item_id=%s", c.name, code, res, c.status, c.itemID)
		}
		if got := origin(ids[0]); got != "MADE_TO_ORDER" {
			t.Errorf("%s: o 1º Item mudou para %v (não é tudo ou nada)", c.name, got)
		}
	}
	// Forma do corpo → 400.
	e.must("PATCH", path, map[string]any{"items": []any{}}, 400)
	e.must("PATCH", path, batchBody(map[string]any{"id": ids[0], "origin": "NOPE"}), 400)
	e.must("PATCH", path, batchBody(map[string]any{"id": ids[0], "origin": "FROM_STOCK"}, map[string]any{"id": ids[0], "origin": "FROM_STOCK"}), 400)
	many := make([]map[string]any, 51)
	for i := range many {
		many[i] = map[string]any{"id": ids[0], "origin": "FROM_STOCK"}
	}
	e.must("PATCH", path, batchBody(many...), 400)
	e.must("PATCH", "/vendas/00000000-0000-0000-0000-000000000001/itens/fulfillment", batchBody(map[string]any{"id": ids[0], "origin": "FROM_STOCK"}), 404)

	// Item travado em Purchase: mudar fornecedor/origem → 409 com item_id; mesmo valor passa.
	e.must("PUT", "/vendas/"+saleID+"/itens/"+ids[1], map[string]any{"produto_nome": "B", "quantidade": 1, "preco_venda_unit": 50, "origin": "MADE_TO_ORDER", "fornecedor_id": sup}, 200)
	e.must("POST", "/purchases", map[string]any{"kind": "MADE_TO_ORDER", "supplier_id": sup, "sale_item_ids": []string{ids[1]}}, 201)
	code, res := e.api.do("PATCH", path, batchBody(map[string]any{"id": ids[0], "origin": "FROM_STOCK"}, map[string]any{"id": ids[1], "origin": "MADE_TO_ORDER", "fornecedor_id": sup2}))
	if code != 409 || res["item_id"] != ids[1] {
		t.Errorf("item em compra: %d %v", code, res)
	}
	if got := origin(ids[0]); got != "MADE_TO_ORDER" {
		t.Errorf("1º Item mudou (%v) apesar do 409", got)
	}
	e.must("PATCH", path, batchBody(map[string]any{"id": ids[1], "origin": "MADE_TO_ORDER", "fornecedor_id": sup}), 200)

	// Sale cancelada → 409.
	e.must("PATCH", "/vendas/"+other+"/delivery-status", map[string]any{"delivery_status": "CANCELED"}, 200)
	e.must("PATCH", "/vendas/"+other+"/itens/fulfillment", batchBody(map[string]any{"id": otherIDs[0], "origin": "MADE_TO_ORDER"}), 409)
}
