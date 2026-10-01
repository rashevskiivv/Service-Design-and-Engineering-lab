# Handoff for Jan: gateway VM + Modal GPU

Topology (DECISIONS D2, decided by Slava): students → `https://<name>` (Caddy on a small always-on VM) → gateway container + SQLite on the VM's persistent disk → Modal (vLLM, one L40S) as the upstream only. Keys live on the VM, so a GPU restart or a provider switch never changes students' URL or keys.

Inputs: `reviews/security-code.md` §4 (items J1–J9), `handoff/slava-signup-steps.md` (Oracle, Azure, GCP facts, re-checked 2026-10-01). Items marked **UNVERIFIED** were not tested by us; the day-1 checks in §8 settle most of them.

## 1. VM

**Size.** 1 vCPU / 1 GB RAM / 10 GB disk is plenty: the gateway is one Go process that relays bytes, plus one SQLite file. Any Linux VM with Docker works.

**Oracle Always Free (first choice, Slava signs up):**
- Shape `VM.Standard.A1.Flex` (Arm, so the image is `linux/arm64`). Always Free covers "1,500 OCPU-hours + 9,000 GB-hours per month, equivalent to 2 OCPUs and 12 GB". **1 OCPU / 6 GB** is plenty and may fit capacity more easily (UNVERIFIED). Stay inside the Always Free limits from the start; never upgrade the account to Pay As You Go.
- **The home region is permanent**, and Always Free exists only there. **Frankfurt** (3 availability domains) beats Milan or Turin (1 each): on "Out of host capacity" you can try another AD instead of only waiting.
- Fallback inside Oracle: `VM.Standard.E2.1.Micro` (1/8 OCPU, 1 GB, amd64). Probably enough for the gateway's few KB/s of streams (UNVERIFIED under load).
- **Idle reclamation.** Oracle's docs: "Idle Always Free compute instances may be reclaimed … if, during a 7-day period … CPU utilization for the 95th percentile is less than 20%, Network utilization is less than 20%, Memory utilization is less than 20% (A1 only)" ([Always Free resources](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm), verified by the lead). An idle gateway meets all three. So:
  - Create the VM **at most about a week before the lab** (runbook T-7d), and check daily that it still exists.
  - Keep the redeploy bundle ready (image tarball, `compose.yml`, `Caddyfile`, `.env` in the password manager): a fresh VM in ≈ 15 min.
  - Keys are minted late (runbook T-1d) and the **DB is backed up off the VM** after every key change (§3 Backup). A restored DB keeps every printed slip valid.

**If Oracle fails:** Azure for Students, with no card. The subscription is disabled when the $100 credit runs out, so it cannot charge. Use `Standard_B2ats_v2` (amd64) or `B2pts_v2` (Arm). Not recommended: a GCP e2-micro. The doc writer reported that its external IPv4 is billed hourly beyond a tiny free tier, so it would not be €0 (**UNVERIFIED**: the lead could not confirm it).

**Always:**
- Persistent disk (the Docker volume holds keys and usage).
- A stable DNS name with an A record to the VM.
- Outbound HTTPS to Modal. Nothing inbound except §4.

## 2. Image

Build on a laptop for the VM's architecture and ship it:

```sh
docker buildx build --platform linux/arm64 -t local-generative-ai:lab --load .   # amd64 VM: linux/amd64
docker save local-generative-ai:lab | gzip > lgai-lab.tar.gz                     # keep it: redeploy bundle
ssh <vm> 'gunzip | docker load' < lgai-lab.tar.gz
```

For the lab image (J9):
- Pin both `FROM` lines by digest first (see the Dockerfile header).
- Run `govulncheck ./...` the day before.

## 3. Run the gateway (`/opt/lgai` on the VM)

`/opt/lgai/.env`:
1. Copy `.env.server.example` and `chmod 600` it.
2. Fill in `LGAI_ADMIN_TOKEN`, `LGAI_UPSTREAM_BASE_URL`, `LGAI_UPSTREAM_API_KEY` and `LGAI_UPSTREAM_HEADERS` (the Modal proxy token).

