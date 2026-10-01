# API guide for exercises (Szymon + students)

For: Szymon (exercises and questions), and the students who solve them. Part A can be copied onto the exercise sheet as is.
Source of truth: `api/openapi.yaml`, `README.md`, `DECISIONS.md` D3 and D6. Limits are the **lab server** values; laptop mode is stricter.

---

## Part A. Student quick start

### What you get

| | |
|---|---|
| **Base URL** | `https://HOST/v1` (the real name is on your slip; `HOST` is a placeholder until Jan sets up the server) |
| **Key** | Printed on your paper slip: `lgai_…`. It is personal and stops working shortly after the lab. Put it in an environment variable, never in code, and never push it to Git. |
| **Model** | Always `"coder"`. Responses show the real model id in the `model` field. Don't copy that back: anything other than `coder` returns 404. |
| **Protocol** | OpenAI-compatible: the official `openai` SDK, curl, or any IDE tool that accepts a custom base URL. |

Set the two variables once per terminal (the slip shows the same lines):

```sh
export OPENAI_BASE_URL=https://HOST/v1
export OPENAI_API_KEY=lgai_...        # from your slip
```

PowerShell: `$env:OPENAI_BASE_URL="https://HOST/v1"` and `$env:OPENAI_API_KEY="lgai_..."`. On Windows use `curl.exe`, not the `curl` alias.

Use **streaming** (`stream: true`). The answer starts in about a second instead of after the whole text is ready. Non-streaming answers are also cut at 1,024 tokens.

### 1. Chat

**curl (streaming):**

```sh
curl -N "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"coder","stream":true,
       "messages":[{"role":"user","content":"Write a Go function that reverses a string with Unicode support."}]}'
```

**curl (non-streaming, readable output with `jq`):**

```sh
curl -s "$OPENAI_BASE_URL/chat/completions" \
  -H "Authorization: Bearer $OPENAI_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"coder","messages":[{"role":"user","content":"What does `static` mean in C?"}]}' \
  | jq -r '.choices[0].message.content'
```

**Python (`pip install openai`):**

```python
from openai import OpenAI

client = OpenAI()  # reads OPENAI_BASE_URL and OPENAI_API_KEY

stream = client.chat.completions.create(
    model="coder",
    stream=True,
    messages=[
        {"role": "system", "content": "You are a concise programming tutor."},
        {"role": "user", "content": "Explain the difference between a Java interface and an abstract class."},
    ],
)
for chunk in stream:
    if chunk.choices:  # the last chunk may have no choices
        print(chunk.choices[0].delta.content or "", end="", flush=True)
print()
```

### 2. Coding tasks: `explain`, `review`, `tests`, `fix`

`POST /v1/tasks/{task}` sends your code with a prompt tuned for the task and language. The answer has the same shape as chat (Markdown in `content`).

| Field | Required | Values / notes |
|---|---|---|
| `language` | yes | `python`, `java`, `go`, `c`, `cpp` (C++ is `cpp`) |
| `code` | yes | Your source code |
| `error` | **`fix` only** | The compiler / runtime / test output, pasted exactly. Optional context for the other tasks |
| `framework` | `tests` only (400 elsewhere) | Free text, up to 64 characters from `A-Z a-z 0-9 space . + # ( ) / _ -`. Defaults: Python `pytest`, Java `JUnit 5`, Go standard `testing` (table-driven), C `assert.h` test program with `main`, C++ `GoogleTest` |
| `instructions` | no | Up to 1,000 characters, e.g. `"focus on memory safety"` or `"answer in Italian"` |
| `stream`, `max_tokens`, `temperature`, `model` | no | As in chat. Default lengths: explain 512 tokens, review / tests / fix 1,024 |

**Unknown fields are rejected** (400 `unknown_field`), so a typo like `lang` fails loudly instead of being ignored.

What each task returns:

| Task | Returns |
|---|---|
| `explain` | Summary, walkthrough of the code, inputs/outputs/side effects |
| `review` | At most 5 findings: bugs, security issues, code smells |
| `tests` | Unit tests in a code block |
| `fix` | The cause and the corrected code |

**curl examples (streaming)**. For readable output, drop `"stream":true` and pipe to `jq -r '.choices[0].message.content'`.

