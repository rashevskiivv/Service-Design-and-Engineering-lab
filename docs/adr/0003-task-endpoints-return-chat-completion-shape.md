# ADR 0003: Task endpoints return OpenAI `chat.completion` / `chat.completion.chunk`

- Status: Accepted, 2026-09-30
- Deciders: Slava (owner), Architect agent

## Context

FR2 adds `explain`, `review`, `tests` and `fix` endpoints with language-aware prompts and streaming. Classmates use curl and the official `openai` Python client, and Giulia demos both. We have to choose the response shape: the same as chat completions, or a custom JSON such as `{"explanation": "..."}`.

## Decision

`POST /v1/tasks/{task}` takes a small task-specific request: `language`, `code`, and optionally `error`, `instructions`, `framework`, `stream`, … The gateway turns it into a chat request (system prompt + user message) and **relays the upstream response unchanged**:

- `stream: false` returns a `chat.completion` object, with the answer in `choices[0].message.content` (Markdown).
- `stream: true` returns SSE `chat.completion.chunk` events terminated by `data: [DONE]`.

With the openai SDK, the typed models still apply through its documented escape hatch for custom endpoints ([openai-python README, "Undocumented endpoints"](https://github.com/openai/openai-python#undocumented-endpoints)):

```python
r = client.post("/tasks/review", cast_to=ChatCompletion, body={"language": "go", "code": src})
s = client.post("/tasks/review", cast_to=ChatCompletion, body={..., "stream": True},
                stream=True, stream_cls=Stream[ChatCompletionChunk])
```

## Alternatives considered

1. **Custom JSON (`{"result": "...", "usage": {...}}`) plus a custom SSE event format.** It is nicer for curl, but it needs a second streaming encoder that re-encodes every chunk, and it has no SDK types. Two formats to test and document. More code for no FR benefit.
2. **Structured output (for example, review findings as a JSON array via `response_format`).** Small coding models follow JSON schemas unreliably, and it breaks streaming readability. Out of scope.
3. **No separate endpoints: a `task` field in `extra_body` on `/v1/chat/completions`.** It works with `chat.completions.create`, but it overloads the OpenAI endpoint's semantics and hides FR2 from the API docs.

## Consequences

- The streaming proxy, usage capture, TTFT and error handling have one code path for chat and tasks.
- The output is free-form Markdown, and prompts ask for a fixed section order. Clients render it and don't parse it.
- The `model` field in responses is the upstream's model id, not our alias. The gateway does not rewrite response bodies.
- Unknown request fields are rejected with 400 (`DisallowUnknownFields`), so typos like `"lang"` fail loudly for students.
