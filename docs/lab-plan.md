# Lab plan: schedule, examples, exercises, mini-assignment

**Draft of 2026-10-15** for the mentoring session on Tue 2026-10-27. It follows the mentor's notes from 2026-10-07 (listed in §7). Owners per block are TBD until the team agrees on roles; the presentation (Giulia) and the exercises (Szymon) keep their owners from 2026-09-30, to confirm.

Course rules that shape it: everything fits in **2 hours**, including helping classmates; examples are **very simple**; slides are not overloaded; the practical mini-assignment takes **15–20 minutes**, ideally at the end of the lab. The teachers' theory question (~10 min, Moodle) comes on top. The room is booked 08:30–11:30, so there is buffer.

Code for the examples and exercises: [`lab/code/`](../lab/README.md). Commands for students: [`lab/cheat-sheet.md`](../lab/cheat-sheet.md).

## 1. Schedule (2 h)

Times assume we start at 08:30.

| Time | Min | Block | What happens | Note | Who |
|---|---|---|---|---|---|
| 08:30 | 25 | **Idea and subject** | Talk (§2). Ends with the hardware→model table | 5 | Giulia |
| 08:55 | 15 | **Setup check, hands-on** | Everyone runs `ollama run <tag> --verbose "..."` and reads the `eval rate` off the screen. Help desk walks around; no model → pair up or USB kit | 1, 4, 5 | help desk TBD |
| 09:10 | 20 | **Guided examples** | E1 "I don't know Go" and E2 "write a simple service" (§3), on the projector; students type along | 6 | TBD |
| 09:30 | 40 | **Exercises** | X1 fix it (10) → X2 review, then test (12) → X3 add your own (18) (§5) | 7 | Szymon + help desk |
| 10:10 | 20 | **Mini-assignment** | Brief (3 min), then students start; questions answered while they work (§6) | 8 | TBD |
| 10:30 | – | End of our 2 h | Buffer, teachers' theory question, feedback | – | – |

The first 40 minutes are the mentor's "idea and subject, focused on the local setup" (note 5): 25 minutes of talk plus 15 minutes of everyone getting their own model to answer.

**Cut list** if we run late (the timekeeper decides):

1. Show only E1 live; E2 from a pre-recorded answer.
2. Shorten X2 to "review only", without tests.
3. X3 becomes optional homework.
4. The mini-assignment **always** starts by 10:10.

## 2. Talk outline (25 min, slides by Giulia)

1. **Why run a coding LLM locally** (3 min): your code never leaves the laptop; works offline; €0; **no limits** — no quotas, no rate limits, no API keys (note 2); open weights you can inspect and reproduce. The trade-off: smaller models, weaker than frontier ones.
2. **How a model fits on a laptop** (6 min):
   - parameters and quantization: 7B parameters at ~4 bits ≈ a 4.7 GB file;
   - memory decides which model you can run; memory bandwidth decides how fast (tokens/s ≈ bandwidth ÷ model size);
   - the context window also costs memory;
   - "thinking" models reason before they answer: slower, sometimes better.
3. **The stack** (5 min): Ollama = runner + model library → an OpenAI-compatible REST API on `localhost:11434` → any client (CLI, `curl`, the `openai` SDK, editors). Link to the course: **a local LLM is just another service behind a REST API**, the same shape as the services in the REST lectures.
4. **Which model for my computer** (5 min): the hardware→model table ([lab-setup.md §1](lab-setup.md)), how to read `eval rate` and `ollama ps` (note 4).
5. **What local models are good and bad at** (4 min). Rules of thumb from Slava's daily use (MacBook M4 Pro, `gpt-oss:20b` + `qwen2.5-coder`):
   - good: explaining code and compiler errors, spotting classic bugs (off-by-one, null, leaks, `strcpy`), test scaffolding, boilerplate;
   - weak: anything that doesn't fit in the prompt, niche or recent library APIs (it invents functions), concurrency and undefined behaviour, consistency;
   - rule: if you already know what the answer should look like, use local; if you're asking because you don't know, verify twice;
   - **facts from tools, judgement from the model**: always run the code. Our own examples (E1 below; and the 1.5B model once called `def avg(xs): return sum(xs)/len(xs)` "correct, no bugs").
6. **Safety** (2 min): Ollama stays on `localhost`, never `OLLAMA_HOST=0.0.0.0` on campus Wi-Fi; treat generated code like code from a stranger.

## 3. Guided examples (20 min, note 6)

Each example: the exact prompt on a slide, run live on the projector, students type along on their own model. Answers from a small and a bigger model are **pre-generated** in case the live run is slow: [`lab/answers/`](../lab/answers/README.md) (`qwen2.5-coder:1.5b`, `qwen2.5-coder:7b`, `qwen3.5:9b`).

### E1. "I don't know Go" (10 min)

File: [`lab/code/go/words/words.go`](../lab/code/go/words/words.go) — reverses the words of a sentence and finds the longest word. It panics.