```sh
H=(-H "Authorization: Bearer $OPENAI_API_KEY" -H "Content-Type: application/json")

# explain (Java)
curl -N "$OPENAI_BASE_URL/tasks/explain" "${H[@]}" -d '{"stream":true,"language":"java",
  "code":"public static int f(int[] a){int s=0;for(int x:a)s+=x;return s/a.length;}"}'

# review (C)
curl -N "$OPENAI_BASE_URL/tasks/review" "${H[@]}" -d '{"stream":true,"language":"c",
  "code":"int main(int argc,char**argv){char buf[8]; strcpy(buf, argv[1]); printf(buf); return 0;}"}'

# tests (Go, explicit framework)
curl -N "$OPENAI_BASE_URL/tasks/tests" "${H[@]}" -d '{"stream":true,"language":"go","framework":"testing (table-driven)",
  "code":"func Clamp(x, lo, hi int) int { if x < lo { return lo }; if x > hi { return hi }; return x }"}'

# fix (Python, with the error output)
curl -N "$OPENAI_BASE_URL/tasks/fix" "${H[@]}" -d '{"stream":true,"language":"python",
  "code":"def avg(xs):\n    return sum(xs) / len(xs)\n\nprint(avg([]))",
  "error":"ZeroDivisionError: division by zero"}'

# C++ uses "cpp"
curl -N "$OPENAI_BASE_URL/tasks/review" "${H[@]}" -d '{"stream":true,"language":"cpp",
  "code":"std::vector<int> v{1,2,3}; for (auto it=v.begin(); it!=v.end(); ++it) if(*it==2) v.erase(it);"}'
```

To send a whole file safely (no manual escaping), let `jq` build the JSON:

```sh
jq -n --arg code "$(cat Main.java)" '{stream:true, language:"java", code:$code}' |
  curl -N "$OPENAI_BASE_URL/tasks/review" "${H[@]}" -d @-
```

**Python (streaming).** The SDK has no method for `/tasks`, so use its generic `client.post`:

```python
from pathlib import Path
from openai import OpenAI, Stream
from openai.types.chat import ChatCompletion, ChatCompletionChunk

client = OpenAI()  # OPENAI_BASE_URL + OPENAI_API_KEY

def task(name: str, language: str, code: str, **extra) -> None:
    """name: explain | review | tests | fix. extra: error=..., framework=..., instructions=..."""
    stream = client.post(
        f"/tasks/{name}",
        cast_to=ChatCompletion,
        body={"language": language, "code": code, "stream": True, **extra},
        stream=True,
        stream_cls=Stream[ChatCompletionChunk],
    )
    for chunk in stream:
        if chunk.choices:
            print(chunk.choices[0].delta.content or "", end="", flush=True)
    print()

task("explain", "go", Path("worker.go").read_text())
task("review", "c", Path("parse.c").read_text(), instructions="focus on memory safety")
task("tests", "java", Path("StringUtils.java").read_text(), framework="JUnit 5")
task("fix", "python", Path("search.py").read_text(),
     error="IndexError: list index out of range (line 7)")
```

Other languages: any HTTP client works (same JSON, `Authorization: Bearer …`). The OpenAI client libraries for Java and Go only need a custom base URL for chat.

### 3. Limits you may hit (lab server)

| Limit | Value | What you get |
|---|---|---|
| Requests per key | 6 per minute, bursts of 3 | `429` `rate_limit_exceeded`, `Retry-After: 10`-ish |
| Parallel requests per key | 2 | `429` `concurrency_limit_exceeded`, `Retry-After: 1` |
| Tokens per key per day | 300,000 (resets 00:00 UTC = 02:00 in Italy while summer time lasts, until 25 Oct) | `429` `insufficient_quota`; **not** retried automatically |
| Input size | 16 KiB of code + error + instructions (≈ 4,500 tokens, roughly 300–400 lines) | `413` `request_too_large` |
| Answer length | chat 512 tokens by default; tasks 512 / 1,024; hard cap 1,536 (maybe 2,048); non-streaming cap 1,024 | `finish_reason: "length"`, the answer is cut. Ask for less, or split the code |
| Whole class at once | 30 answers generate in parallel; up to 24 more wait ≤ 30 s | `503` `server_overloaded`, `Retry-After: 10` |
| Maintenance | — | `503` `maintenance` |

What the status codes mean:

- **429**: you are going too fast; wait `Retry-After` seconds.
- **503**: the shared GPU is full, or the team switched maintenance on; retry after `Retry-After`.
- **The Python SDK retries 429 and 503 automatically** (twice by default, waiting `Retry-After`), so you will usually just notice a short pause. With curl, wait and re-run.
- `401`: wrong or expired key, or the key is not sent as `Authorization: Bearer …`. A key in the URL never works.
- `400`: a bad field or value; the message names the field.
- `404`: wrong model name (use `coder`) or a misspelled task.
- `502`/`504`: the model server had a problem. Retry in a minute and tell the team if it persists.

### 4. Rules for the lab

- One person, one key. Don't share it, post it or commit it. If it leaks, tell us and you get a spare.
- Nothing you send is stored: the server records only counts (tokens, timing, status), never your code or the answers.
- **Treat answers as suggestions.** Compile, run and test them. The model can be confidently wrong, especially about library APIs.

---

## Part B. Notes for writing exercises

The lab model is a mid-size open model (`Qwen3.6-35B-A3B`, thinking off; fallback `Qwen3-Coder-30B-A3B`), not a frontier model. Exercises work best when:

