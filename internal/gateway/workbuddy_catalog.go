package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var workbuddyCredits = regexp.MustCompile(`^x([0-9]+(?:\.[0-9]+)?) credits$`)

// The current account's product config supplies both its roster and credit
// rates. A static candidate list cannot establish an ongoing free promotion.
func (s *Server) workbuddyCatalog(ctx context.Context, b Backend) ([]catalogModel, error) {
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1/v1/models", nil)
	resp, err := s.accountJSON(r, workbuddyAccountID(b), "/v3/config", nil)
	if err != nil {
		return nil, errors.New("WorkBuddy 模型目录连接失败，请检查当前站点授权和代理")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("WorkBuddy 模型目录返回 HTTP " + strconv.Itoa(resp.StatusCode) + "；未用旧目录冒充实时结果")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (5<<20)+1))
	if err != nil || len(raw) > 5<<20 {
		return nil, errors.New("WorkBuddy 模型目录读取失败或超出限制")
	}
	var doc struct {
		Code int `json:"code"`
		Data struct {
			Models []struct {
				ID       string `json:"id"`
				Credits  string `json:"credits"`
				Disabled bool   `json:"disabled"`
			} `json:"models"`
			Agents []struct {
				Name   string   `json:"name"`
				Models []string `json:"models"`
			} `json:"agents"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Code != 0 {
		return nil, errors.New("WorkBuddy 模型目录格式或业务状态无效")
	}
	byID := map[string]catalogModel{}
	order := []string{}
	for _, entry := range doc.Data.Models {
		if entry.Disabled || entry.ID == "" || len(entry.ID) > 160 || strings.ContainsAny(entry.ID, "/\\?#%\r\n\x00") {
			continue
		}
		if _, ok := byID[entry.ID]; ok {
			continue
		}
		access := &modelAccess{Kind: "unknown", Label: "积分费用未声明 · 推理未验证"}
		if match := workbuddyCredits.FindStringSubmatch(entry.Credits); len(match) == 2 {
			if rate, err := strconv.ParseFloat(match[1], 64); err == nil {
				access.Kind = "credits"
				access.Label = "积分倍率 ×" + match[1] + " · 推理未验证"
				if rate == 0 {
					access.Kind = "free_promotion"
					access.Label = "当前目录零积分 · 活动可能调整 · 推理未验证"
				}
			}
		}
		byID[entry.ID] = catalogModel{ID: "workbuddy/" + entry.ID, Object: "model", OwnedBy: "workbuddy", Original: entry.ID, Access: access}
		order = append(order, entry.ID)
	}
	for _, agent := range doc.Data.Agents {
		if agent.Name == "cli" && len(agent.Models) > 0 {
			order = agent.Models
			break
		}
	}
	models := []catalogModel{}
	seen := map[string]bool{}
	for _, id := range order {
		if m, ok := byID[id]; ok && !seen[id] {
			models = append(models, m)
			seen[id] = true
		}
	}
	if len(models) == 0 {
		return nil, errors.New("WorkBuddy 当前站点未返回可用模型目录")
	}
	return models, nil
}
