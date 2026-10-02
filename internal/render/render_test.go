package render

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNegotiate(t *testing.T) {
	tests := []struct {
		accept []string
		want   Format
		ok     bool
	}{
		{nil, FormatFull, true},
		{[]string{""}, FormatFull, true},
		{[]string{"*/*"}, FormatFull, true},
		{[]string{"application/json"}, FormatFull, true}, // the openai SDK
		{[]string{"text/event-stream"}, FormatFull, true},
		{[]string{"text/plain"}, FormatText, true},
		{[]string{"TEXT/Plain; charset=utf-8"}, FormatText, true},
		{[]string{"text/markdown"}, FormatReport, true},
		{[]string{"text/x-code"}, FormatCode, true},
		{[]string{"text/plain;q=0.5, text/markdown;q=0.9"}, FormatReport, true},
		{[]string{"text/plain, text/markdown"}, FormatText, true}, // tie: the earliest
		{[]string{"text/x-code;q=0.2", "*/*;q=0.1"}, FormatCode, true},
		{[]string{"text/plain;q=0, */*"}, FormatFull, true},
		{[]string{"text/html,application/xhtml+xml,*/*;q=0.8"}, FormatFull, true}, // a browser
		{[]string{"text/*"}, FormatFull, true},
		{[]string{"text/plain;q=abc, text/markdown"}, FormatReport, true}, // invalid q ignored
		{[]string{"text/plain;q=2"}, FormatFull, true},                    // only an invalid range: as if absent
		{[]string{"image/png"}, FormatFull, false},
		{[]string{"image/png, text/html;q=0.5"}, FormatFull, false},
		{[]string{"text/plain;q=0"}, FormatFull, false},
	}
	for _, tc := range tests {
		f, ok := Negotiate(tc.accept)
		if f != tc.want || ok != tc.ok {
			t.Errorf("Negotiate(%q) = %v, %v; want %v, %v", tc.accept, f, ok, tc.want, tc.ok)
		}
	}
	if FormatFull.String() != "full" || FormatCode.String() != "code" || FormatFull.Streams() || !FormatReport.Streams() {
		t.Error("Format methods")
	}
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want []Block
	}{
		{"none", "Just prose.\nNo code.", nil},
		{"one", "Fix:\n```python\nprint(1)\n```\nDone.", []Block{{"python", "print(1)\n"}}},
		{"info string words", "``` Go title=x\nfunc F() {}\n```\n", []Block{{"go", "func F() {}\n"}}},
		{"several", "```c\nint a;\n```\ntext\n```\nraw\n```\n", []Block{{"c", "int a;\n"}, {"", "raw\n"}}},
		{"unterminated", "```java\nclass A {}\n", []Block{{"java", "class A {}\n"}}},
		{"nested backticks", "````md\n```python\nx\n```\n````\n", []Block{{"md", "```python\nx\n```\n"}}},
		{"short run inside", "```py\ns = '``'\n```\n", []Block{{"py", "s = '``'\n"}}},
		{"closing needs no text", "```py\n```not a close\nx\n```\n", []Block{{"py", "```not a close\nx\n"}}},
		{"inline is not a fence", "use ```x``` here\n", nil},
		{"crlf", "```c\r\nint a;\r\n```\r\n", []Block{{"c", "int a;\n"}}},
		{"indented fence", "1. Step:\n   ```cpp\n   int x;\n     y;\n   ```\n", []Block{{"cpp", "int x;\n  y;\n"}}},
		{"empty block", "```\n```\n", []Block{{"", ""}}},
	}
	for _, tc := range tests {
		if got := Extract(tc.md); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, got, tc.want)
		}
	}
}

