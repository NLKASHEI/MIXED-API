package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type accountRefresh struct {
	done       chan struct{}
	cancel     context.CancelFunc
	credential accountCredential
	err        error
	finished   time.Time
}
type accountRequestError struct {
	status  int
	message string
}

func (e *accountRequestError) Error() string { return e.message }
func writeAccountError(w http.ResponseWriter, err error) {
	var known *accountRequestError
	if errors.As(err, &known) {
		jsonError(w, known.status, known.message)
		return
	}
	jsonError(w, 502, "无法连接账号服务，请稍后重试")
}

// accountMu must be held. Import/logout invalidates the in-flight generation,
// so even an uncooperative HTTP transport cannot restore an older account.
func (s *Server) cancelRefresh(id string) {
	if flight := s.refreshes[id]; flight != nil {
		flight.cancel()
		delete(s.refreshes, id)
	}
}

// Claims only schedule refresh; provider verification remains authoritative.
func accessExpiry(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return 0
	}
	return claims.Exp
}

func tokenAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Auth struct {
			Account string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.Auth.Account
}

func (s *Server) freshCredential(ctx context.Context, id, rejected string) (accountCredential, error) {
	s.accountMu.Lock()
	c, err := s.credential(id)
	if err != nil {
		s.accountMu.Unlock()
		return c, &accountRequestError{401, "请重新完成本人账号授权"}
	}
	expires := c.Expires
	if expires == 0 {
		expires = accessExpiry(c.AccessToken)
	}
	if rejected != "" && rejected != c.AccessToken || rejected == "" && (expires == 0 || expires > time.Now().Unix()+60) {
		s.accountMu.Unlock()
		return c, nil
	}
	if c.RefreshToken == "" {
		c.NeedsLogin = true
		if s.storeCredential(id, c) != nil {
			s.accountMu.Unlock()
			return c, &accountRequestError{500, "无法保存账号状态"}
		}
		s.accountMu.Unlock()
		return c, &accountRequestError{401, "账号授权已过期，请重新授权"}
	}
	flight := s.refreshes[id]
	if flight == nil || !flight.finished.IsZero() && (flight.err == nil || time.Since(flight.finished) > 5*time.Second) {
		refreshCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		flight = &accountRefresh{done: make(chan struct{}), cancel: cancel}
		if s.refreshes == nil {
			s.refreshes = map[string]*accountRefresh{}
		}
		s.refreshes[id] = flight
		go s.refreshAccount(refreshCtx, id, c, flight)
	}
	s.accountMu.Unlock()
	select {
	case <-ctx.Done():
		return accountCredential{}, ctx.Err()
	case <-flight.done:
		return flight.credential, flight.err
	}
}

func (s *Server) refreshAccount(ctx context.Context, id string, old accountCredential, flight *accountRefresh) {
	defer flight.cancel()
	next, permanent, err := s.exchangeRefresh(ctx, id, old)
	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if s.refreshes[id] != flight {
		err = &accountRequestError{401, "账号授权已更改，请重试请求"}
	} else if err == nil {
		if s.storeCredential(id, next) != nil {
			err = &accountRequestError{500, "无法保存刷新的账号授权"}
		}
	} else if permanent {
		old.NeedsLogin = true
		if s.storeCredential(id, old) != nil {
			err = &accountRequestError{500, "无法保存账号状态"}
		}
		delete(s.logins, id)
	}
	flight.credential, flight.err, flight.finished = next, err, time.Now()
	close(flight.done)
}

