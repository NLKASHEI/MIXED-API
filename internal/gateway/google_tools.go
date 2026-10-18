package gateway

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// Explicit tool choices are a contract, including on Playground where no
// native functionCallingConfig wire field is available. Never retry generation
// to repair a violated contract or manufacture a tool invocation.
type googleToolContract struct {
	required, none, single bool
	names                  map[string]bool
	counts                 map[int]int
}

func googleTools(payload map[string]json.RawMessage) (*googleToolContract, error) {
	var parallel bool = true
	if raw, ok := payload["parallel_tool_calls"]; ok {
		if string(raw) == "null" || json.Unmarshal(raw, &parallel) != nil {
			return nil, errors.New("parallel_tool_calls 必须是布尔值")
		}
	}
	choice := "auto"
	name := ""
	if raw, ok := payload["tool_choice"]; ok {
		if json.Unmarshal(raw, &choice) != nil {
			var named struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(raw, &named) != nil || named.Type != "function" || named.Function.Name == "" {
				return nil, errors.New("工具选择无效")
			}
			choice, name = "required", named.Function.Name
		}
	}
	if choice != "auto" && choice != "none" && choice != "required" {
		return nil, errors.New("工具选择无效")
	}
	if choice == "auto" && parallel {
		return nil, nil
	}
	c := &googleToolContract{required: choice == "required", none: choice == "none", single: !parallel, names: map[string]bool{}, counts: map[int]int{}}
	var tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if raw, ok := payload["tools"]; ok && json.Unmarshal(raw, &tools) != nil {
		return nil, errors.New("工具声明无效")
	}
	for _, tool := range tools {
		n := tool.Function.Name
		if n == "" || c.names[n] {
			return nil, errors.New("工具名称为空或重复")
		}
		c.names[n] = true
	}
	if name != "" {
		if !c.names[name] {
			return nil, errors.New("选择的工具未声明")
		}
		c.names = map[string]bool{name: true}
	}
	if c.required && len(c.names) == 0 {
		return nil, errors.New("required 需要声明函数工具")
	}
	return c, nil
}

func (c *googleToolContract) prepare(gem map[string]any, playground bool) {
	if playground {
		delete(gem, "toolConfig")
	}
	if c == nil {
		return
	}
	if c.none {
		delete(gem, "tools")
		return
	}
	if playground {
		tools, _ := gem["tools"].([]any)
		for _, value := range tools {
			tool, _ := value.(map[string]any)
			decls, _ := tool["functionDeclarations"].([]map[string]any)
			if decls != nil {
				selected := []map[string]any{}
				for _, d := range decls {
					if name, _ := d["name"].(string); c.names[name] {
						selected = append(selected, d)
					}
				}
				tool["functionDeclarations"] = selected
			}
		}
	}
	instruction := ""
	if playground && c.required {
		names := []string{}
		for name := range c.names {
			names = append(names, name)
		}
		sort.Strings(names)
		instruction = "Use one of these functions before answering: " + strings.Join(names, ", ") + "."
	}
	if c.single {
		instruction += "\nCall at most one function in this response."
	}
	if strings.TrimSpace(instruction) != "" {
		system, ok := gem["systemInstruction"].(geminiContent)
		if !ok {
			system = geminiContent{}
		}
		system.Parts = append(system.Parts, geminiPart{Text: instruction})
		gem["systemInstruction"] = system
	}
}

func (c *googleToolContract) check(frame geminiResult) error {
	if c == nil {
		return nil
	}
	for _, candidate := range frame.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Call != nil {
				c.counts[candidate.Index]++
				var args map[string]any
				if c.none || !c.names[part.Call.Name] || c.single && c.counts[candidate.Index] > 1 || json.Unmarshal(part.Call.Args, &args) != nil || args == nil {
					return errors.New("来源违反工具调用约束")
				}
			}
		}
		if candidate.Finish == "STOP" && c.required && c.counts[candidate.Index] == 0 {
			return errors.New("来源未调用要求的工具")
		}
	}
	return nil
}
