# PRD: LAB_07 "Local Generative AI for Coding" (local only)

| | |
|---|---|
| Owner | Slava (rvv) |
| Team (7) | Slava, Jan Piták, Giulia Vallepulcini, Szymon Domański, Afomia. Roles in §9 |
| Course | Service Design and Engineering (SDE), UniTrento, 2026/27 |
| Status | Re-scoped after the drafting session with the mentor on 2026-10-07 ("only local"). Draft for the mentoring session on Tue 2026-10-27 |
| Lab date | **Wed 2026-12-09, 08:30–11:30, room A109.** Course rule: the lab content fits in **2 hours**, including time spent helping classmates |
| Scope of this doc | The whole lab: setup guide, hardware→model table, schedule, examples, exercises, mini-assignment, lab-day runbook. The slides are Giulia's |

---

## 1. Problem and goal

The lab topic is **"Local generative AI for coding"**: how to run an open-weights coding model **on your own computer** and use it for everyday coding work.

During the lab every classmate runs a model **on their own laptop** with [Ollama](https://ollama.com). Nothing is hosted by us: no cloud GPU, no shared server, no API keys. Students talk to the model through the Ollama CLI, `curl`, or the official `openai` Python client pointed at `http://localhost:11434/v1`.

**Success means:**

- by the end of the 40-minute intro, every student (or every pair) has a model answering on their own machine;
- everyone follows the guided examples and finishes at least the first two exercises;
- the mini-assignment can be done in about 20 minutes;
- the team spends €0, and no student code leaves the student's laptop.

## 2. Users

| User | Hardware | How they use it |
|---|---|---|
| Classmates (~30) | Mixed: MacBooks (Apple Silicon, some Intel), Windows and Linux laptops, 8–32 GB RAM, some with an NVIDIA GPU | Ollama CLI, `curl`, Python `openai` client; code in Python first, Java / Go / C / C++ optional |
| Mentor and professors | Their own laptops | Watch, may try the examples |
| Team | Our laptops (Slava: MacBook Pro M4 Pro 24 GB) | Demo on the projector, help desk, test matrix |

## 3. Constraints

- **Only local** (mentor note 1). Every model runs on the student's own machine.
- **No limits** (mentor note 2). A local model has no quotas or rate limits, so there are no per-person keys and nothing to meter.
- **It must run on every computer** (mentor note 4). The hardware→model table covers Apple Silicon, NVIDIA and CPU-only machines down to 8 GB of RAM. The smallest row downloads about 1 GB.
- **Downloads happen at home**, the evening before. Several GB per laptop over campus Wi-Fi does not work.
- **2 hours in total**, with very simple examples and light slides (course rules for labs).
- **€0, open weights,** no calls to OpenAI, Anthropic or any other proprietary API.
- **No Docker for the model.** Docker on macOS cannot use the Metal GPU, so Ollama always runs natively.
- **Privacy:** Ollama stays bound to `127.0.0.1`. Never `OLLAMA_HOST=0.0.0.0` on campus Wi-Fi.

## 4. Lab requirements

| ID | Requirement | Mentor note | Where |
|---|---|---|---|
| LR1 | **Setup guide**: install Ollama, pick a model, check speed. Tested on the team's machines (macOS, Windows, Linux, the weakest laptop we have) before the lab | 1, 4 | [lab-setup.md](lab-setup.md) |
| LR2 | **Hardware→model table**: Apple Silicon / NVIDIA / CPU-only, memory tiers, OS → Ollama tag, download size, expected speed, plus a column of speeds measured by the team | 4 | [lab-setup.md §1](lab-setup.md), test matrix in [runbook §2](runbook-lab-day.md) |
| LR3 | **Schedule with timing**: 40 min idea and subject (focused on the setup) → guided examples → 2–3 exercises → mini-assignment, 2 h in total | 3, 5 | [lab-plan.md §1](lab-plan.md) |
| LR4 | **Guided examples**: the LLM explains Go code to someone who doesn't know Go; the LLM writes a simple service. Plus a bank of spare examples | 3, 6 | [lab-plan.md §3–4](lab-plan.md) |
| LR5 | **Exercise ladder**: simple → sophisticated, guided → "add something of your own" | 7 | [lab-plan.md §5](lab-plan.md) |
| LR6 | **Mini-assignment** (~20 min): an example from the lab with a modification, e.g. another programming language | 8 | [lab-plan.md §6](lab-plan.md) |
| LR7 | **Cheat sheet**: Ollama commands, `curl` and Python against `localhost:11434/v1` | 3 | [lab/cheat-sheet.md](../lab/cheat-sheet.md) |
| LR8 | **Code snippets** for examples and exercises in Python, Java, Go, C, C++, each with a real bug and its error output | 3, 7 | [lab/](../lab/README.md) |
| LR9 | **Lab-day runbook**: countdown, offline kit, help desk, fallbacks | 1 | [runbook-lab-day.md](runbook-lab-day.md) |
| LR10 | **Slides** (Giulia) and the **Delivery** package on Google Drive by Mon 2026-12-07 | – | – |

## 5. Non-functional requirements

- **Works offline** after the model is downloaded.
- **Speed:** each student gets at least reading speed (≈ 10 tok/s). Below 5 tok/s the student moves one row down in the table.
- **Same instructions for everyone:** the class default is one model family (`qwen2.5-coder`) at different sizes. It answers directly, with no "thinking" phase, so every prompt in the lab works the same way on every row.
- **Cross-platform:** every command is given for macOS, Windows and Linux where they differ.
- **Honest numbers:** the table separates measured speeds from estimates.

## 6. Out of scope

- Cloud GPUs, a shared server, TLS, per-person API keys, quotas, rate limits, load testing
- **The Go gateway.** Dropped from the lab on 2026-10-15. The code stays in the repo as a legacy project ([gateway.md](gateway.md)); students never need it
- Fine-tuning, RAG over repositories, agents
- IDE autocomplete (mentioned as an optional extra only)
- Support after the lab

## 7. Deliverables

1. `docs/PRD.md`: this document
2. `docs/lab-setup.md`: setup guide with the hardware→model table
3. `docs/lab-plan.md`: schedule, talk outline, examples, exercises, mini-assignment
4. `docs/runbook-lab-day.md`: countdown, test matrix, offline kit, fallbacks
5. `lab/`: code snippets with their error output, and the cheat sheet
6. Slides (Giulia) and the Delivery folder on Google Drive (by 2026-12-07), including where and which generative-AI tools we used to prepare the lab (course rule)

## 8. Acceptance criteria

- [ ] The setup guide works on at least one Mac, one Windows and one Linux machine, plus the weakest laptop in the team; results are in the test matrix (runbook §2).
- [ ] Every row of the hardware→model table has a team measurement or is clearly marked as an estimate.
- [ ] At the dry run (Tue 2026-11-24) the whole lab fits in 2 hours with a timer running.
- [ ] A teammate who hasn't seen it does the mini-assignment in ≤ 20 minutes.
- [ ] The guided examples and exercises work on the smallest recommended model, or the plan says what to do there instead.
- [ ] Delivery uploaded on Mon 2026-12-07.

## 9. Roles

Agreed on 2026-10-15: the old roles of Slava (cloud + service) and Jan (cloud deploy + Docker) ended with the cloud plan. New roles are **to be agreed by the team**, at the latest at the mentoring session on 2026-10-27 (the worksheet asks for final roles there).

| Member | Role | Status |
|---|---|---|
| Giulia Vallepulcini | Presentation + Q&A | from 2026-09-30, to confirm |
| Szymon Domański | Exercises + questions | from 2026-09-30, to confirm |
| Slava (rvv) | TBD | to agree |
| Jan Piták | TBD | to agree |
| Afomia | TBD | joined later; to agree |

Work that needs an owner: setup guide and hardware table, test matrix on different machines, guided examples (live demo), example bank, mini-assignment and its rubric, cheat sheet, help desk on lab day, Delivery package, after-action report.

## 10. Risks

| Risk | Mitigation |
|---|---|
| Students arrive without Ollama or the model | Setup guide sent ~1 week before with "do it at home"; a check-in form; USB offline kit; exercises work in pairs |
| Weak laptops are too slow | The table always has a smaller row (down to ~1 GB); "under 5 tok/s → one row down"; pair up |
| A "thinking" model looks frozen or returns an empty answer | The class default doesn't think. For the optional thinking models the guide shows `--think=false` and `reasoning_effort="none"` (checked on 2026-10-15: without it, `qwen3.5:9b` spent a 200-token budget on reasoning and returned empty `content`) |
| Answers differ between model sizes, or are wrong | Every example ends with "run it"; a wrong answer becomes a teaching point. Answers from the projector models are pre-generated for the demo |
| Over time (2 h is tight) | Timer at the dry run; a cut list in the runbook; the mini-assignment always starts on time |
| Someone shares their Ollama over campus Wi-Fi | The guide and the talk say never `OLLAMA_HOST=0.0.0.0`; sharing is not a fallback |
| A new Ollama release changes commands | Re-test the guide one week before the lab |

## 11. Decision log

Entries up to 2026-10-01 are kept for history. They are **superseded** by the 2026-10-07 decision; the cloud files they mention survive only at git tag `cloud-final`.

- **2026-09-30** *(superseded)*: Scope, users, load (30 concurrent), budget (€0), laptop + server modes, API surface (OpenAI-compatible + explain/review/tests/fix, streaming), per-person keys + admin API + usage stats (no content), handoff line with Jan, and agent roster, all agreed with Slava. FIM deferred.
- **2026-09-30** *(superseded)*: Cloud. OVH dropped: its trial bills the card automatically after the credit runs out, so there's no hard cap. **Modal is primary**, with a $28 Workspace budget and a $0 spend limit. GCP Cloud Run is the optional backup. Also asking the professor for credits or a UniTrento GPU.
- **2026-09-30** *(superseded)*: Topology. Gateway + SQLite on a separate always-on free CPU VM (Oracle Always Free; Azure for Students as fallback), deployed by Jan. Modal vLLM is the upstream only.
- **2026-09-30** *(superseded)*: Model. Qwen3.6-35B-A3B-FP8 on L40S, thinking off; same-GPU fallback Qwen3-Coder-30B-A3B-Instruct-FP8. Szymon's smoke review picks between them. Lab date: ask the professor.
- **2026-10-01** *(superseded)*: Code review (1 major, 7 minor) and security code review (2 high, 3 low) done. All fixed and re-verified live against Ollama on Slava's Mac. All acceptance criteria of that PRD met in laptop mode; GPU numbers were pending the rehearsal.
- **2026-10-01** *(superseded)*: Readable output requested by Slava. Response formats chosen by `Accept` header (text / markdown report / code), with server-named downloadable files; default stays OpenAI-compatible. See DECISIONS D9.
- **2026-10-07**: **Drafting session with the mentor: only local.** Decisions and notes:
  1. only local, no cloud (test before the lab);
  2. local LLMs have no limits;
  3. define the schedule and bring many examples;
  4. a table of every kind of hardware and the local LLM it can run;
  5. about 40 min of idea and subject focused on the local setup, then guided examples, then 2–3 exercises;
  6. examples: the LLM comments on Go code for someone who doesn't know Go, and writes a simple service;
  7. exercises go from simple to sophisticated and from guided to "add something of your own";
  8. a mini-assignment at the end: something shown in the lab, with a modification such as another programming language.

  Lab date confirmed: **Wed 2026-12-09**. This replaces the cloud, topology, model, keys/limits and load decisions above.
- **2026-10-15**: Slava: the **Go gateway is dropped from the lab**; students use Ollama directly. Cloud and server-mode material deleted on branch `local-only-lab`: `HANDOFF-jan.md`, cloud and performance research, the sign-up and professor-email handoffs, Giulia's and Szymon's cloud-lab handoffs, `.env.server.example`, the key and maintenance scripts, and `cmd/loadtest`. The last pre-decision state is tagged `cloud-final`. The load-test corpus moved to `lab/code/` as lab snippets. The gateway code stays as a legacy project. Roles of Slava, Jan and Afomia: TBD until the team agrees.
- **2026-10-15**: Class default model: the `qwen2.5-coder` family at the size each machine fits (no thinking, sizes 0.5B–32B, the same prompts everywhere). Newer or bigger models (`qwen3.5` with thinking off, `gpt-oss:20b`, `qwen3-coder:30b`) are optional. Measured on Slava's M4 Pro 24 GB with Ollama 0.40.3: `qwen2.5-coder:1.5b` 153 tok/s, `qwen3.5:9b` (`--think=false`) 55 tok/s.
