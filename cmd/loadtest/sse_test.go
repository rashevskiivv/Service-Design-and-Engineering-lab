package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func readAllEvents(t *testing.T, input string) ([]sseEvent, error) {
	t.Helper()
	sr := newSSEReader(strings.NewReader(input))
	var out []sseEvent
	for {
		ev, err := sr.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
		out = append(out, ev)
	}
}

func TestSSEReaderFraming(t *testing.T) {
	input := ": heartbeat comment\n" +
		"\n" +
		"event: message\n" +
		"data: a\n" +
		"data:b\n" + // no space after the colon
		"id: 7\n" +
		"retry: 1000\n" +
		"\n" +
		"\n" + // an extra blank line dispatches nothing
		"data:\n\n" + // empty data is skipped
		": another comment between events\n" +
		"data: {\"x\":1}\n\n" +
		"data: [DONE]" // no trailing newline at EOF
	for name, in := range map[string]string{"LF": input, "CRLF": strings.ReplaceAll(input, "\n", "\r\n")} {
		t.Run(name, func(t *testing.T) {
			got, err := readAllEvents(t, in)
			if err != nil {
				t.Fatal(err)
			}
			want := []sseEvent{
				{event: "message", data: "a\nb"},
				{data: `{"x":1}`},
				{data: "[DONE]"},
			}
			if len(got) != len(want) {
				t.Fatalf("got %d events %q, want %d", len(got), got, len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestSSEReaderEventTypeResetsAfterDispatch(t *testing.T) {
	got, err := readAllEvents(t, "event: error\ndata: {}\n\ndata: {}\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].event != "error" || got[1].event != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestSSEReaderLineTooLong(t *testing.T) {
	in := "data: " + strings.Repeat("x", maxSSELine+10) + "\n\n"
	_, err := readAllEvents(t, in)
	if !errors.Is(err, errSSELineTooLong) {
		t.Fatalf("err = %v, want errSSELineTooLong", err)
	}
}

// fakeClock and scriptedBody make timing deterministic: each Read hands out
// one scripted step and first moves the clock to that step's offset.
type fakeClock struct{ t0, now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type step struct {
	at   time.Duration
	data string
}

type scriptedBody struct {
	steps   []step
	clock   *fakeClock
	i       int
	pending []byte
	err     error // returned after the last step instead of io.EOF, if set
}

func (s *scriptedBody) Read(p []byte) (int, error) {
	if len(s.pending) == 0 {
		if s.i >= len(s.steps) {
			if s.err != nil {
				return 0, s.err
			}
			return 0, io.EOF
		}
		st := s.steps[s.i]
		s.i++
		s.clock.now = s.clock.t0.Add(st.at)
		s.pending = []byte(st.data)
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func ev(json string) string { return "data: " + json + "\n\n" }

const (
	roleChunk   = `{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
	finishChunk = `{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
)

func contentChunk(s string) string {
	return `{"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"` + s + `"},"finish_reason":null}]}`
}

// reasoningScript: role chunk at 10 ms (must not count as a token), a
// comment, reasoning at 500 ms, first content at 700 ms, 19 more content
// chunks every 50 ms (last at 1650 ms), finish, optional usage, [DONE].
func reasoningScript(withUsage bool) []step {
	steps := []step{
		{10 * time.Millisecond, ev(roleChunk)},
		{400 * time.Millisecond, ": keep-alive\n\n"},
		{500 * time.Millisecond, ev(`{"choices":[{"index":0,"delta":{"reasoning_content":"Let me think."}}]}`)},
		{700 * time.Millisecond, ev(contentChunk("Hello"))},
	}
	for k := 1; k <= 19; k++ {
		steps = append(steps, step{time.Duration(700+50*k) * time.Millisecond, ev(contentChunk(" w"))})
	}
	steps = append(steps, step{1700 * time.Millisecond, ev(finishChunk)})
	if withUsage {
		steps = append(steps, step{1710 * time.Millisecond, ev(`{"id":"c1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":50,"completion_tokens":21,"total_tokens":71}}`)})
	}
	return append(steps, step{1720 * time.Millisecond, "data: [DONE]\n\n"})
}

func consumeScript(t *testing.T, steps []step, readErr error) result {
	t.Helper()
	clk := &fakeClock{t0: time.Unix(1_790_000_000, 0)}
	clk.now = clk.t0
	res := result{Stream: true}
	err := consumeSSE(&scriptedBody{steps: steps, clock: clk, err: readErr}, clk.t0, clk.Now, &res, true)
	if readErr == nil && err != nil {
		t.Fatalf("consumeSSE: %v", err)
	}
	res.finalize()
	return res
}

func TestConsumeSSETimingAndUsage(t *testing.T) {
	res := consumeScript(t, reasoningScript(true), nil)
	if res.Class != classOK {
		t.Fatalf("class = %q (%s)", res.Class, res.Error)
	}
	if res.ttft != 500*time.Millisecond {
		t.Errorf("TTFT = %v, want 500ms (role-only chunk must not count)", res.ttft)
	}
	if res.ttfc != 700*time.Millisecond {
		t.Errorf("TTFC = %v, want 700ms (first non-empty delta.content)", res.ttfc)
	}
	if res.CompletionTokens != 21 || res.PromptTokens != 50 || res.TokensSource != tokensFromUsage {
		t.Errorf("tokens = %d/%d from %q, want 50/21 from usage", res.PromptTokens, res.CompletionTokens, res.TokensSource)
	}
	if res.ContentChunks != 20 || res.ReasoningChunks != 1 {
		t.Errorf("chunks content=%d reasoning=%d, want 20 and 1", res.ContentChunks, res.ReasoningChunks)
	}
	if res.FinishReason != "stop" {
		t.Errorf("finish_reason = %q", res.FinishReason)
	}
	if want := "Hello" + strings.Repeat(" w", 19); res.content != want {
		t.Errorf("content = %q, want %q", res.content, want)
	}
	// (21 - 1) tokens over 1650 ms - 500 ms.
	if res.DecodeTokPerSec == nil || abs(*res.DecodeTokPerSec-20/1.15) > 1e-9 {
		t.Errorf("decode tok/s = %v, want %.4f", res.DecodeTokPerSec, 20/1.15)
	}
}

func TestConsumeSSEFallsBackToChunkCount(t *testing.T) {
	res := consumeScript(t, reasoningScript(false), nil)
	if res.Class != classOK || res.TokensSource != tokensFromChunks || res.CompletionTokens != 21 {
		t.Fatalf("class=%s tokens=%d source=%s, want ok/21/chunks", res.Class, res.CompletionTokens, res.TokensSource)
	}
}

func TestConsumeSSEMultiLineDataAndCRLF(t *testing.T) {
	steps := []step{
		{5 * time.Millisecond, "data: {\"choices\":[{\"index\":0,\r\ndata: \"delta\":{\"content\":\"hi\"}}]}\r\n\r\n"},
		{6 * time.Millisecond, "data: [DONE]\r\n\r\n"},
	}
	res := consumeScript(t, steps, nil)
	if res.Class != classOK || res.content != "hi" || res.ttfc != 5*time.Millisecond {
		t.Fatalf("class=%s content=%q ttfc=%v", res.Class, res.content, res.ttfc)
	}
}

func TestConsumeSSEMidStreamError(t *testing.T) {
	cases := map[string]struct {
		event    string
		wantCode string
	}{
		"openai envelope": {ev(`{"error":{"message":"upstream stopped responding","type":"server_error","code":"upstream_timeout"}}`), "upstream_timeout"},
		"vllm top level":  {ev(`{"object":"error","message":"boom","type":"BadRequestError","param":null,"code":400}`), "400"},
		"typed event":     {"event: error\ndata: {\"message\":\"bad\"}\n\n", ""},
		"string error":    {ev(`{"error":"model unloaded"}`), ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			steps := []step{
				{10 * time.Millisecond, ev(contentChunk("partial"))},
				{20 * time.Millisecond, tc.event},
				{30 * time.Millisecond, ev(contentChunk("must not be read"))},
			}
			res := consumeScript(t, steps, nil)
			if res.Class != classStreamError || res.ErrorCode != tc.wantCode {
				t.Fatalf("class=%q code=%q, want stream_error/%q", res.Class, res.ErrorCode, tc.wantCode)
			}
			if res.content != "partial" || !res.hasTTFC {
				t.Errorf("content=%q hasTTFC=%v", res.content, res.hasTTFC)
			}
		})
	}
}

func TestConsumeSSETruncatedAndGarbage(t *testing.T) {
	res := consumeScript(t, []step{{10 * time.Millisecond, ev(contentChunk("x"))}}, nil)
	if res.Class != classTruncated {
		t.Errorf("EOF without [DONE]: class = %q, want %q", res.Class, classTruncated)
	}
	res = consumeScript(t, []step{{10 * time.Millisecond, ev(`not json`)}}, nil)
	if res.Class != classBadResponse {
		t.Errorf("garbage: class = %q, want %q", res.Class, classBadResponse)
	}
	// A read error is left for the caller to classify.
	boom := errors.New("connection reset by peer")
	clk := &fakeClock{t0: time.Unix(0, 0)}
	r2 := result{}
	err := consumeSSE(&scriptedBody{steps: []step{{time.Millisecond, ev(contentChunk("x"))}}, clock: clk, err: boom}, clk.t0, clk.Now, &r2, false)
	if !errors.Is(err, boom) || r2.Class != "" {
		t.Errorf("read error: err=%v class=%q", err, r2.Class)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
