package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

var errGoogleStreamReady = errors.New("first generation event ready")
var errGooglePromptBlocked = errors.New("Google blocked prompt")
var errGoogleStreamIncomplete = errors.New("Google stream incomplete before delivery")

type capturedGoogleBody struct {
	io.Reader
	data bytes.Buffer
}

func (c *capturedGoogleBody) Read(p []byte) (int, error) {
	n, e := c.Reader.Read(p)
	c.data.Write(p[:n])
	return n, e
}

type prefixedGoogleBody struct {
	io.Reader
	io.Closer
}

// Inspect the first real output before delivering SSE. With aggregation enabled
// validate the entire generation before delivery; a failed attempt stays hidden.
// This never restarts an upstream after its content has reached a client.
func prepareGoogleStream(ctx context.Context, resp *http.Response, aggregate bool, expected int) (bool, error) {
	source := resp.Body
	captured := &capturedGoogleBody{Reader: io.LimitReader(source, maxBody+1)}
	finishes := map[int]bool{}
	retryable := true
	err := readSSE(ctx, captured, func(raw []byte) error {
		var frame geminiResult
		if json.Unmarshal(raw, &frame) != nil {
			retryable = false
			return errors.New("invalid Google event")
		}
		var feedback struct {
			Reason string `json:"blockReason"`
		}
		json.Unmarshal(frame.Feedback, &feedback)
		if feedback.Reason != "" && feedback.Reason != "BLOCKED_REASON_UNSPECIFIED" {
			return errGooglePromptBlocked
		}
		if len(frame.Error) > 0 && string(frame.Error) != "null" {
			var failure struct {
				Code   int    `json:"code"`
				Status string `json:"status"`
			}
			json.Unmarshal(frame.Error, &failure)
			retryable = failure.Code == 429 || failure.Code == 502 || failure.Code == 503 || failure.Status == "RESOURCE_EXHAUSTED" || failure.Status == "UNAVAILABLE"
			if resp.Request != nil {
				recordProviderErrorBody(resp.Request, raw)
			}
			return errors.New("Google rejected generation")
		}
		for _, c := range frame.Candidates {
			if c.Index < 0 || c.Index >= expected {
				retryable = false
				return errors.New("invalid Google candidate")
			}
			if c.Finish != "" && c.Finish != "FINISH_REASON_UNSPECIFIED" {
				finishes[c.Index] = true
			}
			meaningful := finishes[c.Index]
			for _, p := range c.Content.Parts {
				meaningful = meaningful || p.Text != "" || p.Call != nil || p.InlineData != nil || p.ThoughtSignature != ""
			}
			if meaningful && !aggregate {
				return errGoogleStreamReady
			}
		}
		return nil
	})
	if errors.Is(err, errGooglePromptBlocked) {
		source.Close()
		resp.StatusCode = http.StatusForbidden
		resp.Body = io.NopCloser(bytes.NewBufferString(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Google promptFeedback blocked this prompt"}}`))
		return false, nil
	}
	if captured.data.Len() > maxBody {
		return false, errors.New("Google pre-delivery buffer limit")
	}
	if errors.Is(err, errGoogleStreamReady) {
		resp.Body = &prefixedGoogleBody{Reader: io.MultiReader(bytes.NewReader(captured.data.Bytes()), source), Closer: source}
		return false, nil
	}
	if err == nil && aggregate && len(finishes) == expected {
		source.Close()
		resp.Body = io.NopCloser(bytes.NewReader(captured.data.Bytes()))
		return false, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err == nil {
		err = errGoogleStreamIncomplete
	}
	return retryable, err
}
