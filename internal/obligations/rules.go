// Package obligations reúne o domínio de contas e movimentações: regras puras (este arquivo) e o acesso a dados transacional (store*.go).
package obligations

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	Payable    = "PAYABLE"
	Receivable = "RECEIVABLE"
)

const (
	StatusOpen    = "OPEN"
	StatusPartial = "PARTIAL"
	StatusSettled = "SETTLED"
)

const (
	InstallmentOpen    = "OPEN"
	InstallmentPartial = "PARTIAL"
	InstallmentCovered = "COVERED"
)

const (
	CatSaleReceipt     = "SALE_RECEIPT"
	CatOtherIncome     = "OTHER_INCOME"
	CatPurchasePayment = "PURCHASE_PAYMENT"
	CatFreight         = "FREIGHT"
	CatLabor           = "LABOR"
	CatCommission      = "COMMISSION"
	CatVehicle         = "VEHICLE"
	CatTaxes           = "TAXES"
	CatFixedCosts      = "FIXED_COSTS"
	CatMarketing       = "MARKETING"
	CatBankFees        = "BANK_FEES"
	CatOtherExpense    = "OTHER_EXPENSE"
)

var (
	Categories = set(CatSaleReceipt, CatOtherIncome, CatPurchasePayment, CatFreight, CatLabor, CatCommission,
		CatVehicle, CatTaxes, CatFixedCosts, CatMarketing, CatBankFees, CatOtherExpense)
	incomeCategories  = set(CatSaleReceipt, CatOtherIncome)
	expenseCategories = set(CatVehicle, CatTaxes, CatFixedCosts, CatMarketing, CatBankFees, CatOtherExpense, CatLabor)
)

const (
	CounterpartySupplier = "SUPPLIER"
	CounterpartyPartner  = "PARTNER"
	CounterpartyCustomer = "CUSTOMER"
	OriginSale           = "SALE"
	OriginPurchase       = "PURCHASE"
)

const (
	ComponentSaleItems      = "SALE_ITEMS"
	ComponentSaleCharge     = "SALE_CHARGE"
	ComponentCostFreight    = "SALE_COST_FREIGHT"
	ComponentCostLabor      = "SALE_COST_LABOR"
	ComponentCostCommission = "SALE_COST_COMMISSION"
	ComponentCostOther      = "SALE_COST_OTHER"
	ComponentPurchaseItems  = "PURCHASE_ITEMS"
	WarningOversettled      = "OVERSETTLED"
	WarningMixedCounterpart = "MIXED_COUNTERPARTIES"
)

var SupplierKinds = set("STONE", "TRANSPORT", "LABOR", "TAX_AUTHORITY", "OTHER")

var (
	Directions       = set(Payable, Receivable)
	Statuses         = set(StatusOpen, StatusPartial, StatusSettled)
	CounterpartyType = set(CounterpartySupplier, CounterpartyPartner, CounterpartyCustomer)
	OriginTypes      = set(OriginSale, OriginPurchase)
)

func set(vals ...string) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}

type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func newErr(status int, format string, a ...any) *Error {
	return &Error{Status: status, Msg: fmt.Sprintf(format, a...)}
}

func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

func Cents(v float64) int64 { return int64(math.Round(v * 100)) }

func FromCents(c int64) float64 { return float64(c) / 100 }

func Round2(v float64) float64 { return FromCents(Cents(v)) }

func HasMoreThan2Decimals(v float64) bool {
	return math.Abs(v*100-math.Round(v*100)) > 1e-6
}

func IsIncome(category string) bool { return incomeCategories[category] }

func IsExpenseCategory(category string) bool { return expenseCategories[category] }

