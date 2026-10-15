package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Lab 7: count word frequencies in several files concurrently,
// one goroutine per file, then print the most frequent words.
//
// Usage: wordfreq [-n 10] [-min 3] [-stop] FILE...

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true,
	"of": true, "to": true, "in": true, "is": true, "it": true,
}

type wordCount struct {
	word  string
	count int
}

type options struct {
	top      int
	minLen   int
	skipStop bool
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r)
	}))
}

func countFile(path string, opts options, counts map[string]int, wg *sync.WaitGroup) {
	defer wg.Done()
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skip:", err)
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Split(bufio.ScanWords)
	for sc.Scan() {
		w := normalize(sc.Text())
		if len(w) < opts.minLen || (opts.skipStop && stopwords[w]) {
			continue
		}
		counts[w]++
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
	}
}

func topN(counts map[string]int, n int) []wordCount {
	list := make([]wordCount, 0, len(counts))
	for w, c := range counts {
		list = append(list, wordCount{w, c})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].word < list[j].word
	})
	if len(list) > n {
		list = list[:n]
	}
	return list
}

func main() {
	var opts options
	flag.IntVar(&opts.top, "n", 10, "number of words to print")
	flag.IntVar(&opts.minLen, "min", 1, "ignore words shorter than this")
	flag.BoolVar(&opts.skipStop, "stop", false, "skip common English stopwords")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: wordfreq [-n 10] [-min 3] [-stop] FILE...")
		os.Exit(1)
	}

	counts := make(map[string]int)
	var wg sync.WaitGroup
	for _, path := range flag.Args() {
		wg.Add(1)
		go countFile(path, opts, counts, &wg)
	}
	wg.Wait()

	total := 0
	for _, c := range counts {
		total += c
	}
	fmt.Printf("%d distinct words, %d in total\n", len(counts), total)
	for i, wc := range topN(counts, opts.top) {
		fmt.Printf("%2d. %-15s %6d  %5.2f%%\n", i+1, wc.word, wc.count, 100*float64(wc.count)/float64(total))
	}
}
