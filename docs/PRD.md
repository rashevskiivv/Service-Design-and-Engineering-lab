# PRD: Local Generative AI for Coding (API service)

| | |
|---|---|
| Owner | Slava (rvv) |
| Team | Giulia (presentation + Q&A), Slava (cloud + service), Jan (cloud deploy + Docker), Szymon (exercises + questions) |
| Status | Agreed. Requirements interview done 2026-09-30 |
| Lab / demo date | In 2–3 weeks (≈ 14–21 Oct 2026); exact date TBD |
| Scope of this doc | **Slava's part**: find a cloud GPU (€0) + build the Go API service in front of a self-hosted open-weights coding model |

---

## 1. Problem and goal

The lab topic is **"Local generative AI for coding"**, delivered **as a service reachable through an API**.

During one lab session, classmates solve Szymon's coding exercises with the help of a coding LLM that **we host ourselves**: an open-weights model with no calls to OpenAI, Anthropic or any other proprietary API. Giulia demos the same service live in the presentation.

**Success means:** during the lab, 10–30 classmates at the same time get streaming answers from our model through our API with their own key, the demo runs without surprises, and the team spends €0.

## 2. Users

| User | How they use it | Load |
|---|---|---|
| Classmates in the lab | curl, the official `openai` Python client, or IDE tools, while doing exercises in **Python, Java, Go, C/C++** | **10–30 concurrent** (design point: 30) |
| Team (live demo) | Giulia/Slava call it during the presentation | 1–3 |
| Professor | Watches the demo, may try it during the lab | 1–2 |
| Team's own tools | Continue / aider / curl via the OpenAI-compatible endpoint | 1–4 |

**After the live demo and exercises the service does not need to stay up or be maintained.** The GPU is needed for a few hours in total (dev + rehearsal + lab), not for weeks.

## 3. Constraints

- **Budget: €0.** Acceptable:
  - credit programmes that need a card on file, with hard spend caps, never exceeding credits
  - a smaller or slower model as a fallback, where requests queue
  - asking the professor for course credits or a UniTrento GPU (Slava sends the email; Claude drafts it)
- **"Local" = both modes:**
  - **server mode**: an open-weights model self-hosted on a rented GPU machine
  - **laptop mode**: the same service runs against Ollama on a laptop (dev + offline fallback for the demo)
- **Language: Go** (module `local-generative-ai`, Go 1.26). Slava's Mac has Go 1.26.4, Ollama 0.32.12 (qwen2.5-coder:1.5b-base, gpt-oss:20b pulled), Docker.
- **Work split with Jan:** Slava delivers the Go service, its `Dockerfile`, and a `docker-compose.yml` that runs it locally. **Jan owns cloud deploy, TLS/reverse proxy, CI.**
- Cloud sign-up and any card entry are done **by Slava himself**; Claude researches and recommends.

## 4. Functional requirements

