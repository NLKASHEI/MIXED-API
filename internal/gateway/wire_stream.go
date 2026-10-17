package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// One event at a time: downstream writes provide backpressure; cancellation
// closes the original provider request rather than a detached background task.
func readSSE(ctx context.Context, body io.Reader, event func([]byte) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), maxBody)
	var data strings.Builder
	dispatch := func() error {
		if data.Len() == 0 {
			return nil
		}
		b := []byte(data.String())
		data.Reset()
		return event(b)
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			if data.Len()+len(line) > maxBody {
				return errors.New("provider event too large")
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return dispatch()
}

type sseOutput struct {
	w         http.ResponseWriter
	id, model string
	created   int64
	started   bool
}

func newSSEOutput(w http.ResponseWriter, model string) *sseOutput {
	return &sseOutput{w: w, id: "chatcmpl-" + newKey(""), model: model, created: time.Now().Unix()}
}
func (o *sseOutput) send(v any) error {
	if !o.started {
		o.w.Header().Set("Content-Type", "text/event-stream")
		o.w.Header().Set("Cache-Control", "no-cache")
		o.w.WriteHeader(200)
		o.started = true
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(o.w, "data: %s\n\n", raw); err != nil {
		return err
	}
	if f, ok := o.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
func (o *sseOutput) chunk(delta map[string]any, finish any, usage any) error {
	choices := []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}
	if delta == nil {
		choices = []any{}
	}
	v := map[string]any{"id": o.id, "object": "chat.completion.chunk", "created": o.created, "model": o.model, "choices": choices}
	if usage != nil {
		v["usage"] = usage
	}
	return o.send(v)
}
func (o *sseOutput) done() {
	_, _ = io.WriteString(o.w, "data: [DONE]\n\n")
	if f, ok := o.w.(http.Flusher); ok {
		f.Flush()
	}
}

func streamResponsesAsChat(w http.ResponseWriter, r *http.Request, body io.Reader, model string, includeUsage bool) {
	out := newSSEOutput(w, model)
	indices := map[int]int{}
	arguments := map[int]string{}
	size := 0
	complete := false
	err := readSSE(r.Context(), body, func(data []byte) error {
		size += len(data)
		if size > maxBody {
			return errors.New("response too large")
		}
		if string(data) == "[DONE]" {
			return nil
		}
		var e struct {
			Type     string         `json:"type"`
			Delta    string         `json:"delta"`
			Index    int            `json:"output_index"`
			Item     map[string]any `json:"item"`
			Response map[string]any `json:"response"`
		}
		if json.Unmarshal(data, &e) != nil {
			return errors.New("invalid provider event")
		}
		switch e.Type {
		case "error", "response.failed":
			return errors.New("provider generation failed")
		case "response.output_text.delta":
			return out.chunk(map[string]any{"content": e.Delta}, nil, nil)
		case "response.reasoning_summary_text.delta":
			return out.chunk(map[string]any{"reasoning_content": e.Delta}, nil, nil)
		case "response.output_item.added":
			if e.Item["type"] != "function_call" {
				return nil
			}
			if _, exists := indices[e.Index]; exists {
				return errors.New("duplicate tool identity")
			}
			id, _ := e.Item["call_id"].(string)
			name, _ := e.Item["name"].(string)
			if id == "" || name == "" {
				return errors.New("missing tool identity")
			}
			args, _ := e.Item["arguments"].(string)
			arguments[e.Index] = args
			index := len(indices)
			indices[e.Index] = index
			return out.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": index, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, nil, nil)
		case "response.function_call_arguments.delta":
			index, ok := indices[e.Index]
			if !ok {
				return errors.New("tool delta without call")
			}
			arguments[e.Index] += e.Delta
			return out.chunk(map[string]any{"tool_calls": []any{map[string]any{"index": index, "function": map[string]any{"arguments": e.Delta}}}}, nil, nil)
		case "response.completed", "response.incomplete":
			if e.Type == "response.completed" {
				for _, args := range arguments {
					if !json.Valid([]byte(args)) {
						return errors.New("invalid completed tool arguments")
					}
				}
			}
			finish := "stop"
			if len(indices) > 0 {
				finish = "tool_calls"
			}
			if e.Type == "response.incomplete" {
				finish = "length"
				if d, ok := e.Response["incomplete_details"].(map[string]any); ok && d["reason"] == "content_filter" {
					finish = "content_filter"
				}
			}
			if err := out.chunk(map[string]any{}, finish, nil); err != nil {
				return err
			}
			if includeUsage {
				if usage, ok := e.Response["usage"].(map[string]any); ok {
					if err := out.chunk(nil, nil, chatUsage(usage)); err != nil {
						return err
					}
				}
			}
			complete = true
			return io.EOF
		}
		return nil
	})
	if complete && err == io.EOF {
		out.done()
		return
	}
	if r.Context().Err() == nil {
		_ = out.send(map[string]any{"error": map[string]string{"message": "服务方流响应在完成前中断"}})
	}
}
