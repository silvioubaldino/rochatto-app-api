package salecomp

import (
	"math"
	"testing"
)

func TestPayeeType(t *testing.T) {
	cases := map[string]string{
		CostCommission: PayeePartner, CostFreight: PayeeSupplier, CostLabor: PayeeSupplier, CostOther: PayeeSupplier,
	}
	for kind, want := range cases {
		if got := PayeeType(kind); got != want {
			t.Errorf("PayeeType(%s) = %s, want %s", kind, got, want)
		}
	}
}

func TestLabels(t *testing.T) {
	d := " rota 3 "
	if got := ChargeLabel(ChargeFreight, &d); got != "Frete cobrado · rota 3" {
		t.Errorf("ChargeLabel = %q", got)
	}
	if got := ChargeLabel(ChargeOther, nil); got != "Outras cobranças" {
		t.Errorf("ChargeLabel other = %q", got)
	}
	if got := CostLabel(CostLabor, nil); got != "Mão de obra" {
		t.Errorf("CostLabel = %q", got)
	}
	blank := "  "
	if got := CostLabel(CostOther, &blank); got != "Outro custo" {
		t.Errorf("CostLabel blank = %q", got)
	}
}

func TestProfitAYDExample(t *testing.T) {
	if got := Profit(4680, 100, 2970, 150); math.Abs(got-1660) > 1e-9 {
		t.Errorf("Profit = %v, want 1660", got)
	}
}