As shipped, the gateway refuses to start; that is intended. The file already carries the review fixes, which are also the image defaults:
- chat input cap `LGAI_CHAT_MAX_INPUT_BYTES=16384`, so it fits vLLM's 8K context with 1.5K of output;
- body-read timeout `LGAI_BODY_READ_TIMEOUT=10s` (security-code V1);
- the review task's default is 1024 tokens.

The startup log line shows the effective values.

`/opt/lgai/compose.yml`:

```yaml
name: lgai
services:
  gateway:
    image: local-generative-ai:lab
    env_file: .env
    ports:
      # J4: loopback only, exactly like this. Only Caddy reaches 8080; admin only via SSH tunnel.
      - "127.0.0.1:8080:8080"
      - "127.0.0.1:8081:8081"
    volumes:
      - gateway-data:/data      # SQLite: keys + usage; survives restarts and image updates
    read_only: true
    tmpfs: ["/tmp:size=16m,mode=1777,noexec,nosuid,nodev"]
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    healthcheck:
      test: ["CMD", "/gateway", "healthcheck"]
      interval: 30s
      timeout: 5s
      retries: 3
    stop_grace_period: 40s
    restart: unless-stopped
    logging: {driver: json-file, options: {max-size: "10m", max-file: "3"}}
volumes:
  gateway-data:
```

```sh
cd /opt/lgai && docker compose up -d && docker compose ps     # "healthy"
curl -s http://127.0.0.1:8080/readyz                           # "ready" only while the GPU runs
```

- Use `docker compose`, not `docker run --env-file`; the latter keeps the single quotes in `.env`.
- Never run the gateway binary natively on the VM with this `.env`: `LGAI_ADDR=:8080` would bind every interface (J4).
- Changing `.env` (e.g. the L2 provider switch) means editing it and then running `docker compose up -d`. The restart turns maintenance mode off.

**Backup** (runbook T-7d and after every key change). The image has no shell, so back up from the host. sqlite3's `.backup` is safe while the gateway runs.

```sh
sudo apt-get install -y sqlite3                                   # once
V=$(docker volume inspect lgai_gateway-data -f '{{.Mountpoint}}')
sudo sh -c "umask 077; sqlite3 '$V/gateway.db' \".backup '/root/gateway-$(date +%F-%H%M).db'\""
# then copy it OFF the VM (a reclaimed VM takes local backups with it), e.g. to the admin laptop (0600):
#   ssh <vm> 'sudo cat /root/gateway-<stamp>.db' > gateway-<stamp>.db
```

The backup holds key hashes and pseudonymous usage, never plaintext keys. Still treat it as sensitive and delete it after the lab.

Restore on a fresh VM (**UNVERIFIED**, rehearse it once):
```sh
docker compose up -d && docker compose stop                       # creates the volume, owned by 65532
V=$(docker volume inspect lgai_gateway-data -f '{{.Mountpoint}}')
sudo rm -f "$V/gateway.db" "$V/gateway.db-wal" "$V/gateway.db-shm"
sudo install -o 65532 -g 65532 -m 600 gateway-<stamp>.db "$V/gateway.db"
docker compose start
```

## 4. Caddy (TLS), per-IP limits, firewall

`/etc/caddy/Caddyfile`:

```caddy
{
	auto_https disable_redirects
	servers {
		timeouts {
			read_header 10s       # J2 (Caddy default 1m); keep idle at its 5m default
		}
	}
	# No `log_credentials`, ever.
}

lgai.example.org {
	request_body {
		max_size 256KB
	}
	# The public listener has no /admin anyway (security #1); belt and braces.
	respond /admin* 404
	reverse_proxy 127.0.0.1:8080 {
		request_buffers 256KB      # J1: read the whole body before contacting the gateway (security-code V1)
		header_up X-Request-ID {http.request.uuid}
	}
	header {
		-Server
		Strict-Transport-Security "max-age=86400"   # J3, optional
	}
}
```