func TestFence(t *testing.T) {
	for in, want := range map[string]string{"x": "```", "a ``` b": "````", "``````": "```````", "`": "```"} {
		if got := Fence(in); got != want {
			t.Errorf("Fence(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Fenced("a ``` b", "text"); got != "````text\na ``` b\n````\n" {
		t.Errorf("Fenced = %q", got)
	}
}

func TestCodeOutput(t *testing.T) {
	const id = "1a2b3c4d5e6f7a8b"
	tests := []struct {
		name, task, lang, answer string
		file, ctype, body        string
		blocks                   int
	}{
		{"no block", "fix", "python", "No code here.", "fix-python-1a2b3c4d.txt", "text/plain", "No code here.", 0},
		{"one block", "fix", "python", "x\n```python\nprint(1)\n```\ny", "fix-python-1a2b3c4d.py", "text/x-python", "print(1)\n", 1},
		{"several blocks", "review", "c", "```c\nint a;\n```\n```c\nint b;\n```", "review-c-1a2b3c4d.c", "text/x-c", "int a;\n\nint b;\n", 2},
		{"java public class", "fix", "java", "```java\npublic final class Calc {\n}\n```", "Calc.java", "text/x-java", "public final class Calc {\n}\n", 1},
		{"java no class", "explain", "java", "```java\nint x;\n```", "explain-java-1a2b3c4d.java", "text/x-java", "int x;\n", 1},
		{"java bad class", "fix", "java", "```java\npublic class A$B {}\n```", "fix-java-1a2b3c4d.java", "text/x-java", "public class A$B {}\n", 1},
		{"tests python", "tests", "python", "```python\ndef test_x(): pass\n```", "test_1a2b3c4d.py", "text/x-python", "def test_x(): pass\n", 1},
		{"tests go", "tests", "go", "```go\nfunc TestX(t *testing.T) {}\n```", "1a2b3c4d_test.go", "text/x-go", "func TestX(t *testing.T) {}\n", 1},
		{"tests java class", "tests", "java", "```java\npublic class CalcTest {}\n```", "CalcTest.java", "text/x-java", "public class CalcTest {}\n", 1},
		{"tests java no class", "tests", "java", "```java\nclass CalcTest {}\n```", "GeneratedTest.java", "text/x-java", "class CalcTest {}\n", 1},
		{"tests cpp", "tests", "cpp", "```cpp\nTEST(A, B) {}\n```", "tests-cpp-1a2b3c4d.cpp", "text/x-c++", "TEST(A, B) {}\n", 1},
		{"chat python", "", "", "```py\nx = 1\n```", "chat-1a2b3c4d.py", "text/x-python", "x = 1\n", 1},
		{"chat c++", "", "", "```c++\nint x;\n```", "chat-1a2b3c4d.cpp", "text/x-c++", "int x;\n", 1},
		{"chat unknown", "", "", "```bash\nls\n```", "chat-1a2b3c4d.txt", "text/plain", "ls\n", 1},
		{"chat java class", "", "", "```java\npublic abstract class Shape {}\n```", "Shape.java", "text/x-java", "public abstract class Shape {}\n", 1},
	}
	for _, tc := range tests {
		out := CodeFile(tc.answer, Naming{Task: tc.task, Language: tc.lang, RequestID: id})
		want := CodeOutput{Body: tc.body, ContentType: tc.ctype + "; charset=utf-8", Filename: tc.file, Blocks: tc.blocks}
		if out != want {
			t.Errorf("%s:\n got %#v\nwant %#v", tc.name, out, want)
		}
	}
}

func TestFilenames(t *testing.T) {
	n := Naming{Task: "review", Language: "go", RequestID: "ABCDEF0123456789"}
	if n.Filename(FormatFull) != "review-go-abcdef01.json" || n.Filename(FormatText) != "review-go-abcdef01.md" || n.Filename(FormatReport) != "review-go-abcdef01.md" {
		t.Errorf("task names: %s %s", n.Filename(FormatFull), n.Filename(FormatText))
	}
	// A client request id that is not hex is hashed; the name stays safe.
	c := Naming{RequestID: `../"evil` + "\u00e9"}
	if name := c.Filename(FormatText); !regexp.MustCompile(`^chat-[0-9a-f]{8}\.md$`).MatchString(name) {
		t.Errorf("chat name %q", name)
	}
	if got := Disposition(`a"b/c\é.md`); got != `attachment; filename="abc.md"` {
		t.Errorf("Disposition = %s", got)
	}
}

func TestReport(t *testing.T) {
	r := Report{
		Title: "Fix (python)", Endpoint: "POST /v1/tasks/fix", Language: "python", Model: "coder", Upstream: "gpt-oss:20b",
		Time:   time.Date(2026, 10, 14, 13, 5, 0, 0, time.FixedZone("CEST", 7200)),
		Inputs: []Input{{"Code", "print(```)", "python"}, {"Error output", "SyntaxError\n", "text"}},
	}
	want := "# Fix (python)\n\n- Endpoint: `POST /v1/tasks/fix`\n- Language: python\n- Model: coder (`gpt-oss:20b`)\n" +
		"- Time: 2026-10-14 11:05:00 UTC\n\n## Input\n\n### Code\n\n````python\nprint(```)\n````\n\n" +
		"### Error output\n\n```text\nSyntaxError\n```\n\n## Answer\n\n"
	if got := r.Head(); got != want {
		t.Errorf("Head:\n%s\nwant:\n%s", got, want)
	}
	if got := Foot(11, 2, true, 1500*time.Millisecond); got != "\n---\n\nTokens: 11 prompt + 2 completion = 13 (estimated). Duration: 1.5 s.\n" {
		t.Errorf("Foot = %q", got)
	}
	chat := Report{Title: "Chat", Endpoint: "POST /v1/chat/completions", Model: "coder", Upstream: "m"}
	if strings.Contains(chat.Head(), "Language") {
		t.Error("chat report shows a language")
	}
}