func (s *Server) exchangeRefresh(ctx context.Context, id string, old accountCredential) (accountCredential, bool, error) {
	endpoint := codexIssuer + "/oauth/token"
	body := map[string]string{"grant_type": "refresh_token", "client_id": codexOAuthClient, "refresh_token": old.RefreshToken}
	if isWorkBuddyAccount(id) {
		endpoint = accountEndpoint(id) + "/plugin/auth/token/refresh"
		body = map[string]string{}
	}
	raw, _ := json.Marshal(body)
	r, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "MIXED-API/"+BuildVersion)
	if isWorkBuddyAccount(id) {
		r.Header.Set("X-Refresh-Token", old.RefreshToken)
		r.Header.Set("X-Auth-Refresh-Source", "plugin")
		r.Header.Set("X-Product-Code", "codebuddy")
		for key, value := range workbuddyIdentity(old) {
			r.Header.Set(key, value)
		}
	}
	resp, err := providerDo(s.client, r)
	failure := &accountRequestError{503, "账号刷新暂时失败，请稍后重试"}
	if err != nil {
		return accountCredential{}, false, failure
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return accountCredential{}, false, failure
	}
	if resp.StatusCode != 200 {
		var e struct {
			Error json.RawMessage `json:"error"`
		}
		json.Unmarshal(data, &e)
		var code string
		if json.Unmarshal(e.Error, &code) != nil {
			var detail struct {
				Code string `json:"code"`
			}
			json.Unmarshal(e.Error, &detail)
			code = detail.Code
		}
		permanent := resp.StatusCode == 401 || resp.StatusCode == 400 && (code == "invalid_grant" || code == "refresh_token_expired" || code == "refresh_token_reused" || code == "refresh_token_invalidated")
		if permanent {
			return accountCredential{}, true, &accountRequestError{401, "账号授权已失效，请重新授权"}
		}
		return accountCredential{}, false, failure
	}
	next := old
	next.Expires, next.NeedsLogin = 0, false
	if isWorkBuddyAccount(id) {
		values, err := decodeAccountResponse(strings.NewReader(string(data)))
		if err != nil {
			return accountCredential{}, false, failure
		}
		next, err = workbuddyTokens(values, old)
		if err != nil {
			return accountCredential{}, false, failure
		}
	} else {
		var token struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			ID      string `json:"id_token"`
			Expiry  int64  `json:"expires_in"`
		}
		if json.Unmarshal(data, &token) != nil {
			return accountCredential{}, false, failure
		}
		for _, issued := range []string{token.ID, token.Access} {
			if account := tokenAccountID(issued); account != "" && account != old.AccountID {
				return accountCredential{}, true, &accountRequestError{401, "账号身份已更改，请重新授权"}
			}
		}
		next.AccessToken = token.Access
		if token.Refresh != "" {
			next.RefreshToken = token.Refresh
		}
		if token.Expiry > 0 && token.Expiry < 31536000 {
			next.Expires = time.Now().Unix() + token.Expiry
		}
	}
	if !validCredential(next) {
		return accountCredential{}, false, failure
	}
	return next, false, nil
}

func (s *Server) accountJSON(r *http.Request, id, path string, body any) (*http.Response, error) {
	c, err := s.freshCredential(r.Context(), id, "")
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		headers := map[string]string{"Authorization": "Bearer " + c.AccessToken, "User-Agent": "MIXED-API/" + BuildVersion, "Accept": "text/event-stream"}
		if id == "codex" {
			headers["ChatGPT-Account-ID"] = c.AccountID
		}
		if isWorkBuddyAccount(id) {
			for key, value := range workbuddyIdentity(c) {
				headers[key] = value
			}
			if path == "/v3/config" {
				headers["X-Product"] = "SaaS"
				headers["X-Requested-With"] = "XMLHttpRequest"
				product := "CLI/2.63.2 CodeBuddy/2.63.2"
				if id == "workbuddy-intl" {
					product = "WorkBuddy/5.5.2"
				}
				if id == "workbuddy-global" {
					product = "CodeBuddy/2.63.2"
				}
				headers["User-Agent"] = product + " (compatible; MIXED-API/" + BuildVersion + ")"
			}
		}
		var resp *http.Response
		if body == nil {
			endpoint := accountEndpoint(id) + path
			if isWorkBuddyAccount(id) && path == "/v3/config" {
				endpoint = strings.TrimSuffix(accountEndpoint(id), "/v2") + path
			}
			request, buildErr := http.NewRequestWithContext(r.Context(), "GET", endpoint, nil)
			if buildErr != nil {
				return nil, buildErr
			}
			for key, value := range headers {
				request.Header.Set(key, value)
			}
			request.Header.Set("Accept", "application/json")
			resp, err = providerDo(s.client, request)
		} else {
			resp, err = providerJSON(s.client, r, accountEndpoint(id)+path, body, headers)
		}
		// Only an explicit pre-generation authentication rejection is replayable.
		// A transport error or any started stream is returned without replay.
		if err != nil || resp.StatusCode != 401 || attempt != 0 {
			return resp, err
		}
		resp.Body.Close()
		c, err = s.freshCredential(r.Context(), id, c.AccessToken)
		if err != nil {
			return nil, err
		}
	}
}
