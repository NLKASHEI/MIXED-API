package gateway

// One-click orchestration: the user chooses a provider once, MIXED-API handles
// provisioning, generating local credentials, enabling the backend, and
// launching the managed child. Account authorization is never synthesized.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (s *Server) enableBackend(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Backends = append([]Backend(nil), s.cfg.Backends...)
	found := false
	for i := range next.Backends {
		if next.Backends[i].ID == id {
			next.Backends[i].Enabled = true
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown backend %q", id)
	}
	if err := Save(s.path, next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

func (s *Server) managedPresent(id string) bool {
	if id == "codex" {
		return toolAvailable("codex")
	}
	if pack, ok := binaryUpstreams[id]; ok {
		dir, e := s.binaryDir(id)
		if e != nil {
			return false
		}
		_, e = findManagedExe(dir, pack.executable)
		return e == nil
	}
	if pack, ok := managedSources[id]; ok {
		dir, e := s.sourceDir(id)
		if e != nil {
			return false
		}
		_, e = os.Stat(filepath.Join(dir, pack.readyFile))
		return e == nil
	}
	return false
}

func (s *Server) quickstartBackend(id string) error {
	if runtime.GOOS != "windows" {
		return errors.New("此版本的本机托管仅支持 Windows，Android 请连接电脑 MIXED-API")
	}
	if id != "codex" {
		if _, ok := binaryUpstreams[id]; !ok {
			if _, ok := managedSources[id]; !ok {
				return errors.New("暂不支持此来源的一键托管")
			}
		}
	}
	s.mu.Lock()
	status := s.status["setup:"+id]
	if strings.HasPrefix(status, "installing") || strings.HasPrefix(status, "quickstart") || strings.HasPrefix(status, "auth_pending:") {
		s.mu.Unlock()
		return errors.New("已有部署任务正在进行")
	}
	s.status["setup:"+id] = "quickstart: preparing"
	s.mu.Unlock()
	if s.managedPresent(id) {
		if err := s.enableBackend(id); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return err
		}
		if id == "codex" {
			s.finishCodexQuickstart()
			return nil
		}
		if err := s.launchInstalled(id); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return err
		}
		s.backendState(id, "ready: 已启动；可以刷新聚合模型")
		return nil
	}
	// Dependency setup cannot update the PATH of an already running Windows
	// process. Offer the official winget installation as an explicit next step
	// rather than claiming the remaining install magically completed.
	missingRuntime := ""
	if id == "vertex" {
		if _, err := findPython(); err != nil {
			missingRuntime = "runtime-python"
		}
	} else if id == "codex" || id == "workbuddy" || id == "aistudio" {
		if _, err := exec.LookPath("node"); err != nil {
			missingRuntime = "runtime-node"
		}
	}
	if missingRuntime != "" {
		if err := s.installRuntime(missingRuntime); err != nil {
			s.backendState(id, "needs_runtime: Windows 运行时安装不可用: "+err.Error())
			return err
		}
		s.backendState(id, "needs_runtime: 已自动启动官方运行时安装；完成后重启 MIXED-API，再按一键接入（Windows 不会立即刷新运行中程序的 PATH）")
		return nil
	}
	if err := s.setupBackend(id); err != nil {
		s.backendState(id, "failed: "+err.Error())
		return err
	}
	go func() {
		deadline := time.NewTimer(33 * time.Minute)
		defer deadline.Stop()
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline.C:
				s.backendState(id, "failed: 自动部署超时。已安装文件会保留供检查")
				return
			case <-ticker.C:
				s.mu.RLock()
				state := s.status["setup:"+id]
				s.mu.RUnlock()
				if strings.HasPrefix(state, "failed:") || strings.HasPrefix(state, "needs_runtime:") {
					return
				}
				if !strings.HasPrefix(state, "installed") {
					continue
				}
				if err := s.enableBackend(id); err != nil {
					s.backendState(id, "failed: 保存启用状态失败: "+err.Error())
					return
				}
				if id == "codex" {
					s.finishCodexQuickstart()
					return
				}
				if err := s.launchInstalled(id); err != nil {
					s.backendState(id, "failed: 已完成安装，但启动失败: "+err.Error())
					return
				}
				s.backendState(id, "ready: 自动安装、配置、启动完成。需要官方登录的来源请完成授权")
				return
			}
		}
	}()
	return nil
}

// The official CLI owns its tokens. We only read a non-secret login summary.
// Reject API-key logins; only ChatGPT subscription sign-in is eligible.
func codexStatusIsSubscription(output string) bool {
    out := strings.ToLower(output)
    return strings.Contains(out, "logged in") && strings.Contains(out, "chatgpt") && !strings.Contains(out, "api key")
}

func codexSubscriptionReady() bool {
    ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
    defer cancel()
    cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "codex", "login", "status")
    out, err := cmd.CombinedOutput()
    return err == nil && codexStatusIsSubscription(string(out))
}

// The user makes one click. We launch the official login when required and
// observe only its success state, never collecting or storing auth tokens.
func (s *Server) finishCodexQuickstart() {
    const id = "codex"
    if !toolAvailable("codex") {
        s.backendState(id, "needs_runtime: 安装后当前进程找不到 Codex CLI，请重启 MIXED-API 再点一次一键接入")
        return
    }
    if codexSubscriptionReady() {
        s.backendState(id, "ready: 已确认 ChatGPT 订阅登录，可以刷新 Codex 模型")
        return
    }
    if err := s.loginInstalled(id); err != nil {
        s.backendState(id, "needs_auth: 无法打开官方 Codex 登录窗口："+err.Error())
        return
    }
    s.backendState(id, "auth_pending: 已自动打开官方登录窗口，请本人完成 ChatGPT 授权；无需填写任何 Token")
    go func() {
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        deadline := time.NewTimer(12 * time.Minute)
        defer deadline.Stop()
        for {
            select {
            case <-deadline.C:
                s.backendState(id, "needs_auth: 尚未检测到 ChatGPT 登录；完成官方授权后重新点一键接入")
                return
            case <-ticker.C:
                if codexSubscriptionReady() {
                    s.backendState(id, "ready: ChatGPT 官方登录完成，已启用 Codex，模型可以刷新")
                    return
                }
            }
        }
    }()
}
