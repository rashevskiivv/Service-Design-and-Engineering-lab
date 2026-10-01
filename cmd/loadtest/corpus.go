package main

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

// corpusFS holds corpus/<lang>/<size>.code.txt and the matching
// <size>.error.txt (compiler or runtime output, sent as "error" to the fix
// task). The files use a .txt extension so the go tool never treats the Go,
// C or C++ snippets as sources of this module.
//
//go:embed corpus
var corpusFS embed.FS

type snippet struct {
	lang    string
	size    string
	code    string
	errText string
}

// corpus maps language -> size -> snippet.
type corpus map[string]map[string]snippet

func loadCorpus() (corpus, error) {
	c := make(corpus, len(languages))
	for _, lang := range languages {
		c[lang] = make(map[string]snippet, len(sizes))
		for _, size := range sizes {
			code, err := corpusFS.ReadFile(path.Join("corpus", lang, size+".code.txt"))
			if err != nil {
				return nil, fmt.Errorf("corpus: %w", err)
			}
			errText, err := corpusFS.ReadFile(path.Join("corpus", lang, size+".error.txt"))
			if err != nil {
				return nil, fmt.Errorf("corpus: %w", err)
			}
			if strings.TrimSpace(string(code)) == "" || strings.TrimSpace(string(errText)) == "" {
				return nil, fmt.Errorf("corpus: %s/%s is empty", lang, size)
			}
			c[lang][size] = snippet{lang: lang, size: size, code: string(code), errText: string(errText)}
		}
	}
	return c, nil
}

// langDisplay returns the human name and the Markdown fence tag of a language.
func langDisplay(lang string) (display, fence string) {
	switch lang {
	case "python":
		return "Python", "python"
	case "java":
		return "Java", "java"
	case "go":
		return "Go", "go"
	case "c":
		return "C", "c"
	case "cpp":
		return "C++", "cpp"
	}
	return lang, ""
}

// fenceFor returns a backtick fence longer than any backtick run in s
// (minimum three), so s cannot close the fenced block early.
func fenceFor(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// chatPrompt is the user message sent to /v1/chat/completions: roughly what
// a student pastes into a chat when asking about an exercise.
func chatPrompt(sn snippet) string {
	display, tag := langDisplay(sn.lang)
	fence := fenceFor(sn.code)
	code := sn.code
	if !strings.HasSuffix(code, "\n") {
		code += "\n"
	}
	return fmt.Sprintf("This is my %s code for a lab exercise. Explain briefly what it does and point out any bugs.\n\n%s%s\n%s%s\n",
		display, fence, tag, code, fence)
}

const chatSystemPrompt = "You are a helpful coding assistant for university programming labs."
