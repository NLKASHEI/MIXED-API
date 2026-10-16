package gateway

// One-click orchestration: the user chooses a provider once, MIXED-API handles
// provisioning, generating local credentials, enabling the backend, and
// launching the managed child. Account authorization is never synthesized.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
 if id==localEngineID && localEnginePresent() {return true}
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
	if id == localEngineID && localEnginePresent() {
		if err := s.enableBackend(id); err != nil {
			return err
		}
		return s.launchLocalEngine()
	}
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
			s.backendState(id, "checking: 正在检查 Codex CLI 的 ChatGPT 订阅授权")
			go s.finishCodexQuickstart()
			return nil
		}
		if err := s.launchInstalled(id); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return err
		}
		s.backendState(id, "checking: 进程已启动；正在确认模型 API 可用")
		go s.verifyManagedBackend(id, 90*time.Second)
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
					s.backendState(id, "checking: Codex CLI 已安装；正在启动官方账号授权或检查已登录状态")
					go s.finishCodexQuickstart()
					return
				}
				if err := s.launchInstalled(id); err != nil {
					s.backendState(id, "failed: 已完成安装，但启动失败: "+err.Error())
					return
				}
				s.backendState(id, "checking: 已自动部署并启动；正在验证模型接口")
				go s.verifyManagedBackend(id, 90*time.Second)
				return
			}
		}
	}()
	return nil
}

// probeManagedModels tests the actual authenticated upstream model endpoint.
// Starting a child process is not evidence that the user can chat with it.
// Keys stay inside request headers; errors/status never print credentials.
func (s *Server) probeManagedModels(ctx context.Context, id string) (int, int, error) {
	cfg := s.config()
	for _, b := range cfg.Backends {
		if b.ID != id || !b.Enabled {
			continue
		}
		req, err := s.backendRequest(ctx, b, http.MethodGet, "/models", nil)
		if err != nil {
			return 0, 0, err
		}
		response, err := s.client.Do(req)
		if err != nil {
			return 0, 0, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return 0, response.StatusCode, fmt.Errorf("upstream HTTP %d", response.StatusCode)
		}
		var responseData struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&responseData); err != nil {
			return 0, response.StatusCode, err
		}
		count := 0
		for _, model := range responseData.Data {
			if model.ID != "" {
				count++
			}
		}
		return count, http.StatusOK, nil
	}
	return 0, 0, errors.New("backend not enabled")
}

// verifyManagedBackend runs only for the providers started by MIXED-API.
// Await user authorization if necessary; never mark "ready" before models exist.
func (s *Server) verifyManagedBackend(id string, maxWait time.Duration) {
	end := time.Now().Add(maxWait)
	sawEmptyModels := false
	lastError := "upstream not ready"
	for {
		s.mu.RLock()
		processState := s.status[id]
		s.mu.RUnlock()
		if strings.HasPrefix(processState, "exited") {
			s.backendState(id, "failed: 上游进程已经退出。请检查本地日志或依赖")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		count, status, err := s.probeManagedModels(ctx, id)
		cancel()
		if err == nil && count > 0 {
			s.backendState(id, fmt.Sprintf("ready: 后端已启动，模型 API 验证成功（%d 个模型）", count))
			return
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			s.backendState(id, "needs_auth: 上游要求登录或授权，请在本卡片完成官方授权后刷新模型")
			return
		}
		if err == nil && count == 0 {
			sawEmptyModels = true
		} else if err != nil {
			lastError = err.Error()
		}
		if time.Now().After(end) {
			if sawEmptyModels {
				s.backendState(id, "needs_auth: 已连接上游，但未发现可用模型。请检查账号授权或上游自身的模型列表")
			} else {
				s.backendState(id, "failed: 进程已启动，但模型 API 检测超时或不可达（"+lastError+"）")
			}
			return
		}
		delay := 2 * time.Second
		if remaining := time.Until(end); remaining < delay {
			delay = remaining
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
}

// Only check the human-readable status of the official CLI, never open,
// copy or persist its OAuth credentials. API-key logins do not count.
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

// One click launches the provider's own authorization flow if necessary,
// then waits for the user to complete it instead of requiring a second click.
func (s *Server) finishCodexQuickstart() {
	const id = "codex"
	if !toolAvailable("codex") {
		s.backendState(id, "needs_runtime: Codex CLI 已安装但当前程序还找不到它；请关闭并重新启动 MIXED-API，再点一键接入")
		return
	}
	if codexSubscriptionReady() {
		s.backendState(id, "ready: ChatGPT 订阅已授权；可以刷新聚合模型")
		return
	}
	if err := s.loginInstalled(id); err != nil {
		s.backendState(id, "needs_auth: 无法打开 Codex 官方登录窗口："+err.Error())
		return
	}
	s.backendState(id, "auth_pending: 已自动打开 Codex 官方登录窗口，请在窗口中完成 ChatGPT 账号授权；无需把 Token 填入本软件")
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		deadline := time.NewTimer(12 * time.Minute)
		defer deadline.Stop()
		for {
			select {
			case <-deadline.C:
				s.backendState(id, "needs_auth: 尚未检测到 ChatGPT 订阅授权；完成 Codex 官方登录后再次点击一键接入")
				return
			case <-ticker.C:
				if codexSubscriptionReady() {
					s.backendState(id, "ready: 已确认 ChatGPT 订阅登录，Codex 模型已可刷新")
					return
				}
			}
		}
	}()
}
