# Code review: gateway (independent, 2026-09-30)

> **Legacy (2026-10-15).** Part of the Go gateway and the cloud plan, written before the mentor's "only local" decision on 2026-10-07. The gateway is not part of the lab; files mentioned here that no longer exist (cloud handoff and research, key scripts, `cmd/loadtest`) are at git tag `cloud-final`. Current plan: [PRD](../PRD.md).

Scope: all Go code under `cmd/gateway` and `internal/`. I skimmed `scripts/` (mint/admin wrappers, `report.sql`); I did not review `cmd/loadtest` in depth.
Spec basis: `docs/PRD.md`, `api/openapi.yaml`, `README.md`, and `docs/DECISIONS.md` (binding). Where DECISIONS or an ADR already settles something, I say so. I flag a decision itself only where I think it is wrong, and label it as such.

**Verdict:** no critical findings. The streaming relay, admission and accounting paths hold up under every failure I could reproduce. There is **1 major** finding, a lab-day UX bug that is easy to hit with the default `openai` client. It is a small fix. The rest are minor or nits.

## How this was verified

- `go test -race -count=3 ./...`: all green. `GOMAXPROCS=1 go test -race -count=8` on `internal/api`, `internal/relay`, `internal/upstream`, `cmd/gateway`: all green, no flakes.
- `go vet ./...` and `staticcheck -checks all ./...` (ST1000/ST1003 style checks excluded): no findings.
- **Black-box runs.** I ran the built gateway binary against a scriptable fake upstream I wrote under the scratchpad (not in the repo), with curl and the installed `openai` Python client:

| Scenario | Result |
|---|---|
| 5 sequential streams / 5 non-streams | 1 upstream TCP connection reused for all ✔ |
| Client drops mid-stream | upstream request cancelled, usage row `499` with estimated tokens, per-key slot free for the next request ✔ |
| Upstream silent after 1 chunk (idle 2 s) | error event after 2.0 s, HTTP 200 stream, row `504 upstream_timeout` ✔ |
| Upstream socket closed mid-stream | generic error event, row `502` ✔. `openai` SDK raises `APIError` ✔ |
| Upstream 500 / upstream down | `502 upstream_error` / `502 upstream_unavailable`, generic text, nothing leaked ✔ |
| Queue: 2 in flight, queue 1, 4 clients | 2 × 200, 1 immediate 503, 1 queued then 503 at 3 s, `Retry-After: 10` ✔ |
| 118 concurrent streams, 40 keys, server knobs (30 in flight + 24 queued) | 54 × 200, 47 × 503, 15 × 429, **0** SQLite lock/busy errors, one usage row per authenticated request ✔ (2 × 401 were my harness gluing two keys together) |
| `include_usage` via SDK | usage chunk delivered ✔ |
| Upstream 200 with HTML / with `{"error":…}` (non-stream) | relayed verbatim as 200 ✘ (m1) |
| SIGTERM with a stream longer than `LGAI_SHUTDOWN_TIMEOUT` | stream cut, `usage insert failed: sql: database is closed` ✘ (m2) |
| Second SDK call while the first stream runs (`RPM=6, burst=3, KEY_MAX_INFLIGHT=1`) | misleading rate-limit error, then lockout ✘ (M1) |

---

## Critical

None.

## Major

### M1. Rejected requests burn rate-limit tokens, and the default SDK retries turn one extra call into a lockout with a misleading error

`internal/api/generate.go:84` takes a rate token (`s.rate.Allow`) **before** the per-key in-flight check (`:103`) and the global gate (`:113`). A request rejected there never runs, but its token is gone. The `openai` client retries 429 and 5xx twice by default (`max_retries=2`) and honours `Retry-After`. The concurrency 429 says `Retry-After: 1` and the overload 503 says `10`, so every rejected call silently costs up to 3 tokens.

