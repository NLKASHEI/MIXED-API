package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkBuddyRegionsIsolateLoginRefreshAndInference(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "workbuddy", Kind: "builtin", URL: builtinBase("workbuddy"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	var mu sync.Mutex
	counts := map[string]int{}
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		host := r.URL.Host
		mu.Lock()
		counts[host+r.URL.Path]++
		mu.Unlock()
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Refresh-Token") != "" {
			if r.Header.Get("X-Domain") != host {
				t.Error("missing regional domain", host, r.Header.Get("X-Domain"))
			}
		}
		var data string
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			if host == "www.workbuddy.ai" && r.URL.Query().Get("platform") != "workbuddy-ai" {
				t.Error("wrong international product platform")
			}
			data = `{"data":{"state":"test","authUrl":"https://` + host + `/login"}}`
		case "/v2/plugin/auth/token":
			data = `{"data":{"accessToken":"old-` + host + `","refreshToken":"refresh-` + host + `","expiresIn":1}}`
		case "/v2/plugin/login/account":
			data = `{"data":{"uid":"user-test"}}`
		case "/v2/plugin/auth/token/refresh":
			if r.Header.Get("X-Refresh-Token") != "refresh-"+host {
				t.Error("region crossed during refresh")
			}
			data = `{"data":{"accessToken":"new-` + host + `","expiresIn":3600}}`
		case "/v2/chat/completions":
			if r.Header.Get("Authorization") != "Bearer new-"+host {
				t.Error("region crossed during generation")
			}
			data = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		default:
			t.Error("unexpected endpoint", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	saveRegion := func(region string) {
		raw, _ := json.Marshal(cfg)
		var body map[string]any
		json.Unmarshal(raw, &body)
		body["backends"].([]any)[0].(map[string]any)["workbuddy_region"] = region
		raw, _ = json.Marshal(body)
		if w := accountTestCall(app, "PUT", "/api/config", "admin", string(raw)); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	login := func(host string) {
		w := accountTestCall(app, "POST", "/api/accounts/workbuddy/login", "admin", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), host) {
			t.Fatal("wrong login region", w.Code, w.Body.String())
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(accountTestCall(app, "GET", "/api/accounts/workbuddy", "admin", "").Body.String(), `"authorized"`) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("login incomplete")
	}
	infer := func() {
		w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"workbuddy/auto","messages":[{"role":"user","content":"hello"}]}`)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	login("copilot.tencent.com")
	infer()
	saveRegion("global")
	if strings.Contains(accountTestCall(app, "GET", "/api/accounts/workbuddy", "admin", "").Body.String(), `"authorized"`) {
		t.Fatal("domestic authorization reused internationally")
	}
	login("www.codebuddy.ai")
	infer()
	saveRegion("intl")
	if strings.Contains(accountTestCall(app, "GET", "/api/accounts/workbuddy", "admin", "").Body.String(), `"authorized"`) {
		t.Fatal("CodeBuddy session reused by WorkBuddy AI")
	}
	login("www.workbuddy.ai")
	infer()
	saveRegion("domestic")
	infer()
	mu.Lock()
	defer mu.Unlock()
	for _, host := range []string{"copilot.tencent.com", "www.codebuddy.ai", "www.workbuddy.ai"} {
		if counts[host+"/v2/plugin/auth/token/refresh"] != 1 {
			t.Fatal("refresh routing", counts)
		}
	}
	if counts["copilot.tencent.com/v2/chat/completions"] != 2 || counts["www.codebuddy.ai/v2/chat/completions"] != 1 {
		t.Fatal("inference routing", counts)
	}
}
