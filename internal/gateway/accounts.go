package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type accountCredential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	Domain       string `json:"domain,omitempty"`
	Expires      int64  `json:"expires,omitempty"`
}
type loginAttempt struct {
	Status  string `json:"status"`
	URL     string `json:"url,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	cancel  context.CancelFunc
}

func accountProvider(id string) bool { return id == "workbuddy" || id == "codex" }
func (s *Server) accountFile(id string) string {
	return filepath.Join(filepath.Dir(s.path), ".mixed-accounts", id+".dat")
}
func (s *Server) credential(id string) (accountCredential, error) {
	if !accountProvider(id) {
		return accountCredential{}, errors.New("来源不支持账户存储")
	}
	raw, err := os.ReadFile(s.accountFile(id))
	if err != nil {
		return accountCredential{}, errors.New("请先完成本人账号授权")
	}
	raw, err = unprotectSecret(raw)
	if err != nil {
		return accountCredential{}, err
	}
	var c accountCredential
	if json.Unmarshal(raw, &c) != nil || c.AccessToken == "" {
		return c, errors.New("账号凭证无效，请重新授权")
	}
	return c, nil
}
func (s *Server) storeCredential(id string, c accountCredential) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	raw, err = protectSecret(raw)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.accountFile(id))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".account-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), s.accountFile(id))
}
func (s *Server) accountState(id string) loginAttempt {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if state := s.logins[id]; state != nil {
		return loginAttempt{Status: state.Status, URL: state.URL, Code: state.Code, Message: state.Message}
	}
	if _, err := s.credential(id); err == nil {
		return loginAttempt{Status: "authorized"}
	}
	return loginAttempt{Status: "none"}
}
func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	id, action, _ := strings.Cut(target, "/")
	if id == "aistudio" || id == "aps" {
		s.handleGoogleAccount(w, r, id, action)
		return
	}
	if !accountProvider(id) {
		jsonError(w, 404, "来源没有此账号接口")
		return
	}
	if r.Method == "GET" && action == "" {
		jsonResult(w, 200, s.accountState(id))
		return
	}
	if r.Method == "POST" && action == "login" {
		state, err := s.beginAccountLogin(r.Context(), id)
		if err != nil {
			jsonError(w, 400, err.Error())
			return
		}
		jsonResult(w, 200, state)
		return
	}
	if r.Method == "POST" && action == "import" && id == "codex" {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var source struct {
			Mode   string             `json:"auth_mode"`
			Tokens *accountCredential `json:"tokens"`
		}
		if json.NewDecoder(r.Body).Decode(&source) != nil || source.Mode != "chatgpt" || source.Tokens == nil || source.Tokens.AccessToken == "" || source.Tokens.AccountID == "" {
			jsonError(w, 400, "请选择本人的 ChatGPT 授权文件，需包含账号标识与访问凭证")
			return
		}
		s.accountMu.Lock()
		defer s.accountMu.Unlock()
		if old := s.logins[id]; old != nil && old.cancel != nil {
			old.cancel()
		}
		delete(s.logins, id)
		if err := s.storeCredential(id, *source.Tokens); err != nil {
			jsonError(w, 500, "无法保存账号授权")
			return
		}
		jsonResult(w, 200, map[string]string{"status": "authorized"})
		return
	}
	if r.Method == "DELETE" && action == "" {
		s.accountMu.Lock()
		defer s.accountMu.Unlock()
		if state := s.logins[id]; state != nil && state.cancel != nil {
			state.cancel()
		}
		delete(s.logins, id)
		if err := os.Remove(s.accountFile(id)); err != nil && !os.IsNotExist(err) {
			jsonError(w, 500, "无法清除本机授权")
			return
		}
		jsonResult(w, 200, map[string]bool{"ok": true})
		return
	}
	jsonError(w, 404, "Unknown account action")
}

func (s *Server) beginAccountLogin(ctx context.Context, id string) (loginAttempt, error) {
	if id == "codex" {
		return s.beginCodexLogin(ctx)
	}
	if id != "workbuddy" {
		return loginAttempt{}, errors.New("该来源的内置授权流程尚未完成")
	}
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(probe, "POST", "https://copilot.tencent.com/plugin/auth/state?platform=workbuddy", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(r)
	if err != nil {
		return loginAttempt{}, errors.New("无法打开 WorkBuddy 授权流程")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return loginAttempt{}, errors.New("WorkBuddy 拒绝发起授权")
	}
	data, err := decodeAccountResponse(resp.Body)
	if err != nil {
		return loginAttempt{}, err
	}
	link, _ := data["authUrl"].(string)
	state, _ := data["state"].(string)
	u, err := url.Parse(link)
	if err != nil || u.User != nil || u.Scheme != "https" || (u.Host != "copilot.tencent.com" && u.Host != "www.codebuddy.cn") || state == "" {
		return loginAttempt{}, errors.New("WorkBuddy 返回无效授权地址")
	}
	flowCtx, stop := context.WithTimeout(context.Background(), 5*time.Minute)
	flow := &loginAttempt{Status: "pending", URL: link, Message: "请在官方页面完成本人授权", cancel: stop}
	s.accountMu.Lock()
	if s.logins == nil {
		s.logins = map[string]*loginAttempt{}
	}
	if old := s.logins[id]; old != nil && old.cancel != nil {
		old.cancel()
	}
	s.logins[id] = flow
	result := loginAttempt{Status: flow.Status, URL: flow.URL, Message: flow.Message}
	s.accountMu.Unlock()
	go s.pollWorkBuddyLogin(flowCtx, flow, state)
	return result, nil
}

func decodeAccountResponse(body io.Reader) (map[string]any, error) {
	var data map[string]any
	if json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&data) != nil {
		return nil, errors.New("服务方授权响应无效")
	}
	for n := 0; n < 3; n++ {
		nested, ok := data["data"].(map[string]any)
		if !ok {
			break
		}
		data = nested
	}
	return data, nil
}
func (s *Server) finishLogin(id string, flow *loginAttempt, c accountCredential, err error) {
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if s.logins[id] != flow {
		return
	}
	if err == nil {
		err = s.storeCredential(id, c)
	}
	flow.URL, flow.Code = "", ""
	if flow.cancel != nil {
		flow.cancel()
	}
	if err != nil {
		flow.Status = "failed"
		flow.Message = "授权未完成，请重新登录"
	} else {
		flow.Status = "authorized"
		flow.Message = "已授权"
	}
}
func (s *Server) pollWorkBuddyLogin(ctx context.Context, flow *loginAttempt, state string) {
	for {
		r, _ := http.NewRequestWithContext(ctx, "GET", "https://copilot.tencent.com/plugin/auth/token?state="+url.QueryEscape(state), nil)
		resp, err := s.client.Do(r)
		if err == nil {
			data, decodeErr := decodeAccountResponse(resp.Body)
			resp.Body.Close()
			if decodeErr == nil && resp.StatusCode == 200 {
				token, _ := data["accessToken"].(string)
				if token != "" {
					refresh, _ := data["refreshToken"].(string)
					domain, _ := data["domain"].(string)
					s.finishLogin("workbuddy", flow, accountCredential{AccessToken: token, RefreshToken: refresh, Domain: domain}, nil)
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			s.finishLogin("workbuddy", flow, accountCredential{}, ctx.Err())
			return
		case <-time.After(2 * time.Second):
		}
	}
}
