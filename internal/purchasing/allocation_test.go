package purchasing

import (
	"errors"
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAllocate_ByQuantity(t *testing.T) {
	lines := []Line{{"m2", 100, 27}, {"M2 ", 257, 100}}
	got, err := Allocate(ByQuantity, 8564.25, lines)
	if err != nil {
		t.Fatal(err)
	}
	if !near(got[0], 8564.25*100/357) || !near(got[1], 8564.25*257/357) {
		t.Errorf("got %v", got)
	}
	if !near(got[0]+got[1], 8564.25) {
		t.Errorf("soma do rateio = %v, quero o total", got[0]+got[1])
	}
	if lc := LandedUnitCost(lines[0], got[0]); !near(lc, 27+8564.25/357) {
		t.Errorf("landed = %v", lc)
	}
}

func TestAllocate_ByValue(t *testing.T) {
	lines := []Line{{"m2", 10, 100}, {"saco", 5, 20}} // base 1000 e 100
	got, err := Allocate(ByValue, 110, lines)
	if err != nil {
		t.Fatal(err)
	}
	if !near(got[0], 100) || !near(got[1], 10) {
		t.Errorf("got %v", got)
	}
}

func TestAllocate_ByValueZeroBaseFallsBackToQuantity(t *testing.T) {
	got, err := Allocate(ByValue, 90, []Line{{"m2", 1, 0}, {"m2", 2, 0}})
	if err != nil || !near(got[0], 30) || !near(got[1], 60) {
		t.Errorf("got %v err %v", got, err)
	}
}

func TestAllocate_BonificationGetsQuantityShare(t *testing.T) {
	got, _ := Allocate(ByQuantity, 100, []Line{{"m2", 3, 50}, {"m2", 1, 0}})
	if !near(got[0], 75) || !near(got[1], 25) {
		t.Errorf("bonificação deve receber rateio por quantidade: %v", got)
	}
	if lc := LandedUnitCost(Line{"m2", 1, 0}, got[1]); !near(lc, 25) {
		t.Errorf("landed da bonificação = %v", lc)
	}
}

func TestAllocate_MixedUnits(t *testing.T) {
	lines := []Line{{"m2", 1, 10}, {"saco", 1, 10}}
	if _, err := Allocate(ByQuantity, 10, lines); !errors.Is(err, ErrMixedUnits) {
		t.Errorf("BY_QUANTITY com unidades diferentes: err = %v", err)
	}
	if _, err := Allocate("", 10, lines); !errors.Is(err, ErrMixedUnits) {
		t.Errorf("método vazio = BY_QUANTITY: err = %v", err)
	}
	if _, err := Allocate(ByValue, 10, lines); err != nil {
		t.Errorf("BY_VALUE aceita unidades diferentes: %v", err)
	}
	if _, err := Allocate(ByQuantity, 0, lines); err != nil {
		t.Errorf("sem custos adicionais não há o que ratear: %v", err)
	}
}

func TestUnitsMixed_LineWithoutUnitAssumesOthers(t *testing.T) {
	if UnitsMixed([]Line{{"", 1, 1}, {"m2", 1, 1}, {" M2", 1, 1}}) {
		t.Error("linha sem unidade assume a das demais; M2 = m2")
	}
	if !UnitsMixed([]Line{{"m2", 1, 1}, {"", 1, 1}, {"m3", 1, 1}}) {
		t.Error("m2 e m3 são diferentes")
	}
}

func TestAllocate_InvalidMethodAndEmpty(t *testing.T) {
	if _, err := Allocate("POR_PESO", 1, nil); err == nil {
		t.Error("método inválido deve falhar")
	}
	if got, err := Allocate(ByQuantity, 50, nil); err != nil || len(got) != 0 {
		t.Errorf("sem linhas: %v %v", got, err)
	}
}

func TestChargeLabel(t *testing.T) {
	d := " ICMS MS "
	empty := "  "
	for _, c := range []struct {
		kind string
		desc *string
		want string
	}{
		{ChargeFreight, nil, "Frete de compra"},
		{ChargeUnloading, &empty, "Descarga"},
		{ChargeTax, &d, "Imposto · ICMS MS"},
		{ChargeOther, nil, "Outros"},
	} {
		if got := ChargeLabel(c.kind, c.desc); got != c.want {
			t.Errorf("ChargeLabel(%s) = %q, quero %q", c.kind, got, c.want)
		}
	}
}
