package finance

import "math"

func Round2(v float64) float64 { return math.Round(v*100) / 100 }

func Round4(v float64) float64 { return math.Round(v*10000) / 10000 }

func AverageTicket(revenue float64, count int64) float64 {
	if count <= 0 {
		return 0
	}
	return Round2(revenue / float64(count))
}

// ConversionRate = ganhos / criados (coorte de Quotes), 0..1 com 4 casas; nil quando não há Quotes no período (RF-18) — "sem dados" é diferente de 0%.
func ConversionRate(won, created int64) *float64 {
	if created <= 0 {
		return nil
	}
	r := Round4(float64(won) / float64(created))
	return &r
}
