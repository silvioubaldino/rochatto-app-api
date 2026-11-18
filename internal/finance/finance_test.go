package finance

import (
	"testing"
	"time"
)

func sp(s string) *string { return &s }

func TestNormalizeMethod(t *testing.T) {
	// Arrange / Act / Assert
	if m, err := NormalizeMethod(nil); m != nil || err != nil {
		t.Errorf("nil = %v %v", m, err)
	}
	if m, err := NormalizeMethod(sp("  ")); m != nil || err != nil {
		t.Errorf("vazio = %v %v", m, err)
	}
	if m, err := NormalizeMethod(sp("PIX")); err != nil || *m != "PIX" {
		t.Errorf("PIX = %v %v", m, err)
	}
	for _, bad := range []string{"pix", "CHEQUE", "Pix"} {
		if _, err := NormalizeMethod(sp(bad)); err != ErrInvalidMethod {
			t.Errorf("%q deveria ser inválido, err = %v", bad, err)
		}
	}
	for m := range PaymentMethods {
		if _, err := NormalizeMethod(sp(m)); err != nil {
			t.Errorf("%s deveria ser válido", m)
		}
	}
}

func TestCheckMethodForStatus(t *testing.T) {
	cases := []struct {
		status string
		method *string
		want   error
	}{
		{ReceiptReceived, nil, ErrMethodRequired},
		{ReceiptReceived, sp("PIX"), nil},
		{ReceiptPending, nil, nil},
		{ReceiptPending, sp("BOLETO"), nil},
	}
	for _, c := range cases {
		if got := CheckMethodForStatus(c.status, c.method); got != c.want {
			t.Errorf("%s/%v = %v, want %v", c.status, c.method, got, c.want)
		}
	}
}

func TestResolvePeriod(t *testing.T) {
	now := time.Date(2026, 11, 18, 22, 30, 0, 0, time.UTC)

	f, to, err := ResolvePeriod("", "", now)
	if err != nil || f.Format("2006-01-02") != "2026-11-01" || to.Format("2006-01-02") != "2026-11-18" {
		t.Errorf("default = %v %v %v", f, to, err)
	}
	f, to, err = ResolvePeriod("2026-01-01", "2026-01-31", now)
	if err != nil || f.Format("2006-01-02") != "2026-01-01" || to.Format("2006-01-02") != "2026-01-31" {
		t.Errorf("explícito = %v %v %v", f, to, err)
	}
	if _, _, err = ResolvePeriod("2026-05-01", "2026-05-01", now); err != nil {
		t.Errorf("from == to deve ser válido: %v", err)
	}
	for _, c := range [][2]string{{"x", ""}, {"", "2026-13-01"}, {"2026-02-01", "2026-01-01"}, {"2026-12-01", ""}} {
		if _, _, err := ResolvePeriod(c[0], c[1], now); err == nil {
			t.Errorf("%v deveria falhar", c)
		}
	}
}
