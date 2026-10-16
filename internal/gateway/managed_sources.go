package gateway

// Optional Python / Node upstreams are installed on demand from immutable
// Git commits. A click initiates the download, dependency setup and local
// configuration. The upstream licenses and logins are not bypassed.

import (
	"context"
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

type sourcePackage struct {
	repo, commit, readyFile string
	needsNPM                bool
}

var managedSources = sourcePackages()

func (s *Server) sourceDir(id string) (string, error) {
	if _, ok := managedSources[id]; !ok {
		return "", errors.New("unmanaged source")
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "MIXED-API", "upstreams", id), nil
}
func (s *Server) installSource(id string) error {
	if runtime.GOOS != "windows" {
		return errors.New("此上游一键部署目前只支持 Windows；Android 可连接电脑的 MIXED-API 网关")
	}
	pack, ok := managedSources[id]
	if !ok {
		return errors.New("unmanaged source")
	}
	if pack.needsNPM || id == "workbuddy" {
		if !toolAvailable("node") {
			return errors.New("需要 Node.js 环境：点击「安装运行环境」让 MIXED-API 自动安装 Node.js，再重新操作")
		}
	}
	if id == "vertex" {
		if _, err := findPython(); err != nil {
			return errors.New("需要 Python 3.11+ 环境：点击「安装运行环境」让 MIXED-API 准备 Python 后重试")
		}
	}
	dst, err := s.sourceDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dst, pack.readyFile)); err == nil {
		return errors.New("已安装；点击启动，不覆盖现有账号及配置")
	}
	s.mu.Lock()
	if strings.HasPrefix(s.status["setup:"+id], "installing") {
		s.mu.Unlock()
		return errors.New("安装正在进行")
	}
	s.status["setup:"+id] = "installing"
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		if _, err := os.Stat(dst); err == nil {
			s.backendState(id, "failed: 目录已存在，避免覆盖登录资料")
			return
		}
		stage, err := os.MkdirTemp(filepath.Dir(dst), ".mixed-source-")
		if err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		defer os.RemoveAll(stage)
		archive := filepath.Join(stage, "source.zip")
		srcURL := fmt.Sprintf("https://github.com/%s/archive/%s.zip", pack.repo, pack.commit)
		s.backendState(id, "installing: 正在下载固定 Git commit 的源码快照")
		if err := downloadCommitSource(ctx, srcURL, archive); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		extracted := filepath.Join(stage, "source")
		if err := safeZipExtract(archive, extracted); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		source, err := findSourceRoot(extracted, pack.readyFile)
		if err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		if pack.needsNPM {
			s.backendState(id, "installing: 正在安装 Node 依赖（首次需要几分钟）")
			cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "npm install --no-audit --no-fund")
			cmd.Dir = source
			if out, e := cmd.CombinedOutput(); e != nil {
				s.backendState(id, "failed: npm install: "+briefOutput(out, e))
				return
			}
		}
		if id == "vertex" {
			s.backendState(id, "installing: 正在建立 Python 环境并安装 Vertex 依赖")
			python, e := findPython()
			if e != nil {
				s.backendState(id, "failed: "+e.Error())
				return
			}
			venv := filepath.Join(source, ".mixed-venv")
			cmd := exec.CommandContext(ctx, python, "-m", "venv", venv)
			cmd.Dir = source
			if out, e := cmd.CombinedOutput(); e != nil {
				s.backendState(id, "failed: Python venv: "+briefOutput(out, e))
				return
			}
			envPython := filepath.Join(venv, "Scripts", "python.exe")
			cmd = exec.CommandContext(ctx, envPython, "-m", "pip", "install", "--disable-pip-version-check", "-r", "app/requirements.txt")
			cmd.Dir = source
			if out, e := cmd.CombinedOutput(); e != nil {
				s.backendState(id, "failed: Python pip: "+briefOutput(out, e))
				return
			}
			if e := copyFilePrivate(filepath.Join(source, "vertexModels.json"), filepath.Join(source, "app", "vertexModels.json")); e != nil {
				s.backendState(id, "failed: "+e.Error())
				return
			}
		}
		key := ""
		if id == "vertex" {
			key = newKey("sk-mix-vertex-")
			if err := os.WriteFile(filepath.Join(source, ".mixed-secret"), []byte(key), 0600); err != nil {
				s.backendState(id, "failed: "+err.Error())
				return
			}
		}
		if id == "aistudio" {
			key = newKey("sk-mix-ai-")
			body := []byte("HOST=127.0.0.1\nPORT=7860\nAPI_KEYS=" + key + "\n")
			if err := os.WriteFile(filepath.Join(source, ".env"), body, 0600); err != nil {
				s.backendState(id, "failed: "+err.Error())
				return
			}
		}
		if err := os.Rename(source, dst); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		if key != "" {
			if err := s.saveGeneratedBackend(id, key); err != nil {
				s.backendState(id, "failed: "+err.Error())
				return
			}
		}
		if id == "workbuddy" {
			s.ensureNoAuth(id)
		}
		s.backendState(id, "installed: 后端安装完成；如需登录请点授权，再点启动")
	}()
	return nil
}

