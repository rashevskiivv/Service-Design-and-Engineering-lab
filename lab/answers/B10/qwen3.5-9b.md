# B10 "Model size matters" · `qwen3.5:9b`

- **Date:** 2026-10-15
- **Model:** `qwen3.5:9b` (ID `d63f830f28d5`), thinking off (`think: false`, same as `--think=false`)
- **Machine:** MacBook Pro M4 Pro 24 GB, macOS 15.8.1, Ollama 0.40.3
- **Settings:** Ollama defaults (model's own temperature, context 4096), native `/api/chat` with streaming, one run, not cherry-picked
- **Prompts:** [lab-plan.md §4 B10](../../../docs/lab-plan.md)

## 1. Review

**Prompt**

````text
Review this for bugs:

def avg(xs): return sum(xs)/len(xs)
````

**Answer** — first word after 0.16 s, 332 tokens at 51.8 tok/s

The provided function `avg` contains two critical issues that will likely cause runtime errors or incorrect results depending on the input:

### 1. ZeroDivisionError
If the list `xs` is empty (`[]`), `len(xs)` returns `0`. Dividing by zero raises a `ZeroDivisionError`.
*   **Scenario:** `avg([])` crashes.

### 2. Type Error (Return Type Mismatch)
The function always performs division, which in Python 3 returns a **float**. If the user expects an integer result (common when averaging numbers that result in whole numbers), this might be undesirable without explicit casting. Furthermore, if `xs` contains non-numeric types but no error handling is added to check length first, the behavior can be unpredictable compared to robust implementations.

### Recommended Fix
To make the function robust, you should handle the empty list case explicitly. Here is the improved version:

```python
def avg(xs):
    if not xs:  # Checks if the list is empty
        raise ValueError("Cannot calculate average of an empty list")
    return sum(xs) / len(xs)

# Alternative: Return None or 0 for empty lists depending on your logic needs
def avg_safe(xs):
    if not xs:
        return None # Or return 0
    return sum(xs) / len(xs)
```

### Summary of Bugs Fixed
1.  **Prevention:** Added a check to prevent division by zero.
2.  **Clarity:** Explicitly defines what happens when no data is provided, rather than crashing unexpectedly.

