package gateway

import (
	"errors"
	"slices"
)

func protocolFields(value map[string]any, allowed ...string) error {
	for key, v := range value {
		if v != nil && !slices.Contains(allowed, key) {
			return errors.New("此来源无法转换协议参数：" + key)
		}
	}
	return nil
}

func responseAnnotations(value any) ([]any, error) {
	out := []any{}
	if value == nil {
		return out, nil
	}
	rows, ok := value.([]any)
	if !ok {
		return nil, errors.New("invalid annotations")
	}
	for _, row := range rows {
		a, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("invalid annotation")
		}
		if citation, ok := a["url_citation"].(map[string]any); ok {
			copy := map[string]any{"type": "url_citation"}
			for k, v := range citation {
				copy[k] = v
			}
			out = append(out, copy)
		} else {
			out = append(out, a)
		}
	}
	return out, nil
}

func messagesReasoning(value any) ([]any, error) {
	rows, ok := value.([]any)
	if !ok {
		return nil, errors.New("invalid reasoning details")
	}
	out := []any{}
	for _, row := range rows {
		d, ok := row.(map[string]any)
		if !ok || d["format"] != "anthropic-claude-v1" {
			return nil, errors.New("unsupported opaque reasoning")
		}
		switch d["type"] {
		case "reasoning.text":
			text, tok := d["text"].(string)
			signature, sok := d["signature"].(string)
			if !tok || !sok {
				return nil, errors.New("invalid thinking")
			}
			out = append(out, map[string]any{"type": "thinking", "thinking": text, "signature": signature})
		case "reasoning.encrypted":
			data, ok := d["data"].(string)
			if !ok {
				return nil, errors.New("invalid encrypted reasoning")
			}
			out = append(out, map[string]any{"type": "redacted_thinking", "data": data})
		default:
			return nil, errors.New("unsupported reasoning type")
		}
	}
	return out, nil
}

func (o *responseStream) reasoning(details []any) error {
	for _, value := range details {
		d, ok := value.(map[string]any)
		if !ok {
			return errors.New("invalid reasoning")
		}
		if o.messages {
			if _, err := messagesReasoning([]any{d}); err != nil {
				return err
			}
		}
		if d["type"] == "reasoning.text" {
			text, ok := d["text"].(string)
			if !ok {
				return errors.New("invalid reasoning text")
			}
			if o.reasoningIndex < 0 {
				if err := o.text(text, true); err != nil {
					return err
				}
			}
			item := o.items[o.reasoningIndex].(map[string]any)
			if item["summary"].([]any)[0].(map[string]any)["text"] != text {
				return errors.New("reasoning signature text mismatch")
			}
			item["reasoning_details"] = []any{d}
			if o.messages {
				if err := o.sendMessages("reasoning_signature", map[string]any{"output_index": o.reasoningIndex, "signature": d["signature"]}); err != nil {
					return err
				}
			}
			o.reasoningIndex = -1
		} else {
			_, err := o.add(map[string]any{"id": "rs_" + newKey(""), "type": "reasoning", "summary": []any{}, "reasoning_details": []any{d}})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *responseStream) refusal(text string) error {
	o.refused = true
	if o.refusalIndex < 0 {
		part := map[string]any{"type": "refusal", "refusal": ""}
		item := map[string]any{"id": "msg_" + newKey(""), "type": "message", "role": "assistant", "status": "in_progress", "content": []any{part}}
		index, err := o.add(item)
		if err != nil {
			return err
		}
		o.refusalIndex = index
		if err := o.send("response.content_part.added", map[string]any{"item_id": item["id"], "output_index": index, "content_index": 0, "part": part}); err != nil {
			return err
		}
	}
	item := o.items[o.refusalIndex].(map[string]any)
	part := item["content"].([]any)[0].(map[string]any)
	part["refusal"] = part["refusal"].(string) + text
	return o.send("response.refusal.delta", map[string]any{"item_id": item["id"], "output_index": o.refusalIndex, "content_index": 0, "delta": text})
}
