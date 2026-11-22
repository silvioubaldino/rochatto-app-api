package pendingactions

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func dp(s string) *string   { return &s }
func fp(f float64) *float64 { return &f }

func TestParseHorizon(t *testing.T) {
	for in, want := range map[string]int{"": 7, "0": 0, "60": 60, "14": 14} {
		if n, err := ParseHorizon(in); err != nil || n != want {
			t.Errorf("ParseHorizon(%q) = %d %v", in, n, err)
		}
	}
	for _, in := range []string{"-1", "61", "x", "1.5"} {
		if _, err := ParseHorizon(in); err == nil {
			t.Errorf("ParseHorizon(%q) deveria falhar", in)
		}
	}
}

func TestWindowOverdueAndStale(t *testing.T) {
	today := day("2026-11-22")
	d := func(s string) *time.Time { v := day(s); return &v }
	if !InWindow(d("2026-11-29"), today, 7, false) || InWindow(d("2026-11-30"), today, 7, false) {
		t.Error("janela de 7 dias: 29/11 dentro, 30/11 fora")
	}
	if !InWindow(d("2026-11-22"), today, 0, false) || !InWindow(d("2026-01-01"), today, 0, false) {
		t.Error("horizonte 0 inclui hoje e o atrasado")
	}
	if InWindow(nil, today, 7, false) || !InWindow(nil, today, 7, true) {
		t.Error("sem data depende de includeNoDate")
	}
	if !IsOverdue(d("2026-11-21"), today) || IsOverdue(d("2026-11-22"), today) || IsOverdue(nil, today) {
		t.Error("vencido = antes de hoje")
	}
	if !IsStale(day("2026-11-14"), today) || IsStale(day("2026-11-15"), today) {
		t.Error("parado = quote_date < hoje − 7 dias (15/11 ainda não)")
	}
}

func item(title, date string, overdue bool, amount *float64) Item {
	it := Item{Entity: Entity{Type: EntityObligation, ID: uuid.New()}, Title: title, Overdue: overdue, Amount: amount}
	if date != "" {
		it.Date = dp(date)
	}
	return it
}

func TestBuildGroup_OrderCountsAndCut(t *testing.T) {
	if BuildGroup(ReceivableDue, nil) != nil {
		t.Error("sem itens → nil")
	}
	items := []Item{
		item("sem data", "", false, fp(5)),
		item("amanhã", "2026-11-23", false, fp(100)),
		item("atrasado tarde", "2026-11-20", true, fp(10)),
		item("atrasado cedo", "2026-11-01", true, fp(20.10)),
		item("hoje maior", "2026-11-22", false, fp(300)),
		item("hoje menor", "2026-11-22", false, nil),
	}
	g := BuildGroup(ReceivableDue, items)
	var got []string
	for _, it := range g.Items {
		got = append(got, it.Title)
	}
	want := []string{"atrasado cedo", "atrasado tarde", "hoje maior", "hoje menor", "amanhã", "sem data"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordem = %v, want %v", got, want)
		}
	}
	if g.Count != 6 || g.OverdueCount != 2 || g.Amount == nil || *g.Amount != 435.10 {
		t.Errorf("count=%d overdue=%d amount=%v", g.Count, g.OverdueCount, g.Amount)
	}
	many := make([]Item, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, item("i", "2026-12-01", i < 3, fp(1)))
	}
	g = BuildGroup(PayableDue, many)
	if len(g.Items) != MaxItems || g.Count != 30 || g.OverdueCount != 3 || *g.Amount != 30 {
		t.Errorf("corte: items=%d count=%d overdue=%d amount=%v", len(g.Items), g.Count, g.OverdueCount, g.Amount)
	}
	for i := 0; i < 3; i++ {
		if !g.Items[i].Overdue {
			t.Errorf("os vencidos vêm primeiro (posição %d)", i)
		}
	}
	if g = BuildGroup(NegativeStock, []Item{item("x", "", false, nil)}); g.Amount != nil {
		t.Errorf("amount deveria ser nil: %v", *g.Amount)
	}
}

func TestAssemble(t *testing.T) {
	a := BuildGroup(PayableDue, []Item{item("a", "2026-11-01", true, fp(1)), item("b", "2026-12-01", false, fp(1))})
	b := BuildGroup(ReceivableDue, []Item{item("c", "2026-11-02", true, fp(1))})
	c := BuildGroup(NegativeStock, []Item{item("d", "", false, nil)})
	resp := Assemble(day("2026-11-22"), 7, map[Kind]*Group{PayableDue: a, ReceivableDue: b, NegativeStock: c, QuoteStale: nil})
	if resp.Today != "2026-11-22" || resp.HorizonDays != 7 || resp.TotalOverdue != 2 {
		t.Errorf("resp = %+v", resp)
	}
	if len(resp.Groups) != 3 || resp.Groups[0].Kind != ReceivableDue || resp.Groups[1].Kind != PayableDue || resp.Groups[2].Kind != NegativeStock {
		t.Errorf("ordem dos grupos = %+v", resp.Groups)
	}
	if empty := Assemble(day("2026-11-22"), 0, nil); empty.Groups == nil || len(empty.Groups) != 0 {
		t.Errorf("vazio deveria ser lista vazia: %+v", empty)
	}
}
