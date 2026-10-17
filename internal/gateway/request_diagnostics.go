package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"syscall"
)

type RequestDetails struct {
	BusinessCode     int    `json:"business_code,omitempty"`
	Stage            string `json:"stage,omitempty"`
	Target           string `json:"target,omitempty"`
	Proxy            string `json:"proxy,omitempty"`
	ProxySource      string `json:"proxy_source,omitempty"`
	ErrorType        string `json:"error_type,omitempty"`
	Cause            string `json:"cause,omitempty"`
	Hint             string `json:"hint,omitempty"`
	UpstreamStatus   int    `json:"upstream_status,omitempty"`
	UpstreamCode     string `json:"upstream_code,omitempty"`
	RetryAfter       string `json:"retry_after,omitempty"`
	ProxyStatus      int    `json:"proxy_status,omitempty"`
	Address          string `json:"address,omitempty"`
	Attempt          int    `json:"attempt,omitempty"`
	RetryStatus      int    `json:"retry_status,omitempty"`
	ResolvedLocation string `json:"resolved_location,omitempty"`
}
type diagnosticKey struct{}
type requestDiagnostic struct {
	mu      sync.Mutex
	details RequestDetails
}

func (d *requestDiagnostic) set(f func(*RequestDetails)) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f(&d.details)
}
func diagnostic(r *http.Request) *requestDiagnostic {
	if r == nil {
		return nil
	}
	d, _ := r.Context().Value(diagnosticKey{}).(*requestDiagnostic)
	return d
}
func safeEndpoint(u *url.URL, path bool) string {
	if u == nil {
		return "直连"
	}
	v := url.URL{Scheme: u.Scheme, Host: u.Host}
	if v.Scheme == "" {
		v.Scheme = "http"
	}
	// Model IDs are recorded separately. No query, userinfo or arbitrary path.
	if path {
		v.Path = "/"
		switch u.Hostname() {
		case "aiplatform.googleapis.com", "opencode.ai", "alkalimakersuite-pa.clients6.google.com", "cloudconsole-pa.clients6.google.com":
			v.Path = u.Path
		}
	}
	return v.String()
}
func recordProxy(r *http.Request, p *url.URL, source string) {
	diagnostic(r).set(func(d *RequestDetails) { d.Proxy = safeEndpoint(p, false); d.ProxySource = source })
}

func providerDo(client *http.Client, req *http.Request) (*http.Response, error) {
	d := diagnostic(req)
	d.set(func(v *RequestDetails) {
		*v = RequestDetails{Stage: "连接准备", Target: safeEndpoint(req.URL, true), Attempt: v.Attempt + 1, RetryStatus: v.RetryStatus}
	})
	stage := func(value string) { d.set(func(v *RequestDetails) { v.Stage = value }) }
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { stage("DNS 解析") },
		ConnectStart: func(_, address string) {
			d.set(func(v *RequestDetails) { v.Stage = "TCP 连接"; v.Address = address })
		},
		ConnectDone: func(_, _ string, e error) {
			if e == nil {
				stage("代理隧道 / TLS 准备")
			}
		},
		TLSHandshakeStart: func() { stage("TLS 握手") },
		TLSHandshakeDone:  func(tls.ConnectionState, error) {},
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				stage("等待上游响应")
			}
		},
		GotFirstResponseByte: func() { stage("读取上游响应") },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := client.Do(req)
	if err != nil {
		d.set(func(v *RequestDetails) {
			if v.ErrorType == "proxy_configuration" {
				return
			}
			v.ErrorType, v.Cause, v.Hint = networkCause(err, v.Proxy)
			if v.ProxyStatus >= 300 {
				v.ErrorType = "proxy_connect"
				v.Cause = fmt.Sprintf("代理拒绝 CONNECT 隧道（HTTP %d）", v.ProxyStatus)
				if v.ProxyStatus == 407 {
					v.Hint = "代理要求认证，请检查代理用户名和密码。"
				}
			}
		})
	} else {
		if resp.Request == nil {
			resp.Request = req
		}
		d.set(func(v *RequestDetails) {
			v.UpstreamStatus = resp.StatusCode
			v.RetryAfter = safeRetryAfter(resp.Header.Get("Retry-After"))
		})
	}
	return resp, err
}
func networkCause(err error, proxy string) (kind, cause, hint string) {
	kind, cause, hint = "network", "网络请求未完成", "检查网络与代理日志；不会自动改用其他来源。"
	var dns *net.DNSError
	var neterr net.Error
	var cert x509.UnknownAuthorityError
	var hostname x509.HostnameError
	switch {
	case errors.Is(err, context.Canceled):
		kind, cause, hint = "cancelled", "请求已取消", "检查客户端是否主动停止请求。"
	case errors.As(err, &dns):
		kind, cause, hint = "dns", "DNS 解析失败", "检查 DNS 或代理是否能解析服务方域名。"
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &neterr) && neterr.Timeout():
		kind, cause, hint = "timeout", "网络连接或响应超时", "检查所选代理是否运行、节点是否可访问 Google；模型目录成功不代表推理通路可达。"
	case errors.As(err, &cert) || errors.As(err, &hostname):
		kind, cause, hint = "tls_certificate", "TLS 证书验证失败", "检查系统时间、证书或代理的 HTTPS 检查设置。"
	case errors.Is(err, syscall.ECONNREFUSED):
		kind, cause, hint = "connection_refused", "目标拒绝 TCP 连接", "检查代理是否启动以及代理端口是否填写正确。"
	default:
		if proxy != "" && proxy != "直连" {
			kind, cause = "proxy_connect", "代理连接或 CONNECT 隧道失败"
			hint = "检查代理端口、认证与节点；软件没有回退直连。"
		}
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		cause += fmt.Sprintf("（系统错误码 %d）", uintptr(errno))
	}
	return
}

