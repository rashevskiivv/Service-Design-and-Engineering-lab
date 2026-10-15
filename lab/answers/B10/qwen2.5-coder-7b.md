# B10 "Model size matters" · `qwen2.5-coder:7b`

- **Date:** 2026-10-15
- **Model:** `qwen2.5-coder:7b` (ID `dae161e27b0e`), no thinking phase
- **Machine:** MacBook Pro M4 Pro 24 GB, macOS 15.8.1, Ollama 0.40.3
- **Settings:** Ollama defaults (model's own temperature, context 4096), native `/api/chat` with streaming, one run, not cherry-picked
- **Prompts:** [lab-plan.md §4 B10](../../../docs/lab-plan.md)

## 1. Review

**Prompt**

````text
Review this for bugs:

def avg(xs): return sum(xs)/len(xs)
````

**Answer** — first word after 0.11 s, 263 tokens at 29.5 tok/s

The function `avg(xs)` you provided is a simple function to calculate the average of a list of numbers. However, it's important to consider a few things to ensure it works correctly and handles edge cases:

1. **Empty List**: If the list `xs` is empty, `len(xs)` will be 0, and attempting to divide by 0 will raise a `ZeroDivisionError`.

2. **Type Checking**: It's good practice to check if the input is indeed a list or if it contains only numbers.

Here's a revised version of your function with these considerations:

```python
def avg(xs):
    if not isinstance(xs, list):
        raise ValueError("Input must be a list.")
    if len(xs) == 0:
        raise ValueError("List cannot be empty.")
    if not all(isinstance(x, (int, float)) for x in xs):
        raise ValueError("All elements in the list must be numbers.")
    
    return sum(xs) / len(xs)
```

This version of the function includes:
- A check to ensure the input is a list.
- A check to ensure the list is not empty.
- A check to ensure all elements in the list are either integers or floats.

This will help prevent common errors and make the function more robust.

