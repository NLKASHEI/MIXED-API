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

func (s *Server) backendState(id, value string) {
	s.mu.Lock()
	s.status["setup:"+id] = value
	s.mu.Unlock()
}
func (s *Server) setupBackend(id string) error {
	if _, ok := runtimePackages[id]; ok {
		return s.installRuntime(id)
	}
	if id == "codex" {
		return s.installCodex()
	}
	if _, ok := binaryUpstreams[id]; ok {
		return s.installBinaryBackend(id)
	}
	if _, ok := managedSources[id]; ok {
		return s.installSource(id)
	}
	return errors.New("不支持的上游")
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
	if _, ok := binaryUpstreams[id]; ok {
		return s.binaryCommand(id)
	}
	if _, ok := managedSources[id]; ok {
		return s.sourceCommand(id)
	}
	return nil, errors.New("不支持一键启动")
}
func (s *Server) launchInstalled(id string) error {
	if id == localEngineID && localEnginePresent() {
		return s.launchLocalEngine()
	}
	if runtime.GOOS != "windows" {
		return errors.New("桌面上游的一键启动只支持 Windows")
	}
	cmd, err := s.installedCommand(id)
	if err != nil {
		return err
	}
	if id == "opencode" {
		dir, e := s.binaryDir(id)
		if e != nil {
			return e
		}
		p, e := findManagedExe(dir, binaryUpstreams[id].executable)
		if e != nil {
			return e
		}
		if e := syncOpenCodeCredentials(filepath.Dir(p), s.config()); e != nil {
			return e
		}
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
	// The managed source installer uses sourceDir, not the historical
	// upstreamDir. Prefer the managed package so the built-in Login button
	// works immediately after one-click deployment.
	dir, err := s.sourceDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir,"package.json"));err!=nil {return errors.New("请先点击一键接入准备 AIStudioToAPI")}
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
	info := map[string]any{"platform": runtime.GOOS, "native_codex_supported": runtime.GOOS == "windows", "tools": map[string]bool{"git": toolAvailable("git"), "node": toolAvailable("node"), "codex": toolAvailable("codex"), "npm": toolAvailable("npm"), "python": toolAvailable("python"), "winget": toolAvailable("winget")}}
	for id, pack := range binaryUpstreams {
		dir, err := s.binaryDir(id)
		if err == nil {
			_, err = findManagedExe(dir, pack.executable)
			info[id+"_installed"] = err == nil
		}
	}
	for id, pack := range managedSources {
		dir, err := s.sourceDir(id)
		if err == nil {
			_, err = os.Stat(filepath.Join(dir, pack.readyFile))
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
		cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "npm install -g @openai/codex@"+reviewedCodexVersion())
		output, err := cmd.CombinedOutput()
		if err != nil {
			s.backendState("codex", "failed: "+briefOutput(output, err))
			return
		}
		s.backendState("codex", "installed；请按登录按钮执行 codex login")
	}()
	return nil
}