**Reproduced** with `LGAI_RATE_RPM=6`, `LGAI_RATE_BURST=3`, `LGAI_KEY_MAX_INFLIGHT=1` (laptop profile):
1. Start one stream with key A.
2. While it runs, make one more SDK call with key A. After 2.0 s it raises `RateLimitError: rate_limit_exceeded` ("rate limit of 6 requests/min exceeded"), although the student sent only 2 requests: 429 concurrency, then a retry that got another 429 concurrency, then a retry that got 429 rate limit.
3. After the first stream has finished, the next request is **still 429 for 7 s**.

The same drain happens under overload: 3 attempts that each get 503 empty the bucket, so when capacity returns the student is rate-limited as well.

This is easy to trigger on lab day: re-running a Jupyter cell while an earlier stream is still open, two notebooks, or a thread pool. The error text sends students and TAs hunting for a limit they did not hit.

**Fix (≈10 lines):** charge the rate limit only for requests that can actually run, and refund it when the gate refuses.
```go
// admit(): quota → per-key in-flight → rate → gate
releaseKey, ok := s.perKey.TryAcquire(p.KeyID, p.MaxInflight)   // before rate.Allow
...
if allowed, wait := s.rate.Allow(p.KeyID, p.RPM, p.Burst); !allowed { releaseKey(); ... }
releaseSlot, waited, err := s.gate.Acquire(ctx)
if err != nil { releaseKey(); s.rate.Refund(p.KeyID, p.Burst); ... }
// limits/rate.go
func (l *RateLimiter) Refund(keyID int64, burst int) {
	l.mu.Lock(); defer l.mu.Unlock()
	if b, ok := l.buckets[keyID]; ok { b.tokens = math.Min(float64(max(burst, 1)), b.tokens+1) }
}
```
Add a test: a 429 concurrency response followed by a request after release must not be rate-limited.

---

## Minor

### m1. Non-streaming relay trusts any upstream 2xx body

**Where:** `internal/relay/json.go:37` (`_ = json.Unmarshal(body, &peek)`) and `:51-55` (copies the upstream `Content-Type`).

**Reproduced.** For non-streaming requests, an upstream 200 with a non-JSON body (e.g. an HTML error page from a proxy in front of the GPU) is relayed as `200 text/html`. An upstream 200 carrying `{"error":…}` is relayed as 200 too. Both are recorded as `status 200` with estimated usage. The SDK then returns an object with `choices=None`, so student code crashes with `TypeError: 'NoneType' object is not subscriptable` instead of a clear API error.

The streaming path already treats both cases as upstream errors, so the two paths are inconsistent. The likelihood is low with Ollama/vLLM, and higher with a hosted proxy in the path (Modal).

**Fix:** if the body is not a JSON object with a non-empty `choices` array, or it has an `error` key, write `502 upstream_error` with the generic message and record it. Always send `Content-Type: application/json`.

### m2. Usage rows are lost when shutdown has to force-close streams

**Where:** `cmd/gateway/main.go:140-149`, with the deferred `st.Close()` at `:78-82`.

**Reproduced.** After `Shutdown` times out, `Close()` returns at once, `run` returns, and the DB closes while handlers are still unwinding. The log shows `usage insert failed … sql: database is closed`. The client gets a truncated stream: no error event, no `[DONE]` (curl exit 18). The openai SDK surfaces this as a connection error.

This affects every runbook L1 "container restart" during a busy lab. The runbook's slides numbers will miss exactly the requests that were cut.

**Fix:** track in-flight generation calls in `api.Server` (a `sync.WaitGroup` incremented in `newCall`, marked done at the end of `finish`). Expose `Wait(ctx)`. In `main`, after the forced `Close()`, wait up to ~3 s before closing the DB.

Optional: give `http.Server` a `BaseContext` that is cancelled on force-close. Streams would then end through the normal 499 path, and the client would see a clean end rather than a reset.

### m3. The upstream's real status is dropped from logs