func downloadCommitSource(ctx context.Context, url, target string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 8 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("snapshot HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxManagedArchive+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxManagedArchive {
		return errors.New("source ZIP too large")
	}
	return nil
}
func findSourceRoot(dir, marker string) (string, error) {
	direct := filepath.Join(dir, filepath.FromSlash(marker))
	if _, err := os.Stat(direct); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			d := filepath.Join(dir, e.Name())
			if _, err := os.Stat(filepath.Join(d, filepath.FromSlash(marker))); err == nil {
				return d, nil
			}
		}
	}
	return "", errors.New("固定源码快照缺少启动文件")
}
func findPython() (string, error) {
	for _, candidate := range []string{"python", "python3"} {
		if p, err := exec.LookPath(candidate); err == nil {
			return p, nil
		}
	}
	return "", errors.New("Python 未安装")
}
func copyFilePrivate(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}
func (s *Server) ensureNoAuth(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Backends = append([]Backend(nil), s.cfg.Backends...)
	for i := range next.Backends {
		if next.Backends[i].ID == id && next.Backends[i].Key == "" {
			next.Backends[i].Auth = "none"
		}
	}
	if Save(s.path, next) == nil {
		s.cfg = next
	}
}
func (s *Server) sourceCommand(id string) (*exec.Cmd, error) {
	p, ok := managedSources[id]
	if !ok {
		return nil, errors.New("not managed")
	}
	dir, err := s.sourceDir(id)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, p.readyFile)); err != nil {
		return nil, errors.New("尚未安装，请先点击一键部署")
	}
	var cmd *exec.Cmd
	switch id {
	case "vertex":
		py := filepath.Join(dir, ".mixed-venv", "Scripts", "python.exe")
		if _, err := os.Stat(py); err != nil {
			return nil, errors.New("Python 依赖未安装完成")
		}
		cmd = exec.Command(py, "-m", "uvicorn", "main:app", "--host", "127.0.0.1", "--port", "8050")
		cmd.Dir = filepath.Join(dir, "app")
		env := append([]string{}, os.Environ()...)
		env = append(env, "API_KEY="+s.getBackendKey(id), "STATE_DIR="+dir)
		for _, b := range s.config().Backends {
			if b.ID == id && b.ProviderKey != "" {
				env = append(env, "VERTEX_EXPRESS_API_KEY="+b.ProviderKey)
			}
		}
		cmd.Env = env
	case "workbuddy":
		cmd = exec.Command("node", "wb-relay.js", "--host", "127.0.0.1")
		cmd.Dir = dir
	case "aistudio":
		cmd = exec.Command("cmd.exe", "/d", "/c", "npm start")
		cmd.Dir = dir
	default:
		return nil, errors.New("not managed")
	}
	s.bindBackendOutput(cmd, id)
	return cmd, nil
}
func (s *Server) getBackendKey(id string) string {
	for _, b := range s.config().Backends {
		if b.ID == id {
			return b.Key
		}
	}
	return ""
}
