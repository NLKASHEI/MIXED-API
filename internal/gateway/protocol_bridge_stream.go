package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// A synchronous pipe preserves downstream backpressure across the protocol
// adapter. Closing the client also closes both the pipe and provider request.
type protocolPipe struct {
	header http.Header
	status int
	ready  chan struct{}
	writer *io.PipeWriter
}

func (p *protocolPipe) Header() http.Header { return p.header }
func (p *protocolPipe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
		close(p.ready)
	}
}
func (p *protocolPipe) Write(data []byte) (int, error) {
	p.WriteHeader(200)
	return p.writer.Write(data)
}
func (p *protocolPipe) Flush() { p.WriteHeader(200) }

func (s *Server) streamProtocolBridge(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage, messages bool) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	stop := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()); _ = writer.CloseWithError(ctx.Err()) })
	defer stop()
	p := &protocolPipe{header: make(http.Header), ready: make(chan struct{}), writer: writer}
	go func() {
		defer writer.Close()
		defer p.WriteHeader(502)
		s.serveIntegrated(p, r.Clone(ctx), b, publicModel, model, payload)
	}()
	select {
	case <-ctx.Done():
		return
	case <-p.ready:
	}
	for _, key := range []string{"Retry-After", "X-Mixed-Backend", "X-Mixed-Protocol-Catalog"} {
		if v := p.header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	if p.status != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(p.status)
		_, _ = io.Copy(w, io.LimitReader(reader, maxBody))
		return
	}
	streamChatAsResponses(w, r, reader, publicModel, messages)
}

type responseStream struct {
	messages                  bool
	w                         http.ResponseWriter
	response                  map[string]any
	items                     []any
	textIndex, reasoningIndex int
	refusalIndex              int
	refused                   bool
	tools                     map[int]map[string]any
	sequence, size            int
	finish                    string
	usage                     map[string]any
}

