package main

import (
	"math"
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	// Worked example from the nearest-rank definition: {15, 20, 35, 40, 50}.
	five := []float64{15, 20, 35, 40, 50}
	for _, tc := range []struct {
		p    float64
		want float64
	}{{5, 15}, {30, 20}, {40, 20}, {50, 35}, {95, 50}, {100, 50}} {
		if got := percentile(five, tc.p); got != tc.want {
			t.Errorf("p%v of %v = %v, want %v", tc.p, five, got, tc.want)
		}
	}

	var twenty []float64
	for i := 1; i <= 20; i++ {
		twenty = append(twenty, float64(i))
	}
	// ceil(0.95*20) = 19 exactly; a naive p/100*n gives 19.000000000000004 -> 20.
	for _, tc := range []struct {
		p    float64
		want float64
	}{{50, 10}, {95, 19}, {99, 20}} {
		if got := percentile(twenty, tc.p); got != tc.want {
			t.Errorf("p%v of 1..20 = %v, want %v", tc.p, got, tc.want)
		}
	}

	var hundred []float64
	for i := 1; i <= 100; i++ {
		hundred = append(hundred, float64(i))
	}
	if percentile(hundred, 50) != 50 || percentile(hundred, 95) != 95 || percentile(hundred, 99) != 99 {
		t.Errorf("1..100: p50/p95/p99 = %v/%v/%v", percentile(hundred, 50), percentile(hundred, 95), percentile(hundred, 99))
	}
	if got := percentile([]float64{7}, 99); got != 7 {
		t.Errorf("single value p99 = %v", got)
	}
	if !math.IsNaN(percentile(nil, 50)) {
		t.Error("empty input should be NaN")
	}
}

func TestNewDistSortsAndSummarizes(t *testing.T) {
	d := newDist([]float64{9, 1, 5, 3, 7})
	if d == nil || d.N != 5 || d.Min != 1 || d.Max != 9 || d.P50 != 5 || d.P95 != 9 {
		t.Fatalf("dist = %+v", d)
	}
	if newDist(nil) != nil {
		t.Error("empty dist should be nil (omitted from JSON)")
	}
}

func okResult(ttfc, e2e time.Duration, tokens int) result {
	r := result{Class: classOK, Status: 200, Stream: true, CompletionTokens: tokens, TokensSource: tokensFromUsage, ContentBytes: 10,
		ttfb: 5 * time.Millisecond, hasTTFB: true, ttft: ttfc, hasTTFT: true, ttfc: ttfc, hasTTFC: true,
		lastTok: e2e - time.Millisecond, e2e: e2e}
	r.finalize()
	return r
}

func TestSummarizeCountsAndRates(t *testing.T) {
	results := []result{
		okResult(1*time.Second, 11*time.Second, 101),
		okResult(2*time.Second, 12*time.Second, 101),
		{Class: classHTTP429, Status: 429, RetryAfter: "3", ErrorCode: "rate_limit_exceeded", Error: "rate limit"},
		{Class: classHTTP429, Status: 429, RetryAfter: "3", ErrorCode: "concurrency_limit_exceeded"},
		{Class: classHTTP503, Status: 503, RetryAfter: "10", ErrorCode: "server_overloaded"},
		{Class: classHTTP503, Status: 503, RetryAfter: "10", ErrorCode: "maintenance"},
		{Class: classHTTP401, Status: 401, ErrorCode: "invalid_api_key"},
		{Class: classHTTP5xx, Status: 502, ErrorCode: "upstream_error"},
		{Class: classStreamError, Status: 200, ErrorCode: "upstream_timeout"},
		{Class: classConnError, Status: 0, Error: "connection refused"},
		{Class: classCancelled, Status: 0},
	}
	s := summarize(results, 20*time.Second)
	if s.Requests != 11 || s.OK != 2 || s.HTTP429 != 2 || s.HTTP503 != 2 || s.Cancelled != 1 {
		t.Fatalf("counts: %+v", s)
	}
	if s.Errors != 4 { // 401, 502, stream_error, conn_error
		t.Errorf("errors = %d, want 4", s.Errors)
	}
	wantHist := map[string]int{"200": 3, "401": 1, "429": 2, "503": 2, "4xx": 0, "5xx": 1, "none": 2}
	for k, v := range wantHist {
		if s.StatusHistogram[k] != v {
			t.Errorf("histogram[%s] = %d, want %d", k, s.StatusHistogram[k], v)
		}
	}
	if s.RetryAfter["429"]["3"] != 2 || s.RetryAfter["503"]["10"] != 2 {
		t.Errorf("retry-after = %v", s.RetryAfter)
	}
	if s.NonOK["http_503:maintenance"] != 1 || s.NonOK["http_429:concurrency_limit_exceeded"] != 1 || s.NonOK["conn_error"] != 1 {
		t.Errorf("non-ok = %v", s.NonOK)
	}
	if s.CompletionTokens != 202 || s.AggregateTokPerSec != 202.0/20 {
		t.Errorf("aggregate = %d tokens, %v tok/s", s.CompletionTokens, s.AggregateTokPerSec)
	}
	if s.TTFC == nil || s.TTFC.N != 2 || s.TTFC.P50 != 1 || s.TTFC.Max != 2 {
		t.Errorf("TTFC dist = %+v", s.TTFC)
	}
	if s.E2E == nil || s.E2E.P99 != 12 {
		t.Errorf("E2E dist = %+v", s.E2E)
	}
	// 100 tokens after the first over (e2e - 1ms - ttft).
	if s.DecodeTokPerSec == nil || abs(s.DecodeTokPerSec.Max-100/9.999) > 1e-9 {
		t.Errorf("decode dist = %+v", s.DecodeTokPerSec)
	}
}
