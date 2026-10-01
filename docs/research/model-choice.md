# Model choice: open-weights coding LLM

Researcher: Cloud & model agent · **All facts checked 2026-09-30** against the linked source (`[Mx]`).
Benchmark numbers are **vendor-reported** unless stated otherwise. Harnesses differ between vendors, so compare rows only roughly.
Checkpoint sizes were computed from the Hugging Face file listings. Ollama sizes come from ollama.com tag pages.
**UNVERIFIED** marks anything I could not confirm today.

---

## TL;DR: pick per hardware tier

| Tier | Pick | Weights | Why | Alternative |
|---|---|---|---|---|
| **80 GB (H100 / A100 80 GB)**, lab GPU | **`Qwen/Qwen3.6-35B-A3B-FP8`** (vLLM, thinking **off**) | 37.5 GB | Apache-2.0, Apr 2026. MoE with **3 B active**, so prefill is cheap when 30 students paste code at once. SWE-bench Verified 73.4 (Qwen) / 70.1 (measured by NVIDIA). SWE-bench **Multilingual** 67.2 / 63.4. LCB v6 80.4. Hybrid attention gives a small KV cache (≈20 KiB/token). | `Qwen/Qwen3.6-27B-FP8` (30.9 GB, dense). Better scores (SWE-V 77.2, SWE-ML 71.3, LCB 83.9) but ~9× more compute per token. Or `Qwen/Qwen3.8-27B-FP8` (Aug 2026, LCB v6 90.3), newest and least battle-tested. Choose after the load test. |
| **48 GB (L40S / RTX 6000 Ada / A6000)** | **`Qwen/Qwen3.6-35B-A3B-FP8`** with `--language-model-only`, `--max-model-len 16384` | 37.5 GB | Same model as the lab, so dev = prod. Leaves roughly 4–7 GB for KV + per-sequence state (my estimate: 0.92 × 45 GiB minus weights minus ~2 GB activations/graphs). | 4-bit AWQ (`QuantTrio/Qwen3.6-35B-A3B-AWQ`, 25.5 GB, community) if the KV cache is too small. Or `Qwen3.6-27B-FP8` (30.9 GB), which leaves more KV room. |
| **24 GB (L4 / A10)** | **`cyankiwi/Qwen3-Coder-30B-A3B-Instruct-AWQ-4bit`** (vLLM) | 18.1 GB | Coder-specialised, **non-thinking** (answers stream immediately), Apache-2.0, 3.3 B active. | `openai/gpt-oss-20b` (12.8 GiB MXFP4, `reasoning_effort: low`) via llama.cpp. vLLM on Ada is "actively working" per the vLLM recipe, so it is UNVERIFIED there. |
| **16 GB (T4 / RTX 4000-class)** | **`gpt-oss-20b`** via llama.cpp / Ollama (`gpt-oss:20b`, 14 GB) | 12.8 GiB | OpenAI states it "run[s] on systems with as little as 16GB memory". Aider Polyglot 26.6 (medium). | `Qwen/Qwen2.5-Coder-14B-Instruct-AWQ` (10.0 GB, official AWQ, works on T4 with vLLM). Older, but no thinking and 32K context. |
| **CPU-only, 24 GB RAM** | **gpt-oss-20b GGUF** (llama.cpp `llama-server`) | ~13 GB | Fewest active params (3.6 B) among the good models, so the best tokens/s on CPU. | `Qwen3-Coder-30B-A3B-Instruct` Q4_K_M (18.6 GB, tight in 24 GB). Either way only a few users at a time: **queue**. |
| **MacBook M4 Pro 24 GB** (laptop mode) | **`gpt-oss:20b`** (already pulled, 14 GB) | 14 GB | Fits under the default Metal limit (~⅔ of RAM ≈ 16 GB). | `qwen3.5:9b` (6.6 GB) or `qwen2.5-coder:7b` (4.7 GB, no thinking, good for integration tests). `qwen3-coder:30b` (19 GB) and `qwen3.6:27b` (17 GB) exceed ~16 GB without `sysctl iogpu.wired_limit_mb`, so don't use them for the demo. **`qwen2.5-coder:1.5b-base` is a base/FIM model, not for chat.** |