func (o *responseStream) send(kind string, fields map[string]any) error {
	if o.messages {
		return o.sendMessages(kind, fields)
	}
	fields["type"], fields["sequence_number"] = kind, o.sequence
	o.sequence++
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(o.w, "event: %s\ndata: %s\n\n", kind, raw); err != nil {
		return err
	}
	return http.NewResponseController(o.w).Flush()
}
func (o *responseStream) add(item map[string]any) (int, error) {
	index := len(o.items)
	o.items = append(o.items, item)
	return index, o.send("response.output_item.added", map[string]any{"output_index": index, "item": item})
}
func (o *responseStream) text(text string, reasoning bool) error {
	index := &o.textIndex
	if reasoning {
		index = &o.reasoningIndex
	}
	if *index < 0 {
		item := map[string]any{"id": "msg_" + newKey(""), "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}
		if reasoning {
			item = map[string]any{"id": "rs_" + newKey(""), "type": "reasoning", "summary": []any{}}
		}
		var err error
		*index, err = o.add(item)
		if err != nil {
			return err
		}
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		kind, key := "response.content_part.added", "content_index"
		if reasoning {
			part = map[string]any{"type": "summary_text", "text": ""}
			kind, key = "response.reasoning_summary_part.added", "summary_index"
			item["summary"] = []any{part}
		} else {
			item["content"] = []any{part}
		}
		if err := o.send(kind, map[string]any{"item_id": item["id"], "output_index": *index, key: 0, "part": part}); err != nil {
			return err
		}
	}
	item := o.items[*index].(map[string]any)
	field, kind, key := "content", "response.output_text.delta", "content_index"
	if reasoning {
		field, kind, key = "summary", "response.reasoning_summary_text.delta", "summary_index"
	}
	part := item[field].([]any)[0].(map[string]any)
	part["text"] = part["text"].(string) + text
	return o.send(kind, map[string]any{"item_id": item["id"], "output_index": *index, key: 0, "delta": text})
}
func (o *responseStream) chunk(raw []byte) error {
	o.size += len(raw)
	if o.size > maxBody {
		return errors.New("response too large")
	}
	var chunk struct {
		Choices []struct {
			Index  int            `json:"index"`
			Delta  map[string]any `json:"delta"`
			Finish *string        `json:"finish_reason"`
		} `json:"choices"`
		Usage map[string]any  `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &chunk) != nil || len(chunk.Error) > 0 || len(chunk.Choices) > 1 {
		return errors.New("invalid stream")
	}
	if chunk.Usage != nil {
		o.usage = chunk.Usage
	}
	for _, c := range chunk.Choices {
		if c.Delta["media"] != nil || c.Delta["audio"] != nil {
			return errors.New("audio representation unavailable; use audio/speech")
		}
		if c.Index != 0 || o.finish != "" {
			return errors.New("data after completion")
		}
		if text, ok := c.Delta["content"].(string); ok && text != "" {
			if err := o.text(text, false); err != nil {
				return err
			}
		}
		if text, ok := c.Delta["reasoning_content"].(string); ok && text != "" {
			if err := o.text(text, true); err != nil {
				return err
			}
		}
		if details, ok := c.Delta["reasoning_details"].([]any); ok {
			if err := o.reasoning(details); err != nil {
				return err
			}
		}
		if refusal, ok := c.Delta["refusal"].(string); ok && refusal != "" {
			if err := o.refusal(refusal); err != nil {
				return err
			}
		}
		if value := c.Delta["annotations"]; value != nil {
			rows, err := responseAnnotations(value)
			if err != nil {
				return err
			}
			if o.messages && len(rows) > 0 {
				return errors.New("citation representation unavailable")
			}
			if o.textIndex < 0 {
				if err := o.text("", false); err != nil {
					return err
				}
			}
			item := o.items[o.textIndex].(map[string]any)
			part := item["content"].([]any)[0].(map[string]any)
			annotations := part["annotations"].([]any)
			for _, row := range rows {
				if err := o.send("response.output_text.annotation.added", map[string]any{"item_id": item["id"], "output_index": o.textIndex, "content_index": 0, "annotation_index": len(annotations), "annotation": row}); err != nil {
					return err
				}
				annotations = append(annotations, row)
			}
			part["annotations"] = annotations
		}
		if calls, ok := c.Delta["tool_calls"].([]any); ok {
			for _, value := range calls {
				call, ok := value.(map[string]any)
				if !ok {
					return errors.New("invalid tool")
				}
				n, ok := call["index"].(float64)
				if !ok || n < 0 || n > 1024 || n != float64(int(n)) {
					return errors.New("invalid tool index")
				}
				f, ok := call["function"].(map[string]any)
				if !ok {
					return errors.New("invalid function")
				}
				item := o.tools[int(n)]
				if item == nil {
					item = map[string]any{"id": "fc_" + newKey(""), "type": "function_call", "call_id": "", "name": "", "arguments": "", "status": "in_progress"}
					o.tools[int(n)] = item
				}
				// Chat permits fragmented identity as well as arguments. The
				// target protocols have no name delta, so emit tools only once
				// their complete identity and opaque metadata are available.
				if id, ok := call["id"].(string); ok {
					item["call_id"] = item["call_id"].(string) + id
				}
				for _, key := range []string{"name", "arguments"} {
					if value, ok := f[key].(string); ok {
						item[key] = item[key].(string) + value
					}
				}
				copyToolMetadata(item, call)
			}
		}
		if c.Finish != nil {
			o.finish = *c.Finish
		}
	}
	return nil
}
func (o *responseStream) complete() error {
	if o.finish != "length" && o.finish != "content_filter" && o.finish != "stop" && o.finish != "tool_calls" {
		return errors.New("unknown finish")
	}
	keys := make([]int, 0, len(o.tools))
	for key, item := range o.tools {
		if item["call_id"] == "" || item["name"] == "" || (o.finish != "length" && o.finish != "content_filter" && !json.Valid([]byte(item["arguments"].(string)))) {
			return errors.New("invalid tool arguments or identity")
		}
		keys = append(keys, key)
	}
	sort.Ints(keys)
	for _, key := range keys {
		item := o.tools[key]
		args := item["arguments"]
		item["arguments"] = ""
		index, err := o.add(item)
		if err != nil {
			return err
		}
		item["arguments"] = args
		if err := o.send("response.function_call_arguments.delta", map[string]any{"item_id": item["id"], "output_index": index, "delta": args}); err != nil {
			return err
		}
	}
	status, event := "completed", "response.completed"
	if o.finish == "length" || o.finish == "content_filter" {
		status, event = "incomplete", "response.incomplete"
		reason := "max_output_tokens"
		if o.finish == "content_filter" {
			reason = "content_filter"
		}
		o.response["incomplete_details"] = map[string]any{"reason": reason}
	} else if o.finish != "stop" && o.finish != "tool_calls" {
		return errors.New("unknown finish")
	}
	for index, value := range o.items {
		item := value.(map[string]any)
		switch item["type"] {
		case "message", "reasoning":
			field, kind, key, partKind := "content", "response.output_text.done", "content_index", "response.content_part.done"
			if item["type"] == "reasoning" {
				field, kind, key, partKind = "summary", "response.reasoning_summary_text.done", "summary_index", "response.reasoning_summary_part.done"
			} else {
				item["status"] = status
			}
			for n, value := range item[field].([]any) {
				part := value.(map[string]any)
				event, textField := kind, "text"
				if part["type"] == "refusal" {
					event, textField = "response.refusal.done", "refusal"
				}
				if err := o.send(event, map[string]any{"item_id": item["id"], "output_index": index, key: n, textField: part[textField]}); err != nil {
					return err
				}
				if err := o.send(partKind, map[string]any{"item_id": item["id"], "output_index": index, key: n, "part": part}); err != nil {
					return err
				}
			}
		case "function_call":
			item["status"] = status
			if err := o.send("response.function_call_arguments.done", map[string]any{"item_id": item["id"], "output_index": index, "arguments": item["arguments"]}); err != nil {
				return err
			}
		}
		if err := o.send("response.output_item.done", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
	}
	o.response["output"], o.response["status"] = o.items, status
	if o.usage != nil {
		u := o.usage
		o.response["usage"] = map[string]any{"input_tokens": u["prompt_tokens"], "output_tokens": u["completion_tokens"], "total_tokens": u["total_tokens"], "input_tokens_details": u["prompt_tokens_details"], "output_tokens_details": u["completion_tokens_details"]}
	}
	return o.send(event, map[string]any{"response": o.response})
}

func streamChatAsResponses(w http.ResponseWriter, r *http.Request, body io.Reader, model string, messages bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	o := &responseStream{w: w, textIndex: -1, reasoningIndex: -1, refusalIndex: -1, tools: map[int]map[string]any{}, items: []any{}, response: map[string]any{"id": "resp_" + newKey(""), "object": "response", "model": model, "created_at": time.Now().Unix(), "status": "in_progress", "output": []any{}, "error": nil, "incomplete_details": nil, "store": false}}
	o.messages = messages
	if o.send("response.created", map[string]any{"response": o.response}) != nil {
		return
	}
	if o.send("response.in_progress", map[string]any{"response": o.response}) != nil {
		return
	}
	done := false
	err := readSSE(r.Context(), body, func(raw []byte) error {
		if string(raw) == "[DONE]" {
			if err := o.complete(); err != nil {
				return err
			}
			done = true
			return io.EOF
		}
		return o.chunk(raw)
	})
	if !done && r.Context().Err() == nil {
		_ = err
		o.response["status"] = "failed"
		o.response["error"] = map[string]any{"code": "provider_stream_error", "message": "服务方流响应未正常完成"}
		_ = o.send("response.failed", map[string]any{"response": o.response})
	}
}
