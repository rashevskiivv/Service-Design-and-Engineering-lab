package render

import "strings"

// Block is one fenced code block of a Markdown answer.
type Block struct {
	Info string // first word of the info string, lower-cased ("python", "c++", "")
	Code string // the content, each line ending in "\n"
}

// Extract returns the fenced code blocks of md (DECISIONS D9). A line whose
// first non-blank characters are three or more backticks opens a block; a line
// of at least as many backticks and nothing else closes it, so shorter
// backtick runs inside the code stay code. An unterminated last block runs to
// the end. Indentation of the opening fence is removed from the content.
func Extract(md string) []Block {
	lines := strings.Split(md, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var blocks []Block
	var cur strings.Builder
	open, indent, info := 0, 0, ""
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimLeft(line, " \t")
		ticks := len(trimmed) - len(strings.TrimLeft(trimmed, "`"))
		rest := trimmed[ticks:]
		if open == 0 {
			// An info string never contains a backtick (CommonMark), which
			// keeps inline code such as ```x``` from opening a block.
			if ticks >= 3 && !strings.Contains(rest, "`") {
				open, indent, info = ticks, len(line)-len(trimmed), ""
				if words := strings.Fields(rest); len(words) > 0 {
					info = strings.ToLower(words[0])
				}
				cur.Reset()
			}
			continue
		}
		if ticks >= open && strings.TrimSpace(rest) == "" {
			blocks = append(blocks, Block{Info: info, Code: cur.String()})
			open = 0
			continue
		}
		cur.WriteString(dedent(line, indent))
		cur.WriteByte('\n')
	}
	if open > 0 {
		blocks = append(blocks, Block{Info: info, Code: cur.String()})
	}
	return blocks
}

// dedent removes up to n leading spaces.
func dedent(line string, n int) string {
	i := 0
	for i < n && i < len(line) && line[i] == ' ' {
		i++
	}
	return line[i:]
}

// Fence returns a run of backticks one longer than the longest run inside s
// (minimum 3), so the text cannot close the block early.
func Fence(s string) string {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// Fenced wraps text in a code fence with the info string.
func Fenced(text, info string) string {
	f := Fence(text)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return f + info + "\n" + text + f + "\n"
}
