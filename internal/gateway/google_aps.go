package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// The anonymous Cloud Console GraphQL API uses Schema map-entry arrays and
// string int64 constraints. Build/Express use REST JSON instead. Protocol
// evidence: APS2api request_config.go / schema.go, pinned in third-party docs.
func apsPayload(body map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err = dec.Decode(&out); err != nil {
		return nil, err
	}
	tools, _ := out["tools"].([]any)
	for _, tool := range tools {
		entry, _ := tool.(map[string]any)
		declarations, _ := entry["functionDeclarations"].([]any)
		for _, value := range declarations {
			declaration, _ := value.(map[string]any)
			schema := declaration["parameters"]
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			declaration["parameters"], err = apsSchema(schema, 0)
			if err != nil {
				return nil, err
			}
		}
	}
	if config, ok := out["generationConfig"].(map[string]any); ok {
		if schema, exists := config["responseJsonSchema"]; exists {
			config["responseSchema"], err = apsSchema(schema, 0)
			if err != nil {
				return nil, err
			}
			delete(config, "responseJsonSchema")
		}
		if topK, ok := config["topK"].(json.Number); ok {
			n, _ := topK.Int64()
			if n > 63 {
				return nil, errors.New("无头V 的 top_k 不能超过 63")
			}
		}
	}
	return out, nil
}

func apsSchema(value any, depth int) (map[string]any, error) {
	if depth > 32 {
		return nil, errors.New("无头V Schema 层级过深")
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("无头V Schema 必须是对象")
	}
	out := map[string]any{}
	for key, value := range m {
		switch key {
		case "$schema", "title": // descriptive metadata, not validation constraints
		case "type":
			name, ok := value.(string)
			if !ok {
				return nil, errors.New("无头V 不支持联合类型 Schema")
			}
			name = strings.ToUpper(name)
			switch name {
			case "STRING", "NUMBER", "INTEGER", "BOOLEAN", "ARRAY", "OBJECT":
				out[key] = name
			default:
				return nil, errors.New("无头V Schema 类型无效")
			}
		case "properties":
			properties, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("无头V Schema properties 必须是对象")
			}
			keys := make([]string, 0, len(properties))
			for name := range properties {
				keys = append(keys, name)
			}
			sort.Strings(keys)
			entries := make([]any, 0, len(keys))
			for _, name := range keys {
				child, err := apsSchema(properties[name], depth+1)
				if err != nil {
					return nil, err
				}
				entries = append(entries, map[string]any{"key": name, "value": child})
			}
			out[key] = entries
		case "items":
			child, err := apsSchema(value, depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = child
		case "minItems", "maxItems", "minLength", "maxLength", "minProperties", "maxProperties":
			number, ok := value.(json.Number)
			if !ok {
				return nil, errors.New("无头V Schema 长度约束必须是非负整数")
			}
			n, err := number.Int64()
			if err != nil || n < 0 {
				return nil, errors.New("无头V Schema 长度约束必须是非负整数")
			}
			out[key] = strconv.FormatInt(n, 10)
		case "description", "format", "nullable", "enum", "required", "minimum", "maximum", "pattern", "propertyOrdering":
			out[key] = value
		default:
			return nil, errors.New("无头V 不支持此 Schema 约束，请使用支持该约束的模型通道")
		}
	}
	return out, nil
}