| ID | Requirement | Priority |
|---|---|---|
| FR1 | **OpenAI-compatible chat**: `POST /v1/chat/completions` (non-streaming and `stream: true` via SSE), `GET /v1/models`. Works unmodified with the official `openai` client by changing `base_url` + `api_key`. | Must |
| FR2 | **Coding task endpoints** with tuned, language-aware prompts: `explain` (what code does), `review` (bugs / smells / security issues), `tests` (generate unit tests), `fix` (code + error message → fixed code). Must handle Python, Java, Go, C/C++. Streaming supported. | Must |
| FR3 | **Per-person API keys** (Bearer). Stored hashed, never in plaintext; revocable. | Must |
| FR4 | **Limits**: per-key rate limit and token quota; a global cap on in-flight upstream requests with a bounded wait queue. Over the limit → `429` + `Retry-After`; queue full → `503` + `Retry-After`. | Must |
| FR5 | **Admin API** (separate admin token): create / list / revoke keys, read usage per key. | Must |
| FR6 | **Usage stats per request**: key id, endpoint, model, prompt/completion tokens, latency (incl. time-to-first-token), status. **No prompt or response content is stored** (classmates' code stays private). | Must |
| FR7 | **Health**: `GET /healthz` (process alive), `GET /readyz` (upstream model server reachable). | Must |
| FR8 | **Backend-agnostic upstream**: the service talks to any OpenAI-compatible model server (Ollama in laptop mode; vLLM / llama.cpp server on a GPU), chosen by config. | Must |
| FR9 | **Load-test tool**: N concurrent streaming clients, reports TTFT, tokens/s per client and aggregate, error rates. Produces the real numbers for Giulia's slides. | Must |
| FR10 | FIM autocomplete for IDEs | **Out** (stretch) |

## 5. Non-functional requirements

- **Performance** (targets to be confirmed by the performance analysis against the chosen GPU and model): 30 concurrent streaming users; answers start streaming within a few seconds; each user gets at least human reading speed (≈ 10+ tok/s). The gateway itself adds negligible latency.
- **Security:**
  - public internet exposure is assumed (TLS terminated by Jan's reverse proxy)
  - keys hashed
  - admin API separated and off by default unless a token is set
  - request body size limits, upstream timeouts, no secrets in logs, no content logging
- **Operability:** a single static Go binary; config via env vars; structured logs; graceful shutdown; state in one SQLite file (no external DB server).
- **Testability:** unit tests against a fake OpenAI-compatible upstream (incl. SSE); integration test against real Ollama on Slava's Mac; race detector clean.

## 6. Out of scope

- FIM autocomplete (stretch)
- A web UI
- Storing prompts/responses
- Multi-node scaling
- Fine-tuning or RAG over repositories
- Maintenance after the lab
- Cloud deployment, TLS and CI (Jan)
- Slides (Giulia)
- The exercises themselves (Szymon)

## 7. Deliverables (this session)

1. `docs/PRD.md`: this document
2. `docs/research/`: free-GPU cloud options with a verified €0 path + fallback; model choice for 4 languages
3. `docs/ARCHITECTURE.md` + decision records, `api/openapi.yaml`
4. The Go service with tests, and the load-test tool
5. `Dockerfile` + `docker-compose.yml` for local run
6. Handoff:
   - API guide + example calls for Szymon's exercises
   - numbers and diagram for Giulia
   - run and deploy notes for Jan
   - cloud sign-up steps for Slava
   - email draft to the professor
7. A local git repo with commits (Slava creates the GitHub remote and pushes)

## 8. Acceptance criteria

- [ ] `go test -race ./...` passes.
- [ ] Against real Ollama on Slava's Mac: non-streaming and streaming chat, plus all four task endpoints, work end to end via curl **and** via the official `openai` Python client.
- [ ] Missing / invalid / revoked key → `401`; over the rate limit → `429` with `Retry-After`; saturated queue → `503` with `Retry-After`.
- [ ] The admin can create a key, use it, revoke it, and see its usage.
- [ ] `docker compose up` runs the service against Ollama on the host.
- [ ] The load-test tool runs N concurrent streaming clients and prints TTFT and tok/s.
- [ ] The cloud recommendation names a concrete €0 path with sources, plus a fallback, plus the sign-up steps.

## 9. Risks

| Risk | Mitigation |
|---|---|
| No free GPU obtainable (quota requests denied or slow, card checks) | Start sign-ups this week; several parallel paths (credit programmes, professor/UniTrento); CPU/small-model fallback with queuing; the laptop-mode demo as the last resort |
| 30 users saturate one GPU | Batching engine (vLLM-class) on the GPU; gateway queue + limits; short `max_tokens` defaults per task |
| Model quality uneven across Python/Java/Go/C++ | Pick the model by multi-language coding benchmarks; the load test also checks answer sanity |
| Abuse of a public GPU endpoint | Per-person keys, quotas, limits; up only during demo/lab |
| Lab date unknown | Plan for the earliest date (≈ 14 Oct) |

## 10. How we work (agents)

Roles agreed with Slava:
- Cloud & model researcher (later: doc writer)
- Architect (also owns the DB schema)
- Performance
- Security
- Critic
- Testing
- Independent code reviewer

Dropped:
- **User-interview agent**: subagents can't talk to the user; the interview was done directly.
- **Separate DB agent**: merged into the architect, since the data is two small SQLite tables.
- **Per-agent reviewers**: replaced by reviewers per deliverable, plus the critic.

Critical claims from agents (prices, quotas, benchmarks) are verified against primary sources before we rely on them.

## 11. Decision log

- **2026-09-30**: Scope, users, load (30 concurrent), budget (€0), laptop + server modes, API surface (OpenAI-compatible + explain/review/tests/fix, streaming), per-person keys + admin API + usage stats (no content), handoff line with Jan, and agent roster, all agreed with Slava. FIM deferred.