func ExpenseCategories() []string {
	out := make([]string, 0, len(expenseCategories))
	for c := range expenseCategories {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// CheckSign valida a categoria e o sinal: entradas (SALE_RECEIPT, OTHER_INCOME) > 0; as demais < 0; amount = 0 nunca.
func CheckSign(category string, amount float64) error {
	if !Categories[category] {
		return newErr(400, "category inválida")
	}
	if Cents(amount) == 0 {
		return newErr(400, "campo 'amount' não pode ser zero")
	}
	if HasMoreThan2Decimals(amount) {
		return newErr(400, "campo 'amount' aceita no máximo 2 casas decimais")
	}
	if IsIncome(category) && amount < 0 {
		return newErr(400, "categoria %s exige valor positivo (entrada)", category)
	}
	if !IsIncome(category) && amount > 0 {
		return newErr(400, "categoria %s exige valor negativo (saída)", category)
	}
	return nil
}

func componentCategory(types []string) string {
	cat := ""
	for _, t := range types {
		var c string
		switch t {
		case ComponentCostFreight:
			c = CatFreight
		case ComponentCostLabor:
			c = CatLabor
		case ComponentCostCommission:
			c = CatCommission
		default:
			return CatOtherExpense
		}
		if cat == "" {
			cat = c
		} else if cat != c {
			return CatOtherExpense
		}
	}
	if cat == "" {
		return CatOtherExpense
	}
	return cat
}

type AccountKind struct {
	Direction      string
	OriginType     string
	ComponentTypes []string
}

// Várias contas: se todas dão a mesma categoria, ela; senão OTHER_EXPENSE.
func DefaultCategory(accounts []AccountKind) string {
	cat := ""
	for _, a := range accounts {
		var c string
		switch {
		case a.Direction == Receivable:
			c = CatSaleReceipt
		case a.OriginType == OriginPurchase:
			c = CatPurchasePayment
		default:
			c = componentCategory(a.ComponentTypes)
		}
		if cat == "" {
			cat = c
		} else if cat != c {
			return CatOtherExpense
		}
	}
	if cat == "" {
		return CatOtherExpense
	}
	return cat
}

type InstallmentInput struct {
	DueDate       time.Time
	Amount        float64
	PaymentMethod *string
	Notes         *string
}

func ValidatePlan(plan []InstallmentInput, accountAmount float64) error {
	var sum int64
	for i, in := range plan {
		if in.DueDate.IsZero() {
			return newErr(400, "installments[%d].due_date é obrigatório", i)
		}
		if in.Amount <= 0 || Cents(in.Amount) <= 0 {
			return newErr(400, "installments[%d].amount deve ser maior que zero", i)
		}
		if HasMoreThan2Decimals(in.Amount) {
			return newErr(400, "installments[%d].amount aceita no máximo 2 casas decimais", i)
		}
		sum += Cents(in.Amount)
	}
	if sum > Cents(accountAmount) {
		return newErr(400, "soma das parcelas (%.2f) maior que o valor da conta (%.2f)", FromCents(sum), accountAmount)
	}
	return nil
}

func addMonthsClamped(d time.Time, n int) time.Time {
	y, m, day := d.Date()
	first := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC).AddDate(0, n, 0)
	last := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

func GenerateInstallments(count int, first time.Time, intervalMonths int, amount float64) ([]InstallmentInput, error) {
	if count < 2 || count > 24 {
		return nil, newErr(400, "count deve estar entre 2 e 24")
	}
	if first.IsZero() {
		return nil, newErr(400, "campo 'first_due_date' é obrigatório")
	}
	if intervalMonths < 1 || intervalMonths > 24 {
		return nil, newErr(400, "interval_months deve estar entre 1 e 24")
	}
	total := Cents(amount)
	if total <= 0 || HasMoreThan2Decimals(amount) {
		return nil, newErr(400, "valor a parcelar deve ser maior que zero, com até 2 casas decimais")
	}
	base := total / int64(count)
	if base <= 0 {
		return nil, newErr(400, "valor a parcelar é pequeno demais para %d parcelas", count)
	}
	out := make([]InstallmentInput, count)
	var acc int64
	for i := 0; i < count; i++ {
		c := base
		if i == count-1 {
			c = total - acc
		}
		acc += c
		out[i] = InstallmentInput{DueDate: addMonthsClamped(first, i*intervalMonths), Amount: FromCents(c)}
	}
	return out, nil
}

type Coverage struct {
	Covered float64
	Status  string
	Overdue bool
}

// CoverPlan cobre as parcelas, na ordem dos vencimentos, com o quitado da conta (espelha a view vw_installment_coverage — usada para testar a paridade).
func CoverPlan(plan []InstallmentInput, settled float64, today time.Time) []Coverage {
	idx := make([]int, len(plan))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return plan[idx[a]].DueDate.Before(plan[idx[b]].DueDate) })
	out := make([]Coverage, len(plan))
	remaining := Cents(settled)
	for _, i := range idx {
		amt := Cents(plan[i].Amount)
		cov := min(max(remaining, 0), amt)
		remaining -= cov
		st := InstallmentOpen
		switch {
		case cov >= amt:
			st = InstallmentCovered
		case cov > 0:
			st = InstallmentPartial
		}
		out[i] = Coverage{Covered: FromCents(cov), Status: st, Overdue: st != InstallmentCovered && plan[i].DueDate.Before(today)}
	}
	return out
}

