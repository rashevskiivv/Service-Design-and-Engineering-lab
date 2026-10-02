package render

import (
	"fmt"
	"strings"
	"time"
)

// Report describes the Markdown report format (DECISIONS D9): Head is written
// before the answer, Foot after it.
type Report struct {
	Title    string // e.g. "Review (python)" or "Chat"
	Endpoint string // e.g. "POST /v1/tasks/review"
	Language string // "" for chat
	Model    string // public alias
	Upstream string // upstream model id
	Time     time.Time
	Inputs   []Input
}

// Input is one fenced section of the request: code, error output, a chat
// message, …
type Input struct {
	Heading string
	Text    string
	Info    string // fence info string, e.g. "python" or "text"
}

// Head renders everything before the answer.
func (r Report) Head() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n- Endpoint: `%s`\n", r.Title, r.Endpoint)
	if r.Language != "" {
		fmt.Fprintf(&b, "- Language: %s\n", r.Language)
	}
	fmt.Fprintf(&b, "- Model: %s (`%s`)\n- Time: %s\n\n## Input\n", r.Model, r.Upstream,
		r.Time.UTC().Format("2006-01-02 15:04:05 UTC"))
	for _, in := range r.Inputs {
		fmt.Fprintf(&b, "\n### %s\n\n%s", in.Heading, Fenced(in.Text, in.Info))
	}
	b.WriteString("\n## Answer\n\n")
	return b.String()
}

// Foot renders the footer after the answer: tokens and duration.
func Foot(prompt, completion int, estimated bool, d time.Duration) string {
	est := ""
	if estimated {
		est = " (estimated)"
	}
	return fmt.Sprintf("\n---\n\nTokens: %d prompt + %d completion = %d%s. Duration: %.1f s.\n",
		prompt, completion, prompt+completion, est, d.Seconds())
}
