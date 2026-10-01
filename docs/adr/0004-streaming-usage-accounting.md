# ADR 0004: Streaming token accounting via `stream_options.include_usage`

- Status: Accepted, 2026-09-30
- Deciders: Slava (owner), Architect agent

## Context

Quotas (FR4) and usage stats (FR6) need prompt and completion token counts. Non-streaming responses carry `usage` on all backends. Streaming responses carry it only if asked. We must count tokens without buffering the stream, and without storing content.

Verified upstream support for `stream_options: {"include_usage": true}` on `/v1/chat/completions`. When it is set, each backend emits a final chunk with `choices: []` plus `usage`, before `[DONE]`:

| Backend | Evidence |
|---|---|
| Ollama | Docs list `stream_options` / `include_usage` as supported ([docs](https://docs.ollama.com/api/openai-compatibility)). Source: `openai/openai.go` defines `StreamOptions{IncludeUsage}`, and `middleware/openai.go` writes a chunk with `Usage` and empty `Choices` before `[DONE]` ([source](https://github.com/ollama/ollama/blob/main/middleware/openai.go)). |
| vLLM | `should_include_usage(stream_options, enable_force_include_usage)` in `vllm/entrypoints/serve/utils/api_utils.py`. `chat_completion/serving.py` emits `final_usage_chunk` when it is set. The server flag `--enable-force-include-usage` forces usage on every chunk ([source](https://github.com/vllm-project/vllm/blob/main/vllm/entrypoints/openai/chat_completion/serving.py)). |
| llama.cpp | `server-schema.cpp` parses `stream_options.include_usage`. `server-task.cpp` appends a chunk with `"choices": []` and `usage` "per OpenAI spec". The final chunk also carries `timings` ([source](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/server-task.cpp)). |

## Decision

1. For every streaming request, the gateway **forces `stream_options.include_usage = true`** upstream.
2. The relay forwards events one at a time. It decodes only the small fields it needs (`choices[].delta.*` presence, `usage`, `error`) and remembers the **last non-null `usage`**, which also handles vLLM's continuous-usage mode.
3. If the client did **not** request usage itself, the usage-only chunk (`choices == []` and `usage != null`) is **not forwarded**. That keeps the stream identical to what OpenAI would send, and naive clients doing `chunk.choices[0]` don't crash.
4. **Fallback** (backend or version without usage, or a stream cut short): completion tokens = number of chunks with a non-empty content, reasoning or tool-call delta (about one token per chunk on Ollama and llama.cpp; may undercount on vLLM). Prompt tokens = ceil(prompt bytes / 4). The row gets `usage_estimated = 1`.
5. Client disconnect: the upstream request is cancelled, and whatever was counted so far is recorded with status 499.

## Alternatives considered

- **Tokenize in the gateway.** This needs the exact tokenizer per model in Go: heavy, and different for every model.
- **Call a `/tokenize` endpoint.** vLLM has one, Ollama doesn't. It is backend-specific and adds an extra round-trip.
- **Don't count streamed tokens.** Then quotas are meaningless for the main use case.

## Consequences

- Accurate counts on all three verified backends. The estimate is only a safety net and is flagged in the data.
- The quota check reads what is already recorded, so requests in flight are not counted. A key can overshoot its daily quota by up to `max_inflight × max_tokens`. Acceptable.
- UNVERIFIED: the exact Ollama release that added `include_usage` (current `main` and docs support it; Slava's 0.32.12 should be checked with one curl during the integration test).
