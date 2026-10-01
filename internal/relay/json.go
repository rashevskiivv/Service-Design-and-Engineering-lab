package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"local-generative-ai/internal/oai"
)

// JSON relays a non-streaming upstream response. The body (≤ 8 MiB) must be a
// chat completion: a JSON object with a non-empty choices array and no error.
// Anything else (an HTML page from a proxy, an error object sent with a 200)
// is a 502 upstream_error with a generic message, as on the streaming path
// (review m1). A valid body is copied verbatim as application/json. Usage
// comes from the body, or is estimated from the answer length when the
// upstream sent none.
func JSON(ctx context.Context, w http.ResponseWriter, resp *http.Response, o Options) Outcome {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONBytes+1))
	if err != nil || len(body) > maxJSONBytes {
		status, code := failure(ctx)
		if status == oai.StatusClientClosed {
			return Outcome{Status: status}
		}
		reason := "read error"
		if err == nil {
			reason = "body larger than 8 MiB"
		}
		return badResponse(w, status, code, reason)
	}

	var peek struct {
		Usage   *oai.Usage      `json:"usage"`
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	reason := ""
	switch {
	case json.Unmarshal(body, &peek) != nil:
		reason = "body is not a JSON chat completion"
	case len(peek.Error) > 0 && string(peek.Error) != "null":
		reason = "body is an error object"
	case len(peek.Choices) == 0:
		reason = "body has no choices"
	}
	if reason != "" {
		mediaType, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
		if len(mediaType) > 64 {
			mediaType = mediaType[:64]
		}
		return badResponse(w, http.StatusBadGateway, oai.CodeUpstreamError, reason+" (content-type "+mediaType+")")
	}

	out := Outcome{Status: http.StatusOK}
	if peek.Usage != nil {
		out.Usage = *peek.Usage
	} else {
		completion := 0
		for _, c := range peek.Choices {
			if c.Message.Content != nil {
				completion += len(*c.Message.Content)
			}
		}
		out.Usage = oai.Usage{PromptTokens: estimate(o.PromptBytes), CompletionTokens: estimate(completion)}
		out.UsageEstimated = true
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens + out.Usage.CompletionTokens

	w.Header().Set("Content-Type", "application/json")
	cw := newWriter(w, o)
	defer cw.clearDeadline()
	w.WriteHeader(http.StatusOK)
	if err := cw.write(body); err != nil {
		out.Status = oai.StatusClientClosed
	}
	return out
}

// badResponse sends the generic error for an unusable upstream answer.
func badResponse(w http.ResponseWriter, status int, code, reason string) Outcome {
	oai.WriteError(w, oai.NewError(status, oai.TypeServer, code, "", "the model server sent an invalid response; retry the request"))
	return Outcome{Status: status, ErrCode: code, Reason: reason}
}
