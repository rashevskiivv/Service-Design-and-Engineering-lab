package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Smoke retries a request rejected with 429/503, honouring Retry-After, so a
// normal lab key (e.g. 6 requests/min) can run all 20 requests.
const (
	smokeMaxAttempts   = 4
	smokeDefaultWait   = 5 * time.Second
	smokeMaxRetryAfter = 120 * time.Second
)

type smokeRow struct {
	Task    string   `json:"task"`
	Lang    string   `json:"lang"`
	Size    string   `json:"size"`
	Verdict string   `json:"verdict"` // PASS, WARN or FAIL
	Notes   []string `json:"notes,omitempty"`
	File    string   `json:"file"`
	Result  int      `json:"result_index"`
}

// judge applies the smoke rules: FAIL on any non-ok outcome or empty
// content; WARN on truncation (finish_reason=length) and on tests/fix
// answers without a fenced code block.
func judge(res *result) (verdict string, notes []string) {
	if res.Class != classOK {
		n := "request failed: " + res.Class
		if res.ErrorCode != "" {
			n += " (" + res.ErrorCode + ")"
		}
		if res.Error != "" {
			n += ": " + truncate(res.Error, 160)
		}
		return "FAIL", []string{n}
	}
	if strings.TrimSpace(res.content) == "" {
		n := "empty content"
		if res.ReasoningBytes > 0 {
			n += fmt.Sprintf(" (only %d bytes of reasoning", res.ReasoningBytes)
			if res.FinishReason != "" {
				n += ", finish_reason=" + res.FinishReason
			}
			n += "; lower reasoning or raise max_tokens)"
		} else if res.FinishReason != "" {
			n += " (finish_reason=" + res.FinishReason + ")"
		}
		return "FAIL", []string{n}
	}
	verdict = "PASS"
	if res.FinishReason == "length" {
		verdict = "WARN"
		notes = append(notes, "truncated: finish_reason=length")
	}
	if (res.Endpoint == "tests" || res.Endpoint == "fix") && !strings.Contains(res.content, "```") {
		verdict = "WARN"
		notes = append(notes, "no fenced code block")
	}
	return verdict, notes
}

// retryDelay parses Retry-After (integer seconds; HTTP dates are not used by
// the gateway) and caps it like the openai client does.
func retryDelay(v string) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		return min(time.Duration(n)*time.Second, smokeMaxRetryAfter)
	}
	return smokeDefaultWait
}

func (r *runner) doWithRetry(ctx context.Context, j job, started time.Time) result {
	var res result
	for attempt := 1; ; attempt++ {
		res = r.do(ctx, j, started, true)
		res.Attempts = attempt
		retryable := res.Class == classHTTP429 || res.Class == classHTTP503
		if !retryable || attempt == smokeMaxAttempts || strings.EqualFold(res.shouldRetry, "false") {
			return res
		}
		d := retryDelay(res.RetryAfter)
		fmt.Fprintf(r.stderr, "smoke: %s/%s got %d (%s), retrying in %s\n", j.endpoint, j.lang, res.Status, res.ErrorCode, d)
		if r.sleep(ctx, d) != nil {
			return res
		}
	}
}

