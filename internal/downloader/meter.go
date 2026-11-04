package downloader

import (
	"context"
	"sync"
	"time"
)

// meter measures speed over the last few seconds.
type meter struct {
	mu      sync.Mutex
	buckets [5]int64
	at      int64 // the second buckets[at%5] is for
}

func (m *meter) add(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roll(time.Now().Unix())
	m.buckets[m.at%int64(len(m.buckets))] += n
}

func (m *meter) roll(now int64) {
	if now-m.at >= int64(len(m.buckets)) {
		clear(m.buckets[:])
	} else {
		for s := m.at + 1; s <= now; s++ {
			m.buckets[s%int64(len(m.buckets))] = 0
		}
	}
	m.at = now
}

// rate is bytes per second, over the last full seconds.
func (m *meter) rate() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	m.roll(now)
	var t int64
	for i, b := range m.buckets {
		if int64(i) != now%int64(len(m.buckets)) {
			t += b
		}
	}
	return t / int64(len(m.buckets)-1)
}

// limiter holds downloads to a speed: a token bucket a second deep.
type limiter struct {
	mu     sync.Mutex
	rate   int64 // bytes a second; 0 is no limit
	tokens float64
	last   time.Time
}

func (l *limiter) set(rate int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rate = rate
}

func (l *limiter) wait(ctx context.Context, n int64) error {
	for {
		l.mu.Lock()
		if l.rate <= 0 {
			l.mu.Unlock()
			return nil
		}
		now := time.Now()
		l.tokens = min(l.tokens+now.Sub(l.last).Seconds()*float64(l.rate), float64(l.rate))
		l.last = now
		if l.tokens >= 0 {
			l.tokens -= float64(n)
			l.mu.Unlock()
			return nil
		}
		wait := time.Duration(-l.tokens / float64(l.rate) * float64(time.Second))
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
