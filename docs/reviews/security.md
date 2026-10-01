# Security review: local-generative-ai gateway

| | |
|---|---|
| Reviewer | Security agent |
| Date | 2026-09-30 |
| Reviewed | [`PRD.md`](../PRD.md) (source of truth), [`ARCHITECTURE.md`](../ARCHITECTURE.md) (design of 2026-09-30), ADR 0001–0004, [`api/openapi.yaml`](../../api/openapi.yaml) 0.1.0, [`db-schema.sql`](../db-schema.sql) v1. Context: `research/cloud-options.md`, `research/model-choice.md`, `research/performance.md` |
| Verdict | The core is sound: per-person 256-bit keys, SHA-256 at rest, no content storage, an allowlist, per-key concurrency, a FIFO gate, auth before limits. **It is not ready for public exposure until the 14 MUST items in §4 are done.** Three reasons: (1) the chat allowlist forwards fields that can stall or attack the engine for everyone; (2) admin isolation relies on a reverse-proxy rule that the primary platform (OVH AI Deploy) has nowhere to put; (3) the example env files contain an admin token that the validator accepts. |
| Legend | Likelihood and impact are H/M/L. **UNVERIFIED** = not confirmed against a primary source today. "§n" = ARCHITECTURE.md section. Line numbers refer to ARCHITECTURE.md as of 2026-09-30. |

---

## 0. Summary

**MUST before the first public run** (details in §4):

1. The admin API gets its own listener, `127.0.0.1:8081` by default. It is never on the public port unless someone opts in explicitly.
2. Startup rejects weak or placeholder admin and upstream secrets. The example files ship with them empty. Today's placeholder is 45 characters long, so it passes the "≥ 32 chars" check.
3. Chat requests go through a **typed allowlist** and are re-encoded before forwarding. Content parts are **text only**, the nesting depth is limited, and `max_completion_tokens` is never forwarded.
4. `chat_template_kwargs` accepts only `{"enable_thinking": bool}`. The claim in ARCHITECTURE §5.1 that it "only affects the caller's own request" is wrong: see CVE-2025-61620 and CVE-2025-62426.
5. `response_format` is limited to `text` / `json_object` and `tool_choice` to `none` / `auto`. `tools` is capped. `logprobs` is dropped.
6. The engine is text-only and can't fetch media URLs, which blocks SSRF. The lab model Qwen3.6-35B-A3B ships a vision encoder.
7. A chat input cap: 24 KiB of text and ≤ 128 messages.
8. The engine is unreachable except through the gateway. vLLM `--api-key` does not protect `/invocations`. Provider auth needs extra upstream headers.
9. The upstream client never follows redirects (Modal answers with a 303 after 150 s), never forwards client headers, and copies back only `Content-Type`.
10. `.gitignore` and `.dockerignore` exclude secrets, DBs and key lists.
11. No prompt content is logged anywhere in the chain (gateway, vLLM, Ollama, proxy, provider). vLLM is pinned to a release that contains the fixes.
12. Provider scaling is fixed at one replica, with spend limits. No "unlimited" key is active outside the load-test window.
13. Key names are pseudonymous, and the data is deleted after the lab.
14. Keys are accepted only in the `Authorization` header.

**The architect got right:** SHA-256 for random keys, no auth cache, the same 401 for unknown and revoked keys, admin compare via hash + `ConstantTimeCompare`, no `ReadTimeout`/`WriteTimeout` plus per-request deadlines, `Proxy: nil`, never logging content or keys, per-key inflight counting queued requests, `n = 1`, dropping `keep_alive`, and a fenced user block in the task prompts.

---

## 1. Scope, attackers, topologies

**Assets.**

- A1: GPU and engine availability during the lab. This is the primary asset: the whole lab depends on it.
- A2: cloud credits and the card on file. OVH has no hard cap; Modal has a hard cap that stops the service.
- A3: student API keys.
- A4: the admin token.
- A5: upstream credentials (vLLM key, OVH AI token, Modal proxy token).
- A6: classmates' code and answers in transit and in logs. The PRD promises that classmates' code stays private.
- A7: the usage DB, which holds pseudonymous personal data.
- A8: host and cloud network integrity (engine host, metadata service).
- A9: the demo and the team's reputation in front of the professor.

**Attackers.**

| ID | Who | Notes |
|---|---|---|
| X1 | Internet scanner/bot, no key | Finds a new hostname through Certificate Transparency or scanning. Sprays `/admin`, `/docs`, `/metrics`, `/invocations`, `/v1/*`. |
| X2 | Curious classmate with a valid key | **The most likely attacker.** CS students in a lab *about this service* will try jailbreaks, `max_tokens: 1e9`, scripts in loops, and key sharing. |
| X3 | Holder of a leaked key | Keys leak through the projector, a GitHub push of exercise code, or a group chat. |
| X4 | Us | Placeholders left in config, a public Ollama, debug logs, a forgotten running app. |

**Topologies.** Controls depend on how Jan deploys.

ARCHITECTURE §1 "server mode" (lines 63–70) assumes a GPU VM: a private Docker network (`http://vllm:8000/v1`) with Jan's nginx in front. The research picked **OVH AI Deploy** (primary) and **Modal** (backup). Both are managed container platforms with **one exposed port behind a provider-managed HTTPS ingress** [cloud-options §3, C62, C64, C7]. On those platforms, several controls that the design delegates to "Jan's proxy" have no place to live: the path block on `/admin/`, per-IP limits, and the "engine on a private network" assumption. That is why §4 makes the gateway self-sufficient.

| | T1: sidecar (**recommended** on OVH AI Deploy and Modal) | T2: split | T3: laptop |
|---|---|---|---|
| Public entry | Provider HTTPS ingress → gateway port (the only exposed port) | Jan's nginx on a VM → gateway | The Mac, on the LAN |
| Engine | vLLM in the **same container**, bound to `127.0.0.1` | vLLM as its own provider app with its own public URL | Ollama, native, `127.0.0.1` |
| Upstream auth | Loopback + vLLM `--api-key` | Provider auth (OVH "restricted" token / Modal proxy token) **and** vLLM `--api-key` | None (Ollama has no auth) |
| Where `/admin` can be isolated | Only in the gateway | nginx or the gateway | The gateway |
| Key DB persistence | Container disk may be ephemeral (UNVERIFIED per platform) → change #26 | VM disk | Named volume |

---

## 2. Threat model

