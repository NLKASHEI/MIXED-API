package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestExpressGeneratesThroughHTTPProxy(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("x-goog-api-key") != "fixture-key" {
			t.Error("provider authentication missing")
		}
		if strings.Contains(r.URL.Path, "streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"proxy-ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
			return
		}
		io.WriteString(w, `{"candidates":[{"index":0,"content":{"parts":[{"text":"proxy-ok"}]},"finishReason":"STOP"}]}`)
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "aiplatform.googleapis.com:443" {
			t.Error("unexpected proxy request")
			http.Error(w, "invalid", 400)
			return
		}
		remote, err := net.Dial("tcp", strings.TrimPrefix(upstream.URL, "https://"))
		if err != nil {
			t.Error(err)
			return
		}
		client, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			remote.Close()
			t.Error(err)
			return
		}
		defer client.Close()
		defer remote.Close()
		buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buf.Flush()
		done := make(chan struct{})
		go func() { io.Copy(remote, buf); remote.Close(); close(done) }()
		io.Copy(client, remote)
		client.Close()
		<-done
	}))
	defer proxy.Close()
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Network = NetworkSettings{ProxyMode: "custom", ProxyURL: proxy.URL}
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "fixture-key", Enabled: true}}
	app := NewServer("", cfg)
	defer app.client.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	app.client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: "example.com"}
	for _, stream := range []string{"false", "true"} {
		w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","messages":[{"role":"user","content":"hi"}],"stream":`+stream+`}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "proxy-ok") {
			t.Fatalf("generation through proxy failed: %d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 2 {
		t.Fatal("not both generation paths reached upstream through proxy")
	}
}

func TestExpressUsesConfiguredProxyAndRecordsTransportFailure(t *testing.T) {
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "CONNECT" || r.Host != "aiplatform.googleapis.com:443" {
			t.Errorf("wrong proxy target %s %s", r.Method, r.Host)
		}
		w.WriteHeader(502)
	}))
	defer proxy.Close()
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "private-provider-key", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	setting := `{"proxy_mode":"custom","proxy_url":"` + strings.Replace(proxy.URL, "http://", "http://private-user:private-password@", 1) + `"}`
	w := accountTestCall(app, "PUT", "/api/network", "admin", setting)
	if w.Code != 200 {
		t.Fatalf("save proxy: %d %s", w.Code, w.Body.String())
	}
	w = accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","messages":[{"role":"user","content":"private-prompt"}]}`)
	if calls.Load() != 1 || w.Code != 502 {
		t.Fatalf("proxy not used: calls=%d status=%d", calls.Load(), w.Code)
	}
	w = accountTestCall(app, "GET", "/api/logs", "admin", "")
	text := w.Body.String()
	for _, part := range []string{`"request_id"`, `"model":"vertex/gemini-test"`, `"stage"`, `"target":"https://aiplatform.googleapis.com/`, `"proxy":"http://127.0.0.1:`, `"error_type"`, `"proxy_status":502`} {
		if !strings.Contains(text, part) {
			t.Errorf("missing %s in %s", part, text)
		}
	}
	for _, secret := range []string{"private-provider-key", "private-user", "private-password", "private-prompt"} {
		if strings.Contains(text, secret) {
			t.Error("secret in diagnostics")
		}
	}
	if accountTestCall(app, "GET", "/api/network", "client", "").Code != 401 {
		t.Error("client can read network settings")
	}
}

func TestTransportDiagnosticsDistinguishDNSAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind string
	}{
		{"dns", &net.DNSError{Err: "no such host", Name: "secret-host", IsNotFound: true}, "dns"},
		{"timeout", context.DeadlineExceeded, "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey = "client"
			cfg.AdminKey = "admin"
			cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "private-key", Enabled: true}}
			app := NewServer("", cfg)
			app.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tc.err })
			w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","messages":[{"role":"user","content":"hi"}]}`)
			if w.Code != 502 {
				t.Fatal(w.Code)
			}
			w = accountTestCall(app, "GET", "/api/logs", "admin", "")
			if !strings.Contains(w.Body.String(), `"error_type":"`+tc.kind+`"`) {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestUpstreamStatusDetailsAreSafeAndUseful(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "private-key", Enabled: true}}
	app := NewServer("", cfg)
	app.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"20"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"private-key private-prompt"}}`))}, nil
	})
	accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","messages":[{"role":"user","content":"private-prompt"}]}`)
	w := accountTestCall(app, "GET", "/api/logs", "admin", "")
	var v any
	if json.Unmarshal(w.Body.Bytes(), &v) != nil {
		t.Fatal("invalid logs")
	}
	for _, part := range []string{`"upstream_status":429`, `RESOURCE_EXHAUSTED`, `"retry_after":"20"`} {
		if !strings.Contains(w.Body.String(), part) {
			t.Error(w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "private-") {
		t.Error("provider contents leaked")
	}
}

func TestExpressProjectAndLocationChooseOfficialResourcePath(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "private-key", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	raw, _ := json.Marshal(cfg)
	var input map[string]any
	json.Unmarshal(raw, &input)
	backend := input["backends"].([]any)[0].(map[string]any)
	backend["express_project"] = "project-test"
	backend["express_location"] = "global"
	raw, _ = json.Marshal(input)
	if w := accountTestCall(app, "PUT", "/api/config", "admin", string(raw)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "aiplatform.googleapis.com" || r.URL.Path != "/v1/projects/project-test/locations/global/publishers/google/models/gemini-test:generateContent" {
			t.Errorf("wrong resource path: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"status":"PERMISSION_DENIED"}}`))}, nil
	})
	accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","messages":[{"role":"user","content":"hi"}]}`)
	for _, bad := range []string{"", "../other", "x?key=secret"} {
		backend["express_project"] = bad
		raw, _ = json.Marshal(input)
		if w := accountTestCall(app, "PUT", "/api/config", "admin", string(raw)); w.Code != 400 {
			t.Errorf("invalid project accepted: %d", w.Code)
		}
	}
}

func TestRequestLogPreservesBusinessCodesWithoutProviderKey(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{`{"error":{"type":"QuotaExhausted","message":"private chat text"}}`, "QuotaExhausted"},
		{`{"code":70021,"message":"private chat text"}`, "70021"},
		{`{"error":{"code":"PRIVATE_PROVIDER_KEY"}}`, "[redacted]"},
	} {
		cfg := Defaults()
		cfg.APIKey = "client"
		cfg.AdminKey = "admin"
		cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none", ProviderKey: "PRIVATE_PROVIDER_KEY", Enabled: true}}
		app := NewServer("", cfg)
		app.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","messages":[{"role":"user","content":"private chat text"}]}`)
		log := accountTestCall(app, "GET", "/api/logs", "admin", "").Body.String()
		if !strings.Contains(log, `"upstream_code":"`+tc.code+`"`) || strings.Contains(log, "PRIVATE_PROVIDER_KEY") || strings.Contains(log, "private chat text") {
			t.Fatal(log)
		}
	}
}
