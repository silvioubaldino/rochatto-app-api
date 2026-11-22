package handlers

import "time"

var brLocation = func() *time.Location {
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return loc
	}
	return time.FixedZone("BRT", -3*60*60)
}()

func today() time.Time { return time.Now().In(brLocation) }
