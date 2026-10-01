# local-generative-ai gateway

An OpenAI-compatible API gateway in front of one self-hosted, open-weights coding model (Ollama on a laptop, or vLLM / llama.cpp on a GPU). It adds:

- per-person API keys, stored as SHA-256, revocable, with optional expiry;
- per-key rate limits, concurrency caps and daily token quotas, plus a global in-flight cap with a bounded queue (`429` / `503` + `Retry-After`);
- language-aware coding tasks (`explain`, `review`, `tests`, `fix` for Python, Java, Go, C and C++);
- usage stats per request (tokens, time to first token, latency, status), with no prompt or answer content, ever;
- an admin API on a separate loopback listener, and a maintenance kill switch.

It is one static Go binary (Go 1.26, stdlib plus `modernc.org/sqlite`) with one SQLite file. The design is in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md), the binding decisions in [`docs/DECISIONS.md`](docs/DECISIONS.md), and the API in [`api/openapi.yaml`](api/openapi.yaml).

```
cmd/gateway          main: config, listeners, graceful shutdown; subcommands healthcheck, gen-secret, gen-keys
internal/config      LGAI_* env → validated Config (secret rules, redacted startup log)
internal/oai         OpenAI wire types, error envelope, the chat request allowlist
internal/auth        key format and hashing, Bearer parsing, user-key and admin-token middleware
internal/store       SQLite: keys (expiry, batch, revoke by prefix, keys-file import) and usage rows
internal/limits      token bucket, per-key in-flight, daily quota, global gate
internal/upstream    upstream HTTP client (no redirects, header allowlist, error mapping, readiness)
internal/relay       SSE relay with per-event flush, usage/TTFT capture, idle watchdog; JSON relay
internal/tasks       task validation and prompts
internal/api         routers, middleware, generation pipeline, admin handlers
scripts/report.sql   aggregate numbers for the slides (no key names)
```

## Quick start (laptop mode, Ollama native)

