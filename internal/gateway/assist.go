package gateway

// Explicit user-triggered workstation setup helpers. They download known
// upstream repositories at reviewed immutable commits; never auto-login users
// or collect credentials. Non-Windows machines use the documented manual setup.

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

type upstreamPackage struct {
	repository, revision, entry, port string
	npm                               bool
}

var upstreamPackages = map[string]upstreamPackage{
	"workbuddy": {"https://github.com/sitingge/workboddy-proxy.git", "676b64d21a1ff7ea7ca6b4def5850ee61e97a06f", "wb-relay.js", "8790", false},
	"aistudio":  {"https://github.com/iBUHub/AIStudioToAPI.git", "fec035bcaa1f99df967abd2f3e0ed93d07369f50", "main.js", "7860", true},
}

func (s *Server) upstreamDir(id string) (string, error) {
	p, ok := upstreamPackages[id]
	if !ok || p.repository == "" {
		return "", errors.New("此上游不支持一键安装；请按配置向导使用官方安装包")
	}
	home, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "MIXED-API", "upstreams", id), nil
}
func (s *Server) backendState(id, value string) {
	s.mu.Lock()
	s.status["setup:"+id] = value
	s.mu.Unlock()
}
func (s *Server) setupBackend(id string) error {
	if id == "codex" {
		return s.installCodex()
	}
	if runtime.GOOS != "windows" {
		return errors.New("一键部署当前仅支持 Windows；Android 请连接外部运行的上游")
	}
	pack, ok := upstreamPackages[id]
	if !ok {
		return errors.New("此上游暂未提供自动安装，请打开配置向导")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("请先安装 Git for Windows，安装后重启 MIXED-API")
	}
	if _, err := exec.LookPath("node"); err != nil {
		return errors.New("请先安装 Node.js LTS，安装后重启 MIXED-API")
	}
	if pack.npm {
		if _, err := exec.LookPath("npm.cmd"); err != nil {
			return errors.New("缺少 npm.cmd；请重新安装 Node.js LTS")
		}
	}
	dst, err := s.upstreamDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dst, pack.entry)); err == nil {
		return errors.New("已经安装过：如需重新安装请先备份/检查旧项目；不会覆盖你的登录数据")
	}
	s.mu.Lock()
	if s.status["setup:"+id] == "installing" {
		s.mu.Unlock()
		return errors.New("已有安装正在进行")
	}
	s.status["setup:"+id] = "installing"
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		// Fresh folder only; don't mutate existing user's source tree.
		if _, err := os.Stat(dst); err == nil {
			s.backendState(id, "failed: 目标目录已存在，请人工检查")
			return
		}
		clone := exec.CommandContext(ctx, "git", "clone", "--no-checkout", "--filter=blob:none", pack.repository, dst)
		if out, err := clone.CombinedOutput(); err != nil {
			s.backendState(id, "failed: git clone: "+briefOutput(out, err))
			return
		}
		checkout := exec.CommandContext(ctx, "git", "-C", dst, "checkout", "--detach", pack.revision)
		if out, err := checkout.CombinedOutput(); err != nil {
			s.backendState(id, "failed: git checkout: "+briefOutput(out, err))
			return
		}
		if pack.npm {
			s.backendState(id, "installing npm packages")
			cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "npm install --no-audit --no-fund")
			cmd.Dir = dst
			if out, err := cmd.CombinedOutput(); err != nil {
				s.backendState(id, "failed: npm install: "+briefOutput(out, err))
				return
			}
		}
		if id == "aistudio" {
			// AIStudioToAPI otherwise defaults to a public 0.0.0.0 listener
			// and a widely-known API key. Force loopback and a fresh secret.
			key := newKey("sk-mixed-aistudio-")
			data := []byte("HOST=127.0.0.1\nPORT=7860\nAPI_KEYS=" + key + "\n")
			if err := os.WriteFile(filepath.Join(dst, ".env"), data, 0600); err != nil {
				s.backendState(id, "failed: secure .env: "+err.Error())
				return
			}
			s.mu.Lock()
			next := s.cfg
			next.Backends = append([]Backend(nil), next.Backends...)
			for i := range next.Backends {
				if next.Backends[i].ID == id {
					next.Backends[i].Key = key
					next.Backends[i].Auth = "bearer"
				}
			}
			if err := Save(s.path, next); err == nil {
				s.cfg = next
			} else {
				s.mu.Unlock()
				s.backendState(id, "failed: cannot save API key: "+err.Error())
				return
			}
			s.mu.Unlock()
		}
		if id == "workbuddy" {
			s.mu.Lock()
			next := s.cfg
			next.Backends = append([]Backend(nil), next.Backends...)
			for i := range next.Backends {
				if next.Backends[i].ID == id && next.Backends[i].Key == "" {
					next.Backends[i].Auth = "none"
				}
			}
			if Save(s.path, next) == nil {
				s.cfg = next
			}
			s.mu.Unlock()
		}
		s.backendState(id, "installed")
	}()
	return nil
}
func briefOutput(data []byte, err error) string {
	str := strings.TrimSpace(string(data))
	if len(str) > 450 {
		str = str[len(str)-450:]
	}
	if str == "" {
		return err.Error()
	}
	return strings.ReplaceAll(str, "\n", " ")
}
func (s *Server) installedCommand(id string) (*exec.Cmd, error) {
	p, ok := upstreamPackages[id]
	if !ok {
		return nil, errors.New("不支持一键启动")
	}
	dst, err := s.upstreamDir(id)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dst, p.entry)); err != nil {
		return nil, errors.New("尚未安装：请先点击准备环境")
	}
	var cmd *exec.Cmd
	switch id {
	case "workbuddy":
		cmd = exec.Command("node", "wb-relay.js", "--host", "127.0.0.1")
	case "aistudio":
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd.exe", "/d", "/c", "npm start")
		} else {
			cmd = exec.Command("npm", "start")
		}
	default:
		return nil, errors.New("未知后端")
	}
	cmd.Dir = dst
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}
func (s *Server) launchInstalled(id string) error {
	if runtime.GOOS != "windows" {
		return errors.New("桌面上游的一键启动只支持 Windows")
	}
	cmd, err := s.installedCommand(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if _, exists := s.children[id]; exists {
		s.mu.Unlock()
		return nil
	}
	if err = cmd.Start(); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("start: %w", err)
	}
	s.children[id] = cmd
	s.status[id] = "running"
	s.mu.Unlock()
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.children[id] == cmd {
			delete(s.children, id)
			s.status[id] = "exited"
			if err != nil {
				s.status[id] = "exited: " + err.Error()
			}
		}
	}()
	return nil
}
func (s *Server) loginInstalled(id string) error {
	if runtime.GOOS != "windows" {
		return errors.New("浏览器登录向导当前只支持 Windows")
	}
	if id == "codex" {
		if !toolAvailable("codex") {
			return errors.New("请先点击安装 Codex CLI")
		}
		cmd := exec.Command("cmd.exe", "/d", "/c", "start", `"Codex Login"`, "cmd.exe", "/k", "codex login")
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	}
	if id != "aistudio" {
		return errors.New("该后端请在原始桌面软件中完成账号登录")
	}
	dir, err := s.upstreamDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return errors.New("请先一键准备 AIStudioToAPI 环境")
	}
	// Interactive terminal: only the upstream login wizard sees Google credentials.
	cmd := exec.Command("cmd.exe", "/d", "/c", "start", "\"AI Studio Login\"", "cmd.exe", "/k", "npm run setup-auth")
	cmd.Dir = dir
	if err = cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
