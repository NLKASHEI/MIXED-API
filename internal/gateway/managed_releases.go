package gateway

// Managed optional upstream binaries. Downloaded at the user's explicit request
// from pinned vendor releases, not redistributed in MIXED-API's installer.
// Cryptographic checksums are locked to the reviewed upstream release assets.

import (
	"archive/zip"
	"context"
	"crypto/sha256"
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
	"strings"
	"time"
)

type binaryUpstream struct {
	url, sha256, executable string
}

var binaryUpstreams = map[string]binaryUpstream{
	"opencode": {
		url:        "https://github.com/jasonxu114514/opencode2api/releases/download/v1.4.0/opencode2api_v1.4.0_windows_amd64.zip",
		sha256:     "f63e1049aa9aa489c5032cdd5f007088c192e4ed5d08afca0122ebdc0de41bab",
		executable: "opencode2api.exe",
	},
	"aps": {
		url:        "https://github.com/DeconstructedCube/APS2api/releases/download/v1.2.0/vertex-proxy-windows-amd64.zip",
		sha256:     "ea394caa57a9bb48d713e503a30db10851622649b50b1068b8d6206d2a502e9e",
		executable: "vertex-proxy.exe",
	},
}

const maxManagedArchive = 75 << 20
const maxManagedUnpacked = 500 << 20

func safeZipExtract(archive, dest string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	base, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	var total int64
	for _, f := range reader.File {
		clean := filepath.Clean(filepath.FromSlash(f.Name))
		if strings.HasPrefix(f.Name, "/") || strings.HasPrefix(f.Name, "\\") || strings.Contains(f.Name, "\\") || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || strings.Contains(clean, ":") {
			return fmt.Errorf("archive has unsafe filename %q", f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return errors.New("archive contains a symlink")
		}
		target := filepath.Join(base, clean)
		rel, e := filepath.Rel(base, target)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return errors.New("archive path escapes destination")
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			continue
		}
		total += int64(f.UncompressedSize64)
		if total > maxManagedUnpacked || f.UncompressedSize64 > maxManagedUnpacked {
			return errors.New("archive exceeds size limit")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			in.Close()
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, int64(f.UncompressedSize64)+1))
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != int64(f.UncompressedSize64) {
			return errors.New("invalid archive data length")
		}
	}
	return nil
}

func retrievePinnedZip(ctx context.Context, client *http.Client, archiveURL, digest, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return err
	}
	rsp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", rsp.StatusCode)
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	digestor := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, digestor), io.LimitReader(rsp.Body, maxManagedArchive+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxManagedArchive {
		return errors.New("download too large")
	}
	actual := hex.EncodeToString(digestor.Sum(nil))
	if !strings.EqualFold(actual, digest) {
		return errors.New("upstream release SHA-256 mismatch: refusing to install")
	}
	return nil
}

func findManagedExe(root, basename string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, ent os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ent.IsDir() {
			return nil
		}
		if strings.EqualFold(ent.Name(), basename) && found == "" {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("upstream archive missing %s", basename)
	}
	return found, nil
}

func (s *Server) binaryDir(id string) (string, error) {
	if _, ok := binaryUpstreams[id]; !ok {
		return "", errors.New("unsupported managed binary")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "MIXED-API", "upstreams", id), nil
}

