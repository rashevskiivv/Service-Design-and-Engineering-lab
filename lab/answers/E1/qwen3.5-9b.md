# E1 "I don't know Go" · `qwen3.5:9b`

- **Date:** 2026-10-15
- **Model:** `qwen3.5:9b` (ID `d63f830f28d5`), thinking off (`think: false`, same as `--think=false`)
- **Machine:** MacBook Pro M4 Pro 24 GB, macOS 15.8.1, Ollama 0.40.3
- **Settings:** Ollama defaults (model's own temperature, context 4096), native `/api/chat` with streaming, one run, not cherry-picked
- **Prompts:** [lab-plan.md §3 E1](../../../docs/lab-plan.md); steps 1–2 are one conversation, step 3 a new one

## 1. Explain

**Prompt**

````text
I know Python but not Go. Explain this Go program line by line,
and compare it with Python where that helps.

package main

import (
	"fmt"
	"strings"
)

// Lab 4, exercise 1: reverse the order of the words in a sentence
// and report the longest word.

func reverseWords(s string) string {
	words := strings.Fields(s)
	for i, j := 0, len(words); i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
	return strings.Join(words, " ")
}

func longest(words []string) string {
	best := ""
	for _, w := range words {
		if len(w) > len(best) {
			best = w
		}
	}
	return best
}

func main() {
	s := "go is a small language with a big standard library"
	fmt.Println(reverseWords(s))
	fmt.Println("longest:", longest(strings.Fields(s)))
}
````

**Answer** — first word after 0.79 s, 2121 tokens at 55.9 tok/s

This Go program demonstrates two core concepts: **slicing manipulation** (reversing an array of strings) and **finding the maximum value based on a condition** (finding the longest string). It contrasts Python's dynamic arrays with Go's static slices.

Here is the line-by-line explanation with Python comparisons.

### 1. Package and Imports
```go
package main

import (
	"fmt"
	"strings"
)
```
*   **`package main`**: This tells the Go compiler that this code should be compiled into an executable binary rather than a library package. In Python, the concept of "packages" is less rigid at the entry point; usually, you just write a file (e.g., `main.py`) and run it directly without declaring a package type, although you can import modules similarly.
*   **`import`**: Go requires explicit imports for every library function used.
    *   **`fmt`**: Used for formatting output (`println`). Equivalent to Python's built-in `print()`.
    *   **`strings`**: A standard library providing string manipulation functions like `Fields` and `Join`. In Python, these capabilities are usually found in the builtin `str` type methods or the `shutil/re` modules, but often handled via direct string indexing since lists are native.

---

### 2. Function: `reverseWords`
```go
func reverseWords(s string) string {
	words := strings.Fields(s)
	for i, j := 0, len(words); i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
	return strings.Join(words, " ")
}
```

#### Line 1: Function Definition
```go
func reverseWords(s string) string {
```
*   **Go**: Defines a function named `reverseWords` taking an argument `s` of type `string` and returning a `string`. Types are explicit (statically typed).
*   **Python Comparison**: In Python, this would be defined as `def reverse_words(s): ...`. No type hints (`: str`) were needed, though modern Python allows them.

#### Line 2: Splitting the String
```go
words := words := strings.Fields(s)
```
*   **Go**: Splits the input string `s` into a slice of strings based on whitespace. It returns `["go", "is", "a", ...]`. The type is inferred as `[]string`.
    *   *Note*: Go does not return a list object directly; it creates a new slice in memory.
*   **Python Comparison**:
    ```python
    words = s.split() # equivalent to strings.Fields
    ```
    `split()` without arguments splits by any whitespace run, matching `Fields` behavior perfectly.

#### Lines 3-5: Reversing the Slice (The Core Logic)
```go
	for i, j := 0, len(words); i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
```
*   **Logic**: This is a "two-pointer" swap algorithm. It starts with pointers `i` (left) and `j` (right). While `i` is less than `j`, it swaps the elements at these positions and moves them inward.
*   **Go Syntax Details**:
    *   `for i, j := 0, len(words); ...`: This is a **`for-range` style initialization**. You can initialize multiple variables here (`i` and `j`) with their starting values.
    *   The condition `i < j` ensures we only swap until the pointers meet in the middle (avoiding double-swapping or index out of bounds).
    *   `i, j = i+1, j-1`: The update clause increments the left pointer and decrements the right pointer simultaneously.
    *   `words[i], words[j] = words[j], words[i]`: This is **multiple assignment**, which swaps two variables in a single statement. It reads both values, then assigns them back reversed.
*   **Python Comparison**:
    While Go allows initialization inside the loop header, Python would write this as:
    ```python
    i, j = 0, len(words) - 1 # Note: Go uses len directly; Python indices are 0-based, so len-1 is the last index
    while i < j:
        words[i], words[j] = words[j], words[i] # Same multi-assignment syntax!
        i += 1
        j -= 1
    ```
    *Key Takeaway*: Python requires explicit `while` loop construction with manual incrementing, whereas Go combines initialization, condition, and update in one header.

#### Line 6: Joining back
```go
	return strings.Join(words, " ")
```
*   **Go**: Takes the reversed slice `words` and joins them back into a single string using `" "` (space) as the delimiter.
*   **Python Comparison**:
    ```python
    return " ".join(words)
    ```
    The syntax is nearly identical in intent, though Go passes the separator as an argument to the method, whereas Python's `join` expects the separator after the dot (e.g., `" ".join(...)`).

---

### 3. Function: `longest`
```go
func longest(words []string) string {
	best := ""
	for _, w := range words {
		if len(w) > len(best) {
			best = w
		}
	}
	return best
}
```

#### Line 1 & 2: Definition and Initialization
```go
func longest(words []string) string {
	best := ""
```
*   **Go**: `longest` takes a slice of strings (`[]string`). `best := ""` initializes a variable to hold the current longest word. Since Go is statically typed, `best` must be declared before assignment or initialized with a value of the correct type (empty string here).
*   **Python Comparison**:
    ```python
    def longest(words): # No need to specify []string explicitly
        best = ""
    ```

#### Lines 4-6: Range Loop and Comparison
```go
	for _, w := range words {
		if len(w) > len(best) {
			best = w
		}
	}
```
*   **Go**: `for _, w := range words` iterates over the slice.
    *   The first element (`_`) is ignored (discard).
    *   The second element (`w`) holds the current word being inspected.
    *   Inside, it compares lengths. If the current word `w` is longer than `best`, it updates `best`.
*   **Python Comparison**:
    ```python
    for w in words: # Python automatically handles the iteration index/value pair
        if len(w) > len(best):
            best = w
    ```
    *Note*: In Python, you don't need the underscore `_` or to explicitly manage indices; the `for` loop yields items directly.

#### Line 9: Return
```go
	return best
```
*   Same as Python's `return best`.

---

### 4. Main Execution Flow
```go
func main() {
	s := "go is a small language with a big standard library"
	fmt.Println(reverseWords(s))
	fmt.Println("longest:", longest(strings.Fields(s)))
}
```

#### Line 2: Variable Assignment
```go
s := "go is a small language with a big standard library"
```
*   **Go**: Defines string `s`. The type `string` is fixed.
*   **Python**:
    ```python
    s = "go is a small language with a big standard library"
    ```

#### Lines 3-4: Output
```go
	fmt.Println(reverseWords(s))
	fmt.Println("longest:", longest(strings.Fields(s)))
```
*   **Go**: Calls the functions and prints results. The format `"longest:"` is inserted directly into the print statement.
*   **Python**:
    ```python
    print(reverse_words(s))
    print(f"longest: {longest(s.split())}") # Using f-string (modern Python) or comma separation
    # print("longest:", longest(s.split())) # This works too in Python, adding a space between args automatically
    ```

### Summary of Differences

| Feature | Go Implementation | Python Equivalent |
| :--- | :--- | :--- |
| **Types** | Explicit: `func ... (string) string`, `[]string` | Implicit/Optional: `def ... (s):`, list objects are dynamic. |
| **Imports** | Mandatory `import "package"` | Optional; built-ins like `print` and `split` are native. |
| **Looping** | `for init; condition; update { }` | `for variable in collection: {}` or `while` loops. |
| **Swapping** | Multi-assignment: `a, b = b, a` | Multi-assignment: `a, b = b, a` (Identical syntax). |
| **Iteration** | `range` requires handling indices (`_`) or explicit indexing. | Iterates values directly; no need to handle indices manually for values. |
| **Memory** | Slices (`[]T`) are dynamic views over an underlying array, but resizing may reallocate. | Lists are full dynamic arrays that resize automatically during iteration/appending. |

### Expected Output
When you run this code:
```text
library standard big with language small a is go
longest: library
```

## 2. Predict (same conversation)

**Prompt**

````text
What does it print?
````

**Answer** — first word after 6.3 s, 605 tokens at 56.9 tok/s

Based on the logic in the Go program, here is exactly what it will print to the console:

```text
library standard big with language small a is go
longest: library
```

### Breakdown of the Output:

1.  **First Line**: `library standard big with language small a is go`
    *   The original string was: `"go is a small language with a big standard library"`
    *   The function `reverseWords` split it into words, reversed the order of that list, and joined them back together.

2.  **Second Line**: `longest: library`
    *   The function `longest` scans the *original* words (split by whitespace) to find the one with the most characters.
    *   Comparing lengths:
        *   "go" (2), "is" (2), "a" (1), "small" (5), "language" (8), "with" (4), "big" (3), "standard" (8), "**library**" (7).
    *   Wait, let me double-check the length of "language" vs "library".
        *   `l-a-n-g-u-a-g-e` = 8 letters.
        *   `l-i-b-r-a-r-y` = 7 letters.
    *   **Correction**: My mental count in the summary above was slightly off regarding "language". Let's re-verify strictly.
        *   `language`: l(1)a(2)n(3)g(4)u(5)a(6)g(7)e(8) -> **8** chars.
        *   `standard`: s(1)t(2)a(3)n(4)d(5)a(6)r(7)d(8) -> **8** chars.
        *   `library`: l(1)i(2)b(3)r(4)a(5)r(6)y(7) -> **7** chars.

    **Re-evaluating the code logic**:
    The function iterates through the words and updates `best` only if `len(w) > len(best)` (strictly greater).
    *   It starts with `best = ""`.
    *   It hits "go" (2), `best` becomes "go".
    *   ...
    *   It hits "language" (8), `best` becomes "language".
    *   It hits "standard" (8). Since 8 is **not** strictly greater than 8 (`> len(best)`), `best` remains "language".

    **So the correct output for the longest word is `language`, not `library`.**

### Corrected Final Output:

```text
library standard big with language small a is go
longest: language
```

## 3. Debug (new conversation, model warm)

**Prompt**

````text
This Go program crashes with the panic below. Explain in 3 sentences
why, and give the smallest fix.

package main

import (
	"fmt"
	"strings"
)

// Lab 4, exercise 1: reverse the order of the words in a sentence
// and report the longest word.

func reverseWords(s string) string {
	words := strings.Fields(s)
	for i, j := 0, len(words); i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
	return strings.Join(words, " ")
}

func longest(words []string) string {
	best := ""
	for _, w := range words {
		if len(w) > len(best) {
			best = w
		}
	}
	return best
}

func main() {
	s := "go is a small language with a big standard library"
	fmt.Println(reverseWords(s))
	fmt.Println("longest:", longest(strings.Fields(s)))
}

$ go run words.go
panic: runtime error: index out of range [10] with length 10

goroutine 1 [running]:
main.reverseWords({0x4bd19e?, 0x40afe8?})
	/home/student/lab4/words.go:14 +0xc7
main.main()
	/home/student/lab4/words.go:31 +0x25
exit status 2
````

**Answer** — 113 tokens at 54.1 tok/s. First word on this prompt: 1.06 s with the model loaded and nothing cached (median of 3), 3.04 s from a cold start (runbook §2)

The panic occurs because `strings.Fields` returns an array with 10 elements (indices 0–9), but the loop condition `i < j` allows `i` to reach index 10 when `j` starts at 10 and decrements, causing an out-of-bounds access during the swap. The smallest fix is to change the initial value of `j` from `len(words)` to `len(words) - 1` so that the valid indices (0 through 9) are correctly paired without exceeding the array bounds.

