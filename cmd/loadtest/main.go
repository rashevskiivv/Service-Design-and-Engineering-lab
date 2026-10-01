// Command loadtest drives concurrent requests against the local-generative-ai
// gateway and reports what students would experience: time to first byte,
// time to first visible content, per-stream decode speed, aggregate
// throughput, latency percentiles and the status mix (docs/DECISIONS.md D8).
//
// It uses only the standard library. The request corpus (5 languages x
// {small, medium} exercise-sized snippets, each with the compiler or runtime
// output used by the fix task) is embedded from ./corpus.
//
// Typical runs:
//
//	export LGAI_KEY=lgai_...               # prefer the env var over -key
//	loadtest -url http://127.0.0.1:8080 -smoke -out loadtest-results/smoke-laptop
//	loadtest -url https://lgai.example.org -c 30 -d 10m -endpoint mix -json run.json
//	loadtest -url https://lgai.example.org -c 30 -burst -size medium
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

// These lists are read-only.
var (
	endpoints = []string{"chat", "explain", "review", "tests", "fix"}
	taskNames = []string{"explain", "review", "tests", "fix"}
	languages = []string{"python", "java", "go", "c", "cpp"}
	sizes     = []string{"small", "medium"}
)

// Exit codes.
const (
	exitOK     = 0 // run finished; no errors other than 429/503
	exitFailed = 1 // load: some request failed (not 429/503); smoke: some answer failed
	exitUsage  = 2 // bad flags or tool failure
)

type config struct {
	baseURL   string // normalized: no trailing slash, no /v1
	key       string
	model     string
	conc      int
	duration  time.Duration
	requests  int
	endpoint  string
	lang      string
	size      string
	stream    bool
	maxTokens int
	burst     bool
	smoke     bool
	outDir    string
	jsonPath  string
	timeout   time.Duration
	seed      uint64
	verbose   bool
}

const usageHeader = `loadtest: load generator and smoke checker for the local-generative-ai gateway.

Modes:
  default   closed loop: -c clients send requests until -n requests are done or -d elapses
  -burst    all -c clients send exactly one request at the same instant
  -smoke    one request per task x language (4 x 5 = 20), sequential; each answer is
            written to -out as Markdown; exit status 1 if any answer is empty or fails

The API key is read from $LGAI_KEY unless -key is given (flags are visible in ps and
shell history). Exit status: 0 ok, 1 failures (load: errors other than 429/503;
smoke: a FAIL verdict), 2 bad usage.

Flags:
`

func parseFlags(args []string, stderr io.Writer, getenv func(string) string) (*config, error) {
	cfg := &config{}
	fs := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usageHeader)
		fs.PrintDefaults()
	}
	fs.StringVar(&cfg.baseURL, "url", "http://127.0.0.1:8080", "gateway base URL, with or without the trailing /v1")
	fs.StringVar(&cfg.key, "key", "", "API key (default $LGAI_KEY)")
	fs.StringVar(&cfg.model, "model", "coder", "model alias sent with every request")
	fs.IntVar(&cfg.conc, "c", 1, "number of concurrent clients")
	fs.DurationVar(&cfg.duration, "d", 0, "stop starting new requests after this long, e.g. 5m (0 = use -n)")
	fs.IntVar(&cfg.requests, "n", 0, "total number of requests (0 with -d 0 = one per client)")
	fs.StringVar(&cfg.endpoint, "endpoint", "mix", "chat|explain|review|tests|fix|mix")
	fs.StringVar(&cfg.lang, "lang", "mix", "python|java|go|c|cpp|mix")
	fs.StringVar(&cfg.size, "size", "mix", "small|medium|mix (smoke: mix means small)")
	fs.BoolVar(&cfg.stream, "stream", true, "request SSE streaming; -stream=false for plain JSON responses")
	fs.IntVar(&cfg.maxTokens, "max-tokens", 0, "max_tokens for every request (0 = the endpoint default)")
	fs.BoolVar(&cfg.burst, "burst", false, "all -c clients start at once and send one request each")
	fs.BoolVar(&cfg.smoke, "smoke", false, "one request per task x language, answers saved to -out, fail on empty content")
	fs.StringVar(&cfg.outDir, "out", "", "smoke: output directory (default loadtest-results/smoke-<UTC time>)")
	fs.StringVar(&cfg.jsonPath, "json", "", "also write the summary and per-request results to this JSON file")
	fs.DurationVar(&cfg.timeout, "timeout", 5*time.Minute, "per-request timeout")
	fs.Uint64Var(&cfg.seed, "seed", 1, "seed for picking endpoint/language/size in mix mode")
	fs.BoolVar(&cfg.verbose, "v", false, "print one line per finished request to stderr")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %q", fs.Args())
	}
	if cfg.key == "" {
		cfg.key = getenv("LGAI_KEY")
	}
	cfg.key = strings.TrimSpace(cfg.key)

	base, err := normalizeBaseURL(cfg.baseURL)
	if err != nil {
		return nil, err
	}
	cfg.baseURL = base

	switch {
	case cfg.conc < 1:
		return nil, errors.New("-c must be at least 1")
	case cfg.requests < 0:
		return nil, errors.New("-n must not be negative")
	case cfg.duration < 0:
		return nil, errors.New("-d must not be negative")
	case cfg.timeout <= 0:
		return nil, errors.New("-timeout must be positive")
	case cfg.maxTokens < 0:
		return nil, errors.New("-max-tokens must not be negative")
	case cfg.endpoint != "mix" && !slices.Contains(endpoints, cfg.endpoint):
		return nil, fmt.Errorf("-endpoint %q: want one of %s or mix", cfg.endpoint, strings.Join(endpoints, ", "))
	case cfg.lang != "mix" && !slices.Contains(languages, cfg.lang):
		return nil, fmt.Errorf("-lang %q: want one of %s or mix", cfg.lang, strings.Join(languages, ", "))
	case cfg.size != "mix" && !slices.Contains(sizes, cfg.size):
		return nil, fmt.Errorf("-size %q: want one of %s or mix", cfg.size, strings.Join(sizes, ", "))
	case cfg.burst && (cfg.requests > 0 || cfg.duration > 0):
		return nil, errors.New("-burst sends exactly one request per client; drop -n and -d")
	case cfg.smoke && cfg.burst:
		return nil, errors.New("-smoke and -burst are separate modes")
	case cfg.smoke && cfg.endpoint == "chat":
		return nil, errors.New("-smoke covers the task endpoints; use -endpoint explain|review|tests|fix|mix")
	}
	return cfg, nil
}

// normalizeBaseURL accepts "http://host:8080", "http://host:8080/" or the
// OpenAI-style "http://host:8080/v1" and returns the root without a slash.
func normalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("-url %q: want http(s)://host[:port]", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("-url %q: no credentials, query or fragment (the key goes in -key or LGAI_KEY)", raw)
	}
	p := strings.TrimRight(u.Path, "/")
	p = strings.TrimSuffix(p, "/v1")
	u.Path = p
	return strings.TrimRight(u.String(), "/"), nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	cfg, err := parseFlags(args, stderr, getenv)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "loadtest: %v\n", err)
		return exitUsage
	}
	r, err := newRunner(cfg, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "loadtest: %v\n", err)
		return exitUsage
	}
	if cfg.key == "" {
		fmt.Fprintln(stderr, "loadtest: warning: no API key (-key or LGAI_KEY); expect 401 responses")
	}
	if cfg.smoke {
		return r.runSmoke(ctx, stdout)
	}
	return r.runLoad(ctx, stdout)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}