1. Explain:

   ```
   I know Python but not Go. Explain this Go program line by line,
   and compare it with Python where that helps.

   <paste words.go>
   ```

2. Predict: "What does it print?"
3. Debug: show [`words.error.txt`](../lab/code/go/words/words.error.txt):

   ```
   This Go program crashes with the panic below. Explain in 3 sentences
   why, and give the smallest fix.

   <paste words.go>
   <paste words.error.txt>
   ```

4. Run the fix (`go run words.go` on the projector).

**What we saw on 2026-10-15** (M4 Pro, Ollama 0.40.3; saved run in [`lab/answers/E1/`](../lab/answers/README.md)):

- *Predict:* none of the three models noticed that the program panics. All "predicted" an output; `qwen2.5-coder:1.5b` even invented scrambled words.
- *Debug, first try:* `qwen2.5-coder:1.5b` and `qwen3.5:9b` (thinking off) both gave the right fix (`j := len(words)-1`) and both explained it wrongly. The 1.5B blamed trailing spaces and punctuation and added an unneeded `TrimRight`; the 9B said the crash comes "once `i` reaches 9", but it happens on the first iteration.
- *Debug, saved run:* `qwen2.5-coder:7b` gave the right fix with a wrong story: "an odd number of words" (there are 10), and a loop condition `i < j-1` that isn't in its own code. `qwen3.5:9b` right fix, muddled story. `qwen2.5-coder:1.5b` this time gave a wrong fix (bounds checks around `words[1]`): same model, same prompt, a different answer.

That's the point of the slide: **the code was right, the story was wrong, and only running it tells you which part to trust.**

### E2. "Write a simple service" (10 min)

```
Write a minimal REST service in Python using only the standard library
(http.server, json). Endpoints:
- GET /notes returns all notes as a JSON list
- POST /notes with body {"text": "..."} adds a note and returns it with an id
Keep the data in memory. Listen on 127.0.0.1:8000.
After the code, give curl commands that test both endpoints.
```

1. Save the answer as `notes.py`, run `python3 notes.py`.
2. In a second terminal, run the model's curl commands (Windows: the projector shows a Python `urllib` alternative).
3. Teaching points:
   - constraints in the prompt (standard library only, so nothing to `pip install` on lab Wi-Fi; a fixed address);
   - ask for the test commands too;
   - it ties the lab to the course: we just designed a REST resource with an LLM.

**Does the model's service run?** Checked 2026-10-15 on the M4 Pro: answer saved as `notes.py`, started with `python3 notes.py` (Python 3.9.6), then the model's own curl commands plus one more `GET`. Answers in [`lab/answers/E2/`](../lab/answers/README.md).

| Model | Runs? | What we saw |
|---|---|---|
| `qwen2.5-coder:1.5b` | yes, as written | `GET` → `[]`; `POST` → `201` with an `id`; `GET` lists it. No input validation |
| `qwen2.5-coder:7b` | yes, with one line changed | It listened on **all interfaces** (`""` instead of `127.0.0.1`); we fixed that before starting it. `GET /notes` returns `{"notes": [...]}`, not a JSON list. Both curls pass. A good live point: check the answer against every constraint of the prompt |
| `qwen3.5:9b` (thinking off) | yes, as written | All 4 curls pass; `400` for a missing `text` or invalid JSON; ids start at 0 |

E2 is the base for X3 and the mini-assignment.

## 4. Example bank (note 3: "bring many examples")

Spare examples for the demo, for fast students, and for questions. Files are in [`lab/code/`](../lab/README.md); each has a real bug and the error output a student would see.

