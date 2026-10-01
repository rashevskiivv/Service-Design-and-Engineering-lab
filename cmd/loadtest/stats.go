package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dist summarizes one metric over the successful requests.
type dist struct {
	N   int     `json:"n"`
	Min float64 `json:"min"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// percentile returns the nearest-rank percentile (rank = ceil(p/100 * n)) of
// an ascending slice. p*n is computed before the division so that, e.g.,
// p95 of 20 values is exactly rank 19 and not 20 through rounding.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	rank := int(math.Ceil(p * float64(n) / 100))
	rank = min(max(rank, 1), n)
	return sorted[rank-1]
}

func newDist(values []float64) *dist {
	if len(values) == 0 {
		return nil
	}
	s := slices.Clone(values)
	slices.Sort(s)
	return &dist{
		N:   len(s),
		Min: s[0],
		P50: percentile(s, 50),
		P95: percentile(s, 95),
		P99: percentile(s, 99),
		Max: s[len(s)-1],
	}
}

type summary struct {
	Requests  int `json:"requests"`
	OK        int `json:"ok"`
	HTTP429   int `json:"http_429"`
	HTTP503   int `json:"http_503"`
	Errors    int `json:"errors"` // not ok, not 429/503, not cancelled
	Cancelled int `json:"cancelled"`
	// StatusHistogram buckets HTTP statuses: 200, 401, 429, 503, 4xx, 5xx,
	// other, none (no HTTP response). StatusCodes has the exact codes.
	StatusHistogram map[string]int `json:"status_histogram"`
	StatusCodes     map[string]int `json:"status_codes"`
	Classes         map[string]int `json:"classes"`
	// NonOK counts every non-ok outcome as class[:error_code].
	NonOK map[string]int `json:"non_ok"`
	// RetryAfter counts the Retry-After values seen, per status.
	RetryAfter map[string]map[string]int `json:"retry_after_seen"`
	// SampleErrors keeps one message per non-ok kind.
	SampleErrors map[string]string `json:"sample_errors,omitempty"`

	WallSeconds        float64 `json:"wall_s"`
	CompletionTokens   int     `json:"completion_tokens"`
	AggregateTokPerSec float64 `json:"aggregate_tok_s"`
	TokensFromUsage    int     `json:"tokens_from_usage"`
	TokensFromChunks   int     `json:"tokens_from_chunks"`
	EmptyContent       int     `json:"ok_with_empty_content"`

	// Seconds (tok/s for DecodeTokPerSec), over ok requests only.
	TTFB            *dist `json:"ttfb_s,omitempty"`
	TTFT            *dist `json:"ttft_s,omitempty"`
	TTFC            *dist `json:"ttfc_s,omitempty"`
	E2E             *dist `json:"e2e_s,omitempty"`
	DecodeTokPerSec *dist `json:"decode_tok_s,omitempty"`
}

func statusBucket(code int) string {
	switch {
	case code == 0:
		return "none"
	case code == 200 || code == 401 || code == 429 || code == 503:
		return strconv.Itoa(code)
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	}
	return "other"
}

func isError(class string) bool {
	switch class {
	case classOK, classHTTP429, classHTTP503, classCancelled:
		return false
	}
	return true
}

// summarize aggregates results. wall is the run's wall-clock time.
func summarize(results []result, wall time.Duration) summary {
	s := summary{
		Requests:        len(results),
		StatusHistogram: map[string]int{"200": 0, "401": 0, "429": 0, "503": 0, "4xx": 0, "5xx": 0, "none": 0},
		StatusCodes:     map[string]int{},
		Classes:         map[string]int{},
		NonOK:           map[string]int{},
		RetryAfter:      map[string]map[string]int{},
		SampleErrors:    map[string]string{},
		WallSeconds:     wall.Seconds(),
	}
	var ttfb, ttft, ttfc, e2e, tps []float64
	for i := range results {
		r := &results[i]
		s.StatusHistogram[statusBucket(r.Status)]++
		if r.Status != 0 {
			s.StatusCodes[strconv.Itoa(r.Status)]++
		}
		s.Classes[r.Class]++
		if r.RetryAfter != "" {
			k := strconv.Itoa(r.Status)
			if s.RetryAfter[k] == nil {
				s.RetryAfter[k] = map[string]int{}
			}
			s.RetryAfter[k][r.RetryAfter]++
		}
		switch r.Class {
		case classOK:
			s.OK++
		case classHTTP429:
			s.HTTP429++
		case classHTTP503:
			s.HTTP503++
		case classCancelled:
			s.Cancelled++
		}
		if isError(r.Class) {
			s.Errors++
		}
		if r.Class != classOK {
			kind := r.Class
			if r.ErrorCode != "" {
				kind += ":" + r.ErrorCode
			}
			s.NonOK[kind]++
			if _, seen := s.SampleErrors[kind]; !seen && r.Error != "" {
				s.SampleErrors[kind] = r.Error
			}
			continue
		}
		s.CompletionTokens += r.CompletionTokens
		switch r.TokensSource {
		case tokensFromUsage:
			s.TokensFromUsage++
		case tokensFromChunks:
			s.TokensFromChunks++
		}
		if r.ContentBytes == 0 {
			s.EmptyContent++
		}
		if r.hasTTFB {
			ttfb = append(ttfb, r.ttfb.Seconds())
		}
		if r.hasTTFT {
			ttft = append(ttft, r.ttft.Seconds())
		}
		if r.hasTTFC {
			ttfc = append(ttfc, r.ttfc.Seconds())
		}
		e2e = append(e2e, r.e2e.Seconds())
		if r.DecodeTokPerSec != nil {
			tps = append(tps, *r.DecodeTokPerSec)
		}
	}
	if len(s.SampleErrors) == 0 {
		s.SampleErrors = nil
	}
	if wall > 0 {
		s.AggregateTokPerSec = float64(s.CompletionTokens) / wall.Seconds()
	}
	s.TTFB, s.TTFT, s.TTFC, s.E2E, s.DecodeTokPerSec = newDist(ttfb), newDist(ttft), newDist(ttfc), newDist(e2e), newDist(tps)
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *config) modeString() string {
	switch {
	case c.smoke:
		return "smoke"
	case c.burst:
		return fmt.Sprintf("burst c=%d", c.conc)
	case c.duration > 0 && c.requests > 0:
		return fmt.Sprintf("c=%d d=%s n=%d", c.conc, c.duration, c.requests)
	case c.duration > 0:
		return fmt.Sprintf("c=%d d=%s", c.conc, c.duration)
	case c.requests > 0:
		return fmt.Sprintf("c=%d n=%d", c.conc, c.requests)
	}
	return fmt.Sprintf("c=%d n=%d", c.conc, c.conc)
}

func printSummary(w io.Writer, cfg *config, started time.Time, s summary) {
	maxTok := "default"
	if cfg.maxTokens > 0 {
		maxTok = strconv.Itoa(cfg.maxTokens)
	}
	fmt.Fprintf(w, "\nlocal-generative-ai loadtest  %s\n", started.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "target    %s  model=%s  endpoint=%s  lang=%s  size=%s  stream=%t  max_tokens=%s\n",
		cfg.baseURL, cfg.model, cfg.endpoint, cfg.lang, cfg.size, cfg.stream, maxTok)
	fmt.Fprintf(w, "run       %s  wall=%.1fs\n\n", cfg.modeString(), s.WallSeconds)

	fmt.Fprintf(w, "requests  %d   ok %d   429 %d   503 %d   errors %d   cancelled %d\n",
		s.Requests, s.OK, s.HTTP429, s.HTTP503, s.Errors, s.Cancelled)
	var hist []string
	for _, k := range []string{"200", "401", "429", "503", "4xx", "5xx", "other", "none"} {
		if v, ok := s.StatusHistogram[k]; ok {
			hist = append(hist, fmt.Sprintf("%s=%d", k, v))
		}
	}
	fmt.Fprintf(w, "status    %s\n", strings.Join(hist, "  "))
	if len(s.RetryAfter) > 0 {
		var parts []string
		for _, st := range sortedKeys(s.RetryAfter) {
			for _, v := range sortedKeys(s.RetryAfter[st]) {
				parts = append(parts, fmt.Sprintf("%s: %ss x%d", st, v, s.RetryAfter[st][v]))
			}
		}
		fmt.Fprintf(w, "retry-after  %s\n", strings.Join(parts, ", "))
	}
	if len(s.NonOK) > 0 {
		fmt.Fprintln(w, "non-ok:")
		for _, k := range sortedKeys(s.NonOK) {
			msg := ""
			if m, ok := s.SampleErrors[k]; ok {
				msg = "  e.g. " + truncate(m, 100)
			}
			fmt.Fprintf(w, "  %-45s %d%s\n", k, s.NonOK[k], msg)
		}
	}
	fmt.Fprintln(w)

	const rowFmt = "%-24s %6s %9s %9s %9s %9s %9s\n"
	fmt.Fprintf(w, rowFmt, "metric (ok requests)", "n", "min", "p50", "p95", "p99", "max")
	row := func(name string, d *dist, format string) {
		if d == nil {
			fmt.Fprintf(w, rowFmt, name, "0", "-", "-", "-", "-", "-")
			return
		}
		f := func(v float64) string { return fmt.Sprintf(format, v) }
		fmt.Fprintf(w, rowFmt, name, strconv.Itoa(d.N), f(d.Min), f(d.P50), f(d.P95), f(d.P99), f(d.Max))
	}
	row("TTFB s (first byte)", s.TTFB, "%.3f")
	row("TTFT s (first token)", s.TTFT, "%.3f")
	row("TTFC s (first content)", s.TTFC, "%.3f")
	row("E2E s", s.E2E, "%.2f")
	row("decode tok/s per stream", s.DecodeTokPerSec, "%.1f")

	fmt.Fprintf(w, "\naggregate %d completion tokens / %.1fs = %.1f tok/s   (token counts: %d from usage, %d from chunk count)\n",
		s.CompletionTokens, s.WallSeconds, s.AggregateTokPerSec, s.TokensFromUsage, s.TokensFromChunks)
	if s.EmptyContent > 0 {
		fmt.Fprintf(w, "warning: %d ok response(s) had empty content (reasoning ate max_tokens?)\n", s.EmptyContent)
	}
}

type configJSON struct {
	URL         string `json:"url"`
	KeySet      bool   `json:"key_set"`
	Model       string `json:"model"`
	Mode        string `json:"mode"`
	Concurrency int    `json:"c"`
	Requests    int    `json:"n"`
	Duration    string `json:"d"`
	Burst       bool   `json:"burst"`
	Smoke       bool   `json:"smoke"`
	Endpoint    string `json:"endpoint"`
	Lang        string `json:"lang"`
	Size        string `json:"size"`
	Stream      bool   `json:"stream"`
	MaxTokens   int    `json:"max_tokens"`
	Timeout     string `json:"timeout"`
	Seed        uint64 `json:"seed"`
}

type report struct {
	Tool      string     `json:"tool"`
	StartedAt string     `json:"started_at"`
	Config    configJSON `json:"config"`
	Summary   summary    `json:"summary"`
	Smoke     []smokeRow `json:"smoke,omitempty"`
	Results   []result   `json:"results"`
}

// writeJSON writes the report. It never contains the API key or any answer text.
func writeJSON(path string, cfg *config, started time.Time, s summary, results []result, smoke []smokeRow) error {
	rep := report{
		Tool:      "local-generative-ai/cmd/loadtest",
		StartedAt: started.UTC().Format(time.RFC3339),
		Config: configJSON{
			URL: cfg.baseURL, KeySet: cfg.key != "", Model: cfg.model, Mode: cfg.modeString(),
			Concurrency: cfg.conc, Requests: cfg.requests, Duration: cfg.duration.String(),
			Burst: cfg.burst, Smoke: cfg.smoke, Endpoint: cfg.endpoint, Lang: cfg.lang, Size: cfg.size,
			Stream: cfg.stream, MaxTokens: cfg.maxTokens, Timeout: cfg.timeout.String(), Seed: cfg.seed,
		},
		Summary: s,
		Smoke:   smoke,
		Results: results,
	}
	if rep.Results == nil {
		rep.Results = []result{}
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
