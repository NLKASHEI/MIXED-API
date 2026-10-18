package desktop

import (
	"net/url"
	"strings"
)

func localNavigation(base, target string) bool {
	b, e := url.Parse(base)
	if e != nil {
		return false
	}
	u, e := url.Parse(target)
	if e != nil || u.User != nil {
		return false
	}
	return u.Scheme == "http" && u.Scheme == b.Scheme && u.Host == b.Host
}

func externalLink(target string) bool {
	u, e := url.Parse(target)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(u.Host) {
	case "github.com", "cloud.google.com", "console.cloud.google.com", "aistudio.google.com", "auth.openai.com", "chatgpt.com", "opencode.ai", "www.codebuddy.cn", "www.workbuddy.ai", "copilot.tencent.com":
		return true
	}
	return false
}
