package tools

import (
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
