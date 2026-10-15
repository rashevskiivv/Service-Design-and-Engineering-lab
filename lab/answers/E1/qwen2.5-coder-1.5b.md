# E1 "I don't know Go" · `qwen2.5-coder:1.5b`

- **Date:** 2026-10-15
- **Model:** `qwen2.5-coder:1.5b` (ID `d7372fd82851`), no thinking phase
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

**Answer** — first word after 0.13 s, 741 tokens at 150.7 tok/s

Sure, I'll explain each part of this Go program line by line:

### Package Declaration
```go
package main
```
This line specifies that the program is in the `main` package. In Go, packages are the basic unit of code organization. Each package can contain functions, variables, and constants that are related.

### Import Statements
```go
import (
	"fmt"
	"strings"
)
```
These lines import the `fmt` package for standard output and `strings` package for string manipulation functions.

### Function `reverseWords`
```go
func reverseWords(s string) string {
	words := strings.Fields(s)
	for i, j := 0, len(words); i < j; i, j = i+1, j-1 {
		words[i], words[j] = words[j], words[i]
	}
	return strings.Join(words, " ")
}
```
- **`strings.Fields(s)`**: This function splits the input string `s` into a slice of words, using spaces as delimiters.
- **`for i, j := 0, len(words); i < j; i, j = i+1, j-1`**: This loop reverses the slice of words using two pointers, `i` starting from the beginning and `j` starting from the end. It swaps the elements at these pointers and moves them towards the center.
- **`strings.Join(words, " ")`**: This function joins the reversed slice of words back into a single string, using spaces as the delimiter.

### Function `longest`
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
- **`words []string`**: This function takes a slice of words as input.
- **`for _, w := range words`**: This loop iterates over each word in the slice.
- **`if len(w) > len(best)`**: This condition checks if the length of the current word `w` is greater than the length of the current longest word `best`.
- **`best = w`**: If the condition is true, it updates `best` to the current word `w`.
- **`return best`**: Finally, it returns the longest word found.

### Function `main`
```go
func main() {
	s := "go is a small language with a big standard library"
	fmt.Println(reverseWords(s))
	fmt.Println("longest:", longest(strings.Fields(s)))
}
```
- **`s := "go is a small language with a big standard library"`**: This line assigns the input string `s` to the variable `s`.
- **`fmt.Println(reverseWords(s))`**: This line calls the `reverseWords` function with the input string `s` and prints the result.
- **`fmt.Println("longest:", longest(strings.Fields(s)))`**: This line calls the `longest` function with the input string `s` (after splitting it into words) and prints the result.

### Summary
- The Go program takes a string as input, reverses the order of the words, and finds the longest word.
- It uses the `strings.Fields` function to split the input string into words, the `reverseWords` function to reverse the order of the words, and the `longest` function to find the longest word in the reversed list of words.
- The `main` function demonstrates how to use these functions with a sample input string.

## 2. Predict (same conversation)

**Prompt**

````text
What does it print?
````

**Answer** — first word after 0.04 s, 283 tokens at 149.5 tok/s

When you run this Go program, it will print the following:

```
gnio si a laegelnoy aig a big stdardlib
longest: big
```

Here's a breakdown of how the program works:

1. **Input**: The input string is `"go is a small language with a big standard library"`.
2. **Splitting the String**: The `strings.Fields` function splits the input string into a slice of words, resulting in `["go", "is", "a", "small", "language", "with", "a", "big", "standardlib"]`.
3. **Reversing the List**: The `reverseWords` function takes the slice of words and reverses it using a two-pointer approach, resulting in `["big", "standardlib", "with", "a", "language", "small", "is", "go"]`.
4. **Finding the Longest Word**: The `longest` function takes the reversed slice of words and finds the longest word, resulting in `"big"`.
5. **Printing the Results**: The program prints the reversed list of words and the longest word, which are `"gnio si a laegelnoy aig a big stdardlib"` and `"big"` respectively.

This program demonstrates how to use Go's standard library functions to manipulate strings and arrays.

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

**Answer** — 222 tokens at 151.6 tok/s. First word on this prompt: 0.18 s with the model loaded and nothing cached (median of 3), 1.38 s from a cold start (runbook §2)

The program crashes because it tries to access an index that is out of range in the `reverseWords` function. The `fields` function splits the string into words, but it does not handle the case where the string is empty or contains only one word. In the `reverseWords` function, when the length of the words slice is 1, it attempts to access `words[1]`, which is out of bounds. To fix this issue, we can add a check to see if the length of the words slice is greater than 0 before accessing the second element. Additionally, we can add a check to ensure that the words slice contains at least one word by returning a default value if it does not. The smallest fix would be to add the following checks:

```go
if len(words) > 0 && len(words[1:]) > 0 {
	words[0], words[1] = words[1], words[0]
}
if len(words) > 0 {
	longestWord := longest(words)
	fmt.Println("longest:", longestWord)
}
```

