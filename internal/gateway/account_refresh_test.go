package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func accountTestCall(app http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestConcurrentExpiredAccountRequestsShareOneRefresh(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "codex", Kind: "builtin", URL: builtinBase("codex"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	var refreshes, generations atomic.Int32
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data := ""
		switch r.URL.Path {
		case "/oauth/token":
			refreshes.Add(1)
			var form map[string]string
			if r.URL.Host != "auth.openai.com" || json.NewDecoder(r.Body).Decode(&form) != nil || form["refresh_token"] != "refresh-old" || form["grant_type"] != "refresh_token" || form["client_id"] != codexOAuthClient {
				t.Error("refresh did not follow the official OAuth grant")
			}
			time.Sleep(30 * time.Millisecond)
			data = `{"access_token":"access-new","refresh_token":"refresh-new","expires_in":3600}`
		case "/backend-api/codex/responses":
			generations.Add(1)
			if r.Header.Get("Authorization") != "Bearer access-new" || r.Header.Get("ChatGPT-Account-ID") != "account-one" {
				t.Error("expired credential sent to generation or account identity lost")
			}
			data = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"output\":[],\"status\":\"completed\"}}\n\n"
		default:
			t.Errorf("unexpected upstream %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	w := accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"access-old","refresh_token":"refresh-old","account_id":"account-one","expires":1}}`)
	if w.Code != 200 {
		t.Fatalf("import: %d", w.Code)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			w := accountTestCall(app, "POST", "/v1/responses", "client", `{"model":"codex/test","input":"hello"}`)
			if w.Code != 200 {
				t.Errorf("refresh inference: %d %s", w.Code, w.Body.String())
			}
		}()
	}
	group.Wait()
	if refreshes.Load() != 1 || generations.Load() != 8 {
		t.Fatalf("refreshes=%d generations=%d", refreshes.Load(), generations.Load())
	}
	// A new gateway instance must use the committed rotation, not the old token.
	restarted := NewServer(app.path, cfg)
	restarted.client.Transport = app.client.Transport
	if w := accountTestCall(restarted, "POST", "/v1/responses", "client", `{"model":"codex/test","input":"again"}`); w.Code != 200 || refreshes.Load() != 1 {
		t.Fatalf("rotated credentials not persisted: %d refreshes=%d", w.Code, refreshes.Load())
	}
	state := accountTestCall(app, "GET", "/api/accounts/codex", "admin", "").Body.String()
	if strings.Contains(state, "access-new") || strings.Contains(state, "refresh-new") {
		t.Fatal("credentials leaked")
	}
}

func TestAccount401RefreshesOnceAndPreservesSourceIdentity(t *testing.T) {
	for _, id := range []string{"codex", "workbuddy"} {
		t.Run(id, func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey, cfg.AdminKey = "client", "admin"
			cfg.Backends = []Backend{{ID: id, Kind: "builtin", URL: builtinBase(id), Auth: "none", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			var refreshes, generations atomic.Int32
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				status, data := 200, ""
				switch r.URL.Path {
				case "/v2/plugin/auth/state":
					data = `{"data":{"state":"test","authUrl":"https://copilot.tencent.com/login"}}`
				case "/v2/plugin/auth/token":
					data = `{"data":{"accessToken":"access-old","refreshToken":"refresh-old","domain":"account-domain"}}`
				case "/v2/plugin/login/account":
					data = `{"data":{"uid":"user-test"}}`
				case "/oauth/token":
					refreshes.Add(1)
					data = `{"access_token":"access-new"}`
				case "/v2/plugin/auth/token/refresh":
					refreshes.Add(1)
					if r.Header.Get("X-Refresh-Token") != "refresh-old" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Domain") != "account-domain" {
						t.Error("incorrect refresh credential boundary")
					}
					data = `{"data":{"accessToken":"access-new"}}`
				default:
					n := generations.Add(1)
					want := "Bearer access-old"
					if n > 1 {
						want = "Bearer access-new"
					}
					if r.Header.Get("Authorization") != want {
						t.Error("wrong generation credential")
					}
					if id == "workbuddy" && r.Header.Get("X-Domain") != "account-domain" {
						t.Error("refresh lost account domain")
					}
					if id == "codex" && r.Header.Get("ChatGPT-Account-ID") != "account-one" {
						t.Error("refresh lost account ID")
					}
					status, data = 401, `{"error":{"message":"access-old refresh-old secret must stay private"}}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			if id == "codex" {
				if w := accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"access-old","refresh_token":"refresh-old","account_id":"account-one"}}`); w.Code != 200 {
					t.Fatal(w.Code)
				}
			} else {
				if w := accountTestCall(app, "POST", "/api/accounts/workbuddy/login", "admin", ""); w.Code != 200 {
					t.Fatal(w.Code)
				}
				until := time.Now().Add(time.Second)
				for !strings.Contains(accountTestCall(app, "GET", "/api/accounts/workbuddy", "admin", "").Body.String(), `"authorized"`) {
					if time.Now().After(until) {
						t.Fatal("test authorization did not complete")
					}
					time.Sleep(time.Millisecond)
				}
			}
			w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"`+id+`/test","messages":[{"role":"user","content":"hello"}]}`)
			if w.Code != 401 || refreshes.Load() != 1 || generations.Load() != 2 {
				t.Fatalf("status=%d refreshes=%d generations=%d", w.Code, refreshes.Load(), generations.Load())
			}
			if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "access-old") {
				t.Fatal("provider credential leaked")
			}
		})
	}
}

func TestAccountChangeInvalidatesAnInflightRefresh(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "logout", true: "replace"}[replace], func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey, cfg.AdminKey = "client", "admin"
			cfg.Backends = []Backend{{ID: "codex", Kind: "builtin", URL: builtinBase("codex"), Auth: "none", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			started, release := make(chan struct{}), make(chan struct{})
			var generations atomic.Int32
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				data := ""
				if r.URL.Path == "/oauth/token" {
					close(started)
					<-release
					data = `{"access_token":"stale-refresh-result","expires_in":3600}`
				} else {
					generations.Add(1)
					if r.Header.Get("Authorization") != "Bearer replacement" || r.Header.Get("ChatGPT-Account-ID") != "second-account" {
						t.Error("stale account was resurrected")
					}
					data = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"ok\",\"output\":[]}}\n\n"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"refresh","account_id":"first-account","expires":1}}`)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- accountTestCall(app, "POST", "/v1/responses", "client", `{"model":"codex/test","input":"hello"}`)
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				close(release)
				t.Fatal("refresh never started")
			}
			if w := accountTestCall(app, "DELETE", "/api/accounts/codex", "client", ""); w.Code != 401 {
				t.Fatal("client key can logout")
			}
			if replace {
				if w := accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"replacement","account_id":"second-account"}}`); w.Code != 200 {
					t.Fatal(w.Code)
				}
			} else if w := accountTestCall(app, "DELETE", "/api/accounts/codex", "admin", ""); w.Code != 200 {
				t.Fatal(w.Code)
			}
			close(release)
			select {
			case w := <-done:
				if w.Code != 401 {
					t.Fatalf("old request survived account change: %d", w.Code)
				}
			case <-time.After(time.Second):
				t.Fatal("refresh did not end")
			}
			w := accountTestCall(app, "POST", "/v1/responses", "client", `{"model":"codex/test","input":"after change"}`)
			want := 401
			if replace {
				want = 200
			}
			if w.Code != want || generations.Load() != map[bool]int32{false: 0, true: 1}[replace] {
				t.Fatalf("new request %d generations %d", w.Code, generations.Load())
			}
		})
	}
}

func TestPermanentRefreshRejectionRequiresLoginAfterRestart(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "codex", Kind: "builtin", URL: builtinBase("codex"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	var calls atomic.Int32
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Path != "/oauth/token" {
			t.Error("revoked credentials reached generation")
		}
		return &http.Response{StatusCode: 400, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant","error_description":"private-token"}`))}, nil
	})
	accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"old","refresh_token":"refresh","account_id":"account","expires":1}}`)
	for range 2 {
		w := accountTestCall(app, "POST", "/v1/responses", "client", `{"model":"codex/test","input":"hello"}`)
		if w.Code != 401 || strings.Contains(w.Body.String(), "private-token") {
			t.Fatalf("unsafe failure %d %s", w.Code, w.Body.String())
		}
		restarted := NewServer(app.path, cfg)
		restarted.client.Transport = app.client.Transport
		app = restarted
	}
	if calls.Load() != 1 {
		t.Fatalf("revoked grant retried %d times", calls.Load())
	}
	if state := accountTestCall(app, "GET", "/api/accounts/codex", "admin", "").Body.String(); !strings.Contains(state, `"none"`) {
		t.Fatal(state)
	}
}

func TestCodexModelDiscoveryRefreshesBeforeUsingExpiredJWT(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "codex", Kind: "builtin", URL: builtinBase("codex"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	var refreshed atomic.Bool
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data := ""
		if r.URL.Path == "/oauth/token" {
			refreshed.Store(true)
			data = `{"access_token":"current","expires_in":3600}`
		} else {
			if r.Method != "GET" || r.URL.Path != "/backend-api/codex/models" || r.Header.Get("Authorization") != "Bearer current" {
				t.Error("catalog sent an expired credential or wrong method")
			}
			data = `{"models":[{"slug":"model-one"}]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	accountTestCall(app, "POST", "/api/accounts/codex/import", "admin", `{"auth_mode":"chatgpt","tokens":{"access_token":"header.eyJleHAiOjF9.signature","refresh_token":"refresh","account_id":"account"}}`)
	w := accountTestCall(app, "GET", "/v1/models", "client", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "codex/model-one") || !refreshed.Load() {
		t.Fatalf("catalog refresh missing: %d %s", w.Code, w.Body.String())
	}
}

