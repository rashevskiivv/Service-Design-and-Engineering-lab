# Lab setup: run a coding LLM on your own laptop

For every student of LAB_07 (SDE, Wed 2026-12-09). Everything runs on your machine: no account, no API key, no limits, and no internet once the model is downloaded.

**Do steps 1–4 at home, the evening before the lab.** The model is a download of 1–20 GB, and 30 laptops downloading at once over campus Wi-Fi will not work.

**Status (2026-10-15):** commands checked against the Ollama docs and on a MacBook Pro M4 Pro 24 GB with Ollama 0.40.3. The other rows of the table are not tested yet; the team fills them in before the lab (runbook §2).

## 0. What you need

| OS | Minimum | GPU acceleration |
|---|---|---|
| macOS | macOS 14 Sonoma or newer | Apple Silicon (M1–M5): yes, via Metal. Intel Macs: CPU only |
| Windows | Windows 10 22H2 or newer, Home or Pro. No administrator rights needed | NVIDIA with driver 551.61 or newer; some AMD Radeon cards (ROCm or Vulkan) |
| Linux | Any recent distribution | NVIDIA with driver 550 or newer; AMD with ROCm v7, or Vulkan |

- **Disk:** the model's size plus about 5 GB. On Windows add 4 GB for Ollama itself.
- **Python 3** for the examples and exercises. Java, Go, gcc or g++ only if you want to run those snippets too.
- **Can't install** (tablet, Chromebook, older macOS or Windows)? Pair up with a neighbour in the lab. That is a normal way to do the lab.

## 1. Pick your model

Find your hardware:

- **macOS:** Apple menu → About This Mac → *Chip* and *Memory*.
- **Windows:** Task Manager → Performance → *Memory*; under *GPU*, the *Dedicated GPU memory* (NVIDIA/AMD only).
- **Linux:** `free -h`; for NVIDIA, `nvidia-smi`.

Then pick your row. The **class default** is the `qwen2.5-coder` family: a coding model that answers right away (no "thinking" phase), in sizes from 0.5B to 32B, so every prompt in the lab works the same way on every row. The last column is optional, for when you're curious.

| Your machine | Memory | Class default | Download | Speed (tok/s) | Bigger / newer, optional |
|---|---|---|---|---|---|
| **Apple Silicon** (M1–M5) | 8 GB | `qwen2.5-coder:3b` | 1.9 GB | ≈ 20–40 | `qwen3.5:2b` (2.7 GB, thinks: §4) |
| | 16 GB | `qwen2.5-coder:7b` | 4.7 GB | ≈ 12–25 | `qwen3.5:9b` (6.6–7.6 GB, thinks), tight |
| | 24–32 GB | `qwen2.5-coder:7b` | 4.7 GB | ≈ 30–50 | `qwen3.5:9b`: **55 measured** (M4 Pro 24 GB, thinking off) · `gpt-oss:20b` (14 GB, thinks): **49.5 measured** · `qwen2.5-coder:14b` (9.0 GB) |
| | 36 GB and more | `qwen2.5-coder:14b` | 9.0 GB | ≈ 15–45 | `qwen3-coder:30b` (19 GB, fast: only 3B active) |
| **NVIDIA GPU** (Windows / Linux) | 4 GB VRAM | `qwen2.5-coder:3b` | 1.9 GB | ≈ 30–60 | – |
| | 6–8 GB VRAM | `qwen2.5-coder:7b` | 4.7 GB | ≈ 30–60 | `qwen3.5:4b` (3.3–4.0 GB, thinks) |
| | 12–16 GB VRAM | `qwen2.5-coder:14b` | 9.0 GB | ≈ 25–50 | `qwen3.5:9b` (thinks); `gpt-oss:20b` with 16 GB (thinks) |
| | 24 GB VRAM and more | `qwen2.5-coder:14b` | 9.0 GB | ≈ 50–80 | `qwen3-coder:30b` (19 GB) · `qwen2.5-coder:32b` (20 GB) |
| **CPU only** (most laptops with integrated graphics, Intel Macs) | 8 GB RAM | `qwen2.5-coder:1.5b` | 1.0 GB | ≈ 10–25 | `qwen2.5-coder:0.5b` (0.4 GB, very weak) |
| | 16 GB RAM | `qwen2.5-coder:3b` | 1.9 GB | ≈ 6–15 | `qwen2.5-coder:7b`, ≈ 3–8 tok/s |
| | 32 GB RAM and more | `qwen2.5-coder:7b` | 4.7 GB | ≈ 3–8 | `gpt-oss:20b` (14 GB, thinks; 3.6B active, so faster than its size suggests) |
| **Fastest test model**, any machine | – | `qwen2.5-coder:1.5b` | 1.0 GB | **153 measured** (M4 Pro) | Fine for the setup check; weak on real code |

How to read it:

