package mcp

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// rateLimiter: janela deslizante por Connection, em memória da instância (AYD-006 §1:
// 120 chamadas tools/call por minuto por Connection, por instância).
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[uuid.UUID][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, hits: map[uuid.UUID][]time.Time{}}
}

// Allow registra uma chamada e diz se cabe na janela.
func (l *rateLimiter) Allow(id uuid.UUID, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-l.window)
	h := l.hits[id]
	i := 0
	for i < len(h) && !h[i].After(cut) {
		i++
	}
	h = h[i:]
	if len(h) >= l.limit {
		l.hits[id] = h
		return false
	}
	l.hits[id] = append(h, now)
	if len(l.hits) > 1024 { // higiene: descarta Connections sem atividade recente
		for k, v := range l.hits {
			if len(v) == 0 || !v[len(v)-1].After(cut) {
				delete(l.hits, k)
			}
		}
	}
	return true
}
