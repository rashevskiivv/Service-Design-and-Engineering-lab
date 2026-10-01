# Decisions after review (wave 2), binding for implementation

Date: 2026-09-30.
Inputs:
- `reviews/critic.md`
- `reviews/security.md`
- Claims re-checked at the source by the lead (see §7)

**Precedence when documents disagree:** this file > `reviews/security.md` §4–§5 > `ARCHITECTURE.md` > `research/performance.md` > other research.

---

## D1. Cloud (critic B2)

- **Primary: Modal**, on an **L40S** for dev, rehearsal and lab.
  - Set a Workspace budget of **$28** and a spend limit of **$0** on day 1. Modal docs: "Modal stops workloads that would incur additional out-of-pocket charges."
  - Keep a ledger and hold a $12 reserve for the lab.
  - One GPU container only (`max_containers=1`).
  - Keep warm only during the lab window.
  - Weights cached on a Modal Volume.
  - Provider proxy auth + vLLM `--api-key`.
  - Apply for Modal academic credits in parallel.
- **OVHcloud is dropped.**
  - Its trial auto-bills the card after the credit runs out: "Once this has been used up, you will be billed automatically".
  - The trial page doesn't name AI Deploy. That violates PRD §3 unless Slava explicitly amends the PRD.
- **Parallel backup:** GCP Cloud Run GPU with a spend cap, plus the professor email (course credits / UniTrento GPU).
- **Modal cuts web requests at 150 s** (web function request timeout); the docs don't say whether streaming is exempt.
  - Until the day-1 test proves streams survive past 150 s, the server profile uses `LGAI_MAX_TOKENS_CAP=1536`.
  - Non-streaming requests are clamped to 1024 tokens (D5).

## D2. Topology (critic B1). Host choice pending Slava + Jan; the code supports both