**Final recommendation:** serve **Qwen3.6-35B-A3B-FP8** on the rented GPU (L40S for dev, H100 for lab, see `cloud-options.md`). Use **gpt-oss:20b** in laptop mode. Keep **Qwen3-Coder-30B-A3B AWQ** as the plan for a 24 GB GPU (e.g. Modal L4 / Cloud Run L4).

---

## 1. Requirements recap

- Good at **Python, Java, Go, C/C++**. Tasks: explain / review / tests / fix on snippets, plus free chat.
- **30 concurrent streaming users**, ≥ ~10 tok/s each, TTFT of a few seconds.
- Open weights, licence OK for a university lab (non-commercial; permissive preferred).
- Served by vLLM (GPU) or Ollama / llama.cpp (laptop/CPU) behind our OpenAI-compatible Go gateway.

---

## 2. Candidates: facts

Active = parameters used per token (MoE). Context = native context from `config.json` or the card.
Sizes are checkpoint sizes (weights only). **Add KV cache and activations on top.**

| Model (HF id) | Released | Params total / active | Licence | Context | Thinking by default? | BF16 | FP8 | 4-bit (AWQ/GPTQ) | GGUF Q4_K_M | vLLM | Ollama tag |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `Qwen/Qwen3.6-35B-A3B` | 2026-04-15 | 36.0 B (incl. vision) / 3 B; 256 experts, 8 routed + 1 shared | Apache-2.0 | 262,144 | **Yes**, switchable [M1] | 71.9 GB | **37.5 GB** (official) | 25.0–25.5 GB (community: cyankiwi, QuantTrio); NVFP4 (nvidia/RedHatAI, Blackwell only) | 22.1 GB (unsloth) | ≥ 0.19.0 recommended [M1] | `qwen3.6:35b-a3b` (24 GB q4_K_M), `-q8_0` 39 GB |
| `Qwen/Qwen3.6-27B` | 2026-04-21 | 27.8 B dense | Apache-2.0 | 262,144 | Yes, switchable [M2] | 55.6 GB | **30.9 GB** (official) | 20.4 GB (`cyankiwi/…-AWQ-INT4`) | 16.8 GB | ≥ 0.19.0 [M2] | `qwen3.6:27b` (17 GB q4_K_M) |
| `Qwen/Qwen3.8-27B` | 2026-08-05 | 27.8 B dense | Apache-2.0 | 262,144 | Yes, switchable; `reasoning_effort` [M3] | ~55.6 GB (est.) | **30.9 GB** (official) | n/a checked | Ollama 18 GB | "compatible with … vLLM, SGLang" [M3] | `qwen3.8:27b` |
| `Qwen/Qwen3.5-9B` | 2026-02-27 | 9.65 B dense | Apache-2.0 | 262,144 | Yes | 19.3 GB | — | — | 5.7 GB | ≥ 0.19 (same arch) | `qwen3.5:9b` (6.6 GB) |
| `Qwen/Qwen3-Coder-30B-A3B-Instruct` | 2025-07-31 | 30.5 B / 3.3 B; 128 experts, 8 active | Apache-2.0 | 262,144 | **No** (instruct) | 61 GB | **31.2 GB** (official) | **18.1 GB** (`cyankiwi/…-AWQ-4bit`) | 18.6 GB | yes | `qwen3-coder:30b` (19 GB) |
| `Qwen/Qwen3-Coder-Next` | 2026-01-30 | 79.7 B / ~3 B; 512 experts, 10 active | Apache-2.0 | 262,144 | No | ~159 GB | ~80 GB (official FP8) | — | Ollama 52 GB | yes | `qwen3-coder-next` (52 GB). Needs a 96 GB+ GPU, **out of scope** |
| `Qwen/Qwen2.5-Coder-7B/14B/32B-Instruct` | 2024-09/11 | 7.6 / 14.8 / 32.8 B dense | Apache-2.0 | 32,768 (YaRN to 128K) | No | 15.2 / 29.5 / 65.5 GB | — | **5.6 / 10.0 / 19.3 GB** (official AWQ) | Ollama 4.7 / 9.0 / 20 GB | yes | `qwen2.5-coder:7b` / `:14b` / `:32b` |
| `openai/gpt-oss-20b` | 2025-08-04 | 20.9 B / 3.6 B; 32 experts, 4 active | Apache-2.0 | 131,072 | **Yes, always** (low/medium/high effort; harmony format) [M5] | — | native **MXFP4**: checkpoint 12.8 GiB [M5] | — | Ollama 14 GB | H100/H200/B200 + A100 (Marlin MXFP4); Ada "actively working" [M9] | `gpt-oss:20b` |
| `mistralai/Devstral-Small-2-24B-Instruct-2512` | 2025-11-28 | 24 B dense (vision) | Apache-2.0 | 393,216 | No (agentic tool use) | ~48 GB | native **FP8** ≈ 25 GB (est.) | — | Ollama 15 GB | yes | `devstral-small-2:24b` |
| `zai-org/GLM-4.7-Flash` | 2026-01-19 | 31.2 B / ~3 B (64 experts, 4 active) | MIT | 202,752 | Yes (UNVERIFIED) | 62.4 GB | community | community | Ollama 19 GB | yes (UNVERIFIED version) | `glm-4.7-flash` |
| `nvidia/NVIDIA-Nemotron-3.5-Lightning-30B-A3B-BF16` | 2026-08-01 | 31.6 B / ~3 B (hybrid Mamba) | OpenMDW-1.1 (permissive; not re-read) | 262,144 | UNVERIFIED | 66 GB (Ollama bf16) | Ollama mxfp8 34 GB | NVFP4 23 GB | Ollama 25 GB | yes (card shows vLLM cmd) | `nemotron-3.5-lightning:30b-a3b` |
| `google/gemma-4-26B-A4B-it` | 2026-03-11 | 25.8 B / ~4 B | **Apache-2.0** | 262,144 | UNVERIFIED | 51.6 GB | Ollama mxfp8 29 GB | QAT w4a16 (official) | Ollama 18 GB | yes | `gemma4:26b` |
| `ibm-granite/granite-4.2-30b` / `-8b` | 2026-08-07 | 29.3 B / 8.8 B dense | Apache-2.0 | 131,072 | UNVERIFIED | ~58 / ~18 GB | MXFP4 variants (official) | — | Ollama 17–18 / 5.3 GB | yes | `granite4.2:30b` / `:8b` |
| `deepseek-ai/DeepSeek-Coder-V2-Lite-Instruct` | 2024-06-14 | 15.7 B / 2.4 B | DeepSeek Model License ("supports commercial use" [M6]) | 128K (card) | No | 31.4 GB | — | — | Ollama `16b` 8.9 GB | yes (`trust_remote_code`) | `deepseek-coder-v2:16b` |
| `mistralai/Codestral-22B-v0.1` | 2024-05-29 | 22.2 B dense | **MNPL-0.1** (Mistral *Non-Production* Licence) | 32,768 | No | ~44 GB | — | — | Ollama 13 GB | yes | `codestral:22b` |