func toolAvailable(tool string) bool { _, err := exec.LookPath(tool); return err == nil }
func (s *Server) assistStatus() any {
	info := map[string]any{"platform": runtime.GOOS, "native_codex_supported": runtime.GOOS == "windows", "tools": map[string]bool{"git": toolAvailable("git"), "node": toolAvailable("node"), "codex": toolAvailable("codex"), "npm": toolAvailable("npm")}}
	for id := range upstreamPackages {
		if dir, err := s.upstreamDir(id); err == nil {
			_, err = os.Stat(filepath.Join(dir, upstreamPackages[id].entry))
			info[id+"_installed"] = err == nil
		}
	}
	return info
}

func (s *Server) installCodex() error {
	if runtime.GOOS != "windows" {
		return errors.New("一键安装 Codex CLI 目前仅支持 Windows")
	}
	if !toolAvailable("node") {
		return errors.New("请先安装 Node.js LTS：https://nodejs.org/")
	}
	s.mu.Lock()
	if s.status["setup:codex"] == "installing" {
		s.mu.Unlock()
		return errors.New("Codex 安装进行中")
	}
	s.status["setup:codex"] = "installing"
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "npm install -g @openai/codex@latest")
		output, err := cmd.CombinedOutput()
		if err != nil {
			s.backendState("codex", "failed: "+briefOutput(output, err))
			return
		}
		s.backendState("codex", "installed；请按登录按钮执行 codex login")
	}()
	return nil
}
