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

// fenceLanguages maps a fence info string to a task language.
var fenceLanguages = map[string]string{
	"python": "python", "py": "python", "python3": "python", "py3": "python",
	"java": "java", "go": "go", "golang": "go",
	"c": "c", "cpp": "cpp", "c++": "cpp", "cc": "cpp", "cxx": "cpp", "hpp": "cpp",
}

// windowsDevices are file names Windows treats as devices whatever the
// extension; curl -OJ there would write to the device.
var windowsDevices = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

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
	Blocks      int    // for X-LGAI-Code-Blocks: the blocks kept
	Language    string // the file's language; "" for a .txt
}

// CodeFile extracts the fenced blocks of answer that are in the target
// language, joined by one blank line, and names the file (DECISIONS D9). The
// target is the task's language, or for chat the first block with a known
// language. Untagged blocks are kept too; if no block matches, all are kept,
// so a run command or a build snippet in another language stays out of the
// file. With no block at all, the whole answer is returned as text/plain in a
// .txt file.
func CodeFile(answer string, n Naming) CodeOutput {
	blocks := Extract(answer)
	if len(blocks) == 0 {
		return CodeOutput{Body: answer, ContentType: "text/plain; charset=utf-8", Filename: n.base() + ".txt"}
	}
	lang := n.Language
	if lang == "" {
		for _, b := range blocks {
			if l := fenceLanguages[b.Info]; l != "" {
				lang = l
				break
			}
		}
	}
	var parts []string
	for _, b := range blocks {
		if b.Info == "" || (lang != "" && fenceLanguages[b.Info] == lang) {
			parts = append(parts, b.Code)
		}
	}
	if len(parts) == 0 {
		for _, b := range blocks {
			parts = append(parts, b.Code)
		}
	}
	out := CodeOutput{Body: strings.Join(parts, "\n"), Blocks: len(parts)}
	if _, known := extensions[lang]; known {
		out.Language = lang
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
	if m == nil || !classNameOK.MatchString(m[1]) || windowsDevices.MatchString(m[1]) {
		return ""
	}
	return m[1]
}

// TruncatedNote is the line appended to a code file whose answer was cut off,
// as a comment in the file's language so the compiler or test error points at
// the cause.
func TruncatedNote(lang string) string {
	const note = "[truncated: the answer reached max_tokens, so this file is incomplete]\n"
	switch lang {
	case "python":
		return "# " + note
	case "java", "go", "c", "cpp":
		return "// " + note
	}
	return note
}
