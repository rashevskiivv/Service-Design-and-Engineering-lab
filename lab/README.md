# Lab material

Code for the guided examples, the example bank and the exercises of LAB_07. How they are used: [docs/lab-plan.md](../docs/lab-plan.md). Commands: [cheat-sheet.md](cheat-sheet.md).

Every program is a small "student lab" with **one real bug**, and next to it the exact compiler or runtime output a student would see (`*.error.txt`). Paste both into your local model, then run the fix yourself.

| File | Program | What goes wrong | Used in |
|---|---|---|---|
| [`code/go/words/words.go`](code/go/words/words.go) | Reverse the words of a sentence, find the longest | `panic: index out of range` | E1, X1 |
| [`code/go/wordfreq/wordfreq.go`](code/go/wordfreq/wordfreq.go) | Word frequencies over several files with goroutines | `fatal error: concurrent map writes` | B6 |
| [`code/python/words.py`](code/python/words.py) | Most frequent words in a text | `KeyError` | X1, B1 |
| [`code/python/gradebook.py`](code/python/gradebook.py) | Gradebook from CSV text with a report | `ZeroDivisionError` for a student with no grades | X2, B9 |
| [`code/java/Temperatures.java`](code/java/Temperatures.java) | Statistics over a week of temperatures | `ArrayIndexOutOfBoundsException` (off by one) | X1, B5 |
| [`code/java/Library.java`](code/java/Library.java) | Small library loan system | Compile error: lossy conversion from `double` to `int` | X2, B4 |
| [`code/c/names.c`](code/c/names.c) | Print a name as "Last, First", count vowels | Stack buffer overflow in `strcat` (AddressSanitizer) | X2, B2 |
| [`code/c/students.c`](code/c/students.c) | Sorted linked list of student records | Use after free in `free_list` (gcc warning + ASan) | B7 |
| [`code/cpp/stack.cpp`](code/cpp/stack.cpp) | Bounded stack of integers | Compile error: `const` object calls a non-`const` method | B3 |
| [`code/cpp/matrix.cpp`](code/cpp/matrix.cpp) | Dense matrix with `+` and `*` | `double free` at exit (rule of three) | B8 |

Run them:

```sh
python3 code/python/words.py
cd code/go/words && go run words.go
javac code/java/Temperatures.java && java -cp code/java Temperatures
gcc -g -Wall -fsanitize=address -o names code/c/names.c && ./names
g++ -std=c++17 -Wall -g -o matrix code/cpp/matrix.cpp && ./matrix
```

The Go programs live in their own module (`lab/go.mod`), so they never become part of the gateway's `go build ./...` at the repo root.

These snippets were written as the load-test corpus of the old cloud plan (`cmd/loadtest/corpus/`, see git tag `cloud-final`) and moved here on 2026-10-15.