func TestWorkBuddyV2AuthorizationKeepsExpiryAndAccountMetadata(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey, cfg.AdminKey = "client", "admin"
	cfg.Backends = []Backend{{ID: "workbuddy", Kind: "builtin", URL: builtinBase("workbuddy"), Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	var refreshes atomic.Int32
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, data := 200, ""
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			data = `{"data":{"authUrl":"https://copilot.tencent.com/login","state":"official-test"}}`
		case "/v2/plugin/auth/token":
			data = `{"data":{"accessToken":"expired","refreshToken":"rotate","domain":"domain-one","expiresAt":1000}}`
		case "/v2/plugin/login/account":
			if r.Header.Get("Authorization") != "Bearer expired" || r.Header.Get("X-Domain") != "domain-one" {
				t.Error("account lookup lost token domain")
			}
			data = `{"data":{"uid":"user-one","enterpriseId":"enterprise-one","departmentInfo":"department-one"}}`
		case "/v2/plugin/auth/token/refresh":
			refreshes.Add(1)
			if r.Header.Get("X-User-Id") != "user-one" || r.Header.Get("X-Enterprise-Id") != "enterprise-one" {
				t.Error("refresh omitted account metadata")
			}
			data = `{"data":{"accessToken":"current","expiresIn":3600}}`
		case "/v2/chat/completions":
			if r.Header.Get("Authorization") != "Bearer current" || r.Header.Get("X-User-Id") != "user-one" || r.Header.Get("X-Department-Info") != "department-one" {
				t.Error("generation lost expiry or account metadata")
			}
			data = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		default:
			status, data = 404, `{}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
	})
	if w := accountTestCall(app, "POST", "/api/accounts/workbuddy/login", "admin", ""); w.Code != 200 {
		t.Fatalf("official v2 auth: %d %s", w.Code, w.Body.String())
	}
	until := time.Now().Add(time.Second)
	for !strings.Contains(accountTestCall(app, "GET", "/api/accounts/workbuddy", "admin", "").Body.String(), `"authorized"`) {
		if time.Now().After(until) {
			t.Fatal("authorization did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"workbuddy/auto","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || refreshes.Load() != 1 {
		t.Fatalf("expiry lost: %d refreshes=%d", w.Code, refreshes.Load())
	}
}