- **They are snippet-level:** one function or class, ≤ ~150 lines (well under the 16 KiB cap). It sees only what is sent: no repository, no files, no internet.
- **The bug is concrete:** paste the exact error or test output into `error` for `fix`.
- **There is something to verify:** students compile and run the answer, so the model is a helper, not an oracle.
- **They match the task endpoints,** so students try all four.
- **One exercise shows a failure on purpose**, e.g. a niche or invented library function, so students see hallucination.
- **Start times are staggered:** 30 people pasting at the same second makes the first answer take several seconds (queue).

### 5 ideas per language category

| # | Python | Java | Go | C / C++ |
|---|---|---|---|---|
| 1 | **explain**: a dense generator/comprehension pipeline (e.g. word frequencies with `itertools.groupby`) | **explain**: a stream pipeline with `Collectors.groupingBy` + `counting()` | **explain**: a worker pool with goroutines, channels and `sync.WaitGroup` | **review (C)**: `strcpy` into `char buf[8]` plus `printf(user_input)` (overflow + format string) |
| 2 | **review**: a function with a mutable default argument and a bare `except:` | **review**: a class overriding `equals` but not `hashCode`, stored in a `HashSet` | **review**: ignored `err` values and `defer f.Close()` inside a loop over many files | **fix (C)**: returning a pointer to a local array (gcc warning + garbage output) |
| 3 | **tests** (pytest): `parse_duration("1h30m") -> int` seconds, incl. bad input | **tests** (JUnit 5): `isPalindrome(String)` with null, empty, case and Unicode | **tests** (table-driven): `ParseKV("a=1;b=2") (map[string]string, error)` | **tests (C, assert.h)**: `int parse_int(const char *s, int *out)` overflow / empty / sign cases |
| 4 | **fix**: off-by-one in a binary search with its `IndexError` traceback | **fix**: `ConcurrentModificationException` from removing inside a for-each loop (stack trace) | **fix**: `panic: assignment to entry in nil map` with the stack trace | **fix (C++)**: erasing from a `std::vector` while iterating (crash / UB) |
| 5 | **chat**: "make this O(n²) duplicate finder O(n)", then ask for the complexity of both | **review**: a `FileReader` without try-with-resources; ask for the Java 17 idiom | **fix**: a data race on a counter, given `go test -race` output (mutex vs `atomic`) | **explain (C++)**: a small RAII class with rule of five vs `std::unique_ptr` |

Each idea is one function plus, for `fix`, the exact error text, which fits a 2–3 hour lab.

---

## Part C. The smoke review: Szymon picks the model (DECISIONS D3)

**Goal:** choose between `Qwen/Qwen3.6-35B-A3B-FP8` (primary) and `Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8` (same-GPU fallback) based on the answers students would actually get.

**When:** at rehearsal 1 (runbook T-4d), on the real GPU. The laptop smoke runs used a tiny test model (`qwen2.5-coder:1.5b`), so judge **nothing** from them.

**How the answers are produced** (Slava / Jan run it, one GPU, one model at a time):

1. With Qwen3.6 deployed:
   ```sh
   loadtest -url https://HOST -smoke -out loadtest-results/smoke-qwen36
   ```
2. Jan redeploys the Modal app with the fallback model, and Slava points `LGAI_MODELS` at it. Run again:
   ```sh
   loadtest -url https://HOST -smoke -out loadtest-results/smoke-qwen3coder
   ```

Each folder holds `index.md` (machine verdicts) and 20 files named `<task>-<language>.md`: 4 tasks × 5 languages, the same small snippets for both models. The machine verdicts are:

| Verdict | Meaning |
|---|---|
| **FAIL** | Empty answer or a failed request. Disqualifying unless it's a one-off network error |
| **WARN** | Cut at the token limit, or a `tests`/`fix` answer without a code block |
| **PASS** | Neither problem; not a quality judgement |

**Your rubric.** Score each of the 20 answers per model 0 / 1 / 2 on:

| Criterion | 0 | 1 | 2 |
|---|---|---|---|
| Correct | wrong / doesn't compile | minor slip | correct; you would accept it |
| Useful to a student | misses the point | partly | finds the real issue, explains why |
| Fits the format | rambling or cut | ok | concise; `review` ≤ 5 findings; `tests`/`fix` with a runnable code block |

Then:
- Also check 3–5 of **your own exercises** by hand on each model (Part A curl/Python). These matter more than the built-in snippets.
- **Watch for:** invented APIs, tests that don't test the edge cases, a "fix" that hides the error instead of fixing the cause, and any visible `<think>` text (thinking should be off; report it).
- **Decision rule:**
  - Take the model with the higher total, weighting Java, Go, C and C++ at least as much as Python.
  - If the totals are within ~10 %, tell Slava: he breaks the tie with the rehearsal speed numbers.
- **Send Slava** the two totals, the winner, and 2–3 example answers that made the difference, by the end of rehearsal day. Slava and Jan switch the model if needed (one redeploy) before the freeze.