| # | Shows | Input | Prompt idea |
|---|---|---|---|
| B1 | Fix from a traceback | `python/words.py` + `words.error.txt` (`KeyError`) | "Fix this; then rewrite it with `collections.Counter`" |
| B2 | Security review | `c/names.c` + `names.error.txt` (stack buffer overflow in `strcat`) | "Review this C code for memory-safety bugs; explain the ASan report" |
| B3 | A compiler error in plain words | `cpp/stack.cpp` + `stack.error.txt` (const correctness) | "Explain this g++ error to a Java programmer" |
| B4 | Types | `java/Library.java` + `Library.error.txt` (lossy `double` → `int`) | "Fix it without losing cents" |
| B5 | Tests that catch a bug | `java/Temperatures.java` (off-by-one in `maxRise`) | "Write JUnit 5 tests for `maxRise`, including edge cases" |
| B6 | Concurrency | `go/wordfreq/` + error (`concurrent map writes`) | "Fix it two ways: a mutex, and one map per goroutine. Which is better here?" |
| B7 | Memory bug | `c/students.c` + error (use after free in `free_list`) | "Find the bug from the sanitizer output" |
| B8 | Rule of three | `cpp/matrix.cpp` + error (double free) | "Why does it crash at exit? Fix it in modern C++" |
| B9 | Translate | `python/gradebook.py` | "Rewrite this in Java (or Go), same behaviour" — then find that both versions crash on a student with no grades (`ZeroDivisionError`) |
| B10 | Model size matters | `def avg(xs): return sum(xs)/len(xs)` | "Review this for bugs" on `qwen2.5-coder:1.5b` vs a 7B+ model. On 2026-10-15 all three models found the empty list ([answers](../lab/answers/README.md)), so the 1.5B's "no bugs" doesn't reproduce reliably: show it as run-to-run variance |
| B11 | Hallucination on purpose | – | "How do I use Python's `json.parse_strict()`?" (the function doesn't exist; small models often explain it anyway) |
| B12 | Thinking on/off | any B-item | Same prompt with `--think=true` and `--think=false` on `qwen3.5`: time vs quality |
| B13 | The model inside your own program | – | 10-line Python script with the `openai` client that sends a file to `localhost:11434/v1` and prints a review (the cheat sheet has the base code) |

More ideas per language (explain / review / tests / fix): a Python comprehension pipeline, Java `equals` without `hashCode`, Go ignored `err` + `defer` in a loop, C `printf(user_input)`, C++ erasing from a vector while iterating.

## 5. Exercises: the ladder (40 min, note 7)

From simple to sophisticated, and from guided to "add something of your own". Pairs are fine.

### X1. Fix it with the LLM (10 min, fully guided)

Pick one program: [`python/words.py`](../lab/code/python/words.py), [`java/Temperatures.java`](../lab/code/java/Temperatures.java) or [`go/words/words.go`](../lab/code/go/words/words.go). Use this prompt as it is:

```
This <language> program fails with the error below.
Explain the cause in 2 sentences, then give only the fixed function.

Code:
<paste the program>

Error:
<paste the .error.txt file>
```

**Done when** the program runs and prints the right result.

### X2. Review, then test (12 min, half guided)

Pick [`python/gradebook.py`](../lab/code/python/gradebook.py), [`java/Library.java`](../lab/code/java/Library.java) or [`c/names.c`](../lab/code/c/names.c).

1. Ask the model to review the code for bugs (write the prompt yourself; hint on the slide: say the language, ask for a list ordered by severity).
2. Ask it for unit tests that would have caught the most serious bug.
3. Run the tests.

**Done when** the tests run and you can name one claim of the model that was wrong or that you couldn't verify.

### X3. Add something of your own (18 min, open)

Start from your E2 `notes.py`. With the model's help, add **one feature of your choice**, for example:

- `DELETE /notes/{id}` with `404` for unknown ids;
- search: `GET /notes?q=word`;
- validation: `400` when `text` is missing or empty;
- saving notes to a JSON file so they survive a restart.

Or write a small tool that uses the local model from code (example B13).

**Done when** it runs, you have a `curl` (or Python) test for the new feature, and you can show the prompts you used.

## 6. Mini-assignment (~20 min, note 8)

Something from the lab, with a modification: **rebuild the E2 notes service in another programming language**, using only your local model, and add one change.

> Using your local model (any row of the table), write the notes service from example E2 in a language other than Python (for example Go, Java or JavaScript/Node.js), with the endpoints `GET /notes` and `POST /notes`, plus **one** of: `DELETE /notes/{id}`, or search with `GET /notes?q=`.
>
> Submit:
> 1. the code (one file is fine);
> 2. the prompts you used, copied from your terminal;
> 3. your model tag, your row of the hardware table, and your `eval rate`;
> 4. 2–3 sentences: one thing the model got wrong and how you noticed.

Students start in the lab and may finish later. The course gives 2 weeks for mini-assignments; where to submit is TBD with the teachers.

Grading idea, to agree with the mentor: it runs; both original endpoints work; the extra endpoint works; the reflection names a concrete mistake.

**Suggestions for the teachers' theory question:** "Why does a 7B-parameter model need about 5 GB of memory?"; "Name two reasons to run a coding LLM locally and one limitation"; "What does an OpenAI-compatible API on localhost give you?"

## 7. Coverage of the mentor's notes

| Note | Where |
|---|---|
| 1. Only local; test before the lab | Whole lab on Ollama; test matrix and dry run in the [runbook](runbook-lab-day.md) |
| 2. No limits | Talk §2.1; no keys or quotas anywhere |
| 3. Schedule + many examples | §1, §3, §4 |
| 4. Hardware → model table | [lab-setup.md §1](lab-setup.md); talk §2.4; setup check |
| 5. 40 min idea and subject → guided examples → 2–3 exercises | §1 (25 + 15 min, E1–E2, X1–X3) |
| 6. Go code commentary; write a simple service | E1, E2 |
| 7. Simple → sophisticated, guided → own | X1 → X2 → X3 |
| 8. Mini-assignment with a modification (another language) | §6 |

## 8. Open questions for the mentoring session (27.10)

- Is the 25 + 15 split of the first 40 minutes OK, or should the setup check move before the talk?
- Can students do the exercises and the mini-assignment in pairs?
- Where do students submit the mini-assignment, and does the grading idea fit?
- Python 3 is required; Java / Go / C toolchains optional. OK?
- Final roles (worksheet: "final roles" at the mentoring session).
