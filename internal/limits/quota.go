package limits

import (
	"context"
	"errors"
	"time"
)

// ErrQuotaExceeded means the key used its daily token quota.
var ErrQuotaExceeded = errors.New("daily token quota exceeded")

// UsageReader reads the tokens a key used since a time.
type UsageReader interface {
	TokensSince(ctx context.Context, keyID int64, since time.Time) (int64, error)
}

// Quota enforces the per-key daily token quota over the UTC day. It counts
// finished requests only, so in-flight ones can overshoot slightly (accepted).
type Quota struct {
	r   UsageReader
	now Clock
}

// NewQuota returns a Quota reading usage from r.
func NewQuota(r UsageReader, now Clock) *Quota { return &Quota{r: r, now: now} }

// Check returns ErrQuotaExceeded and the time until 00:00 UTC when keyID has
// used at least limit tokens today. limit 0 means unlimited.
func (q *Quota) Check(ctx context.Context, keyID, limit int64) (time.Duration, error) {
	if limit <= 0 {
		return 0, nil
	}
	now := q.now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	used, err := q.r.TokensSince(ctx, keyID, day)
	if err != nil {
		return 0, err
	}
	if used >= limit {
		return day.Add(24 * time.Hour).Sub(now), ErrQuotaExceeded
	}
	return 0, nil
}