**Where:** `internal/upstream/client.go:172-194` and `internal/api/generate.go:152-154`.

**Reproduced.** Upstream 401, 403, 404 and 500 all log as `upstream request failed status=502 code=upstream_error`. On day 1 with Modal (proxy auth plus vLLM `--api-key`), "wrong key", "wrong path/model" and "server crashed" look identical in the request logs. `/readyz` and the startup check do log the health status, but only for the health URL.

**Fix:** add an unexported-to-clients field (e.g. `oai.Error.UpstreamStatus int`, never serialized) and log it next to `status`. Five lines.

### m4. `review` truncation (lead's observation b): mostly tuning, but the README steers students into the capped path

This is not a gateway bug; the clamp logic is correct. Two things make it likelier than it needs to be:
- **The README's own task example is non-streaming** (`README.md:93`, `client.post("/tasks/review", …)` without `stream`). The effective budget is `min(768 review default, LGAI_NONSTREAM_MAX_TOKENS)` = 768 on both profiles. So the documented usage is exactly the configuration that truncated.
- **The prompt asks for a lot per finding:** location, problem, why, and a fix snippet, over three severity levels. The tasks also force `temperature` 0.1/0.2 (`internal/tasks/tasks.go:50-55`, `handlers.go:74-77`), which overrides any `LGAI_MODEL_DEFAULTS` temperature. Small models at low temperature often loop. First check the five truncated answers: a repeating tail means looping, not verbosity. In that case the fix is a penalty or a bigger model, not more tokens.

**Suggested:**
- Set the review default to 1024.
- Bound the prompt: "at most 5 findings, most severe first; fix snippets ≤ 6 lines; no style nits unless there are no bugs".
- Make the README task example `stream: True`.
- Judge on the lab model, not on the 1.5B. With `gpt-oss` on the laptop, reasoning tokens come out of the same `max_tokens` budget, so truncation will be worse there.

### m5. (Decision, labelled) `LGAI_CHAT_MAX_INPUT_BYTES=24576` can exceed an 8K context

Code tokenizes at roughly 3-3.5 bytes per token, so 24 KiB is about 7-8K tokens before `max_tokens` (up to 1536 on the server). If vLLM runs at 8K, a near-cap chat returns a clean `400 context_length_exceeded`, which is fine and mapped well. Ollama at `OLLAMA_CONTEXT_LENGTH=8192` silently truncates the prompt and answers the wrong question.

Tasks are fine: 16 KiB + 1024 fits. **Fix:** lower the chat cap to about 16-20 KiB, or run 16K context. Either way, record the max-model-len in D6.

### m6. A panic after admission is recorded as 200

**Where:** `internal/api/generate.go:35` sets `row.Status` to 200 up front, so a panic in `run`/`relay` records a success row. Also, `recoverer` (`internal/api/middleware.go:159-174`) writes a JSON 500 into an already-started SSE stream (`superfluous WriteHeader` plus JSON appended to the event stream).

The slots are correctly released by the defers.

**Fix:** initialise `Status: http.StatusInternalServerError, ErrorCode: "internal_error"`; every normal path already overwrites it. In `recoverer`, skip the write when the status was already sent (check `statusWriter.status != 0`).

### m7. Maintenance mode is in-memory only

**Where:** `internal/api/server.go:45`, `admin.go:254`.

A restart silently turns maintenance mode off. This includes `restart: unless-stopped` after a crash, and runbook L1 during the mass-leak procedure (`maintenance on → revoke → mint → off`).

**Fix:** persist it (a one-row table, or `PRAGMA user_version`-style key/value) and read it at startup. Or, at the least, log `maintenance=false` at startup and note it in the runbook.

---

## Nits