func safeRetryAfter(value string) string {
	if len(value) == 0 || len(value) > 80 {
		return ""
	}
	if _, err := http.ParseTime(value); err == nil {
		return value
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return value
}

// Keep structured service status, not arbitrary messages that can echo prompts,
// Authorization headers, cookies or the user's entire request.
func recordProviderFailure(resp *http.Response) {
	if resp.Request == nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return
	}
	recordProviderErrorBody(resp.Request, raw)
}

func recordProviderErrorBody(req *http.Request, raw []byte) {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	fields := root
	if item := root["error"]; len(item) > 0 {
		var nested map[string]json.RawMessage
		if json.Unmarshal(item, &nested) == nil && nested != nil {
			fields = nested
		}
	}
	code := ""
	for _, key := range []string{"status", "code", "type"} {
		raw := fields[key]
		var text string
		if json.Unmarshal(raw, &text) == nil && diagnosticCode.MatchString(text) {
			code = text
			break
		}
		var number int64
		if json.Unmarshal(raw, &number) == nil && number != 0 {
			code = strconv.FormatInt(number, 10)
			break
		}
	}
	diagnostic(req).set(func(d *RequestDetails) {
		if code != "" {
			d.UpstreamCode = code
		}
		if d.UpstreamStatus >= 400 {
			d.ErrorType = "upstream_http"
			d.Cause = fmt.Sprintf("服务方返回 HTTP %d", d.UpstreamStatus)
		} else {
			d.ErrorType = "upstream_stream"
			d.Cause = "上游在响应正文中返回业务错误"
		}
		d.Hint = "根据服务方错误码检查模型、账号权限与用量；刷新登录不会补充积分。"
		if req.URL != nil && req.URL.Hostname() == "aiplatform.googleapis.com" {
			if d.UpstreamStatus == 404 {
				var message string
				json.Unmarshal(fields["message"], &message)
				if match := expressResource.FindStringSubmatch(message); len(match) == 4 {
					d.ResolvedLocation = match[2]
				}
				d.Hint = "Google 未找到该模型或当前项目无权访问。检查模型 ID；在快速V配置中探测 Key 项目，再选择 global 或模型支持的区域。404 不会自动重试，也不会替换模型。"
			} else if d.UpstreamStatus == 429 {
				d.Hint = "Google 返回资源不足或限流；单凭 429 无法判断是额度耗尽还是临时拥堵。已按快速V设置重试；查看尝试次数、等待时间及官方项目用量。换登录或刷新模型不会补额度。"
			}
		}
	})
}

var diagnosticCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
