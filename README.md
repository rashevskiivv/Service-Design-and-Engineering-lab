# Local Generative AI for Coding: SDE LAB_07 (team 7)

Lab materials for **"Local Generative AI for Coding"**, Service Design and Engineering, UniTrento. The lab is on **Wed 2026-12-09**.

Every student runs an open-weights coding model **on their own laptop** with [Ollama](https://ollama.com). There's no cloud and no shared server, so there are no API keys and no limits. The model answers on `localhost`, through the CLI or an OpenAI-compatible API.

## Quick start (students)

Do this at home, before the lab. The model is a download of 1–20 GB.

1. Find your row in the **hardware→model table**: [docs/lab-setup.md §1](docs/lab-setup.md#1-pick-your-model).
2. Install Ollama from <https://ollama.com/download> (Linux: `curl -fsSL https://ollama.com/install.sh | sh`).
3. Download your model and check its speed (replace the tag with yours):

   ```sh
   ollama pull qwen2.5-coder:7b
   ollama run qwen2.5-coder:7b --verbose "Write a Python function that reverses a string"
   ```

   `eval rate` is your speed in tokens per second. Under 5 tok/s, take the next row down.

4. Use it from code. The base URL is `http://localhost:11434/v1`, and any API key works:

   ```python
   from openai import OpenAI  # pip install openai

   client = OpenAI(base_url="http://localhost:11434/v1", api_key="ollama")
   reply = client.chat.completions.create(
       model="qwen2.5-coder:7b",
       messages=[{"role": "user", "content": "Explain what a Go slice is"}],
   )
   print(reply.choices[0].message.content)
   ```

More commands: [lab/cheat-sheet.md](lab/cheat-sheet.md). Problems: [lab-setup.md §7](docs/lab-setup.md#7-troubleshooting).

## What's here

| Path | What |
|---|---|
| [docs/lab-setup.md](docs/lab-setup.md) | Setup guide for students, with the hardware→model table |
| [docs/lab-plan.md](docs/lab-plan.md) | 2-hour schedule, talk outline, guided examples, example bank, exercise ladder, mini-assignment |
| [docs/runbook-lab-day.md](docs/runbook-lab-day.md) | Countdown, test matrix, offline kit, help desk and fallbacks |
| [lab/](lab/README.md) | Buggy programs in Python, Java, Go, C and C++ with their error output; the cheat sheet |

## Milestones

| Date | What |
|---|---|
| Wed 2026-10-07 | Drafting session with the mentor: **only local** ✅ |
| Tue 2026-10-27 | Mentoring session: schedule, examples, hardware table, final roles |
| Tue 2026-11-24 | Dry run |
| Mon 2026-12-07 | Delivery (Google Drive) |
| Wed 2026-12-09 | **LAB_07**, 08:30–11:30, A109 |

## Team and history

Team 7: Slava (rvv), Jan Piták, Giulia Vallepulcini, Szymon Domański, Afomia. Roles are being agreed.

Until the mentor's "only local" decision on 2026-10-07, the plan was a shared cloud GPU behind our own Go API gateway. That version (gateway code, cloud research, design docs, load tester) is kept on branch [`archive/cloud-gateway`](https://github.com/rashevskiivv/Service-Design-and-Engineering-lab/tree/archive/cloud-gateway) and tag `cloud-final`. It is not part of the lab.