// AllocationInput é uma alocação pedida; Amount nil = o aberto da conta.
type AllocationInput struct {
	ObligationID string
	Amount       *float64
}

type AccountState struct {
	ID           string
	Direction    string
	Open         float64
	SaleCanceled bool
	Counterparty string
}

type Allocated struct {
	ObligationID string
	Amount       float64
}

func Allocate(req []AllocationInput, states map[string]AccountState) (allocs []Allocated, direction string, warnings []string, err error) {
	warnings = []string{}
	if len(req) == 0 {
		return nil, "", nil, newErr(400, "allocations não pode ser vazio")
	}
	seen := map[string]bool{}
	counterparties := map[string]bool{}
	for _, a := range req {
		st, ok := states[a.ObligationID]
		if !ok {
			return nil, "", nil, newErr(404, "conta não encontrada: %s", a.ObligationID)
		}
		if seen[a.ObligationID] {
			return nil, "", nil, newErr(400, "conta repetida em allocations: %s", a.ObligationID)
		}
		seen[a.ObligationID] = true
		if direction == "" {
			direction = st.Direction
		} else if direction != st.Direction {
			return nil, "", nil, newErr(400, "contas a pagar e a receber não podem ser quitadas na mesma movimentação")
		}
		if a.Amount != nil && Cents(*a.Amount) <= 0 {
			return nil, "", nil, newErr(400, "allocations.amount deve ser maior que zero")
		}
		if a.Amount != nil && HasMoreThan2Decimals(*a.Amount) {
			return nil, "", nil, newErr(400, "allocations.amount aceita no máximo 2 casas decimais")
		}
		counterparties[st.Counterparty] = true
	}
	for _, a := range req {
		st := states[a.ObligationID]
		if st.Direction == Receivable && st.SaleCanceled {
			return nil, "", nil, newErr(409, "venda cancelada: recebimentos bloqueados")
		}
		amt := Cents(st.Open)
		if a.Amount != nil {
			amt = Cents(*a.Amount)
		}
		if amt <= 0 || amt > Cents(st.Open) {
			return nil, "", nil, newErr(409, "valor acima do devido")
		}
		allocs = append(allocs, Allocated{ObligationID: a.ObligationID, Amount: FromCents(amt)})
	}
	if len(counterparties) > 1 {
		warnings = append(warnings, WarningMixedCounterpart)
	}
	return allocs, direction, warnings, nil
}

func Sum(allocs []Allocated) float64 {
	var c int64
	for _, a := range allocs {
		c += Cents(a.Amount)
	}
	return FromCents(c)
}

func SignFor(direction string) float64 {
	if direction == Receivable {
		return 1
	}
	return -1
}