func (s *Server) installBinaryBackend(id string) error {
	if runtime.GOOS != "windows" {
		return errors.New("此二进制自动安装器暂只支持 Windows")
	}
	pack, ok := binaryUpstreams[id]
	if !ok {
		return errors.New("unsupported upstream")
	}
	dst, err := s.binaryDir(id)
	if err != nil {
		return err
	}
	if _, err = findManagedExe(dst, pack.executable); err == nil {
		return errors.New("此上游已安装；启动即可，不会覆盖已有的密钥和设置")
	}
	s.mu.Lock()
	if strings.HasPrefix(s.status["setup:"+id], "installing") {
		s.mu.Unlock()
		return errors.New("安装正在进行，请勿重复点击")
	}
	s.status["setup:"+id] = "installing"
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		if _, err := os.Stat(dst); err == nil {
			s.backendState(id, "failed: 目标目录已存在；不会覆盖用户数据")
			return
		}
		stage, err := os.MkdirTemp(filepath.Dir(dst), ".mixed-staging-")
		if err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		defer os.RemoveAll(stage)
		archive := filepath.Join(stage, "vendor.zip")
		s.backendState(id, "installing: 正在下载官方固定版本并验证 SHA-256")
		downloader := &http.Client{Timeout: 7 * time.Minute}
		if err := retrievePinnedZip(ctx, downloader, pack.url, pack.sha256, archive); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		extract := filepath.Join(stage, "files")
		s.backendState(id, "installing: 校验成功，正在解压")
		if err := safeZipExtract(archive, extract); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		exe, err := findManagedExe(extract, pack.executable)
		if err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		exeDir := filepath.Dir(exe)
		var apiKey string
		if id == "opencode" {
			apiKey = newKey("sk-mix-open-")
			if err := writeOpenCodeConfig(exeDir, apiKey, s.config()); err != nil {
				s.backendState(id, "failed: "+err.Error())
				return
			}
		} else {
			apiKey = newKey("sk-mix-aps-")
			if err := writeAPSConfig(exeDir, apiKey); err != nil {
				s.backendState(id, "failed: "+err.Error())
				return
			}
		}
		// Rename the verified, configured directory as an atomic final step.
		if err := os.Rename(extract, dst); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		if err := s.saveGeneratedBackend(id, apiKey); err != nil {
			s.backendState(id, "failed: "+err.Error())
			return
		}
		s.backendState(id, "installed: 已自动生成本地密钥，点击启动上游即可")
	}()
	return nil
}

func writeOpenCodeConfig(root, key string, cfg Config) error {
	var providerKey string
	for _, b := range cfg.Backends {
		if b.ID == "opencode" {
			providerKey = b.ProviderKey
			break
		}
	}
	data := map[string]any{
		"listen": "127.0.0.1:8080", "server_keys": []string{key},
		"zen_keys": []string{}, "go_keys": []string{}, "anonymous": providerKey == "",
		"proxies": []string{"direct"},
		"webui":   map[string]any{"enabled": true, "listen": "127.0.0.1:8081", "username": "admin", "password": newKey("admin-")},
	}
	if providerKey != "" {
		data["zen_keys"] = []string{providerKey}
	}
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "config.json"), content, 0600)
}

func writeAPSConfig(root, key string) error {
	dir := filepath.Join(root, "config")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Native APS2api format: name:key:description, one key per line.
	if err := os.WriteFile(filepath.Join(dir, "api_keys.txt"), []byte("mixed:"+key+":MIXED-API\n"), 0600); err != nil {
		return err
	}
	settings := map[string]any{"admin_password": newKey("admin-"), "port_api": 2156, "telemetry_enabled": false}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)
}

func (s *Server) saveGeneratedBackend(id, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Backends = append([]Backend(nil), s.cfg.Backends...)
	found := false
	for i := range next.Backends {
		if next.Backends[i].ID == id {
			next.Backends[i].Key = key
			next.Backends[i].Auth = "bearer"
			next.Backends[i].Enabled = true
			found = true
		}
	}
	if !found {
		return errors.New("backend not configured")
	}
	if err := Save(s.path, next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

func (s *Server) binaryCommand(id string) (*exec.Cmd, error) {
	pack, ok := binaryUpstreams[id]
	if !ok {
		return nil, errors.New("unsupported managed binary")
	}
	dir, err := s.binaryDir(id)
	if err != nil {
		return nil, err
	}
	path, err := findManagedExe(dir, pack.executable)
	if err != nil {
		return nil, errors.New("尚未安装：请先点击一键部署")
	}
	var cmd *exec.Cmd
	if id == "opencode" {
		cmd = exec.Command(path, "-config", "config.json")
	} else {
		cmd = exec.Command(path)
	}
	cmd.Dir = filepath.Dir(path)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd, nil
}

// Keep provider credentials in the MIXED-API form and apply them to the local
// managed OpenCode configuration just before launching the process. Never edit
// externally managed OpenCode setups.
func syncOpenCodeCredentials(root string, cfg Config) error {
	path := filepath.Join(root, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var current map[string]json.RawMessage
	if err = json.Unmarshal(raw, &current); err != nil {
		return err
	}
	var zen string
	for _, b := range cfg.Backends {
		if b.ID == "opencode" {
			zen = strings.TrimSpace(b.ProviderKey)
		}
	}
	keys := []string{}
	if zen != "" {
		keys = append(keys, zen)
	}
	current["zen_keys"], _ = json.Marshal(keys)
	current["anonymous"], _ = json.Marshal(zen == "")
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
