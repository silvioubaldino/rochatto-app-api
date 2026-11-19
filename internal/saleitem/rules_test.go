package saleitem

import "testing"

func TestResolve(t *testing.T) {
	svc, bad, empty := KindService, "X", ""
	for _, c := range []struct {
		in   *string
		want string
		ok   bool
	}{{nil, KindProduct, true}, {&empty, KindProduct, true}, {&svc, KindService, true}, {&bad, "X", false}} {
		got, ok := Resolve(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("Resolve(%v) = %q,%v; quero %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		f    Fields
		bad  bool
	}{
		{"service livre", Fields{Kind: KindService}, false},
		{"service com catálogo", Fields{Kind: KindService, HasService: true}, false},
		{"service com produto", Fields{Kind: KindService, HasProduct: true}, true},
		{"service com origem", Fields{Kind: KindService, HasOrigin: true}, true},
		{"service com fornecedor", Fields{Kind: KindService, HasSupplier: true}, true},
		{"product com service_id", Fields{Kind: KindProduct, HasService: true}, true},
		{"product normal", Fields{Kind: KindProduct, HasProduct: true, HasOrigin: true, HasSupplier: true}, false},
	}
	for _, c := range cases {
		if got := Validate(c.f); (got != "") != c.bad {
			t.Errorf("%s: Validate = %q", c.name, got)
		}
	}
}

func TestLaborCostAmount(t *testing.T) {
	if got := LaborCostAmount(12, 240); got != 2880 {
		t.Errorf("12 m² × R$ 240 = %v, quero 2880", got)
	}
	if got := LaborCostAmount(0.333, 10.01); got != 3.33 {
		t.Errorf("arredondamento = %v, quero 3.33", got)
	}
	if got := LaborCostAmount(5, 0); got != 0 {
		t.Errorf("custo 0 = %v", got)
	}
}
