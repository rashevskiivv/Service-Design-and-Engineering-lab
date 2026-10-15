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
