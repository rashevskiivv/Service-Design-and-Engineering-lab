# Lab-day runbook (local only)

- **Lab:** Wed 2026-12-09, 08:30–11:30, room A109. **T** = lab start; T-7d is 7 days before.
- **Topology:** there is none. Every student runs Ollama and a model on their own laptop ([lab-setup.md](lab-setup.md)). We host nothing, hand out no keys and meter nothing (mentor notes 1 and 2, [lab-plan.md §7](lab-plan.md)).
- **Plan of the 2 hours:** [lab-plan.md](lab-plan.md). Owners marked *TBD* get names when the team agrees on roles.

The cloud runbook (GPU bring-up, key slips, maintenance switch) is obsolete; it is on branch `archive/cloud-gateway`.

## 0. Cheat sheet for the team

| What | Command |
|---|---|
| Speed of a model | `ollama run <tag> --verbose "Write a Python function that reverses a string"` → `eval rate` |
| GPU or CPU? | `ollama ps` (`PROCESSOR` column) |
| Thinking off | `ollama run qwen3.5:9b --think=false`; API: `reasoning_effort="none"` |
| Keep a model warm for the demo | `ollama run <tag> --keepalive 3h "hi"` |
| More context | `OLLAMA_CONTEXT_LENGTH=8192 ollama serve` (quit the app first) |
| Free memory | `ollama stop <tag>` |
| Disk | `ollama list`, `ollama rm <tag>` |

## 1. Countdown

- [ ] **Tue 27.10, mentoring session:** bring the [lab plan](lab-plan.md) (schedule, examples, ladder, mini-assignment), the hardware table, a first slide skeleton. Leave with **final roles** (worksheet item).
- [ ] **By ~T-4w (Tue 10.11): test matrix** (§2). Every team member installs Ollama on their own machine and fills a row. Owner: TBD.
- [ ] **By T-3w:** update the speed column of [lab-setup.md §1](lab-setup.md) with the measured numbers; drop or move rows that turned out too slow. Pre-generate the E1, E2 and B10 answers on a small and a bigger model (screenshots or text) for the projector.
- [ ] **Tue 24.11 (T-15d), dry run:** the whole lab with a timer, every slide, example and exercise. A teammate who hasn't seen it does the mini-assignment (≤ 20 min). Test the offline kit (§3) on a clean machine. Record a backup video of E1 and E2.
- [ ] **~Wed 02.12 (T-7d): announcement to the class** (channel TBD: Moodle or the course chat): `lab-setup.md` + `lab/cheat-sheet.md`, "do steps 1–4 at home", and a short form: OS, chip/GPU, RAM, model tag, `eval rate`, works yes/no. Check Ollama's release notes; if a new version came out, re-run the setup on one Mac and one Windows machine.
- [ ] **Mon 07.12 (T-2d), Delivery** to Google Drive: slides, `lab-setup.md`, `lab-plan.md`, `lab/` (code + cheat sheet), the mini-assignment text, and where and which generative-AI tools we used to prepare the lab.
- [ ] **T-1d:**
  - read the form; write to everyone who answered "doesn't work" or reported under 5 tok/s;
  - USB sticks ready (§3);
  - projector laptop and backup laptop: the models from E1/E2 pulled (`qwen2.5-coder:1.5b`, `qwen2.5-coder:7b`, one bigger model), pre-generated answers open, font size large, Wi-Fi off test passes;
  - everyone's laptop charged.

## 2. Test matrix (mentor note 1: "test before the lab")

One row per machine; cover at least **Apple Silicon 8 or 16 GB, Windows with NVIDIA, Windows without a GPU, Linux,** and the weakest laptop we can find. Use the prompt from [lab-setup.md §4](lab-setup.md); run it twice and keep the second `eval rate` (the first includes loading). Also time E1 (paste `words.go` + error) to see how long the first word takes on a pasted file.

| Who | OS | Chip / GPU | RAM / VRAM | Model tag | `eval rate` (tok/s) | First word on E1 (s) | `ollama ps` | Works for the lab? | Notes |
|---|---|---|---|---|---|---|---|---|---|
| Slava | macOS 15 | Apple M4 Pro | 24 GB | `qwen2.5-coder:1.5b` | 153 | – | 100% GPU | yes | 2026-10-15, Ollama 0.40.3 |
| Slava | macOS 15 | Apple M4 Pro | 24 GB | `qwen3.5:9b` `--think=false` | 55 | – | 100% GPU | yes | 2026-10-15, MLX runner |
| Slava | macOS 15 | Apple M4 Pro | 24 GB | `gpt-oss:20b` | 49.5 | – | 100% GPU | yes | measured 2026-09-30 |
| … | | | | | | | | | |

Slava's rows still miss the class-default `qwen2.5-coder:7b` and the E1 timing.

## 3. Offline kit (USB sticks)

For students who arrive without Ollama or without a model. Campus Wi-Fi is not an option for multi-GB downloads.

- **Installers** for macOS, Windows and Linux, the version we tested (from <https://ollama.com/download>).
- **Small models** (`qwen2.5-coder:1.5b`, `qwen2.5-coder:3b`): pulled on a clean machine, then the whole Ollama models folder copied to the stick (`~/.ollama/models` on macOS/Linux, `%HOMEPATH%\.ollama\models` on Windows). The student quits Ollama, copies the folder's contents into the same place on their machine, and starts Ollama again. **Untested: try it at the dry run**, including Mac → Windows. If it fails, the fallback is pairing (§4, L1).
- Two or three sticks; one team member runs the "USB station" during the setup check.

## 4. During the lab

**Help desk:** two team members walk around from the setup check until the end (names TBD). One keeps the time and calls the cut list ([lab-plan.md §1](lab-plan.md)).

**Fallback ladder** (the helper decides on the spot; the timekeeper announces L2 and up):

| Step | Situation | Action | Tell students |
|---|---|---|---|
| **L0** | One student: slow (< 5 tok/s), freezes, `ollama ps` shows a CPU/GPU split | One row down in the table; close the browser tabs and the IDE's AI plugins; `ollama stop` other models | "Take the next smaller model; it's the same family, the prompts stay the same." |
| **L1** | One student without Ollama or a model | Pair with a neighbour now; USB kit in parallel if they want their own setup. Last resort: `qwen2.5-coder:1.5b` (1 GB) over a phone hotspot | "Work in pairs for now." |
| **L2** | More than a third of the class without a working setup | Exercises in pairs or trios; the projector runs every guided step | "We continue in pairs." |
| **L3** | The projector laptop fails | Switch to the backup laptop (same models, same answers prepared) | – |
| **L4** | Both fail | Pre-generated answers and the recorded E1/E2 video | – |

**Never** "fix" the lab by sharing a team laptop's Ollama over campus Wi-Fi (`OLLAMA_HOST=0.0.0.0`): it is unauthenticated and open to the whole network.

## 5. After the lab

- [ ] Collect the mini-assignment submissions (where: TBD with the teachers); 2 weeks to submit.
- [ ] Note which hardware failed or was slow, from the form and the help desk; update the table.
- [ ] **After-action report** (1 page of critical self-reflection, course rule): draft within a week while it's fresh. Owner: TBD.
- [ ] Remind students they can free the disk space: `ollama list`, `ollama rm <tag>`.
