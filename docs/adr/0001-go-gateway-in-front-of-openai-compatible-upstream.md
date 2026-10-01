# ADR 0001: A Go gateway in front of any OpenAI-compatible model server

- Status: Accepted, 2026-09-30
- Deciders: Slava (owner), Architect agent

## Context

30 classmates must reach a self-hosted model over the public internet with their own keys, limits and usage stats (PRD FR3–FR6). The model server changes between modes: Ollama on a MacBook (laptop mode) and vLLM or llama.cpp server on a rented GPU (server mode, FR8). All three already speak the OpenAI Chat Completions protocol. None of them gives us per-person keys with quotas and usage stats:

- vLLM `--api-key` is one shared key, and it only protects `/v1`, `/v2` and `/inference`; `/invocations` stays open ([vLLM docs](https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/)).
- llama.cpp `--api-key` accepts a comma-separated list of keys, but with no per-key limits or accounting ([llama.cpp server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)).
- Ollama has no auth at all and binds to 127.0.0.1 by default ([Ollama FAQ](https://docs.ollama.com/faq)).

## Decision

Build a small Go service (one static binary) that exposes the OpenAI-compatible surface plus the task endpoints. It forwards to exactly one configured upstream base URL (`LGAI_UPSTREAM_BASE_URL`). The model server is never exposed directly: it listens on localhost or a private Docker network, and when it supports one it also gets an upstream key (`LGAI_UPSTREAM_API_KEY`).

The gateway speaks only the OpenAI wire protocol to the upstream: `POST /chat/completions` and `GET /models`, plus an optional health URL. It never uses backend-native APIs, so switching modes is a config change.

## Alternatives considered

1. **Expose the model server directly and give out its key.** Zero code, but no per-person keys, revocation, quotas or usage (fails FR3–FR6), and it leaves vLLM's unauthenticated `/invocations` public.
2. **Off-the-shelf LLM proxy (e.g. LiteLLM).** Has keys and budgets, but it is Python with a larger footprint and usually Postgres for key management. It also doesn't cover the language-aware task endpoints (FR2), and the PRD fixes the language as Go.
3. **Reverse-proxy auth (nginx `auth_request` or basic auth in front).** Covers keys, but not token quotas, a fair FIFO queue with `Retry-After`, TTFT/usage stats or the task prompts.

## Consequences

- One more hop. It is in-process streaming with per-event flush, so latency is negligible and measurable (the load tester can target the upstream directly for comparison).
- We depend only on the subset of the OpenAI protocol that all three backends implement. Request fields pass through an allowlist. Request-local extensions such as `chat_template_kwargs` are kept. Extensions that affect other users, such as Ollama `keep_alive` (which could unload the model for everyone), are dropped.
- The gateway, not the model server, is where overload is decided. So `LGAI_MAX_INFLIGHT` must stay at or below the upstream's parallel capacity (Ollama `OLLAMA_NUM_PARALLEL`, llama.cpp `-np`, vLLM `--max-num-seqs`). The upstream's own queue then stays empty and clients get our 503 + `Retry-After`.
- Public model names are aliases (`LGAI_MODELS=coder=<upstream id>`), so Szymon's exercises use `model="coder"` in both modes.
