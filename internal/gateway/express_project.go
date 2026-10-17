package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Same no-generation probe as vertex2openai/app/express_key_probe.py. Parse
// only a complete resource name, never expose Google's arbitrary error text.
const expressProbeModel = "mixed-api-probe-nonexistent-model"

var expressResource = regexp.MustCompile(`projects/([a-z0-9][a-z0-9-]{0,62})/locations/([a-z0-9][a-z0-9-]{0,62})/publishers/google/models/([A-Za-z0-9._-]+)`)

func (s *Server) handleExpressProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, 405, "Use POST")
		return
	}
	var input struct {
		Key string `json:"provider_key"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	var extra any
	if dec.Decode(&input) != nil || dec.Decode(&extra) != io.EOF || strings.TrimSpace(input.Key) == "" {
		jsonError(w, 400, "请先填写 Express API Key")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	d := &requestDiagnostic{}
	r = r.WithContext(context.WithValue(ctx, diagnosticKey{}, d))
	resp, err := providerJSON(s.client, r, builtinBase("vertex")+"/v1/publishers/google/models/"+expressProbeModel+":generateContent", map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": "ping"}}}}}, map[string]string{"x-goog-api-key": strings.TrimSpace(input.Key)})
	if err != nil {
		d.mu.Lock()
		details := d.details
		d.mu.Unlock()
		jsonResult(w, 502, map[string]any{"ok": false, "message": "项目探测连接失败，请检查代理", "details": details})
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var result struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(raw, &result)
	match := expressResource.FindStringSubmatch(result.Error.Message)
	if resp.StatusCode == 404 && len(match) == 4 && match[3] == expressProbeModel && !strings.Contains(match[0], input.Key) {
		jsonResult(w, 200, map[string]any{"ok": true, "project": match[1], "location": match[2], "message": "已识别 Key 所属项目与默认路由区域。可选择 global 后保存；探测成功不代表模型可用。"})
		return
	}
	recordProviderErrorBody(resp.Request, raw)
	message := "未取得项目路径，请手动填写当前 Key 所属的项目 ID"
	if resp.StatusCode == 429 {
		message = "Google 对探测也返回 429，暂时无法识别项目；请稍后重试或手动填写"
	}
	if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 {
		message = "Google 拒绝此 Key 的探测请求，请检查 Key 类型、限制及项目权限"
	}
	// No raw upstream contents or unrecognized code is returned from a probe.
	jsonResult(w, 200, map[string]any{"ok": false, "message": message, "upstream_status": resp.StatusCode})
}
