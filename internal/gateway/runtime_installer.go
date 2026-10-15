package gateway

// Optional Windows package-manager orchestration. This runs only after an
// explicit click in the local admin console; it never installs anything just
// because a website or API client connected to the gateway.

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var runtimePackages = map[string]string{
	"runtime-node":   "OpenJS.NodeJS.LTS",
	"runtime-python": "Python.Python.3.12",
}

func (s *Server) installRuntime(id string) error {
	if runtime.GOOS != "windows" {
		return errors.New("仅支持 Windows 上由用户主动启动的运行时安装")
	}
	pack, ok := runtimePackages[id]
	if !ok {
		return errors.New("未知运行时")
	}
	if !toolAvailable("winget") {
		return errors.New("Windows Package Manager (winget) 不可用，无法安全自动安装运行时")
	}
	s.mu.Lock()
	if strings.HasPrefix(s.status["setup:"+id], "installing") {
		s.mu.Unlock()
		return errors.New("安装已经开始")
	}
	s.status["setup:"+id] = "installing"
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "winget", "install", "--id", pack, "--exact", "--source", "winget", "--scope", "user", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity")
		s.backendState(id, "installing: 正在通过 Windows Package Manager 安装官方运行时")
		out, err := cmd.CombinedOutput()
		if err != nil {
			s.backendState(id, "failed: winget: "+briefOutput(out, err))
			return
		}
		s.backendState(id, "installed: 运行时安装完毕；请重启 MIXED-API 后再次一键部署")
	}()
	return nil
}
