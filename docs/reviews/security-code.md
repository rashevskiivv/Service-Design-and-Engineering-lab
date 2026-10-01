# Security review of the implementation

| | |
|---|---|
| Reviewer | Security agent |
| Date | 2026-10-01 |
| Code | `/mnt/user-data/outputs/local-generative-ai` as of 2026-10-01, after the fixes from `reviews/code-review.md` |
| Baseline | [`security.md`](security.md) §4 (changes), §5 (allowlist), §6 (test checklist). Adopted set per `DECISIONS.md` D5: MUST #1–#14, SHOULD #15–#19, #21–#26 |
| How | Read every file under `cmd/gateway` and `internal/**`, plus the scripts, env examples, Dockerfile, compose file, README and `HANDOFF-jan.md`. Ran `go test -race ./...` (all pass). Ran the gateway built from this tree with the laptop settings and a `gen-secret` admin token, against a fake upstream (`/tmp`) that logs every raw body and header it receives. No repo file was changed apart from this one |
| Verdict | **Not yet safe to go public: fix V1 and V2 first.** Both let one ordinary student key degrade or stop the service for the whole class. With them fixed (plus Caddy `request_buffers`, Jan item J1), I consider it safe for the lab. The rest of the request path held up under attack: allowlist, smuggling, SSRF, upstream isolation, secrets and logging |

---

## 1. Confirmed vulnerabilities (ranked)

### V1 · High: one lab key can exhaust all connections with slow request bodies

**What happens.** Per-key limits run in `admit` (`internal/api/generate.go:94`, per-key in-flight; `:118`, rate). `admit` is called only after the whole body has been read and validated (`internal/api/handlers.go` `chat`/`task` → `readBody`, `internal/api/middleware.go:189`). Before that point nothing limits how many body reads one key can have open.

- Each slow body holds a connection for up to `LGAI_BODY_READ_TIMEOUT` (30 s, `internal/config/config.go:149`). The client then reconnects.
- `LGAI_MAX_CONNS` (`cmd/gateway/listener.go:18`, default 256) then stops accepting. Every other client hangs, `/healthz` included.

**Through Caddy.** Caddy streams request bodies to the upstream unless `request_buffers` is set ([Caddy reverse_proxy docs](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy): request bodies are buffered only with `request_buffers`). So each slow upload also holds one Caddy→gateway connection. **UNVERIFIED** end to end through Caddy; this is the expected behaviour from the docs.

**Repro** (gateway with `LGAI_MAX_CONNS=32`; key `lab-02` has the default `rpm 6`, `inflight 1`):

```python
import socket, time
socks = []
for _ in range(32):
    s = socket.create_connection(("127.0.0.1", 8080))
    s.sendall(b"POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer " + KEY_LAB02 +
              b"\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{")
    socks.append(s)
while True:                      # one byte per socket every 2 s; reconnect after the 30 s 408
    for s in socks: s.send(b" ")
    time.sleep(2)
```

**Observed:**

- Another key's `GET /v1/models` timed out after 8 s, both at 1 s and at 13 s into the attack.
- Unauthenticated `GET /healthz` timed out after 3 s.
- The gateway logged the 32 requests as `408` after 30 s.
- After the attacker closed its sockets, the next request returned 200 in 0 ms.

At the default cap of 256 this takes 256 sockets from one laptop.

**Fix:**

1. **Take the per-key in-flight slot before reading the body.** Move `s.perKey.TryAcquire` out of `admit` and into `chat`/`task`, right after authentication and before `readBody`. Hold the slot through validation and admission, and release it in `finish` or on refusal.
   - One key can then hold at most `LGAI_KEY_MAX_INFLIGHT` body reads (1–2). 40 keys × 2 = 80, which is below the 256 cap.
   - Keep a 429 `concurrency_limit_exceeded` for the extra ones.
2. **Shorten the body-read timeout.** Set `LGAI_BODY_READ_TIMEOUT` to `10s`: a 256 KiB body arrives in under 1 s on any real uplink.
3. **Buffer at the proxy (Jan, J1).** Add `request_buffers 256KB` to `reverse_proxy`, so slow uploads cost Caddy goroutines instead of gateway connections.
4. **Add a test.** One key opens 2 × `MAX_CONNS` slow bodies, and another key must still get a 200.

