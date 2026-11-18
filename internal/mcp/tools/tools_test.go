package tools

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/silvioubaldino/sales-backend/internal/mcp/toolkit"
)

func TestNameMatchesIgnoresAccentsAndCase(t *testing.T) {
	cases := []struct {
		q, name string
		want    bool
	}{
		{"joao", "João Silva", true}, {"JOÃO", "joao pereira", true}, {"são josé", "Pedras Sao Jose", true},
		{"  silva ", "João Silva", true}, {"", "qualquer", true}, {"maria", "João", false}, {"ç", "Conceição", true},
	}
	for _, c := range cases {
		if got := nameMatches(c.q, c.name); got != c.want {
			t.Errorf("nameMatches(%q,%q) = %v", c.q, c.name, got)
		}
	}
}

func TestPaginate(t *testing.T) {
	all := []int{1, 2, 3, 4, 5}
	p := toolkit.Paginate(all, toolkit.Page{Limit: 2, Offset: 1})
	if len(p.Items) != 2 || p.Items[0] != 2 || p.Total != 5 || p.Offset != 1 || !p.HasMore {
		t.Errorf("página = %+v", p)
	}
	p = toolkit.Paginate(all, toolkit.Page{Limit: 10, Offset: 4})
	if len(p.Items) != 1 || p.HasMore {
		t.Errorf("última página = %+v", p)
	}
	// offset além do total → vazia e não-nula (JSON []), padrão de 20 itens, teto de 100.
	if p = toolkit.Paginate(all, toolkit.Page{Offset: 99}); p.Items == nil || len(p.Items) != 0 || p.Offset != 5 || p.HasMore {
		t.Errorf("offset além = %+v", p)
	}
	if l, _ := (toolkit.Page{}).Normalize(); l != 20 {
		t.Errorf("limit padrão = %d", l)
	}
	if l, _ := (toolkit.Page{Limit: 5000}).Normalize(); l != 100 {
		t.Errorf("limit máximo = %d", l)
	}
}

func TestLegacyAdapters(t *testing.T) {
	empty, name := "", "Ana"
	c := legacyCustomer{ID: "1", Nome: "João", Telefone: &empty, Observacoes: &name}.toEN()
	if c.Name != "João" || c.Phone != nil || c.Notes == nil || *c.Notes != "Ana" {
		t.Errorf("customer = %+v (string vazia vira ausente)", c)
	}
	r := legacyReceipt{ID: "r", Valor: 10, Status: "recebido"}.toEN()
	p := legacyReceipt{ID: "p", Valor: 10, Status: "pendente"}.toEN()
	if r.Status != "RECEIVED" || p.Status != "PENDING" {
		t.Errorf("status do Receipt: %v %v", r.Status, p.Status)
	}
	s := legacySale{FreteValor: 300, FretePago: 100, TotalVenda: 1000, Receivable: 400, Lucro: 250}.toEN()
	if s.FreightAmount != 300 || s.FreightPaid != 100 || s.Total != 1000 || s.Receivable != 400 || s.Profit != 250 {
		t.Errorf("sale = %+v (calculados vêm da API, sem recálculo)", s)
	}
}

func TestToLegacySalePutAppliesOnlySentFields(t *testing.T) {
	note, date := "x", Date("2026-12-20")
	cur := legacySale{FreteValor: 300, FretePago: 0, ComissaoValor: 40, ComissaoPaga: true, Observacoes: &note, DataEntregaPrevista: &date}
	paid := toolkit.Money(300)
	body := toLegacySalePut(cur, UpdateSaleIn{FreightPaid: &paid})
	if body["frete_pago"] != 300.0 || body["frete_valor"] != 300.0 || body["comissao_valor"] != 40.0 || body["comissao_paga"] != true ||
		*(body["observacoes"].(*string)) != "x" || *(body["data_entrega_prevista"].(*Date)) != "2026-12-20" {
		t.Errorf("PUT = %v", body)
	}
	// null explícito limpa; false explícito é diferente de omitido.
	no := false
	body = toLegacySalePut(cur, UpdateSaleIn{
		ExpectedDeliveryDate: toolkit.Nullable[Date]{Present: true, Null: true},
		Notes:                toolkit.Nullable[string]{Present: true, Null: true}, CommissionPaid: &no,
	})
	if body["data_entrega_prevista"] != nil || body["observacoes"] != nil || body["comissao_paga"] != false || body["frete_valor"] != 300.0 {
		t.Errorf("PUT com null = %v", body)
	}
}

