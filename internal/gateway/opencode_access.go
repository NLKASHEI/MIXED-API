package gateway

import (
	"slices"
	"strings"
)

type modelAccess struct {
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Verified bool   `json:"verified"`
}

// Price/auth classification is not evidence that this account can generate.
func openCodeAccess(id string) *modelAccess {
	for _, source := range upstreamCatalog.Sources {
		if source.ID != "opencode" {
			continue
		}
		if slices.Contains(source.RetiredModels, id) {
			return &modelAccess{Kind: "retired", Label: "上游已撤下或停用"}
		}
		if slices.Contains(source.FreeModels, id) {
			return &modelAccess{Kind: "free_candidate", Label: "免费无头候选 · 本应用未验证可用"}
		}
	}
	if _, known := openCodePinned[id]; known && !strings.HasSuffix(id, "-free") {
		return &modelAccess{Kind: "key_paid", Label: "需要 Key · 按账号计费"}
	}
	return &modelAccess{Kind: "unknown", Label: "权限与费用待确认"}
}