Sources: HF API / `config.json` / model cards [M1–M8], Ollama tag pages [M10], vLLM docs [M9].

---

## 3. Coding benchmarks (focus: multi-language)

SWE-bench **Multilingual** (C, C++, Go, Java, JS/TS, PHP, Ruby, Rust) and **Aider Polyglot** (C++, Go, Java, JS, Python, Rust) are the closest to our 4 languages. **MultiPL-E** is older and saturated.

| Model | SWE-bench Verified | SWE-bench Multilingual | LiveCodeBench v6 | Aider Polyglot | Other |
|---|---|---|---|---|---|
| **Qwen3.6-35B-A3B** | **73.4** (Qwen) · **70.12** (NVIDIA-measured) | **67.2** (Qwen) · **63.40** (NVIDIA) | 80.4 | — | Terminal-Bench 2.0: 51.5 [M1, M7] |
| Qwen3.6-27B | 77.2 | 71.3 | 83.9 | — | SWE-bench Pro 53.5 [M2] |
| Qwen3.8-27B | — | — | **90.3** | — | SWE-bench Pro 61.7; Terminal-Bench 2.1: 73.0 [M3] |
| Qwen3.5-27B / 35B-A3B | 72.4–75.0 / 69.2–70.0 | 69.3 / 60.3 | 80.7 / 74.6 | — | [M1, M4] (numbers differ between the 3.5 and 3.6 cards) |
| Qwen3.5-9B | — | — | 65.6 | — | [M4] |
| Qwen3-Coder-Next (80B-A3B) | 70.6–71.3 | — | 58.9 (report's LCB split) | **66.2** | MultiPL-E 88.2 [M11] |
| Qwen3-Coder-30B-A3B | 51.6 (OpenHands, 100 turns; Qwen image, quoted in [M12]) | — | — | — | UNVERIFIED as text |
| gpt-oss-20b | 60.7 high · 53.2 med · 37.4 low (OpenAI); 52.44 (NVIDIA) | 41.93 (NVIDIA) | 74.6 (per Qwen) / 61.0 (per Z.ai) | **34.2 high · 26.6 med · 16.6 low** | [M5, M7, M4, M8] |
| Devstral Small 2 (24B) | 68.0 | 55.7 | — | — | Terminal-Bench 2: 22.5 [M13] |
| GLM-4.7-Flash | 59.2 | — | 64.0 | — | [M8] |
| Nemotron 3.5 Lightning 30B-A3B | 51.56 | 39.33 | — | — | [M7] |
| Gemma 4 26B-A4B | 57.40 (NVIDIA) vs **17.4** (Qwen table): harness-dependent, treat with care | 43.40 (NVIDIA) | 77.1 (Google) | — | [M7, M1, M14] |
| granite-4.2-30b / -8b | 57.0 / 47.67 | 41.89 / 30.78 | 75.77 / 73.24 | — | [M15] |
| Qwen2.5-Coder-32B-Instruct | — | — | 31.4 (older LCB window) | **16.4** (whole) / 8.0 (diff) | MultiPL-E avg 79.4 (Py 92.7, Java 80.4, C++ 79.5) [M16, M17] |
| Qwen2.5-Coder-14B-Instruct | — | — | 23.4 (older LCB) | — | MultiPL-E avg 79.6 (Py 89.0, Java 79.7, C++ 85.1) [M16] |
| Qwen2.5-Coder-7B-Instruct | — | — | 18.2 (older LCB) | — | MultiPL-E avg 76.5 (Py 87.8, Java 76.5, C++ 75.6) [M16] |
| DeepSeek-Coder-V2-Lite-Instruct | 0.0 | — | 24.3 (older LCB) | 44.4 (old Aider *edit* bench, not Polyglot) | HumanEval Py 81.1, Java 76.6, C++ 75.8 [M18] |
| Codestral 22B v0.1 | — | — | — | — (Codestral 25.01, API-only: 11.1) | [M17] |

Reading this table:
- The 2026 Qwen3.5/3.6/3.8 generation is **far ahead** of 2024 coder models (Qwen2.5-Coder, DeepSeek-Coder-V2-Lite, Codestral) on every multi-language agentic benchmark.
- NVIDIA's independent-ish table (Nemotron card) confirms Qwen3.6-35B-A3B leads the ~30B-A3B class, **above** gpt-oss-20b, Gemma 4 26B-A4B and Nemotron 3.5 Lightning [M7].
- **Go** is not in MultiPL-E's published Qwen2.5 table. Go coverage comes from SWE-bench Multilingual and Aider Polyglot, which our pick leads.
- The official Aider leaderboard has not been updated since Oct 2025 and lacks all 2026 small models [M17].

---

## 4. Serving notes (for the architect / performance / Jan)

1. **Turn thinking off** for Qwen3.5/3.6/3.8. They "operate in thinking mode by default" [M1]. Options:
   - Server-wide: `--reasoning-parser qwen3 --default-chat-template-kwargs '{"enable_thinking": false}'` [M9b].
   - Per request: `chat_template_kwargs: {"enable_thinking": false}` [M1].

   With the reasoning parser, any thinking text arrives in `reasoning_content`, not `content`. The gateway must pass unknown delta fields through or drop them. Recommended non-thinking sampling: `temperature 0.7, top_p 0.8, top_k 20, presence_penalty 1.5` [M1].
2. Add **`--language-model-only`** to skip the vision encoder and free KV memory [M1, M9b].
3. **Context**: set `--max-model-len` to 16K–32K. The 262K default wastes memory, and the ≥128K advice applies to thinking mode only [M1].
4. **MTP speculative decoding:** leave off for the lab. It "degrades text throughput under high concurrency" [M9b].
5. **KV-cache sizing** (my arithmetic from `config.json`, bf16):

   | Model | KV per token | Extra per-sequence state |
   |---|---|---|
   | Qwen3.6-35B-A3B | 10 of 40 layers full-attn × 2 KV heads × 256 dim ≈ **20 KiB/token** | Gated-DeltaNet state ≈ 30 layers × 32 heads × 128 × 128 × 2 B ≈ **31 MB per sequence** |
   | Qwen3.6-27B | ≈ 64 KiB/token | ≈ 75 MB per sequence |
   | Qwen3-Coder-30B-A3B | ≈ **96 KiB/token** | — |
   | gpt-oss-20b | ≈ 24 KiB/token (12 full layers; the other 12 slide over 128 tokens) | — |

   30 users × 8K tokens each ≈ 4.7 GB for Qwen3.6-35B-A3B vs 22.5 GB for Qwen3-Coder-30B-A3B.
6. **MoE caveat for the performance agent:** at batch ≈ 30, each layer touches a large share of the 256 experts, so the memory-bandwidth advantage of "3 B active" shrinks during decode. The big win is **prefill/TTFT** when 30 students paste 1–3K-token files at once: ~3 B vs ~27 B FLOPs/token. Measure `Qwen3.6-35B-A3B-FP8` vs `Qwen3.6-27B-FP8` on the rehearsal GPU before the lab.
7. **gpt-oss** needs the harmony chat format, which vLLM, Ollama and llama.cpp handle. It always reasons, so use `reasoning_effort: "low"` for fast visible output. vLLM officially supports H100/H200/B200 and A100. On Ada (L4/L40S) vLLM is "actively working" [M9], so use llama.cpp/Ollama there or test early.
8. **vLLM needs compute capability ≥ 7.5**, so V100/V100S are out [M9].
9. **FP8 checkpoints** run natively on Ada/Hopper/Blackwell. On A100 they run weight-only via Marlin (expected to work; UNVERIFIED for this architecture).

---

## 5. Rejected, and why

| Model | Reason |
|---|---|
| Codestral 22B v0.1 | MNPL = non-production licence. 2024-era quality. No reason to take the licence risk. |
| DeepSeek-Coder-V2-Lite | 2024, SWE-bench 0.0, needs `trust_remote_code`. Clearly beaten. |
| Qwen2.5-Coder-7B/14B/32B | Good and simple (no thinking), but superseded. Keep **7B** only for fast laptop tests and **14B-AWQ** for a 16 GB T4 fallback. |
| Qwen3-Coder-Next / Qwen3.8-Flash-Next / DeepSeek-V4-Flash / GLM-5.3-Flash | 80 B–763 B total, too big for one ≤80 GB GPU. Qwen3.8-Flash-Next is also under the non-Apache "qwen-community-1.0" licence. |
| Devstral Small 2 | Strong agentic SWE, but dense 24 B (slower per token than 3 B-active MoE) and tuned for tool-using agents rather than chat explanations. |
| Nemotron 3.5 Lightning, GLM-4.7-Flash, Gemma 4 26B-A4B, Granite 4.2 | All fine, but behind Qwen3.6-35B-A3B on multi-language SWE-bench in the same weight class [M7]. |

---

## 6. Least-certain claims

1. The Qwen3-Coder-30B-A3B SWE-bench Verified score (51.6) is only in an image plus an HF discussion quote.
2. Whether vLLM runs gpt-oss-20b on Ada GPUs (L4/L40S) today.
3. The exact FP8 size of Devstral Small 2 (the repo holds two formats) and the unconfirmed Granite 4.2 thinking default.

---

## Sources

All checked 2026-09-30.

- [M1] Qwen3.6-35B-A3B card: https://huggingface.co/Qwen/Qwen3.6-35B-A3B
  - FP8: https://huggingface.co/Qwen/Qwen3.6-35B-A3B-FP8
- [M2] Qwen3.6-27B card: https://huggingface.co/Qwen/Qwen3.6-27B
- [M3] Qwen3.8-27B card: https://huggingface.co/Qwen/Qwen3.8-27B
- [M4] Qwen3.5 cards:
  - https://huggingface.co/Qwen/Qwen3.5-9B
  - https://huggingface.co/Qwen/Qwen3.5-27B
  - https://huggingface.co/Qwen/Qwen3.5-35B-A3B
- [M5] gpt-oss model card:
  - https://arxiv.org/html/2508.10925v1
  - https://huggingface.co/openai/gpt-oss-20b
- [M6] DeepSeek-Coder-V2-Lite-Instruct card: https://huggingface.co/deepseek-ai/DeepSeek-Coder-V2-Lite-Instruct
- [M7] NVIDIA Nemotron 3.5 Lightning card (NVIDIA-measured comparison table): https://huggingface.co/nvidia/NVIDIA-Nemotron-3.5-Lightning-30B-A3B-BF16
- [M8] GLM-4.7-Flash card: https://huggingface.co/zai-org/GLM-4.7-Flash
- [M9] vLLM GPU requirements and gpt-oss recipe:
  - https://docs.vllm.ai/en/latest/getting_started/installation/gpu.html
  - https://docs.vllm.ai/projects/recipes/en/latest/OpenAI/GPT-OSS.html
- [M9b] vLLM Qwen3.5/3.6 recipe: https://raw.githubusercontent.com/vllm-project/recipes/main/Qwen/Qwen3.5.md
- [M10] Ollama tags: https://ollama.com/library/ (`qwen3.6`, `qwen3.5`, `qwen3.8`, `qwen3-coder`, `qwen3-coder-next`, `gpt-oss`, `qwen2.5-coder`, `devstral-small-2`, `glm-4.7-flash`, `gemma4`, `granite4.2`, `nemotron-3.5-lightning`, `deepseek-coder-v2`, `codestral`)
- [M11] Qwen3-Coder-Next tech report: https://arxiv.org/html/2603.00729v1
- [M12] Qwen3-Coder-30B-A3B:
  - https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct
  - Discussion #30: https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct/discussions/30
- [M13] Devstral Small 2 card: https://huggingface.co/mistralai/Devstral-Small-2-24B-Instruct-2512
- [M14] Gemma 4 cards:
  - https://huggingface.co/google/gemma-4-26B-A4B-it
  - https://huggingface.co/google/gemma-4-31B-it
- [M15] Granite 4.2 cards:
  - https://huggingface.co/ibm-granite/granite-4.2-30b
  - https://huggingface.co/ibm-granite/granite-4.2-8b
- [M16] Qwen2.5-Coder tech report (Table 17, MultiPL-E): https://arxiv.org/html/2409.12186
- [M17] Aider Polyglot leaderboard data: https://raw.githubusercontent.com/Aider-AI/aider/main/aider/website/_data/polyglot_leaderboard.yml
- [M18] DeepSeek-Coder-V2 paper: https://arxiv.org/html/2406.11931
- [M19] Apple Silicon Metal memory limit (~⅔ of RAM ≤ 36 GB; `iogpu.wired_limit_mb`), community source: https://github.com/ggml-org/llama.cpp/discussions/2182
- Quantized repos:
  - https://huggingface.co/cyankiwi/Qwen3-Coder-30B-A3B-Instruct-AWQ-4bit
  - https://huggingface.co/QuantTrio/Qwen3.6-35B-A3B-AWQ
  - https://huggingface.co/cyankiwi/Qwen3.6-27B-AWQ-INT4
  - https://huggingface.co/unsloth/Qwen3.6-35B-A3B-GGUF
