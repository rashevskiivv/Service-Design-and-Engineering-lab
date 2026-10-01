package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"
	"time"
)

// selector picks endpoint, language and size for each request. It is used
// from a single goroutine only.
type selector struct {
	cfg    *config
	corpus corpus
	rng    *rand.Rand
}

func newSelector(cfg *config, c corpus) *selector {
	return &selector{cfg: cfg, corpus: c, rng: rand.New(rand.NewPCG(cfg.seed, cfg.seed^0x9e3779b97f4a7c15))}
}

func (s *selector) pick(v string, all []string) string {
	if v != "mix" {
		return v
	}
	return all[s.rng.IntN(len(all))]
}

func (s *selector) next(i int) job {
	ep := s.pick(s.cfg.endpoint, endpoints)
	lang := s.pick(s.cfg.lang, languages)
	size := s.pick(s.cfg.size, sizes)
	return job{index: i, endpoint: ep, lang: lang, size: size, sn: s.corpus[lang][size]}
}

// runLoad runs the closed-loop or burst load and prints the report.
func (r *runner) runLoad(ctx context.Context, stdout io.Writer) int {
	cfg := r.cfg
	sel := newSelector(cfg, r.corpus)
	started := r.now()
	resCh := make(chan result, cfg.conc)
	var wg sync.WaitGroup

	if cfg.burst {
		jobs := make([]job, cfg.conc)
		for i := range jobs {
			jobs[i] = sel.next(i)
		}
		start := make(chan struct{})
		for _, j := range jobs {
			wg.Go(func() {
				<-start
				resCh <- r.do(ctx, j, started, false)
			})
		}
		close(start) // every goroutine is released at the same instant
	} else {
		total := cfg.requests
		if total == 0 && cfg.duration == 0 {
			total = cfg.conc
		}
		var stop <-chan time.Time // nil (never fires) without -d
		if cfg.duration > 0 {
			t := time.NewTimer(cfg.duration)
			defer t.Stop()
			stop = t.C
		}
		jobs := make(chan job)
		go func() {
			defer close(jobs)
			for i := 0; total == 0 || i < total; i++ {
				j := sel.next(i)
				select { // check the deadline first so no request starts after it
				case <-stop:
					return
				case <-ctx.Done():
					return
				default:
				}
				select {
				case jobs <- j:
				case <-stop:
					return
				case <-ctx.Done():
					return
				}
			}
		}()
		for range cfg.conc {
			wg.Go(func() {
				for j := range jobs {
					resCh <- r.do(ctx, j, started, false)
				}
			})
		}
	}
	go func() {
		wg.Wait()
		close(resCh)
	}()

	results := r.collect(ctx, resCh, started)
	wall := r.now().Sub(started)
	s := summarize(results, wall)
	printSummary(stdout, cfg, started, s)
	if cfg.jsonPath != "" {
		if err := writeJSON(cfg.jsonPath, cfg, started, s, results, nil); err != nil {
			fmt.Fprintf(r.stderr, "loadtest: write -json: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(stdout, "json      %s\n", cfg.jsonPath)
	}
	if s.Errors > 0 {
		return exitFailed
	}
	return exitOK
}

// collect gathers results in completion order and prints progress to stderr
// every 10 s (and one line per request with -v).
func (r *runner) collect(ctx context.Context, resCh <-chan result, started time.Time) []result {
	var results []result
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	counts := map[string]int{}
	for {
		select {
		case res, ok := <-resCh:
			if !ok {
				return results
			}
			results = append(results, res)
			counts[res.Class]++
			if r.cfg.verbose {
				fmt.Fprintln(r.stderr, res.describe())
			}
		case <-tick.C:
			errs := 0
			for k, v := range counts {
				if isError(k) {
					errs += v
				}
			}
			fmt.Fprintf(r.stderr, "[%5.0fs] done %d  ok %d  429 %d  503 %d  errors %d\n",
				r.now().Sub(started).Seconds(), len(results), counts[classOK], counts[classHTTP429], counts[classHTTP503], errs)
		case <-ctx.Done():
			// Interrupted: in-flight requests are cancelled by ctx and still
			// report (as "cancelled"), so keep draining until resCh closes.
			ctx = context.Background()
		}
	}
}