func TestToLegacyItemPut(t *testing.T) {
	sup := UUID("s1")
	cur := legacySaleItem{ID: "i", ProdutoNome: "G", Origin: "MADE_TO_ORDER", Quantidade: 2, PrecoVendaUnit: 10, CustoUnit: 6, FornecedorID: &sup}
	// Só a quantidade: o resto permanece, inclusive o custo.
	q := toolkit.Qty(3)
	b := toLegacyItemPut(cur, UpdateSaleItemIn{Quantity: &q})
	if b["quantidade"] != 3.0 || b["preco_venda_unit"] != 10.0 || b["custo_unit"] != 6.0 || b["origin"] != toolkit.ItemOrigin("MADE_TO_ORDER") || b["fornecedor_id"] != &sup {
		t.Errorf("PUT = %v", b)
	}
	// Mudar a origem sem informar custo omite custo_unit (a API aplica RN-16); com custo, vale o informado.
	b = toLegacyItemPut(cur, UpdateSaleItemIn{Origin: "FROM_STOCK"})
	if _, has := b["custo_unit"]; has || b["origin"] != toolkit.ItemOrigin("FROM_STOCK") {
		t.Errorf("origem nova sem custo = %v", b)
	}
	c := toolkit.Money(7)
	if b = toLegacyItemPut(cur, UpdateSaleItemIn{Origin: "FROM_STOCK", UnitCost: &c}); b["custo_unit"] != 7.0 {
		t.Errorf("custo informado = %v", b)
	}
	// Mesma origem não mexe no custo; supplier null limpa id e nome.
	b = toLegacyItemPut(cur, UpdateSaleItemIn{Origin: "MADE_TO_ORDER", SupplierID: toolkit.Nullable[UUID]{Present: true, Null: true}})
	if b["custo_unit"] != 6.0 || b["fornecedor_id"] != nil || b["fornecedor_nome"] != nil {
		t.Errorf("supplier null = %v", b)
	}
}

func TestDecimalPlacesChecks(t *testing.T) {
	for _, ok := range []float64{0, 10, 10.5, 10.55, 1234567.89, 0.1 + 0.2} {
		if checkMoney("v", ok) != nil {
			t.Errorf("%v deveria ser dinheiro válido", ok)
		}
	}
	for _, bad := range []float64{10.001, 0.005, 1.234} {
		if checkMoney("v", bad) == nil {
			t.Errorf("%v tem mais de 2 casas", bad)
		}
	}
	if checkQty("q", 2.125) != nil || checkQty("q", 2.1255) == nil {
		t.Error("quantidade aceita até 3 casas")
	}
}

func TestNullableDecodesPresenceAndNull(t *testing.T) {
	var in UpdateSaleIn
	if err := json.Unmarshal([]byte(`{"sale_id":"a","notes":null,"expected_delivery_date":"2026-01-02"}`), &in); err != nil {
		t.Fatal(err)
	}
	if !in.Notes.Present || !in.Notes.Null || !in.ExpectedDeliveryDate.Present || in.ExpectedDeliveryDate.Null || in.ExpectedDeliveryDate.Value != "2026-01-02" {
		t.Errorf("notes=%+v data=%+v", in.Notes, in.ExpectedDeliveryDate)
	}
	var none UpdateSaleIn
	_ = json.Unmarshal([]byte(`{"sale_id":"a"}`), &none)
	if none.Notes.Present || none.ExpectedDeliveryDate.Present {
		t.Error("campos omitidos não podem aparecer como presentes")
	}
}

func TestAfterWriteHidesAPIError(t *testing.T) {
	err := afterWrite(&toolkit.APIError{Status: 404, Message: "venda não encontrada"})
	var ae *toolkit.APIError
	var te *toolkit.ToolError
	if errors.As(err, &ae) || errors.As(err, &te) {
		t.Errorf("falha pós-escrita deve virar erro opaco (→ INTERNAL com call_id): %v", err)
	}
}