- **n1.** Quota check uses the request context (`generate.go:91-95`). A client hanging up during that query is logged as `quota check failed` and recorded as `500 internal_error` instead of 499. Use `ctx.Err() != nil → 499`.
- **n2.** `LGAI_UPSTREAM_BASE_URL` without `/v1` produces 404 for everything. The startup warning says "upstream not reachable at startup" (`main.go:191`), which is misleading, since it was reachable. Warn specifically when `/models` returns 404, or when the base path does not end in `/v1`.
- **n3.** Minting a second batch with the same `name_prefix` creates duplicate names (`admin.go:127-132`; `name` has no UNIQUE). The runbook correctly uses `lab2-`. Consider rejecting a prefix that already has active keys.
- **n4.** `GET /admin/keys/{id}/usage?since=2026-10-14T14:00:00+02:00`: an unescaped `+` becomes a space and gives a 400 (`admin.go:219-220`). Mention `Z` or `%2B` in the docs, or accept a space before the offset.
- **n5.** After `[DONE]` the relay returns without reading the body to EOF (`relay/stream.go:61-62`, `generate.go:158`). Upstream connection reuse then depends on the terminating chunk arriving in the same read. It did with my fake upstream (1 connection for 5 streams). With vLLM behind Modal TLS, a miss costs a handshake per request. Optional: bounded drain, e.g. up to 4 KiB with a 100 ms timer.
- **n6.** Dead or test-only code in production types: `relay.Outcome.BytesOut` (computed, only read by a test; the access log uses `statusWriter.bytes`), `Server.authRoutes` and the exported `Server.SetMaintenance` (tests only).
- **n7.** `ttft_ms` is measured from request arrival, so it includes queue wait (`relay.Options.Start`). That is the right user-facing number, but `report.sql` prints it as "TTFT" next to `queue_ms`. Label it "TTFT incl. queue" on Giulia's slides.
- **n8.** Lead's observation (a): the `model` field is the upstream id. This is **deliberate** (ADR 0003 §Consequences, and the openapi examples show `gpt-oss:20b`). The SDK does not care. I'd keep it; rewriting would mean re-encoding every chunk. Add one README line ("responses name the underlying model, e.g. `qwen2.5-coder:1.5b`; always send `coder`") so students don't copy the upstream id into `model` and get 404.

---

## What's good

- **The SSE relay is careful.**
  - It handles event-at-a-time framing with a bounded line reader, CRLF, multi-line `data:`, comments and EOF without a trailing blank line.
  - It flushes after every event and sets a per-write deadline that it always clears, so there is no stale deadline on keep-alive.
  - Idle watchdog, total timeout and client-gone are separated by context *cause*, so 499/502/504 are classified correctly. I reproduced all three.
  - The generic error event is exactly what `openai-python` turns into `APIError`.
- **Admission accounting is correct on every exit path.** Idempotent releases, `defer release()` in one place, cancellation-aware queue waits, and a buffered-channel gate that hands a freed slot to the oldest waiter (no barging). It is synctest-tested. The 118-request burst admitted exactly 30 + 24.
- **Usage rows cover every authenticated outcome**, with 499 and estimated counts. The insert uses a detached context with a timeout, so a vanished client can't lose its row.
- **SQLite setup is right for the scale:** one connection, WAL, `busy_timeout`, `_txlock=immediate`, 0600 files, indexed quota query, idempotent schema, and a refusal to run on a newer schema.
- **Security posture:**
  - Typed allowlist re-encode.
  - No client headers upstream, no redirects.
  - Generic upstream errors.
  - Auth before the body is read, `Connection: close` on errors with unread bodies.
  - Loopback-by-default admin listener.
  - Secret validation, redacted config logging.
  - Fail-fast config that reports **all** errors at once and names each variable.
- **Tests assert behaviour, not coverage.** A fake upstream with realistic failure modes, end-to-end relay tests, a slow-reader test that proves slot release, `testing/synctest` for time-dependent gate logic, and security tests that grep logs and DB for secrets and content. They were stable across 11 race runs, including under `GOMAXPROCS=1`.
- **Proportionate code for a 2-week project.** Small packages with clear ownership, and comments that explain *why* (deadline hygiene, cause taxonomy). DECISIONS D4 cuts were actually applied rather than half-built.

