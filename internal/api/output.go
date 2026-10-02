package api

import (
	"net/http"
	"strconv"
	"strings"

	"local-generative-ai/internal/oai"
	"local-generative-ai/internal/relay"
	"local-generative-ai/internal/render"
	"local-generative-ai/internal/tasks"
)

// output says how a generation answer goes back to the client (DECISIONS D9).
type output struct {
	format     render.Format
	stream     bool // the client asked for a stream
	wantsUsage bool // full format: forward the usage-only chunk
	naming     render.Naming
	report     render.Report // report format only
}

// negotiate picks the response format from the Accept header; only
// unsupported types give 406. It is a refusal like any invalid request.
func (c *call) negotiate() (render.Format, bool) {
	f, ok := render.Negotiate(c.r.Header.Values("Accept"))
	if !ok {
		c.refuse(oai.NewError(http.StatusNotAcceptable, oai.TypeInvalidRequest, oai.CodeNotAcceptable, "",
			"Accept must allow application/json, text/event-stream, text/plain, text/markdown or text/x-code"))
		return f, false
	}
	c.format = f.String()
	return f, true
}

// sink builds the relay sink that writes o's format from the upstream stream.
func (c *call) sink(o output) relay.Sink {
	switch o.format {
	case render.FormatFull:
		return relay.SSE{WantsUsage: o.wantsUsage}
	case render.FormatCode:
		return &relay.Buffered{Render: func(answer string, _ relay.Outcome) relay.Response {
			code := render.CodeFile(answer, o.naming)
			h := http.Header{}
			h.Set("Content-Disposition", render.Disposition(code.Filename))
			h.Set("X-LGAI-Code-Blocks", strconv.Itoa(code.Blocks))
			return relay.Response{ContentType: code.ContentType, Header: h, Body: code.Body}
		}}
	}
	ctype, head := "text/plain; charset=utf-8", ""
	var foot func(relay.Outcome) string
	if o.format == render.FormatReport {
		ctype, head = "text/markdown; charset=utf-8", o.report.Head()
		foot = func(out relay.Outcome) string {
			return render.Foot(out.Usage.PromptTokens, out.Usage.CompletionTokens, out.UsageEstimated, c.s.now().Sub(c.start))
		}
	}
	disposition := render.Disposition(o.naming.Filename(o.format))
	if o.stream {
		return &relay.Live{ContentType: ctype, Disposition: disposition, Head: head, Foot: foot}
	}
	return &relay.Buffered{Render: func(answer string, out relay.Outcome) relay.Response {
		body := head + answer
		if !strings.HasSuffix(answer, "\n") {
			body += "\n"
		}
		if foot != nil {
			body += foot(out)
		}
		h := http.Header{}
		h.Set("Content-Disposition", disposition)
		return relay.Response{ContentType: ctype, Header: h, Body: body}
	}}
}

// taskReport is the report header of a task request: its fields, fenced.
func (c *call) taskReport(k tasks.Kind, req tasks.Request, alias, upstreamID string) render.Report {
	r := render.Report{
		Title:    strings.ToUpper(string(k[:1])) + string(k[1:]) + " (" + req.Language + ")",
		Endpoint: "POST /v1/tasks/" + string(k), Language: req.Language,
		Model: alias, Upstream: upstreamID, Time: c.start,
		Inputs: []render.Input{{Heading: "Code", Text: req.Code, Info: req.Language}},
	}
	for _, in := range []render.Input{
		{Heading: "Error output", Text: req.Error, Info: "text"},
		{Heading: "Instructions", Text: req.Instructions, Info: "text"},
		{Heading: "Test framework", Text: req.Framework, Info: "text"},
	} {
		if in.Text != "" {
			r.Inputs = append(r.Inputs, in)
		}
	}
	return r
}

// chatReport is the report header of a chat request: every message, fenced.
func (c *call) chatReport(n oai.Normalized) render.Report {
	r := render.Report{Title: "Chat", Endpoint: "POST /v1/chat/completions",
		Model: n.PublicModel, Upstream: n.Request.Model, Time: c.start}
	for _, m := range n.Request.Messages {
		text := "(no text)"
		if m.Content != nil {
			text = *m.Content
		}
		r.Inputs = append(r.Inputs, render.Input{Heading: m.Role, Text: text, Info: "text"})
	}
	return r
}