- **Preferred:** gateway + SQLite on a small always-on CPU host with a stable HTTPS name (Jan's proxy/TLS). The GPU endpoint is only the upstream.
  - A fallback becomes an env change on that host.
  - The admin listener stays on localhost, reached over SSH.
- **Also supported:** gateway in the same container as vLLM (single exposed port, possibly ephemeral disk). This needs:
  - `LGAI_ADMIN_ON_PUBLIC=true` (explicit opt-in, WARN at startup);
  - `LGAI_KEYS_FILE`: pre-minted key **hashes** imported at startup, so keys survive restarts.

## D3. Model (critic M1, M7)

- **Lab:** `Qwen/Qwen3.6-35B-A3B-FP8` on L40S, thinking off, `--language-model-only`.
  - Conditional on the day-1 checklist: ≥ 30 sequences at 8K, no reasoning deltas, 30 parallel streams for 10 min without errors.
- **Pre-decided fallback on the same GPU:** `Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8`.
- **Laptop:** `gpt-oss:20b` with `reasoning_effort: low` via `LGAI_MODEL_DEFAULTS`. A/B against `qwen2.5-coder:7b` on time-to-first-content.
- **Clients always use `model: "coder"`**, an alias mapped to the upstream id.
- Szymon judges the 20 task×language smoke answers and picks the winner.

## D4. Gateway scope cuts (critic M6)

| Cut from ARCHITECTURE | Build instead |
|---|---|
| Strict-FIFO hand-off gate | Buffered-channel semaphore + atomic waiting counter (bounded) + `select` with a timer / ctx |
| Two DB pools | One `*sql.DB`, `SetMaxOpenConns(1)`, WAL, `busy_timeout` |
| `/admin/usage` global endpoint | `GET /admin/keys/{id}/usage` + `scripts/report.sql` for Giulia's numbers |
| Six-step shutdown | `srv.Shutdown(ctx 30s)` for both listeners, then `db.Close()` |
| Mid-stream cause taxonomy | One generic mid-stream SSE error event + the idle watchdog |
| Big load-tester spec | The minimal spec in D8 |

## D5. Gateway additions and changes (critic + security)

**MUST** (security §4 #1–#14, all of them):
- #1: admin API on its own listener, `LGAI_ADMIN_ADDR` default `127.0.0.1:8081`. No token → no admin listener. The public router has no `/admin`. `LGAI_ADMIN_ON_PUBLIC=true` opt-in mounts it on the public listener with a WARN.
- #2: secret validation (≥ 43 chars, ≥ 16 distinct, reject placeholder words). Example env files ship with empty secrets.
- #3–#7: typed request allowlist, re-encoded; exactly security §5 (tables 5.1, 5.2, 5.3, 5.5, 5.6).
  - `chat_template_kwargs` accepts only `{enable_thinking: bool}`.
  - `response_format` is text/json_object only; `tool_choice` is none/auto only; `logprobs` is dropped.
  - Non-text content parts → 400.
  - Chat input cap `LGAI_CHAT_MAX_INPUT_BYTES=16384` (was 24576; see the D6 note); `messages` ≤ 128; JSON depth ≤ 32; `LGAI_MAX_BODY_BYTES=262144`.
- #8: `LGAI_UPSTREAM_HEADERS` (secret, `Name=value;Name2=value2`, redacted). Refuse to start if the upstream is non-loopback/non-private and has no auth, unless `LGAI_UPSTREAM_ALLOW_UNAUTHENTICATED=true`.
- #9: the upstream client doesn't follow redirects (3xx → 502), forwards only the allowed headers (never the client's `Authorization`), copies back only `Content-Type`.
- #10: `.gitignore` / `.dockerignore`.
- #11: no content logging.
- #14: keys only from `Authorization: Bearer`.

**Also build:**
- `LGAI_MODEL_DEFAULTS` (critic M3): per-alias JSON of fields merged into the upstream body unless the client set them. Only allowlisted fields: `reasoning_effort`, `chat_template_kwargs.enable_thinking`, `temperature`, `top_p`, `top_k`, `presence_penalty`.
  - Example: `coder={"reasoning_effort":"low"}`.
- **Timeouts (critic M4, security #18):**
  - the first-byte / response-header timeout applies to **streaming** only;
  - non-streaming relies on the overall `LGAI_UPSTREAM_TIMEOUT`;
  - non-streaming `max_tokens` is clamped to `LGAI_NONSTREAM_MAX_TOKENS` (default 1024).
- **Lab ops (security #25, critic m3):**
  - `POST /admin/keys/batch {count, name_prefix, expires_at?}` returns the plaintext keys once.
  - `POST /admin/keys/revoke {name_prefix}` or `{all:true}`.
  - `PUT /admin/maintenance {enabled}`: generation returns 503 `maintenance` with `Retry-After`; health, models and admin keep working.
  - `scripts/mint-keys.sh` wraps batch → `keys.csv` (0600) + printable slips.
- **Security SHOULDs:**
  - #15: key `expires_at` (+ `LGAI_KEY_TTL`, default 0 = none).
  - #16: security headers, no CORS.
  - #17: connection cap `LGAI_MAX_CONNS=256`.
  - #19: `framework` goes in the user message, with a pattern.
  - #21: umask 077; unknown-`LGAI_*` warnings print names only.
  - #22: distroless static nonroot image, pinned.
  - #23: laptop binds loopback.
  - #24: generic upstream errors and `/readyz` (cached 2 s).
  - #26: `LGAI_KEYS_FILE`.
- **COULDs:** skipped for now.

## D6. One knob table (defaults; critic m1)

| Knob | Server (L40S) | Laptop (M4 Pro, Ollama) |
|---|---|---|
| `LGAI_MAX_INFLIGHT` | 30 (vLLM `--max-num-seqs 32`) | 4 (= `OLLAMA_NUM_PARALLEL`) |
| `LGAI_QUEUE_SIZE` | 24 | 16 |
| `LGAI_QUEUE_TIMEOUT` | 30s | 60s |
| `LGAI_OVERLOAD_RETRY_AFTER` (static) | 10s | 10s |
| `LGAI_RATE_RPM` / `LGAI_RATE_BURST` | 6 / 3 | 6 / 3 |
| `LGAI_KEY_MAX_INFLIGHT` | 2 | 1 |
| `LGAI_TOKEN_QUOTA_DAILY` | 300000 | 300000 |
| `LGAI_CHAT_DEFAULT_MAX_TOKENS` | 512 | 512 |
| `LGAI_MAX_TOKENS_CAP` | 1536 (2048 once the Modal stream test passes) | 1024 |
| `LGAI_NONSTREAM_MAX_TOKENS` | 1024 | 768 |
| Task defaults (explain/review/tests/fix) | 512 / 1024 / 1024 / 1024 | same, clamped by cap |
| `LGAI_UPSTREAM_HEADER_TIMEOUT` (stream first byte) | 60s | 120s (cold model load) |
| `LGAI_UPSTREAM_IDLE_TIMEOUT` (between chunks) | 30s | 60s |
| `LGAI_UPSTREAM_TIMEOUT` (whole request) | 5m | 10m |
| `LGAI_WRITE_TIMEOUT` (per write, via ResponseController) | 10s | 10s |
| `LGAI_BODY_READ_TIMEOUT` (per request body) | 10s | 10s |
| `LGAI_TASK_MAX_INPUT_BYTES` / `LGAI_CHAT_MAX_INPUT_BYTES` / `LGAI_MAX_BODY_BYTES` | 16384 / 16384 / 262144 | same |

Changed 2026-10-01 after the code reviews (`reviews/code-review.md` m4, m5; `reviews/security-code.md` V1):
- The chat input cap went from 24576 to 16384. Code is about 3.5 B/token, so 16 KiB is ≈ 4.7k tokens, and with `LGAI_MAX_TOKENS_CAP` (≤ 2048) a request fits an 8192-token context. At 24 KiB, Ollama would silently truncate the prompt.
- The `review` task default went from 768 to 1024, and its prompt now asks for at most 5 findings.
- `LGAI_BODY_READ_TIMEOUT` went from 30s to 10s (`reviews/security-code.md` V1). The key's in-flight slot is now taken before the body is read, and a shorter timeout bounds how long one slow body holds a connection.

## D7. Lab-day runbook

`docs/runbook-lab-day.md` covers:
- the L0–L4 ladder: spare keys, container restart, provider switch, laptop demo, video;
- triggers;
- 40 keys minted for 30 students;
- two people with admin + console access;
- rehearsals from campus Wi-Fi;
- freeze the day before.

## D8. Load tester (`cmd/loadtest`), minimal

**Flags:**
- `-url`, `-key` (or `LGAI_KEY` env)
- `-c` (concurrency), `-d` (duration) or `-n` (requests)
- `-endpoint chat|explain|review|tests|fix|mix`
- `-lang python|java|go|c|cpp|mix`, `-size small|medium|mix`
- `-stream` (default true), `-max-tokens`
- `-burst` (all c start at once, one request each)
- `-smoke` (1 request per task×language: 20 total; save answers to a dir for Szymon; fail on empty content)
- `-json out.json`, `-timeout`

**Corpus:** embedded, 5 languages × {small, medium} snippets + one error message per snippet for `fix`.

**Metrics:**
- TTFB (first SSE byte)
- **TTFC** (first non-empty `delta.content`)
- per-stream completion tok/s (usage chunk if present, else chunk count)
- aggregate tok/s
- end-to-end latency p50/p95/p99
- status histogram (200/401/429/503/5xx)
- `Retry-After` observed
- errors

**Output:** a table on stdout + optional JSON.

## 7. Claims re-checked by the lead today (primary sources)

- **OVH trial:** "US$ 200 free credits" (varies by country), one month from first project, card required, auto-billed after. AI Deploy not named. AI Deploy is billed by running time; stopping the app stops billing. https://www.ovhcloud.com/en/public-cloud/free-trial/ · https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-deploy-getting-started
- **Modal:** "$30 / month free compute". L40S $0.000542/s. "10 GPU concurrency". https://modal.com/pricing
- **Modal spend limit** stops workloads that would incur out-of-pocket charges; the Workspace budget is "the hard outer cap". https://modal.com/docs/guide/budgets
- **Modal:** "maximum HTTP request timeout of 150 seconds" for all web function types, then a 303 redirect; streaming is not addressed. https://modal.com/docs/guide/webhook-timeouts
- **Oracle Always Free A1** = 2 OCPU / 12 GB. https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm
- **`Qwen/Qwen3.6-35B-A3B`** and its `-FP8` variant exist on Hugging Face.
- **CVE-2025-62426** (vLLM `chat_template_kwargs` DoS) exists: GHSA-69j4-grxj-j64p.
