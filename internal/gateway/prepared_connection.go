package gateway

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Managed runtime files live in the user profile while each portable/installed
// gateway has its own config. Reusing a prepared runtime must restore its
// transport credentials, not ask the user to find an internal API key.
func (s *Server) restorePreparedConnection(id string) error {
	if id == "codex" {
		return nil
	}
	var key, base string
	auth := "bearer"
	for _, b := range Defaults().Backends {
		if b.ID == id {
			base = b.URL
		}
	}
	if base == "" {
		return errors.New("未知的受管来源")
	}
	switch id {
	case "aps":
		var root string
		if localEnginePresent() {
			var err error
			root, err = localEngineDir()
			if err != nil {
				return err
			}
			base = "http://127.0.0.1:2157/v1"
		} else {
			dir, err := s.binaryDir(id)
			if err != nil {
				return err
			}
			exe, err := findManagedExe(dir, binaryUpstreams[id].executable)
			if err != nil {
				return err
			}
			root = filepath.Dir(exe)
		}
		raw, err := os.ReadFile(filepath.Join(root, "config", "api_keys.txt"))
		if err != nil {
			return errors.New("无头V的本机通信配置缺失，请检查组件目录或恢复备份")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
			if len(parts) >= 2 && strings.HasPrefix(parts[1], "sk-") {
				key = parts[1]
				break
			}
		}
	case "opencode":
		dir, err := s.binaryDir(id)
		if err != nil {
			return err
		}
		exe, err := findManagedExe(dir, binaryUpstreams[id].executable)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "config.json"))
		if err != nil {
			return errors.New("OpenCode 本机通信配置缺失")
		}
		var settings struct {
			Keys []string `json:"server_keys"`
		}
		if json.Unmarshal(raw, &settings) != nil {
			return errors.New("OpenCode 本机通信配置格式无效")
		}
		if len(settings.Keys) > 0 {
			key = settings.Keys[0]
		}
	case "vertex":
		dir, err := s.sourceDir(id)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(dir, ".mixed-secret"))
		if err != nil {
			return errors.New("快速V本机通信配置缺失")
		}
		key = strings.TrimSpace(string(raw))
	case "aistudio":
		dir, err := s.sourceDir(id)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err != nil {
			return errors.New("AI Studio 本机通信配置缺失")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && name == "API_KEYS" {
				key = strings.Trim(strings.TrimSpace(strings.SplitN(value, ",", 2)[0]), "\"'")
				break
			}
		}
	case "workbuddy":
		auth = "none"
	}
	if auth != "none" && key == "" {
		return errors.New("组件的本机通信 Key 缺失；不会覆盖已有组件或账号数据")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Backends = append([]Backend(nil), s.cfg.Backends...)
	for i := range next.Backends {
		b := &next.Backends[i]
		if b.ID == id {
			b.URL = base
			b.Key = key
			b.Auth = auth
		}
	}
	if err := Save(s.path, next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}