---

# D9 review (2026-10-01)

Scope: the D9 response formats only:
- `internal/render`
- the `Sink` refactor in `internal/relay` (`sinks.go`, `stream.go`)
- the wiring in `internal/api/output.go`, `generate.go` and `handlers.go`

The four builder deviations in the brief are accepted by the lead, so I do not report them.

**Verdict:** no critical findings, **1 major**, 4 minor, 7 nits. The refactor kept the full format intact: byte-identical in both modes. Cancel, the idle watchdog, write deadlines and usage rows work on every sink. The filename and header-injection guards hold. The real gap is that a truncated answer looks complete in every new format.

## How this was verified

- `go test -race -count=3 ./...` green.
- `GOMAXPROCS=1 go test -race -count=6` on the new tests: green, no flakes.
- `go vet` and `staticcheck` clean.
- Unit probes of `render.Negotiate` / `CodeFile` / `Disposition` in a **copy** of the module under the scratchpad.
- Black-box runs of the built gateway against a scripted fake upstream (exact SSE bytes, `finish_reason`, and stall / die / error / flood modes), with curl and raw sockets.

| Check | Result |
|---|---|
| Full SSE with `include_usage` vs the upstream bytes | `cmp` identical ✔ |
| Full SSE without `include_usage` | identical minus the usage chunk ✔ |
| Full non-stream JSON | identical body ✔ (+ `Content-Disposition`, `Vary`) |
| `text/plain` live, deltas 400 ms apart | one chunk per delta at 0.8 / 1.2 / 1.6 / 2.0 s ✔ |
| Client cancel at 1 s on text / code / report | upstream cancelled, row `499`, estimated tokens ✔ |
| Upstream stalls (idle 2 s) | text: 200 + `[error: …]` at 2.0 s. code: JSON **504**. Rows 504 ✔ |
| Upstream dies (code format) | JSON 502, row 502 ✔ |
| Slow reader on a live text stream (`WRITE_TIMEOUT=1s`) | gave up after 1.05 s, row 499; the next request on the same key (cap 1) got 200 ✔ |
| 406 and text/markdown responses | `nosniff`, CSP, `no-store`, `Vary: Accept` all present; 406 is a JSON envelope; no upstream call, no row ✔ |
| Content in `gw.log` (prompts, answers, upstream error text) | 0 hits ✔ |
| Model answer `public class Evil"; filename="x.sh`, `Ünïcode`, `A\r\nB` | filenames `Evil.java` and `A.java`. The class regex and `Disposition()`'s ASCII allowlist both strip; injection is impossible ✔ |
| Path traversal | every part is server-built (`id8` is hex or a hash of `../../etc`; class names allow no `.` or `/`) ✔ |
| Answer cut at `max_tokens` (`finish_reason: length`) | ✘ D9-M1 |
| Upstream ignoring `max_tokens` (240 MB of deltas, code format) | ✘ D9-m1: peak RSS 20 MB → **1.2 GB** |

## Major

### D9-M1. Truncated answers look complete in text, report and code

`finish_reason` is never read. `oai.ChunkPeek` (`internal/oai/types.go`) has no such field, and `relay.Outcome` carries none. So:
- `Live.Finish` (`internal/relay/sinks.go:98-107`) and the report footer (`render.Foot`) end a cut-off answer exactly like a finished one.
- `render.Extract` deliberately takes an unterminated last fence "to the end" (`internal/render/fence.go:49-51`).

**Reproduced** with `POST /v1/tasks/tests` (python), `Accept: text/x-code`, and an upstream answer cut mid-statement with `finish_reason: "length"`:
- response: `200`, `X-LGAI-Code-Blocks: 1`, `filename="test_….py"`;
- the body ends in `assert add(-1`, and `python -c 'import ast; ast.parse(...)'` fails with `SyntaxError: '(' was never closed`;
- text and report formats: also 200, no hint; the report footer says only `Tokens: 7 prompt + 1 completion`.

