package gateway

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"time"
)

// prepareBackend installs dependencies without treating installation as login
// or starting a provider before the current onboarding step asks for it.
func (s *Server) prepareBackend(id string) error {
	if runtime.GOOS != "windows" {
		return errors.New("手机请连接电脑网关，或接入已有 HTTP 服务")
	}
	if id != "codex" {
		_, binary := binaryUpstreams[id]
		_, source := managedSources[id]
		if !binary && !source {
			return errors.New("未知来源")
		}
	}
	if s.managedPresent(id) {
		if err := s.restorePreparedConnection(id); err != nil {
			return err
		}
		s.backendState(id, "installed: 运行组件已准备，下一步完成本人授权")
		return nil
	}
	s.mu.RLock()
	state := s.status["setup:"+id]
	s.mu.RUnlock()
	if strings.HasPrefix(state, "installing") {
		return errors.New("正在准备，请等待当前步骤完成")
	}
	missing := ""
	if id == "vertex" {
		if _, err := findPython(); err != nil {
			missing = "runtime-python"
		}
	} else if id == "codex" || id == "workbuddy" || id == "aistudio" {
		if !toolAvailable("node") {
			missing = "runtime-node"
		}
	}
	if missing != "" {
		if err := s.installRuntime(missing); err != nil {
			return err
		}
		s.backendState(id, "needs_runtime: 系统运行环境安装窗口已打开。安装完成后关闭并重新打开 MIXED，再继续第 1 步。")
		return nil
	}
	return s.setupBackend(id)
}

// Activation happens only after the user confirms the authorization step.
// Credential-dependent services are restarted to pick up newly saved official
// keys; all other running processes retain their existing login/consent session.
func (s *Server) activateBackend(ctx context.Context, id string) error {
	if err := s.enableBackend(id); err != nil {
		return err
	}
	if id == "codex" {
		return nil
	}
	if id == "vertex" || id == "opencode" {
		s.stopProcess(id)
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			s.mu.RLock()
			_, running := s.children[id]
			s.mu.RUnlock()
			if !running {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return errors.New("原服务还未退出，请稍后重试本步骤")
			case <-ticker.C:
			}
		}
	}
	return s.launchInstalled(id)
}
