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
	s := legacySale{TotalVenda: 1000, ChargesTotal: 100, CostsTotal: 150, CostsPaid: 50, CostsOpen: 100, Receivable: 400, Lucro: 250}.toEN()
	if s.Total != 1000 || s.ChargesTotal != 100 || s.CostsTotal != 150 || s.CostsPaid != 50 || s.CostsOpen != 100 || s.Receivable != 400 || s.Profit != 250 {
		t.Errorf("sale = %+v (calculados vêm da API, sem recálculo)", s)
	}
	if sup := (legacySupplier{ID: "s", Nome: "Z"}).toEN(); sup.Kind != "STONE" {
		t.Errorf("fornecedor sem kind = %+v, quero STONE", sup)
	}
}

func TestReceiptsOfTranslatesInstallmentsAndSettlements(t *testing.T) {
	pm := toolkit.PaymentMethod("PIX")
	d := &ObligationDetail{
		ObligationSummary: ObligationSummary{Open: 400},
		Installments: []Installment{
			{ID: "i1", DueDate: "2026-12-01", Amount: 300, Covered: 300, Status: "COVERED"},
			{ID: "i2", DueDate: "2026-12-15", Amount: 300, Covered: 200, Status: "PARTIAL", PaymentMethod: &pm},
		},
		Settlements: []ObligationSettlement{{ID: "s1", Amount: 500, Movement: MovementSummary{ID: "m1", OccurredOn: "2026-11-01", PaymentMethod: &pm}}},
	}
	got := receiptsOf(d)
	if len(got) != 2 {
		t.Fatalf("receipts = %+v", got)
	}
	if p := got[0]; p.ID != "i2" || p.Status != "PENDING" || p.Amount != 100 || p.ScheduledDate == nil || *p.ScheduledDate != "2026-12-15" {
		t.Errorf("pendente = %+v (parcela parcial: pelo que falta)", p)
	}
	if r := got[1]; r.ID != "s1" || r.Status != "RECEIVED" || r.Amount != 500 || r.PaidAt == nil || *r.PaidAt != "2026-11-01" || r.PaymentMethod == nil {
		t.Errorf("recebido = %+v", r)
	}
	d.Open = 0
	if got = receiptsOf(d); len(got) != 1 || got[0].Status != "RECEIVED" {
		t.Errorf("sem aberto = %+v", got)
	}
	if got = receiptsOf(nil); got == nil || len(got) != 0 {
		t.Errorf("venda sem conta = %+v, quero []", got)
	}
}

func TestRound2(t *testing.T) {
	for in, want := range map[float64]float64{100: 100, 33.335: 33.34, 0.1 + 0.2: 0.3, -5.555: -5.56} {
		if got := round2(in); got != want && (got-want > 0.0001 || want-got > 0.0001) {
			t.Errorf("round2(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestRestPurchaseSummaryMapsPaidAndPayable(t *testing.T) {
	p := restPurchaseSummary{ID: "p", TotalCost: 200, Paid: 150, Payable: 50, PaymentStatus: "PENDING"}.toEN()
	if p.ToPay != 50 || p.PaidAmount == nil || *p.PaidAmount != 150 || p.PaidAt != nil {
		t.Errorf("purchase = %+v", p)
	}
	if q := (restPurchaseSummary{TotalCost: 200, Payable: 200}).toEN(); q.PaidAmount != nil || q.ToPay != 200 {
		t.Errorf("sem pagamento = %+v (paid_amount omitido)", q)
	}
}

func TestToLegacySalePutAppliesOnlySentFields(t *testing.T) {
	note, date := "x", Date("2026-12-20")
	cur := legacySale{Observacoes: &note, DataEntregaPrevista: &date}
	body := toLegacySalePut(cur, UpdateSaleIn{Notes: toolkit.Nullable[string]{Present: true, Value: "y"}})
	if body["observacoes"] != "y" {
		t.Errorf("PUT = %v", body)
	}
	if *(body["data_entrega_prevista"].(*Date)) != "2026-12-20" {
		t.Errorf("PUT = %v", body)
	}
	for _, gone := range []string{"frete_valor", "comissao_valor", "frete_pago", "comissao_paga"} {
		if _, has := body[gone]; has {
			t.Errorf("%s não existe mais no PUT: %v", gone, body)
		}
	}
	body = toLegacySalePut(cur, UpdateSaleIn{
		ExpectedDeliveryDate: toolkit.Nullable[Date]{Present: true, Null: true},
		Notes:                toolkit.Nullable[string]{Present: true, Null: true},
	})
	if body["data_entrega_prevista"] != nil || body["observacoes"] != nil {
		t.Errorf("PUT com null = %v", body)
	}
}

func TestToLegacyItemPut(t *testing.T) {
	sup := UUID("s1")
	mto := toolkit.ItemOrigin("MADE_TO_ORDER")
	cur := legacySaleItem{ID: "i", ProdutoNome: "G", Origin: &mto, Quantidade: 2, PrecoVendaUnit: 10, CustoUnit: 6, FornecedorID: &sup}
	q := toolkit.Qty(3)
	b := toLegacyItemPut(cur, UpdateSaleItemIn{Quantity: &q})
	if b["quantidade"] != 3.0 || b["preco_venda_unit"] != 10.0 || b["custo_unit"] != 6.0 || b["origin"] != toolkit.ItemOrigin("MADE_TO_ORDER") || b["fornecedor_id"] != &sup {
		t.Errorf("PUT = %v", b)
	}
	b = toLegacyItemPut(cur, UpdateSaleItemIn{Origin: "FROM_STOCK"})
	if _, has := b["custo_unit"]; has || b["origin"] != toolkit.ItemOrigin("FROM_STOCK") {
		t.Errorf("origem nova sem custo = %v", b)
	}
	c := toolkit.Money(7)
	if b = toLegacyItemPut(cur, UpdateSaleItemIn{Origin: "FROM_STOCK", UnitCost: &c}); b["custo_unit"] != 7.0 {
		t.Errorf("custo informado = %v", b)
	}
	b = toLegacyItemPut(cur, UpdateSaleItemIn{Origin: "MADE_TO_ORDER", SupplierID: toolkit.Nullable[UUID]{Present: true, Null: true}})
	if b["custo_unit"] != 6.0 || b["fornecedor_id"] != nil || b["fornecedor_nome"] != nil {
		t.Errorf("supplier null = %v", b)
	}
}

func TestToLegacyItemPutService(t *testing.T) {
	svc := UUID("sv1")
	cur := legacySaleItem{ID: "i", ProdutoNome: "Instalação", Kind: "SERVICE", ServiceID: &svc, Quantidade: 12, PrecoVendaUnit: 300}
	p := toolkit.Money(320)
	b := toLegacyItemPut(cur, UpdateSaleItemIn{UnitPrice: &p})
	if b["kind"] != toolkit.ItemKind("SERVICE") || b["service_id"] != &svc || b["origin"] != nil || b["preco_venda_unit"] != 320.0 {
		t.Errorf("PUT de serviço = %v", b)
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
