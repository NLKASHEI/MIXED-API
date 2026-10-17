package gateway

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
)

// MakerSuite uses JSON arrays indexed by protobuf field number minus one.
// The field evidence and MIT source revision are recorded in
// docs/third-party/google-web.md. Gemini remains our shared internal format.
func playgroundOperation(model string, body any) (googleweb.Operation, error) {
	raw, _ := json.Marshal(body)
	var gem map[string]any
	if json.Unmarshal(raw, &gem) != nil {
		return googleweb.Operation{}, errors.New("无效生成请求")
	}
	contents, ok := gem["contents"].([]any)
	if !ok || len(contents) == 0 {
		return googleweb.Operation{}, errors.New("消息不能为空")
	}
	wire := make([]any, 11)
	wire[0] = "models/" + model
	wire[10] = 1
	encoded := []any{}
	binding := []string{}
	prompt := []string{}
	encodeContent := func(v any) (any, error) {
		c, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("消息内容无效")
		}
		parts, ok := c["parts"].([]any)
		if !ok {
			return nil, errors.New("消息缺少 parts")
		}
		result := []any{}
		for _, v := range parts {
			p, ok := v.(map[string]any)
			if !ok {
				return nil, errors.New("消息 part 无效")
			}
			part := make([]any, 15)
			value := ""
			for key, v := range p {
				switch key {
				case "text":
					part[1] = v
					value, _ = v.(string)
					prompt = append(prompt, value)
				case "inlineData":
					d, ok := v.(map[string]any)
					if !ok {
						return nil, errors.New("媒体数据无效")
					}
					part[2] = []any{d["mimeType"], d["data"]}
					value, _ = d["data"].(string)
				case "fileData":
					d, ok := v.(map[string]any)
					if !ok {
						return nil, errors.New("媒体引用无效")
					}
					part[6] = []any{d["mimeType"], d["fileUri"]}
				case "thought":
					part[12] = v
				case "thoughtSignature":
					part[14] = v
				case "functionCall", "functionResponse":
					f, ok := v.(map[string]any)
					if !ok {
						return nil, errors.New("工具内容无效")
					}
					field, name := 10, "args"
					if key == "functionResponse" {
						field, name = 11, "response"
					}
					obj, ok := f[name].(map[string]any)
					if !ok {
						return nil, errors.New("工具参数或结果必须是对象")
					}
					part[field] = []any{f["name"], protoStruct(obj), f["id"]}
				default:
					return nil, errors.New("Playground 尚不支持此消息内容字段")
				}
			}
			binding = append(binding, value)
			result = append(result, trimProto(part))
		}
		role := c["role"]
		if role == nil {
			role = "user"
		}
		return []any{result, role}, nil
	}
	for _, c := range contents {
		value, err := encodeContent(c)
		if err != nil {
			return googleweb.Operation{}, err
		}
		encoded = append(encoded, value)
	}
	wire[1] = encoded
	contentBinding := strings.Join(binding, " ")
	contentPrompt := strings.Join(prompt, " ")
	if system, ok := gem["systemInstruction"]; ok {
		value, err := encodeContent(system)
		if err != nil {
			return googleweb.Operation{}, err
		}
		wire[5] = value
	}
	config := make([]any, 27)
	config[13] = 1
	if settings, ok := gem["generationConfig"].(map[string]any); ok {
		for key, v := range settings {
			switch key {
			case "stopSequences":
				config[1] = v
			case "candidateCount":
				if v != float64(1) {
					return googleweb.Operation{}, errors.New("Playground 仅支持 n=1")
				}
			case "maxOutputTokens":
				config[3] = v
			case "temperature":
				config[4] = v
			case "topP":
				config[5] = v
			case "topK":
				config[6] = v
			case "responseMimeType":
				config[7] = v
			case "responseSchema", "responseJsonSchema":
				schema, err := playgroundSchema(v, 0)
				if err != nil {
					return googleweb.Operation{}, err
				}
				config[8] = schema
			case "seed":
				config[18] = v
			case "thinkingConfig":
				t, ok := v.(map[string]any)
				if !ok {
					return googleweb.Operation{}, errors.New("思考参数无效")
				}
				level := map[string]int{"LOW": 1, "MEDIUM": 2, "HIGH": 3, "MINIMAL": 4}
				l, _ := t["thinkingLevel"].(string)
				config[16] = []any{t["includeThoughts"], t["thinkingBudget"], nil, level[strings.ToUpper(l)]}
			case "responseModalities":
				items, ok := v.([]any)
				if !ok {
					return googleweb.Operation{}, errors.New("输出类型无效")
				}
				mapped := []any{}
				for _, item := range items {
					name, _ := item.(string)
					code := map[string]int{"TEXT": 1, "IMAGE": 2, "AUDIO": 3}[name]
					if code == 0 {
						return googleweb.Operation{}, errors.New("输出类型尚未支持")
					}
					mapped = append(mapped, code)
				}
				config[14] = mapped
			case "imageConfig":
				im, ok := v.(map[string]any)
				if !ok {
					return googleweb.Operation{}, errors.New("图片参数无效")
				}
				config[26] = []any{im["aspectRatio"], im["imageSize"]}
			default:
				return googleweb.Operation{}, errors.New("Playground 尚不支持此生成参数")
			}
		}
	}
	wire[3] = trimProto(config)
	if tools, ok := gem["tools"].([]any); ok {
		mapped := []any{}
		for _, value := range tools {
			tool, ok := value.(map[string]any)
			if !ok {
				return googleweb.Operation{}, errors.New("工具声明无效")
			}
			for key, v := range tool {
				switch key {
				case "functionDeclarations":
					functions, ok := v.([]any)
					if !ok {
						return googleweb.Operation{}, errors.New("工具声明无效")
					}
					decl := []any{}
					for _, f := range functions {
						item, ok := f.(map[string]any)
						if !ok {
							return googleweb.Operation{}, errors.New("工具声明无效")
						}
						schema, err := playgroundSchema(item["parameters"], 0)
						if err != nil {
							return googleweb.Operation{}, err
						}
						decl = append(decl, []any{item["name"], item["description"], schema})
					}
					mapped = append(mapped, []any{nil, decl})
				case "googleSearch":
					mapped = append(mapped, []any{nil, nil, nil, []any{nil, []any{[]any{}}}})
				case "codeExecution":
					mapped = append(mapped, []any{[]any{}})
				case "urlContext":
					t := make([]any, 8)
					t[7] = []any{}
					mapped = append(mapped, t)
				default:
					return googleweb.Operation{}, errors.New("Playground 尚不支持此工具")
				}
			}
		}
		wire[6] = mapped
	}
	if tc, ok := gem["toolConfig"].(map[string]any); ok {
		if f, ok := tc["functionCallingConfig"].(map[string]any); ok && f["mode"] != "AUTO" {
			return googleweb.Operation{}, errors.New("Playground 的强制工具选择尚未完成，请使用 Build")
		}
	}
	op := googleweb.Operation{Kind: "playground", Model: model, Binding: contentBinding, Prompt: contentPrompt}
	op.Body, _ = json.Marshal(wire)
	return op, nil
}
func trimProto(v []any) []any {
	for len(v) > 0 && v[len(v)-1] == nil {
		v = v[:len(v)-1]
	}
	return v
}
func protoAt(v []any, n int) any {
	if n < 0 || n >= len(v) {
		return nil
	}
	return v[n]
}
func protoArray(v any) []any { a, _ := v.([]any); return a }
func protoStruct(m map[string]any) []any {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	entries := []any{}
	for _, k := range keys {
		entries = append(entries, []any{k, protoValue(m[k])})
	}
	return []any{entries}
}
func protoValue(v any) []any {
	switch x := v.(type) {
	case nil:
		return []any{0}
	case float64:
		return []any{nil, x}
	case string:
		return []any{nil, nil, x}
	case bool:
		return []any{nil, nil, nil, x}
	case map[string]any:
		return []any{nil, nil, nil, nil, protoStruct(x)}
	case []any:
		values := []any{}
		for _, item := range x {
			values = append(values, protoValue(item))
		}
		return []any{nil, nil, nil, nil, nil, []any{values}}
	}
	return []any{0}
}
func fromProtoValue(v any, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("tool value too deep")
	}
	a, ok := v.([]any)
	if !ok {
		return nil, errors.New("invalid tool value")
	}
	for i, item := range a {
		if item == nil {
			continue
		}
		switch i {
		case 0:
			return nil, nil
		case 1, 2, 3:
			return item, nil
		case 4:
			return fromProtoStruct(item, depth+1)
		case 5:
			items := protoArray(protoAt(protoArray(item), 0))
			out := []any{}
			for _, child := range items {
				x, err := fromProtoValue(child, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, x)
			}
			return out, nil
		}
	}
	return nil, errors.New("empty tool value")
}
func fromProtoStruct(v any, depth int) (map[string]any, error) {
	out := map[string]any{}
	for _, e := range protoArray(protoAt(protoArray(v), 0)) {
		entry := protoArray(e)
		name, ok := protoAt(entry, 0).(string)
		if !ok {
			return nil, errors.New("invalid tool key")
		}
		value, err := fromProtoValue(protoAt(entry, 1), depth+1)
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, nil
}

func playgroundSchema(value any, depth int) ([]any, error) {
	if depth > 32 {
		return nil, errors.New("工具 Schema 层级过深")
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("工具 Schema 必须是对象")
	}
	out := make([]any, 22)
	for k, v := range m {
		switch k {
		case "$schema", "title":
		case "type":
			s, ok := v.(string)
			if !ok {
				return nil, errors.New("Playground 暂不支持联合类型 Schema，请使用 Build")
			}
			code := map[string]int{"STRING": 1, "NUMBER": 2, "INTEGER": 3, "BOOLEAN": 4, "ARRAY": 5, "OBJECT": 6}[strings.ToUpper(s)]
			if code == 0 {
				return nil, errors.New("Schema 类型无效")
			}
			out[0] = code
		case "items":
			child, err := playgroundSchema(v, depth+1)
			if err != nil {
				return nil, err
			}
			out[5] = child
		case "properties":
			properties, ok := v.(map[string]any)
			if !ok {
				return nil, errors.New("Schema properties 无效")
			}
			names := []string{}
			for name := range properties {
				names = append(names, name)
			}
			sort.Strings(names)
			pairs := []any{}
			for _, name := range names {
				child, err := playgroundSchema(properties[name], depth+1)
				if err != nil {
					return nil, err
				}
				pairs = append(pairs, []any{name, child})
			}
			out[6] = pairs
		default:
			indices := map[string]int{"format": 1, "description": 2, "nullable": 3, "enum": 4, "required": 7, "minProperties": 8, "maxProperties": 9, "minimum": 10, "maximum": 11, "minLength": 12, "maxLength": 13, "pattern": 14, "maxItems": 20, "minItems": 21}
			i, ok := indices[k]
			if !ok {
				return nil, errors.New("Playground 尚不支持该 Schema 约束，请使用 Build")
			}
			out[i] = v
		}
	}
	return trimProto(out), nil
}

func decodePlaygroundFrame(raw []json.RawMessage) (geminiResult, error) {
	var frame []any
	data, _ := json.Marshal(raw)
	if json.Unmarshal(data, &frame) != nil {
		return geminiResult{}, errors.New("invalid Playground frame")
	}
	candidates := []any{}
	for index, v := range protoArray(protoAt(frame, 0)) {
		c := protoArray(v)
		content := protoArray(protoAt(c, 0))
		parts := []any{}
		for _, v := range protoArray(protoAt(content, 0)) {
			p := protoArray(v)
			part := map[string]any{}
			if t, ok := protoAt(p, 1).(string); ok {
				part["text"] = t
			}
			if im := protoArray(protoAt(p, 2)); len(im) >= 2 {
				part["inlineData"] = map[string]any{"mimeType": im[0], "data": im[1]}
			}
			if protoAt(p, 12) == true || protoAt(p, 12) == float64(1) {
				part["thought"] = true
			}
			if sig, ok := protoAt(p, 14).(string); ok {
				part["thoughtSignature"] = sig
			}
			if call := protoArray(protoAt(p, 10)); len(call) >= 2 {
				args, err := fromProtoStruct(call[1], 0)
				if err != nil {
					return geminiResult{}, err
				}
				part["functionCall"] = map[string]any{"name": call[0], "args": args}
			}
			if len(part) > 0 {
				parts = append(parts, part)
			}
		}
		code, _ := protoAt(c, 1).(float64)
		finish := map[int]string{0: "", 1: "STOP", 2: "MAX_TOKENS", 3: "SAFETY", 4: "RECITATION", 5: "OTHER", 6: "BLOCKLIST", 7: "PROHIBITED_CONTENT", 8: "SPII", 9: "MALFORMED_FUNCTION_CALL"}[int(code)]
		if code != 0 && finish == "" {
			return geminiResult{}, errors.New("unknown Playground finish code")
		}
		candidates = append(candidates, map[string]any{"index": index, "content": map[string]any{"role": "model", "parts": parts}, "finishReason": finish})
	}
	u := protoArray(protoAt(frame, 2))
	result := map[string]any{"candidates": candidates, "usageMetadata": map[string]any{"promptTokenCount": protoAt(u, 0), "candidatesTokenCount": protoAt(u, 1), "totalTokenCount": protoAt(u, 2), "cachedContentTokenCount": protoAt(u, 3), "thoughtsTokenCount": protoAt(u, 9)}}
	var out geminiResult
	data, _ = json.Marshal(result)
	if json.Unmarshal(data, &out) != nil {
		return out, errors.New("invalid Playground result")
	}
	return out, nil
}
