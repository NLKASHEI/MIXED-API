package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
)

func (s *Server) handleGoogleAccount(w http.ResponseWriter, r *http.Request, id, action string) {
	if s.google == nil {
		jsonError(w, 503, "请在 MIXED 应用窗口中打开此来源的授权步骤")
		return
	}
	if r.Method == "POST" && action == "login" {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		if err := s.google.Open(ctx, id); err != nil {
			message := "无法连接来源浏览器，请检查来源窗口与系统默认浏览器设置"
			var setup *googleweb.SetupError
			if errors.As(err, &setup) {
				message = setup.Message
			}
			jsonError(w, 503, message)
			return
		}
		jsonResult(w, 200, map[string]string{"status": "waiting", "message": "请在默认浏览器完成本人登录与页面提示，然后返回连接会话"})
		return
	}
	if r.Method == "POST" && action == "initialize" {
		initializer, ok := s.google.(interface {
			Initialize(context.Context, string) googleweb.State
		})
		if !ok {
			jsonError(w, 503, "当前环境无法初始化此来源页面")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
		defer cancel()
		jsonResult(w, 200, initializer.Initialize(ctx, id))
		return
	}
	if r.Method == "GET" && action == "" {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		jsonResult(w, 200, s.google.State(ctx, id))
		return
	}
	jsonError(w, 405, "不支持此来源账号操作")
}
func (s *Server) googleWebModels(ctx context.Context, id string) ([]string, error) {
	if s.google == nil || s.google.State(ctx, id).Status != "ready" {
		return nil, errors.New("请先打开来源页面并连接会话")
	}
	if id == "aps" {
		return []string{"gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.5-pro", "gemini-3-flash-preview", "gemini-3.1-pro-preview", "gemini-3.1-flash-lite", "gemini-3.1-flash-image", "gemini-3.5-flash"}, nil
	}
	response, err := s.google.Send(ctx, id, googleweb.Operation{Kind: "models"})
	if err != nil {
		return nil, errors.New("无法读取 AIS 模型目录")
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("AIS 拒绝模型目录请求，请检查本人授权")
	}
	var root []json.RawMessage
	decoded, decodeErr := io.ReadAll(&sparseArrayReader{reader: bufio.NewReader(strings.NewReader(string(raw)))})
	if readErr != nil || decodeErr != nil || len(raw) > 8<<20 || json.Unmarshal(decoded, &root) != nil || len(root) == 0 {
		return nil, errors.New("AIS 模型目录格式无效")
	}
	var rows [][]json.RawMessage
	if json.Unmarshal(root[0], &rows) != nil {
		return nil, errors.New("AIS 模型列表无效")
	}
	ids := []string{}
	unary := map[string]bool{}
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) < 8 {
			continue
		}
		var name string
		var methods []string
		json.Unmarshal(row[0], &name)
		json.Unmarshal(row[7], &methods)
		chat := false
		for _, method := range methods {
			if strings.EqualFold(method, "generateContent") {
				chat = true
			}
		}
		name = strings.TrimPrefix(name, "models/")
		if !chat || name == "" || strings.ContainsAny(name, "/\\?#%") || seen[name] {
			continue
		}
		seen[name] = true
		if len(row) > 78 && string(row[78]) != "null" {
			continue
		}
		ids = append(ids, "playground/"+name)
		var modes []int
		if len(row) > 82 {
			json.Unmarshal(row[82], &modes)
		}
		unary[name] = len(modes) > 0
	}
	buildSeen := map[string]bool{}
	token := ""
	complete := false
	for page := 0; page < 10; page++ {
		response, err := s.google.Send(ctx, id, googleweb.Operation{Kind: "build_models", PageToken: token})
		if err != nil {
			return nil, errors.New("无法读取 Build 模型目录")
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
		response.Body.Close()
		if response.StatusCode != 200 || readErr != nil || len(raw) > 8<<20 {
			return nil, errors.New("Build 模型目录请求失败")
		}
		decoded, err := io.ReadAll(&sparseArrayReader{reader: bufio.NewReader(strings.NewReader(string(raw)))})
		var frame []json.RawMessage
		if err != nil || json.Unmarshal(decoded, &frame) != nil {
			return nil, errors.New("Build 模型目录格式无效")
		}
		content, err := googleProxyBody(frame)
		if err != nil {
			return nil, errors.New("Build 模型目录缺少正文")
		}
		var catalog struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			Next string `json:"nextPageToken"`
		}
		if json.Unmarshal(content, &catalog) != nil {
			return nil, errors.New("Build 模型目录正文无效")
		}
		for _, m := range catalog.Models {
			name := strings.TrimPrefix(m.Name, "models/")
			if name == "" || strings.ContainsAny(name, "/\\?#%") || buildSeen[name] {
				continue
			}
			for _, method := range m.Methods {
				if method == "generateContent" {
					ids = append(ids, "build/"+name)
					buildSeen[name] = true
					if _, known := unary[name]; !known {
						unary[name] = true
					}
					break
				}
			}
		}
		if catalog.Next == "" {
			complete = true
			break
		}
		if token == catalog.Next {
			return nil, errors.New("Build 模型目录分页重复")
		}
		token = catalog.Next
	}
	if !complete {
		return nil, errors.New("Build 模型目录分页超限")
	}
	s.mu.Lock()
	s.googleBuildUnary = unary
	s.mu.Unlock()
	if len(ids) == 0 {
		return nil, errors.New("AIS 当前账号没有可用的对话模型")
	}
	return ids, nil
}
