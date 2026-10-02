// Package render implements the readable response formats of DECISIONS D9:
// Accept negotiation, code-fence extraction, download filenames and the
// Markdown report. Its functions are pure; internal/relay writes the result.
package render

import (
	"sort"
	"strconv"
	"strings"
)

// Format is a response format chosen by the Accept header.
type Format int

// The formats of DECISIONS D9.
const (
	FormatFull   Format = iota // today's OpenAI JSON / SSE
	FormatText                 // the answer text only
	FormatReport               // a Markdown report: header, input, answer, footer
	FormatCode                 // only the fenced code blocks of the answer
)

// String names the format in logs.
func (f Format) String() string {
	return [...]string{"full", "text", "report", "code"}[f]
}

// Streams reports whether the gateway always streams upstream for f.
func (f Format) Streams() bool { return f != FormatFull }

// mediaFormats maps each supported media range to its format. Wildcards pick
// the first row of the D9 table, so they keep today's behaviour.
var mediaFormats = map[string]Format{
	"*/*": FormatFull, "application/*": FormatFull, "text/*": FormatFull,
	"application/json": FormatFull, "text/event-stream": FormatFull,
	"text/plain": FormatText, "text/markdown": FormatReport, "text/x-code": FormatCode,
}

// Negotiate picks the format from the Accept header values: the supported
// media range with the highest q, the earliest one on a tie. Parameters other
// than q are ignored, and so are ranges with an invalid q. No Accept header
// means FormatFull. ok is false when only unsupported types are acceptable (406).
func Negotiate(accept []string) (f Format, ok bool) {
	type choice struct {
		format Format
		q      float64
	}
	var ranges, choices []choice
	for _, part := range strings.Split(strings.Join(accept, ","), ",") {
		media, params, _ := strings.Cut(part, ";")
		media = strings.ToLower(strings.TrimSpace(media))
		if media == "" {
			continue
		}
		q, valid := weight(params)
		if !valid {
			continue
		}
		ranges = append(ranges, choice{q: q})
		if f, known := mediaFormats[media]; known && q > 0 {
			choices = append(choices, choice{f, q})
		}
	}
	if len(ranges) == 0 {
		return FormatFull, true
	}
	if len(choices) == 0 {
		return FormatFull, false
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].q > choices[j].q })
	return choices[0].format, true
}

// weight returns the q parameter (default 1); valid is false when q is not a
// number in [0, 1].
func weight(params string) (q float64, valid bool) {
	for _, p := range strings.Split(params, ";") {
		name, value, _ := strings.Cut(p, "=")
		if !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return q, err == nil && q >= 0 && q <= 1
	}
	return 1, true
}