1. Start Ollama natively (Docker on macOS can't use the Metal GPU). The default context of 4096 tokens is too small for the task inputs:

   ```sh
   ollama pull gpt-oss:20b
   OLLAMA_NUM_PARALLEL=4 OLLAMA_CONTEXT_LENGTH=8192 OLLAMA_KEEP_ALIVE=-1 ollama serve
   ```

   Never set `OLLAMA_HOST=0.0.0.0` on a shared network.

2. Run the gateway. `LGAI_MAX_INFLIGHT` defaults to 4, which matches `OLLAMA_NUM_PARALLEL=4`.

   ```sh
   export LGAI_MODELS=coder=gpt-oss:20b
   export LGAI_MODEL_DEFAULTS='coder={"reasoning_effort":"low"}'
   export LGAI_ADMIN_TOKEN="$(go run ./cmd/gateway gen-secret)"   # or: openssl rand -base64 32
   go run ./cmd/gateway
   ```

   The public API listens on `127.0.0.1:8080`, the admin API on `127.0.0.1:8081`, and the database is `data/gateway.db` (mode 0600). The upstream defaults to `http://127.0.0.1:11434/v1`. The startup log prints the effective configuration with secrets shown only as `set`/`unset`. `.env.laptop.example` holds every setting; load it with `set -a && . ./.env && set +a`.

3. Create a key on the admin listener. The plaintext key appears only in this response. The token goes to curl through `-K <(printf …)` (`printf` is a bash builtin), so it never shows up in `ps`. `scripts/admin-lib.sh` does the same.

   ```sh
   curl -s http://127.0.0.1:8081/admin/keys \
     -K <(printf 'header = "Authorization: Bearer %s"\n' "$LGAI_ADMIN_TOKEN") \
     -d '{"name":"team-demo"}'
   # {"id":1,"name":"team-demo","key_prefix":"lgai_Xy3kQ9a",...,"key":"lgai_..."}
   export KEY=lgai_...
   ```

## Example calls

Always send `model="coder"`; the response's `model` field shows the real upstream model id (e.g. `gpt-oss:20b`). Don't copy that id back into requests, or you'll get a 404. Use streaming: answers start sooner, and non-streaming answers are capped at `LGAI_NONSTREAM_MAX_TOKENS`.

```sh
# Streaming chat (SSE)
curl -N http://127.0.0.1:8080/v1/chat/completions -H "Authorization: Bearer $KEY" \
  -d '{"model":"coder","stream":true,"messages":[{"role":"user","content":"Reverse a string in Go"}]}'

# Coding task, streaming: explain | review | tests | fix
curl -N http://127.0.0.1:8080/v1/tasks/fix -H "Authorization: Bearer $KEY" \
  -d '{"model":"coder","stream":true,"language":"python","code":"def avg(xs):\n    return sum(xs)/len(xs)\n\nprint(avg([]))","error":"ZeroDivisionError: division by zero"}'
```

The official `openai` Python client works unchanged:

```python
from openai import OpenAI, Stream
from openai.types.chat import ChatCompletion, ChatCompletionChunk

client = OpenAI(base_url="http://127.0.0.1:8080/v1", api_key="lgai_...")

stream = client.chat.completions.create(
    model="coder",
    messages=[{"role": "user", "content": "Write a C function that reverses a linked list."}],
    stream=True,
)
for chunk in stream:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="", flush=True)

# Task endpoints return the same chat.completion chunks.
review = client.post(
    "/tasks/review",
    cast_to=ChatCompletion,
    body={"model": "coder", "language": "java", "code": "int f(int[] a){return a[a.length];}", "stream": True},
    stream=True,
    stream_cls=Stream[ChatCompletionChunk],
)
for chunk in review:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="", flush=True)
```

Task fields: `language` (python, java, go, c, cpp), `code`, `error` (required for `fix`), `instructions` (≤ 1000 chars), `framework` (`tests` only), plus `model`, `stream`, `stream_options`, `max_tokens` and `temperature`. Unknown fields are rejected, so typos fail loudly.

## Admin operations

The admin API is served only on `LGAI_ADMIN_ADDR` (default `127.0.0.1:8081`, reached over SSH on a server: `ssh -L 8081:127.0.0.1:8081 host`). It starts only when `LGAI_ADMIN_TOKEN` is set.

| Call | Purpose |
|---|---|
| `POST /admin/keys` `{"name","expires_at"?,"rpm_limit"?,"daily_token_quota"?,"max_inflight"?}` | one key (plaintext shown once) |
| `POST /admin/keys/batch` `{"count":40,"name_prefix":"lab-","expires_at"?}` | `lab-01` … `lab-40` (`scripts/mint-keys.sh` wraps this) |
| `GET /admin/keys`, `GET /admin/keys/{id}` | list / one key (never plaintext or hash) |
| `DELETE /admin/keys/{id}` | revoke one key; applies to the next request |
| `POST /admin/keys/revoke` `{"name_prefix":"lab-"}` or `{"all":true}` | bulk revoke |
| `GET /admin/keys/{id}/usage?since=&until=` | usage summary of one key |
| `PUT /admin/maintenance` `{"enabled":true}` | chat and tasks return 503 `maintenance`; models, health and admin keep working |

Use seat or role labels as key names (`lab-07`, `team-demo`), never real names. For the slides, run `sqlite3 -readonly -header -column data/gateway.db < scripts/report.sql` to get totals, status histogram, latency/TTFT/queue percentiles, per-stream tok/s and per-minute throughput, all without key names.

Offline keys for a disposable container (`LGAI_KEYS_FILE`, hashes only):

```sh
go run ./cmd/gateway gen-keys -n 40 -prefix lab- -expires 2026-10-14T18:00:00Z -out keys.csv -hashes keys.hashes
# keys.csv: name,key (hand out, then delete). keys.hashes: set LGAI_KEYS_FILE to it.
```

The gateway imports the hashes at startup. An already known hash is left untouched, so a revoked key is never revived.

## Server mode (DECISIONS D2)

The gateway and its SQLite file run on a small always-on CPU VM with a persistent disk, behind the TLS proxy. vLLM on Modal is only the upstream:

```sh
LGAI_ADDR=:8080                      # behind the proxy
LGAI_UPSTREAM_BASE_URL=https://WORKSPACE--lgai-vllm-serve.modal.run/v1
LGAI_UPSTREAM_API_KEY_FILE=/run/secrets/vllm-api-key    # vLLM --api-key
LGAI_UPSTREAM_HEADERS_FILE=/run/secrets/modal-headers   # Modal-Key=...;Modal-Secret=...
LGAI_MODELS=coder=Qwen/Qwen3.6-35B-A3B-FP8
LGAI_MODEL_DEFAULTS='coder={"chat_template_kwargs":{"enable_thinking":false}}'
```

The gateway refuses to start when a public upstream has no auth (no API key and no extra headers), unless `LGAI_UPSTREAM_ALLOW_UNAUTHENTICATED=true`. It never follows upstream redirects (Modal answers a 303 after 150 s), and it sends upstream only `Content-Type`, `Accept`, `X-Request-ID`, the upstream `Authorization` and `LGAI_UPSTREAM_HEADERS`. Use the server column of the knob table (`.env.server.example`). The secondary path, the gateway in the same container as vLLM, needs `LGAI_ADMIN_ON_PUBLIC=true` (logged as a WARN) and `LGAI_KEYS_FILE`.

## Configuration

Every setting is an `LGAI_*` environment variable, read once at startup. Invalid values stop the gateway with a message naming each bad variable. Unknown `LGAI_*` names are logged as warnings (names only, never values). Defaults are the laptop column of [DECISIONS D6](docs/DECISIONS.md), and the rules are in [ARCHITECTURE §9](docs/ARCHITECTURE.md). Secrets can also come from `NAME_FILE`.

| Variable | Default | Meaning |
|---|---|---|
| `LGAI_ADDR` | `127.0.0.1:8080` | public listener (containers: `:8080`) |
| `LGAI_ADMIN_ADDR` | `127.0.0.1:8081` | admin listener |
| `LGAI_ADMIN_TOKEN` (`_FILE`) | empty = admin API off | ≥ 43 chars, ≥ 16 distinct, no placeholder words |
| `LGAI_ADMIN_ON_PUBLIC` | `false` | also mount `/admin` on the public listener (WARN) |
| `LGAI_DB_PATH` | `data/gateway.db` | SQLite file |
| `LGAI_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LGAI_MODELS` | required | `alias=upstream-id[,…]`; the first is the default |
| `LGAI_MODEL_DEFAULTS` | empty | `alias={json}[;alias={json}]`; only `reasoning_effort`, `chat_template_kwargs.enable_thinking`, `temperature`, `top_p`, `top_k`, `presence_penalty` |
| `LGAI_UPSTREAM_BASE_URL` | `http://127.0.0.1:11434/v1` | OpenAI-compatible base including `/v1` |
| `LGAI_UPSTREAM_API_KEY` (`_FILE`) | empty | sent as `Authorization: Bearer` upstream; same rules as the admin token |
| `LGAI_UPSTREAM_HEADERS` (`_FILE`) | empty | extra upstream headers `Name=value;Name2=value2` (secret) |
| `LGAI_UPSTREAM_HEALTH_URL` | `{base}/models` | readiness probe |
| `LGAI_UPSTREAM_ALLOW_UNAUTHENTICATED` | `false` | allow a public upstream without auth |
| `LGAI_UPSTREAM_HEADER_TIMEOUT` | `120s` | first byte of a **stream** (server: 60s) |
| `LGAI_UPSTREAM_IDLE_TIMEOUT` | `60s` | max silence between stream events (server: 30s) |
| `LGAI_UPSTREAM_TIMEOUT` | `10m` | whole upstream request (server: 5m) |
| `LGAI_MAX_INFLIGHT` | `4` | global concurrent upstream requests (server: 30) |
| `LGAI_QUEUE_SIZE` | `16` | requests waiting for a slot (server: 24) |
| `LGAI_QUEUE_TIMEOUT` | `60s` | max wait in the queue (server: 30s) |
| `LGAI_OVERLOAD_RETRY_AFTER` | `10s` | `Retry-After` on 503 |
| `LGAI_RATE_RPM` / `LGAI_RATE_BURST` | `6` / `3` | per-key token bucket (0 rpm = off) |
| `LGAI_KEY_MAX_INFLIGHT` | `1` | per-key concurrent requests (server: 2; 0 = off) |
| `LGAI_TOKEN_QUOTA_DAILY` | `300000` | per-key tokens per UTC day (0 = off) |
| `LGAI_KEY_TTL` | `0` | default key lifetime for new keys (0 = none) |
| `LGAI_KEYS_FILE` | empty | key hashes imported at startup |
| `LGAI_CHAT_DEFAULT_MAX_TOKENS` | `512` | chat `max_tokens` when unset |
| `LGAI_MAX_TOKENS_CAP` | `1024` | clamp for every request (server: 1536) |
| `LGAI_NONSTREAM_MAX_TOKENS` | `768` | clamp for non-streaming requests (server: 1024) |
| `LGAI_TASK_MAX_INPUT_BYTES` | `16384` | task code + error + instructions + framework |
| `LGAI_CHAT_MAX_INPUT_BYTES` | `16384` | chat message text + tools (fits an 8K context with the cap) |
| `LGAI_MAX_BODY_BYTES` | `262144` | any request body |
| `LGAI_READ_HEADER_TIMEOUT` | `10s` | slowloris guard |
| `LGAI_BODY_READ_TIMEOUT` | `10s` | per request body, read only after the key's in-flight slot is taken |
| `LGAI_WRITE_TIMEOUT` | `10s` | per write to the client (a stalled reader is cut off) |
| `LGAI_IDLE_TIMEOUT` | `120s` | keep-alive idle |
| `LGAI_SHUTDOWN_TIMEOUT` | `30s` | drain time on SIGTERM |
| `LGAI_MAX_CONNS` | `256` | open connections per listener |

## Development

```sh
go build ./... && go vet ./... && gofmt -l .
go test ./...
go test -race ./...     # needs cgo (a C compiler)
```

The unit tests run against `internal/testupstream`, a fake OpenAI-compatible server that records every request and scripts streaming, usage chunks, errors, stalls and redirects. Nothing needs Ollama.

## Docker

Files: `Dockerfile`, `docker-compose.yml` (laptop), `.env.laptop.example`, `.env.server.example`, `.dockerignore`. The server VM setup (compose file for the VM, Caddy, firewall, Modal app) is in [docs/HANDOFF-jan.md](docs/HANDOFF-jan.md).

### Laptop: gateway in Docker, Ollama native

Docker on macOS cannot use the Metal GPU, so Ollama always runs natively.

```sh
cp .env.laptop.example .env      # set LGAI_ADMIN_TOKEN: go run ./cmd/gateway gen-secret
OLLAMA_NUM_PARALLEL=4 OLLAMA_CONTEXT_LENGTH=8192 OLLAMA_KEEP_ALIVE=-1 OLLAMA_MAX_QUEUE=16 ollama serve
docker compose up --build -d
curl -s http://127.0.0.1:8080/readyz
```

One `.env` serves both ways. It holds the native values (`127.0.0.1` listeners, `data/gateway.db`, Ollama at `127.0.0.1:11434`), and the compose file overrides the four container-specific ones: `LGAI_ADDR=:8080`, `LGAI_ADMIN_ADDR=:8081`, `LGAI_DB_PATH=/data/gateway.db`, `LGAI_UPSTREAM_BASE_URL=http://host.docker.internal:11434/v1`. Compose gives `environment:` precedence over `env_file:`. Both ports are published on `127.0.0.1` only. Values with `;` or JSON are single-quoted in the examples: Compose and `. ./.env` read them literally, but `docker run --env-file` keeps the quotes.

### Can the container reach Ollama on 127.0.0.1?

Ollama binds `127.0.0.1:11434` by default. What we found (2026-09-30):

- **Docker Desktop for Mac:** expected to work, but not documented. Docker's docs say `host.docker.internal` "resolves to the internal IP address of your host", but their example server listens on all interfaces ([networking how-tos](https://docs.docker.com/desktop/features/networking/networking-how-tos/)). On the Docker forum, a community leader (not an official statement) explains that the request "is forwarded to your physical hosts loopback ip address" ([forums.docker.com](https://forums.docker.com/t/connection-refused-on-host-docker-internal/136925), macOS). Verify once: `docker run --rm curlimages/curl -s http://host.docker.internal:11434/v1/models`.
- **Linux Docker Engine:** does not work. `host-gateway` is the bridge IP (`172.17.0.1`), where a loopback-only service is not listening; the usual fix in the Open WebUI threads is `OLLAMA_HOST=0.0.0.0` ([open-webui #5903](https://github.com/open-webui/open-webui/discussions/5903)).

If the check fails, use one of these options, in order of preference:

1. **Run the gateway natively:** `set -a && . ./.env && set +a && go run ./cmd/gateway`. Only loopback is involved (ARCHITECTURE §17.4, security #23).
2. **Linux only:** set `OLLAMA_HOST=172.17.0.1:11434` (the Docker bridge) and add a host firewall rule. macOS has no host-side bridge interface.
3. **Never** use `OLLAMA_HOST=0.0.0.0` on campus or other shared Wi-Fi. It exposes an unauthenticated Ollama to the whole network, and anyone on it can then also pull or delete models.

### The image

- **Build:** a multi-stage build. `golang:1.26.8-bookworm` builds `./cmd/gateway` with `CGO_ENABLED=0 -trimpath -ldflags="-s -w"`, `GOFLAGS=-mod=readonly` and `GOTOOLCHAIN=local`.
- **Runtime:** `gcr.io/distroless/static-debian12:nonroot`, which has CA certificates but no shell and no package manager, and runs as uid 65532.
- **Cross-building:** runs natively on the build machine, e.g. `docker buildx build --platform linux/arm64 -t local-generative-ai:lab .` for an Oracle A1 VM.
- **`/data` ownership:** `/data` is owned by 65532 with mode 0700, so a new named volume inherits that owner. A root-owned volume makes SQLite fail with "readonly database".
- **Image defaults:** `LGAI_ADDR=:8080` and `LGAI_DB_PATH=/data/gateway.db`. The admin listener keeps its default `127.0.0.1:8081` (container-local) unless `LGAI_ADMIN_ADDR=:8081` is set.
- **Healthcheck:** `HEALTHCHECK` runs `/gateway healthcheck` in exec form, which needs no shell. Upstream readiness is `/readyz`, checked from outside.
- **Pinning (security #22):** tags are pinned to a patch release. The Dockerfile header lists the digests observed on 2026-09-30 and the `docker buildx imagetools inspect` commands. For the lab image, append `@sha256:<digest>` to both `FROM` lines.
- **Compose hardening:**
  - `read_only: true`; only `/data` and a 16 MB `/tmp` are writable
  - `cap_drop: [ALL]`
  - `no-new-privileges`
  - `stop_grace_period: 40s`, longer than `LGAI_SHUTDOWN_TIMEOUT`
  - log rotation
- **`.dockerignore`:** keeps `.git`, `docs/`, every `.env*`, databases, key CSVs, key slips and certificates out of the build context.

### Data

- **Where:** the SQLite file lives in the named volume `local-generative-ai_gateway-data`.
- **Copy out** (e.g. for `scripts/report.sql`): `docker compose stop gateway && docker cp "$(docker compose ps -aq gateway)":/data/gateway.db ./gateway.db`. Stopping first checkpoints the WAL.
- **Delete:** `docker compose down -v`. The after-lab list is in [docs/runbook-lab-day.md](docs/runbook-lab-day.md) §8.

### Lab tooling

- **Keys:** `scripts/mint-keys.sh`, `scripts/revoke-prefix.sh` and `scripts/maintenance.sh` wrap the admin API. They need only bash, curl and POSIX tools, and they read the token from `LGAI_ADMIN_TOKEN`, never from an argument.
- **Load tester:** `cmd/loadtest` (`go run ./cmd/loadtest -h`) measures TTFB, TTFC, tok/s and latency percentiles. `-smoke` writes the 20 task × language answers for review.
- **Runbook:** [docs/runbook-lab-day.md](docs/runbook-lab-day.md) covers the lab day itself.
