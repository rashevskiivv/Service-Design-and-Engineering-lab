# Performance analysis: capacity, engine, gateway limits, load test, rehearsal

| | |
|---|---|
| Role | Performance engineer (research agent) |
| Date | 2026-09-30 (all sources checked on this date) |
| Scope | PRD §5 performance target: 30 concurrent streaming users, ≥ 10 tok/s each, TTFT ≤ ~5 s p95 |
| Status | Draft for the lead. The numbers are **model estimates** until the GPU rehearsal (§7) replaces them with measurements |

**Conventions.** **ESTIMATE** marks a number that comes out of the performance model in §1.4. **UNVERIFIED** marks a fact I could not confirm from a primary source. Every spec and benchmark has a URL in §9. GB = 10⁹ bytes (vendor specs, file sizes). GiB = 2³⁰ bytes (memory budgets).

---

## 0. TL;DR

1. **Which combos pass** (30 users streaming at once, ≥ 10 tok/s per user, TTFT p95 ≤ 5 s; full table in §2.4):
   - **24 GB class (L4, A10G) and RTX 4000 Ada 20 GB:** pass with **Qwen2.5-Coder-7B-Instruct-AWQ** (≈ 14–16 tok/s per user) or **gpt-oss-20b** (≈ 13–16 tok/s per user; see the reasoning-token caveat in §2.5). The margin is thin: if the real engine is 30 % less efficient than the model assumes, these drop to ≈ 9–11 tok/s. Qwen2.5-Coder-14B-AWQ and Qwen3-Coder-30B-A3B **fail** on these cards. There is not enough KV cache left after the weights.
   - **A100 40/80 GB:** passes by a wide margin (≥ 20 tok/s per user) for every model except the two that only just fit on 40 GB (14B-BF16 and 30B-FP8), which run short of KV. The best candidate there is **Qwen3-Coder-30B-A3B**: AWQ-4bit on 40 GB, BF16 or FP8 on 80 GB.
   - **T4 16 GB:** Qwen2.5-Coder-7B-AWQ is **marginal**. It gives ≈ 10 tok/s per user when all 30 users stream at once, and passes under realistic lab traffic. gpt-oss-20b has no vLLM path on T4.
   - **CPU (Oracle A1):** **fails by more than 10×**. It gives about 10–20 tok/s *in total*, and a 1,500-token prompt takes 20–150 s to prefill. **Note:** since 2026-06-15 the Always Free A1 allowance is **2 OCPU / 12 GB**, not 4 / 24 (§2.6).
2. **"Everyone click now" burst:** 30 prompts of 1,500 tokens arriving in the same second give **TTFT p95 ≈ 11–23 s** on the 24 GB class. Only an A100 gets under 5 s. Streaming speed stays fine. Tell students to expect a few seconds of "thinking" in that moment, or stagger them. Prefix caching helps when they paste the same exercise.
3. **Engine for GPU:** vLLM `v0.30.0`, with `--max-model-len 8192 --max-num-seqs 32 --max-num-batched-tokens 2048 --gpu-memory-utilization 0.90 --enable-prefix-caching`. Exact commands are in §3.1. **Engine for CPU:** `llama-server` with `-np 2 -kvu -c 16384 -t <cores> --cache-reuse 256` (§3.2). **Ollama** is only for laptop mode. Its docs say it reserves `NUM_PARALLEL × CONTEXT_LENGTH` of memory, and a published benchmark shows it far behind vLLM beyond a few users (§3.3).
4. **Gateway defaults for the 24 GB class:** MAX_INFLIGHT = 32, QUEUE_SIZE = 24, QUEUE_TIMEOUT = 30 s, RATE_RPM = 6 per key (burst 3), TOKEN_QUOTA = 300k tokens per key per day. `max_tokens` default / max per endpoint: explain 512/1024, review 768/1536, tests 1024/2048, fix 1024/2048, chat 512/2048 (§5).
5. **Laptop mode (M4 Pro, Ollama, gpt-oss:20b):** about **5 concurrent users** with code-sized prompts (TTFT p95 ≈ 11–13 s, ≈ 24 tok/s each), or **8–10** with short prompts. It cannot serve 30. The bottleneck is prompt processing (≈ 334 tok/s measured) (§4).

---

## 1. Inputs and assumptions

### 1.1 Workload model

| Parameter | Value used | Source / reasoning |
|---|---|---|
| Concurrent users (design point) | 30 | PRD §2 |
| Prompt size (code + system prompt + instruction) | uniform 800–2,200 tokens (mean 1,500) | brief: "typical input 200–2,000 tokens"; I used the heavy end on purpose. **Measure Szymon's exercises** (§7) |
| Output size | uniform 200–1,000 tokens (mean 600) | brief |
| Context the engine must support per request | 8,192 (max prompt ≈ 6K + max output 2K) | §5 |
| Average context during decode | 2,000 tokens (prompt + half the output) | used for KV-read bandwidth |

I simulate three load patterns. All of them are closed-loop: a user sends the next request only after the previous one has finished.

- **SAT (saturation):** 30 users with **zero think time**. All 30 are always streaming, and a new 1,500-token prompt arrives every time a stream ends. This is the literal PRD design point and the most pessimistic steady state. **The verdict uses SAT.**
- **LAB (realistic):** 30 users arrive over 60 s. Think time between requests is exponential with a mean of 30 s. **UNVERIFIED** assumption about how students behave.
- **BURST:** all 30 users send a 1,500-token prompt (500-token answer) at the same instant. This is the "everyone try exercise 1 now" moment.

### 1.2 Hardware specs

