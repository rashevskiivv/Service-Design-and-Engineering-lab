package main

import (
	"strings"
	"testing"
)

// Rough token estimate (bytes / 3.5) must fall inside each size class:
// small is one short exercise, medium a full lab file. The fix input
// (code + error) must fit LGAI_TASK_MAX_INPUT_BYTES (16384).
func TestCorpusComplete(t *testing.T) {
	c, err := loadCorpus()
	if err != nil {
		t.Fatal(err)
	}
	ranges := map[string][2]float64{"small": {120, 450}, "medium": {500, 1300}}
	for _, lang := range languages {
		for _, size := range sizes {
			sn, ok := c[lang][size]
			if !ok {
				t.Fatalf("missing %s/%s", lang, size)
			}
			est := float64(len(sn.code)) / 3.5
			if r := ranges[size]; est < r[0] || est > r[1] {
				t.Errorf("%s/%s: ~%.0f tokens, outside %v", lang, size, est, r)
			}
			if n := len(sn.code) + len(sn.errText); n > 16384 {
				t.Errorf("%s/%s: fix input %d bytes > 16384", lang, size, n)
			}
			if strings.Contains(sn.code, "```") {
				t.Errorf("%s/%s: code contains a Markdown fence", lang, size)
			}
		}
	}
}

func TestChatPromptFencesCode(t *testing.T) {
	sn := snippet{lang: "cpp", code: "int main() { return 0; }"}
	p := chatPrompt(sn)
	if !strings.Contains(p, "C++ code") || !strings.Contains(p, "```cpp\nint main() { return 0; }\n```\n") {
		t.Errorf("prompt = %q", p)
	}
	if got := fenceFor("a ```` b"); got != "`````" {
		t.Errorf("fenceFor = %q, want 5 backticks", got)
	}
}
