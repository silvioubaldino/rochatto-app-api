package obligations

import (
	"testing"
	"time"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func status(err error) int {
	if e := AsError(err); e != nil {
		return e.Status
	}
	return 0
}

func TestCheckSign(t *testing.T) {
	cases := []struct {
		cat    string
		amount float64
		want   int
	}{
		{CatSaleReceipt, 100, 0},
		{CatOtherIncome, 2100, 0},
		{CatVehicle, -317.87, 0},
		{CatPurchasePayment, -1, 0},
		{CatLabor, -50, 0},
		{CatSaleReceipt, -100, 400},
		{CatVehicle, 10, 400},
		{CatVehicle, 0, 400},
		{"NOPE", -1, 400},
		{CatVehicle, -1.234, 400},
	}
	for _, c := range cases {
		if got := status(CheckSign(c.cat, c.amount)); got != c.want {
			t.Errorf("CheckSign(%s, %v) = %d, want %d", c.cat, c.amount, got, c.want)
		}
	}
}

func TestDefaultCategory(t *testing.T) {
	recv := AccountKind{Direction: Receivable, OriginType: OriginSale, ComponentTypes: []string{ComponentSaleItems}}
	purchase := AccountKind{Direction: Payable, OriginType: OriginPurchase, ComponentTypes: []string{ComponentPurchaseItems}}
	freight := AccountKind{Direction: Payable, OriginType: OriginSale, ComponentTypes: []string{ComponentCostFreight}}
	commission := AccountKind{Direction: Payable, OriginType: OriginSale, ComponentTypes: []string{ComponentCostCommission}}
	mixed := AccountKind{Direction: Payable, OriginType: OriginSale, ComponentTypes: []string{ComponentCostFreight, ComponentCostCommission}}
	labor := AccountKind{Direction: Payable, OriginType: OriginSale, ComponentTypes: []string{ComponentCostLabor, ComponentCostLabor}}
	other := AccountKind{Direction: Payable, OriginType: OriginSale, ComponentTypes: []string{ComponentCostOther}}
	cases := []struct {
		name string
		in   []AccountKind
		want string
	}{
		{"só mão de obra", []AccountKind{labor}, CatLabor},
		{"outro custo", []AccountKind{other}, CatOtherExpense},
		{"receber", []AccountKind{recv}, CatSaleReceipt},
		{"compra", []AccountKind{purchase}, CatPurchasePayment},
		{"duas compras", []AccountKind{purchase, purchase}, CatPurchasePayment},
		{"só frete", []AccountKind{freight}, CatFreight},
		{"só comissão", []AccountKind{commission}, CatCommission},
		{"frete e comissão na mesma conta", []AccountKind{mixed}, CatOtherExpense},
		{"compra + frete", []AccountKind{purchase, freight}, CatOtherExpense},
		{"vazio", nil, CatOtherExpense},
	}
	for _, c := range cases {
		if got := DefaultCategory(c.in); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestGenerateInstallments(t *testing.T) {
	plan, err := GenerateInstallments(3, d(2026, 1, 15), 1, 100)
	if err != nil || len(plan) != 3 {
		t.Fatalf("plano = %v %v", plan, err)
	}
	if plan[0].Amount != 33.33 || plan[1].Amount != 33.33 || plan[2].Amount != 33.34 {
		t.Errorf("valores = %v %v %v", plan[0].Amount, plan[1].Amount, plan[2].Amount)
	}
	if got := Sum2(plan); got != 100 {
		t.Errorf("Σ = %v, want 100", got)
	}
	if plan[1].DueDate != d(2026, 2, 15) || plan[2].DueDate != d(2026, 3, 15) {
		t.Errorf("datas = %v %v", plan[1].DueDate, plan[2].DueDate)
	}

	plan, _ = GenerateInstallments(3, d(2026, 1, 31), 1, 30)
	if plan[1].DueDate != d(2026, 2, 28) || plan[2].DueDate != d(2026, 3, 31) {
		t.Errorf("fim de mês = %v %v", plan[1].DueDate, plan[2].DueDate)
	}
	plan, _ = GenerateInstallments(2, d(2028, 1, 31), 1, 10)
	if plan[1].DueDate != d(2028, 2, 29) {
		t.Errorf("bissexto = %v", plan[1].DueDate)
	}
	plan, _ = GenerateInstallments(2, d(2026, 11, 30), 3, 10)
	if plan[1].DueDate != d(2027, 2, 28) {
		t.Errorf("intervalo 3 = %v", plan[1].DueDate)
	}

	for _, c := range []struct {
		count  int
		amount float64
	}{{1, 100}, {25, 100}, {0, 100}, {2, 0}, {2, -5}, {2, 0.01}} {
		if _, err := GenerateInstallments(c.count, d(2026, 1, 1), 1, c.amount); status(err) != 400 {
			t.Errorf("count=%d amount=%v: err = %v, want 400", c.count, c.amount, err)
		}
	}
	if _, err := GenerateInstallments(2, time.Time{}, 1, 10); status(err) != 400 {
		t.Errorf("sem first_due_date = %v", err)
	}
}

func Sum2(plan []InstallmentInput) float64 {
	var c int64
	for _, p := range plan {
		c += Cents(p.Amount)
	}
	return FromCents(c)
}

func TestValidatePlan(t *testing.T) {
	ok := []InstallmentInput{{DueDate: d(2026, 1, 1), Amount: 60}, {DueDate: d(2026, 2, 1), Amount: 40}}
	if err := ValidatePlan(ok, 100); err != nil {
		t.Errorf("Σ = amount: %v", err)
	}
	if err := ValidatePlan(ok[:1], 100); err != nil {
		t.Errorf("Σ < amount deve valer: %v", err)
	}
	if err := ValidatePlan(nil, 100); err != nil {
		t.Errorf("plano vazio limpa o plano: %v", err)
	}
	if err := ValidatePlan(ok, 99.99); status(err) != 400 {
		t.Errorf("Σ > amount = %v, want 400", err)
	}
	if err := ValidatePlan([]InstallmentInput{{DueDate: d(2026, 1, 1), Amount: 0}}, 100); status(err) != 400 {
		t.Errorf("parcela 0 = %v", err)
	}
	if err := ValidatePlan([]InstallmentInput{{Amount: 10}}, 100); status(err) != 400 {
		t.Errorf("sem due_date = %v", err)
	}
	if err := ValidatePlan([]InstallmentInput{{DueDate: d(2026, 1, 1), Amount: 1.005}}, 100); status(err) != 400 {
		t.Errorf("3 casas = %v", err)
	}
}

func TestCoverPlan(t *testing.T) {
	today := d(2026, 11, 18)
	// Fora de ordem de propósito: a cobertura segue o vencimento, não a ordem de entrada.
	plan := []InstallmentInput{
		{DueDate: d(2026, 12, 1), Amount: 100},
		{DueDate: d(2026, 11, 1), Amount: 100},
		{DueDate: d(2026, 10, 1), Amount: 100},
	}
	cov := CoverPlan(plan, 150, today)
	if cov[2].Status != InstallmentCovered || cov[2].Covered != 100 {
		t.Errorf("1ª = %+v", cov[2])
	}
	if cov[1].Status != InstallmentPartial || cov[1].Covered != 50 || !cov[1].Overdue {
		t.Errorf("2ª = %+v (parcial e vencida)", cov[1])
	}
	if cov[0].Status != InstallmentOpen || cov[0].Covered != 0 || cov[0].Overdue {
		t.Errorf("3ª = %+v (aberta, não vencida)", cov[0])
	}
	cov = CoverPlan(plan, 0, today)
	if !cov[2].Overdue || !cov[1].Overdue || cov[0].Overdue {
		t.Errorf("vencidas com quitado 0 = %+v", cov)
	}
	// Quitado maior que o plano cobre tudo; coberta nunca é vencida.
	cov = CoverPlan(plan, 1000, today)
	for _, c := range cov {
		if c.Status != InstallmentCovered || c.Overdue {
			t.Errorf("tudo coberto = %+v", c)
		}
	}
}

func TestAllocate(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	states := map[string]AccountState{
		"a": {ID: "a", Direction: Payable, Open: 100, Counterparty: "SUPPLIER:1"},
		"b": {ID: "b", Direction: Payable, Open: 50, Counterparty: "SUPPLIER:1"},
		"c": {ID: "c", Direction: Payable, Open: 30, Counterparty: "SUPPLIER:2"},
		"r": {ID: "r", Direction: Receivable, Open: 70, Counterparty: "CUSTOMER:9"},
		"x": {ID: "x", Direction: Receivable, Open: 70, SaleCanceled: true, Counterparty: "CUSTOMER:9"},
		"z": {ID: "z", Direction: Payable, Open: 0, Counterparty: "SUPPLIER:1"},
	}
	t.Run("default = aberto e Σ", func(t *testing.T) {
		allocs, dir, warns, err := Allocate([]AllocationInput{{ObligationID: "a"}, {ObligationID: "b", Amount: f(20)}}, states)
		if err != nil || dir != Payable || len(warns) != 0 || Sum(allocs) != 120 || allocs[0].Amount != 100 {
			t.Fatalf("allocs=%v dir=%s warns=%v err=%v", allocs, dir, warns, err)
		}
	})
	t.Run("credores diferentes → warning", func(t *testing.T) {
		_, _, warns, err := Allocate([]AllocationInput{{ObligationID: "a"}, {ObligationID: "c"}}, states)
		if err != nil || len(warns) != 1 || warns[0] != WarningMixedCounterpart {
			t.Errorf("warns=%v err=%v", warns, err)
		}
	})
	for name, c := range map[string]struct {
		in   []AllocationInput
		want int
	}{
		"vazio":               {nil, 400},
		"amount zero":         {[]AllocationInput{{ObligationID: "a", Amount: f(0)}}, 400},
		"amount negativo":     {[]AllocationInput{{ObligationID: "a", Amount: f(-1)}}, 400},
		"direções diferentes": {[]AllocationInput{{ObligationID: "a"}, {ObligationID: "r"}}, 400},
		"conta repetida":      {[]AllocationInput{{ObligationID: "a"}, {ObligationID: "a"}}, 400},
		"acima do aberto":     {[]AllocationInput{{ObligationID: "a", Amount: f(100.01)}}, 409},
		"venda cancelada":     {[]AllocationInput{{ObligationID: "x"}}, 409},
		"sem aberto":          {[]AllocationInput{{ObligationID: "z"}}, 409},
		"inexistente":         {[]AllocationInput{{ObligationID: "nope"}}, 404},
	} {
		if _, _, _, err := Allocate(c.in, states); status(err) != c.want {
			t.Errorf("%s: err = %v, want %d", name, err, c.want)
		}
	}
}

func TestMoney(t *testing.T) {
	if Cents(0.1+0.2) != 30 || Cents(317.87) != 31787 || Round2(2.675) != 2.68 && Round2(2.675) != 2.67 {
		t.Error("conversões de centavos")
	}
	if HasMoreThan2Decimals(10.5) || !HasMoreThan2Decimals(10.555) {
		t.Error("HasMoreThan2Decimals")
	}
	if SignFor(Receivable) != 1 || SignFor(Payable) != -1 {
		t.Error("SignFor")
	}
	if !IsExpenseCategory(CatLabor) || IsExpenseCategory(CatPurchasePayment) || IsExpenseCategory(CatFreight) {
		t.Error("categorias de despesa")
	}
}
