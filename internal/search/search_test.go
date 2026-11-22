package search

import "testing"

func TestNormalize(t *testing.T) {
	if got := Normalize("  Mármore Ção  "); got != "marmore cao" {
		t.Errorf("Normalize = %q", got)
	}
}

func TestAsNumber(t *testing.T) {
	cases := map[string]struct {
		n  int
		ok bool
	}{"98": {98, true}, " #12 ": {12, true}, "9a": {0, false}, "": {0, false}, "-3": {0, false}, "1234567890": {0, false}}
	for in, want := range cases {
		n, ok := AsNumber(in)
		if ok != want.ok || (ok && n != want.n) {
			t.Errorf("AsNumber(%q) = %d,%v", in, n, ok)
		}
	}
}

func TestParseQuery(t *testing.T) {
	if _, err := ParseQuery(" a "); err == nil {
		t.Error("1 caractere deveria falhar")
	}
	q, err := ParseQuery("98")
	if err != nil || q.Number == nil || *q.Number != 98 || q.Digits != "98" {
		t.Errorf("ParseQuery(98) = %+v %v", q, err)
	}
	q, _ = ParseQuery("Mármore")
	if q.Text != "marmore" || q.Number != nil || q.Digits != "" {
		t.Errorf("ParseQuery(Mármore) = %+v", q)
	}
	q, _ = ParseQuery("(11) 9999-0")
	if q.Digits != "1199990" {
		t.Errorf("telefone = %+v", q)
	}
}

func TestParseTypes(t *testing.T) {
	all, err := ParseTypes("")
	if err != nil || len(all) != len(AllTypes) {
		t.Fatalf("default = %v %v", all, err)
	}
	got, err := ParseTypes("sale, customer,SALE")
	if err != nil || len(got) != 2 || got[0] != Sale || got[1] != Customer {
		t.Errorf("types = %v %v", got, err)
	}
	if _, err := ParseTypes("SALE,FOO"); err == nil {
		t.Error("tipo inválido deveria falhar")
	}
}

func TestParseLimit(t *testing.T) {
	for in, want := range map[string]int{"": 5, "1": 1, "10": 10} {
		if n, err := ParseLimit(in); err != nil || n != want {
			t.Errorf("ParseLimit(%q) = %d %v", in, n, err)
		}
	}
	for _, in := range []string{"0", "11", "x", "-1"} {
		if _, err := ParseLimit(in); err == nil {
			t.Errorf("ParseLimit(%q) deveria falhar", in)
		}
	}
}

func TestEscapeLike(t *testing.T) {
	if got := EscapeLike(`50%_a\`); got != `50\%\_a\\` {
		t.Errorf("EscapeLike = %q", got)
	}
}
