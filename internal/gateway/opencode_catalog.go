package gateway

import (
	_ "embed"
	"encoding/json"
	"errors"
)

// Protocol metadata only, extracted from the MIT-licensed official database.
// Pin: anomalyco/models.dev f4088b386739663acaf3a88c916d7dfe9ec5efd4.
// This is not an account entitlement list; discovery still queries OpenCode.
//
//go:embed opencode-models.json
var openCodeModelData []byte

type openCodeTarget struct {
	Protocol string `json:"protocol"`
	Base     string `json:"base"`
}

var openCodePinned = func() map[string]openCodeTarget {
	var out map[string]openCodeTarget
	if err := json.Unmarshal(openCodeModelData, &out); err != nil {
		panic(err)
	}
	for _, target := range out {
		if !validOpenCodeTarget(target) {
			panic("invalid embedded OpenCode route")
		}
	}
	return out
}()

func validOpenCodeTarget(target openCodeTarget) bool {
	switch target.Protocol {
	case "chat/completions", "responses":
		return target.Base == "https://opencode.ai/inference/openai/v1" || target.Base == "https://opencode.ai/inference/systemone/v1"
	case "messages":
		return target.Base == "https://opencode.ai/inference/anthropic/v1"
	case "gemini":
		return target.Base == "https://opencode.ai/inference/google/v1beta"
	}
	return false
}

func targetForSDK(sdk, api string) (openCodeTarget, error) {
	target := openCodeTarget{}
	switch sdk {
	case "@ai-sdk/openai":
		target = openCodeTarget{"responses", "https://opencode.ai/inference/openai/v1"}
	case "@ai-sdk/openai-compatible":
		target = openCodeTarget{"chat/completions", "https://opencode.ai/inference/openai/v1"}
	case "@ai-sdk/anthropic":
		target = openCodeTarget{"messages", "https://opencode.ai/inference/anthropic/v1"}
	case "@ai-sdk/google":
		target = openCodeTarget{"gemini", "https://opencode.ai/inference/google/v1beta"}
	default:
		return target, errors.New("该模型的协议尚不支持")
	}
	if api != "" {
		target.Base = api
	}
	if !validOpenCodeTarget(target) {
		return openCodeTarget{}, errors.New("OpenCode 模型目录中的端点不受信任")
	}
	return target, nil
}