- **Measured** numbers are from our laptops (Ollama's `eval rate`). **≈** numbers are rough estimates from memory size and bandwidth, not measurements. The team replaces them before the lab.
- **Under 5 tok/s** feels like waiting. Go one row down. 10 tok/s and more is comfortable reading speed.
- **AMD Radeon or Intel Arc:** if `ollama ps` shows the model on the GPU (step 4), use the NVIDIA row with the same memory. Otherwise use the CPU-only row.
- **Apple Silicon:** the GPU can use only part of the memory (roughly two thirds by default), and your browser and IDE need some too. That's why a 16 GB Mac doesn't get a 14B model.
- **CPU only:** reading a pasted file is slow too. 100 lines of code can take 10–30 s before the first word appears.
- **Licences:** all are open weights. `qwen2.5-coder:3b` is under the Qwen Research licence (non-commercial, fine for a course); the other `qwen2.5-coder` sizes are Apache 2.0.

## 2. Install Ollama

- **macOS / Windows:** download the installer from <https://ollama.com/download> and run it. Ollama then runs in the background (menu-bar or tray icon).
- **Linux:**

  ```sh
  curl -fsSL https://ollama.com/install.sh | sh
  ```

Check:

```sh
ollama --version
```

We tested 0.40.3. Any newer version should work.

## 3. Download your model

Use your tag from step 1:

```sh
ollama pull qwen2.5-coder:7b
ollama list
```

## 4. Check that it answers, and how fast

```sh
ollama run qwen2.5-coder:7b --verbose "Write a Python function that reverses a string"
```

- The answer streams in. After it, `--verbose` prints timings. **`eval rate`** is your speed in tokens per second.
- Without a prompt, `ollama run qwen2.5-coder:7b` opens a chat. Type `/bye` to leave.
- Is it on the GPU? While the model is loaded, run:

  ```sh
  ollama ps
  ```

  The `PROCESSOR` column should say `100% GPU` (Apple Silicon, NVIDIA). A CPU/GPU split on a GPU machine means the model is too big: go one row down. `100% CPU` is expected on CPU-only machines.

**Optional thinking models** (`qwen3.5`, `gpt-oss`) reason before answering. In the lab, turn that off so they answer right away:

```sh
ollama run qwen3.5:9b --think=false --verbose "Write a Python function that reverses a string"
```

`gpt-oss` can't turn thinking off completely. Use `--think=low` there.

**Tell us your result** before the lab: your row, the model tag and the `eval rate` (the form link comes with the lab announcement).

## 5. Use it from code

Ollama serves an OpenAI-compatible API at `http://localhost:11434/v1`. The client wants an API key, but Ollama ignores it; any string works.

```sh
curl -N http://localhost:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen2.5-coder:7b","stream":true,
       "messages":[{"role":"user","content":"Explain what a Go slice is"}]}'
```

On Windows, use the Python version: PowerShell's `curl` is a different program, and its quoting and line breaks differ.

```python
# pip install openai
from openai import OpenAI

client = OpenAI(base_url="http://localhost:11434/v1", api_key="ollama")
stream = client.chat.completions.create(
    model="qwen2.5-coder:7b",
    stream=True,
    messages=[{"role": "user", "content": "Write a unit test for a Java Stack class"}],
)
for chunk in stream:
    if chunk.choices and chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="", flush=True)
```

- **Thinking models through the API:** add `reasoning_effort="none"` to `create(...)` (`"low"` for `gpt-oss`). Without it, `qwen3.5` spends its tokens on hidden reasoning first. We checked: with `max_tokens=200` it returned an empty answer.
- **Context length:** Ollama's default is 4096 tokens on machines with less than 24 GB of GPU memory. That fills up fast with pasted code. Raise it to 8192 in the Ollama app's settings (context length slider), or start the server yourself:
  - macOS / Linux: `OLLAMA_CONTEXT_LENGTH=8192 ollama serve`
  - Windows PowerShell: `$env:OLLAMA_CONTEXT_LENGTH=8192; ollama serve`

  More context needs more memory. If `ollama serve` says "address already in use", quit the Ollama app first (menu-bar or tray icon; Linux: `sudo systemctl stop ollama`).

## 6. Optional: in your editor

Editor extensions such as [Continue](https://www.continue.dev) for VS Code and JetBrains can use Ollama as their model provider. That's not part of the lab and we haven't tested it; try it after the lab if you like.

## 7. Troubleshooting

| Problem | Fix |
|---|---|
| `connection refused` on port 11434 | Ollama isn't running. Start the app, or run `ollama serve` |
| `address already in use` on `ollama serve` | Ollama is already running. Quit the app or stop the service (§5) |
| Very slow, laptop hot, fans loud | Model too big. Check `ollama ps`; go one row down (§1) |
| `model not found` | Run `ollama list` and use the exact tag |
| Long silence before the answer, or an empty answer | A thinking model. Use `--think=false` (CLI) or `reasoning_effort="none"` (API), or use the class default |
| Answer cut off, or it ignores half of your code | Context too small. Set it to 8192 (§5) |
| Not enough disk space | `ollama rm <tag>` removes a model. Windows: set `OLLAMA_MODELS` to a folder on another drive |
| Windows: `curl` complains about `-H` or the JSON | Use the Python version (§5) |
| The answer is confidently wrong | Normal for small models. Run the code; that's what the lab is about |

## 8. Safety

- **Never set `OLLAMA_HOST=0.0.0.0`** on campus or other shared Wi-Fi. Ollama has no authentication; anyone on the network could use your model and also pull or delete models.
- Download models only with `ollama pull` from the official library.
- Read and run generated code the way you would code from a stranger on the internet: it can be wrong or unsafe.

## 9. After the lab

Models stay on disk. `ollama list` shows them, and `ollama rm <tag>` frees the space.
