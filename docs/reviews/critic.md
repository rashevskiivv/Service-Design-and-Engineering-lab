# Critic review: PRD vs research, architecture and performance docs

| | |
|---|---|
| Role | Critic (uncomfortable questions) |
| Date | 2026-09-30 |
| Reviewed | `docs/PRD.md` (source of truth), `docs/research/cloud-options.md`, `docs/research/model-choice.md`, `docs/ARCHITECTURE.md`, `docs/adr/0001–0004`, `api/openapi.yaml`, `docs/db-schema.sql`, `docs/research/performance.md` |
| Checked today at the source | Modal budgets page, Modal request-timeout page, Modal preemption page, Modal vLLM example ([links in §5](#5-sources-i-checked-today)); `Qwen/Qwen3.6-35B-A3B-FP8` `config.json` and file sizes; Qwen3.6 model card (sampling, benchmark notes); vLLM releases on PyPI. Facts the lead verified today (OVH trial terms, Modal prices, Oracle A1, Qwen3.6 on HF) are used as given. |
| Labels | **ESTIMATE** = my arithmetic, not measured. **UNVERIFIED** = not confirmed at a primary source. |

---

## 0. Verdict in five lines

1. **The custom Go gateway is the right call.** The PRD fixes Go, and no off-the-shelf proxy covers FR2 (task prompts) plus FR4 (fair global queue with `Retry-After`). But the design is sized for a product, not a two-week lab. Cut it (M6).
2. **There are two blockers, and neither is in the code.** Nobody has decided where the gateway and its SQLite file run in server mode. On both chosen GPU platforms the disk is ephemeral, so all 30 keys disappear on any restart (B1). The primary cloud (OVH) cannot be hard-capped, which breaks the PRD's budget rule. Modal can be hard-capped (B2).
3. **Nobody has sized the configuration we would actually run.** `performance.md` never models Qwen3.6-35B-A3B, L40S or H100. The 24 GB plan in `model-choice.md` is a model that `performance.md` marks **FAIL** (M1).
4. **The "10-minute fallback" is not designed yet.** Laptop mode cannot serve classmates over campus Wi-Fi, and switching providers changes the students' URL (M5).
5. **The PRD's data and SQLite choices are fine.** Streaming usage accounting (ADR 0004) and the `net/http` timeout pitfall (§5.4) are good work. Keep them.

---

## 1. Is the approach right? (question 1)

| Alternative | Keys per person, hashed (FR3) | Quotas + usage without content (FR4, FR6) | Fair global queue + `Retry-After` (FR4) | Task endpoints (FR2) | Go (PRD §3) | Verdict |
|---|---|---|---|---|---|---|
| vLLM `--api-key` | One shared key; `/invocations` stays open (ADR 0001) | No | No | No | n/a | Only as break-glass (see M5) |
| Ollama + Caddy `basic_auth` | Yes (bcrypt in Caddyfile; revoke = edit + reload) | No | No | No | n/a | Fails FR4–FR6 |
| OpenWebUI | Per-user keys | Partial | No | No | No (Python) | Web UI is out of scope; heavy |
| LiteLLM proxy | Yes (virtual keys; Postgres needed for key management, per ADR 0001) | Yes (rpm/tpm, budgets, spend logs) | Per-key parallel limits, no FIFO + `Retry-After` | No | No (Python) | Strongest alternative, but it breaks the language constraint and still needs FR2 code |
| **Custom Go gateway** | Yes | Yes | Yes | Yes | Yes | **Keep.** It is also the course deliverable |

**What earns its keep:** hashed keys and the admin API (FR3, FR5); the per-key token bucket, per-key in-flight cap and daily quota (FR4); the global gate (FR4); the SSE relay with forced `include_usage` and suppression of the usage-only chunk (FR6; also keeps naive `chunk.choices[0]` code from crashing); task templates (FR2); `/healthz` and `/readyz` (FR7); the alias map `coder=…` (FR8).

**What to drop or simplify:** see M6. The PRD's "Must" list can all be delivered in two weeks, but only if the design stops polishing parts the lab will never exercise.

---

## 2. Findings, ranked

| ID | Severity | Finding |
|---|---|---|
| B1 | **blocker** | Server-mode topology is undefined. Gateway state is ephemeral on both chosen platforms, and a fallback changes the students' URL |
| B2 | **blocker** | The primary cloud (OVH) breaks the PRD budget rule. Modal can be hard-capped, so Modal should be primary unless Slava amends the PRD |
| M1 | major | Nobody has sized the model/GPU we would actually run, and the 24 GB plan is a documented FAIL |
| M2 | major | Modal needs specific settings and tests: 150 s request limit, autoscaling, keep-warm, auth, preemption |
| M3 | major | Reasoning/thinking defaults have no mechanism in the gateway, so the laptop fallback can return empty answers |
| M4 | major | Non-streaming calls, the default `openai` SDK path, are killed after 120 s (and at 150 s on Modal) |
| M5 | major | The "fallback at 10 minutes' notice" is not designed, and laptop mode cannot serve 30 classmates on campus Wi-Fi |
| M6 | major | Timeline: about 2 weeks for a design with dozens of polished parts. Cut scope and set milestones |
| M7 | major | Model risk: Qwen3.6 has a new hybrid architecture, and its benchmarks are thinking-mode numbers. Pre-decide a same-GPU fallback |
| m1 | minor | Gateway knob values differ between ARCHITECTURE and performance |
| m2 | minor | Two different specs for the load-test tool, and the performance one is oversized |
| m3 | minor | No bulk key minting, no handout workflow, no kill switch |
| m4 | minor | Over-engineered internals: strict-FIFO gate, two DB pools, `/admin/usage`, six-step shutdown |
| m5 | minor | Admin API exposed on the public internet |
| m6 | minor | Cold-start supply chain: Hugging Face download, Docker Hub pulls, image size |
| m7 | minor | `presence_penalty 1.5` on code output is untested |
| m8 | minor | Prefix caching: template order, and whether it works for the hybrid model |
| m9 | minor | OVH voucher: amount, currency and the one-month clock |
| m10 | minor | Modal academic credits are not in the sign-up checklist |

---

### B1 (blocker). Where do the gateway and its SQLite file run in server mode?

**Evidence**
- ARCHITECTURE §1, deployment table: gateway = "Container next to the model server (Jan)", upstream = "`http://vllm:8000/v1` on a private Docker network". The `.env.server.example` in §9 says the same.
- cloud-options §3, OVH: AI Deploy is one container with "**Only one port** (default 8080)", UID 42420, and "local storage is ephemeral, so the model is re-downloaded on every start". The checklist says "**Stop and delete the app right after the lab**" and "stop all AI Deploy apps after every session".
- Modal is a serverless function with no Docker network. The official vLLM example mounts Volumes only for the HF and vLLM caches, and GPU functions are preemptible (M2).
- PRD §5: "state in one SQLite file". PRD FR3: keys revocable. ARCHITECTURE §7: a key is "Shown once … never retrievable".

**Why it matters.** Neither platform has the private Docker network the architecture assumes. If the gateway runs inside the GPU container, `gateway.db` lives on ephemeral disk. Then:
- every stop, redeploy, preemption or crash deletes all keys and all usage rows;
- keys cannot be minted before lab day;
- a mid-lab restart invalidates 30 keys at once.

A switch to the backup provider changes the base URL too. At 10 minutes' notice, 30 students would need a new URL **and** a new key. PRD §3 also gives Jan "TLS/reverse proxy", while the providers already terminate TLS. So nobody knows where Jan's proxy would even sit.

**Recommended change (decide by Fri 2 Oct; owners Jan + Slava)**
1. **Run the gateway and SQLite on a small always-on CPU host with a stable HTTPS name. The GPU endpoints are upstreams only.**
   - Candidates at €0:
     - Oracle Always Free (the E2.1.Micro or A1 2 OCPU / 12 GB shape is plenty for a Go binary; an Always Free account cannot incur charges unless upgraded, UNVERIFIED wording, check at sign-up)
     - any box Jan already has
   - Put Caddy or nginx on it for TLS, plus a free DNS name (e.g. DuckDNS).
2. The GPU side is then a plain upstream:
   - `LGAI_UPSTREAM_BASE_URL=https://<provider-url>/v1`
   - upstream auth: vLLM `--api-key`, or the OVH app token in "restricted" mode. That removes the "Bearer clash" noted in cloud-options §3.
3. Payoffs:
   - keys persist and can be minted and printed days ahead;
   - the fallback becomes a server-side env change plus a restart;
   - `/admin` can bind to localhost and be reached over SSH (this closes ARCHITECTURE §17.5);
   - even laptop mode can sit behind the same public URL through a tunnel, when the venue has internet.
4. If a separate host is truly impossible, the minimum is:
   - mint keys offline and load their hashes at startup (`LGAI_KEYS_FILE`), so keys survive restarts;
   - accept that usage rows are lost on restart;
   - put the provider URL change in the fallback runbook.
5. Update ARCHITECTURE §1 and §9 and the PRD §3 split (who runs the CPU host) to match.

---

### B2 (blocker). The primary cloud breaks the PRD budget rule

**Evidence**
- PRD §3: "**Budget: €0.** Acceptable: credit programmes that need a card on file, **with hard spend caps, never exceeding credits**".
- OVH (lead-verified today): "Once this has been used up, you will be billed automatically via the added payment method", so there is no hard cap. The trial page names AI Notebooks and AI Training, **not AI Deploy**. cloud-options itself says coverage is "**inferred**" (§3, §6.1).
- Modal budgets page, checked today:
  - "Workspace budgets (also shown as your usage limit in the dashboard) cap total usage for the Workspace during the current billing cycle — **before credits are applied**."
  - "The Workspace budget is the **hard outer cap** for the entire Workspace."
  - "When the spend limit is reached, Modal stops workloads that would incur additional out-of-pocket charges … If you do not set a custom spend limit, Modal uses the cycle's usage limit minus credits."
  - Only *Environment* budgets are "available on the Team and Enterprise plans". Workspace budgets and spend limits are not plan-restricted on the page. Confirm in the Starter dashboard.
- cloud-options TL;DR says Modal's "spend limit is a hard stop". The nuance: the **default** spend limit is "usage limit minus credits", so it can be > $0 until someone sets it.

**Why it matters.** OVH breaks the letter of the PRD. The main risk is not a forgotten app (€3.10/h × 24 h ≈ €74, still inside €200) but two others:
1. **The voucher may not apply to AI Deploy at all**, in which case the card pays from minute one.
2. OVH Public Cloud is post-paid, so a low-limit virtual card does **not** cap the liability. The debt remains (UNVERIFIED for OVH specifically; typical for post-paid billing).

Modal *can* be capped: a Workspace budget ≤ $30 plus a spend limit of $0.

**Recommended change**
1. **Make Modal the primary for rehearsal and lab**:
   - on **L40S** (≈ $2.40/h all-in, ≈ 12 h per $30; see M1 for why L40S is enough);
   - Workspace budget **$28** (a margin for undocumented enforcement lag) and spend limit **$0**, both set on day 1.
2. Keep a budget ledger. Nothing may run if it would leave **< $12** for the lab: 3.5 h × $2.4 = $8.4, plus margin.
3. **OVH only with an explicit PRD amendment by Slava** (decision log entry), and with these safeguards:
   - a smoke test that proves the voucher is consumed by AI Deploy, done before any real use;
   - a scheduled auto-stop (e.g. a free GitHub Actions cron running `ovhai app stop` for every app at 20:00 daily);
   - a daily billing check.

   If Slava doesn't amend the PRD, drop OVH. Keep the GCP Cloud Run spend-cap path (cloud-options §3: caps are "computed on gross cost", which is genuinely capped) as the parallel backup.
4. Fix the cloud-options TL;DR wording: "hard stop" → "Workspace budget = hard cap on usage; spend limit must be set to $0 explicitly".

---

### M1 (major). Nobody has sized what we would actually run, and the 24 GB plan is a documented FAIL

**Evidence**
- model-choice TL;DR: the lab pick is `Qwen3.6-35B-A3B-FP8` on "L40S for dev, H100 for lab". cloud-options TL;DR: "1× L40S 48 GB (dev) / 1× H100 80 GB (rehearsal + lab)".
- performance §1.2 hardware table: T4, L4, A10G, RTX 4000 Ada, A100 40/80, M4 Pro, A1. **No L40S, no H100.** §1.3 models: Qwen2.5-Coder 7B/14B, Qwen3-Coder-30B-A3B, gpt-oss-20b, 1.5B/3B. **No Qwen3.6.** The gateway knobs in §5 are sized for the "24 GB class".
- model-choice TL;DR, 24 GB tier: "`cyankiwi/Qwen3-Coder-30B-A3B-Instruct-AWQ-4bit` … Keep … as the plan for a 24 GB GPU (e.g. Modal L4 / Cloud Run L4)".
- performance §2.3: "Qwen3-Coder-30B-A3B AWQ on L4/A10G: … **1.6 GiB** of KV … ≈ 7 sequences … SAT TTFT p95 of 70–110 s … **Not recommended below 40 GB**". Rows 7 and 12 say **FAIL**.
- performance used the 16.8 GB QuantTrio size. The cyankiwi quant that model-choice recommends is 18.1 GB, which leaves ≈ 0.4 GiB of KV, so it is worse still.

**My back-of-envelope for the real candidates** (ESTIMATE, using performance §1.4's method; Qwen3.6 config checked today: 40 layers = 10 full attention + 30 Gated DeltaNet, 2 KV heads × 256 dims → 20 KiB/token; DeltaNet state ≈ 30 MiB/sequence in bf16; 256 experts × 512 intermediate):

| Combo | KV pool (0.90 util, 3 GiB overhead → 0.92, 2 GiB) | Sequences @ 2.5K / @ 8K | Pure decode @ 30 users, η 0.55–0.7 | Burst 30 × 1.5K, TTFT of the last prompt |
|---|---|---|---|---|
| L40S × Qwen3.6-35B-A3B-FP8 (language-model-only, ≈ 36.6 GB) | 3.4 → 5.3 GiB | 44–69 / 18–29 | **18–23 tok/s** | ≈ 3–6 s |
| L40S × Qwen3-Coder-30B-A3B-FP8 (31.2 GB) | 8.5 → 10.4 GiB | 37–45 / 11–14 | 14–18 tok/s | ≈ 3–6 s |
| H100 (PCIe–SXM) × Qwen3.6-FP8 | large | ≫ 30 | 40–78 tok/s | ≈ 1–2 s |

With prefill interference (performance's SAT loses about 25–30 %), L40S lands around **13–17 tok/s per user at 30 streaming at once**. That passes the PRD's ≥ 10 tok/s, but without a big margin. H100 is comfortable but costs 1.8× per hour. At batch 30, Qwen3.6 touches only 61 % of its 256 small experts per step, against 86 % for Qwen3-Coder's 128. That, plus a 5× smaller KV, is why it decodes faster on the same card.

**Uncertain, and not in any doc:**
- how vLLM pads KV pages for hybrid (DeltaNet) models;
- FP8 block-quant MoE kernel speed on Ada (L40S);
- whether the DeltaNet state is kept in fp32 (that would double the 30 MiB).

**Recommended change**
1. The performance agent re-runs Appendix A for **{Qwen3.6-35B-A3B-FP8, Qwen3-Coder-30B-A3B-FP8} × {L40S, H100}** and re-sizes §5 for that GPU class.
2. Delete "Qwen3-Coder-30B-A3B AWQ on 24 GB" as a *lab* plan in model-choice and cloud-options. A 24 GB card is **dev-only** (1–3 users).
3. Do dev on the lab model and lab GPU (L40S), not on L4 with a different model. Otherwise prompt tuning happens against the wrong model.
4. Day-1 GPU bring-up must record vLLM's "Maximum concurrency for 8,192 tokens" line and the KV GiB. Go on L40S only if it shows ≥ 30 sequences at 8K.

---

### M2 (major). Modal needs specific settings and tests

**Evidence (checked today)**
- Timeouts: "All Web Function types (`modal.fastapi_endpoint`, `modal.asgi_app`, `modal.wsgi_app`, and **`modal.web_server`**) have a maximum HTTP request timeout of **150 seconds** enforced … an HTTP status 303 redirect response is returned". The page does **not** exempt streaming. cloud-options §3 hopes "Streaming responses probably avoid it (UNVERIFIED)".
- performance §5's own worst legitimate case is "≈ 23 s TTFT and 2,048 tokens at ≈ 8 tok/s ≈ 4.5 min".
- Preemption: "All Modal Functions are subject to preemption by default … Preemptions are rare"; "The `nonpreemptible` parameter is not supported for GPU Functions."
- The vLLM example uses `scaledown_window=15 * MINUTES`, `startup_timeout=10 * MINUTES`, `target_concurrency=100`, and Volumes for `/root/.cache/huggingface` and `/root/.cache/vllm`. It has **no API key and no proxy auth**. Cold start is "typically tens of seconds to a few minutes".
- ADR 0001: vLLM `--api-key` "only protects `/v1`, `/v2` and `/inference`; `/invocations` stays open".

**Why it matters.**
- A stream that runs past 150 s may be cut mid-answer. A non-streaming request past 150 s gets a 303 that our gateway was never designed for (see M4).
- A `*.modal.run` URL follows a predictable pattern (`<workspace>--<app>-<function>`). An open `/invocations` lets anyone who guesses it burn the $30 cap. With the cap hit, the lab is dead.
- Autoscaling or scale-to-zero during a lull in the lab means a multi-minute cold start in front of 30 people.

**Recommended change (Jan's Modal app, week 1)**
1. **Test first (≈ $1):** one 5-minute SSE stream and one 3-minute non-streaming request through `*.modal.run`.
   - If streams are cut at 150 s: set `LGAI_MAX_TOKENS_CAP` so the worst case stays < 120 s (≈ 1,024 at ≥ 10 tok/s), or try a `modal.forward` tunnel (UNVERIFIED that it avoids the limit).
2. Auth and scaling:
   - `vllm … --api-key $UPSTREAM_KEY`, **plus** Modal proxy auth (`requires_proxy_auth=True`; availability on Starter is UNVERIFIED), which closes `/invocations` at Modal's edge. This needs the gateway to send arbitrary upstream headers: add `LGAI_UPSTREAM_HEADERS`, a few lines of code.
   - `max_containers=1` (never a second GPU).
   - `target_concurrency` / `@modal.concurrent` ≥ `LGAI_MAX_INFLIGHT`, so Modal never queues or scales on our behalf.
3. Keep-warm and caching:
   - `min_containers=1` **only** for the lab window: deploy with it at T−60 min, redeploy with 0, or `modal app stop`, right after.
   - HF and vLLM caches on Volumes, so a restart after preemption takes minutes, not a 37 GB download.
4. Preemption drill in the rehearsal: kill the container during the load test and time the recovery. Put the student-facing message in the runbook (M5).

---

### M3 (major). Reasoning defaults have no mechanism in the gateway

**Evidence**
- ARCHITECTURE §17.2 lists the problem as an open question: disable thinking on the server, "Or: raise the task `max_tokens` defaults". Nothing is decided, and there is no config knob.
- performance §3.1: "The gateway should send `reasoning_effort: "low"` by default for gpt-oss", and §5 adds "+256" tokens for gpt-oss.
- Laptop mode (ARCHITECTURE §9) uses `gpt-oss:20b` with `LGAI_CHAT_DEFAULT_MAX_TOKENS=512` and `MAX_TOKENS_CAP=1024`. model-choice §4.7: gpt-oss "always reasons".
- The Qwen3.6 card: "Qwen3.6 models operate in thinking mode by default".

**Why it matters.** Suppose `--default-chat-template-kwargs` is missing on the vLLM command line (Jan's image), or we are in laptop mode with gpt-oss. Reasoning then eats `max_tokens`, and students get `finish_reason: length` with **empty `content`** after a long silence. The openai SDK prints nothing. Our fallback demo is exactly the gpt-oss path.

**Recommended change**
1. Add a per-model default: `LGAI_MODEL_DEFAULTS='coder={"reasoning_effort":"low"}'` for laptop mode, and `{"chat_template_kwargs":{"enable_thinking":false}}` for Qwen3.6. It is merged into every upstream request unless the client set the field. About 30 lines of code.
2. Keep the vLLM server-side default as well, so there are two layers.
3. Add a `smoke` check that fails on empty `content`. It is already in performance §6.3, so make it a gate for **laptop mode too**.
4. In laptop mode, measure time-to-first-*content* for gpt-oss:20b (low effort) against a non-thinking model (`qwen2.5-coder:7b`, 4.7 GB). Pick the one that shows text first. For a demo, visible text within 2 s beats benchmark points.

---

### M4 (major). Non-streaming calls die after 120 s

**Evidence**
- ARCHITECTURE §5.1: "`ResponseHeaderTimeout = UPSTREAM_HEADER_TIMEOUT` (covers a cold model load)", default `120s` (§9).
- For a non-streaming request, an OpenAI-compatible server sends headers only after the **whole** completion is generated.
- `MAX_TOKENS_CAP=2048` (server). Under load, the 24 GB-class rates in performance §2.5 are 13–16 tok/s, so 2,048 tokens take 130–160 s → **504**.
- `client.chat.completions.create(...)` without `stream=True` is the default in nearly every student snippet. Modal's 150 s limit (M2) hits the same path.

**Recommended change**
- Apply the header timeout only to `stream: true`. For non-streaming, rely on `UPSTREAM_TIMEOUT`, and clamp non-streaming `max_tokens` to what finishes in < 120 s at the rehearsal-measured rate (e.g. 1,024).
- Optionally, always stream upstream and assemble the JSON for non-streaming clients. That gives one code path, TTFT for every request, and immunity to provider request limits. Do it only if time allows (M6).
- Document "use `stream=True`" in Szymon's API guide.
- If Jan fronts the gateway with Cloudflare proxying, note its origin-response limit for non-streaming requests (UNVERIFIED value; test).

---

### M5 (major). The "fallback at 10 minutes' notice" is not designed

**Evidence**
- PRD §9 names "the laptop-mode demo as the last resort".
- cloud-options §5 "Lab day" is three lines: start 45 min early, hand out keys, stop after.
- performance §4: laptop mode handles "about **5 concurrent users** … **30 is out of reach**".
- ARCHITECTURE §17.4: exposing Ollama "on university Wi-Fi" is a concern. Nothing covers whether classmates can reach the laptop at all.
- Campus Wi-Fi (eduroam-type networks) commonly isolates clients. UNVERIFIED for UniTrento: test it.

**The single points of failure today:**
- one GPU container (preemptible on Modal; H100 availability "UNVERIFIED" on OVH);
- one gateway process plus one SQLite file (ephemeral, B1);
- one trial account and card holder (Slava);
- one HF download at every cold start (m6);
- campus Wi-Fi;
- one demo laptop that also runs Ollama;
- the provider ingress (SSE through OVH "UNVERIFIED").

**Recommended change: write `docs/runbook-lab-day.md` and rehearse it**
1. **Ladder, with a time budget for each step:**
   - **L0 (1 min):** a key problem → hand out a spare key. Mint 40 for 30 students.
   - **L1 (≤ 5 min):** the GPU container restarted or was preempted → wait for the Volume-cached restart; announce "retry in 3 minutes".
   - **L2 (≤ 10 min):** provider down → change `LGAI_UPSTREAM_BASE_URL` on the gateway host to the standby and restart. Students keep their URL and keys (needs B1).
     - The standby has its image built, weights cached and a **measured** cold start < 8 min.
     - If there is no funded standby, L2 does not exist; say so honestly.
   - **L3:** all GPUs gone → Giulia demos from the laptop running Ollama (no network needed). Szymon switches the exercises to pairs using team keys at reduced quota, or to pre-generated answers.
   - **L4:** a pre-recorded demo video (5 minutes, recorded at rehearsal).
2. **Triggers:** `/readyz` failing > 2 min, or `jq` over the access log showing TTFT p95 > 30 s for 5 min.
3. **Rehearse from campus Wi-Fi:**
   - 30 simulated clients through the public URL, not the upstream;
   - a container kill (L1);
   - a timed provider switch (L2);
   - the demo with Wi-Fi off (L3).
4. **Demo laptop:** the machine with Ollama is the machine that presents, or Giulia's demo runs against the laptop over `localhost`. Bring a phone hotspot for the demo machine only.
5. Two people hold the admin token and the cloud consoles, not only Slava.

---

### M6 (major). About two weeks; cut scope and set milestones

**Evidence**
- PRD header: lab "≈ 14–21 Oct"; PRD §9: "Plan for the earliest date (≈ 14 Oct)". That is **10 working days from today**.
- ARCHITECTURE specifies, among other things:
  - a strict-FIFO gate with hand-off and cancellation races (§8);
  - two SQLite pools (ADR 0002);
  - a six-step graceful shutdown (§12);
  - a mid-stream error taxonomy (§5.3.5–7);
  - `/admin/usage` (openapi).
- performance §6 specifies a load tester with five scenarios, ramps, `-keys-file`, an embedded corpus of 5 languages × 3 sizes × ≥ 3 snippets plus error files, and token-size tests.
- None of the GPU-side work (image, streaming test, prompt tuning, rehearsals) has started.

**Recommended change**
- **Cut or simplify.** None of these is a PRD "Must"; each is replaced by something smaller:

  | Cut | Replace with |
  |---|---|
  | Strict-FIFO gate with hand-off | Buffered-channel semaphore, an atomic queue counter and `select` with a timer. The Go runtime wakes blocked senders in FIFO order; that is an implementation detail, but good enough here |
  | Two DB pools | One `*sql.DB` with `MaxOpenConns(1)` (≤ 5 writes/s) |
  | `/admin/usage` | `GET /admin/keys/{id}/usage` (FR5) plus a documented `report.sql` for Giulia's numbers |
  | Six-step shutdown | `srv.Shutdown(30s)` + `db.Close()` |
  | The `idle`/`shutdown`/`client-gone` cause taxonomy | One generic mid-stream error event; keep the idle watchdog |
  | Load-tester scenarios `step`, `lab`, `-keys-file`, the large corpus | `-c N -d D -endpoint E`, `-burst`, `-smoke`, 5 small code files (one per language) plus 5 medium ones, and TTFT, TTFC and tok/s percentiles. `vllm bench serve` covers the raw engine curve |

- **Add** (small, and missing): `LGAI_UPSTREAM_HEADERS` (M2), `LGAI_MODEL_DEFAULTS` (M3), the non-streaming timeout fix (M4), a mint script and revoke-by-prefix (m3).
- **Milestones** (for a 14 Oct lab; shift them if the date is later):

  | Date | Milestone |
  |---|---|
  | Fri 2 Oct | Topology (B1) and cloud (B2) decided; Modal and the CPU host signed up; lab date asked of the professor |
  | Mon 5 Oct | vLLM plus the target model up on L40S; "max concurrency" logged; a 5-minute SSE through the provider URL passes; gateway MVP (auth, chat stream relay, gate, usage rows) working against Ollama |
  | Thu 8 Oct | Tasks, admin, limits and the minimal load tester done; gateway deployed on the host; end to end against the GPU |
  | Sat 10 Oct | Rehearsal 1: 30 clients for 45 min plus L1/L2 drills; Szymon reviews the 20 task×language answers |
  | Mon 12 Oct | Rehearsal 2 from campus Wi-Fi; keys minted and printed; demo video recorded |
  | Tue 13 Oct | Freeze. No deploys |

  If Mon 5 Oct slips by more than 2 days, drop the task endpoints' `framework` and `instructions` extras first, and never the rehearsal.

---

### M7 (major). Model risk: pre-decide a same-GPU fallback

**Evidence**
- The Qwen3.6-35B-A3B config (checked today) uses `layer_types` = 30 `linear_attention` + 10 `full_attention`, a Gated-DeltaNet hybrid. model-choice [M1] requires vLLM ≥ 0.19. vLLM 0.30.0 exists (PyPI, uploaded 2026-09-22; the Docker tag `v0.30.0` exists).
- Hybrid (Mamba/DeltaNet-style) serving in vLLM is newer and less exercised than standard attention MoE. Prefix caching and CUDA graphs for these layers are exactly where problems appear (UNVERIFIED for 0.30.0).
- The Qwen3.6 card's benchmark notes: "SWE-Bench Series: Internal agent scaffold … temp=1.0, top_p=0.95, 200K context window". Those are thinking-mode sampling settings. We run it with thinking **off**, so the benchmark gap to non-thinking coder models is overstated.
- No benchmark in model-choice measures snippet-level *explain / review / tests / fix*, which is what students will do. SWE-bench Multilingual is agentic repo repair.

**Recommended change**
1. Day-1 bring-up checklist for Qwen3.6-FP8 on L40S:
   - it loads with `--language-model-only`;
   - KV ≥ 30 sequences at 8K;
   - thinking is off by default (no `reasoning` deltas);
   - prefix caching is reported as active;
   - 30 parallel streams run for 10 min without errors.
2. **Pre-decided fallback on the same GPU: `Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8`** (official, 31.2 GB, non-thinking, standard Qwen3-MoE). It is the same image with a different model id, so the switch costs one redeploy.
3. The real model gate is the performance §6.3 `smoke` run (20 task×language pairs), **judged by Szymon**. Take whichever model gives better Java/C++/Go answers on his exercises.

---

### m1 (minor). Gateway knob values differ between ARCHITECTURE and performance

| Knob | ARCHITECTURE §8/§9 | performance §5 |
|---|---|---|
| `QUEUE_SIZE` (server) | 64 | 24 |
| `RATE_RPM` / burst | 20 / 5 | 6 / 3 |
| `TOKEN_QUOTA_DAILY` | 200,000 | 300,000 |
| Chat default `max_tokens` | 1,024 | 512 |
| Upstream idle timeout | 60 s | 30 s |
| First token / header timeout | 120 s | 60 s |
| 503 `Retry-After` | fixed 5 s | `ceil(queue/drain)`, 5–60 s |
| Input cap | 16 KiB bytes ≈ 4k tokens | 6,000 tokens |
| vLLM `--max-model-len` | — | 8,192 (model-choice: 16,384 on 48 GB) |

ARCHITECTURE §17.1 says its values are placeholders, so this is expected, but nobody owns the reconciliation. **Change:** after M1, performance publishes one table for L40S (and H100), ARCHITECTURE §9 copies it, and `Retry-After` stays **static** (simpler; the SDK honours it either way).

### m2 (minor). Two different specs for the load-test tool

ARCHITECTURE §14 gives `-c -n -d -endpoint -body-file -warmup -json`. performance §6.2–6.4 gives `-scenario smoke|step|lab|burst|limits`, `-levels`, `-users`, `-ramp`, `-keys-file` and an embedded corpus. **Change:** one spec, the minimal one in M6, owned by ARCHITECTURE.

### m3 (minor). No bulk minting, no handout workflow, no kill switch

**Evidence.** openapi `POST /admin/keys` creates one key; ARCHITECTURE §7: "Shown once". There is no global off switch.

**Change**
- `scripts/mint-keys.sh 40 lab-seat- > keys.csv` (a curl loop), then a printable sheet: one slip per seat with the key, the base URL, and a 3-line Python snippet with `stream=True`. Destroy the CSV after the lab.
- `DELETE /admin/keys?name_prefix=lab-seat-` to revoke a whole class.
- `LGAI_PAUSED` or `POST /admin/pause` (every generation → 503) as the abuse kill switch.
- The **cost** kill switch is at the provider: `modal app stop` / `ovhai app stop`, scripted and rehearsed.

### m4 (minor). Over-engineered internals

Covered in M6: the strict-FIFO hand-off gate, two DB pools, `/admin/usage`, the six-step shutdown and the cause taxonomy. Each one is correct but costs tests and days. None of them changes what 30 students experience.

### m5 (minor). Admin API on the public internet

ARCHITECTURE §17.5 leaves this open. **Change:** with B1's separate host, bind admin to `127.0.0.1:8081` and use it over SSH. This is one more listener, not a new feature. It is the security agent's call, but it is cheap.

### m6 (minor). Cold-start supply chain

**Evidence.** OVH: "the model is re-downloaded on every start (~37 GB)" (cloud-options §3). `vllm/vllm-openai` images are many GB, and Docker Hub rate-limits anonymous pulls per IP; shared cloud egress IPs make that worse (UNVERIFIED for OVH).

**Change:**
- Cache the weights (Modal Volume; OVH Object Storage attached as a cached volume).
- Use an HF token for downloads.
- Push the wrapper image to a registry Jan controls (e.g. GHCR) and pin it by digest.
- Measure a cold start end to end in rehearsal.

### m7 (minor). `presence_penalty 1.5` on code output

model-choice §4.1 recommends the card's non-thinking row (`presence_penalty 1.5`). The same card recommends `presence_penalty=0.0` "for precise coding tasks" (in thinking mode). A 1.5 penalty on every repeated token can distort code, where identifiers must repeat. **Change:** A/B 1.5 against 0.0 on the `tests` and `fix` smoke outputs, and let Szymon judge.

### m8 (minor). Prefix caching

performance §3.1 counts on prefix caching for the "everyone paste exercise 1" burst. **Change:**
- Order the task templates as `system prompt → exercise code → per-request fields (language hints, instructions, error)`, so the shared prefix is maximal.
- Check the vLLM log/metrics that prefix caching is actually active for the hybrid model (UNVERIFIED).

### m9 (minor). OVH voucher amount, currency and clock

cloud-options says "€200". The lead verified "US$ 200 free credits (amount varies by country)". The voucher runs one month from **first project creation**. **Change:**
- Say "≈ 200 in local currency; check the IT page".
- If OVH is kept (B2), create the project only once the lab date is confirmed ≤ 3 weeks out. Create the account and pass any ID check now.

### m10 (minor). Modal academic credits

cloud-options §2 mentions "academic grants 'up to $10k'" but the §5 checklist omits them. **Change:** Slava applies today. It takes 10 minutes and could turn a $30 lab into a comfortable one. Plan as if the grant will not arrive.

---

## 3. Contradictions between the documents

| # | Document A says | Document B says | Resolution |
|---|---|---|---|
| C1 | model-choice TL;DR: 24 GB tier = `Qwen3-Coder-30B-A3B AWQ`, "Keep … as the plan for a 24 GB GPU (e.g. Modal L4 / Cloud Run L4)" | performance §2.3 + rows 7/12: that combo **FAILS** (KV ≈ 7 sequences; SAT TTFT p95 73–112 s); "Not recommended below 40 GB" | performance is right. 24 GB = dev only (M1) |
| C2 | cloud-options + model-choice: lab = Qwen3.6-35B-A3B-FP8 on L40S / H100 | performance: never models Qwen3.6, L40S or H100; knobs sized for the "24 GB class" | re-run performance for the real combos (M1) |
| C3 | model-choice 80 GB pick: Qwen3.6-35B-A3B-FP8 | performance §2.5 80 GB pick: Qwen3-Coder-30B-A3B (BF16/FP8) | Qwen3.6 primary, Qwen3-Coder-FP8 as the pre-decided fallback (M7) |
| C4 | model-choice §5: Qwen2.5-Coder-7B "superseded", keep only for laptop tests or T4 | performance §0/§2.5/§7: Qwen2.5-Coder-7B-AWQ is a 24 GB **PASS** pick and the first NO-GO fallback | moot if the lab runs on ≥ 48 GB; otherwise note the quality cost |
| C5 | model-choice §4.7: gpt-oss on Ada with vLLM is "actively working … UNVERIFIED" | performance row 6: gpt-oss-20b on L4 with vLLM **PASS** (devforth ran vLLM 0.15.1 on L4) | minor; only matters for a 24 GB plan |
| C6 | cloud-options TL;DR: Modal "spend limit is a hard stop" | Modal docs: the default spend limit = usage limit − credits; the **Workspace budget** is the hard cap; both must be set | set both explicitly (B2) |
| C7 | cloud-options §3: the 150 s limit is probably avoided by streaming (UNVERIFIED) | Modal docs: the limit applies to all web function types including `modal.web_server`, with no streaming exemption stated. performance §5: legitimate streams up to ≈ 4.5 min | test on day 1; cap `max_tokens` (M2) |
| C8 | ARCHITECTURE §1/§9: gateway + vLLM on a private Docker network behind Jan's TLS proxy | cloud-options: OVH = one container, one port, ephemeral disk, provider HTTPS; Modal = serverless function, provider HTTPS | separate gateway host (B1) |
| C9 | ARCHITECTURE §8/§9 knob values | performance §5 knob values | one table (m1) |
| C10 | ARCHITECTURE §14 load-test CLI | performance §6 load-test CLI | one minimal spec (m2) |
| C11 | model-choice 48 GB tier: "Leaves roughly 4–7 GB for KV" (0.92 util, 2 GB overhead), `--max-model-len 16384` | performance conventions (0.90 util, 3 GiB MoE overhead) give ≈ 3.4 GiB; performance uses `--max-model-len 8192` | measure the vLLM log on day 1; use 8,192 unless ≥ 30 sequences fit at 16K |
| C12 | cloud-options: "€200" OVH voucher | lead-verified: "US$ 200 (amount varies by country)" | m9 |
| C13 | cloud-options §4: the Modal budget fits because "dev on L4 (~$6)", then rehearsal + lab on L40S/H100 | performance: the 24 GB model fails (C1), so L4 dev means prompt tuning on a *different* model. cloud-options §1's "plan 15 h" on L40S would be ≈ $36 > $30 | dev on the lab model and GPU (L40S) with a strict ledger; laptop mode for gateway dev (B2, M1) |

---

## 4. Numbers: sanity check (question 4)

- **KV per token.** The formula `2 × layers × KV heads × head_dim × bytes` is applied correctly to every model in performance §1.3. I re-derived Qwen3.6 from today's `config.json`: 10 full-attention layers × 2 × 2 × 256 × 2 B = **20,480 B/token**. Gated DeltaNet: 30 layers × 32 heads × 128 × 128 × 2 B = **30 MiB per sequence**. Both match model-choice §4.5.
- **Bandwidth-bound decode with an expert-touch fraction `1 − (1 − k/E)^B`.** The method is sound and the right one for MoE at batch 30. The constants are calibrated on batch-1 data, as performance §8 admits. The ±30 % band is honest.
- **TTFT under a 30-user burst.** The method is sound (total prompt tokens ÷ prefill rate). For the real candidates it is ≈ 3–6 s on L40S and ≈ 1–2 s on H100 (ESTIMATE, §2 M1). Much better than the 11–23 s headline in performance §0.2, which is for 24 GB cards.
- **Realistic lab load.** SAT (zero think time) is a deliberate worst case, and LAB (30 s think time) is still aggressive for students who read and write code between requests. Real risk sits in the burst at the start of each exercise and in long `tests` outputs, not in steady state.
- **Data volumes.** 30 students × ≤ 100 requests ≈ 3,000 usage rows per lab, well under 1 MB, at ≤ 5 writes/s. SQLite in WAL mode is massively sufficient. The quota `SUM` per request scans ≈ 100 indexed rows. **No change**, except that one pool is enough (m4).
- **Modal budget.** GPU per-second prices (lead-verified) plus ≈ $0.45/h CPU and RAM:
  - L4 ≈ $1.24/h (24 h per $30)
  - L40S ≈ $2.40/h (12.5 h)
  - A100-80 ≈ $2.94/h (10 h)
  - H100 ≈ $4.39/h (6.8 h)

  A realistic plan: dev and prompt tuning 3 h, bring-up and streaming tests 1 h, rehearsals 1.5 h, lab 3.5 h, all on L40S, ≈ 9 h ≈ **$22**. That leaves ≈ $6–8 of margin, and **no room for H100 in the lab**, or for a forgotten `min_containers=1` overnight.

---

## 5. Sources I checked today

- Modal budgets and spend limits: https://modal.com/docs/guide/budgets
- Modal request timeouts: https://modal.com/docs/guide/webhook-timeouts
- Modal preemption: https://modal.com/docs/guide/preemption
- Modal vLLM example: https://modal.com/docs/examples/vllm_inference
- Qwen3.6-35B-A3B-FP8 `config.json` and file sizes (37.49 GB total): https://huggingface.co/Qwen/Qwen3.6-35B-A3B-FP8 (API `?blobs=true`)
- Qwen3.6-35B-A3B card, sampling and benchmark notes: https://huggingface.co/Qwen/Qwen3.6-35B-A3B
- vLLM 0.30.0 on PyPI (uploaded 2026-09-22): https://pypi.org/pypi/vllm/json. Docker Hub tag `vllm/vllm-openai:v0.30.0` returns 200.

---

## Decisions I'd make now

1. **Topology (B1):**
   - Gateway and SQLite run on a small always-on CPU host with a stable HTTPS name, fronted by Jan's proxy.
   - GPU endpoints are upstreams only. A fallback is an env change on that host.
   - Record this in ARCHITECTURE §1 and in the PRD split with Jan.
2. **Cloud (B2):**
   - **Modal is the primary** on **L40S** for dev, rehearsal and lab. Set a Workspace budget of $28 and a spend limit of $0 today.
   - Keep a ledger with a $12 lab reserve.
   - Apply for Modal academic credits today.
   - OVH only if Slava writes an explicit PRD amendment accepting the uncapped card, with a voucher-consumption check and a scheduled auto-stop. Otherwise, GCP Cloud Run with a spend cap is the parallel backup.
3. **Model (M1, M7):**
   - Qwen3.6-35B-A3B-FP8 on L40S, conditional on the day-1 checklist.
   - Pre-decided fallback on the same GPU: Qwen3-Coder-30B-A3B-Instruct-FP8.
   - Drop "Qwen3-Coder AWQ on 24 GB" as a lab plan.
   - Szymon's smoke review picks the winner.
4. **Performance (M1, m1):** re-run the model for {Qwen3.6-FP8, Qwen3-Coder-FP8} × {L40S, H100}. Publish one knob table and copy it into ARCHITECTURE §9.
5. **Gateway scope (M6):**
   - **Cut:** strict-FIFO gate, second DB pool, `/admin/usage`, six-step shutdown, load-tester scenarios beyond `-c/-d/-burst/-smoke`.
   - **Add:** `LGAI_UPSTREAM_HEADERS`, `LGAI_MODEL_DEFAULTS` (thinking off / `reasoning_effort: low`), a header timeout for streams only, non-streaming `max_tokens` ≤ 1,024, a mint script, revoke-by-prefix, a pause switch.
6. **Day-1 tests on the GPU (≈ $2):**
   - a 5-minute SSE stream and a 3-minute non-streaming request through the provider URL;
   - the vLLM "max concurrency" line;
   - thinking is off;
   - a cold-start time from cached weights.
7. **Lab-day runbook (M5):**
   - the L0–L4 ladder with triggers;
   - 40 keys minted and printed;
   - a demo video recorded at rehearsal;
   - the L1/L2/L3 drills rehearsed from campus Wi-Fi;
   - two people with admin and console access.
8. **Freeze on the day before the lab.** If the gateway MVP is not end-to-end on the GPU by Thu 8 Oct, ask the professor for the later lab date (≈ 21 Oct) rather than cutting the rehearsal.