Yesterday the lead saw `review` truncate 5/5 answers on the small model, so this will happen on lab day. The code format exists to be "saved as a file and run", so here it hands students a broken file with every success signal.

**Fix (≈25 lines):**
1. Add `FinishReason *string` to `ChunkPeek.Choices[]` and keep the last non-null value in `streamState`. Expose it as `Outcome.FinishReason`.
2. On `"length"`:
   - **text:** append `\n\n[truncated: the answer reached max_tokens; ask for less or raise max_tokens]\n`;
   - **report:** add `Finish: length (truncated)` to the footer;
   - **code:** set `X-LGAI-Finish-Reason: length` and append one comment line in the language's syntax (`# [truncated …]` for Python, `// [truncated …]` for the others). The compiler or pytest error then points at the cause.
3. Add one end-to-end test per format with the fake upstream sending `finish_reason: "length"`.

## Minor

### D9-m1. The `Buffered` sink has no byte cap

`Buffered.Event` (`internal/relay/sinks.go:141-144`) appends every delta to a `strings.Builder`. The only bound is the upstream honouring `max_tokens` (the request's clamp) plus `LGAI_UPSTREAM_TIMEOUT`. The non-streaming JSON path is capped at 8 MiB; this path is not. `CodeFile` then makes further copies: `Split`, the block builders, and the `Join`.

**Reproduced** with an upstream that ignores `max_tokens: 16`: an 80 MB answer took peak RSS from 20 MB to 479 MB; a 240 MB answer took it to 1.2 GB. Both returned 200, and the row recorded `completion_tokens=60000`. Ollama and vLLM do honour `max_tokens`, so this needs a buggy or misconfigured upstream. But one such request per concurrent student would OOM the gateway.

**Fix:** a cap in `Buffered.Event` (1 MiB is ~100× any `LGAI_MAX_TOKENS_CAP` answer) that returns a distinct `errAnswerTooLarge`. In `flushEvent` (`internal/relay/stream.go:159`), treat that error as an upstream failure (`s.reason = "answer too large"`, **not** `gone`), so `Fail` sends a JSON 502 instead of recording a 499. Optionally count bytes for `Live` too.

### D9-m2. A mid-stream failure in live formats does not close the connection

`Live.Fail` (`internal/relay/sinks.go:110-112`) appends the `[error: …]` line. The handler then returns normally, so net/http sends the chunked terminator and keeps the connection alive.

**Reproduced** (raw socket): `…[error: …]\n` arrives, then `0\r\n\r\n`, then the socket stays open. curl exits **0**, and `curl -fOJ … && pytest` cannot tell this from success. D9 says "append one line … then close".

**Fix:** after recording the row, abort the response so the terminator is never sent; curl then exits 18 and wget retries. Two ways:
- **Option 1:** `http.NewResponseController(w).Hijack()` + `Close()` for HTTP/1.1.
- **Option 2:** `panic(http.ErrAbortHandler)` from `call.finish`, after `InsertUsage`. This also works behind an HTTP/2 proxy, but `accessLog` must then log in a `defer` (today it would drop the line).

### D9-m3. (Decision, labelled) Code format concatenates blocks of every language into one file

D9 says "concatenate all blocks", and `CodeFile` (`internal/render/filename.go:87-99`) does. Models often add a run command or a build snippet after the code. Reproduced:
- **tests/python:** a `pytest -q` block lands at module level in `test_….py` (`NameError` at collection).
- **tests/java:** an ```` ```xml ```` Maven `<dependency>` block lands in `CalcTest.java`, which won't compile.
- **chat:** when the first block is ```` ```bash ```` (`pip install …`), a Python answer is saved as `.txt` / `text/plain`.

**Fix:**
- **Tasks:** keep blocks whose info string maps to the task language, or is empty. Use all blocks only if none match.
- **Chat:** choose the language of the first block with a known language and keep that language's blocks (plus untagged ones).
- `X-LGAI-Code-Blocks` should count the kept blocks.

### D9-m4. Test gaps for the new surface

The new tests are behavioural and stable:
- byte comparison against the upstream;
- headers per format;
- the flush sequence in `TestLiveSink`;
- mid-stream errors per format;
- 406 without an upstream call or a row.

Missing, all of which I reproduced by hand:
- client cancel and the idle watchdog on `Live` and `Buffered`;
- the write deadline with a slow reader on `Live`;
- truncation (D9-M1);
- the buffer cap (D9-m1);
- Accept edge cases: `q=0` on a wildcard, case and whitespace in params, a 16 KiB header.

These are cheap to add with the existing `testupstream` behaviours (`StallAfter`, `ChunkDelay`, `Chunks`).

## Nits

- **D9-n1. Tab-indented fences keep their tabs.** `dedent` strips only spaces, but `indent` counts tabs too (`internal/render/fence.go:56-62`). ```` \t```python ```` inside a list therefore yields `\tdef f():`, and Python raises `IndentationError`. Fix: strip the opening line's exact whitespace prefix.
- **D9-n2. `~~~` fences are not recognised** (0 blocks → `.txt`). They are CommonMark-valid and rare from these models; D9 says backticks, so this is a docs line.
- **D9-n3. Wildcards override a more specific `q=0`.** `Accept: text/*;q=0.5, text/plain;q=0` → full, and a non-stream full answer is `application/json`, which doesn't match `text/*` (`negotiate.go:33-37`). It is harmless (RFC 9110 allows ignoring Accept). Note it in the README rather than implementing specificity.
- **D9-n4. The drain runs before `sink.Finish`** (`internal/relay/stream.go:71-76`). The footer, the chunked terminator and the whole `Buffered` response wait for the upstream EOF, up to 100 ms. Call `end()` first, then `drain()`.
- **D9-n5. `Live` commits `200` at `Start`**, before the first upstream event (`sinks.go:83-92`). If the upstream fails before any token (idle during a long prompt prefill), curl gets `200` + `[error]` instead of a JSON 502/504 that `curl -f` would catch. Option: delay `Begin` and `Head` until the first non-empty text or `[DONE]`.
- **D9-n6. Windows reserved device names pass the class allowlist.** `public class CON` → `CON.java`. With `curl -OJ` on Windows this writes to the console device. Add `CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9]` (case-insensitive) to the fallback.
- **D9-n7. Other content is dropped silently.** Tool-call-only chat answers, and reasoning-only answers (gpt-oss spending `max_tokens` on reasoning), give an empty text body or an empty `.txt` with `X-LGAI-Code-Blocks: 0`. D9-M1's finish-reason line would cover the second case.

## What's good (D9)

- **The `Sink` interface is the right seam.** Framing, usage, TTFT, the idle watchdog, cancel causes and failure classification stay in one place (`Stream`). The sinks only format, and the full format is byte-identical.
- **Filenames are safe by construction:** server parts, a strict class regex, then an ASCII allowlist in `Disposition`. `id8` hashes a non-hex client request id rather than echoing it.
- **Errors are clean.** Every error before the response starts is a JSON envelope with the right status. Buffered formats turn upstream failures into a normal 502/504, and upstream error text never reaches the client or the log.
- **`Accept` parsing is small and lenient.** It handles q-values, case, whitespace, params, invalid q (ignored), real-world headers (browser, axios, Java `HttpURLConnection`, a 16 KiB list), and gives 406 only when nothing supported is acceptable.
- **The new tests assert bytes and headers, not coverage, and are stable** under `-race` and `GOMAXPROCS=1`.
