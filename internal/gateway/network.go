package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-ieproxy"
)

type NetworkSettings struct {
	ProxyMode string `json:"proxy_mode"`
	ProxyURL  string `json:"proxy_url,omitempty"`
}

func (n NetworkSettings) validate() error {
	switch n.ProxyMode {
	case "", "system", "direct":
		if n.ProxyURL != "" {
			return errors.New("代理地址只用于手动代理模式")
		}
	case "custom":
		if _, err := parseProxy(n.ProxyURL); err != nil {
			return err
		}
	default:
		return errors.New("代理模式必须为 system、custom 或 direct")
	}
	return nil
}
func parseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Hostname() == "" || u.Fragment != "" || u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("代理地址格式错误，请填写 http://主机:端口 或 socks5://主机:端口")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("代理端口必须在 1 到 65535 之间")
		}
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return u, nil
	}
	return nil, errors.New("代理协议仅支持 HTTP、HTTPS、SOCKS5")
}

// go-ieproxy provides the audited Windows static/PAC settings. Serialize its
// configuration refresh; each returned function owns an immutable snapshot.
var systemProxyMu sync.Mutex

func systemProxyFunc() func(*http.Request) (*url.URL, error) {
	systemProxyMu.Lock()
	defer systemProxyMu.Unlock()
	conf := ieproxy.ReloadConf()
	resolve := ieproxy.GetProxyFunc()
	envProxy := os.Getenv("HTTP_PROXY") != "" || os.Getenv("http_proxy") != "" || os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != ""
	return func(r *http.Request) (*url.URL, error) {
		p, err := resolve(r)
		if err == nil && p == nil && conf.Automatic.Active && !envProxy {
			return nil, errors.New("系统 PAC 未返回可用代理，请在设置中明确选择手动代理或直连")
		}
		return p, err
	}
}
func (s *Server) requestProxy(r *http.Request) (*url.URL, error) {
	s.mu.Lock()
	if time.Since(s.systemProxyAt) > 5*time.Second {
		s.systemProxy = systemProxyFunc()
		s.systemProxyAt = time.Now()
	}
	settings, system := s.cfg.Network, s.systemProxy
	s.mu.Unlock()
	host := r.URL.Hostname()
	if strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback() {
		recordProxy(r, nil, "本机直连")
		return nil, nil
	}
	var proxy *url.URL
	var err error
	source := "系统/环境代理"
	switch settings.ProxyMode {
	case "direct":
		source = "手动直连"
	case "custom":
		proxy, err = parseProxy(settings.ProxyURL)
		source = "手动代理"
	default:
		if raw := os.Getenv("PROXY_URL"); raw != "" {
			proxy, err = parseProxy(raw)
			source = "PROXY_URL"
		} else {
			proxy, err = system(r)
		}
	}
	recordProxy(r, proxy, source)
	if err != nil {
		diagnostic(r).set(func(v *RequestDetails) {
			v.Stage = "选择出站代理"
			v.Proxy = "代理解析失败，未发送请求"
			v.ErrorType = "proxy_configuration"
			v.Cause = "代理地址或 PAC 无法解析"
			v.Hint = "在设置 → 出站代理填写代理软件的 HTTP / SOCKS5 地址；PAC 返回直连时需本人明确选择直连。"
		})
	}
	return proxy, err
}
func (s *Server) handleNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		s.mu.Lock()
		s.systemProxyAt = time.Time{}
		s.mu.Unlock()
		n := s.config().Network
		if n.ProxyMode == "" {
			n.ProxyMode = "system"
		}
		probe, _ := http.NewRequest("GET", builtinBase("vertex"), nil)
		p, err := s.requestProxy(probe)
		route := "直连"
		if p != nil {
			route = safeEndpoint(p, false)
		}
		result := map[string]any{"settings": n, "effective_proxy": route}
		if err != nil {
			result["error"] = "代理配置解析失败，或 PAC 未返回可用代理；请手动设置代理，或明确选择直连"
		}
		jsonResult(w, 200, result)
		return
	}
	if r.Method != "PUT" {
		jsonError(w, 405, "Use GET or PUT")
		return
	}
	var n NetworkSettings
	var extra any
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&n) != nil || dec.Decode(&extra) != io.EOF {
		jsonError(w, 400, "代理设置格式错误")
		return
	}
	if err := n.validate(); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	system := systemProxyFunc()
	s.mu.Lock()
	next := s.cfg
	next.Network = n
	if Save(s.path, next) != nil {
		s.mu.Unlock()
		jsonError(w, 500, "无法保存代理设置")
		return
	}
	s.cfg = next
	s.systemProxy = system
	s.mu.Unlock()
	s.client.CloseIdleConnections()
	jsonResult(w, 200, map[string]bool{"saved": true})
}
