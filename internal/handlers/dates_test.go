package handlers

import (
	"testing"
	"time"
)

func TestTodayUsesBusinessTimezone(t *testing.T) {
	if name := brLocation.String(); name != "America/Sao_Paulo" {
		t.Skipf("tzdata indisponível (fallback %s)", name)
	}
	// 2026-11-18 01:00 UTC = 2026-11-17 22:00 em São Paulo: a data do negócio ainda é dia 17.
	utc := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	if got := utc.In(brLocation).Format("2006-01-02"); got != "2026-11-17" {
		t.Errorf("data em São Paulo = %s, want 2026-11-17", got)
	}
	if today().Location() != brLocation {
		t.Errorf("today() não está no fuso do negócio")
	}
}