// runSmoke sends one request per task x language, writes each answer as
// Markdown for human review, and fails if any answer is empty.
func (r *runner) runSmoke(ctx context.Context, stdout io.Writer) int {
	cfg := r.cfg
	size := cfg.size
	if size == "mix" {
		size = "small"
	}
	taskList := taskNames
	if cfg.endpoint != "mix" {
		taskList = []string{cfg.endpoint}
	}
	langList := languages
	if cfg.lang != "mix" {
		langList = []string{cfg.lang}
	}
	started := r.now()
	outDir := cfg.outDir
	if outDir == "" {
		outDir = filepath.Join("loadtest-results", "smoke-"+started.UTC().Format("20060102T150405Z"))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(r.stderr, "loadtest: %v\n", err)
		return exitUsage
	}

	var (
		results []result
		rows    []smokeRow
	)
	for _, task := range taskList {
		for _, lang := range langList {
			if ctx.Err() != nil {
				break
			}
			j := job{index: len(results), endpoint: task, lang: lang, size: size, sn: r.corpus[lang][size]}
			res := r.doWithRetry(ctx, j, started)
			if cfg.verbose {
				fmt.Fprintln(r.stderr, res.describe())
			}
			verdict, notes := judge(&res)
			row := smokeRow{Task: task, Lang: lang, Size: size, Verdict: verdict, Notes: notes,
				File: task + "-" + lang + ".md", Result: res.Index}
			if err := os.WriteFile(filepath.Join(outDir, row.File), []byte(answerMarkdown(cfg, j, &res, row)), 0o644); err != nil {
				fmt.Fprintf(r.stderr, "loadtest: %v\n", err)
				return exitUsage
			}
			results = append(results, res)
			rows = append(rows, row)
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "index.md"), []byte(indexMarkdown(cfg, started, results, rows)), 0o644); err != nil {
		fmt.Fprintf(r.stderr, "loadtest: %v\n", err)
		return exitUsage
	}

	counts := map[string]int{}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "task\tlang\tverdict\tstatus\tfinish\ttokens\tTTFC s\tE2E s\tnotes")
	for i, row := range rows {
		res := &results[i]
		counts[row.Verdict]++
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%.1f\t%s\n", row.Task, row.Lang, row.Verdict, res.Status,
			dash(res.FinishReason), tokensCell(res), secondsCell(res.hasTTFC, res.ttfc), res.e2e.Seconds(), strings.Join(row.Notes, "; "))
	}
	tw.Flush()
	fmt.Fprintf(stdout, "\nsmoke: %d answers in %s  PASS %d  WARN %d  FAIL %d\n",
		len(rows), outDir, counts["PASS"], counts["WARN"], counts["FAIL"])

	s := summarize(results, r.now().Sub(started))
	printSummary(stdout, cfg, started, s)
	if cfg.jsonPath != "" {
		if err := writeJSON(cfg.jsonPath, cfg, started, s, results, rows); err != nil {
			fmt.Fprintf(r.stderr, "loadtest: write -json: %v\n", err)
			return exitUsage
		}
	}
	if counts["FAIL"] > 0 || ctx.Err() != nil || len(rows) < len(taskList)*len(langList) {
		return exitFailed
	}
	return exitOK
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func tokensCell(res *result) string {
	if res.TokensSource == "" {
		return "-"
	}
	return fmt.Sprintf("%d (%s)", res.CompletionTokens, res.TokensSource)
}

func secondsCell(ok bool, d time.Duration) string {
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%.2f", d.Seconds())
}

func answerMarkdown(cfg *config, j job, res *result, row smokeRow) string {
	display, tag := langDisplay(j.lang)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: %s (%s snippet)\n\n", j.endpoint, display, j.size)
	fmt.Fprintf(&b, "| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| verdict | **%s** %s |\n", row.Verdict, strings.Join(row.Notes, "; "))
	fmt.Fprintf(&b, "| request | `POST /v1/tasks/%s` model `%s`, stream %t, attempts %d |\n", j.endpoint, cfg.model, cfg.stream, res.Attempts)
	fmt.Fprintf(&b, "| status | %d %s |\n", res.Status, res.Class)
	fmt.Fprintf(&b, "| finish_reason | %s |\n", dash(res.FinishReason))
	fmt.Fprintf(&b, "| tokens | prompt %d, completion %s |\n", res.PromptTokens, tokensCell(res))
	fmt.Fprintf(&b, "| TTFC / E2E | %s s / %.2f s |\n\n", secondsCell(res.hasTTFC, res.ttfc), res.e2e.Seconds())
	fence := fenceFor(j.sn.code)
	fmt.Fprintf(&b, "## Input: code\n\n%s%s\n%s", fence, tag, j.sn.code)
	if !strings.HasSuffix(j.sn.code, "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%s\n\n", fence)
	if j.endpoint == "fix" {
		ef := fenceFor(j.sn.errText)
		fmt.Fprintf(&b, "## Input: error output\n\n%stext\n%s", ef, j.sn.errText)
		if !strings.HasSuffix(j.sn.errText, "\n") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s\n\n", ef)
	}
	b.WriteString("## Answer\n\n")
	if strings.TrimSpace(res.content) == "" {
		b.WriteString("_(no content)_\n")
	} else {
		b.WriteString(res.content)
		if !strings.HasSuffix(res.content, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func indexMarkdown(cfg *config, started time.Time, results []result, rows []smokeRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Smoke run %s\n\n", started.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Target `%s`, model `%s`, stream %t. One request per task and language; judge the answers by hand.\n\n", cfg.baseURL, cfg.model, cfg.stream)
	b.WriteString("| task | language | verdict | finish | tokens | TTFC s | E2E s | notes |\n|---|---|---|---|---|---|---|---|\n")
	for i, row := range rows {
		res := &results[i]
		fmt.Fprintf(&b, "| [%s](%s) | %s | %s | %s | %s | %s | %.1f | %s |\n", row.Task, row.File, row.Lang, row.Verdict,
			dash(res.FinishReason), tokensCell(res), secondsCell(res.hasTTFC, res.ttfc), res.e2e.Seconds(),
			strings.ReplaceAll(strings.Join(row.Notes, "; "), "|", "/"))
	}
	return b.String()
}
