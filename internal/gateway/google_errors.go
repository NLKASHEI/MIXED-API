package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
)

// Only numeric codes and fixed known enum values leave this boundary. Provider
// messages can contain prompts or credentials and are never kept or returned.
func googleBusinessError(ctx context.Context, raw json.RawMessage) error {
	var value struct {
		Code       json.RawMessage `json:"code"`
		Status     string          `json:"status"`
		ErrorType  string          `json:"errorType"`
		Extensions struct {
			Status struct {
				Code   json.RawMessage `json:"code"`
				Status string          `json:"status"`
			} `json:"status"`
		} `json:"extensions"`
	}
	json.Unmarshal(raw, &value)
	codeRaw := value.Code
	if len(value.Extensions.Status.Code) > 0 {
		codeRaw = value.Extensions.Status.Code
	}
	code := 0
	if json.Unmarshal(codeRaw, &code) != nil {
		var text string
		json.Unmarshal(codeRaw, &text)
		code, _ = strconv.Atoi(text)
	}
	if !(code >= 1 && code <= 16 || code >= 400 && code <= 599) {
		code = 0
	}
	names := map[int]string{1: "CANCELLED", 2: "UNKNOWN", 3: "INVALID_ARGUMENT", 4: "DEADLINE_EXCEEDED", 5: "NOT_FOUND", 6: "ALREADY_EXISTS", 7: "PERMISSION_DENIED", 8: "RESOURCE_EXHAUSTED", 9: "FAILED_PRECONDITION", 10: "ABORTED", 11: "OUT_OF_RANGE", 12: "UNIMPLEMENTED", 13: "INTERNAL", 14: "UNAVAILABLE", 15: "DATA_LOSS", 16: "UNAUTHENTICATED", 400: "INVALID_ARGUMENT", 401: "UNAUTHENTICATED", 403: "PERMISSION_DENIED", 404: "NOT_FOUND", 429: "RESOURCE_EXHAUSTED", 500: "INTERNAL", 503: "UNAVAILABLE"}
	name := names[code]
	if name == "" {
		for _, known := range names {
			if known == value.Status || known == value.ErrorType || known == value.Extensions.Status.Status {
				name = known
				break
			}
		}
	}
	d, _ := ctx.Value(diagnosticKey{}).(*requestDiagnostic)
	d.set(func(v *RequestDetails) {
		v.Stage = "解析上游协议"
		v.ErrorType = "upstream_business"
		v.BusinessCode = code
		v.UpstreamCode = name
		v.Cause = "上游 HTTP 响应成功，但正文拒绝了生成请求"
		v.Hint = "根据业务错误码检查官方页面、模型权限与网络；此请求未按成功处理，也不会自动重放。"
		switch name {
		case "RESOURCE_EXHAUSTED":
			v.Cause = "上游正文返回资源不足或限流"
			v.Hint = "检查官方用量与服务拥堵情况；业务错误不会自动重试，刷新登录不会补充额度。"
		case "PERMISSION_DENIED", "UNAUTHENTICATED":
			v.Hint = "请本人检查官方页面登录、验证与模型权限；浏览器连接成功不代表拥有该模型权限。"
		case "NOT_FOUND":
			v.Hint = "检查所选模型与来源，不会自动改走其他模型或通道。"
		}
	})
	return errors.New("upstream protocol rejected generation")
}
