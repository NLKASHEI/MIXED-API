package gateway

import (
	"errors"
	"reflect"
	"strings"
)

// Expand only local schema references before projecting into Playground's
// protobuf Schema. Do not fetch URLs or replace recursive constraints with {}.
func expandPlaygroundRefs(value any) (any, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("Schema 必须是对象")
	}
	remaining := 4096
	active := map[string]bool{}
	var walk func(any, int) (any, error)
	walk = func(value any, depth int) (any, error) {
		remaining--
		if remaining < 0 || depth > 32 {
			return nil, errors.New("Schema 引用展开超出限制，请使用 Build")
		}
		m, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("Schema 节点必须是对象")
		}
		out := map[string]any{}
		if raw, exists := m["$ref"]; exists {
			ref, ok := raw.(string)
			if !ok || (!strings.HasPrefix(ref, "#/$defs/") && !strings.HasPrefix(ref, "#/definitions/")) || active[ref] {
				return nil, errors.New("Playground 不支持远程、循环或此类 Schema 引用，请使用 Build")
			}
			var target any = root
			for _, token := range strings.Split(ref[2:], "/") {
				token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
				object, ok := target.(map[string]any)
				if !ok {
					return nil, errors.New("Schema 引用路径无效")
				}
				var found bool
				target, found = object[token]
				if !found {
					return nil, errors.New("Schema 引用目标不存在")
				}
			}
			active[ref] = true
			resolved, err := walk(target, depth+1)
			delete(active, ref)
			if err != nil {
				return nil, err
			}
			out = resolved.(map[string]any)
		}
		for key, child := range m {
			if key == "$ref" || key == "$defs" || key == "definitions" {
				continue
			}
			var err error
			switch key {
			case "properties":
				properties, ok := child.(map[string]any)
				if !ok {
					return nil, errors.New("Schema properties 无效")
				}
				fields := map[string]any{}
				for name, schema := range properties {
					fields[name], err = walk(schema, depth+1)
					if err != nil {
						return nil, err
					}
				}
				child = fields
			case "items":
				child, err = walk(child, depth+1)
				if err != nil {
					return nil, err
				}
			}
			if old, exists := out[key]; exists && !reflect.DeepEqual(old, child) {
				return nil, errors.New("Schema 引用包含冲突的同级约束，请使用 Build")
			}
			out[key] = child
		}
		return out, nil
	}
	return walk(root, 0)
}
