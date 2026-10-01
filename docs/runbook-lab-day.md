# Lab-day runbook

Sources: `DECISIONS.md` D1, D3, D7; `reviews/critic.md` M5, M6; `reviews/security.md` §7; `research/performance.md` §7; `docs/HANDOFF-jan.md`.

- **Lab date: not known yet** (Slava asks the professor). Everything is relative to **T = lab start**: T-9d is 9 days before, T-60 is 60 minutes before.
- **Topology** (D2, decided): students → `https://HOST` (Caddy) → gateway + SQLite on an always-on CPU VM (`/opt/lgai`, Jan) → vLLM on Modal (one L40S) as the upstream only. Keys live on the VM, so GPU trouble never changes students' URL or keys. Replace `HOST` with the public name.

## 0. Cheat sheet

| What | Command |
|---|---|
| Health | `curl -s https://HOST/healthz`, `curl -s https://HOST/readyz` (`ready` only while the GPU runs) |
| Admin tunnel | `ssh -N -L 8081:127.0.0.1:8081 <vm>`; the scripts then use `http://127.0.0.1:8081` |
| Admin token in the shell | `read -rs LGAI_ADMIN_TOKEN && export LGAI_ADMIN_TOKEN` (paste from the password manager; nothing lands in history) |
| Mint keys | `PUBLIC_BASE_URL=https://HOST/v1 bash scripts/mint-keys.sh 40 lab- <T + lab length + 1 h, RFC 3339>` |
| Revoke one key | `curl -sS -X DELETE -K <(printf 'header = "Authorization: Bearer %s"\n' "$LGAI_ADMIN_TOKEN") http://127.0.0.1:8081/admin/keys/<id>` |
| Revoke a class | `bash scripts/revoke-prefix.sh lab-` |
| Maintenance (generation → 503) | `bash scripts/maintenance.sh on` / `off` / `status` |
| GPU warm for the lab | `KEEP_WARM=1 modal deploy lgai_vllm.py` |
| GPU back to zero | `modal deploy lgai_vllm.py`; kill switch: `modal app stop lgai-vllm` |
| Smoke (20 answers) | `LGAI_KEY=<team key> loadtest -url https://HOST -smoke -out loadtest-results/smoke-labday` |

Live monitoring, on the VM in `/opt/lgai`:

```sh
# status mix of chat/task requests, last 5 min
docker compose logs --no-log-prefix --since 5m gateway | jq -Rn '[inputs | fromjson? | select(.msg=="request" and ((.route // "") | startswith("POST /v1/"))) | .status] | group_by(.) | map({(.[0]|tostring): length}) | add'
# TTFT p95 in ms, last 5 min (ttft_ms is -1 when not applicable)
docker compose logs --no-log-prefix --since 5m gateway | jq -Rn '[inputs | fromjson? | select(.msg=="request" and (.ttft_ms // -1) >= 0) | .ttft_ms] | sort | if length > 0 then .[(length*0.95|ceil)-1] else null end'
# readiness every 10 s
while sleep 10; do printf '%s ' "$(date +%T)"; curl -s -o /dev/null -w '%{http_code}\n' https://HOST/readyz; done
```

The admin token never goes on a command line, a slide or the projector. Build the load tester with `go build -o bin/loadtest ./cmd/loadtest`.

## 1. Countdown (critic M6 milestones, shifted to T)

- [ ] **T-12d:** Modal signed up; Workspace budget $28, spend limit $0, ledger started (D1). Oracle account (home region Frankfurt, permanent) and DNS name ready. Lab date asked.
- [ ] **T-9d:** vLLM on L40S passes the day-1 checks in `HANDOFF-jan.md` §7: both auth layers, ≥ 30 sequences at 8K, thinking off, and the **streams-past-150 s test**. Result decides `LGAI_MAX_TOKENS_CAP`: 2048 on pass, 1536 otherwise.
- [ ] **T-7d: Oracle idle reclamation.** Oracle may reclaim an Always Free instance whose CPU (p95), network and memory (A1) all stay below 20 % for 7 days; an idle gateway meets all three (`HANDOFF-jan.md` §1). So:
  - [ ] Create the VM no earlier than about now (Caddy + gateway per `HANDOFF-jan.md` §3–4).
  - [ ] From here to T, check daily that the VM still exists and `https://HOST/healthz` answers. Keep the image tarball, `compose.yml`, `Caddyfile` and `.env` (in the password manager) ready to redeploy on a fresh VM in ≈ 15 min.
  - [ ] **DB backup** after every change to keys (and right after minting at T-1d): `HANDOFF-jan.md` §3 "Backup". A restored DB keeps every minted key valid, so slips stay usable on a new VM.