| GPU | VRAM (spec) | VRAM visible to CUDA (used here) | Memory bandwidth | Dense FP16 tensor TFLOPS | Source |
|---|---|---|---|---|---|
| NVIDIA T4 | 16 GB GDDR6 | 15.0 GiB (UNVERIFIED typical `nvidia-smi` 15,360 MiB) | 320 GB/s ("320+") | 65 | [NVIDIA T4](https://www.nvidia.com/en-us/data-center/tesla-t4/) |
| NVIDIA L4 | 24 GB | 22.5 GiB (UNVERIFIED typical 23,034 MiB) | 300 GB/s | 121 (242 with sparsity; "one-half lower without sparsity") | [NVIDIA L4](https://www.nvidia.com/en-us/data-center/l4/) |
| NVIDIA A10G (AWS) | 24 GB GDDR6 | 22.5 GiB (UNVERIFIED) | 600 GB/s | 70 (140 with sparsity) | [AWS A10G datasheet](https://d1.awsstatic.com/product-marketing/ec2/NVIDIA_AWS_A10G_DataSheet_FINAL_02_17_2022.pdf) |
| RTX 4000 Ada | 20 GB GDDR6 ECC | 20.0 GiB (UNVERIFIED) | 360 GB/s | ≈ 82 (**ESTIMATE**: 327.6 "peak tensor" ÷ 4, assuming FP8 + sparsity) | [Lenovo Press LP2144](https://lenovopress.lenovo.com/lp2144.pdf), [NVIDIA RTX 4000](https://www.nvidia.com/en-us/design-visualization/rtx-4000/) |
| A100 40 GB | 40 GB HBM2 | 39.5 GiB | 1,555 GB/s | 312 | [A100 datasheet](https://www.nvidia.com/content/dam/en-zz/Solutions/Data-Center/a100/pdf/nvidia-a100-datasheet-us-nvidia-1758950-r4-web.pdf) |
| A100 80 GB | 80 GB HBM2e | 79.2 GiB | 1,935 (PCIe) / 2,039 (SXM) GB/s; I use 1,935 | 312 | [NVIDIA A100](https://www.nvidia.com/en-us/data-center/a100/) |
| Apple M4 Pro (Slava's MBP, 24 GB) | 24 GB unified | ≈ 16 GiB usable by the GPU (from the brief) | 273 GB/s | n/a (calibrated from measurements, §1.4) | [Apple newsroom](https://www.apple.com/newsroom/2024/10/apple-introduces-m4-pro-and-m4-max/) |
| Oracle A1 (Ampere Altra) | Always Free: **2 OCPU / 12 GB** since 2026-06-15 (was 4 / 24) | — | ≈ 22 GB/s effective (4 OCPU, back-solved, §2.6) | ≈ 0.065 effective (§2.6) | [OCI Always Free docs](https://docs.oracle.com/en-us/iaas/Content/FreeTier/resourceref.htm), [InfoQ 2026-07](https://www.infoq.com/news/2026/07/oracle-cloud-free-tier-limits/) |

### 1.3 Model architectures (from each model's `config.json` on Hugging Face)

KV cache per token = 2 (K and V) × layers × KV heads × head_dim × bytes per element (2 for FP16/BF16).

| Model | Layers | Q heads / KV heads | head_dim | Attention notes | KV bytes/token (FP16) | Weights on disk (quant) | Active params per token |
|---|---|---|---|---|---|---|---|
| [Qwen2.5-Coder-7B-Instruct](https://huggingface.co/Qwen/Qwen2.5-Coder-7B-Instruct/blob/main/config.json) | 28 | 28 / 4 | 3584/28 = 128 | full attention (`use_sliding_window: false`) | 2×28×4×128×2 = **57,344 B = 56 KiB** | BF16 15.23 GB; [AWQ](https://huggingface.co/Qwen/Qwen2.5-Coder-7B-Instruct-AWQ) 5.57 GB; GGUF Q4_K_M 4.68 GB | 7.6 B (dense) |
| [Qwen2.5-Coder-14B-Instruct](https://huggingface.co/Qwen/Qwen2.5-Coder-14B-Instruct/blob/main/config.json) | 48 | 40 / 8 | 5120/40 = 128 | full | 2×48×8×128×2 = **196,608 B = 192 KiB** | BF16 29.54 GB; [AWQ](https://huggingface.co/Qwen/Qwen2.5-Coder-14B-Instruct-AWQ) 9.98 GB | 14.8 B (dense) |
| [Qwen3-Coder-30B-A3B-Instruct](https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct/blob/main/config.json) | 48 | 32 / 4 | 128 | full; MoE with 128 experts, 8 active per token | 2×48×4×128×2 = **98,304 B = 96 KiB** | BF16 61.07 GB; [FP8](https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8) 31.18 GB; AWQ (community: [QuantTrio](https://huggingface.co/QuantTrio/Qwen3-Coder-30B-A3B-Instruct-AWQ) 16.81 GB, [cyankiwi](https://huggingface.co/cyankiwi/Qwen3-Coder-30B-A3B-Instruct-AWQ-4bit) 18.09 GB) | ≈ 3.0 B non-embedding (0.9 B attention + 0.3 B lm_head + 8/128 × 29.0 B experts) |
| [gpt-oss-20b](https://huggingface.co/openai/gpt-oss-20b/blob/main/config.json) | 24 | 64 / 8 | 64 | **hybrid**: 12 `sliding_attention` layers (window 128) alternate with 12 `full_attention` layers; MoE with 32 experts, 4 active per token | full layers only: 2×12×8×64×2 = **24,576 B = 24 KiB**. Sliding layers add a **constant ≈ 3.1 MB per sequence** (2×12×8×64×2×128) | MXFP4 13.76 GB ([index `total_size` 13,761,264,768](https://huggingface.co/openai/gpt-oss-20b/blob/main/model.safetensors.index.json)); [GGUF](https://huggingface.co/ggml-org/gpt-oss-20b-GGUF) 12.11 GB | ≈ 3.6 B (0.64 B attention + 0.58 B lm_head + 4/32 × 19.1 B experts) |
| [Qwen2.5-Coder-1.5B-Instruct](https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct/blob/main/config.json) | 28 | 12 / 2 | 128 | full | 28,672 B = 28 KiB | [GGUF](https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF) Q4_0 1.07 GB, Q4_K_M 1.12 GB | 1.5 B |
| [Qwen2.5-Coder-3B-Instruct](https://huggingface.co/Qwen/Qwen2.5-Coder-3B-Instruct/blob/main/config.json) | 36 | 16 / 2 | 128 | full | 36,864 B = 36 KiB | [GGUF](https://huggingface.co/Qwen/Qwen2.5-Coder-3B-Instruct-GGUF) Q4_0 2.00 GB, Q4_K_M 2.10 GB | 3.1 B |

File sizes come from the Hugging Face API (`/api/models/<repo>?blobs=true`). Expert parameter counts come from the configs. For gpt-oss-20b: 24 layers × 32 experts × 3 × 2880² = 19.11 B expert parameters. At MXFP4 (4.25 bits per parameter) that is 10.15 GB, and the other 1.8 B parameters in BF16 add 3.61 GB, which reproduces the 13.76 GB total. For Qwen3-Coder-30B-A3B: 48 × 128 × 3 × 2048 × 768 = 28.99 B expert parameters.

### 1.4 Performance model (how every number in §2 is produced)

This is the standard roofline-style estimate. Decode steps are limited by memory bandwidth, prefill by compute. The simulator in Appendix A implements it.

- **Decode step time** for a batch of *B* sequences:
  `t_step = max(t_mem, t_comp) + 2 ms`
  - `t_mem = (weights read + KV read) / (bandwidth × η)`
    - Weights read are the non-embedding weights. For MoE models, add expert bytes × *f(B)*, where *f(B)* = 1 − (1 − k/E)^B is the expected fraction of experts that at least one token in the step touches.
    - KV read = Σ context × KV bytes/token.
  - `t_comp = FLOPs / (dense TFLOPS × MFU)`, with FLOPs = 2 × active params × tokens + attention FLOPs.
- **Per-user decode rate** = 1 / t_step. **Aggregate** = B / t_step.
- **Prefill** runs as chunked prefill: each engine step takes up to 2,048 tokens, which is the running decodes plus prompt chunks. In steps that contain prefill, compute usually dominates, so everyone's decode pauses briefly. The SAT/LAB/BURST simulations capture this interference.
- **Efficiency constants (ESTIMATES), calibrated on published numbers:**

| Constant | Value | Calibration point → model vs measured |
|---|---|---|
| η, BF16 weights | 0.70 (T4 0.60) | Qwen2.5-7B BF16, A100-80GB, vLLM, batch 1: model 80 vs **84.3 tok/s measured**. 14B BF16: 44 vs **46.3** ([Qwen speed benchmark](https://qwen.readthedocs.io/en/v2.5/benchmark/speed_benchmark.html)) |
| η, INT4 (AWQ/GPTQ, Marlin) | 0.55 (T4 0.50) | 7B AWQ at batch 1 on A100: model 171 vs **148**; 14B AWQ: 108 vs **93** (same source). I kept 0.55 even though Marlin is more efficient at batch ≥ 16, so these numbers lean conservative |
| η, MXFP4 (gpt-oss) | 0.75 (T4 llama.cpp 0.55) | L4, vLLM 0.15.1: model 42–54 vs **~60 tok/s** single stream ([devforth](https://devforth.io/insights/self-hosted-gpt-real-response-time-token-throughput-and-cost-on-l4-l40s-and-h100-for-gpt-oss-20b/)). M4 Pro: model 56 vs **49.5** (Slava's measurement) |
| Prefill MFU | dense 0.40 (T4 0.30); MoE 0.25 | L4 gpt-oss-20b: model 3,547 tok/s vs **≈ 3,800 tok/s** measured (TTFT 3.5 s on a ≈ 13k-token prompt, devforth). T4 llama.cpp 7B pp512 1,310 t/s ⇒ ≈ 27 % MFU ([llama.cpp CUDA scoreboard](https://github.com/ggml-org/llama.cpp/discussions/15013)) |
| vLLM non-KV overhead (activations, CUDA graphs, sampler) | dense 2.0 GiB, MoE 3.0 GiB | Back-solved from vLLM's own log on L4 (gpt-oss-20b, `--gpu-memory-utilization 0.9`, fp8 KV): "Maximum concurrency for 131,072 tokens per request: 2.78x" ⇒ KV pool ≈ 4.2 GiB ⇒ overhead ≈ 3.3 GiB (devforth) |

**Cross-checks:**
- devforth ran 8 parallel users with ≈ 13k-token prompts on L4. The model gives the last TTFT as 8 × 13.3k / 3,550 ≈ 30 s. They measured a maximum TTFT of **26 s**.
- GPUStack measured gpt-oss-20b on A100 with vLLM at a mean TPOT of 48.6 ms under a 1,000-prompt flood, ≈ 20 tok/s per stream ([GPUStack](https://docs.gpustack.ai/2.0/performance-lab/gpt-oss-20b/a100/)). This is consistent with the model once prefill interference at ≈ 256-way concurrency is included.

**Sensitivity:** I re-ran the model with every η and MFU scaled by 0.7 and by 1.3. The results are in §2.5.

---

## 2. Capacity math

### 2.1 Worked example: L4 24 GB × Qwen2.5-Coder-7B-Instruct-AWQ (vLLM)

1. **Weights resident:** 5,570,829,760 B = **5.19 GiB**.
2. **Memory budget for KV:** 22.5 GiB × 0.90 (`--gpu-memory-utilization`) = 20.25 GiB. Subtract 5.19 GiB of weights and 2.0 GiB of overhead: 20.25 − 5.19 − 2.0 = **13.06 GiB**.
3. **KV per token:** 2 × 28 layers × 4 KV heads × 128 × 2 B = **57,344 B (56 KiB)**.
4. **Tokens that fit:** 13.06 × 2³⁰ / 57,344 = **244,576 tokens**.
   - At 8K per sequence: 244,576 / 8,192 = **29.9 sequences**.
   - At the typical 2.5K (1.5K prompt + 1K output): **97.8 sequences**.
   - vLLM allocates KV pages on demand, so the typical figure is the one that matters. 30 users fit with 3× headroom.
5. **Decode step at 30 concurrent streams (context 2,000 each):**
   - Weights read per step = 5.57 GB − 1.09 GB embedding table (a lookup, not a full read) = **4.48 GB**.
   - KV read = 30 × 2,000 × 57,344 B = **3.44 GB**.
   - Total **7.92 GB** at 300 GB/s × 0.55 = 165 GB/s effective, so t_mem = **48.0 ms**.
   - Compute check: 30 × 14.1 GFLOP + attention ≈ 448 GFLOP at 121 × 0.40 = 48.4 TFLOPS gives t_comp = 9.3 ms, so the step is memory-bound.
   - t_step = 48.0 + 2 = **50 ms**, which gives **20.0 tok/s per user and 600 tok/s aggregate** (pure decode).
6. **Prefill of a 1,500-token prompt:**
   - 1,500 × (14.14 GFLOP + 0.30 GFLOP attention) = 21.7 TFLOP at 48.4 TFLOPS effective = **0.45 s**. That is a prefill rate of ≈ 3,350 tok/s.
   - The prompt is processed in one mixed step together with the 29 running decodes, plus on average half of an in-flight step. So **TTFT ≈ 0.5 s** when one prompt arrives while 29 others stream.
   - Side effect: those 29 streams pause for ≈ 0.45 s.
7. **With prefill interference (SAT simulation, 30 users with zero think time):** per-user **14.7 tok/s** at p50 (13.7 at p5), TTFT p95 **0.7 s**, aggregate ≈ 416 tok/s. In SAT, ≈ 30 % of GPU time goes to prefill.
8. **BURST (30 × 1,500 tokens at once):** 45,000 prompt tokens at ≈ 3,300 tok/s = 13.6 s for the last prompt. **TTFT p95 ≈ 13 s**, p50 ≈ 7 s.

### 2.2 Worked example: gpt-oss-20b (sliding-window KV + MoE) on L4 24 GB

1. **Weights:** 13,761,264,768 B = **12.82 GiB**. Experts are MXFP4 (10.15 GB). Attention, router, embedding and lm_head are BF16 (3.61 GB).
2. **KV budget:** 20.25 − 12.82 − 3.0 (MoE overhead) = **4.43 GiB**.
3. **KV per token:** only the 12 full-attention layers grow with context: 2 × 12 × 8 × 64 × 2 = **24,576 B**.
   - The 12 sliding layers keep only the last 128 tokens: 12 × 2 × 8 × 64 × 2 × 128 = **3.1 MB per sequence**, a constant.
   - vLLM's hybrid KV-cache manager allocates them that way. Its "2.78x" log line on L4 (§1.4) is only consistent with this layout.
   - **Tokens:** 4.43 × 2³⁰ / 24,576 = **193,700**. That is **23 sequences at 8K** and **≈ 74 at 2.5K**.
   - This is 2.3× more tokens per GiB than Qwen2.5-7B. Only half of the layers have 8 KV heads × 64 dims of full context.
4. **Decode at 30 streams:**
   - Expert fraction touched = 1 − (1 − 4/32)³⁰ = 1 − 0.875³⁰ = **0.982**. At batch 30 nearly **all** expert weights are read on every step, so the MoE "only 3.6 B active" advantage disappears for bandwidth.
   - Bytes per step = 2.43 GB (attention + lm_head) + 10.15 × 0.982 = 9.97 GB (experts) + 1.47 GB KV + 0.09 GB sliding KV = **13.96 GB**.
   - At 300 × 0.75 = 225 GB/s that is 62 ms, + 2 ms = 64 ms, so **15.6 tok/s per user and 468 aggregate**.
   - At batch 1 the bytes are only 2.43 + 10.15 × 0.125 = **3.70 GB**, so ≈ 54 tok/s. That matches devforth's ~60.
5. **Prefill:** 7.2 GFLOP/token at 121 × 0.25 = 30 TFLOPS gives ≈ 4,000 tok/s, so a 1,500-token prompt takes **≈ 0.4 s**.
6. **SAT:** **12.7 tok/s per user** (p5 12.1), TTFT p95 0.6 s. **BURST** p95 ≈ 11 s.

### 2.3 Why Qwen3-Coder-30B-A3B and Qwen2.5-Coder-14B fail on 24 GB cards

- **Qwen3-Coder-30B-A3B AWQ on L4/A10G:** 20.25 − 15.66 (weights) − 3.0 = **1.6 GiB** of KV. At 96 KiB/token that is **17k tokens**, only **≈ 7 sequences at 2.5K**. The other 23 users queue, which gives SAT TTFT p95 of 70–110 s.
  - FP8 KV plus `--gpu-memory-utilization 0.95` would raise this to ≈ 2.7 GiB and ≈ 59k tokens, about 23 sequences. That is still short of 30, and the 3 GiB overhead figure is itself uncertain.
  - **Not recommended below 40 GB.**
- **Qwen2.5-Coder-14B AWQ on L4/A10G:** KV is **192 KiB/token** (8 KV heads × 48 layers). That leaves 9.0 GiB, about 49k tokens or ≈ 20 sequences at 2.5K.
  - Bytes per step at 30 streams = 8.42 GB of weights + 11.8 GB of KV. KV alone is bigger than the weights, so the rate is ≈ 8 tok/s per user on L4.
  - **Fail on both capacity and speed.**
- **Expert fraction for Qwen3-Coder at batch 30** = 1 − (120/128)³⁰ = **0.856**. With 128 small experts, a 30-token step still touches 86 % of them.

### 2.4 Comparison table (all combos that fit; KV in FP16; vLLM unless noted)

Column definitions:
- **KV pool** = VRAM × 0.90 − weights − overhead.
- **Seqs @ 8K / 2.5K** = KV pool ÷ KV bytes per sequence.
- **1 stream** = batch-1 decode rate.
- **Pure decode @ 30** = 30 streams at context 2K, with no prefill interference.
- **TTFT, 1,500-token prompt** = one new prompt arriving while 29 others stream.
- **SAT / LAB / BURST** = simulated scenarios from §1.1.
- **Verdict:**
  - **PASS** = SAT per-user p50 ≥ 10 tok/s and SAT TTFT p95 ≤ 5 s.
  - **MARGINAL** = fails SAT but passes LAB.
  - **FAIL** = neither.

All numbers are **ESTIMATES** from the model in §1.4 and Appendix A.

| # | GPU | Model · quant | Weights (GiB) | KV/token (KiB) | KV pool (GiB) | Seqs @ 8K | Seqs @ 2.5K | 1 stream (tok/s) | Pure decode @ 30: per user / aggregate (tok/s) | TTFT, 1,500-tok prompt, 29 others streaming (s) | SAT: per-user tok/s p50 (p5) | SAT: TTFT p95 (s) | LAB: TTFT p95 (s) / tok/s p50 | BURST 30×1.5K: TTFT p95 (s) | Verdict |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | T4 16 GB | Qwen2.5-Coder-7B · AWQ-4bit | 5.19 | 56 | 6.3 | 14 | 47 | 33 | 19.4 / 582 | 1.2 | 9.7 (8.7) | 1.6 | 4.1 / 13.9 | 33 | MARGINAL |
| 2 | T4 16 GB | Qwen2.5-Coder-14B · AWQ-4bit | 9.29 | 192 | 2.2 | 1 | 5 | 18 | 7.8 / 234 | 2.3 | 12.4 (10.7) | 302.0 | 266.5 / 12.3 | 198 | FAIL |
| 3 | L4 24 GB | Qwen2.5-Coder-7B · AWQ-4bit | 5.19 | 56 | 13.1 | 30 | 98 | 34 | 20.0 / 600 | 0.5 | 14.7 (13.7) | 0.7 | 1.1 / 20.4 | 13 | **PASS** |
| 4 | L4 24 GB | Qwen2.5-Coder-7B · BF16 | 14.19 | 56 | 4.1 | 9 | 30 | 14 | 11.7 / 350 | 0.5 | 9.8 (9.4) | 0.7 | 1.0 / 11.1 | 13 | MARGINAL |
| 5 | L4 24 GB | Qwen2.5-Coder-14B · AWQ-4bit | 9.29 | 192 | 9.0 | 6 | 20 | 18 | 8.0 / 241 | 1.0 | 7.7 (7.3) | 40.4 | 19.4 / 8.0 | 77 | FAIL |
| 6 | L4 24 GB | gpt-oss-20b · MXFP4 | 12.82 | 24 | 4.4 | 23 | 74 | 54 | 15.6 / 468 | 0.4 | 12.7 (12.1) | 0.6 | 0.9 / 15.2 | 11 | **PASS**¹ |
| 7 | L4 24 GB | Qwen3-Coder-30B-A3B · AWQ-4bit | 15.66 | 96 | 1.6 | 2 | 7 | 64 | 8.2 / 245 | 0.4 | 18.0 (16.8)² | 112.0 | 92.0 / 18.1 | 89 | FAIL |
| 8 | A10G 24 GB | Qwen2.5-Coder-7B · AWQ-4bit | 5.19 | 56 | 13.1 | 30 | 98 | 63 | 38.5 / 1154 | 0.8 | 16.1 (14.0) | 1.2 | 2.4 / 29.3 | 23 | **PASS** |
| 9 | A10G 24 GB | Qwen2.5-Coder-7B · BF16 | 14.19 | 56 | 4.1 | 9 | 30 | 28 | 22.8 / 684 | 0.8 | 12.5 (11.3) | 1.2 | 2.2 / 16.9 | 23 | **PASS** |
| 10 | A10G 24 GB | Qwen2.5-Coder-14B · AWQ-4bit | 9.29 | 192 | 9.0 | 6 | 20 | 35 | 15.8 / 474 | 1.6 | 9.5 (8.5) | 35.6 | 11.7 / 10.6 | 72 | FAIL |
| 11 | A10G 24 GB | gpt-oss-20b · MXFP4 | 12.82 | 24 | 4.4 | 23 | 74 | 97 | 30.3 / 908 | 0.7 | 16.1 (14.3) | 0.9 | 1.9 / 24.7 | 19 | **PASS**¹ ³ |
| 12 | A10G 24 GB | Qwen3-Coder-30B-A3B · AWQ-4bit | 15.66 | 96 | 1.6 | 2 | 7 | 114 | 16.1 / 482 | 0.6 | 30.7 (27.4)² | 73.4 | 44.9 / 30.6 | 58 | FAIL |
| 13 | RTX 4000 Ada 20 GB | Qwen2.5-Coder-7B · AWQ-4bit | 5.19 | 56 | 10.8 | 25 | 81 | 40 | 23.8 / 714 | 0.7 | 14.0 (12.6) | 1.0 | 2.1 / 20.6 | 20 | **PASS** |
| 14 | RTX 4000 Ada 20 GB | Qwen2.5-Coder-14B · AWQ-4bit | 9.29 | 192 | 6.7 | 4 | 15 | 22 | 9.6 / 288 | 1.4 | 9.3 (8.4) | 66.9 | 40.9 / 9.4 | 76 | FAIL |
| 15 | RTX 4000 Ada 20 GB | gpt-oss-20b · MXFP4 | 12.82 | 24 | 2.2 | 11 | 36 | 63 | 18.6 / 558 | 0.6 | 12.9 (12.0) | 0.8 | 1.3 / 16.6 | 16 | **PASS**¹ ⁴ |
| 16 | RTX 4000 Ada 20 GB | Qwen3-Coder-30B-A3B · AWQ-4bit | 15.66 | 96 | <0 | 0 | 0 | — | — | — | — | — | — | — | does not fit |
| 17 | A100 40 GB | Qwen2.5-Coder-7B · BF16 | 14.19 | 56 | 19.4 | 44 | 145 | 66 | 55.1 / 1653 | 0.2 | 39.1 (35.9) | 0.3 | 0.3 / 56.0 | 5 | **PASS** |
| 18 | A100 40 GB | Qwen2.5-Coder-14B · AWQ-4bit | 9.29 | 192 | 24.3 | 16 | 53 | 81 | 39.0 / 1170 | 0.4 | 24.8 (22.4) | 0.5 | 0.8 / 48.5 | 10 | **PASS** |
| 19 | A100 40 GB | Qwen2.5-Coder-14B · BF16 | 27.51 | 192 | 6.0 | 4 | 13 | 36 | 25.9 / 778 | 0.4 | 25.2 (23.4)² | 32.9 | 6.4 / 25.9 | 26 | FAIL |
| 20 | A100 40 GB | gpt-oss-20b · MXFP4 | 12.82 | 24 | 19.7 | 104 | 328 | 192 | 71.6 / 2147 | 0.2 | 49.5 (45.6) | 0.2 | 0.3 / 100.7 | 4 | **PASS** |
| 21 | A100 40 GB | Qwen3-Coder-30B-A3B · AWQ-4bit | 15.66 | 96 | 16.9 | 23 | 74 | 217 | 39.6 / 1188 | 0.1 | 33.4 (31.8) | 0.2 | 0.2 / 83.0 | 4 | **PASS** |
| 22 | A100 40 GB | Qwen3-Coder-30B-A3B · FP8 | 29.03 | 96 | 3.5 | 5 | 15 | 190 | 31.5 / 946 | 0.1 | 37.1 (35.4)² | 16.8 | 0.2 / 58.3 | 16 | MARGINAL |
| 23 | A100 80 GB | Qwen2.5-Coder-14B · BF16 | 27.51 | 192 | 41.8 | 28 | 91 | 44 | 31.9 / 956 | 0.4 | 21.7 (19.9) | 0.5 | 0.8 / 31.2 | 10 | **PASS** |
| 24 | A100 80 GB | gpt-oss-20b · MXFP4 | 12.82 | 24 | 55.5 | 291 | 922 | 218 | 86.0 / 2581 | 0.2 | 55.5 (50.7) | 0.2 | 0.3 / 122.6 | 4 | **PASS** |
| 25 | A100 80 GB | Qwen3-Coder-30B-A3B · FP8 | 29.03 | 96 | 39.2 | 52 | 171 | 217 | 38.7 / 1160 | 0.1 | 32.6 (31.0) | 0.2 | 0.3 / 79.7 | 4 | **PASS**⁵ |
| 26 | A100 80 GB | Qwen3-Coder-30B-A3B · BF16 | 56.87 | 96 | 11.4 | 15 | 50 | 151 | 22.3 / 670 | 0.2 | 20.5 (20.0) | 0.2 | 0.3 / 33.4 | 4 | **PASS** |
| 27 | T4 16 GB | gpt-oss-20b · MXFP4 GGUF (llama-server) | 11.28 | 24 | 2.1 | 11 | 34 | 48 | 12.7 / 381 | 1.0 | 8.4 (7.8) | 1.4 | 2.5 / 10.0 | 27 | FAIL / edge⁶ |

Footnotes:

1. **gpt-oss-20b is a reasoning model.** Its tok/s includes reasoning tokens, which vLLM streams in `delta.reasoning`. Even at `reasoning_effort: "low"`, the first visible *content* token arrives after the reasoning. That adds ≈ 1 s per 13 reasoning tokens on L4 at 30 users. The reasoning length at low effort is **UNVERIFIED**; the load test measures it as TTFC (§6).
2. The per-user rate looks high here because KV capacity lets only a few streams run. Everyone else waits in the queue, which is why TTFT explodes.
3. vLLM runs gpt-oss on Ampere (A100 is documented, with the `TRITON_ATTN` backend and Marlin MXFP4 MoE kernels). A10G is Ampere sm86 but is not named in the recipe, so it is **UNVERIFIED**. On Ada, devforth ran it on L4 with vLLM 0.15.1.
4. The KV pool is only 2.2 GiB, about 36 sequences at 2.5K. Use `--kv-cache-dtype fp8` here (Ada supports FP8), which doubles it to ≈ 72.
5. FP8 checkpoints on Ampere run as weight-only FP8 through the Marlin kernels (the [vLLM quantization hardware table](https://github.com/vllm-project/vllm/blob/main/docs/features/quantization/README.md) lists Marlin FP8 on Ampere). FP8 MoE on A100 is **UNVERIFIED**. BF16 (row 26) is the safe fallback on 80 GB.
6. vLLM does not support gpt-oss on T4 ("Turing does not support Marlin MXFP4", same vLLM table). The llama-server estimate assumes vLLM-like batching efficiency, which is **UNVERIFIED** and optimistic.

### 2.5 Verdict

| GPU you get | Recommended model for performance | Expected per-user rate at 30 (SAT, p50) | Burst TTFT p95 | Verdict |
|---|---|---|---|---|
| **A100 80 GB** | Qwen3-Coder-30B-A3B (BF16, or FP8 if it loads) | 20–33 tok/s | ≈ 4 s | **PASS**, comfortable. Could raise MAX_INFLIGHT to 48 |
| **A100 40 GB** | Qwen3-Coder-30B-A3B AWQ (community quant, quality **UNVERIFIED**) or gpt-oss-20b | 33 / 50 tok/s | ≈ 4 s | **PASS**, comfortable |
| **A10G 24 GB** | Qwen2.5-Coder-7B-AWQ or gpt-oss-20b | ≈ 16 tok/s | ≈ 19–23 s | **PASS**, thin margin |
| **L4 24 GB** | Qwen2.5-Coder-7B-AWQ or gpt-oss-20b | ≈ 13–15 tok/s | ≈ 11–13 s | **PASS**, thin margin |
| **RTX 4000 Ada 20 GB** | Qwen2.5-Coder-7B-AWQ, or gpt-oss-20b with fp8 KV | ≈ 13–14 tok/s | ≈ 16–20 s | **PASS**, thin margin |
| **T4 16 GB** | Qwen2.5-Coder-7B-AWQ (`--dtype half`) | ≈ 10 tok/s (LAB 14) | ≈ 33 s | **MARGINAL**. Cut `max_tokens` defaults and set MAX_INFLIGHT to 24: streams run at ≈ 11 tok/s, the other 6 users wait (SAT TTFT p95 ≈ 19 s; LAB ≈ 4.5 s) |
| **CPU only** | Qwen2.5-Coder-1.5B Q4_0 | 1–2 users total | — | **FAIL** for the lab. Use as a last-resort queue-backed fallback only; laptop mode is better (§4) |

**Model choice is the model researcher's call.** Performance only rules out Qwen2.5-14B and Qwen3-Coder-30B on 24 GB-or-less cards.

**Sensitivity:** with every η and MFU scaled by **0.7**, the 24 GB-class passing combos drop to SAT **9.0–11.3 tok/s**. That is borderline, although they still pass LAB at 10–18 tok/s. With the constants scaled by 1.3 they rise to 16–21 tok/s. A100 combos stay above 20 tok/s either way. **This is why the rehearsal (§7) is a hard gate.**

### 2.6 CPU-only case (Oracle Ampere A1)

**Always Free was halved:** Oracle's docs now say 1,500 OCPU-hours and 9,000 GB-hours per month, "equivalent to 2 OCPUs and 12 GB of memory" ([OCI](https://docs.oracle.com/en-us/iaas/Content/FreeTier/resourceref.htm)). InfoQ reports the change took effect on **2026-06-15**; the previous allowance was 4 OCPU / 24 GB ([InfoQ](https://www.infoq.com/news/2026/07/oracle-cloud-free-tier-limits/)). A pay-as-you-go tenancy running 4 OCPUs for a few hours might stay inside the hour-based allowance. That is **UNVERIFIED** and belongs to the cloud research.

The only published measurements on this exact shape (4 OCPU / 24 GB, llama.cpp, Qwen2.5-**3B** Q4_K_M) are from [tiffena.me, Nov 2025](https://tiffena.me/blog/ai-infrastructure/benchmark-cpu-only-llm-inference-oracle-ampere-a1-llama.cpp-ollama-docker/) and [part 2](https://tiffena.me/blog/ai-infrastructure/benchmark-cpu-only-llm-inference-llama.cpp-server-flags/):
- Single-stream decode is **10.57 tok/s**.
- Aggregate throughput **stays at ≈ 10–11 tok/s** at 2, 4, 8 and 12 concurrent requests. That means the CPU is compute-bound, and batching does not add throughput.
- A cold 2,571-token prompt took **173 s** with default flags, ≈ 15 tok/s prefill. Their "186 ms" figure with tuned flags is most likely a prompt-cache hit, not a real prefill.

Back-solved constants (**ESTIMATE**): ≈ 22 GB/s effective bandwidth and ≈ 65 GFLOPS effective compute on 4 OCPU; half of that on 2 OCPU.

| Shape | Model (GGUF) | Single stream tok/s | Aggregate ceiling tok/s | Prefill tok/s | TTFT for 300 / 1,500-token prompt | Users at ≥ 10 tok/s |
|---|---|---|---|---|---|---|
| 4 OCPU / 24 GB | Qwen2.5-Coder-1.5B | ≈ 19 | ≈ 20 | ≈ 20 (pessimistic) to ≈ 80 (Q4_0 ARM repack; **UNVERIFIED**) | 4–14 s / 20–70 s | 1–2 |
| 4 OCPU / 24 GB | Qwen2.5-Coder-3B | ≈ 10 (measured 10.6) | ≈ 10 (measured) | ≈ 10–40 | 8–30 s / 40–150 s | 1 |
| 2 OCPU / 12 GB (current free) | Qwen2.5-Coder-1.5B | ≈ 10 | ≈ 10 | ≈ 10–40 | 8–30 s / 40–150 s | 1 |

With 30 users, each would get **≈ 0.3–0.7 tok/s**. The CPU is not a lab platform. It only keeps the service "answering" in a disaster, with short prompts, small `max_tokens` and a queue.

---

## 3. Engine recommendation

### 3.1 GPU: vLLM

**Why vLLM:**
- **Continuous batching with paged KV.** Memory is used per token actually generated, not per reserved slot. This is what makes "30 × 8K" possible in 13 GiB, since the typical request uses only 2.5K.
- **Chunked prefill.** Prompts do not block other streams.
- **Automatic prefix caching.** It is on by default ([`cache.py`](https://github.com/vllm-project/vllm/blob/main/vllm/config/cache.py): `enable_prefix_caching: bool = True`). The shared system prompt of each task endpoint, and identical exercise code, is prefilled once.
- A Prometheus `/metrics` endpoint, a built-in `vllm bench serve` tool to cross-check our load tester, documented gpt-oss support, and T4 support ("compute capability 7.5 or higher (e.g., T4 …)", per the [vLLM GPU install docs](https://docs.vllm.ai/en/latest/getting_started/installation/gpu.html)).

**Version and defaults:**
- Pin **vLLM v0.30.0**, released 2026-09-22. Docker tag `vllm/vllm-openai:v0.30.0` (amd64 and arm64) is on Docker Hub.
- Current defaults on these GPUs, read from the vLLM `main` branch on 2026-09-30 (v0.30.0 is assumed to match; **UNVERIFIED**), from [`arg_utils.py`](https://github.com/vllm-project/vllm/blob/main/vllm/engine/arg_utils.py): `max_num_batched_tokens = 2048` and `max_num_seqs = 256` for the API server on anything other than an H100-class card; `gpu_memory_utilization = 0.92`.
- I set all of these explicitly so that the gateway limits match the engine.

**SGLang (v0.5.20) is a valid alternative.** Its RadixAttention prefix caching is strong. However, GPUStack measured SGLang 0.5.4 far below vLLM for gpt-oss-20b on A100 (559 vs 9,743 tok/s), which may be configuration-specific. We have more documentation and a recipe for vLLM. Keep SGLang as plan B only.

**Common flags (every GPU config):**

| Flag | Value | Why |
|---|---|---|
| `--max-model-len` | `8192` (`16384` on A100) | Largest input ≈ 6K tokens (≈ 24 KB of code) + 2K output (§5). A smaller value limits the worst case and protects KV |
| `--max-num-seqs` | `32` (`64` on A100) | ≥ 30 students + 2 team members. **Gateway MAX_INFLIGHT must be ≤ this**, so vLLM never queues internally and the gateway owns the queue |
| `--max-num-batched-tokens` | `2048` | Chunk size. A 1.5K prompt fits in one step. Larger chunks lengthen the decode stalls everyone sees (≈ 0.45 s per 2K chunk on L4) without speeding compute-bound prefill much |
| `--gpu-memory-utilization` | `0.90` | Leaves ≈ 2 GiB headroom against OOM. The KV math above assumes this value |
| `--enable-prefix-caching` | on | Already the default; written explicitly |
| `--kv-cache-dtype` | `auto` (FP16). Use `fp8` only on Ada (L4, RTX 4000 Ada) if the rehearsal shows KV pressure | FP8 KV gives +5–15 % decode speed in the model and 2× capacity. It needs no calibration, but its quality effect is **UNVERIFIED** for these models |
| `--served-model-name` | short, stable name | The gateway's `MODEL` config and `/v1/models` use it |
| `--api-key` | random secret, shared with the gateway only | The engine must not be reachable without it. Also bind to `127.0.0.1` or the private network (Jan owns exposure) |
| request logging | leave `--enable-log-requests` **off** (default `False`) | PRD: no content logging |

**Config G-24 (L4 / A10G / RTX 4000 Ada), Qwen2.5-Coder-7B-Instruct-AWQ:**

```bash
docker run -d --name vllm --gpus all --ipc=host \
  -p 127.0.0.1:8000:8000 \
  -v "$HOME/.cache/huggingface:/root/.cache/huggingface" \
  vllm/vllm-openai:v0.30.0 \
  Qwen/Qwen2.5-Coder-7B-Instruct-AWQ \
  --served-model-name qwen2.5-coder-7b \
  --max-model-len 8192 \
  --max-num-seqs 32 \
  --max-num-batched-tokens 2048 \
  --gpu-memory-utilization 0.90 \
  --enable-prefix-caching \
  --api-key "$VLLM_API_KEY"
# T4: add  --dtype half   (Turing has no BF16) and use --max-num-seqs 24
```

vLLM detects AWQ from the checkpoint and picks the Marlin kernel. Check the startup log for `awq_marlin`.

**Config G-24, gpt-oss-20b:**

```bash
docker run -d --name vllm --gpus all --ipc=host -p 127.0.0.1:8000:8000 \
  -v "$HOME/.cache/huggingface:/root/.cache/huggingface" \
  vllm/vllm-openai:v0.30.0 \
  openai/gpt-oss-20b \
  --served-model-name gpt-oss-20b \
  --max-model-len 8192 --max-num-seqs 32 --max-num-batched-tokens 2048 \
  --gpu-memory-utilization 0.90 --enable-prefix-caching \
  --api-key "$VLLM_API_KEY"
# RTX 4000 Ada: add --kv-cache-dtype fp8 (KV pool only 2.2 GiB otherwise)
# A/B in rehearsal: --async-scheduling (GPUStack: +12% throughput on A100)
```

The gateway should send `reasoning_effort: "low"` by default for gpt-oss. Reasoning text streams in `delta.reasoning`, which vLLM renamed from `reasoning_content` ([vLLM reasoning docs](https://github.com/vllm-project/vllm/blob/main/docs/features/reasoning_outputs.md)).

**Config G-40 (A100 40 GB), Qwen3-Coder-30B-A3B AWQ:**

```bash
... vllm/vllm-openai:v0.30.0 \
  cyankiwi/Qwen3-Coder-30B-A3B-Instruct-AWQ-4bit \
  --served-model-name qwen3-coder-30b \
  --max-model-len 16384 --max-num-seqs 64 --max-num-batched-tokens 2048 \
  --gpu-memory-utilization 0.90 --enable-prefix-caching --api-key "$VLLM_API_KEY"
```

This is a community quant, so its quality is **UNVERIFIED**. The alternative `QuantTrio/Qwen3-Coder-30B-A3B-Instruct-AWQ` is 16.8 GB.

**Config G-80 (A100 80 GB), Qwen3-Coder-30B-A3B:** the same command with `Qwen/Qwen3-Coder-30B-A3B-Instruct` (BF16, the safe option) or `Qwen/Qwen3-Coder-30B-A3B-Instruct-FP8` (more KV headroom; FP8 MoE on Ampere is **UNVERIFIED**).

### 3.2 CPU: llama.cpp `llama-server`

Flags come from the current [llama-server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md):
- `-np, --parallel N` sets the number of slots.
- `-cb` (continuous batching) is on by default.
- `-kvu, --kv-unified` shares one KV pool across slots. Without it, each slot gets `-c / -np`.
- `--cache-reuse N` reuses a cached prefix via KV shifting.
- `-t` and `-tb` set threads for decode and for batch/prompt processing.
- `--metrics` exposes Prometheus metrics.

```bash
# 4 OCPU (if available)
llama-server -m qwen2.5-coder-1.5b-instruct-q4_0.gguf \
  --host 127.0.0.1 --port 8081 --api-key "$UPSTREAM_KEY" \
  -t 4 -tb 4 \
  -c 16384 -np 2 -kvu \
  -b 512 -ub 512 \
  --cache-reuse 256 \
  -n 512 \
  --jinja --metrics
# 2 OCPU / 12 GB (current Always Free): -t 2 -tb 2 -np 1 -c 8192
```

- **Q4_0, not Q4_K_M, on ARM.** llama.cpp repacks Q4_0 at runtime for ARM dot-product kernels, which speeds up prompt processing. This is **UNVERIFIED** on A1, so run `llama-bench -m <q4_0> -m <q4_k_m> -p 1500 -n 128 -t 4` first.
- `-np 2`: the aggregate does not grow with more slots on this CPU (measured, §2.6). More slots only split the same ≈ 10–20 tok/s among more people.

### 3.3 Is Ollama adequate for 30 concurrent users? No. Laptop mode only.

- **It serializes by default.** `OLLAMA_NUM_PARALLEL` defaults to **1**.
- **Memory is reserved per slot.** The docs say "Required RAM will scale by `OLLAMA_NUM_PARALLEL` * `OLLAMA_CONTEXT_LENGTH`" and "a 2K context with 4 parallel requests will result in an 8K context" ([Ollama FAQ](https://docs.ollama.com/faq)). 30 slots × 8K = 245K tokens reserved whether used or not; for a 7B model that is 13 GiB. vLLM's paged KV reserves nothing up front.
- **The default context is only 4K on GPUs with less than 24 GiB** ([Ollama context length](https://docs.ollama.com/context-length)). It must be raised explicitly.
- **Queue behaviour:** the default queue is `OLLAMA_MAX_QUEUE=512`, and when it is full Ollama returns **503** (FAQ). The gateway should map that the same way as its own queue-full.
- **Throughput:** Red Hat compared the two on an A100-40GB with Llama-3.1-8B. vLLM reached **793 tok/s** peak against Ollama's **41 tok/s** at default settings. P99 TTFT was 80 ms vs 673 ms. Even with `NUM_PARALLEL=32`, "tuned Ollama could not match vLLM's throughput scaling or maintain stable latency" ([Red Hat, 2025-08](https://developers.redhat.com/articles/2025/08/08/ollama-vs-vllm-deep-dive-performance-benchmarking)).
- **Mac caveat:** Ollama's experimental **MLX runner** ignores `OLLAMA_NUM_PARALLEL` and "handles one request at a time" ([ollama#17666](https://github.com/ollama/ollama/issues/17666)). GGUF models on the llama.cpp path do batch. Whether Slava's gpt-oss:20b uses the batching path is **UNVERIFIED**; test it in §4.

---

## 4. Laptop mode capacity (MacBook Pro M4 Pro 24 GB, Ollama)

Model calibration:
- **Slava's measurements:** gpt-oss:20b decodes at 49.5 tok/s and processes prompts at ≈ 334 tok/s, using 12 GB resident at 32K context.
- **llama.cpp scoreboard for M4 Pro:** LLaMA-2-7B Q4_0 gives pp512 **364 t/s** and tg128 **49.6 t/s** on the 16-core GPU, and 440 / 50.7 on the 20-core GPU ([llama.cpp #4167](https://github.com/ggml-org/llama.cpp/discussions/4167)). Which GPU variant Slava's MBP has is **UNVERIFIED**; 24 GB configurations usually have 16 cores.
- **The bottleneck is prompt processing.** A 1,500-token prompt alone takes **≈ 4.5 s**.

Simulated LAB traffic (think time 30 s), gpt-oss:20b, Ollama-like batching (512-token prompt chunks). All numbers are **ESTIMATES**:

| Users | `OLLAMA_NUM_PARALLEL` | Prompts 800–2,200 tok: TTFT p50 / p95 (s) | Per-user tok/s p50 | Prompts 300–800 tok: TTFT p95 (s) / tok/s |
|---|---|---|---|---|
| 1 | any | 4.9 / 6.6 | 55 | — |
| 3 | 4 | 5.5 / 7.9 | 37 | — |
| 5 | 4 | 5.4 / 13.2 | 24 | 3.3 / 37 |
| 8 | 4 | 16.9 / 36.9 | 19 | 7.4 / 27 |
| 8 | 8 | 6.1 / 13.4 | 16.5 | 3.8 / 26 |
| 10 | 8 | 7.4 / 19.1 | 12.8 | 4.3 / 21 |
| 15 | 8 | 28.3 / 47.4 | 10.3 | 13.6 / 15 |

**Answer:** laptop mode handles about **5 concurrent users** comfortably with exercise-sized code, or **8–10** if prompts stay short (< 800 tokens). **30 is out of reach.** The offline fallback covers the live demo plus a small group, or the class in shifts. Qwen2.5-Coder-7B (Q4_K_M) is no faster here (prefill ≈ 275 tok/s estimated), so keep gpt-oss:20b.

Note that `qwen2.5-coder:1.5b-base` on the Mac is a **base (FIM) model, not instruct**, so it is unsuitable for chat or task endpoints. If a tiny fallback is wanted, pull `qwen2.5-coder:1.5b` (the instruct variant).

**Recommended laptop settings:**
- `OLLAMA_NUM_PARALLEL=4`, `OLLAMA_CONTEXT_LENGTH=8192`. That is 32K tokens in total, the configuration Slava measured at 12 GB.
- `OLLAMA_NUM_PARALLEL=8` only if `ollama ps` still shows 100 % GPU and macOS memory pressure stays green. The estimate is +0.75–1.5 GiB of KV, **UNVERIFIED**.
- `OLLAMA_MAX_QUEUE=16` (the gateway queues anyway).
- Run the Go gateway natively, or cap the Docker VM's memory, so the model keeps its ≈ 16 GiB of GPU-wired memory.

**Do this now on the Mac (10 minutes, no GPU needed):** after `cmd/loadtest` exists, run `step` at 1, 2, 4, 8 against Ollama.
- If per-stream tok/s at 4 concurrent is about the same as at 1 **and** TTFT grows linearly, requests are being serialized (the MLX-runner problem): laptop capacity is 2–3 users.
- A possible 2× prefill gain is to use `llama-server` with `-ub 2048 -b 2048` instead of Ollama. The llama.cpp guide shows M4 Max pp2048 1,277 t/s vs M1 Pro 516 t/s for gpt-oss-20b ([llama.cpp #15396](https://github.com/ggml-org/llama.cpp/discussions/15396)), so an M4 Pro plausibly reaches about 600 t/s against Ollama's measured 334. **UNVERIFIED**.

---

## 5. Gateway knob sizing

The rule of thumb:
- **MAX_INFLIGHT** = the number of sequences the engine runs concurrently while still meeting the per-user rate.
- **QUEUE_SIZE** ≈ drain rate × QUEUE_TIMEOUT, where drain rate = aggregate tok/s ÷ mean output tokens.
- **Per-key limits** exist to stop one key from taking a disproportionate share. They are not what protects the GPU; the global limits are.

| Knob | 24 GB class (L4 / A10G / RTX 4000 Ada) | A100 class | T4 | Laptop (M4 Pro, Ollama) | CPU (A1) | Derivation |
|---|---|---|---|---|---|---|
| `MAX_INFLIGHT` | **32** | 48 | 24 | 4 | 2 (4 OCPU) / 1 (2 OCPU) | Equals `--max-num-seqs` 32 (vLLM), `OLLAMA_NUM_PARALLEL`, or `-np`. At 30–32 streams the 24 GB class gives 13–16 tok/s per user (SAT). KV fits ≥ 74 sequences at 2.5K for the recommended models (RTX 4000 Ada + gpt-oss needs fp8 KV for that). On T4, 24 streams run at ≈ 11.4 tok/s instead of ≈ 9.7 at 30, but the other 6 users queue (SAT TTFT p95 ≈ 19 s) |
| `QUEUE_SIZE` | **24** | 48 | 16 | 8 | 4 | SAT aggregate ≈ 360–450 tok/s ÷ 600 tokens per request ≈ 0.6–0.75 requests/s drained, × 30 s ≈ 18–23 → 24. A100: ≈ 1–2.7 requests/s → 48. Laptop: ≈ 55 tok/s ÷ 600 ≈ 0.09/s × 60 s ≈ 6 → 8 |
| `QUEUE_TIMEOUT` | **30 s** | 30 s | 30 s | 60 s | 120 s | How long a person tolerates "waiting to start". After that, return `503` + `Retry-After`. Slower backends get longer timeouts because the alternative (retry later) is not faster |
| `Retry-After` (503) | `ceil(queue_len / drain_rate)`, clamped to 5–60 s | same | same | same, clamped to 10–120 s | same | With drain ≈ 0.7/s and a full queue of 24: ≈ 35 s |
| `RATE_RPM` per key | **6** (token bucket, burst 3) | 10 | 4 | 4 | 2 | Fair share at saturation = 0.7 requests/s × 60 ÷ 30 users ≈ **1.4 requests/min per user**. 6 allows ≈ 4× fair share for retries and quick follow-ups, while one key can use at most ≈ 14 % of capacity. Team/demo keys: 60 |
| `TOKEN_QUOTA` per key (prompt + completion, per day) | **300,000** | 500,000 | 200,000 | 100,000 | 30,000 | Expected lab use: ≈ 60 requests × (1.5K + 0.6K) ≈ 126K, so 300K is ≈ 2.4× headroom. A runaway script at 6 RPM × 2.1K tokens = 12.6K tokens/min hits the quota in ≈ 24 min. Team/demo keys: 2,000,000 |
| **Suggested extra:** per-key in-flight cap | 2 | 2 | 2 | 1 | 1 | Without it, one student with a parallel script can hold many of the 32 slots. RATE_RPM alone does not prevent that, because streams last 30–60 s |
| **Suggested extra:** `MAX_INPUT_TOKENS` | 6,000 (≈ 24 KB of code) | 12,000 | 4,000 | 4,000 | 1,000 | max-model-len 8,192 − max output 2,048 − ≈ 150 tokens of template. On CPU, TTFT is ≈ 1 s per 10–40 prompt tokens. Reject larger inputs with `413` / `400` |
| **Suggested extra:** upstream timeouts | first token 60 s; idle between tokens 30 s; total 10 min | same | same | first token 180 s | first token 300 s | Worst legitimate case on GPU is a burst of ≈ 23 s TTFT and 2,048 tokens at ≈ 8 tok/s ≈ 4.5 min |

**`max_tokens` per endpoint.** The default is used when the client omits `max_tokens`. Larger requested values are clamped to the maximum.

| Endpoint | GPU default / max | Laptop & T4 default / max | CPU default / max | Reasoning |
|---|---|---|---|---|
| `explain` | 512 / 1024 | 384 / 768 | 256 / 384 | A prose explanation of a snippet. 512 tokens ≈ 35 s at 15 tok/s |
| `review` | 768 / 1536 | 512 / 1024 | 256 / 512 | A list of bugs, smells and security issues with short fixes |
| `tests` | 1024 / 2048 | 768 / 1536 | 384 / 512 | Test files are long, and Java/C++ boilerplate costs tokens |
| `fix` | 1024 / 2048 | 768 / 1536 | 384 / 512 | The whole fixed function plus an explanation |
| `chat` (`/v1/chat/completions`) | 512 / 2048 | 384 / 1536 | 256 / 512 | IDE tools usually send their own `max_tokens`; this cap bounds the worst case |
| gpt-oss only | add **+256** to each default, and set `reasoning_effort: "low"` | same | n/a | Reasoning tokens count toward `max_tokens` |

**Why the defaults matter.** Each stream holds a slot for roughly `max_tokens` ÷ per-user rate. At 15 tok/s:
- 512 tokens ≈ 34 s, 1,024 ≈ 68 s, 2,048 ≈ 2.3 min.
- Halving the mean output roughly doubles the drain rate. It is the cheapest lever if the rehearsal comes in low.

---

## 6. Load-test tool spec: `cmd/loadtest`

### 6.1 Purpose

- Drive N concurrent **streaming** clients against the gateway (or directly against the engine, for comparison).
- Measure user-visible latency and throughput.
- Emit a console table plus CSV/JSON for Giulia's slides.
- It must be a single Go binary using only the stdlib (`net/http`, `encoding/json`, `embed`, `flag`), in module `local-generative-ai`, Go 1.26.

### 6.2 CLI flags

| Flag | Type / default | Meaning |
|---|---|---|
| `-base-url` | string, `http://localhost:8080` | Gateway root. Endpoints are appended as `/v1/chat/completions` or the task path |
| `-task-path` | string, `/v1/tasks/{task}` | Template for task endpoints; `{task}` becomes explain, review, tests or fix. **Must match `api/openapi.yaml`**; the architect owns the final paths |
| `-api-key` | string, env `LOADTEST_API_KEY` | A single key that all virtual users share (warns that per-key RATE_RPM will cause 429s) |
| `-keys-file` | path | One key per line. Virtual user *i* uses key `i mod len`. **Use this for realistic runs**: 30 users = 30 keys |
| `-model` | string, from `GET /v1/models` if empty | Model name for chat requests |
| `-scenario` | `smoke\|step\|lab\|burst\|limits`, default `step` | See §6.3 |
| `-levels` | list, `1,10,30` | Concurrency levels for `step` |
| `-users` | int, `30` | Number of virtual users for `lab`, `burst` and `limits` |
| `-duration` | duration, `3m` | Per level (`step`) or total (`lab`) |
| `-ramp` | duration, `60s` | Users start uniformly spread over this window (`lab`); for `step`, the ramp between levels |
| `-think` | `exp:30s` \| `const:0s` \| `uniform:10s-60s`, default `const:0s` for step and `exp:30s` for lab | Pause between one user's requests |
| `-warmup` | int, `3` | Requests per level excluded from statistics (they still run) |
| `-mix` | `explain=0.25,review=0.2,tests=0.2,fix=0.2,chat=0.15` | Endpoint weights |
| `-langs` | `python,java,go,c,cpp` | Chosen uniformly per request |
| `-sizes` | `small=0.3,medium=0.5,large=0.2` | Prompt-size class weights (§6.4). Use `large=1` to force ≈ 1,500-token prompts |
| `-max-tokens` | int, 0 = the endpoint default | Override `max_tokens` |
| `-reasoning-effort` | string, empty | Passed through for gpt-oss (`low`) |
| `-temperature` | float, `0.2` | For comparable runs |
| `-seed` | int, `42` | Makes prompt selection and think times deterministic |
| `-cache-bust` | `top\|none`, default `top` | `top` puts a unique comment (`# run <uuid> req <n>`) as the **first** code line, so prefix caching cannot reuse the code (conservative). `none` shows the best case when everyone sends identical exercise code |
| `-timeout` | duration, `10m` | Per-request timeout |
| `-slo-ttft` | duration, `5s` | p95 TTFT target used for pass/fail |
| `-slo-tps` | float, `10` | p50 per-stream decode tok/s target |
| `-slo-level` | int, `30` | The level the SLO is judged at |
| `-out` | dir, `results/<UTC timestamp>-<tag>` | Where CSV/JSON files are written |
| `-tag` | string | Label, e.g. `L4-qwen7b-awq-gw`, copied into every file |
| `-metrics-url` | URL, empty | Optional engine `/metrics` (vLLM Prometheus), scraped every 5 s into `engine_metrics.csv` |
| `-dry-run` | bool | Print 5 generated requests and exit |
| `-v` | bool | Log each request's result line to stderr |

Exit codes: `0` = SLO met at `-slo-level`; `1` = SLO missed; `2` = error rate above 1 % or tool failure.

### 6.3 Scenarios

| Scenario | Definition | Use |
|---|---|---|
| `smoke` | 1 user, sequentially: every task × every language (4 × 5 = 20) + 2 chat, non-streaming **and** streaming for chat | Functional sanity. Also checks answers are non-empty, `tests`/`fix` contain a fenced code block, and `finish_reason` is present |
| `step` | For each level L in `-levels`: start L closed-loop users (ramped over `-ramp`), run `-duration`, stop, drain, next level. Default think time 0 = saturation | Capacity curve; the headline slide numbers. Include one level **above** capacity (e.g. 40) to exercise the queue and 503s |
| `lab` | `-users` users arrive uniformly over `-ramp` (default 60 s), think time `exp:30s`, mixed endpoints and sizes, for `-duration` (default 15 m) | Realistic lab session; TTFT p95 go/no-go |
| `burst` | `-users` users send one `large` request each at the same instant (barrier), wait for all to finish, pause 60 s, repeat 3 times | The "everyone click now" moment |
| `limits` | (a) One key sends at 2× `RATE_RPM` for 90 s: expect `429` with `Retry-After`. (b) Open `MAX_INFLIGHT + QUEUE_SIZE + 10` concurrent streams: expect exactly ≈ 10 `503`s with `Retry-After`; queued ones must start within `QUEUE_TIMEOUT` or get `503`. (c) An invalid key: `401` | Verify FR4 and PRD acceptance criteria |

### 6.4 Workload corpus

- Embed it with `//go:embed corpus`. Layout: `cmd/loadtest/corpus/<lang>/<size>/<name>.<ext>` with `lang` ∈ {python, java, go, c, cpp}, `size` ∈ {small (≈ 150–400 tokens), medium (≈ 600–1,200), large (≈ 1,500–2,200)}.
- Provide at least 3 snippets per (lang, size). They should be realistic exercise-style code (algorithms, data structures, file I/O, a class with a bug). Szymon's real exercises are preferred once available.
- For `fix`, each snippet has a sibling `<name>.error.txt` with a realistic compiler or runtime error.
- **Token sizes:** at build time, a `go test` estimates tokens as `len(bytes)/3.5` and asserts each file falls inside its class range. The real count comes back from `usage.prompt_tokens` and is recorded per request.
- **Request bodies:**
  - Chat: `{"model":…, "messages":[{"role":"system","content":"You are a coding assistant."},{"role":"user","content":"<instruction per task>\n```<lang>\n<code>\n```"}], "stream":true, "stream_options":{"include_usage":true}, "max_tokens":…, "temperature":…}`.
  - Task endpoints: body per `api/openapi.yaml`. Until that exists, assume `{"language":"<lang>","code":"<code>","error":"<text, fix only>","stream":true,"max_tokens":…}`.

### 6.5 Measuring correctly from SSE

Per request, using `time.Now()` (monotonic):

1. `t0` = immediately before `client.Do(req)`.
2. `t_ttfb` = `httptrace.ClientTrace.GotFirstResponseByte`. This is recorded but **is not TTFT**, because the gateway may send headers early.
3. **Parse SSE with `bufio.Reader`.** Do not use `bufio.Scanner` with its 64 KB default; if you use Scanner, set a 1 MiB buffer.
   - Read line by line. Lines starting with `:` are comments or heartbeats: ignore them.
   - `data: ` lines are accumulated until a blank line ends the event. Multiple `data:` lines are joined with `\n`.
   - `data: [DONE]` ends the stream.
   - A stream that ends without `[DONE]` counts as error `stream_truncated`.
4. **For each event:** parse JSON. If it has a top-level `error` object, record error `upstream_error` with its message code.
   - Otherwise take `choices[0].delta`. Let `c = delta.content`, and `r = delta.reasoning` or `delta.reasoning_content` (accept both; vLLM renamed the field, and Ollama also uses `reasoning`).
   - **Token-bearing event** = `len(c) + len(r) > 0`. Servers (vLLM, and possibly the gateway) can send a role-only first event `{"role":"assistant","content":""}`, possibly before any token exists. It **must not** count as the first token.
   - `t_first_token` = time of the first token-bearing event → **TTFT = t_first_token − t0**.
   - `t_first_content` = first event with `len(c) > 0` → **TTFC = t_first_content − t0**. It differs from TTFT only for reasoning models.
   - Record each token-bearing event's timestamp for **ITL**, the gaps between consecutive events: keep p50, p99 and max per request. Max ITL shows prefill stalls.
   - `finish_reason` comes from the last choice that has one.
   - A final event with `usage` (and empty `choices`) gives `prompt_tokens` and `completion_tokens`.
5. **Tokens:**
   - `completion_tokens` from `usage` → `tokens_source=usage`.
   - Fallback: the count of token-bearing events → `tokens_source=chunks` (approximate; vLLM usually sends 1 token per event at the default `stream_interval=1`, but this is not guaranteed).
   - The gateway should force `include_usage` upstream (architect).
6. **Rates:**
   - **Per-stream decode tok/s** = `(completion_tokens − 1) / (t_last_token − t_first_token)`. This excludes TTFT and is the "reading speed" metric the SLO uses.
   - **E2E latency** = `t_done − t0`.
   - **Aggregate tok/s per level** = Σ `completion_tokens` of requests *completed* in the measurement window ÷ window length. The window starts after warmup and ramp and ends at the level's stop.
   - **Time series:** count token-bearing events per 5 s bucket and active streams per bucket.
7. **Status classes:** `ok`, `http_429`, `http_503` (record `Retry-After`), `http_4xx`, `http_5xx`, `timeout`, `stream_truncated`, `upstream_error`, `conn_error`. 429 and 503 are **expected-under-load**; report them separately from the **error rate**, which covers everything else.
8. **Percentiles:** nearest-rank on the sorted per-request values of successful requests in the window. Always print `n`. Aggregate tok/s is not a percentile.
9. **Client side must not be the bottleneck:**
   - Use one `http.Client` with `Transport{MaxIdleConns: 0, MaxIdleConnsPerHost: 256, MaxConnsPerHost: 0, DisableCompression: true, ForceAttemptHTTP2: false, ResponseHeaderTimeout: 0}`. Disabling compression matters: gzip would buffer the stream.
   - One goroutine per virtual user. Results go through a buffered channel to one collector.
   - Record the tool's own CPU% at the end, and warn if > 50 %.
   - For "pure server" numbers, run the tool on the GPU VM itself. For "what students see", run it from a laptop on the university network.

### 6.6 Output

**Console (one row per level):**

```
tag=L4-qwen7b-awq-gw  scenario=step  model=qwen2.5-coder-7b  2026-10-10T14:03Z
level  n   ok   429  503  err%  TTFT p50/p95/p99 (s)  TTFC p95  tok/s/stream p5/p50/p95  agg tok/s  E2E p50/p95 (s)  ITL p99/max (ms)  SLO
   1   40   40    0    0  0.0   0.21/0.35/0.41        0.35      32.1/33.0/33.8            33        15.2/29.8        35/48             -
  10   ...
  30  512  512    0    0  0.0   0.52/0.74/1.10        0.74      13.1/14.9/16.0            420       38.0/67.1        480/610           PASS
```

**Files in `-out`:**
- `summary.json`: tool version and git commit, tag, all flags, start/end time, server `/v1/models` response, per-level stats (same fields as the console), the SLO result, and the tool's own CPU%.
- `requests.csv`, one row per request: `ts_start_unix_ms, level, user, key_id_suffix, endpoint, lang, size_class, http_status, status_class, retry_after_s, prompt_tokens, completion_tokens, tokens_source, ttfb_ms, ttft_ms, ttfc_ms, e2e_ms, decode_tps, itl_p50_ms, itl_p99_ms, itl_max_ms, finish_reason, error`. It stores **no prompt or response text**.
- `levels.csv`: one row per level with every console column, for Giulia's charts.
- `timeseries.csv`: `t_s, level, active_streams, tokens_in_bucket, tok_s, n_429, n_503, n_err`.
- `engine_metrics.csv` (if `-metrics-url`): raw samples of `vllm:num_requests_running`, `vllm:num_requests_waiting`, `vllm:kv_cache_usage_perc`, `vllm:num_preemptions_total`, and the TTFT histogram buckets. Metric names vary by vLLM version, so store whatever matches the prefix `vllm:`.

### 6.7 Tests for the tool itself

- A fake SSE server (httptest) with a scripted timeline: role event at 10 ms, first content at 500 ms, 99 more tokens every 50 ms, a usage event, then `[DONE]`. Assert TTFT = 500 ± 5 ms, decode = 99 / 4.95 s = 20.0 tok/s, and `tokens_source=usage`.
- The same script without the usage event → `tokens_source=chunks`, count 100.
- Reasoning events for 1 s before content → TTFT < TTFC by 1 s.
- Comment lines, multi-line `data:` and CRLF line endings are handled.
- A truncated stream → `stream_truncated`.
- 429 and 503 with `Retry-After` are classified and the value parsed.
- The percentile function is checked against a hand-computed list.
- The race detector is clean (`go test -race`).

---

## 7. Rehearsal plan (on the real GPU, before the lab)

GPU hours are scarce, so script each phase as one command and save everything under `results/`. Target: **≤ 3 GPU-hours in total**.

### Phase 0: now, on Slava's Mac (no GPU)

1. `loadtest -scenario smoke` against the gateway in laptop mode: verifies the tool and all endpoints in all languages.
2. `loadtest -scenario step -levels 1,2,4,8` with `OLLAMA_NUM_PARALLEL=4`, then `=8`. This gives the real laptop capacity and settles whether Ollama batches gpt-oss (§4).
3. Tokenize 5–10 of Szymon's real exercises through the gateway (log `usage.prompt_tokens`). If typical prompts are ≪ 1,500 tokens, every TTFT number in this doc improves.

### Phase 1: GPU bring-up (≈ 30 min)

1. Start vLLM with the §3.1 config. **Record from the startup log:**
   - the quantization kernel (e.g. `awq_marlin`),
   - "KV cache … GiB / tokens",
   - "Maximum concurrency for 8,192 tokens per request: N×". Compare with the "Seqs @ 8K" column; a large deviation means the overhead estimate is off.
2. `nvidia-smi` idle memory; then run `nvidia-smi dmon -s pucm -d 5` for the whole session. Watch power and clock throttling on T4/L4, which are 70–72 W cards.
3. Cross-check with vLLM's own tool, direct to the engine:
   `vllm bench serve --model <served> --dataset-name random --random-input-len 1500 --random-output-len 500 --max-concurrency {1,10,30} --num-prompts {20,100,300}`.
   Verify the exact flag names with `vllm bench serve --help` for v0.30.0.

### Phase 2: our load test, engine direct vs through the gateway (≈ 60 min)

| Step | Command (abridged) | Records |
|---|---|---|
| 2a | `-base-url http://127.0.0.1:8000 -scenario step -levels 1,10,20,30,40 -sizes large=1 -duration 3m` (direct to vLLM, chat only) | Raw engine capacity curve |
| 2b | Same through the gateway, with `-keys-file` of 40 keys | **Gateway overhead** = Δ TTFT p50 between 2a and 2b |
| 2c | `-scenario lab -users 30 -duration 15m` | Realistic TTFT p95 and tok/s (the main slide) |
| 2d | `-scenario burst -users 30` | Burst TTFT p95 (the "click now" slide) |
| 2e | `-scenario limits` | 429 / 503 / Retry-After behaviour |
| 2f | `-scenario lab -users 30 -duration 30m` (soak) | Stability: no errors, no memory growth, no preemptions |
| 2g (only if needed) | Repeat 2c with `--kv-cache-dtype fp8`, `--async-scheduling`, or halved `max_tokens` defaults | Which lever to pull |

### Go / no-go thresholds (judged on 2c and step level 30 of 2b)

| Metric | GO | GO with mitigations | NO-GO |
|---|---|---|---|
| Per-stream decode tok/s at 30 users, p50 | ≥ 12 | 8–12 → halve `max_tokens` defaults, set MAX_INFLIGHT to 24 | < 8 |
| Per-stream decode tok/s, p5 | ≥ 8 | 5–8 | < 5 |
| TTFT p95, `lab` | ≤ 5 s | 5–10 s → shorten the system prompts, cap MAX_INPUT_TOKENS at 3,000 | > 10 s |
| TTFT p95, `burst` | ≤ 20 s | 20–40 s → tell students, stagger the start, rely on prefix caching | > 40 s |
| TTFC p95 (gpt-oss only) | ≤ 8 s | 8–15 s | > 15 s → switch to Qwen2.5-Coder-7B-AWQ |
| Error rate (excluding 429/503) | 0 % (≤ 0.5 %) | ≤ 1 % | > 1 % |
| 503s at 30 users (`lab`) | 0 | < 2 % | ≥ 2 % |
| vLLM preemptions (`num_preemptions_total`) | 0 | < 10 per 15 min | growing steadily (KV too small → lower `--max-num-seqs` or `--max-model-len`) |
| KV cache usage peak | < 80 % | 80–95 % | 100 % sustained |
| Gateway overhead (Δ TTFT p50, 2b − 2a) | < 20 ms | < 100 ms | ≥ 100 ms (a gateway bug; fix it before the lab) |
| 30-min soak | no errors, flat GPU/host memory | — | any crash or OOM |
| `smoke` answer sanity (20 task×language pairs) | all non-empty, code blocks present for `tests`/`fix` | 1–2 weak answers | any empty or garbage → model/prompt issue |

**NO-GO actions, in order:**
1. A smaller or more quantized model on the same GPU (e.g. gpt-oss-20b → Qwen2.5-Coder-7B-AWQ).
2. Split the class into two shifts of 15.
3. Laptop mode for the demo plus a small group (§4).
4. The CPU fallback, for "the API still answers" only.

---

## 8. Least-certain numbers (for the lead to re-check)

1. **Efficiency constants: bandwidth utilisation η 0.55–0.75, prefill MFU 0.25–0.40, 2 ms step overhead.** Every per-user tok/s figure for the 24 GB class (≈ 13–16) depends on them. They were calibrated on batch-1 benchmarks and one L4 gpt-oss test, not on 30-way batches. The ±30 % sensitivity range is 9–21 tok/s. Only the rehearsal settles this.
2. **vLLM non-KV memory overhead (2 GiB dense / 3 GiB MoE).** It was back-solved from one log line, and it decides whether Qwen3-Coder-30B-A3B could squeeze onto 24 GB and how much KV the RTX 4000 Ada has for gpt-oss.
3. **Laptop behaviour:** whether Ollama 0.32 batches gpt-oss:20b on the Mac at all (MLX runner vs llama.cpp path, [ollama#17666](https://github.com/ollama/ollama/issues/17666)), and how efficiently batched MoE decode runs on Metal. Laptop capacity is 2–3 users if requests are serialized and ≈ 5–8 if they are batched.

Also uncertain:
- **CPU prefill** on A1: 10–80 tok/s depending on quant and build.
- **gpt-oss reasoning length at "low" effort**, which drives TTFC.
- **Student think time**, which drives LAB vs SAT.
- **VRAM visible to CUDA** per card (typical `nvidia-smi` values, not datasheet numbers).

---

## 9. Sources (all checked 2026-09-30)

**Hardware:**
- T4: https://www.nvidia.com/en-us/data-center/tesla-t4/
- L4: https://www.nvidia.com/en-us/data-center/l4/
- A10G: https://d1.awsstatic.com/product-marketing/ec2/NVIDIA_AWS_A10G_DataSheet_FINAL_02_17_2022.pdf
- RTX 4000 Ada: https://lenovopress.lenovo.com/lp2144.pdf and https://www.nvidia.com/en-us/design-visualization/rtx-4000/
- A100: https://www.nvidia.com/content/dam/en-zz/Solutions/Data-Center/a100/pdf/nvidia-a100-datasheet-us-nvidia-1758950-r4-web.pdf and https://www.nvidia.com/en-us/data-center/a100/
- M4 Pro: https://www.apple.com/newsroom/2024/10/apple-introduces-m4-pro-and-m4-max/
- OCI Always Free: https://docs.oracle.com/en-us/iaas/Content/FreeTier/resourceref.htm and https://www.infoq.com/news/2026/07/oracle-cloud-free-tier-limits/

**Model configs and weights (Hugging Face):**
- Configs: `https://huggingface.co/<repo>/blob/main/config.json` for Qwen/Qwen2.5-Coder-{1.5B,3B,7B,14B}-Instruct, Qwen/Qwen3-Coder-30B-A3B-Instruct and openai/gpt-oss-20b.
- File sizes: `https://huggingface.co/api/models/<repo>?blobs=true` for the repos above and for -AWQ, -GPTQ-Int4, -FP8 and -GGUF variants, QuantTrio/…-AWQ, cyankiwi/…-AWQ-4bit, unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF and ggml-org/gpt-oss-20b-GGUF.
- gpt-oss index: https://huggingface.co/openai/gpt-oss-20b/blob/main/model.safetensors.index.json

**Benchmarks:**
- Qwen2.5 speed benchmark (A100, vLLM): https://qwen.readthedocs.io/en/v2.5/benchmark/speed_benchmark.html
- devforth, gpt-oss-20b on L4/L40S/H100 (2026-02-20): https://devforth.io/insights/self-hosted-gpt-real-response-time-token-throughput-and-cost-on-l4-l40s-and-h100-for-gpt-oss-20b/
- GPUStack, gpt-oss-20b on A100: https://docs.gpustack.ai/2.0/performance-lab/gpt-oss-20b/a100/
- llama.cpp CUDA scoreboard: https://github.com/ggml-org/llama.cpp/discussions/15013
- llama.cpp Apple Silicon: https://github.com/ggml-org/llama.cpp/discussions/4167
- llama.cpp gpt-oss guide: https://github.com/ggml-org/llama.cpp/discussions/15396
- Red Hat, Ollama vs vLLM: https://developers.redhat.com/articles/2025/08/08/ollama-vs-vllm-deep-dive-performance-benchmarking
- OCI A1 CPU benchmarks: https://tiffena.me/blog/ai-infrastructure/benchmark-cpu-only-llm-inference-oracle-ampere-a1-llama.cpp-ollama-docker/ and https://tiffena.me/blog/ai-infrastructure/benchmark-cpu-only-llm-inference-llama.cpp-server-flags/

**Engines:**
- vLLM GPU install: https://docs.vllm.ai/en/latest/getting_started/installation/gpu.html
- vLLM quantization hardware table: https://github.com/vllm-project/vllm/blob/main/docs/features/quantization/README.md
- vLLM gpt-oss recipe: https://docs.vllm.ai/projects/recipes/en/latest/OpenAI/GPT-OSS.html
- vLLM engine args: https://docs.vllm.ai/en/latest/configuration/engine_args.html
- vLLM config source: https://github.com/vllm-project/vllm/blob/main/vllm/config/scheduler.py, https://github.com/vllm-project/vllm/blob/main/vllm/config/cache.py and https://github.com/vllm-project/vllm/blob/main/vllm/engine/arg_utils.py
- vLLM reasoning outputs: https://github.com/vllm-project/vllm/blob/main/docs/features/reasoning_outputs.md
- vLLM Docker: https://github.com/vllm-project/vllm/blob/main/docs/deployment/docker.md
- vLLM release: https://pypi.org/project/vllm/ (0.30.0, 2026-09-22) and https://hub.docker.com/r/vllm/vllm-openai/tags (v0.30.0)
- llama-server: https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md
- Ollama: https://docs.ollama.com/faq, https://docs.ollama.com/context-length, https://docs.ollama.com/api/openai-compatibility, https://github.com/ollama/ollama/issues/17666 and https://github.com/ollama/ollama/issues/14510

---

## Appendix A: performance model and simulator (reproducible)

To reproduce the §2.4 table, run `python3 capacity.py rows.json` (Python 3, no dependencies, ≈ 15 s). The simulator works step by step:
- Each engine step takes all running decodes (1 token each) plus FCFS prompt chunks, up to 2,048 tokens.
- The step time comes from §1.4.
- A request is admitted only while its prompt + output fits the KV pool, and at most 32 run at once.
- TTFT is recorded at the end of the step that finishes the prompt.
- Per-stream rate = (output − 1) / (t_done − t_first).

The laptop and CPU rows in §2.6 and §4 use the same code with the GPU entry `M4Pro` (Ollama-like 512-token chunks, `max_seqs` = `NUM_PARALLEL`) and the CPU entries shown in A.2.

<details>
<summary>A.1 capacity.py</summary>

```python
"""Capacity math + step-level continuous-batching simulator for the performance doc.

Usage: python3 capacity.py rows.json   (prints one line per GPU x model; ~15 s)
All efficiency constants are ESTIMATES, calibrated against:
 - Qwen2.5 speed benchmark (A100-80GB, vLLM 0.6.3, batch 1): 7B BF16 84.28 tok/s, 14B BF16 46.30, 7B AWQ 148.10, 14B AWQ 92.66
 - devforth gpt-oss-20b on L4 (vLLM 0.15.1): ~60 tok/s single stream, TTFT 3.5 s for ~13k-token prompt
 - Slava's M4 Pro: gpt-oss:20b 49.5 tok/s, prompt eval ~334 tok/s
"""
import math, random, json, sys

GiB = 2**30
GB = 1e9

# ---------- hardware ----------
GPUS = {
    # vram_gib = total memory CUDA reports (typical nvidia-smi value, UNVERIFIED)
    # bw = GB/s spec; tf = dense FP16 tensor TFLOPS spec (RTX4000Ada: estimate)
    "T4":         dict(vram_gib=15.0, bw=320,  tf=65,  mfu=0.30, eta16=0.60, eta4=0.50, fp8kv=False, turing=True),
    "L4":         dict(vram_gib=22.5, bw=300,  tf=121, mfu=0.40, eta16=0.70, eta4=0.55, fp8kv=True,  turing=False),
    "A10G":       dict(vram_gib=22.5, bw=600,  tf=70,  mfu=0.40, eta16=0.70, eta4=0.55, fp8kv=False, turing=False),
    "RTX4000Ada": dict(vram_gib=20.0, bw=360,  tf=82,  mfu=0.40, eta16=0.70, eta4=0.55, fp8kv=True,  turing=False),
    "A100-40":    dict(vram_gib=39.5, bw=1555, tf=312, mfu=0.40, eta16=0.70, eta4=0.55, fp8kv=False, turing=False),
    "A100-80":    dict(vram_gib=79.2, bw=1935, tf=312, mfu=0.40, eta16=0.70, eta4=0.55, fp8kv=False, turing=False),
    # llama.cpp on T4 (gpt-oss has no vLLM path on Turing); whole card minus compute buffers
    "T4-llamacpp": dict(vram_gib=15.0, bw=320, tf=65, mfu=0.30, eta16=0.60, eta4=0.50, fp8kv=False, turing=False, util=0.97, ovh=1.2, mx=0.55),
    # Slava's MacBook Pro M4 Pro 24 GB, Ollama: ~16 GiB GPU-wired; tf*mfu calibrated to 334 tok/s gpt-oss prompt eval
    "M4Pro":      dict(vram_gib=16.0, bw=273, tf=9.8, mfu=0.40, eta16=0.70, eta4=0.70, fp8kv=False, turing=False, util=1.0, ovh=1.0, mx=0.75),
}
STEP_OVERHEAD_S = 0.002  # per engine step (scheduling, sampling, detok) - estimate
GPU_UTIL = 0.90

# ---------- models ----------
# w_gib: resident weights; dense_gb: non-expert bytes read every decode step (embedding table excluded);
# exp_gb: routed-expert bytes (fraction touched depends on tokens in step); E,k: experts total / per token
# fpt: forward FLOPs per token from weights (2 * active non-embedding params)
# kv: KV bytes/token (full-attention layers, fp16); swa_seq: fixed per-seq bytes for sliding-window layers (fp16)
# attn: (n_full_layers, n_q_heads, head_dim) for attention FLOPs
MODELS = {
    "Qwen2.5-Coder-7B AWQ":      dict(w_gib=5.19,  dense_gb=4.48,  exp_gb=0,     E=0,   k=0, fpt=2*7.07e9,  kv=57344,  swa_seq=0, attn=(28, 28, 128), q="int4", moe=False),
    "Qwen2.5-Coder-7B BF16":     dict(w_gib=14.19, dense_gb=14.14, exp_gb=0,     E=0,   k=0, fpt=2*7.07e9,  kv=57344,  swa_seq=0, attn=(28, 28, 128), q="16",   moe=False),
    "Qwen2.5-Coder-14B AWQ":     dict(w_gib=9.29,  dense_gb=8.42,  exp_gb=0,     E=0,   k=0, fpt=2*13.99e9, kv=196608, swa_seq=0, attn=(48, 40, 128), q="int4", moe=False),
    "Qwen2.5-Coder-14B BF16":    dict(w_gib=27.51, dense_gb=27.98, exp_gb=0,     E=0,   k=0, fpt=2*13.99e9, kv=196608, swa_seq=0, attn=(48, 40, 128), q="16",   moe=False),
    "Qwen3-Coder-30B-A3B AWQ":   dict(w_gib=15.66, dense_gb=1.09,  exp_gb=15.07, E=128, k=8, fpt=2*3.04e9,  kv=98304,  swa_seq=0, attn=(48, 32, 128), q="int4", moe=True),
    "Qwen3-Coder-30B-A3B FP8":   dict(w_gib=29.03, dense_gb=1.53,  exp_gb=29.10, E=128, k=8, fpt=2*3.04e9,  kv=98304,  swa_seq=0, attn=(48, 32, 128), q="fp8",  moe=True),
    "Qwen3-Coder-30B-A3B BF16":  dict(w_gib=56.87, dense_gb=2.43,  exp_gb=57.98, E=128, k=8, fpt=2*3.04e9,  kv=98304,  swa_seq=0, attn=(48, 32, 128), q="16",   moe=True),
    "gpt-oss-20b MXFP4":         dict(w_gib=12.82, dense_gb=2.43,  exp_gb=10.15, E=32,  k=4, fpt=2*3.61e9,  kv=24576,  swa_seq=12*2048*128, attn=(12, 64, 64), q="mxfp4", moe=True),
    "gpt-oss-20b GGUF":          dict(w_gib=11.28, dense_gb=1.96,  exp_gb=10.15, E=32,  k=4, fpt=2*3.61e9,  kv=24576,  swa_seq=12*2048*128, attn=(12, 64, 64), q="mxfp4", moe=True),
    "Qwen2.5-Coder-7B Q4_K_M":   dict(w_gib=4.36,  dense_gb=4.25,  exp_gb=0,     E=0,   k=0, fpt=2*7.07e9,  kv=57344,  swa_seq=0, attn=(28, 28, 128), q="int4", moe=False),
    "Qwen2.5-Coder-1.5B Q4_K_M": dict(w_gib=1.04,  dense_gb=1.10,  exp_gb=0,     E=0,   k=0, fpt=2*1.54e9,  kv=28672,  swa_seq=0, attn=(28, 12, 128), q="int4", moe=False),
}

def overhead_gib(m, g=None):
    if g is not None and "ovh" in g:
        return g["ovh"]
    # activations + CUDA graphs + CUDA context + sampler (estimate; MoE value back-solved from the
    # 'Maximum concurrency 2.78x' that vLLM printed for gpt-oss-20b on L4 in the devforth test)
    return 3.0 if m["moe"] else 2.0

def fits(gname, mname):
    g, m = GPUS[gname], MODELS[mname]
    if g["turing"] and m["q"] in ("mxfp4",):
        return False, "vLLM: Turing has no Marlin MXFP4"
    if g["turing"] and m["q"] == "fp8":
        return False, "no FP8 on Turing"
    budget = kv_budget_gib(gname, mname)
    if budget <= 0.5:
        return False, f"weights {m['w_gib']:.1f} GiB + overhead leave {budget:.1f} GiB"
    return True, ""

def kv_budget_gib(gname, mname):
    g, m = GPUS[gname], MODELS[mname]
    return g["vram_gib"] * g.get("util", GPU_UTIL) - m["w_gib"] - overhead_gib(m, g)

def kv_tokens(gname, mname, fp8=False):
    m = MODELS[mname]
    b = kv_budget_gib(gname, mname) * GiB
    per = m["kv"] / (2 if fp8 else 1)
    return b / per

def seqs_at(gname, mname, ctx, fp8=False):
    m = MODELS[mname]
    b = kv_budget_gib(gname, mname) * GiB
    per_seq = ctx * m["kv"] / (2 if fp8 else 1) + m["swa_seq"] / (2 if fp8 else 1)
    return b / per_seq

def eta(g, m):
    if m["q"] == "mxfp4":
        return g.get("mx", 0.75)  # calibrated: devforth L4 ~60 tok/s, M4 Pro 49.5 tok/s
    return g["eta4"] if m["q"] == "int4" else g["eta16"]

def mfu(g, m):
    return g["mfu"] * (0.625 if m["moe"] else 1.0)  # MoE prefill less efficient: 0.25 vs 0.40 (calibrated on devforth L4)

def expert_frac(m, ntok):
    if not m["moe"] or ntok <= 0:
        return 0.0
    return 1 - (1 - m["k"] / m["E"]) ** ntok

def attn_flops_per_token(m, ctx):
    L, H, d = m["attn"]
    return 4 * L * H * d * ctx

def step_time(g, m, n_decode, decode_ctx_sum, n_prefill, prefill_ctx_mid, fp8=False):
    ntok = n_decode + n_prefill
    kvb = m["kv"] / (2 if fp8 else 1)
    kv_read = decode_ctx_sum * kvb + n_decode * m["swa_seq"] / (2 if fp8 else 1)
    wbytes = m["dense_gb"] * GB + m["exp_gb"] * GB * expert_frac(m, ntok)
    t_mem = (wbytes + kv_read) / (g["bw"] * GB * eta(g, m))
    flops = ntok * m["fpt"] + n_decode * attn_flops_per_token(m, decode_ctx_sum / max(n_decode, 1)) \
        + n_prefill * attn_flops_per_token(m, prefill_ctx_mid)
    t_comp = flops / (g["tf"] * 1e12 * mfu(g, m))
    return max(t_mem, t_comp) + STEP_OVERHEAD_S, t_mem, t_comp

# ---------- simulator ----------
class Req:
    __slots__ = ("user", "arr", "prompt", "out", "pf_done", "gen", "t_first", "t_done")
    def __init__(s, user, arr, prompt, out):
        s.user, s.arr, s.prompt, s.out = user, arr, prompt, out
        s.pf_done = 0; s.gen = 0; s.t_first = None; s.t_done = None

def simulate(g, m, *, users=30, think_mean=30.0, ramp=60.0, duration=1200.0, burst=False,
             prompt_rng=(800, 2200), out_rng=(200, 1000), max_seqs=32, chunk=2048, fp8=False,
             kv_capacity_tokens=None, seed=1, warmup=60.0):
    rnd = random.Random(seed)
    t = 0.0
    pending = []  # (arrival_time, user)
    if burst:
        for u in range(users):
            pending.append((0.0, u))
    else:
        for u in range(users):
            pending.append((rnd.uniform(0, ramp), u))
    pending.sort()
    waiting, running, done = [], [], []
    cap = kv_capacity_tokens if kv_capacity_tokens else float("inf")
    def new_req(u, at):
        if burst:
            return Req(u, at, 1500, 500)
        return Req(u, at, rnd.randint(*prompt_rng), rnd.randint(*out_rng))
    while t < duration:
        while pending and pending[0][0] <= t:
            at, u = pending.pop(0)
            waiting.append(new_req(u, at))
        # admit (reserve prompt + expected output in KV)
        used = sum(r.prompt + r.out for r in running)
        while waiting and len(running) < max_seqs and used + waiting[0].prompt + waiting[0].out <= cap:
            r = waiting.pop(0); running.append(r); used += r.prompt + r.out
        if not running:
            if pending:
                t = max(t, pending[0][0]); continue
            if burst:
                break
            t += 0.05; continue
        dec = [r for r in running if r.pf_done >= r.prompt]
        budget = chunk - len(dec)
        pf_alloc = []
        for r in running:
            if r.pf_done < r.prompt and budget > 0:
                take = min(budget, r.prompt - r.pf_done)
                pf_alloc.append((r, take)); budget -= take
        n_pf = sum(x for _, x in pf_alloc)
        ctx_sum = sum(r.prompt + r.gen for r in dec)
        mid = (sum((r.pf_done + x / 2) * x for r, x in pf_alloc) / n_pf) if n_pf else 0
        dt, _, _ = step_time(g, m, len(dec), ctx_sum, n_pf, mid, fp8)
        t += dt
        for r in dec:
            r.gen += 1
        for r, x in pf_alloc:
            r.pf_done += x
            if r.pf_done >= r.prompt:
                r.gen = 1; r.t_first = t
        fin = [r for r in running if r.pf_done >= r.prompt and r.gen >= r.out]
        for r in fin:
            r.t_done = t; running.remove(r); done.append(r)
            if not burst:
                pending.append((t + rnd.expovariate(1 / think_mean) if think_mean > 0 else t, r.user))
        pending.sort()
    ok = [r for r in done if r.arr >= (0 if burst else warmup)]
    if not ok:
        return None
    ttft = sorted(r.t_first - r.arr for r in ok)
    rate = sorted((r.out - 1) / (r.t_done - r.t_first) for r in ok if r.t_done > r.t_first)
    span = (max(r.t_done for r in ok) - (0 if burst else warmup))
    agg = sum(r.out for r in ok) / span if span > 0 else 0
    def pct(a, p):
        return a[min(len(a) - 1, int(math.ceil(p / 100 * len(a))) - 1)]
    return dict(n=len(ok), ttft_p50=pct(ttft, 50), ttft_p95=pct(ttft, 95), ttft_p99=pct(ttft, 99),
                tps_p50=pct(rate, 50), tps_p5=pct(rate, 5), agg=agg)

COMBOS = [
    ("T4", "Qwen2.5-Coder-7B AWQ"),
    ("T4", "Qwen2.5-Coder-14B AWQ"),
    ("L4", "Qwen2.5-Coder-7B AWQ"),
    ("L4", "Qwen2.5-Coder-7B BF16"),
    ("L4", "Qwen2.5-Coder-14B AWQ"),
    ("L4", "gpt-oss-20b MXFP4"),
    ("L4", "Qwen3-Coder-30B-A3B AWQ"),
    ("A10G", "Qwen2.5-Coder-7B AWQ"),
    ("A10G", "Qwen2.5-Coder-7B BF16"),
    ("A10G", "Qwen2.5-Coder-14B AWQ"),
    ("A10G", "gpt-oss-20b MXFP4"),
    ("A10G", "Qwen3-Coder-30B-A3B AWQ"),
    ("RTX4000Ada", "Qwen2.5-Coder-7B AWQ"),
    ("RTX4000Ada", "Qwen2.5-Coder-14B AWQ"),
    ("RTX4000Ada", "gpt-oss-20b MXFP4"),
    ("RTX4000Ada", "Qwen3-Coder-30B-A3B AWQ"),
    ("A100-40", "Qwen2.5-Coder-7B BF16"),
    ("A100-40", "Qwen2.5-Coder-14B AWQ"),
    ("A100-40", "Qwen2.5-Coder-14B BF16"),
    ("A100-40", "gpt-oss-20b MXFP4"),
    ("A100-40", "Qwen3-Coder-30B-A3B AWQ"),
    ("A100-40", "Qwen3-Coder-30B-A3B FP8"),
    ("A100-80", "Qwen2.5-Coder-14B BF16"),
    ("A100-80", "gpt-oss-20b MXFP4"),
    ("A100-80", "Qwen3-Coder-30B-A3B FP8"),
    ("A100-80", "Qwen3-Coder-30B-A3B BF16"),
    ("T4-llamacpp", "gpt-oss-20b GGUF"),
]

def analyze(gname, mname, fp8=None):
    g, m = GPUS[gname], MODELS[mname]
    ok, why = fits(gname, mname)
    if fp8 is None:
        fp8 = g["fp8kv"]
    row = dict(gpu=gname, model=mname, fits=ok, why=why, fp8=fp8)
    if not ok:
        return row
    row["kv_gib"] = kv_budget_gib(gname, mname)
    row["kv_tok"] = kv_tokens(gname, mname, fp8)
    row["seq8k"] = seqs_at(gname, mname, 8192, fp8)
    row["seq2k5"] = seqs_at(gname, mname, 2500, fp8)
    # steady decode, 30 streams, avg ctx 2000
    B = 30
    dt, tm, tc = step_time(g, m, B, B * 2000, 0, 0, fp8)
    row["dec_step_ms"] = dt * 1000; row["dec_tmem_ms"] = tm * 1000; row["dec_tcomp_ms"] = tc * 1000
    row["dec_user"] = 1 / dt; row["dec_agg"] = B / dt
    dt1, _, _ = step_time(g, m, 1, 2000, 0, 0, fp8)
    row["single"] = 1 / dt1
    # prefill rate & TTFT for 1500-token prompt while 29 others decode (one mixed step)
    dtp, _, tcp = step_time(g, m, 29, 29 * 2000, 1500, 750, fp8)
    row["pf_rate"] = 1500 / (tcp)
    row["ttft_one"] = dtp + dt / 2  # wait half a decode step + the mixed step
    # burst: 30 x 1500 at once; p95 ~ 29th completes -> 29*1500 tokens of prefill at chunk 2048
    row["ttft_burst_p95"] = 29 * 1500 / row["pf_rate"] * 1.0
    cap = row["kv_tok"]
    sat = simulate(g, m, think_mean=0.0, fp8=fp8, kv_capacity_tokens=cap)
    lab = simulate(g, m, think_mean=30.0, fp8=fp8, kv_capacity_tokens=cap)
    bur = simulate(g, m, burst=True, fp8=fp8, kv_capacity_tokens=cap)
    row["sat"], row["lab"], row["burst"] = sat, lab, bur
    return row

if __name__ == "__main__":
    rows = [analyze(g, m, fp8=False) for g, m in COMBOS]  # table in the doc uses fp16 KV everywhere
    json.dump(rows, open(sys.argv[1] if len(sys.argv) > 1 else "rows.json", "w"), indent=1)
    for r in rows:
        if not r["fits"]:
            print(f"{r['gpu']:11s} {r['model']:26s} DOES NOT FIT: {r['why']}")
            continue
        s, l, b = r["sat"], r["lab"], r["burst"]
        print(f"{r['gpu']:11s} {r['model']:26s} kv={r['kv_gib']:.1f}GiB tok={r['kv_tok']/1000:.0f}k seq8k={r['seq8k']:.1f} seq2.5k={r['seq2k5']:.1f} "
              f"single={r['single']:.0f} dec30={r['dec_user']:.1f}/{r['dec_agg']:.0f} pf={r['pf_rate']:.0f} ttft1={r['ttft_one']:.2f} burst95={r['ttft_burst_p95']:.1f} | "
              f"SAT ttft95={s['ttft_p95']:.1f} tps50={s['tps_p50']:.1f} tps5={s['tps_p5']:.1f} agg={s['agg']:.0f} | "
              f"LAB ttft95={l['ttft_p95']:.1f} tps50={l['tps_p50']:.1f} tps5={l['tps_p5']:.1f} | BURST ttft95={b['ttft_p95']:.1f} tps50={b['tps_p50']:.1f}")
```

</details>

<details>
<summary>A.2 laptop_cpu.py (laptop and CPU runs)</summary>

```python
from capacity import *
import capacity
# Laptop: M4 Pro, Ollama-like: chunk=512 (num_batch), NUM_PARALLEL slots
print("== M4 Pro single-stream / prefill check")
g = GPUS["M4Pro"]
for mn in ["gpt-oss-20b GGUF", "Qwen2.5-Coder-7B Q4_K_M", "Qwen2.5-Coder-1.5B Q4_K_M"]:
    m = MODELS[mn]
    dt,_,_ = step_time(g, m, 1, 500, 0, 0)
    _,_,tc = step_time(g, m, 0, 0, 512, 256)
    print(f"{mn:28s} single={1/dt:.1f} tok/s  prefill={512/tc:.0f} tok/s  -> 1500-tok TTFT alone {1500/(512/tc):.1f}s")
print("== M4 Pro lab scenario (think 30s), prompts 800-2200, out 200-1000")
for mn in ["gpt-oss-20b GGUF", "Qwen2.5-Coder-7B Q4_K_M"]:
    for npar in [1, 2, 4, 8]:
        for users in [1, 3, 5, 8, 10, 15]:
            r = simulate(g, MODELS[mn], users=users, think_mean=30.0, max_seqs=npar, chunk=512, duration=1800)
            print(f"{mn:24s} NP={npar} users={users:2d} ttft50={r['ttft_p50']:5.1f} ttft95={r['ttft_p95']:6.1f} tps50={r['tps_p50']:5.1f} tps5={r['tps_p5']:5.1f} agg={r['agg']:5.0f}")
print("== M4 Pro lab scenario, SHORT prompts 300-800, out 200-600")
for mn in ["gpt-oss-20b GGUF"]:
    for npar in [4, 8]:
        for users in [5, 8, 10, 15]:
            r = simulate(g, MODELS[mn], users=users, think_mean=30.0, max_seqs=npar, chunk=512, duration=1800, prompt_rng=(300,800), out_rng=(200,600))
            print(f"{mn:24s} NP={npar} users={users:2d} ttft50={r['ttft_p50']:5.1f} ttft95={r['ttft_p95']:6.1f} tps50={r['tps_p50']:5.1f} tps5={r['tps_p5']:5.1f} agg={r['agg']:5.0f}")

# CPU: Oracle A1 (effective numbers back-solved from tiffena.me measurements, Qwen2.5-3B Q4_K_M, 4 OCPU)
capacity.STEP_OVERHEAD_S = 0.002
CPU = {
 "A1-4ocpu": dict(vram_gib=22.0, bw=21.6, tf=0.065, mfu=1.0, eta16=1.0, eta4=1.0, fp8kv=False, turing=False, util=1.0, ovh=0.5, mx=1.0),
 "A1-2ocpu": dict(vram_gib=10.0, bw=13.0, tf=0.0325, mfu=1.0, eta16=1.0, eta4=1.0, fp8kv=False, turing=False, util=1.0, ovh=0.5, mx=1.0),
}
MODELS["Qwen2.5-Coder-3B Q4_K_M"] = dict(w_gib=1.96, dense_gb=2.0, exp_gb=0, E=0, k=0, fpt=2*3.09e9, kv=36864, swa_seq=0, attn=(36,16,128), q="int4", moe=False)
print("== CPU")
for cn, c in CPU.items():
    for mn in ["Qwen2.5-Coder-1.5B Q4_K_M", "Qwen2.5-Coder-3B Q4_K_M"]:
        m = MODELS[mn]
        dt,_,_ = step_time(c, m, 1, 500, 0, 0)
        _,_,tc = step_time(c, m, 0, 0, 512, 256)
        agg = [ (b/step_time(c, m, b, b*1000, 0, 0)[0]) for b in (1,2,4,8)]
        print(f"{cn} {mn:26s} single={1/dt:.1f} prefill={512/tc:.0f} tok/s TTFT(1500)={1500/(512/tc):.0f}s TTFT(300)={300/(512/tc):.0f}s agg@1,2,4,8={[round(a,1) for a in agg]}")
        for npar in [1,2,4]:
            for users in [1,2,3,5]:
                r = simulate(c, m, users=users, think_mean=60.0, max_seqs=npar, chunk=512, duration=3600, prompt_rng=(200,500), out_rng=(100,300))
                print(f"   NP={npar} users={users} (short prompts 200-500, out 100-300) ttft50={r['ttft_p50']:5.1f} ttft95={r['ttft_p95']:6.1f} tps50={r['tps_p50']:5.1f} agg={r['agg']:5.1f}")
```

</details>
