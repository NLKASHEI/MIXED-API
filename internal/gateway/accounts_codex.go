package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Public client ID and device flow from openai/codex rust-v0.163.0. This
// implements the protocol in-process; it never installs or launches the CLI.
const codexOAuthClient = "app_EMoamEEZ73f0CkXaXp7hrann"
const codexIssuer = "https://auth.openai.com"

func (s *Server) beginCodexLogin(ctx context.Context) (loginAttempt, error) {
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(probe, "POST", codexIssuer+"/api/accounts/deviceauth/usercode", strings.NewReader(`{"client_id":"`+codexOAuthClient+`"}`))
	r.Header.Set("Content-Type", "application/json")
	resp, err := providerDo(s.client, r)
	if err != nil {
		return loginAttempt{}, errors.New("无法发起 Codex 授权")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return loginAttempt{}, errors.New("Codex 拒绝设备授权，请确认账户允许此登录方式")
	}
	var data struct {
		Device    string          `json:"device_auth_id"`
		Code      string          `json:"user_code"`
		Alternate string          `json:"usercode"`
		Interval  json.RawMessage `json:"interval"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data) != nil || data.Device == "" {
		return loginAttempt{}, errors.New("Codex 授权响应无效")
	}
	if data.Code == "" {
		data.Code = data.Alternate
	}
	if data.Code == "" {
		return loginAttempt{}, errors.New("Codex 未返回设备授权码")
	}
	interval := 5
	var numeric int
	if json.Unmarshal(data.Interval, &numeric) == nil {
		interval = numeric
	} else {
		var text string
		if json.Unmarshal(data.Interval, &text) == nil {
			if n, e := strconv.Atoi(text); e == nil {
				interval = n
			}
		}
	}
	interval = max(1, min(30, interval))
	flowCtx, stop := context.WithTimeout(context.Background(), 15*time.Minute)
	flow := &loginAttempt{Status: "pending", URL: codexIssuer + "/codex/device", Code: data.Code, Message: "在官方页面输入设备码并完成本人授权", cancel: stop}
	s.accountMu.Lock()
	if s.logins == nil {
		s.logins = map[string]*loginAttempt{}
	}
	if old := s.logins["codex"]; old != nil && old.cancel != nil {
		old.cancel()
	}
	s.logins["codex"] = flow
	result := loginAttempt{Status: flow.Status, URL: flow.URL, Code: flow.Code, Message: flow.Message}
	s.accountMu.Unlock()
	go s.pollCodexLogin(flowCtx, flow, data.Device, data.Code, time.Duration(interval)*time.Second)
	return result, nil
}

func (s *Server) pollCodexLogin(ctx context.Context, flow *loginAttempt, device, code string, interval time.Duration) {
	for {
		raw, _ := json.Marshal(map[string]string{"device_auth_id": device, "user_code": code})
		r, _ := http.NewRequestWithContext(ctx, "POST", codexIssuer+"/api/accounts/deviceauth/token", strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		resp, err := providerDo(s.client, r)
		if err == nil {
			status := resp.StatusCode
			var token struct {
				Code     string `json:"authorization_code"`
				Verifier string `json:"code_verifier"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token)
			resp.Body.Close()
			if status == 200 && decodeErr == nil && token.Code != "" && token.Verifier != "" {
				credential, err := s.exchangeCodexToken(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {codexOAuthClient}, "code": {token.Code}, "code_verifier": {token.Verifier}, "redirect_uri": {codexIssuer + "/deviceauth/callback"}})
				s.finishLogin("codex", flow, credential, err)
				return
			}
			if status != 403 && status != 404 && status != 429 {
				s.finishLogin("codex", flow, accountCredential{}, errors.New("authorization denied"))
				return
			}
		}
		select {
		case <-ctx.Done():
			s.finishLogin("codex", flow, accountCredential{}, ctx.Err())
			return
		case <-time.After(interval):
		}
	}
}

func (s *Server) exchangeCodexToken(ctx context.Context, form url.Values) (accountCredential, error) {
	r, _ := http.NewRequestWithContext(ctx, "POST", codexIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := providerDo(s.client, r)
	if err != nil {
		return accountCredential{}, errors.New("Codex 授权交换失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return accountCredential{}, errors.New("Codex 拒绝授权交换")
	}
	var data struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		ID      string `json:"id_token"`
		Expiry  int64  `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data) != nil || data.Access == "" {
		return accountCredential{}, errors.New("Codex 授权响应无效")
	}
	credential := accountCredential{AccessToken: data.Access, RefreshToken: data.Refresh}
	// JWT claims only select the account header. The upstream verifies the
	// issued token; decoding a claim locally never authorizes a gateway route.
	parts := strings.Split(data.ID, ".")
	if len(parts) == 3 {
		if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var claims map[string]json.RawMessage
			if json.Unmarshal(raw, &claims) == nil {
				var auth struct {
					Account string `json:"chatgpt_account_id"`
				}
				if json.Unmarshal(claims["https://api.openai.com/auth"], &auth) == nil {
					credential.AccountID = auth.Account
				}
			}
		}
	}
	if data.Expiry > 0 {
		credential.Expires = time.Now().Unix() + data.Expiry
	}
	if credential.AccountID == "" {
		return accountCredential{}, errors.New("Codex 未返回账号标识")
	}
	return credential, nil
}
