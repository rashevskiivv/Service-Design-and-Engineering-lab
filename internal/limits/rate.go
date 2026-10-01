// Package limits implements the admission limits: a per-key token bucket, a
// per-key in-flight cap, the daily token quota, and the global Gate.
package limits

import (
	"math"
	"sync"
	"time"
)

// Clock returns the current time; tests substitute a fake.
type Clock func() time.Time

// RateLimiter is an in-memory token bucket per key id: capacity burst, refill
// rpm/60 tokens per second. The map is bounded by the number of keys.
type RateLimiter struct {
	mu      sync.Mutex
	now     Clock
	buckets map[int64]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter returns an empty limiter.
func NewRateLimiter(now Clock) *RateLimiter {
	return &RateLimiter{now: now, buckets: map[int64]*bucket{}}
}

// Allow takes one token for keyID. rpm 0 means unlimited. When the bucket is
// empty it returns false and the time until the next token.
func (l *RateLimiter) Allow(keyID int64, rpm, burst int) (bool, time.Duration) {
	return l.take(keyID, rpm, burst, true)
}

// Peek reports what Allow would answer, without taking a token.
func (l *RateLimiter) Peek(keyID int64, rpm, burst int) (bool, time.Duration) {
	return l.take(keyID, rpm, burst, false)
}

func (l *RateLimiter) take(keyID int64, rpm, burst int, consume bool) (bool, time.Duration) {
	if rpm <= 0 {
		return true, 0
	}
	burst = max(burst, 1)
	rate := float64(rpm) / 60 // tokens per second
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[keyID]
	if !ok {
		b = &bucket{tokens: float64(burst), last: now}
		l.buckets[keyID] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(float64(burst), b.tokens+elapsed*rate)
		b.last = now
	}
	if b.tokens >= 1 {
		if consume {
			b.tokens--
		}
		return true, 0
	}
	wait := time.Duration(math.Ceil((1 - b.tokens) / rate * float64(time.Second)))
	return false, max(wait, time.Second)
}

// Refund gives back the token an Allow took for a request that never ran
// (the global gate refused it, or the client left while queued). Without it,
// the openai SDK's automatic retries would drain the bucket. rpm 0 took
// nothing, so there is nothing to refund.
func (l *RateLimiter) Refund(keyID int64, rpm, burst int) {
	if rpm <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[keyID]; ok {
		b.tokens = math.Min(float64(max(burst, 1)), b.tokens+1)
	}
}
