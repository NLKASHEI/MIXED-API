package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Both wire encoders consume the same validated event state, so incomplete
// tools and cancellation have identical semantics in Responses and Messages.
func (o *responseStream) sendMessages(kind string, fields map[string]any) error {
	write := func(kind string, fields map[string]any) error {
		fields["type"] = kind
		raw, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(o.w, "event: %s\ndata: %s\n\n", kind, raw); err != nil {
			return err
		}
		return http.NewResponseController(o.w).Flush()
	}
	index := fields["output_index"]
	switch kind {
	case "response.created":
		return write("message_start", map[string]any{"message": map[string]any{"id": o.response["id"], "type": "message", "role": "assistant", "model": o.response["model"], "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0}}})
	case "response.content_part.added":
		return write("content_block_start", map[string]any{"index": index, "content_block": map[string]any{"type": "text", "text": ""}})
	case "response.reasoning_summary_part.added":
		return write("content_block_start", map[string]any{"index": index, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
	case "response.output_item.added":
		item := fields["item"].(map[string]any)
		if item["type"] == "function_call" {
			tool := map[string]any{"type": "tool_use", "id": item["call_id"], "name": item["name"], "input": map[string]any{}}
			copyToolMetadata(tool, item)
			return write("content_block_start", map[string]any{"index": index, "content_block": tool})
		}
	case "response.output_text.delta":
		return write("content_block_delta", map[string]any{"index": index, "delta": map[string]any{"type": "text_delta", "text": fields["delta"]}})
	case "response.reasoning_summary_text.delta":
		return write("content_block_delta", map[string]any{"index": index, "delta": map[string]any{"type": "thinking_delta", "thinking": fields["delta"]}})
	case "response.function_call_arguments.delta":
		return write("content_block_delta", map[string]any{"index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": fields["delta"]}})
	case "response.output_item.done":
		return write("content_block_stop", map[string]any{"index": index})
	case "response.completed", "response.incomplete":
		stop := "end_turn"
		if len(o.tools) > 0 {
			stop = "tool_use"
		}
		if o.finish == "length" {
			stop = "max_tokens"
		} else if o.finish == "content_filter" {
			stop = "refusal"
		}
		usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
		if o.usage != nil {
			usage["input_tokens"], usage["output_tokens"] = o.usage["prompt_tokens"], o.usage["completion_tokens"]
		}
		if err := write("message_delta", map[string]any{"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": usage}); err != nil {
			return err
		}
		return write("message_stop", map[string]any{})
	case "response.failed":
		return write("error", map[string]any{"error": map[string]any{"type": "api_error", "message": "服务方流响应未正常完成"}})
	}
	return nil
}
