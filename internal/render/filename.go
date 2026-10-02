package render

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Naming holds the server-controlled parts of a download filename.
type Naming struct {
	Task      string // "explain" | "review" | "tests" | "fix"; "" for chat
	Language  string // the task's language; "" for chat
	RequestID string
}

var (
	hex8Pattern  = regexp.MustCompile(`^[0-9a-f]{8}`)
	classPattern = regexp.MustCompile(`(?m)^[ \t]*public[ \t]+(?:(?:final|abstract)[ \t]+)?class[ \t]+([A-Za-z0-9_$]+)`)
	classNameOK  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

var extensions = map[string]string{"python": ".py", "java": ".java", "go": ".go", "c": ".c", "cpp": ".cpp"}

var codeTypes = map[string]string{
	"python": "text/x-python", "java": "text/x-java", "go": "text/x-go", "c": "text/x-c", "cpp": "text/x-c++",
}

// fenceLanguages maps a fence info string to a task language (chat code).
var fenceLanguages = map[string]string{
	"python": "python", "py": "python", "java": "java", "go": "go", "golang": "go",
	"c": "c", "cpp": "cpp", "c++": "cpp", "cc": "cpp",
}

// id8 is the first 8 hex characters of the request id. A client-supplied id
// that does not start with hex is hashed, so the name stays hex.
func (n Naming) id8() string {
	id := strings.ToLower(n.RequestID)
	if m := hex8Pattern.FindString(id); m != "" {
		return m
	}
	sum := sha256.Sum256([]byte(n.RequestID))
	return hex.EncodeToString(sum[:4])
}

// base is "<task>-<language>-<id8>" for tasks and "chat-<id8>" for chat.
func (n Naming) base() string {
	if n.Task == "" {
		return "chat-" + n.id8()
	}
	return n.Task + "-" + n.Language + "-" + n.id8()
}

// Filename names a full (.json), text or report (.md) download.
func (n Naming) Filename(f Format) string {
	if f == FormatFull {
		return n.base() + ".json"
	}
	return n.base() + ".md"
}

// Disposition is the Content-Disposition value that makes curl -OJ and wget
// --content-disposition save the response under name. Names are built from
// server-controlled parts only; anything else is dropped as a last guard.
func Disposition(name string) string {
	safe := strings.Map(func(r rune) rune {
		if r < 0x80 && (r == '.' || r == '-' || r == '_' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')) {
			return r
		}
		return -1
	}, name)
	return `attachment; filename="` + safe + `"`
}

// CodeOutput is the code format's response.
type CodeOutput struct {
	Body        string
	ContentType string // with charset
	Filename    string
	Blocks      int // for X-LGAI-Code-Blocks
}

// CodeFile extracts the fenced blocks of answer, joined by one blank line, and
// names the file (DECISIONS D9). With no block, the whole answer is returned
// as text/plain in a .txt file. The language is the task's, or for chat the
// first fence's info string.
func CodeFile(answer string, n Naming) CodeOutput {
	blocks := Extract(answer)
	if len(blocks) == 0 {
		return CodeOutput{Body: answer, ContentType: "text/plain; charset=utf-8", Filename: n.base() + ".txt"}
	}
	parts := make([]string, len(blocks))
	for i, b := range blocks {
		parts[i] = b.Code
	}
	out := CodeOutput{Body: strings.Join(parts, "\n"), Blocks: len(blocks)}
	lang := n.Language
	if lang == "" {
		lang = fenceLanguages[blocks[0].Info]
	}
	out.ContentType = "text/plain; charset=utf-8"
	if t, ok := codeTypes[lang]; ok {
		out.ContentType = t + "; charset=utf-8"
	}
	out.Filename = codeFilename(n, lang, out.Body)
	return out
}

func codeFilename(n Naming, lang, code string) string {
	ext, ok := extensions[lang]
	if !ok {
		return n.base() + ".txt"
	}
	class := javaClass(code)
	if n.Task == "tests" {
		switch lang {
		case "python":
			return "test_" + n.id8() + ".py"
		case "go":
			return n.id8() + "_test.go"
		case "java":
			// javac requires a public class to live in <Class>.java, so the
			// test class found in the code names the file as is.
			if class == "" {
				return "GeneratedTest.java"
			}
			return class + ".java"
		}
	}
	if lang == "java" && class != "" {
		return class + ".java"
	}
	return n.base() + ext
}

// javaClass returns the first public class name in code, if it is a safe
// identifier.
func javaClass(code string) string {
	m := classPattern.FindStringSubmatch(code)
	if m == nil || !classNameOK.MatchString(m[1]) {
		return ""
	}
	return m[1]
}
