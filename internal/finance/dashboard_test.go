package finance

import "testing"

func TestAverageTicket(t *testing.T) {
	cases := []struct {
		revenue float64
		count   int64
		want    float64
	}{
		{0, 0, 0},
		{1000, 0, 0},
		{1000, 4, 250},
		{100, 3, 33.33},
		{200, 3, 66.67},
	}
	for _, c := range cases {
		if got := AverageTicket(c.revenue, c.count); got != c.want {
			t.Errorf("AverageTicket(%v,%d) = %v, want %v", c.revenue, c.count, got, c.want)
		}
	}
}

func TestConversionRate(t *testing.T) {
	if got := ConversionRate(0, 0); got != nil {
		t.Errorf("created=0 deveria ser nil, got %v", *got)
	}
	cases := []struct {
		won, created int64
		want         float64
	}{
		{0, 5, 0}, {5, 5, 1}, {1, 3, 0.3333}, {2, 3, 0.6667}, {1, 4, 0.25},
	}
	for _, c := range cases {
		got := ConversionRate(c.won, c.created)
		if got == nil || *got != c.want {
			t.Errorf("ConversionRate(%d,%d) = %v, want %v", c.won, c.created, got, c.want)
		}
	}
}
