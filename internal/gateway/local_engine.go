package gateway

import (
	"crypto/sha256"
	"debug/buildinfo"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const localEngineID = "aps"

type localEngineOrigin struct {
	SHA256   string `json:"sha256"`
	Module   string `json:"module"`
	Version  string `json:"version"`
	Revision string `json:"revision"`
	Modified bool   `json:"modified"`
}

func localEngineDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "MIXED-API", "upstreams", "aps-local"), nil
}

func inspectLocalEngine(path string) (localEngineOrigin, error) {
	var origin localEngineOrigin
	f, err := pe.Open(path)
	if err != nil {
		return origin, errors.New("本地引擎不是有效的 Windows PE 程序")
	}
	defer f.Close()
	if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return origin, errors.New("需要 Windows x64 引擎")
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return origin, errors.New("无法读取引擎的 Go 构建来源")
	}
	if info.Main.Path != "github.com/bsfdsagfadg/vertex" && info.Main.Path != "github.com/DeconstructedCube/APS2api" {
		return origin, errors.New("此本地接入器仅支持 Vertex Proxy / APS2api 同源引擎")
	}
	input, err := os.Open(path)
	if err != nil {
		return origin, err
	}
	defer input.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, io.LimitReader(input, 150<<20)); err != nil {
		return origin, err
	}
	origin.SHA256 = hex.EncodeToString(digest.Sum(nil))
	origin.Module = info.Main.Path
	origin.Version = info.Main.Version
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			origin.Revision = s.Value
		}
		if s.Key == "vcs.modified" {
			origin.Modified = s.Value == "true"
		}
	}
	return origin, nil
}

// Import reads a local, user-selected package, creates an independent private
// workspace and never starts the executable or changes the original directory.
// Build metadata identifies provenance; it is not a signature or trust check.
func (s *Server) importLocalEngine(root string) (localEngineOrigin, error) {
	var origin localEngineOrigin
	if runtime.GOOS != "windows" {
		return origin, errors.New("本地 Windows 引擎导入只支持 Windows；手机可连接电脑网关")
	}
	if !filepath.IsAbs(root) {
		return origin, errors.New("请填写包所在目录的完整绝对路径")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return origin, errors.New("找不到本地目录")
	}
	exe := filepath.Join(root, "vertex-proxy.exe")
	stat, err := os.Stat(exe)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 150<<20 {
		return origin, errors.New("目录中缺少有效的 vertex-proxy.exe（最大 150 MB）")
	}
	origin, err = inspectLocalEngine(exe)
	if err != nil {
		return origin, err
	}
	dst, err := localEngineDir()
	if err != nil {
		return origin, err
	}
	if _, err = os.Stat(dst); !os.IsNotExist(err) {
		return origin, errors.New("增强引擎目录已存在；为保留数据，不覆盖导入")
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return origin, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dst), ".local-engine-")
	if err != nil {
		return origin, err
	}
	defer os.RemoveAll(stage)
	// Copy only distribution files; never import login sessions, databases,
	// subscription URLs or rules-acceptance state from the source directory.
	for _, name := range []string{"vertex-proxy.exe", "rules.txt", "LICENSE", "部署指南.md", "config/models.json"} {
		src := filepath.Join(root, filepath.FromSlash(name))
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) && name != "vertex-proxy.exe" {
			continue
		}
		if err != nil {
			return origin, err
		}
		if err = os.MkdirAll(filepath.Dir(filepath.Join(stage, name)), 0700); err != nil {
			return origin, err
		}
		if err = os.WriteFile(filepath.Join(stage, name), data, 0600); err != nil {
			return origin, err
		}
	}
	key := newKey("sk-mix-local-")
	if err = os.MkdirAll(filepath.Join(stage, "config"), 0700); err != nil {
		return origin, err
	}
	settings := map[string]any{"port_api": 2157, "admin_password": newKey("admin-"), "telemetry_enabled": false, "max_retries": 2, "request_timeout_seconds": 180}
	data, _ := json.MarshalIndent(settings, "", "  ")
	if err = os.WriteFile(filepath.Join(stage, "config", "config.json"), data, 0600); err != nil {
		return origin, err
	}
	if err = os.WriteFile(filepath.Join(stage, "config", "api_keys.txt"), []byte("mixed:"+key+":MIXED local engine\n"), 0600); err != nil {
		return origin, err
	}
	data, _ = json.MarshalIndent(origin, "", "  ")
	if err = os.WriteFile(filepath.Join(stage, ".mixed-origin.json"), data, 0600); err != nil {
		return origin, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Backends = append([]Backend(nil), s.cfg.Backends...)
	found:=false
 for i:=range next.Backends {if next.Backends[i].ID==localEngineID {next.Backends[i].URL="http://127.0.0.1:2157/v1";next.Backends[i].Key=key;next.Backends[i].Auth="bearer";next.Backends[i].Name="无头V";next.Backends[i].Enabled=false;found=true;break}}
 if !found {return origin,errors.New("无头V来源未配置")}
	if err = os.Rename(stage, dst); err != nil {
		return origin, err
	}
	if err = Save(s.path, next); err != nil {
		os.Rename(dst,stage)
 return origin, fmt.Errorf("配置保存失败，未启用新组件：%w", err)
	}
	s.cfg = next
	return origin, nil
}

func (s *Server) handleLocalImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, 400, "需要 JSON path 字段")
		return
	}
	origin, err := s.importLocalEngine(input.Path)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	jsonResult(w, 201, map[string]any{"origin": origin, "message": "无头V运行组件已准备（尚未启动）。回到工作台按步骤接入即可，统一客户端地址不变。"})
}

func (s *Server) launchLocalEngine() error {
	if runtime.GOOS != "windows" {
		return errors.New("此增强引擎只支持 Windows")
	}
	dir, err := localEngineDir()
	if err != nil {
		return err
	}
	exe := filepath.Join(dir, "vertex-proxy.exe")
	if _, err = inspectLocalEngine(exe); err != nil {
		return err
	}
	// An interactive window is intentional: only the user may accept upstream
	// rules. No agreed-rules file, stdin "yes", or equivalent is synthesized.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, running := s.children[localEngineID]; running {
		return nil
	}
	// Values are environment data, never interpolated PowerShell source. The
	// visible engine window is required for the user's first rules acceptance.
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$p = Start-Process -FilePath $env:MIXED_ENGINE_EXE -WorkingDirectory $env:MIXED_ENGINE_DIR -WindowStyle Normal -PassThru; $p.Id`)
	cmd.Env = append(os.Environ(), "MIXED_ENGINE_EXE="+exe, "MIXED_ENGINE_DIR="+dir)
	hideHelperWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return errors.New("无法打开引擎窗口，请检查 PowerShell 是否可用")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || pid <= 0 {
		return errors.New("未获得增强引擎进程 ID")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	child := &exec.Cmd{Process: process}
	s.children[localEngineID] = child
	s.status[localEngineID] = "running"
	go func() {
		_, waitErr := process.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.children[localEngineID] == child {
			delete(s.children, localEngineID)
			s.status[localEngineID] = "exited"
			if waitErr != nil {
				s.status[localEngineID] = "exited: 引擎已结束"
			}
		}
	}()
	s.status["setup:"+localEngineID] = "auth_pending: 请在增强引擎窗口阅读并本人确认使用规则，然后返回检查模型"
	return nil
}

func localEnginePresent() bool {dir,err:=localEngineDir();if err!=nil{return false};info,err:=os.Stat(filepath.Join(dir,"vertex-proxy.exe"));return err==nil&&info.Mode().IsRegular()}
