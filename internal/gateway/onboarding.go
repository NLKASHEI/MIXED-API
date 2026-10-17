package gateway

import (
	"context"
	"errors"
	"strings"
)

// Preparation is local and offline. It never installs a runtime or starts an
// upstream application. Existing-service mode remains an explicit alternative.
func (s *Server) prepareBackend(id string) error {
	switch id {
	case "vertex", "opencode", "workbuddy", "codex":
		return s.prepareIntegrated(id)
	}
	return errors.New("该来源的内置核心尚未完成")
}

func (s *Server) activateBackend(_ context.Context, id string) error {
	for _, b := range s.config().Backends {
		if b.ID != id {
			continue
		}
		if b.Kind != "builtin" {
			return errors.New("请先准备内置来源，或保存已有服务配置")
		}
		if id == "vertex" && strings.TrimSpace(b.ProviderKey) == "" {
			return errors.New("请填写 Express API Key")
		}
		if id == "workbuddy" || id == "codex" {
			if _, err := s.credential(id); err != nil {
				return err
			}
		}
		return s.enableBackend(id)
	}
	return errors.New("来源不存在")
}
