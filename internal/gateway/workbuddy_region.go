package gateway

// Regional accounts are private storage slots, not extra public backends.
// The domestic slot retains the old filename for existing installations.
func workbuddyAccountID(b Backend) string {
	if b.ID == "workbuddy" && b.WorkBuddyRegion == "intl" {
		return "workbuddy-intl"
	}
	if b.ID == "workbuddy" && b.WorkBuddyRegion == "global" {
		return "workbuddy-global"
	}
	return b.ID
}
func isWorkBuddyAccount(id string) bool {
	return id == "workbuddy" || id == "workbuddy-global" || id == "workbuddy-intl"
}
func accountEndpoint(id string) string {
	if id == "workbuddy-intl" {
		return "https://www.workbuddy.ai/v2"
	}
	if id == "workbuddy-global" {
		return "https://www.codebuddy.ai/v2"
	}
	return builtinBase(id)
}
func (s *Server) activeAccountID(id string) string {
	if id == "workbuddy" {
		for _, b := range s.config().Backends {
			if b.ID == id {
				return workbuddyAccountID(b)
			}
		}
	}
	return id
}
func workbuddyAuthHost(id, host string) bool {
	if id == "workbuddy-intl" {
		return host == "www.workbuddy.ai" || host == "workbuddy.ai"
	}
	if id == "workbuddy-global" {
		return host == "www.codebuddy.ai" || host == "codebuddy.ai"
	}
	return host == "copilot.tencent.com" || host == "www.codebuddy.cn"
}

func workbuddyPlatform(id string) string {
	if id == "workbuddy-intl" {
		return "workbuddy-ai"
	}
	if id == "workbuddy-global" {
		return "VSCode"
	}
	return "workbuddy"
}
