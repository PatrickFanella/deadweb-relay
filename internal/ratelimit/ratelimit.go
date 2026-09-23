package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	start time.Time
	count int
}

type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	items  map[string]bucket
}

func New(limit int, window time.Duration) *Limiter {
	if limit < 1 {
		limit = 1
	}
	return &Limiter{limit: limit, window: window, items: map[string]bucket{}}
}

func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.items[key]
	if b.start.IsZero() || now.Sub(b.start) >= l.window {
		b = bucket{start: now}
	}
	if b.count >= l.limit {
		l.items[key] = b
		return false
	}
	b.count++
	l.items[key] = b
	return true
}