| ID | Asset | Threat (actor) | L | I | Mitigation (change #) | OWASP |
|---|---|---|---|---|---|---|
| T01 | A1 | Oversized generation: `max_tokens` 10⁶, `n` > 1, `best_of`, beam search, `min_tokens`/`ignore_eos`, a 1 MiB prompt (X2) | H | H | Clamps, `n = 1` and the allowlist already exist; add #3, #7 | API4, LLM10 |
| T02 | A1 | Engine-wide CPU stall through forwarded vLLM extensions: `chat_template_kwargs` (CVE-2025-61620: Jinja template override; CVE-2025-62426: `tokenize: true` blocks the event loop), `response_format.json_schema` / `tool_choice: required` (grammar compilation), `logprobs` (X2) | M | H: everyone stalls | #3, #4, #5; pinned patched vLLM #11 | API4, API10 |
| T03 | A8 | **SSRF**: `image_url` / `video_url` content parts are fetched by vLLM for a vision-capable model. vLLM fetches any http(s) URL by default, including link-local/private addresses, and follows redirects (vLLM issue #57157; CVE-2025-6242, CVE-2026-24779) (X2) | M | H: cloud metadata, internal services | #3 (text only), #6 | API7 |
| T04 | A4 | Admin API reachable on the public URL; a managed ingress can't path-block it; brute force or a leaked token (X1, X3) | M | H: mint unlimited keys, read everyone's usage | #1, #2 | API2, API5, API8 |
| T05 | A4 | The example placeholder `replace-with-output-of-openssl-rand-base64-32` (45 chars) passes the "≥ 32 chars" check and is public in the repo (X4 → X1) | M | H | #2 | API8 |
| T06 | A1, A2 | Direct engine access that bypasses the gateway: a public Modal/OVH engine URL. vLLM `--api-key` leaves `/invocations` open (verified) (X1) | M (T2) / L (T1) | H: free GPU, no limits, credits burned | #8 | API8, API9 |
| T07 | A3 | Leaked key via the projector, GitHub or a chat (X3) | H | M: bounded by per-key limits | Per-key limits and no-cache revocation already exist; add #15, runbook §7 | API2 |
| T08 | A3 | Key in a URL, a log line or an error echo (X4) | L | M | #14; the logging rules already exist | API2 |
| T09 | A3, A7 | DB file or backup leaked | L | L: the hashes of 256-bit keys are useless; names and activity are personal data | SHA-256 (keep), #13, #21 | API3 |
| T10 | A1 | Unauthenticated flood, header slowloris, many idle connections, huge headers (X1) | M | M | `ReadHeaderTimeout` and `MaxHeaderBytes` already exist; add #17, #20; auth before body parse (#3) | API4 |
| T11 | A1 | A slow/trickle reader keeps a Gate slot after the GPU has finished (X2) | L | M | The per-write deadline already exists; add #18 | API4 |
| T12 | A2, A1 | Load-driven autoscaling or a forgotten running app burns credits. Modal's hard cap then kills the service mid-lab; OVH charges the card (X2, X4) | M | H | #12 | API4, LLM10 |
| T13 | A6 | Prompts logged by the engine, Ollama, the proxy or provider log storage (X4) | M | M: breaks a PRD promise | #11 | LLM02 |
| T14 | A6 | A cross-user prefix-cache timing probe ("did someone already submit this code?") (X2) | L | L | #29 (accept, or use `cache_salt`) | LLM02 |
| T15 | A9 | Prompt injection in `code`/`error`/`instructions`; role spoofing with chat-template control tokens; planted text in shared snippets (X2) | H | L: no tools, no secrets, no cross-user state | #19, #28, prompt rules §3.4 | LLM01, LLM07 |
| T16 | A9 | Jailbroken or offensive output during the live demo, under the team's name (X2) | M | M | Demo runbook §7.5; accept residual risk | LLM01, LLM09 |
| T17 | Users | Students run hallucinated code or install hallucinated package names (self) | M | M | A warning in Szymon's guide (§3.4) | LLM05, LLM09 |
| T18 | A1, A8 | Vulnerable Go stdlib, modernc or vLLM; unpinned images (X4) | M | M–H | #11, #22 | API8, LLM03 |
| T19 | A4, A5 | `.env`, the DB or a key CSV gets committed or copied into the build context (X4) | M | H | #10 | API8 |
| T20 | A3, A10 | Laptop mode: the gateway on `0.0.0.0` over plain HTTP on university Wi-Fi; `OLLAMA_HOST=0.0.0.0` exposes an unauthenticated Ollama (X2 on the LAN) | M | M | #23 | API8 |
| T21 | A5, A6 | Upstream redirects followed (the Modal 303), provider `Set-Cookie` copied back, upstream error text leaking internals (X4, provider) | M | L–M | #9, #24 | API10 |
| T22 | A3 | Browser-based abuse or a CORS misconfiguration added at the proxy | L | L | No CORS (already); #16, #20 | API8 |

---

## 3. Design review by area

### 3.1 Authentication and authorization

**Keep as designed:**

- **Key format.** `lgai_` + 256 bits, base64url, with a `WellFormed` pre-check (§7, line 631).
- **SHA-256, no salt, no pepper.** This is the right choice.
  - Argon2 and bcrypt exist to slow offline guessing of *low-entropy* secrets ([OWASP Password Storage CS](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) is about passwords). A uniformly random 256-bit key can only be attacked by preimage, and SHA-256 resists that.
  - An HMAC with a pepper helps only when keys are guessable, or when the DB leaks without the pepper. Neither matters at 256 bits, and the pepper would be one more secret to manage.
  - A slow hash would make every unauthenticated request cost CPU (a DoS amplifier), and it would kill the O(1) indexed lookup.
- **Timing.** A lookup by digest reveals nothing useful, because the attacker controls only the preimage. `key_prefix` stores about 42 random bits of each key, which leaves about 214 bits unknown. That's fine.
- **No auth cache**, so revocation takes effect on the next request. If a cache is ever added, give it a TTL ≤ 5 s and purge it on revoke.
- **Same 401 for unknown and revoked keys.** The admin check uses `ConstantTimeCompare(sha256(a), sha256(b))`, which is correct and length-independent.
- **Mass assignment is blocked.** `KeyCreateRequest` has `additionalProperties: false`. Keep it, and test it (§6 A).

**Fix:**

- The admin token's **strength** check accepts a public placeholder (#2), and its **placement** depends on a proxy that T1 lacks (#1).
- **Fail-closed middleware.** Wrap the *whole* admin mux in `RequireAdmin`, and the whole public mux in `RequireKey` with explicit exemptions for `/healthz` and `/readyz`. Then a newly added route can't ship unauthenticated (#1, test §6 A).
- **Keys only in the header.** Say explicitly that keys are accepted only in `Authorization: Bearer` (#14).
- **Revocation vs in-flight requests.** Revocation doesn't stop in-flight or queued requests. They can run until `UPSTREAM_TIMEOUT` (10 min). This is COULD #27; #18 shortens the window.
- **Expiry and bulk operations.** There is no key expiry and no bulk operation, so rehearsal keys stay valid forever (#15, #25).

### 3.2 GPU abuse and resource consumption

**Good:**

- `max_tokens` is clamped and `n` > 1 is rejected.
- `best_of`, `use_beam_search`, `min_tokens`, `ignore_eos` and the `guided_*` / `structured_outputs` extensions are all dropped, because they are not on the allowlist.
- Per-key inflight (2) counts queued requests.
- The Gate is strict FIFO.
- Limiters run only after auth.

**Problems:**

- **Allowlisted but dangerous fields:** `chat_template_kwargs` passed as-is, `response_format` with any type, `tools` / `tool_choice` unbounded, `logprobs` / `top_logprobs` ≤ 20, `stop` items of unbounded length, `top_k` unbounded, and content parts passed as-is. §5 gives the replacement list.
- **Chat input is bounded only by the 1 MiB body cap.** Tasks get 16 KiB. `performance.md` sets `--max-model-len 8192` (about 6k input tokens, about 24 KB), so a 1 MiB prompt can never succeed. It still costs JSON parsing and engine tokenization before the context-length rejection (#7).
- **`max_completion_tokens` is listed in the allowlist** (§5.1, line 464) *and* described as "renamed". The upstream body must contain only the clamped `max_tokens`. I believe vLLM's `ChatCompletionRequest` prefers `max_completion_tokens` when both are set, which would bypass the clamp (from memory of `protocol.py`; UNVERIFIED for v0.30.0). Test §6 B makes it moot.
- **JSON parsing.** Go's `encoding/json` matches keys case-insensitively, and the last duplicate wins (documented `Unmarshal` behaviour). So never splice raw client bytes: forward only values re-encoded from typed structs. `encoding/json` stops at 10 000 nesting levels (scanner `maxNestingDepth`, from Go source, not re-read today). Add a cheap depth ≤ 32 pre-check, because free-form objects (`tools[].function.parameters`) reach a Python parser upstream (#3).
- **Slow readers.** Kernel and proxy buffers absorb most responses (≤ 2048 tokens is a few hundred KB of SSE). A stalled client therefore mostly pins a **Gate slot**, not GPU compute. It can do so for up to `UPSTREAM_TIMEOUT` = 10 min if it trickles reads under the 30 s write deadline (#18). UNVERIFIED: that vLLM keeps decoding into its output queue when the HTTP consumer stalls.
- **No connection cap.** Go's `http.Server` accepts unlimited connections (#17).
- **Money.** OVH has no hard cap. Modal's cap is hard, so load-driven autoscaling ends the lab early (#12).
- **The load-test key.** The openapi example (`rpm_limit: 0, daily_token_quota: 0, max_inflight: 64`) creates a key that, if leaked, can take all 32 slots. It must not exist outside the load-test window (#12).

### 3.3 Upstream exposure and SSRF

- **Client input can't choose the upstream URL.** The base URL comes from config and the path is fixed. `model` is resolved through the alias table (unknown → 404) and is never interpolated into a URL. Keep the exact-match lookup.
- **The real SSRF vector is multimodal content parts.** `openapi.yaml` `ChatMessage.content` accepts arrays of arbitrary objects, "forwarded as-is". `model-choice.md` lists Qwen3.6-35B-A3B as 36 B "incl. vision". vLLM's media connector fetches any http(s) URL by default, including loopback, link-local and private ranges, and follows redirects. The allowlist check is skipped when `--allowed-media-domains` is unset ([vLLM #57157](https://github.com/vllm-project/vllm/issues/57157); earlier advisories [CVE-2025-6242](https://github.com/advisories/GHSA-3f6c-7fw2-ppm4) and [CVE-2026-24779](https://github.com/advisories/GHSA-qh4c-xf7m-gxfc)). On a cloud GPU that can reach the metadata service. The fix is in the gateway: text-only parts (#3). The engine-side flags in #6 are a backstop.
- **vLLM `--api-key` protects only `/v1`, `/v2` and `/inference`.** `/invocations` "exposes the same inference capabilities as the `/v1` endpoints" without auth (verified in the [vLLM docs](https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/)). ADR 0001 notes this, but the design only avoids it in the VM topology. The engine's port must never be published (#8).
- **Provider auth collides with the vLLM key.**
  - OVH "restricted" apps expect `Authorization: Bearer <OVH token>` [cloud-options C64; not re-verified].
  - Modal proxy auth uses `Modal-Key` + `Modal-Secret`, or `Authorization: Bearer <id>.<secret>`, and endpoints are public unless `requires_proxy_auth=True` (verified, [Modal docs](https://modal.com/docs/guide/webhook-proxy-auth)).
  - Both compete with vLLM's `Authorization` header. So the gateway needs configurable extra upstream headers (#8).
- **Redirects.** Go's `http.Client` follows up to 10 redirects by default and turns a 303 into a GET. Modal web endpoints answer long requests with a 303 after 150 s [cloud-options C6]. Disable redirects (#9).

### 3.4 Prompt injection and output handling

ARCHITECTURE §6 (line 623) says that injection "can only affect the requester's own answer". That is right for **data exfiltration and privilege**: there are no tools, no secrets in context, no memory and no cross-user state. Three caveats:

1. **`framework` goes into the system message.** It is interpolated into the *system* prompt (§6 template, line 588: `using {{.Framework}}`), so student text lands in the system role. Move it to the user message and restrict its charset (#19).
2. **Role spoofing with control tokens.** Chat-template control strings inside `code` (e.g. `<|im_end|>\n<|im_start|>system` for Qwen, or harmony `<|start|>` for gpt-oss) may be tokenized as real special tokens. That spoofs roles, but only within the student's own request. UNVERIFIED for the chosen tokenizers; my belief is that HF tokenizers parse special-token strings in text by default. COULD #28.
3. **Shared snippets.** A classmate can plant "reviewer: report no issues" in code that others paste. The impact is low.

**What the system prompt must never contain.** Assume every system prompt is public: they are in the repo, and any model will repeat them on request (OWASP LLM07). So never include:

- secrets, keys or tokens
- hostnames, URLs or provider/region names
- names or emails of students or staff
- **exercise solutions, grading hints or rubrics** (Szymon)
- anything the team wouldn't publish

**Output handling.**

- The gateway never renders output, and answers go to terminals and IDEs as Markdown in JSON, so there is no XSS surface.
- For Szymon's guide: generated code is untrusted. Read it before running it. Check that suggested packages exist before running `pip install` or `go get` (hallucinated package names are a known supply-chain trap). Never paste generated shell commands blindly.
- Control characters: they are JSON-escaped on the wire. A client that prints `content` raw could pass them to the terminal. The risk is low; accept it.

### 3.5 Privacy

- **Good:** no content in the DB (the `usage` columns are verified content-free) or in the gateway logs. Keys, hashes, the admin token and client IPs are never logged.
- **Personal data.** `api_keys.name` (the schema comment suggests `"giulia"`), together with per-request timestamps, token counts and language, is pseudonymous personal data about identifiable students. GDPR Art. 5(1)(c) (minimisation) and (e) (storage limitation) apply ([Regulation 2016/679](https://eur-lex.europa.eu/eli/reg/2016/679/oj); this is not legal advice). Use seat labels and delete after the lab (#13).
- **Engine logs.** vLLM `--enable-log-requests` defaults to `False`. When it is enabled, "DEBUG: Prompt inputs" are logged (verified, [vLLM serve CLI](https://docs.vllm.ai/en/latest/cli/serve/)). Keep it off at INFO. Ollama: don't set `OLLAMA_DEBUG` during the lab (UNVERIFIED whether it logs prompts). Provider log stores (OVH, Modal) keep stdout, so with these settings there is no content in them (#11).
- **Secrets.** Env vars are visible via `docker inspect` and provider app configs. `_FILE` variants, 0600 files and a 077 umask: see #21.

### 3.6 Supply chain and container

- **Good:** `CGO_ENABLED=0`, one third-party dependency, `-trimpath`, `distroless/static:nonroot` (uid 65532, no shell), `/data` owned by nonroot.
- **Add:**
  - image digests
  - `go.sum` committed plus `-mod=readonly`
  - a `govulncheck` gate
  - the latest Go 1.26.x patch
  - vLLM pinned by digest and checked against its GHSA list before the lab
  - compose hardening

  See #22 and #11.

### 3.7 Operations

- **Missing today:**
  - a maintenance switch
  - bulk create and bulk revoke
  - key expiry
  - a distribution procedure
  - a leaked-key procedure
  - demo hygiene
  - a post-lab deletion step
- **Where they are covered:** #15 and #25, plus the runbook in §7. TLS and HSTS are Jan's (#20). CORS is already off, and #16 adds headers.

---

## 4. Required changes

Each item is tagged, names the exact place to change, and gives the reason. "Arch" = ARCHITECTURE.md, "OAS" = api/openapi.yaml.

### MUST

**#1 [MUST] The admin API runs on its own listener.** *Where:*

- Arch §3.1 `Config` (add `AdminAddr string`, `AdminOnPublic bool`)
- `NewRouter` (split it into `NewPublicRouter` + `NewAdminRouter`)
- §3.1 route comment (lines 423–427)
- §7 "Admin token" (lines 638–645)
- §9 table
- §12 (a second `http.Server`, with the same timeouts)
- §13 compose (don't publish 8081)
- §17 Q5 (answer: **yes**)
- OAS `info.description` ("every `/admin/*` path returns 404") and `servers`

*What:*

- `LGAI_ADMIN_ADDR` defaults to `127.0.0.1:8081`. The admin mux is served only there, wrapped as a whole in `RequireAdmin` (fail-closed).
- The public router never registers `/admin/`, so it returns a JSON 404.
- If there's no token, the admin listener doesn't start.
- Reaching it:
  - T2/VM: `ssh -L 8081:127.0.0.1:8081`.
  - Compose on the laptop: `127.0.0.1:8081:8081`.
  - T1 single-port platforms: `docker/ovhai exec` if available (UNVERIFIED for OVH AI Deploy), or #26, or **explicit** `LGAI_ADMIN_ON_PUBLIC=true`. The last one mounts the admin mux on the public listener and logs a WARN at startup.

*Why:* T04. Managed ingresses can't path-block, and the design's fallback ("block at the proxy") silently disappears on the primary platform.

**#2 [MUST] Secret validation; example files ship with no usable secrets.** *Where:*

- Arch §7 admin bullet 1 (line 640)
- §9 rows `LGAI_ADMIN_TOKEN` and `LGAI_UPSTREAM_API_KEY`
- `.env.laptop.example` (line 715) and `.env.server.example` (lines 733 and 738)
- OAS `components.securitySchemes.adminToken`

*What:*

- `LGAI_ADMIN_TOKEN`:
  - ≥ 43 chars (32 random bytes, base64)
  - ≥ 16 distinct characters
  - reject, case-insensitively, any value containing `replace`, `example`, `changeme`, `secret`, `password` or `admin`
- `LGAI_UPSTREAM_API_KEY`: the same rules when it is set.
- Both examples ship **empty**, with a comment: `openssl rand -base64 32`.
- Optional: a `gateway gen-secret` subcommand.

*Why:* T05. `replace-with-output-of-openssl-rand-base64-32` is 45 chars. It passes today's check, and it is public on GitHub.

**#3 [MUST] Chat normalization is a typed allowlist, re-encoded.** *Where:*

- Arch §5.1 bullet 3 (line 464: replace the list with a pointer to §5 of this review)
- §3.1 `oai.Message` (line 242) and `NormalizeChat` (lines 267–278)
- §2 step 4
- OAS `ChatCompletionRequest` (lines 723–761) and `ChatMessage` (lines 699–714: `content` array items become `{type: text, text}` only)

*What:*

- Decode into typed structs.
- Forward **only** values re-encoded from those structs, never raw client bytes. `json.RawMessage` is allowed only for `tools[].function.parameters`, after its size and depth checks.
- Apply exactly the rules in §5: unknown top-level and message fields are dropped; non-text content parts return 400; `max_completion_tokens` is merged and never forwarded; depth ≤ 32.
- Authenticate **before** reading or parsing the body. §2 already orders it this way; keep it and test it.

*Why:* T01, T02, T03. Go's case-insensitive, last-duplicate-wins decoding makes raw-byte forwarding bypassable.

**#4 [MUST] `chat_template_kwargs` accepts only `{"enable_thinking": <bool>}`.** Any other key or type returns 400 `unsupported_parameter`. *Where:* Arch §5.1 (line 464: delete "It only affects the caller's own request"), §17.2, ADR 0001 "Consequences" (the architect should amend it), and OAS `chat_template_kwargs`. *Why:* T02. The vLLM advisories are real engine-wide DoS: [CVE-2025-61620](https://github.com/advisories/GHSA-6fvq-23cw-5628) (a `chat_template` smuggled through `chat_template_kwargs` overrides the template, fixed 0.11.0) and [CVE-2025-62426](https://github.com/vllm-project/vllm/security/advisories/GHSA-69j4-grxj-j64p) (`tokenize: true` blocks the API server's event loop, fixed 0.11.1).

**#5 [MUST] Constrain structured-output and tool fields.** *Where:* Arch §5.1 and OAS `ChatCompletionRequest`. *What:*

- `response_format.type` ∈ {`text`, `json_object`}. `json_schema`, `structural_tag` and anything else return 400. ADR 0003 already rules structured output out of scope.
- `tool_choice` ∈ {`none`, `auto`}. `required` and named-function objects return 400, because they force guided decoding.
- `tools` is capped as in §5.
- `logprobs` and `top_logprobs` are dropped.

*Why:* T02. Grammar compilation and logprob computation happen engine-side for everyone, and vLLM has a history of schema-driven crashes and DoS. The tool-schema and JSON-schema advisories were fixed in 0.9.0, per the GHSA database (not re-read today).

**#6 [MUST] The engine is text-only and can't fetch media.** *Where:* Arch §1 deployment table (server-mode row) and §13 (new "Model server hardening" list for Jan). *What:*

- For a model with a vision encoder: start vLLM with `--language-model-only` (per `model-choice.md` [M1, M9b]; the flag is not on the vLLM CLI page I read, so UNVERIFIED) or an equivalent that disables multimodal inputs.
- Never set `--allowed-local-media-path`.
- Set `VLLM_MEDIA_URL_ALLOW_REDIRECTS=0` (the env name is from vLLM #57157).

*Why:* T03. This is defence in depth behind #3.

**#7 [MUST] A chat input cap.** *Where:* Arch §3.1 `Gen` (add `ChatMaxInputBytes`), §9 (new `LGAI_CHAT_MAX_INPUT_BYTES`, default `24576`), §2 step 4, and OAS chat description plus the 413 response. *What:*

- The sum of the UTF-8 bytes of all message text plus the serialized `tools` must be ≤ the cap, or the request gets 413.
- `messages` ≤ 128.
- Keep `cap + MAX_TOKENS_CAP` consistent with `--max-model-len`. With 24 KiB at about 4 B/token that is about 6k + 2k = 8k, matching `performance.md`.
- Also lower `LGAI_MAX_BODY_BYTES` to `262144` (SHOULD).

*Why:* T01.

**#8 [MUST] The engine is reachable only through the gateway.** *Where:*

- Arch §1 (the diagram label "private network, upstream API key" and the deployment table)
- §9 (new `LGAI_UPSTREAM_HEADERS`, a secret: `Name=value;Name2=value2`, redacted in logs)
- §13 Jan notes

*What:*

- **Prefer T1.** vLLM runs `--host 127.0.0.1` in the gateway's container, and only the gateway port is exposed.
- **In T2**, the engine's public URL must be behind provider auth: OVH "restricted" access, or Modal `requires_proxy_auth=True` with `Modal-Key`/`Modal-Secret` sent via `LGAI_UPSTREAM_HEADERS`. vLLM `--api-key` is used as well. It is never enough on its own, because `/invocations`, `/metrics` and `/docs` stay open.
- **Startup.** If the upstream host is not loopback or private and neither `LGAI_UPSTREAM_API_KEY` nor `LGAI_UPSTREAM_HEADERS` is set, refuse to start (or require `LGAI_UPSTREAM_ALLOW_UNAUTHENTICATED=true`).

*Why:* T06.

**#9 [MUST] Upstream client hygiene.** *Where:* Arch §5.1 "Upstream transport" (lines 465–470), §3.1 `upstream.New`, and §5.3 non-streaming (line 520). *What:*

- `CheckRedirect` returns `http.ErrUseLastResponse`. A 3xx becomes 502 `upstream_error`.
- Upstream requests carry only these headers: `Content-Type`, `Accept`, `X-Request-ID`, the upstream `Authorization` and the headers from `LGAI_UPSTREAM_HEADERS`. In particular the **client's `Authorization` is never forwarded**.
- Only `Content-Type` is copied back to the client.
- TLS verification is always on, with no "insecure" option.

*Why:* T21 and the Modal 303. It also keeps student keys away from third-party providers.

**#10 [MUST] Secrets never enter the repo or the image.** *Where:* Arch §3 layout (line 180: add the files) and §13 Dockerfile.

*`.gitignore`:*

- `.env`
- `.env.*`, but keep `!.env.*.example`
- `data/`
- `*.db`, `*.db-wal`, `*.db-shm`
- `keys*.csv`, `*.keys`
- `*.pem`, `*.key`
- `/loadtest-results/`

*`.dockerignore`:* the same list, plus `.git` and `docs/`.

*Why:* T19. Slava pushes to GitHub, and GitHub secret scanning doesn't know the `lgai_` format.

**#11 [MUST] No content logging anywhere in the chain; a patched and hardened engine.** *Where:* Arch §11 "Never logged" (lines 795–799: extend it to the whole chain) and §13 (new "Model server hardening" list). *What:*

- **vLLM:**
  - `--enable-log-requests` off (the default, verified), log level INFO
  - `--disable-fastapi-docs`
  - never `VLLM_SERVER_DEV_MODE` or `VLLM_ALLOW_RUNTIME_LORA_UPDATING` (env names from memory, UNVERIFIED)
  - no `--trust-remote-code` unless the model requires it
  - image `vllm/vllm-openai:v0.30.0@sha256:…`. It must be ≥ 0.11.1 for the `chat_template_kwargs` fixes. v0.30.0 qualifies; re-check the [GHSA list](https://github.com/vllm-project/vllm/security/advisories) on deploy day.
- **Ollama:** no `OLLAMA_DEBUG`.
- **Proxy:** no `$request_body` and no `Authorization` in log formats.
- **Rehearsal check:** send a canary prompt and grep the engine and provider logs for it.

*Why:* T13, T02, T18.

**#12 [MUST] Cost and availability guards.** *Where:* Arch §13 (Jan) and §17 (new risk). *What:*

- **Fixed capacity.** OVH AI Deploy runs with fixed replicas = 1 and autoscaling off (UNVERIFIED flag names). Modal runs with `max_containers=1` and a workspace spend limit [cloud-options C4].
- **Stop and delete apps** after every session.
- **No "unlimited" keys** (any override `= 0` or `max_inflight` > 4) may be active while the service is public, outside the load-test window. Create the load-test key right before the run and revoke it right after.

*Why:* T12, T01.

**#13 [MUST] Pseudonymous keys; delete after the lab.** *Where:* `db-schema.sql` `api_keys.name` comment (`"giulia"` → `"lab-07"`, `"team-demo"`), Arch §8 notes (demo key naming), §17 (new "Retention" item), and OAS `KeyCreateRequest.name` description. *What:*

- Key names are seat or role labels. There are no real names or emails in the DB, and any seat-to-person mapping stays on paper or doesn't exist.
- After the lab: export the aggregate numbers without names, then delete the DB, its `-wal`/`-shm`, the volume, `keys.csv`, the printed slips and the provider apps and logs (§7.6).

*Why:* the PRD privacy promise and GDPR minimisation.

**#14 [MUST] Keys only from `Authorization: Bearer`.** *Where:* Arch §7 "User keys" (lines 629–636) and OAS `securitySchemes.apiKey`. *What:*

- No query-string, cookie or `X-API-Key` fallback. The scheme name is case-insensitive (RFC 9110 §11.1).
- 401 bodies never echo the presented value.
- No key or token ever appears in a URL (admin calls included).

*Why:* T08. Proxy and provider access logs record URLs.

### SHOULD

**#15 [SHOULD] Key expiry.** *Where:*

- `db-schema.sql` `api_keys`: add `expires_at INTEGER`, `CHECK (expires_at IS NULL OR expires_at > created_at)`, and to the auth query `AND (expires_at IS NULL OR expires_at > ?)`
- Arch §7 and §9 (`LGAI_KEY_TTL`, default `12h`, `0` = none)
- OAS `KeyCreateRequest` (`expires_at`, date-time or null) and `Key`

*What:* An expired key gets the same 401 as an unknown one. Lab keys expire one hour after the lab ends. *Why:* T07. Rehearsal and lab keys die even if someone forgets to revoke them.

**#16 [SHOULD] Security headers on every response; no CORS.** *Where:* Arch §2 step 1 (middleware), §5.3.1 (SSE headers), §12 "CORS" (line 823), and OAS `info.description`. *What:*

- All responses, SSE and errors included, carry:
  - `Cache-Control: no-store` (replacing `no-cache` on SSE)
  - `X-Content-Type-Options: nosniff`
  - `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`
  - `Referrer-Policy: no-referrer`
  - `Content-Type: application/json; charset=utf-8` for JSON
- No `Access-Control-*` headers, ever. `OPTIONS` returns a JSON 405 without CORS headers.

*Why:* the [OWASP REST Security Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/REST_Security_Cheat_Sheet.html), "Security Headers". `no-store` keeps intermediaries from caching students' code.

**#17 [SHOULD] A connection cap.** *Where:* Arch §12 and §9 (`LGAI_MAX_CONNS`, default `256`). *What:* wrap the listener with a semaphore on `Accept`. It's about 20 lines of stdlib and avoids adding `x/net/netutil`. *Why:* T10. The gateway is the edge in T1.

**#18 [SHOULD] Tighter stream timeouts.** *Where:* Arch §9. *What:*

- `LGAI_WRITE_TIMEOUT` goes from 30s to `10s`.
- Server-mode `LGAI_UPSTREAM_TIMEOUT` goes from 10m to `5m`. For reference, 2048 tokens at 10 tok/s is 205 s.
- Keep laptop mode separate.

*Why:* T11.

**#19 [SHOULD] No client text in the system message.** *Where:* Arch §6 fields table (line 551) and the prompt template (line 588), and OAS `TaskRequest.framework` (add `pattern: '^[A-Za-z0-9 .+#()/_-]{1,64}$'`). *What:*

- Render `framework` in the user message ("Use this test framework: …").
- Count it towards `TASK_MAX_INPUT_BYTES`.
- `framework` sent to a task other than `tests` returns 400.
- Never build template *source* from client input.

*Why:* T15.

**#20 [SHOULD] Proxy and ingress rules.** *Where:* Arch §13 "Reverse proxy (Jan)" (lines 854–862). *What:*

- **TLS.** Use TLS 1.2+ (prefer 1.3). No plain-HTTP API listener: don't redirect port 80, close it or return 403, because a redirected API client has already sent its key in clear text.
- **HSTS** `max-age=86400` on Jan's own domain only (API clients ignore it; it's cheap).
- **Nginx settings:**
  - `server_tokens off`
  - `client_max_body_size 256k`
  - per-IP `limit_req` / `limit_conn` for the unauthenticated flood
  - no CORS headers added
  - access logs without the body and without `Authorization`
- **Admin.** `/admin/` isn't routed at all, which #1 makes the default.

*Why:* T10, T22.

**#21 [SHOULD] Secret and file hygiene.** *Where:* Arch §9 intro (line 671), §3.1 `Config.LogValue` and `store.Open`, and §13. *What:*

- `LGAI_ADMIN_TOKEN_FILE`, `LGAI_UPSTREAM_API_KEY_FILE` and `LGAI_UPSTREAM_HEADERS_FILE` for Docker or provider secrets.
- Unknown-`LGAI_*` warnings print **names only**, since a typo like `LGAI_ADMIN_TOKN=…` would otherwise leak the value.
- Redact the userinfo and query of any URL in the config log.
- `syscall.Umask(0o077)` at startup, so the DB, `-wal` and `-shm` are 0600 and `/data` is 0700.
- `keys.csv` is 0600 on the admin laptop.

*Why:* T09, T19.

**#22 [SHOULD] Supply-chain pins and a vulnerability gate.** *Where:* Arch §4 and §13 Dockerfile. *What:*

- **Images:** `golang:1.26.x@sha256:…` and `gcr.io/distroless/static-debian12:nonroot@sha256:…`.
- **Go module hygiene:** commit `go.sum`, build with `GOFLAGS=-mod=readonly`, use the latest 1.26 patch.
- **CI gate:** [`govulncheck ./...`](https://go.dev/doc/security/vuln/) (pin the tool version) must exit 0, in CI and on the day before the lab.
- **Compose hardening:**
  - `read_only: true` (only `/data` is writable)
  - `cap_drop: [ALL]`
  - `security_opt: ["no-new-privileges:true"]`

*Why:* T18.

**#23 [SHOULD] Laptop mode binds loopback.** *Where:* Arch §9 `LGAI_ADDR` row (line 675), §13 compose `ports` (line 849), and §17 Q4 (line 949). *What:*

- Native default `127.0.0.1:8080`. Compose publishes `127.0.0.1:8080:8080`.
- Never `OLLAMA_HOST=0.0.0.0` on a shared network.
- If the laptop fallback must serve the class, put it behind Jan's TLS proxy, or accept plain HTTP on the LAN **only** with keys that expire at the end of the lab.

*Why:* T20.

**#24 [SHOULD] Upstream error text and `/readyz`.** *Where:* Arch §5.2 (line 483) and OAS `/readyz` 503 example (it leaks `dial tcp 127.0.0.1:11434`). *What:*

- Forward the upstream `message` (≤ 512 B) only for upstream 400/422, which concern the client's own input.
- Every other status gets a fixed generic message; the details go to the log.
- `/readyz` returns a generic message, caches its probe result for 2 s (singleflight), and never shows hosts or ports.

*Why:* T21. It also stops unauthenticated callers from turning `/readyz` into upstream request amplification.

**#25 [SHOULD] Admin operations for the lab.** *Where:* Arch §3.1 routes and OAS (new paths). *What:*

- `POST /admin/keys/batch {"count": 40, "name_prefix": "lab-", "expires_at": …}`, which returns the plaintext keys once.
- `POST /admin/keys/revoke {"name_prefix": "lab-"}`, or `{"all": true}`.
- `PUT /admin/maintenance {"enabled": true}`. While enabled, generation returns 503 `maintenance` with `Retry-After`; `/v1/models`, health and admin keep working.
- Log every action at INFO with counts only.

*Why:* the kill switch and mass rotation (§7).

**#26 [SHOULD] Keys survive a restart without a shared fallback key.** *Where:* Arch §13, §17, and §9 (`LGAI_KEYS_FILE`). *What:*

- Either put the DB on persistent storage, or let the admin generate keys offline (`gateway keys gen -n 40` writes `keys.csv` with the plaintext and a hashes file).
- The gateway then imports **hashes only** at startup (upsert by hash). Revocation still happens in the DB.

*Why:* in T1 the container disk may be ephemeral (UNVERIFIED for OVH AI Deploy and Modal). A restart mid-lab would otherwise lead to "everyone use this one key".

### COULD

**#27 [COULD] Revoke cancels in-flight work.** `KeyInflight` keeps a per-key set of `CancelCauseFunc`, and revoke cancels them with the cause `key_revoked`. *Where:* Arch §8, §3.1 `limits.KeyInflight`.

**#28 [COULD] Neutralise chat-template control tokens in task inputs.** Replace strings like `<|im_start|>`, `<|im_end|>`, `<|endoftext|>`, `<|start|>`, `<|end|>`, `<|channel|>`, `<|message|>` with a visibly broken form (e.g. insert a U+200B) inside `code`, `error` and `instructions`. *Where:* Arch §6. (UNVERIFIED that the tokenizers parse them; the change breaks nothing real.)

**#29 [COULD] Per-key `cache_salt` against prefix-cache timing probes.** vLLM only. The cost is less cross-user prefix reuse of the ~200-token task system prompt. Otherwise accept T14 explicitly. *Where:* Arch §5.1. (`cache_salt` support per vLLM docs: UNVERIFIED today.)

**#30 [COULD] Easier-to-type keys.** Use 128-bit Crockford base32, which is ~26 chars, plus a CRC32 checksum suffix. Also add a gitleaks rule for `lgai_[A-Za-z0-9_-]{43}`. *Where:* Arch §7, OAS `KeyCreated.key.pattern`. 128 bits is still far beyond online guessing.

**#31 [COULD] Trust `X-Request-ID` only from the proxy.** `LGAI_TRUST_REQUEST_ID=true`. Otherwise always generate an ID and log the client's one separately. *Where:* Arch §11 (line 789).

**#32 [COULD] Correct §10 and log admin auth failures.**

- **§10 claim.** Arch §10 says "every non-2xx … is application/json". That is not true for `net/http`'s own plain-text 400 (malformed request) and 431 (headers > `MaxHeaderBytes`), which are sent before any handler runs. Document it.
- **Admin auth failures.** Log them at WARN (without the value) and rate-limit them globally to 10/min, to stop log flooding.

---

## 5. Request-field allowlist and clamps

**Principle.** The gateway **builds** the upstream body from typed, validated values. Anything not listed here never reaches the engine.

- Out-of-range values return **400 `invalid_value`**, except `max_tokens`, which is clamped because OpenAI clients expect that.
- Unsupported-but-meaningful fields return **400 `unsupported_parameter`**, so students aren't surprised when a field silently does nothing.
- Unknown top-level fields are **dropped silently**, for SDK compatibility (for example the SDK's `user`, `metadata` and `store`).

### 5.1 `POST /v1/chat/completions`: top level

| Field | Accepted | Clamp / rule | Forwarded as |
|---|---|---|---|
| `model` | string ≤ 64 | exact alias match, else 404 `model_not_found`; missing → default | upstream id from `LGAI_MODELS` |
| `messages` | array, 1–128 | per-message rules in §5.2; total text ≤ `LGAI_CHAT_MAX_INPUT_BYTES` (24 KiB) → else 413 | normalized array |
| `stream` | bool | — | as-is |
| `stream_options` | `{include_usage: bool}` | only with `stream: true` (else dropped); other keys (`continuous_usage_stats`, …) dropped | `{"include_usage": true}` (forced; client value only controls forwarding of the usage chunk, ADR 0004) |
| `max_tokens` | int ≥ 1, or null | null/missing → `LGAI_CHAT_DEFAULT_MAX_TOKENS`; `≤ 0` → 400; > cap → `LGAI_MAX_TOKENS_CAP` | `max_tokens` (**always present**) |
| `max_completion_tokens` | int ≥ 1, or null | merged: effective = min of the present values, then clamp | **never forwarded** |
| `temperature` | number | [0, 2] | as-is |
| `top_p` | number | (0, 1] | as-is |
| `top_k` | int | −1, 0, or 1–100 | as-is (Ollama ignores) |
| `stop` | string or array | ≤ 4 items, each 1–64 bytes | as array |
| `seed` | int64 | — | as-is |
| `frequency_penalty`, `presence_penalty` | number | [−2, 2] | as-is |
| `response_format` | object | `type` ∈ {`text`, `json_object`}; other keys dropped; other types → 400 | `{"type": …}` |
| `tools` | array | ≤ 16; each `{type:"function", function:{name ^[A-Za-z0-9_-]{1,64}$, description ≤ 1024 B, parameters: object, ≤ 8 KiB, depth ≤ 8}}`; serialized tools count towards the input cap; other keys dropped | re-encoded |
| `tool_choice` | string | `none` \| `auto`; `required` or object → 400 | as-is |
| `parallel_tool_calls` | bool | only with `tools` | as-is |
| `reasoning_effort` | string | `low` \| `medium` \| `high` | as-is |
| `chat_template_kwargs` | object | exactly `{enable_thinking: bool}`; any other key/type → 400 | `{"enable_thinking": …}` |
| `n` | int | absent or 1; else 400 | omitted |
| `logprobs`, `top_logprobs` | — | **dropped** | — |
| everything else | — | dropped (see §5.6) | — |

### 5.2 `messages[i]`

| Field | Rule |
|---|---|
| `role` | `system` \| `developer` \| `user` \| `assistant` \| `tool`, else 400 |
| `content` | string; or an array of ≤ 64 parts where **every** part is `{type: "text", text: string}`, **flattened to one string** before forwarding; `null` only for `assistant` with `tool_calls`. Any other part type (`image_url`, `input_audio`, `video_url`, `audio_url`, `file`, `image_embeds`, …) → 400 `unsupported_parameter`, `param: messages[i].content[j].type` |
| `name` | `^[A-Za-z0-9_-]{1,64}$` |
| `tool_calls` | `assistant` only; ≤ 16; each `{id ≤ 64, type: "function", function: {name as above, arguments: string}}`; the arguments count towards the input cap |
| `tool_call_id` | `tool` only; ≤ 64 |
| anything else (`reasoning`, `reasoning_content`, `audio`, `refusal`, …) | dropped |

### 5.3 Whole-body limits (chat and tasks)

- The body is ≤ `LGAI_MAX_BODY_BYTES`: currently 1 MiB, recommended 256 KiB (#7). Over the limit → 413.
- JSON depth is ≤ 32 anywhere (a pre-scan). Deeper → 400 `invalid_json`.
- The body must be valid UTF-8. Invalid → 400.
- The key is authenticated **before** the body is read.

### 5.4 Fields the gateway sets itself (never client-controlled)

- `model` (upstream id)
- `stream_options.include_usage`
- `max_tokens`
- the task `messages`
- the task sampling constants from `docs/research/model-choice.md` (`top_p`, `top_k`, `presence_penalty`)
- a server-default `reasoning_effort` or `chat_template_kwargs` from config, where needed

Upstream headers are limited to the set in #9.

### 5.5 `POST /v1/tasks/{task}`

Unknown fields → 400 `unknown_field` (as designed; ADR 0003). `{task}` ∈ enum, else 404.

| Field | Rule | Goes into |
|---|---|---|
| `language` | `python` \| `java` \| `go` \| `c` \| `cpp` | the template choice |
| `code` | required, non-blank, valid UTF-8 | the **user** message, fenced |
| `error` | required for `fix`; optional otherwise | the **user** message, fenced |
| `instructions` | ≤ 1000 chars | the **user** message |
| `framework` | `tests` only; `^[A-Za-z0-9 .+#()/_-]{1,64}$` | the **user** message (#19) |
| `model` | exact alias | upstream id |
| `stream` | bool | as-is |
| `stream_options` | `{include_usage: bool}` only | forced true upstream |
| `max_tokens` | int ≥ 1 | clamp ≤ `LGAI_MAX_TOKENS_CAP`; default per task |
| `temperature` | [0, 2] | as-is; default per task |
| Size | `code + error + instructions + framework` ≤ `LGAI_TASK_MAX_INPUT_BYTES` (16 KiB) | else 413 |

**The upstream body for a task** is exactly:

- `model`
- `messages: [system (server text only), user (rendered)]`
- `stream`
- `stream_options` (when streaming)
- `max_tokens`
- `temperature`
- the server-side constants from §5.4

### 5.6 Must be absent upstream (a test fixture; not exhaustive)

- **Ollama and llama.cpp native options:** `keep_alive`, `options`, `format`, `think`, `raw`, `template`, `system`, `n_probs`, `cache_prompt`, `id_slot`, `lora`, `grammar`, `json_schema`
- **vLLM sampling extras:** `best_of`, `use_beam_search`, `length_penalty`, `min_tokens`, `ignore_eos`, `min_p`, `repetition_penalty`, `stop_token_ids`, `bad_words`, `allowed_token_ids`, `logit_bias`, `logits_processors`
- **vLLM prompt and template controls:** `prompt_logprobs`, `echo`, `add_generation_prompt`, `continue_final_message`, `add_special_tokens`, `documents`, `chat_template`, `mm_processor_kwargs`, `truncate_prompt_tokens`
- **vLLM structured outputs:** `guided_json`, `guided_regex`, `guided_choice`, `guided_grammar`, `guided_decoding_backend`, `structured_outputs`
- **vLLM server internals:** `priority`, `request_id`, `cache_salt` (unless #29), `kv_transfer_params`, `vllm_xargs`, `return_tokens_as_token_ids`, `return_token_ids`
- **OpenAI-only fields:** `user`, `metadata`, `store`, `service_tier`, `prediction`, `modalities`, `audio`, `web_search_options`, `functions`, `function_call`

---

## 6. Security test checklist (for the testing agent)

**Harness.** Use a fake upstream (`httptest.Server`) that records every request: method, path, headers and body, decoded into `map[string]any`. Assertions on "what reached upstream" use that record. Timeouts use small values or `testing/synctest` (ARCHITECTURE §15).

### A. Auth and authorization (`internal/auth`, `internal/api`, `internal/config`)

- [ ] `TestKeyGenerateFormat`: 10 000 keys all match `^lgai_[A-Za-z0-9_-]{43}$`, are unique, and decode to 32 bytes.
- [ ] `TestWellFormedRejects`: a wrong prefix, lengths 47 and 49, `=` padding, `+` and `/`, embedded whitespace, a Unicode look-alike and an empty string all return false. The store is never queried (use a spy `KeyLookup`).
- [ ] `TestBearerParsing`:
  - `Bearer k` and `bearer k` → accepted.
  - `Basic …`, `Bearer` with no key, and two `Authorization` headers → 401.
  - A key in `?api_key=`, in `X-API-Key` or in a cookie → 401 (#14).
  - The 401 body never contains the presented value.
- [ ] `TestUnknownAndRevokedIdentical`: the status, the body (apart from the request id) and the header set are the same.
- [ ] `TestRevokeIsImmediate`: create → 200, revoke, and the very next request → 401, with no sleep.
- [ ] `TestExpiredKey` (#15): past `expires_at` → the same 401 as an unknown key.
- [ ] `TestAllPublicRoutesRequireKey`: walk the registered public routes. Every route except `/healthz` and `/readyz` returns 401 without a key (fail-closed).
- [ ] `TestAdminNotOnPublicHandler` (#1):
  - `/admin/keys` on the public handler returns JSON 404, even with a valid admin token.
  - It works on the admin handler.
  - With `AdminOnPublic=true` it works on the public handler too.
- [ ] `TestAdminAuth`: missing → 401; wrong → 403; a wrong token of a different length → 403 (no panic); a user key → 403; the admin token on `/v1/*` → 401.
- [ ] `TestAdminTokenValidation` (#2): `config.Load` fails for these values:
  - a 42-char token
  - the literal `replace-with-output-of-openssl-rand-base64-32`
  - `replace-me`
  - `aaaaaaaa…` (43 chars)
  - a value containing `changeme`

  It succeeds for 20 outputs of `base64(rand 32)`. `LGAI_UPSTREAM_API_KEY` follows the same rules.
- [ ] `TestKeyCreateMassAssignment`: `id`, `key`, `key_hash`, `key_prefix`, `created_at` or `revoked_at` in the body → 400 `unknown_field`.
- [ ] `TestPlaintextKeyShownOnce`: the 201 response has the key and `Cache-Control: no-store`. List, usage and revoke responses contain neither the plaintext nor the hex or base64 of its hash.
- [ ] `TestAuthBeforeBody`: an unauthenticated POST with a 2 MiB body returns 401, not 413. A counting reader shows the handler read 0 body bytes.

### B. Chat normalization (`internal/oai`): table-driven, plus fuzzing

- [ ] **Every field in §5.6 is absent** from the recorded upstream body. The upstream key set ⊆ the allowlist in §5.1.
- [ ] **`max_tokens`:**
  - absent → the default; `null` → the default; `0` and `-1` → 400; `1e9` → the cap; `1.5` → 400
  - only `max_completion_tokens: 50` → upstream `max_tokens: 50`
  - both present (`100`, `50`) → 50
  - `max_completion_tokens` never appears upstream
- [ ] **Case and duplicate bypass:** `{"max_tokens":10,"MAX_TOKENS":999999}` and `{"max_tokens":10,"max_tokens":999999}` → upstream has exactly one `max_tokens` ≤ cap and no other case variant.
- [ ] **`n`:** `2` → 400; `1` → OK.
- [ ] **`stream_options`:** without `stream` → dropped. With `stream` → exactly `{"include_usage":true}`. `continuous_usage_stats` → dropped.
- [ ] **`chat_template_kwargs`:** `{"enable_thinking":false}` → forwarded. These all return 400: `{"chat_template":"{% for …"}`, `{"tokenize":true}`, `{"enable_thinking":"yes"}`, `{"enable_thinking":false,"x":1}`.
- [ ] **`response_format`:** `text` and `json_object` → OK. `json_schema`, `structural_tag`, `{"type":"grammar"}` → 400.
- [ ] **`tools`:** 17 tools → 400; a tool name with a space → 400; `parameters` at depth 9 → 400; `parameters` > 8 KiB → 400.
- [ ] **`tool_choice`:** `"required"` or `{"type":"function",…}` → 400; `"auto"` and `"none"` → OK.
- [ ] **`logprobs`/`top_logprobs`:** absent upstream.
- [ ] **`stop`:** 5 items → 400; a 65-byte item → 400; `""` → 400; a single string → forwarded as a 1-element array.
- [ ] **Content parts:**
  - `image_url` with `http://169.254.169.254/latest/meta-data/`, and `input_audio`, `video_url`, `file`, `image_embeds` → 400. The fake upstream records **0** requests.
  - Text parts are flattened to one string.
  - An unknown `role` → 400. Unknown message fields are dropped.
- [ ] **Input size:** 128 messages → OK; 129 → 400. Total text of `LGAI_CHAT_MAX_INPUT_BYTES` → OK; one more byte → 413. The `tools` JSON counts towards the cap.
- [ ] **Nesting:** depth 33 → 400 `invalid_json`. A body of 1 MiB of `[` → 400 in under 50 ms, with no panic.
- [ ] **Model:** `coder/../x`, `coder?x=1`, `%2e%2e` and a 65-char string → 404. The recorded upstream path is always `{base}/chat/completions`.
- [ ] **`FuzzNormalizeChat`** (seed corpus = the OAS examples plus the cases above). Invariants:
  - no panic
  - on success: upstream keys ⊆ the allowlist, `1 ≤ max_tokens ≤ cap`, every `content` is a string or null, depth ≤ 32, valid JSON

### C. Task endpoints (`internal/tasks`)

- [ ] **Unknown field** → 400 `unknown_field`.
- [ ] **`framework`:** containing `\n`, 65 chars long, or sent to `review` → 400 (#19).
- [ ] **System-prompt isolation:** put distinct canaries in `code`, `error`, `instructions` and `framework`. The recorded upstream `messages[0]` (system) contains none of them, and `messages[1]` (user) contains all of them.
- [ ] **Fences:** code containing ```` ``` ```` and ```` `````` ```` gets a fence one longer than its longest backtick run. The same holds for `error`.
- [ ] **No template evaluation:** code containing `{{.Code}}`, `{{printf "%s" 1}}` or `{{template "x"}}` appears byte-for-byte in the user message.
- [ ] **Size:** `code+error+instructions+framework` = cap + 1 → 413.
- [ ] **Upstream key set:** the recorded upstream body key set equals the list in §5.5 exactly.
- [ ] **`FuzzTaskDecode`:** no panic; on success the system message is one of 20 precomputed strings (4 tasks × 5 languages).

### D. Limits and DoS (`internal/limits`, `internal/relay`, `cmd/gateway` server)

- [ ] **Existing limit tests:** the rate limit gives 429 with `Retry-After ≥ 1`; per-key inflight gives 429; a full queue gives 503 with `Retry-After`; the quota gives 429 with `X-Should-Retry: false`. Keep these.
- [ ] `TestSlowReaderReleasesSlot`:
  - Setup: a real `httptest.Server` and `WRITE_TIMEOUT=200ms`. The client reads the headers and then stops reading. The fake upstream streams 10 000 events.
  - The handler returns.
  - `Gate.Stats()` shows inflight back at 0.
  - The fake upstream sees its request context cancelled.
  - The usage row has status 499.
- [ ] `TestSlowHeaders`: after a partial request line, the connection is closed once `ReadHeaderTimeout` expires.
- [ ] `TestSlowBody`: headers followed by 1 byte/s of body → closed after `BODY_READ_TIMEOUT`; no Gate slot is ever acquired.
- [ ] `TestOversizedHeaders`: a 20 KiB header → 431, and the handler is never invoked.
- [ ] `TestConnCap` (#17): the (N+1)th connection isn't served until one of the N closes.
- [ ] `TestBodyLimit`: `LGAI_MAX_BODY_BYTES + 1` → a JSON 413.
- [ ] `TestNoUnlimitedDefaults`: `config.Load` of both example env files produces no limit equal to 0 except the documented ones.

### E. Upstream client (`internal/upstream`)

- [ ] `TestNoRedirects` (#9): the fake upstream returns `303 Location: <second server>`. The gateway returns 502, and the second server records 0 requests.
- [ ] `TestUpstreamRequestHeaders`:
  - The client sends `Authorization: Bearer lgai_…`, `Cookie`, `X-Forwarded-For` and `X-Custom`.
  - Upstream receives `Authorization` = `Bearer <LGAI_UPSTREAM_API_KEY>`, or no `Authorization` when that key is unset. It **never** receives the lgai key, and never the other client headers.
  - `LGAI_UPSTREAM_HEADERS` entries are present.
- [ ] `TestResponseHeaderHygiene`: the fake upstream sets `Set-Cookie`, `Server` and `X-Internal`. None of them reaches the client, and `Content-Type` is preserved.
- [ ] `TestUpstreamErrorText` (#24):
  - Upstream 500 with the message `/root/.cache/huggingface/CANARY` → the client body has no `CANARY`.
  - Upstream 400 with the message `bad CANARY2` → the client sees `CANARY2`, truncated to ≤ 512 B.
- [ ] `TestUpstreamTLSVerify`: an `https` upstream with a self-signed certificate → 502. There is no config knob to disable verification.
- [ ] `TestUpstreamOversize`: a non-stream body of 8 MiB + 1 → 502. An SSE line over 1 MiB → a mid-stream error event.
- [ ] `TestUnauthenticatedPublicUpstreamRefused` (#8): an upstream `https://example.com/v1` with no key or headers → `config.Load` fails, unless the opt-in flag is set.

### F. Privacy and logging

- [ ] `TestNoSecretsOrContentAnywhere` (the canary test):
  - Run one streaming chat, one non-streaming task, one upstream error, and an admin create + revoke, with slog JSON at **DEBUG** written to a buffer.
  - None of the following may appear in the log buffer, in the SQLite file or in its `-wal`:
    - the plaintext key
    - its SHA-256 hex
    - the admin token
    - the upstream key
    - the prompt canary
    - the completion canary (emitted by the fake upstream)
    - the upstream error canary
- [ ] `TestConfigLogValue`:
  - Secrets render as `set`/`unset`.
  - An upstream URL `https://u:p@h/v1?token=x` is logged without `u:p` and without `token=x`.
  - An unknown `LGAI_ADMIN_TOKN=secretvalue` produces a warning that contains the name and not the value.
- [ ] `TestDBFileModes` (#21): after `store.Open` and one insert, the DB, `-wal` and `-shm` are mode 0600.
- [ ] `TestUsageSchemaHasNoContentColumns`: `PRAGMA table_info(usage)` returns exactly the documented column set.
- [ ] `TestSQLInjectionLiteral`: the key name `x'); DROP TABLE api_keys;--` round-trips literally, and the table still exists.

### G. HTTP surface

- [ ] `TestSecurityHeaders` (#16): check a JSON 200, an SSE 200, a 400, a 401, a 404 (catch-all), a 429 and a 503. All carry:
  - `X-Content-Type-Options: nosniff`
  - `Cache-Control: no-store`
  - the CSP
  - no `Access-Control-*` header
- [ ] `TestNoCORS`: `OPTIONS /v1/chat/completions` with `Origin` and `Access-Control-Request-Method` → a JSON 405 without CORS headers.
- [ ] `TestReadyzCachedAndGeneric` (#24): 100 calls in a row cause ≤ 1 upstream probe per cache window. The 503 body contains no `host:port`.
- [ ] `TestMaintenanceMode` (#25): when on, chat and tasks return 503 `maintenance` with `Retry-After`, while `/v1/models`, `/healthz` and admin still work. When off, requests return 200.
- [ ] `TestRequestIDSanitised`: `a b`, 65 chars and `%00` are replaced by a generated id. A valid id is echoed.

### H. Build and CI

- [ ] `govulncheck ./...` exits 0 (CI gate, with the tool version pinned).
- [ ] `go vet ./...` and `go test -race ./...` pass.
- [ ] Image check: the user is 65532, there's no `/bin/sh`, and there's no `.env`, `*.db` or `keys*.csv` in any layer (a CI script over `docker save`, or container-structure-test).
- [ ] Repo check: `git ls-files | grep -E '(^|/)\.env$|\.db(-wal|-shm)?$|keys.*\.csv$'` is empty. gitleaks runs with the custom rule `lgai_[A-Za-z0-9_-]{43}`.

### I. Deployment smoke (manual, at rehearsal; Jan + Slava)

- [ ] **Unknown paths:** from outside, with no key, `GET /invocations`, `/docs`, `/openapi.json`, `/metrics`, `/health`, `/version` and `/tokenize` return the gateway's JSON 404 or 401. They must never produce a vLLM response.
- [ ] **Engine URL:** in T2, the engine's own URL without the provider token → the provider's 401/403.
- [ ] **Admin:** `/admin/keys` on the public URL → 404 (unless `LGAI_ADMIN_ON_PUBLIC`).
- [ ] **Plain HTTP:** `http://` (port 80) doesn't serve the API and doesn't redirect.
- [ ] **Media parts:** a chat request with an `image_url` part → 400 from the gateway.
- [ ] **Log check:** after a canary prompt, `grep CANARY` over the vLLM logs, the provider log view and the proxy logs finds nothing.
- [ ] **Credentials:** the provider app shows replicas = 1 and autoscaling off. The load-test key has been revoked.

---

## 7. Operations runbook (security)

### 7.1 Before going public (T-1 day)

- All MUST items are done. §6 H and §6 I pass. `govulncheck` is clean. The vLLM image is pinned by digest and the GHSA list has been re-checked.
- **Fresh secrets.** Generate a fresh admin token and upstream key with `openssl rand -base64 32`. Store them in a password manager and put them in the deployment via `_FILE` or provider secrets. They never appear in shell history, and they are never pasted into chats.
- **No unlimited keys.** None may be active. Demo and team keys use modest overrides (≤ 4 inflight, a quota > 0).

### 7.2 Issuing and distributing keys (T-1 h)

1. **Batch-create the keys** (#25). Create (number of students + ~30%) keys named `lab-01…lab-40`, expiring 1 h after the lab ends. Save the one-time output as `keys.csv` (mode 0600) on the admin laptop only.
2. **Print one slip per key.** Each slip has the seat label, the key, the `https://` base URL, and a quickstart:

   ```
   export OPENAI_BASE_URL=https://…/v1
   export OPENAI_API_KEY=lgai_…
   ```

   Hand the slips out in person. Keep the spares with the admin.
3. **Never** do any of these:
   - put keys on slides or the projector, or in the group chat, Moodle or a shared doc
   - email the full list
   - share one key between several people
4. **Tell the class** (Szymon's sheet):
   - keep the key in an env var, not in code
   - don't push it
   - it stops working when the lab ends
   - one person, one key
5. **Absent students.** An individual email from the university account is acceptable, because the key expires anyway.

### 7.3 A leaked or misused key during the lab

1. **Identify it.** The student reports it (the first 12 chars on their slip = `key_prefix`), or `GET /admin/usage` shows a spike, or `jq 'select(.key_id==N)'` on the access log.
2. **Revoke it** with `DELETE /admin/keys/{id}`. This takes effect on the next request. In-flight streams finish within their `max_tokens` (#27 would cut them).
3. **Hand the student a spare slip.** Nothing restarts.
4. **Public leaks.** If the leak was public (GitHub, a chat), watch the log for 401s on that `key_id`. They're harmless and confirm the revocation.
5. **Mass leaks** (e.g. a photo of the slips posted): turn maintenance on, bulk-revoke with prefix `lab-`, hand out new slips, turn maintenance off. If there are no spare slips left, create a new batch.

### 7.4 Kill-switch ladder (least to most disruptive)

1. Revoke the offending key or keys.
2. `PUT /admin/maintenance {"enabled": true}` (#25). Generation returns 503 and the admin API stays reachable.
3. SIGTERM the gateway (graceful drain, §12).
4. Stop the GPU app at the provider (`ovhai app stop <id>` / `modal app stop <name>`). This also stops the billing.

### 7.5 Live demo (Giulia)

- Use a `team-demo` key with normal limits, exported in the shell **before** the talk, off-screen.
- Never run `cat .env`, `env` or `history`, and never run admin calls on the projector.
- Blur keys in slides and screenshots.
- Revoke the demo key after the talk.
- Keep the demo on rehearsed prompts and the task endpoints. Don't take live prompts from the audience on the projector.

### 7.6 After the lab (the same day)

- Revoke all keys (bulk). Stop **and delete** the GPU apps. Delete provider tokens (OVH AI token, Modal proxy token).
- Export the aggregate numbers for Giulia **without key names**.
- **Delete these files:**
  - the DB and its `-wal`/`-shm`
  - the Docker volume
  - `keys.csv`
  - any `.env` with real secrets
- **Also clean up:**
  - shred the printed slips
  - delete the provider logs where the platform allows it
- Rotate the admin token if the configuration will be reused.

---

## 8. Sources and verification status

| Source | Used for | Status |
|---|---|---|
| [OWASP API Security Top 10 2023](https://owasp.org/API-Security/editions/2023/en/0x11-t10/) | The threat categories API1–API10 | Known list; not re-read today |
| [OWASP Top 10 for LLM Applications 2025](https://genai.owasp.org/llm-top-10/) | LLM01, 02, 03, 05, 07, 09, 10 | Known list; not re-read today |
| [OWASP REST Security Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/REST_Security_Cheat_Sheet.html) | JSON API security headers, HTTPS-only | Not re-read today |
| [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) | Slow hashes are for passwords | Not re-read today |
| [vLLM OpenAI-compatible server docs](https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/) | `--api-key` covers only `/v1`, `/v2`, `/inference`; `/invocations` unauthenticated; "Do not rely on `--api-key` alone" | **Verified** 2026-09-30 |
| [vLLM `serve` CLI docs](https://docs.vllm.ai/en/latest/cli/serve/) | `--enable-log-requests` default False (DEBUG logs prompts), `--max-logprobs` 20, `--trust-request-chat-template` default False, `--allowed-media-domains`, `--disable-fastapi-docs` | **Verified** 2026-09-30. `--language-model-only` was not found on that page (UNVERIFIED) |
| [GHSA-6fvq-23cw-5628 / CVE-2025-61620](https://github.com/advisories/GHSA-6fvq-23cw-5628) | A Jinja template smuggled via `chat_template_kwargs`; fixed 0.11.0 | **Verified** |
| [GHSA-69j4-grxj-j64p / CVE-2025-62426](https://github.com/vllm-project/vllm/security/advisories/GHSA-69j4-grxj-j64p) | `chat_template_kwargs {"tokenize": true}` blocks the event loop; fixed 0.11.1 | **Verified** |
| [vLLM issue #57157](https://github.com/vllm-project/vllm/issues/57157) | Media URLs: any host incl. link-local by default; redirects bypass the checks; `VLLM_MEDIA_URL_ALLOW_REDIRECTS` | **Verified** (issue content); fix/release status UNVERIFIED |
| [CVE-2025-6242](https://github.com/advisories/GHSA-3f6c-7fw2-ppm4), [CVE-2026-24779](https://github.com/advisories/GHSA-qh4c-xf7m-gxfc) | Earlier `MediaConnector` SSRF advisories | Titles from the GitHub Advisory DB; details not re-read |
| [CVE-2025-62164](https://github.com/advisories/GHSA-mrw7-hf4f-83pf) | `prompt_embeds` deserialization, a reason never to expose the engine | Title only; not re-read |
| [Modal proxy auth tokens](https://modal.com/docs/guide/webhook-proxy-auth) | Public by default; `requires_proxy_auth=True`; `Modal-Key`/`Modal-Secret` or `Authorization: Bearer id.secret` | **Verified** 2026-09-30 |
| `research/cloud-options.md` [C4, C6, C62, C64] | OVH one port + restricted Bearer token; Modal 150 s → 303; spend limit | From the research doc; not re-verified by me |
| `research/model-choice.md`, `research/performance.md` | Qwen3.6-35B-A3B includes vision; `--language-model-only`; `--max-model-len 8192`; vLLM v0.30.0 | From the research docs |
| Go `net/http` `Client` docs (redirect policy, sensitive-header stripping), `encoding/json` `Unmarshal` docs (case-insensitive keys) and scanner (`maxNestingDepth` 10 000) | #3, #9 | Background knowledge; not re-read today |
| [Go vulnerability management / govulncheck](https://go.dev/doc/security/vuln/) | #22 | Not re-read today |
| [GDPR, Regulation (EU) 2016/679, Art. 5](https://eur-lex.europa.eu/eli/reg/2016/679/oj) | Minimisation, storage limitation | Not legal advice |
| vLLM `max_completion_tokens` precedence; env names `VLLM_SERVER_DEV_MODE` and `VLLM_ALLOW_RUNTIME_LORA_UPDATING`; `cache_salt`; HF tokenizers parsing special-token strings in text; `OLLAMA_DEBUG` prompt logging; OVH AI Deploy exec/volumes/autoscaling flags | #3, #11, #26, #28, #29 | **UNVERIFIED**. The tests or the rehearsal smoke list must confirm them |
