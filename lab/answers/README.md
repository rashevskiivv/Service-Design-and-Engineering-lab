# Pre-generated answers (projector backup)

Answers to E1, E2 and B10 from [lab-plan.md](../../docs/lab-plan.md), generated on 2026-10-15 on Slava's MacBook Pro M4 Pro 24 GB with Ollama 0.40.3. One run per model with Ollama's defaults, not cherry-picked. Use them when a live run is slow (cut list, [lab-plan.md §1](../../docs/lab-plan.md)) or to compare a small and a bigger model side by side.

| | `qwen2.5-coder:1.5b` | `qwen2.5-coder:7b` (class default) | `qwen3.5:9b`, thinking off |
|---|---|---|---|
| **E1** explain → predict → debug | [E1](E1/qwen2.5-coder-1.5b.md): predicts scrambled words; wrong fix (bounds checks) | [E1](E1/qwen2.5-coder-7b.md): predict misses the panic; right fix, wrong story | [E1](E1/qwen3.5-9b.md): predict misses the panic; right fix, muddled story |
| **E2** notes service | [E2](E2/qwen2.5-coder-1.5b.md): runs as written | [E2](E2/qwen2.5-coder-7b.md): runs, but listens on all interfaces and `GET` returns `{"notes": [...]}` | [E2](E2/qwen3.5-9b.md): runs as written, with `400` on bad input |
| **B10** review `avg` | [B10](B10/qwen2.5-coder-1.5b.md): finds the empty list | [B10](B10/qwen2.5-coder-7b.md): finds the empty list | [B10](B10/qwen3.5-9b.md): finds the empty list |

- The tok/s inside the files are per answer and vary run to run. Reference speeds: [runbook §2](../../docs/runbook-lab-day.md).
- File names use `-` instead of `:` so the repository checks out on Windows.
