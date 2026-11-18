package tools_test

import (
	"math"
	"testing"
)

func TestIntegration_AddPurchaseCharge(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture()
	created := e.ok("create_purchase", map[string]any{"kind": "STOCK_REPLENISHMENT", "supplier_id": f.supplier,
		"items": []map[string]any{{"product_id": f.product, "quantity": 100, "unit_cost": 50}}})
	pid := obj(created["purchase"])["id"].(string)

	out := e.ok("add_purchase_charge", map[string]any{"purchase_id": pid, "kind": "FREIGHT", "amount": 500, "description": "Frete SP"})
	ch := obj(out["charge"])
	if ch["kind"] != "FREIGHT" || ch["amount"] != float64(500) || ch["payee_supplier_id"] != f.supplier || ch["obligation_id"] == nil || ch["description"] != "Frete SP" {
		t.Fatalf("charge = %v", ch)
	}
	if len(out["warnings"].([]any)) != 0 {
		t.Errorf("warnings = %v", out["warnings"])
	}
	p := obj(out["purchase"])
	comp := obj(p["composition"])
	if comp["goods_total"] != float64(5000) || comp["charges_total"] != float64(500) || comp["total"] != float64(5500) || len(comp["charges"].([]any)) != 1 {
		t.Errorf("composition = %v", comp)
	}
	if p["payable"] != float64(5500) || p["open"] != float64(5500) || p["paid"] != float64(0) || p["allocation_method"] != "BY_QUANTITY" {
		t.Errorf("payable/open/paid = %v/%v/%v", p["payable"], p["open"], p["paid"])
	}
	for _, gone := range []string{"payment_status", "paid_amount", "paid_at", "to_pay"} {
		if _, has := p[gone]; has {
			t.Errorf("get_purchase não deve trazer %s", gone)
		}
	}
	l := obj(p["lines"].([]any)[0])
	if l["line_type"] != "STOCK_ITEM" || l["allocated_charges"] != float64(500) || l["landed_total"] != float64(5500) || l["landed_unit_cost"] != float64(55) {
		t.Errorf("linha = %v", l)
	}
	if obs := p["obligations"].([]any); len(obs) != 1 || obj(obs[0])["amount"] != float64(5500) {
		t.Errorf("obligations = %v", p["obligations"])
	}

	gp := obj(e.ok("get_purchase", map[string]any{"purchase_id": pid})["purchase"])
	if obj(gp["composition"])["total"] != float64(5500) || len(gp["lines"].([]any)) != 1 {
		t.Errorf("get_purchase = %v", gp)
	}

	args := map[string]any{"purchase_id": pid, "kind": "UNLOADING", "amount": 100}
	first := e.ok("add_purchase_charge", args)
	dup := e.ok("add_purchase_charge", args)
	if dup["deduplicated"] != true || obj(dup["charge"])["id"] != obj(first["charge"])["id"] {
		t.Errorf("dedupe = %v", dup)
	}
	args["allow_duplicate"] = true
	if again := e.ok("add_purchase_charge", args); obj(again["charge"])["id"] == obj(first["charge"])["id"] {
		t.Error("allow_duplicate deveria criar outro custo")
	}
	if n := len(obj(obj(e.ok("get_purchase", map[string]any{"purchase_id": pid})["purchase"])["composition"])["charges"].([]any)); n != 3 {
		t.Errorf("custos = %d, want 3", n)
	}

	e.ok("set_purchase_status", map[string]any{"purchase_id": pid, "status": "ORDERED"})
	e.ok("set_purchase_status", map[string]any{"purchase_id": pid, "status": "RECEIVED"})
	sb := obj(e.ok("get_stock_balance", map[string]any{"product_id": f.product})["balance"])
	if cost := sb["last_inflow_cost"].(float64); math.Abs(cost-57) > 0.005 {
		t.Errorf("last_inflow_cost = %v, want 57", cost)
	}
	if sv, ok := sb["stock_value"].(float64); !ok || sv <= 0 {
		t.Errorf("stock_value = %v", sb["stock_value"])
	}
	list := e.ok("get_stock_balance", map[string]any{"query": "Escrita"})
	if tv, ok := list["total_stock_value"].(float64); !ok || tv < sb["stock_value"].(float64) {
		t.Errorf("total_stock_value = %v", list["total_stock_value"])
	}

	e.fail("add_purchase_charge", map[string]any{"purchase_id": pid, "kind": "FREIGHT", "amount": 0}, "INVALID_INPUT", "")
	e.fail("add_purchase_charge", map[string]any{"purchase_id": pid, "kind": "FRETE", "amount": 5}, "INVALID_INPUT", "")
	e.fail("add_purchase_charge", map[string]any{"purchase_id": pid, "kind": "FREIGHT", "amount": 5.123}, "INVALID_INPUT", "2 casas")
	e.fail("add_purchase_charge", map[string]any{"purchase_id": uuidNew(), "kind": "FREIGHT", "amount": 5}, "NOT_FOUND", "")
	e.fail("add_purchase_charge", map[string]any{"purchase_id": pid, "kind": "FREIGHT", "amount": 5, "payee_supplier_id": uuidNew()}, "NOT_FOUND", "")
}
