# E1 "I don't know Go" · `qwen2.5-coder:7b`

- **Date:** 2026-10-15
- **Model:** `qwen2.5-coder:7b` (ID `dae161e27b0e`), no thinking phase
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

**Answer** — first word after 0.59 s, 782 tokens at 38.1 tok/s

Let's walk through this Go program line by line and compare it with Python where applicable:

```go
package main
```
This line specifies that the package name is `main`. In Go, the `main` package is the entry point for a program.

```go
import (
	"fmt"
	"strings"
)
```
This line imports the `fmt` and `strings` packages. `fmt` is used for formatted I/O operations, and `strings` provides a variety of functions for manipulating strings.

```go
func reverseWords(s string) string {
```
This line defines a function named `reverseWords` that takes a single string `s` as an argument and returns a string.

```go
	words := strings.Fields(s)
```
This line uses the `Fields` function from the `strings` package to split the input string `s` into a slice of words. The `Fields` function splits a string into words based on whitespace.

```go
	for i, j := 0, len(words); i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
```
This line uses a `for` loop to reverse the order of the words in the slice. The loop runs from the start to the middle of the slice, swapping the elements at indices `i` and `j`. The indices `i` and `j` are incremented and decremented respectively until they meet in the middle.

```go
	return strings.Join(words, " ")
}
```
This line uses the `Join` function from the `strings` package to join the words in the slice back into a single string, with spaces between the words.

```go
func longest(words []string) string {
```
This line defines a function named `longest` that takes a slice of strings `words` as an argument and returns a string.

```go
	best := ""
```
This line initializes a variable `best` to an empty string. This variable will store the longest word found.

```go
	for _, w := range words {
		if len(w) > len(best) {
			best = w
		}
	}
```
This line uses a `for` loop to iterate over the words in the slice. For each word `w`, it checks if the length of `w` is greater than the length of `best`. If it is, `best` is updated to `w`.

```go
	return best
}
```
This line returns the longest word found.

```go
func main() {
	s := "go is a small language with a big standard library"
	fmt.Println(reverseWords(s))
	fmt.Println("longest:", longest(strings.Fields(s)))
}
```
This is the main function, which is the entry point of the program. It defines a string `s` and prints the result of calling `reverseWords(s)`. It also prints the longest word in the string by calling `longest(strings.Fields(s))`.

Comparison with Python:

```python
def reverse_words(s):
    words = s.split()
    words.reverse()
    return ' '.join(words)

def longest(words):
    best = ""
    for w in words:
        if len(w) > len(best):
            best = w
    return best

s = "go is a small language with a big standard library"
print(reverse_words(s))
print("longest:", longest(s.split()))
```

This Python program achieves the same functionality as the Go program. The `split` method in Python is equivalent to `strings.Fields` in Go, and the `reverse` method is equivalent to the loop used to reverse the slice in Go. The `join` method in Python is equivalent to `strings.Join` in Go.

## 2. Predict (same conversation)

**Prompt**

````text
What does it print?
````

**Answer** — first word after 0.13 s, 67 tokens at 47.5 tok/s

The Go program prints:

```
library standard big with a language small is go
longest: library
```

The Python program prints:

```
library standard big with a language small is go
longest: library
```

Both programs achieve the same results: the reversed sentence and the longest word in the sentence.

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

**Answer** — 378 tokens at 45.9 tok/s. First word on this prompt: 0.87 s with the model loaded and nothing cached (median of 3), 2.27 s from a cold start (runbook §2)

The Go program crashes because the `for` loop in `reverseWords` function is using the incorrect loop condition. The loop attempts to reverse the words in place, but it does not handle the case when the number of words is odd correctly. When the number of words is odd, the loop ends up with two indices that are the same, causing an out-of-range panic.

To fix the issue, the loop should only run until `i < j-1` to ensure that pairs of words are swapped correctly. Additionally, the `longest` function is called with `strings.Fields(s)` twice, which is unnecessary and can be optimized.

Here is the smallest fix for the code:

```go
package main

import (
	"fmt"
	"strings"
)

// Lab 4, exercise 1: reverse the order of the words in a sentence
// and report the longest word.

func reverseWords(s string) string {
	words := strings.Fields(s)
	for i, j := 0, len(words)-1; i < j; i, j = i+1, j-1 {
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
```

In this fix, the loop condition is changed to `i < j-1` to correctly handle the case when the number of words is odd, and the `longest` function is called with `strings.Fields(s)` only once.

