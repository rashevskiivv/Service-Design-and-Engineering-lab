# B10 "Model size matters" · `qwen2.5-coder:1.5b`

- **Date:** 2026-10-15
- **Model:** `qwen2.5-coder:1.5b` (ID `d7372fd82851`), no thinking phase
- **Machine:** MacBook Pro M4 Pro 24 GB, macOS 15.8.1, Ollama 0.40.3
- **Settings:** Ollama defaults (model's own temperature, context 4096), native `/api/chat` with streaming, one run, not cherry-picked
- **Prompts:** [lab-plan.md §4 B10](../../../docs/lab-plan.md)

## 1. Review

**Prompt**

````text
Review this for bugs:

def avg(xs): return sum(xs)/len(xs)
````

**Answer** — first word after 0.03 s, 326 tokens at 75.6 tok/s

The function `avg` is a simple implementation of calculating the average of a list of numbers. However, it has a few bugs:

1. **Divide by Zero**: If the list `xs` is empty, the function will raise a `ZeroDivisionError` because the length of an empty list is `0`. To prevent this, we can add a check at the beginning of the function to handle the case where the list is empty.

2. **Return Type**: The function returns a float, which is correct for the average of a list of numbers. However, if the list contains only one element, the function will return a float even if the element is not an integer. This can be problematic if you expect the average to be an integer.

Here's the corrected version of the function:

```python
def avg(xs):
    # Check if the list is empty
    if not xs:
        return None  # or handle the case differently, e.g., raise a ValueError
    
    # Calculate the sum of the list
    total = sum(xs)
    
    # Calculate the average
    return total / len(xs)
```

In this corrected version:
- The function checks if the list `xs` is empty. If it is, it returns `None` or raises a `ValueError` depending on your preference.
- If the list is not empty, it calculates the sum of the list using `sum(xs)`.
- It then calculates the average by dividing the total by the length of the list.

This version ensures that the function handles edge cases gracefully and provides a consistent output.

