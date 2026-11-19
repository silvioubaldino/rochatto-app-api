package tools_test

// Testes de integração das ferramentas de serviços (AYD-010 / SPEC-011): search_services,
// get_top_services e kind/service_id nas ferramentas de orçamento e venda. Só com TEST_DATABASE_URL.
import (
	"testing"
)

func TestIntegration_ServiceTools(t *testing.T) {
	e := newEnv(t)
	f := e.writeFixture()
	inst := e.mustREST("POST", "/services", map[string]any{"name": "Instalação", "unit": "m²", "reference_price": 300, "reference_cost": 240}, 201)["id"].(string)
	e.mustREST("POST", "/services", map[string]any{"name": "Consultoria", "unit": "visita"}, 201)

	t.Run("search_services", func(t *testing.T) {
		out := e.ok("search_services", map[string]any{"query": "INSTALACAO"})
		list := items(out)
		if len(list) != 1 {
			t.Fatalf("search_services = %v", out)
		}
		s := obj(list[0])
		if s["id"] != inst || s["name"] != "Instalação" || s["unit"] != "m²" || s["reference_price"] != float64(300) || s["reference_cost"] != float64(240) {
			t.Errorf("serviço = %v", s)
		}
		if all := items(e.ok("search_services", map[string]any{})); len(all) != 2 || obj(all[0])["name"] != "Consultoria" {
			t.Errorf("sem filtro, ordem por nome = %v", all)
		}
		if p := items(e.ok("search_services", map[string]any{"limit": 1, "offset": 1})); len(p) != 1 || obj(p[0])["name"] != "Instalação" {
			t.Errorf("paginação = %v", p)
		}
		e.fail("search_services", map[string]any{"limit": 0}, "INVALID_INPUT", "")
	})

	t.Run("create_quote e create_quick_sale com serviço", func(t *testing.T) {
		q := obj(e.ok("create_quote", map[string]any{"customer_id": f.customer, "items": []map[string]any{
			{"product_id": f.product, "product_name": "Granito Escrita", "quantity": 12, "unit_price": 400, "unit_cost": 250},
			{"kind": "SERVICE", "service_id": inst, "product_name": "Instalação", "quantity": 12, "unit_price": 300, "unit_cost": 240},
		}})["quote"])
		qi := q["items"].([]any)
		if obj(qi[0])["kind"] != "PRODUCT" || obj(qi[1])["kind"] != "SERVICE" || obj(qi[1])["service_id"] != inst || obj(qi[1])["product_id"] != nil {
			t.Fatalf("items do quote = %v", qi)
		}
		got := obj(e.ok("get_quote", map[string]any{"quote_id": q["id"]})["quote"])
		if obj(got["items"].([]any)[1])["kind"] != "SERVICE" || got["total_quote"] != float64(8400) {
			t.Errorf("get_quote = %v", got)
		}
		e.fail("create_quote", map[string]any{"customer_id": f.customer, "items": []map[string]any{
			{"kind": "SERVICE", "product_id": f.product, "product_name": "x", "quantity": 1, "unit_price": 1, "unit_cost": 0}}}, "INVALID_INPUT", "SERVICE")
		e.fail("create_quote", map[string]any{"customer_id": f.customer, "items": []map[string]any{
			{"service_id": inst, "product_name": "x", "quantity": 1, "unit_price": 1, "unit_cost": 0}}}, "INVALID_INPUT", "SERVICE")
		e.fail("create_quote", map[string]any{"customer_id": f.customer, "items": []map[string]any{
			{"kind": "SERVICE", "service_id": "00000000-0000-0000-0000-000000000000", "product_name": "x", "quantity": 1, "unit_price": 1, "unit_cost": 0}}}, "NOT_FOUND", "")

		conv := obj(e.ok("convert_quote_to_sale", map[string]any{"quote_id": q["id"]})["sale"])
		sale := e.ok("get_sale", map[string]any{"sale_id": conv["id"]})["sale"]
		checkServiceSale(t, obj(sale), inst, 2880)
	})

	var sale, svcItem string
	t.Run("get_sale e update_sale_item", func(t *testing.T) {
		out := e.ok("create_quick_sale", map[string]any{"customer_id": f.customer, "items": []map[string]any{
			{"product_id": f.product, "product_name": "Granito Escrita", "quantity": 8, "unit_price": 400, "unit_cost": 250},
			{"kind": "SERVICE", "service_id": inst, "product_name": "Instalação", "quantity": 8, "unit_price": 350, "unit_cost": 200},
		}})
		s := obj(out["sale"])
		sale = s["id"].(string)
		checkServiceSale(t, s, inst, 1600)
		for _, it := range s["items"].([]any) {
			if obj(it)["kind"] == "SERVICE" {
				svcItem = obj(it)["id"].(string)
			}
		}

		upd := obj(e.ok("update_sale_item", map[string]any{"sale_id": sale, "item_id": svcItem, "unit_price": 360, "quantity": 10})["item"])
		if upd["kind"] != "SERVICE" || upd["unit_price"] != float64(360) || upd["quantity"] != float64(10) || upd["origin"] != nil || upd["service_id"] != inst {
			t.Errorf("update_sale_item = %v", upd)
		}
		e.fail("update_sale_item", map[string]any{"sale_id": sale, "item_id": svcItem, "origin": "FROM_STOCK"}, "INVALID_INPUT", "serviço")
		e.fail("update_sale_item", map[string]any{"sale_id": sale, "item_id": svcItem, "supplier_id": f.supplier}, "INVALID_INPUT", "serviço")
		e.fail("update_sale_item", map[string]any{"sale_id": sale, "item_id": svcItem, "unit_cost": 5}, "INVALID_INPUT", "serviço")
	})

	t.Run("get_top_services e get_top_products", func(t *testing.T) {
		out := e.ok("get_top_services", map[string]any{})
		list := items(out)
		if len(list) != 1 {
			t.Fatalf("get_top_services = %v", out)
		}
		s := obj(list[0])
		// 12 m² a 300 (quote convertido) + 10 m² a 360 (venda rápida, depois do ajuste); custo 2880 + 1600.
		if s["service_id"] != inst || s["service_name"] != "Instalação" || s["unit"] != "m²" || s["sales_count"] != float64(2) ||
			s["quantity"] != float64(22) || s["revenue"] != float64(3600+3600) || s["average_price"] != float64(7200)/22 {
			t.Errorf("Instalação = %v", s)
		}
		if items(e.ok("get_top_services", map[string]any{"from": "2000-01-01", "to": "2000-12-31"})) == nil {
			t.Error("período vazio deve ser lista vazia")
		}
		for _, it := range items(e.ok("get_top_products", map[string]any{})) {
			if obj(it)["product_name"] == "Instalação" {
				t.Errorf("serviço no ranking de produtos: %v", it)
			}
		}
	})
}

// checkServiceSale confere o Item de serviço e o custo LABOR criado na conversão.
func checkServiceSale(t *testing.T, sale map[string]any, serviceID string, laborCost float64) {
	t.Helper()
	var svc map[string]any
	for _, it := range sale["items"].([]any) {
		if obj(it)["kind"] == "SERVICE" {
			svc = obj(it)
		}
	}
	if svc == nil || svc["service_id"] != serviceID || svc["origin"] != nil || svc["product_id"] != nil || svc["unit_cost"] != float64(0) {
		t.Fatalf("Item de serviço = %v", sale["items"])
	}
	costs := obj(sale["composition"])["costs"].([]any)
	if len(costs) != 1 {
		t.Fatalf("custos = %v", costs)
	}
	c := obj(costs[0])
	if c["kind"] != "LABOR" || c["amount"] != laborCost || c["sale_item_id"] != svc["id"] || c["payee"] != nil {
		t.Errorf("custo LABOR = %v", c)
	}
}