### V2 · High (grows with time): rejected requests are unthrottled and recorded, and the quota SUM then starves the single DB connection

**What happens:**

- **Not rate limited.** A request that fails validation (400), size (413) or the body-read timeout (408) is refused before `admit`, so it never touches the rate limiter or the per-key in-flight cap.
- **Always recorded.** It still writes a usage row: `finish` always calls `InsertUsage` (`internal/api/generate.go:74`).
- **Expensive admission order.** `admit` runs the quota check, `SELECT SUM(...) FROM usage WHERE key_id = ? AND ts >= ?` (`generate.go:102`), **before** the rate check (`generate.go:118`). So every request of that key, even one that ends in 429, scans all of the key's rows for the day.
- **One DB connection for everything.** All SQLite work goes through one connection (`internal/store/store.go:54`, `SetMaxOpenConns(1)`): every student's auth lookup, the quota checks and the usage inserts.

**Repro:** 16 threads, key `lab-03`, `POST /v1/chat/completions` with body `{}`.

| Measured | Value |
|---|---|
| Flood rate (one Python client) | 2,645 req/s, all 400, every one recorded |
| Rows after 75 s | 172,201; the DB grew to 16.5 MB (~96 B/row) |
| Attacker's real requests after that | ~76 ms each (200s and 429s alike) |
| Other keys' `GET /v1/models` during that | p50 61 ms (0.6 ms before the flood) |

**Extrapolation (UNVERIFIED beyond 172k rows; cost is linear in rows):**

- About 0.9 GB of database per hour of flooding, on a 10 GB VM disk.
- Seconds per quota SUM after an hour. Every student's authentication then queues behind it.
- Usage inserts have a 2 s timeout (`generate.go:72`), so they would start failing. Lost rows mean every key's quota is undercounted (inferred).

**Fix.** (a) and (c) are a few lines each and remove the amplification. (b) bounds CPU and bandwidth.

- **(a) No usage row before admission.** Don't write a usage row for requests refused before `admit` (400/413/408); log them only, as 401s already are. Or count them in an in-memory per-key counter if Giulia wants the number.
- **(b) Cap rejected requests per key.** Add a small per-key bucket for rejected requests (e.g. 30/min), checked right after auth. When it's empty, answer 429 without reading the body.
- **(c) Rate check before quota.** Order `admit` as per-key in-flight → rate → quota → gate, so a 429 never touches SQLite. The SDK does not retry a quota 429 (`X-Should-Retry: false`), so the review-M1 concern doesn't apply to that order.
- **(d) Optional: no per-request SUM.** Replace the SUM with an in-memory per-key daily counter, seeded once from SQLite and incremented on insert.
- **(e) Add a test.** 200 invalid requests from one key give at most `burst` + the error-bucket size of 400s, then 429. They write no usage rows.

### V3 · Low: `LGAI_UPSTREAM_HEADERS` accepts control characters other than CR, LF and NUL

**What happens.** `internal/config/parse.go:168` rejects only `\r`, `\n` and `\x00`.

**Repro.** `LGAI_UPSTREAM_HEADERS='Modal-Key=a\x01b'` (also `\x7f`) starts without error. Then every upstream request fails inside `net/http`'s header validation: `/readyz` returns 503 and every generation request gets 502. Nothing is injected (it fails closed), but a typo pasted from a password manager surfaces only at request time.

**Fix.** Reject every byte below 0x20 except HTAB, and 0x7f (the rule of `httpguts.ValidHeaderFieldValue`). Add those two cases to `TestUpstreamHeaders`.

### V4 · Low: the README admin example puts the admin token on a command line

**What happens.** `README.md:52-53` runs `curl ... -H "Authorization: Bearer $LGAI_ADMIN_TOKEN"`. While curl runs, the token is in its argv, readable by other local users through `ps` or `/proc/<pid>/cmdline`. People copy README snippets onto the VM.

**Scripts (task item 4).** The scripts never put the token on a command line or in a URL:

- `scripts/admin-lib.sh:43` passes the header through `-K <(printf …)`. `printf` is a bash builtin, so nothing is exec'd with the token.
- `admin-lib.sh:28-32` reads the token from the environment or from `LGAI_ADMIN_TOKEN_FILE`, never from an argument.
- `scripts/mint-keys.sh` and `scripts/revoke-prefix.sh` put only the prefix in the body.
- `docs/runbook-lab-day.md:14,16` uses `read -rs` and the same `-K <(printf …)` form.
- `docs/HANDOFF-jan.md:178` uses the same form for the Modal secrets.
- The load tester takes its key from `LGAI_KEY`. A `-key` flag exists but is documented as visible in `ps`.

**Fix.** Use the `-K <(printf 'header = "Authorization: Bearer %s"\n' "$LGAI_ADMIN_TOKEN")` form in the README too.

### V5 · Low (only when port 8080 is reachable without Caddy): idle keep-alive connections fill the connection cap

**Repro.** Open 32 connections, send `GET /healthz` on each, then leave them idle. Another key's request times out after 8 s. The connections are held for `LGAI_IDLE_TIMEOUT` (120 s, `config.go:151`). Partial headers are cut after 10 s by `ReadHeaderTimeout` (`TestSlowHeaders`).

**Scope.** Behind Caddy, client connections end at Caddy, so this never reaches the gateway. It matters for laptop mode on a shared LAN, or if a firewall rule exposes 8080.

**Fix.** Keep 8080 on loopback (already the default and in compose), and set `LGAI_IDLE_TIMEOUT=30s`.

### Notes, not vulnerabilities

- **Tool fields reach vLLM.** `tool_choice: "auto"` is forwarded even without `tools`. `tools[].function.parameters` is forwarded as compacted raw JSON, with duplicate keys and regex `pattern` values intact (verified).
  - This is safe as long as vLLM runs **without** `--enable-auto-tool-choice` and `--tool-call-parser`, as in `HANDOFF-jan.md` §6. vLLM then answers 400 to `"auto"` (UNVERIFIED), which the gateway relays as a 400.
  - Keep it that way (J7).
- **Duplicate JSON keys are last-wins**, in chat (`fields` map) and in tasks (`encoding/json`). Every value is still validated and the upstream body is rebuilt. So `{"n":2,"n":1}` is accepted with `n` dropped, and `{"type":"image_url","type":"text",…}` becomes a plain text part (verified; `169.254` never reached the upstream). This is not exploitable.
- **The route-auth test checks its own list.** `TestAllPublicRoutesRequireKey` walks `s.authRoutes`, which only the `authed` helper fills. A route added later with `mux.Handle` would not be covered. A table of probe paths would make the test independent of the helper.

---

## 2. Status of adopted items

Implemented = code (or deployment doc, for engine items) does what the item says and I verified it in tests or live. Partial = a named part is missing.