Notes (Caddy docs, checked 2026-09-30 and 2026-10-01):
- **SSE:** `text/event-stream` responses are flushed immediately, so no `flush_interval` is needed. Do **not** set `flush_interval -1`: that mode "does not cancel the request to the backend even if the client disconnects", and a disconnect should stop generation.
- **Timeouts:** no response timeout by default, so long streams are fine. Do **not** add `timeouts { read_body … }` without testing a stream longer than its value. The docs call it "a hard limit on the whole upload", and it may cancel running streams like Go's `ReadTimeout` (UNVERIFIED for Caddy).
- **Logs, no body and no `Authorization`:**
  - Caddy never logs request bodies.
  - If you enable `log`, `Authorization` is written as `REDACTED` by default.
  - The gateway's own Docker log has no content and no keys by design.
  - Leave Caddy access logs off unless you need them.

**Per-IP limits** (security #20). These are only against an unauthenticated flood; fairness between students comes from the gateway's per-key limits.
- **Campus NAT caveat:** the whole class may reach you from a few campus NAT addresses (UNVERIFIED for UniTrento, so check the source IPs at rehearsal 2). Keep the limits generous.
- **Simplest: a connection cap in the host firewall.** Example: `iptables -I INPUT -p tcp --syn --dport 443 -m connlimit --connlimit-above 200 --connlimit-mask 32 -j REJECT --reject-with tcp-reset`, inserted before the 443 ACCEPT rule and persisted in `/etc/iptables/rules.v4`. The exact persistence on Oracle's Ubuntu image is UNVERIFIED.
- **A request-rate limit needs a third-party Caddy module** (`mholt/caddy-ratelimit`, a custom build with `xcaddy`; syntax UNVERIFIED), or nginx with `limit_req`/`limit_conn`. Optional for a 3-hour lab.

**Firewall:**
- **Inbound:** **443/tcp** from anywhere; **22/tcp** only from your and Slava's IPs; nothing else.
- **Port 80 stays closed.** Caddy then gets its certificate with the TLS-ALPN challenge on 443 (both challenges are on by default). No API client can send its key over plain HTTP (security #20).
- **Oracle, open 443 twice:**
  1. In the VCN security list, and remove any SSH-from-anywhere rule.
  2. In the host firewall: platform images "have a default set of firewall rules that allow only SSH access". On Ubuntu, edit iptables and **don't use UFW** ("might cause an instance not to boot"). Never block `169.254.0.2`.

## 5. Admin access

```sh
ssh -N -L 8081:127.0.0.1:8081 <vm>        # keep it open in one terminal
read -rs LGAI_ADMIN_TOKEN && export LGAI_ADMIN_TOKEN
bash scripts/maintenance.sh status        # the scripts default to ADMIN_URL=http://127.0.0.1:8081
```

- Two people hold the admin token, SSH access and the Modal console (D7).
- The admin token never goes on a command line: the scripts pass it to curl through `-K <(printf …)`.

## 6. Modal side: the vLLM app (sketch)

Account settings first (D1): Workspace budget **$28**, spend limit **$0**.

Secrets:
- In the dashboard (not on a command line), create a secret `lgai-vllm` holding `VLLM_API_KEY`, the same value as the gateway's `LGAI_UPSTREAM_API_KEY`.
- Create a proxy token (dashboard, or `modal workspace proxy-tokens`) for `LGAI_UPSTREAM_HEADERS`.

`lgai_vllm.py` follows Modal's current vLLM example (`@app.server`) and the vLLM flags from `research/performance.md` §3.1 and `research/model-choice.md` §4. **UNVERIFIED as a whole:** check it against your installed `modal` version.

```python
import json, os, subprocess
import modal

MODEL = "Qwen/Qwen3.6-35B-A3B-FP8"   # fallback, same GPU (D3): "Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8"
PORT = 8000

image = (
    modal.Image.from_registry("nvidia/cuda:12.9.0-devel-ubuntu22.04", add_python="3.12")
    .entrypoint([])
    .uv_pip_install("vllm==0.30.0")          # pinned; must be >= 0.11.1 (chat_template_kwargs CVEs)
    .env({
        "HF_XET_HIGH_PERFORMANCE": "1",
        "VLLM_MEDIA_URL_ALLOW_REDIRECTS": "0",  # J7 defence in depth (name from vLLM issue #57157)
    })
)
hf_cache = modal.Volume.from_name("lgai-hf-cache", create_if_missing=True)      # ~37.5 GB of weights, downloaded once
vllm_cache = modal.Volume.from_name("lgai-vllm-cache", create_if_missing=True)  # compile cache: faster restarts

app = modal.App("lgai-vllm")

@app.server(
    image=image,
    gpu="L40S",
    port=PORT,
    min_containers=int(os.environ.get("KEEP_WARM", "0")),  # 1 only during the lab window (D1)
    max_containers=1,              # never a second GPU (D1)
    target_concurrency=32,         # = --max-num-seqs; the gateway queues, Modal never scales out
    scaledown_window=10 * 60,
    startup_timeout=20 * 60,       # the first start downloads the weights into the Volume
    volumes={"/root/.cache/huggingface": hf_cache, "/root/.cache/vllm": vllm_cache},
    secrets=[modal.Secret.from_name("lgai-vllm")],
    # No unauthenticated=True: Servers require Modal proxy auth by default (Modal-Key / Modal-Secret).
)
class VLLM:
    @modal.enter()
    def start(self):
        self.proc = subprocess.Popen([
            "vllm", "serve", MODEL, "--host", "0.0.0.0", "--port", str(PORT),
            "--served-model-name", MODEL,                     # = the upstream id in LGAI_MODELS
            "--api-key", os.environ["VLLM_API_KEY"],
            "--language-model-only",                          # skip the vision encoder: more KV, no media inputs
            "--reasoning-parser", "qwen3",
            "--default-chat-template-kwargs", json.dumps({"enable_thinking": False}),  # thinking off
            "--max-model-len", "8192",                        # fits a 16 KiB chat input + 1536 output tokens
            "--max-num-seqs", "32",
            "--max-num-batched-tokens", "2048",
            "--gpu-memory-utilization", "0.90",
            "--enable-prefix-caching",
            "--disable-fastapi-docs",
            # J7: never add --enable-auto-tool-choice, --tool-call-parser, --allowed-local-media-path,
            # --enable-log-requests (logs prompts) or --trust-remote-code.
        ])

    @modal.exit()
    def stop(self):
        self.proc.terminate()
```

```sh
modal deploy lgai_vllm.py                 # idle: no container, no GPU cost; requests get 503
KEEP_WARM=1 modal deploy lgai_vllm.py     # lab window only, from T-60 min
modal deploy lgai_vllm.py                 # right after the lab (back to 0), or:
modal app stop lgai-vllm                  # kill switch; stops billing
```

**Sources:**
- **Modal "Servers" guide and vLLM example:** servers need auth unless `unauthenticated=True`; "When a Server has no active containers, requests will be rejected with a 503"; the example uses `*.modal.direct` URLs.
- **vLLM Qwen3.5/3.6 recipe:** `--language-model-only`, and `--reasoning-parser qwen3 --default-chat-template-kwargs '{"enable_thinking": false}'`.

**Alternative:** `@modal.web_server(port, requires_proxy_auth=True)` with `@modal.concurrent(max_inputs=32)` gives a `*.modal.run` URL and cold-starts on request. But it is a web function, so the documented 150 s limit applies.

## 7. Abuse and privacy checks through the public URL (J5, J6)

- [ ] **Log canary (J5):**
  1. Send one chat request containing a unique string (`CANARY-<date>`) through `https://<name>`.
  2. Run `modal app logs lgai-vllm | grep CANARY` and grep the Caddy log (if enabled) and `docker compose logs gateway`. All must find nothing.
- [ ] **Slow bodies (J6):** one test key opens 300 slow request bodies. Another key must still get `GET /v1/models` → 200 within 1 s.
- [ ] **Invalid flood (J6):** 1,000 `{}` requests from one key give mostly 429, and the usage row count barely moves (`sqlite3 … 'SELECT count(*) FROM usage'`).
- [ ] **Unknown paths:** `/invocations`, `/docs`, `/metrics`, `/health`, `/admin/keys` on `https://<name>` → the gateway's JSON 404/401, never a vLLM response; `http://<name>` does not connect.

## 8. Day-1 checks on the GPU (≈ $1–2)

```sh
URL=https://...                  # exactly as printed by modal deploy, without /v1
read -rs MK; read -rs MS; read -rs VK; export MK MS VK   # proxy token id/secret, VLLM_API_KEY
hdr() { printf 'header = "Modal-Key: %s"\nheader = "Modal-Secret: %s"\nheader = "Authorization: Bearer %s"\n' "$MK" "$MS" "$VK"; }
```

- [ ] **Both auth layers (J8):** `curl -sS -K <(hdr) $URL/v1/models` → 200.
  - Without the Modal headers → Modal's 401/403.
  - Without `Authorization` → vLLM's 401.
  - **UNVERIFIED:** whether Modal forwards our `Authorization` header to the container when `Modal-Key`/`Modal-Secret` are used. If it does not, drop `--api-key` and rely on Modal proxy auth; `LGAI_UPSTREAM_API_KEY` then stays empty.
- [ ] **Closed paths:** `/invocations`, `/metrics` and `/docs` without the Modal headers → 401/403 from Modal.
- [ ] **vLLM log** (`modal app logs lgai-vllm`, D3, J7):
  - "Maximum concurrency for 8,192 tokens per request" ≥ **30**;
  - `--language-model-only` accepted;
  - prefix caching on.
- [ ] **Thinking off:** one chat answer has no `reasoning_content` deltas.
- [ ] **Streams past 150 s** (D1, critic M2). Modal documents the 150 s limit for web functions; its Servers guide mentions no limit (**UNVERIFIED** for streams). Run 30 parallel streams long enough to pass 150 s:

  ```sh
  body='{"model":"Qwen/Qwen3.6-35B-A3B-FP8","stream":true,"max_tokens":4000,"ignore_eos":true,"messages":[{"role":"user","content":"Count upwards from 1, one number per line."}]}'
  for i in $(seq 30); do
    curl -sSN -K <(hdr) -H 'Content-Type: application/json' -d "$body" "$URL/v1/chat/completions" \
      -o "s$i.txt" -w "$i status=%{http_code} first_byte=%{time_starttransfer}s total=%{time_total}s\n" &
  done; wait
  grep -L '\[DONE\]' s*.txt      # must print nothing
  ```

  - **Pass:** every status is 200, most totals are > 150 s, and every file ends with `[DONE]`. Then tell Slava to raise `LGAI_MAX_TOKENS_CAP` to 2048.
  - **On a 303 or a cut stream:** keep 1536, which stays under 150 s at ≥ 10 tok/s.
- [ ] **10-minute soak through the gateway (J9):** `LGAI_KEY=<temporary load-test key> loadtest -url https://<name> -c 30 -d 10m -json soak.json`, with no errors (D3).
  - Create that key with `rpm_limit: 0, daily_token_quota: 0, max_inflight: 64`.
  - Revoke it right after (security #12).
- [ ] **Preemption drill:** stop the container mid-run and time the recovery, for runbook L1.
- [ ] **Restore drill:** restore a DB backup on a scratch VM once (§3), and time it.
