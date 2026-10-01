package store

import (
	"context"
	"database/sql"
	"time"
)

// UsageRow is one generation request. It holds counts and timings only, never
// content or keys.
type UsageRow struct {
	TS               time.Time
	RequestID        string
	KeyID            int64
	Endpoint         string // chat | explain | review | tests | fix
	Model            string // public alias; "" → NULL
	Language         string // tasks only; "" → NULL
	Stream           bool
	Status           int    // effective status; 499 = client disconnected
	ErrorCode        string // "" → NULL
	PromptTokens     int
	CompletionTokens int
	Estimated        bool           // counts estimated (ADR 0004)
	Queue            *time.Duration // nil → NULL
	TTFT             *time.Duration // nil → NULL
	Latency          time.Duration
}

// InsertUsage records one usage row.
func (s *Store) InsertUsage(ctx context.Context, u UsageRow) error {
	durMS := func(d *time.Duration) any {
		if d == nil {
			return nil
		}
		return d.Milliseconds()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO usage
		(ts, request_id, key_id, endpoint, model, language, stream, status, error_code,
		 prompt_tokens, completion_tokens, usage_estimated, queue_ms, ttft_ms, latency_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ms(u.TS), u.RequestID, u.KeyID, u.Endpoint, nullStr(u.Model), nullStr(u.Language), u.Stream, u.Status,
		nullStr(u.ErrorCode), max(u.PromptTokens, 0), max(u.CompletionTokens, 0), u.Estimated,
		durMS(u.Queue), durMS(u.TTFT), max(u.Latency.Milliseconds(), 0))
	return err
}

// TokensSince sums prompt + completion tokens of a key since a time (the daily
// quota window).
func (s *Store) TokensSince(ctx context.Context, keyID int64, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(prompt_tokens + completion_tokens), 0)
		FROM usage WHERE key_id = ? AND ts >= ?`, keyID, ms(since)).Scan(&n)
	return n, err
}

// UsageSummary aggregates one key's usage in [Since, Until).
type UsageSummary struct {
	KeyID            int64      `json:"key_id"`
	Name             string     `json:"name"`
	Since            time.Time  `json:"since"`
	Until            time.Time  `json:"until"`
	Requests         int64      `json:"requests"`
	OK               int64      `json:"ok"`
	RateLimited      int64      `json:"rate_limited"`
	Overloaded       int64      `json:"overloaded"`
	UpstreamErrors   int64      `json:"upstream_errors"`
	ClientCancelled  int64      `json:"client_cancelled"`
	PromptTokens     int64      `json:"prompt_tokens"`
	CompletionTokens int64      `json:"completion_tokens"`
	TotalTokens      int64      `json:"total_tokens"`
	EstimatedRows    int64      `json:"estimated_rows"`
	AvgTTFTMS        *int64     `json:"avg_ttft_ms"`
	AvgLatencyMS     *int64     `json:"avg_latency_ms"`
	FirstRequestAt   *time.Time `json:"first_request_at"`
	LastRequestAt    *time.Time `json:"last_request_at"`
}

// KeyUsage summarises one key's usage in [since, until). It returns
// ErrNotFound if the key does not exist.
func (s *Store) KeyUsage(ctx context.Context, keyID int64, since, until time.Time) (UsageSummary, error) {
	k, err := s.GetKey(ctx, keyID)
	if err != nil {
		return UsageSummary{}, err
	}
	u := UsageSummary{KeyID: k.ID, Name: k.Name, Since: since.UTC(), Until: until.UTC()}
	untilMS := ms(until)
	if until.After(fromMS(untilMS)) {
		untilMS++ // rows are stored in whole ms; round the exclusive bound up
	}
	var ttft, latency, first, last sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(status = 200), 0), COALESCE(SUM(status = 429), 0), COALESCE(SUM(status = 503), 0),
		COALESCE(SUM(status >= 500 AND status <> 503), 0), COALESCE(SUM(status = 499), 0),
		COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(usage_estimated), 0),
		CAST(AVG(ttft_ms) AS INTEGER), CAST(AVG(latency_ms) AS INTEGER), MIN(ts), MAX(ts)
		FROM usage WHERE key_id = ? AND ts >= ? AND ts < ?`, keyID, ms(since), untilMS).Scan(
		&u.Requests, &u.OK, &u.RateLimited, &u.Overloaded, &u.UpstreamErrors, &u.ClientCancelled,
		&u.PromptTokens, &u.CompletionTokens, &u.EstimatedRows, &ttft, &latency, &first, &last)
	if err != nil {
		return UsageSummary{}, err
	}
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	u.AvgTTFTMS, u.AvgLatencyMS = nullInt(ttft), nullInt(latency)
	u.FirstRequestAt, u.LastRequestAt = nullTime(first), nullTime(last)
	return u, nil
}