- [ ] **T-6d:** Gateway deployed on the VM, end to end against the GPU; security smoke (security §6 I) passes.
- [ ] **T-4d:** Rehearsal 1: `loadtest -c 30 -d 10m` through `https://HOST`, plus the L1 and L2 drills (§5). Szymon judges the 20 smoke answers (D3 model gate).
- [ ] **T-2d:** Rehearsal 2 **from campus Wi-Fi**, plus the L3 drill (demo with Wi-Fi off). L4 demo video recorded.
- [ ] **T-1d:** freeze (§2).

## 2. T-1 day: freeze

- [ ] **Freeze.** No deploys after today (D7). Note the commit and the image digest that will run.
- [ ] `go vet ./...`, `go test -race ./...` and `govulncheck ./...` are clean (security §6 H).
- [ ] vLLM pinned (≥ 0.11.1), GHSA list re-checked (security #11). Flags as in `HANDOFF-jan.md` §6: `--language-model-only`, thinking off, `--disable-fastapi-docs`, no `--enable-log-requests`.
- [ ] **Fresh secrets** (`openssl rand -base64 32`): admin token, vLLM API key, Modal proxy token. They sit in the password manager and reach the VM via `.env` (0600) or `*_FILE`; never in shell history or chat (security §7.1).
- [ ] **Two people** hold the admin token, SSH to the VM and the Modal console (D7, critic M5).
- [ ] **Money** (D1): the ledger shows ≥ $12 left for the lab. vLLM app deployed with `min_containers=0`.
- [ ] `LGAI_MAX_TOKENS_CAP` matches the T-9d streaming result.
- [ ] **Rehearsal numbers** meet the GO column of performance §7 (TTFC p95 ≤ 5 s, decode tok/s p50 ≥ 12, errors 0 %). Drill times written down: L1 ____ min, L2 ____ min (or "no funded standby").
- [ ] **No unlimited keys** active: in `GET /admin/keys` every key has `max_inflight` ≤ 4 and a quota > 0; the rehearsal load-test key is revoked (security #12, §7.1).
- [ ] **Keys minted** (40 for 30 students, D7), expiring 1 h after the lab ends; slips printed; `keys.csv` (0600) only on the admin laptop. Then take a DB backup (T-7d item) and check the VM is still alive.
- [ ] **Laptop fallback (L3) ready:** `ollama pull gpt-oss:20b`; `.env` from `.env.laptop.example`; the gateway starts natively; demo prompts rehearsed; phone hotspot charged (for the demo machine only).
- [ ] **L4:** the demo video is on two devices.

## 3. T-60 min: bring-up

- [ ] **T-60** `KEEP_WARM=1 modal deploy lgai_vllm.py` (keep-warm only for the lab window, D1). Weights load from the Modal Volume. Until a container runs, Modal answers 503 and `/readyz` is not ready: expected.
- [ ] `modal app logs lgai-vllm`: "Maximum concurrency for 8,192 tokens" ≥ 30.
- [ ] VM: `docker compose ps` shows `healthy`; `https://HOST/readyz` returns `{"status":"ready",...}`.
- [ ] Monitoring terminal open (§0).
- [ ] **T-40** Smoke with the team key exits 0; open 2–3 answers. Smoke waits out 429s, so allow about 5 minutes.
- [ ] **T-20** Giulia's demo key exported in her shell, off-screen (security §7.5).
- [ ] **T-15** Hand out the slips (§4).

## 4. Key handout

- [ ] One slip per student, in person. Spares stay with the admin.
- [ ] Never: keys on slides, the projector, group chat, Moodle, a shared doc, or email lists (security §7.2).
- [ ] Say it once (Szymon): key in an env var, not in code; don't push it; one person, one key; it stops after the lab; use `stream=True`.
- [ ] No seat-to-name list is written down (security #13).

## 5. During the lab: fallback ladder

**Triggers** (critic M5): `/readyz` fails for > 2 min, or TTFT p95 > 30 s for 5 min, or the status mix shows mostly 502/503/504. The admin on duty decides the step and announces it.

| Step | Situation | Action | Budget | Tell students |
|---|---|---|---|---|
| **L0** | One student: 401, lost key, leaked key | Hand out a spare slip; if the key leaked, revoke it (§6) | 1 min | – |
| **L1** | GPU container restarted or preempted (`/readyz` 503, 502/503 spike) | Wait for the Volume-cached restart. Optional: `maintenance.sh on` so clients get a clean 503 + `Retry-After`, then `off` | ≤ 5 min | "Retry in 3 minutes; your key stays valid." |
| **L2** | Modal down, or no restart after 5 min | **An env change on the VM:** in `/opt/lgai/.env` point `LGAI_UPSTREAM_BASE_URL` (plus `LGAI_UPSTREAM_API_KEY`/`LGAI_UPSTREAM_HEADERS`, and `LGAI_MODELS` if the model id differs) at the standby, then `docker compose up -d` (this restarts the gateway, so maintenance is OFF again). Streams in flight are cut; nothing else changes: **students keep their URL and keys** (they are in SQLite on the VM). Needs a warm standby (D1 backup: GCP Cloud Run GPU, or a UniTrento GPU) with a measured cold start < 8 min. **No funded standby = no L2: go to L3** | ≤ 10 min | "Same URL and key, back in 10 minutes." |
| **L3** | All GPUs gone | Giulia demos from the laptop: Ollama + gateway natively on `localhost`, no network. Szymon switches to pairs with team keys at reduced quota, or to pre-generated answers. The laptop serves ~5 users, never the class (performance §4) | 5 min | "We continue in pairs / with prepared answers." |
| **L4** | Laptop fails too | Play the recorded demo video | 1 min | – |

## 6. Leaked or misused key

1. **Identify.** The first 12 characters on the slip are the `key_prefix`; `keys.csv` maps it to the key id. Or find the `key_id` in the access log: `docker compose logs --no-log-prefix gateway | jq -Rn 'inputs | fromjson? | select(.msg=="request" and .key_id==N)'`.
2. **Revoke** it: `DELETE /admin/keys/<id>` (§0). The next request gets 401; running streams finish within their `max_tokens`.
3. **Replace** it with a spare slip. Nothing restarts.
4. **Public leak** (GitHub, chat): 401s for that `key_id` in the log confirm the revocation.
5. **Mass leak** (e.g. a photo of the slips): `maintenance.sh on` → `revoke-prefix.sh lab-` → `OUT_DIR=batch2 bash scripts/mint-keys.sh 40 lab2- <expiry>` → print and hand out → `maintenance.sh off`.

## 7. Kill switches, least to most disruptive (security §7.4)

1. Revoke the offending key(s): `DELETE /admin/keys/<id>` or `revoke-prefix.sh <prefix>`.
2. `bash scripts/maintenance.sh on`: every generation request gets 503 `maintenance`; health, models and admin keep working.
   Maintenance resets to OFF on restart; re-enable it after any restart if needed (`maintenance.sh status` shows the current state).
3. Stop the gateway: `docker compose stop gateway` on the VM (SIGTERM; streams drain for up to 30 s).
4. `modal app stop lgai-vllm`: the GPU and its billing stop. The Modal spend limit ($0 out of pocket) is the hard backstop.

## 8. After the lab (same day)

- [ ] `bash scripts/revoke-prefix.sh --all --yes`.
- [ ] `modal app stop lgai-vllm`, then delete the app; delete the Modal proxy token. Next day: check billing and close the ledger.
- [ ] Aggregate numbers for Giulia, **without key names**: on the VM, `docker compose stop gateway` (checkpoints the WAL), `docker cp "$(docker compose ps -aq gateway)":/data/gateway.db ./gateway.db`, run `sqlite3 -readonly gateway.db < scripts/report.sql`, keep only the aggregate output.
- [ ] Delete: that DB copy and its `-wal`/`-shm`, the volume (`docker compose down -v`), `keys.csv`, `keys-slips.md`, every `.env` holding real secrets (`/opt/lgai/.env` included), and `rehearsal.json` if it will be published. The VM itself can go too.
- [ ] Shred the printed slips (used and spare).
- [ ] Delete provider and Caddy logs where they exist.
- [ ] Rotate the admin token if this configuration is ever reused.
