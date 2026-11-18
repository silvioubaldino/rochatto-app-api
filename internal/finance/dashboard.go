package finance

import "math"

// Round2 arredonda para 2 casas (valores monetários).
func Round2(v float64) float64 { return math.Round(v*100) / 100 }

// Round4 arredonda para 4 casas (taxas 0..1).
func Round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// AverageTicket = receita / nº de Sales, com 2 casas; 0 quando não há Sales (RF-18).
func AverageTicket(revenue float64, count int64) float64 {
	if count <= 0 {
		return 0
	}
	return Round2(revenue / float64(count))
}

// ConversionRate = ganhos / criados (coorte de Quotes), 0..1 com 4 casas; nil quando não
// há Quotes no período (RF-18) — "sem dados" é diferente de 0%.
func ConversionRate(won, created int64) *float64 {
	if created <= 0 {
		return nil
	}
	r := Round4(float64(won) / float64(created))
	return &r
}
