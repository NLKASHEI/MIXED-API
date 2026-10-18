package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func workbuddyAnonymousHeaders(h http.Header) {
	h.Set("User-Agent", "MIXED-API/"+BuildVersion)
	h.Set("X-Product-Code", "codebuddy")
	for _, key := range []string{"X-No-Authorization", "X-No-User-Id", "X-No-Enterprise-Id", "X-No-Department-Info"} {
		h.Set(key, "true")
	}
}
func workbuddyIdentity(c accountCredential) map[string]string {
	h := map[string]string{"X-Product-Code": "codebuddy"}
	if c.Domain == "" {
		id := c.Site
		if id == "" {
			id = "workbuddy"
		}
		if isWorkBuddyAccount(id) {
			u, _ := url.Parse(accountEndpoint(id))
			c.Domain = u.Hostname()
		}
	}
	for key, value := range map[string]string{"X-Domain": c.Domain, "X-User-Id": c.UserID, "X-Enterprise-Id": c.EnterpriseID, "X-Tenant-Id": c.EnterpriseID, "X-Department-Info": c.Department} {
		if value != "" {
			h[key] = value
		}
	}
	return h
}
func accountNumber(v any) int64 {
	switch value := v.(type) {
	case float64:
		if value > 0 && value < 1e16 && value == float64(int64(value)) {
			return int64(value)
		}
	case json.Number:
		n, _ := value.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(value, 10, 64)
		return n
	}
	return 0
}
func workbuddyTokens(data map[string]any, c accountCredential) (accountCredential, error) {
	token, _ := data["accessToken"].(string)
	if token == "" {
		return c, errors.New("账号令牌响应无效")
	}
	c.AccessToken = token
	c.Expires = 0
	c.NeedsLogin = false
	if value, _ := data["refreshToken"].(string); value != "" {
		c.RefreshToken = value
	}
	if value, _ := data["domain"].(string); value != "" {
		c.Domain = value
	}
	if value := accountNumber(data["expiresAt"]); value > 0 {
		c.Expires = value / 1000
	}
	if value := accountNumber(data["expiresIn"]); c.Expires == 0 && value > 0 && value < 31536000 {
		c.Expires = time.Now().Unix() + value
	}
	return c, nil
}
func (s *Server) workbuddyAccount(ctx context.Context, id, state string, c accountCredential) (accountCredential, error) {
	c.Site = id
	r, _ := http.NewRequestWithContext(ctx, "GET", accountEndpoint(id)+"/plugin/login/account?state="+url.QueryEscape(state), nil)
	workbuddyAnonymousHeaders(r.Header)
	r.Header.Del("X-No-Authorization")
	r.Header.Set("Authorization", "Bearer "+c.AccessToken)
	for key, value := range workbuddyIdentity(c) {
		r.Header.Set(key, value)
	}
	resp, err := providerDo(s.client, r)
	if err != nil {
		return c, errors.New("账号资料暂未就绪")
	}
	defer resp.Body.Close()
	data, err := decodeAccountResponse(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		return c, errors.New("账号资料暂未就绪")
	}
	value := func(key string) string {
		if v, ok := data[key].(string); ok {
			return v
		}
		if n := accountNumber(data[key]); n > 0 {
			return strconv.FormatInt(n, 10)
		}
		return ""
	}
	c.UserID, c.EnterpriseID, c.Department = value("uid"), value("enterpriseId"), value("departmentInfo")
	if c.UserID == "" {
		return c, errors.New("账号资料暂未就绪")
	}
	return c, nil
}