| # | Item | Status | Where (file:line) | Evidence |
|---|---|---|---|---|
| 1 | Admin on its own listener; public router has no `/admin` | Implemented | `internal/config/config.go:108` (default `127.0.0.1:8081`); `internal/api/server.go:103-123` (public mux; `/admin/` only with `LGAI_ADMIN_ON_PUBLIC`); `server.go:125-129` + `internal/auth/middleware.go:92` (whole admin mux behind `RequireAdmin`); `cmd/gateway/main.go:122` | Live: admin token on public `/admin/keys` → 404; `TestAdminNotOnPublicHandler`, `TestAdminAuth` |
| 2 | Secret rules; empty example secrets | Implemented | `config.go:216` `ValidateSecret` (≥ 43 chars, ≥ 16 distinct, placeholder words); `internal/config/parse.go:93` (`NAME`/`NAME_FILE`); `.env.laptop.example:29`, `.env.server.example:27`; `gen-secret` in `cmd/gateway/commands.go` | `TestAdminTokenValidation`, `TestGenSecretPassesValidation` |
| 3 | Typed allowlist, re-encoded; auth before body | Implemented | `internal/oai/normalize.go:50-109`, `types.go:7-26`; depth `normalize.go:55`; auth runs in `RequireKey` before `readBody` | Live §3 rows A–D; `TestForbiddenFieldsNeverForwarded`, `TestAuthBeforeBody` |
| 4 | `chat_template_kwargs` = `{enable_thinking: bool}` only | Implemented | `internal/oai/params.go:197-220` | Live: extra key, `tokenize`, escaped `tokenize`, `chat_template`, nested value → 400; duplicates re-validated |
| 5 | `response_format`, `tool_choice`, `tools` caps; `logprobs` dropped | Implemented | `normalize.go:254-292`, `:456-524`; no logprob fields in `ChatRequest` | Unit table; live |
| 6 | Engine text-only, no media fetch | Implemented in the gateway; Partial at the engine | Gateway: `normalize.go:376-406` (non-text part → 400). Engine: `HANDOFF-jan.md:147` `--language-model-only`; `VLLM_MEDIA_URL_ALLOW_REDIRECTS=0` not set | Live: 7 part types → 400, 0 upstream calls. Engine flag still UNVERIFIED |
| 7 | Chat input cap, ≤ 128 messages, 256 KiB body | Implemented | `config.go:144` (16384, stricter than the 24 KiB asked for), `normalize.go:91`, `normalize.go:15`; `config.go:147` | Live: 24577 B → 413; 300 KB with Content-Length and chunked → 413; unauthenticated 300 KB → 401 |
| 8 | Engine reachable only through the gateway | Implemented (verification is Jan's day-1 check) | `config.go:183-187` (public upstream with no auth → refuse to start); `parse.go:142-179`; `HANDOFF-jan.md:138` (Modal proxy auth on by default), `:146` `--api-key`, `:182` closed-path check | `TestUnauthenticatedPublicUpstreamRefused` |
| 9 | No redirects; header allowlist; only Content-Type back | Implemented | `internal/upstream/client.go:59,71-72`; `client.go:94-118`; `internal/relay/json.go:75` (always `application/json`) | Live: 303 → 502, redirect target got 0 requests; upstream saw only `Accept, Authorization (upstream key), Content-Length, Content-Type, Modal-Key, Modal-Secret, X-Request-Id`; no `lgai_` string ever reached the upstream; `Set-Cookie`/`Server` not relayed |
| 10 | `.gitignore` / `.dockerignore` | Implemented | `.gitignore`, `.dockerignore` | A built `gateway` binary sits in the repo root; both files ignore it |
| 11 | No content logging; patched, hardened engine | Implemented in code; Partial in deployment | Gateway logs no content (no `Debug` calls; `TestNoSecretsOrContentAnywhere` at DEBUG); `HANDOFF-jan.md:119` `vllm==0.30.0` (≥ 0.11.1), `:155-156` | No canary-prompt grep of `modal app logs` or the Caddy logs in the day-1 checks (J5) |
| 12 | One replica, spend limits, no unlimited keys outside load tests | Implemented (docs) | `HANDOFF-jan.md:132` `max_containers=1`, `:105` budget/spend limit, `:197` create/revoke the load-test key | Operational |
| 13 | Seat-label names; delete after the lab | Implemented | `internal/store/schema.sql` header; `internal/api/admin.go` `checkName` message; `docs/runbook-lab-day.md:105-112` | — |
| 14 | Keys only from `Authorization: Bearer` | Implemented | `internal/auth/keys.go:50` | Live: query string and `X-API-Key` → 401; `TestBearerParsing` covers query, header, cookie |
| 15 | Key expiry | Implemented | `schema.sql:20`; `internal/store/keys.go:103-113`; `admin.go` `expiry`; `config.go:113` | `TestUnknownRevokedExpiredIdentical`, `TestKeyTTL` |
| 16 | Security headers, no CORS | Implemented | `internal/api/middleware.go:57` | Live: `no-store`, `nosniff`, CSP on 200 and 502; `TestSecurityHeaders` covers 200/SSE/400/401/404/405/429/503/admin |
| 17 | Connection cap | Implemented | `cmd/gateway/listener.go:18`, `main.go:109` | `TestConnCap`. The cap itself is the resource V1/V5 exhaust |
| 18 | Tighter stream timeouts (adopted via D5) | Implemented | `config.go:150` (`WRITE_TIMEOUT` 10 s); `.env.server.example` `LGAI_UPSTREAM_TIMEOUT=5m` | Live: a client that stopped reading was cut after ~10 s, recorded as 499, and the upstream was cancelled (fake upstream: "ctx cancelled after 3348 chunks") |
| 19 | `framework` in the user message, with a pattern | Implemented | `internal/tasks/tasks.go:104-111`, `internal/tasks/prompts.go:94` | `TestSystemPromptIsolation` |
| 21 | Secret/file hygiene | Implemented | `parse.go:93` (`_FILE`); `config.go:324` (unknown-variable warnings print names only); `config.go:301` (`redactURL`); `cmd/gateway/umask_unix.go:8`; `store.Open` 0600 | Live: DB, `-wal`, `-shm` are 0600; `TestConfigLogValue` |
| 22 | Pins and vulnerability gate | Partial | `Dockerfile`: patch tags plus `-mod=readonly` and `GOTOOLCHAIN=local`; digests only in a comment; compose hardening present | `FROM` not digest-pinned; `govulncheck` is a manual runbook step (`runbook-lab-day.md:48`), no CI; no gitleaks rule for `lgai_` keys |
| 23 | Laptop binds loopback | Implemented | `config.go:107`; compose `127.0.0.1:` ports | — |
| 24 | Generic upstream errors; cached, generic `/readyz` | Implemented | `client.go:174-196`; `internal/api/handlers.go:124-152` | Live: 50 concurrent `/readyz` → 1 upstream probe; redirect → generic 502 |
| 25 | Batch keys, bulk revoke, maintenance | Implemented | `internal/api/admin.go` (`createKeyBatch`, `revokeKeys`, `setMaintenance`); `server.go:129-141` | `TestBatchAndRevokeByPrefix`, `TestMaintenanceMode` |
| 26 | Keys file of hashes | Implemented | `internal/store/keysfile.go`; `commands.go` `gen-keys`; `main.go` `importKeys` | `TestKeysFileImport`, `TestGenKeysRoundTrip` |
| 20 | Proxy rules (not formally adopted) | Mostly covered | `HANDOFF-jan.md` §4: port 80 closed, `max_size 256KB`, `/admin*` → 404, `X-Request-ID` overwritten, no `log_credentials` | Missing: `request_buffers` (V1), `read_header`, HSTS (J1–J3) |

### 2.1 §5 allowlist, field by field (`internal/oai`)

✓ = matches §5 exactly. Δ = differs; the difference is noted.

| Field | Rule in §5 | Code | Result |
|---|---|---|---|
| `model` | ≤ 64, exact alias, else 404 | `normalize.go:111-128` | ✓ (non-string → 400) |
| `messages` | 1–128; text ≤ cap → 413 | `normalize.go:79-93, 296-318` | ✓ |
| `stream` / `stream_options` | dropped without stream; forced `include_usage`; other keys dropped | `normalize.go:132-161` | ✓ |
| `max_tokens` | null → default; ≤ 0 → 400; clamp; always sent | `normalize.go:95-99`, `params.go:25-35`, `types.go:12` | ✓ (non-stream also clamped to `NONSTREAM_MAX_TOKENS`) |
| `max_completion_tokens` | min of the two, never forwarded | `normalize.go:166-182` | ✓ (live: 999999 → 1024 stream / 768 non-stream; field absent upstream) |
| `temperature`, `top_p`, `top_k` | [0,2], (0,1], {−1, 0, 1–100} | `params.go:157-180` | ✓ |
| `stop` | ≤ 4 items, 1–64 B, sent as an array | `normalize.go:220-249` | ✓ |
| `seed`, penalties | int64; [−2, 2] | `normalize.go:195-207` | ✓ (a huge seed saturates; harmless) |
| `response_format` | text / json_object; other keys dropped | `normalize.go:255-270` | ✓ |
| `tools` | ≤ 16, name pattern, description ≤ 1 KiB, parameters object ≤ 8 KiB and depth ≤ 8, counted toward the cap | `normalize.go:456-524` | ✓ (`strict` and other keys dropped, verified) |
| `tool_choice` | none / auto; required or object → 400 | `normalize.go:271-281` | ✓ (`auto` is kept without `tools`, see Notes) |
| `parallel_tool_calls` | only with `tools` | `normalize.go:282-290` | ✓ |
| `reasoning_effort` | low / medium / high | `params.go:182-193` | ✓ |
| `chat_template_kwargs` | exactly `{enable_thinking: bool}` | `params.go:197-220` | ✓ |
| `n` | absent or 1 | `normalize.go:70-74` | ✓ |
| `logprobs`, `top_logprobs` | dropped | not in `ChatRequest` | ✓ |
| `messages[i].role` / `content` / `name` / `tool_calls` / `tool_call_id` | §5.2 | `normalize.go:320-452` | ✓ (`null` content only on an assistant message with `tool_calls`) |
| Whole body | ≤ 256 KiB, depth ≤ 32, UTF-8, auth first | `middleware.go:189-210`, `normalize.go:52-57` | ✓ (200k `[` rejected in 2 ms) |
| Task body (§5.5) | strict fields, enums, `framework` only for `tests`, size including `framework` | `tasks.go:81-134`, `strict.go:15` | ✓; upstream key set asserted in `TestTaskEndToEnd` |
| §5.6 must-be-absent list | none upstream | `TestForbiddenFieldsNeverForwarded` (`normalize_test.go:244`) | ✓, every listed name plus `_debug_render_only`; case variants and `\u`-escaped keys also dropped (live) |

### 2.2 Break-it log (live, against the logging fake upstream)

| Attack | Result |
|---|---|
| `MAX_TOKENS`, `Max_Completion_Tokens`, `Chat_Template_Kwargs`, `Stream_Options` (case variants) | dropped; upstream keys `max_tokens, messages, model, reasoning_effort, stream`; `max_tokens` = 10 |
| Duplicate `max_tokens` (5, 999999) | last wins, then clamped to 768 |
| `max_tokens: 999999` | decodes to `max_tokens`, clamped to 768 |
| Duplicate `chat_template_kwargs` (good, then `tokenize`) | 400; reversed order → only `{"enable_thinking":true}` sent |
| Depth 33 in an ignored field / 200k `[` / 5000 `[` inside a string | 400 / 400 in 2 ms / accepted (strings aren't counted, correct) |
| 300 KB body with Content-Length / chunked / no key | 413 / 413 / 401 |
| `image_url`, `input_audio`, `video_url`, `audio_url`, `file`, `image_embeds`, `input_image` parts pointing at `169.254.169.254` | 400 each, 0 upstream calls |
| Client sends `Authorization`, `Cookie`, `X-Forwarded-For`, `Modal-Key`, `X-Api-Key`, `Proxy-Authorization` | upstream got only the gateway's own headers; client `Modal-Key` not leaked; no `lgai_` string in any upstream request |
| Upstream 303 to another port (stream and non-stream) | 502 `upstream_error`; no `Location` to the client; redirect target got 0 requests |
| Upstream 200 with `text/html` and a script | 502, sent as `application/json` (fixed since the code review) |
| `LGAI_UPSTREAM_HEADERS` with CRLF, LF, `Host`, `Transfer-Encoding`, `Content-Length`, `authorization` next to an API key, bad name, empty value | refused at startup |
| `LGAI_UPSTREAM_HEADERS` with `\x01` or `\x7f` | starts, then fails at runtime (V3) |
| Slow body: one key, 32 sockets | service down (V1) |
| Slow reader on a stream | cut after ~10 s, 499 recorded, upstream cancelled |
| 50 concurrent `/readyz` | 1 upstream probe |
| Invalid-request flood | not limited; every request recorded (V2) |

---

## 3. Gaps in §6 checklist coverage

Everything in §6 A–G has a real assertion, with these exceptions:

| §6 | Gap | Suggested test |
|---|---|---|
| A | `TestAllPublicRoutesRequireKey` checks the helper's own list, not the mux | Probe a fixed table of paths and methods on `PublicHandler()`; every one except `/healthz` and `/readyz` must give 401 or 404 |
| B | No regression cases for `\u`-escaped keys or duplicate content-part `type` (both safe today) | Add `{"max_tokens":999999}` and `{"type":"image_url","type":"text",…}` to `TestNormalizeChatTable` |
| D | No "one key, many slow bodies" test (V1) | `TestSlowBodiesOneKeyCannotStarveOthers` |
| D | No test that invalid floods are limited and not recorded (V2) | `TestRejectedRequestsAreThrottledAndNotRecorded` |
| D | `TestNoUnlimitedDefaults` checks `Load` defaults, not the two `.env.*.example` files as §6 asks | Parse both files (with secrets filled in) and assert the same |
| E | Header values with `\x01`/`\x7f` not tested (V3) | Extend `TestUpstreamHeaders` |
| H | `govulncheck`, `go vet`, `go test -race` are manual runbook steps, with no CI; no automated image check (uid, no shell, no `.env`/`*.db` in layers); no gitleaks rule for `lgai_[A-Za-z0-9_-]{43}` | A `make check` or pre-push script at minimum; Jan owns CI |
| I | Day-1 checks (`HANDOFF-jan.md` §7) have no canary-prompt grep of `modal app logs lgai-vllm` and the Caddy log, and no slow-body or flood test through Caddy | J5, J6 |

---

## 4. Remaining items for Jan (proxy, VM, Modal)

1. **J1 · Caddy `request_buffers` (V1).** Add `request_buffers 256KB` inside `reverse_proxy` (same size as `max_size`), so Caddy reads the whole body before it opens an upstream request.
   - Do **not** add `timeouts { read_body … }` without testing a stream longer than that value. The Caddy docs call it "a hard limit on the whole upload", and it may behave like Go's `ReadTimeout`, which cancels running streams (ARCHITECTURE §5.4). UNVERIFIED for Caddy.
2. **J2 · `read_header` timeout.** Caddy's default is 1 min ([Caddy options](https://caddyserver.com/docs/caddyfile/options)). Set `servers { timeouts { read_header 10s } }`. Keep `idle` at its 5 min default or lower.
3. **J3 · HSTS (optional, cheap).** `header Strict-Transport-Security "max-age=86400"` on the lab domain.
4. **J4 · Ports.** `.env.server.example` sets `LGAI_ADDR=:8080` and `LGAI_ADMIN_ADDR=:8081` for the container. Keep the compose mappings `127.0.0.1:8080:8080` and `127.0.0.1:8081:8081` exactly. Never run the binary natively on the VM with that `.env`, because it would bind every interface. Firewall: 443 open; 22 only for you and Slava; 80 closed (as in `HANDOFF-jan.md:91`).
5. **J5 · Log canary on day 1.** Send one chat request with a unique string (e.g. `CANARY-<date>`) through the public URL. Then `modal app logs lgai-vllm | grep CANARY` and grep the Caddy log, if enabled: both must find nothing.
6. **J6 · Abuse checks through the public URL, after V1 and V2 are fixed.**
   - **Slow bodies:** one test key opens 300 slow bodies; another key must still get `/v1/models` 200 within 1 s.
   - **Invalid flood:** 1,000 `{}` requests from one key → mostly 429, and the usage row count barely moves.
7. **J7 · Keep vLLM tool and media features off.**
   - Don't add `--enable-auto-tool-choice`, `--tool-call-parser`, `--allowed-local-media-path`, `--enable-log-requests` or `--trust-remote-code`.
   - Keep `--language-model-only` and confirm in the startup log that vLLM accepts it.
   - Optional defence in depth: env `VLLM_MEDIA_URL_ALLOW_REDIRECTS=0` (variable name from vLLM issue #57157).
8. **J8 · Both auth layers.** Confirm that Modal forwards our `Authorization` header to the container (already **UNVERIFIED** in `HANDOFF-jan.md:181`). Also confirm that `/invocations`, `/metrics` and `/docs` without the Modal headers return Modal's 401/403.
9. **J9 · Lab image and load-test key.**
   - Pin both `FROM` lines by digest for the lab image.
   - Run `govulncheck ./...` the day before.
   - Create the `rpm_limit: 0, max_inflight: 64` load-test key only for the soak run and revoke it right after (`HANDOFF-jan.md:197`).

---

## 5. Sources

- Code and docs in this repo, at the file:line references above. Live results come from a gateway built from this tree on 2026-10-01, with the laptop settings, `LGAI_MAX_CONNS=32`, and a fake upstream that recorded raw bodies and headers. The test scripts are kept outside the repo.
- Caddy `reverse_proxy` docs (`request_buffers`; request bodies not buffered by default; SSE flushed immediately): https://caddyserver.com/docs/caddyfile/directives/reverse_proxy, read 2026-10-01.
- Caddy global options (`timeouts`: `read_body` "hard limit on the whole upload", default none; `read_header` default 1m; `idle` default 5m; `max_header_size` 16 KiB): https://caddyserver.com/docs/caddyfile/options, read 2026-10-01.
- [`security.md`](security.md) §8 for the vLLM, Modal and OWASP sources behind the design items.
