package handlers

import "time"

// brLocation é o fuso do negócio. Datas "de hoje" (Quote, Sale) usam o calendário de
// São Paulo; em UTC, depois das 21h a data gravada seria a do dia seguinte.
var brLocation = func() *time.Location {
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return loc
	}
	return time.FixedZone("BRT", -3*60*60)
}()

// today devolve o instante atual no fuso do negócio.
func today() time.Time { return time.Now().In(brLocation) }
