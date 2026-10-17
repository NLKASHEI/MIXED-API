package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type Backend struct {
	ID          string `json:"id"`
	Kind        string `json:"kind,omitempty"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Key         string `json:"key,omitempty"`
	ProviderKey string `json:"provider_key,omitempty"`
	Auth        string `json:"auth"`
	Enabled     bool   `json:"enabled"`
	Command     string `json:"command,omitempty"`
	Workdir     string `json:"workdir,omitempty"`
}
type Config struct {
	Listen   string            `json:"listen"`
	APIKey   string            `json:"api_key"`
	AdminKey string            `json:"admin_key"`
	Backends []Backend         `json:"backends"`
	Aliases  map[string]string `json:"aliases,omitempty"`
}

var Guide = map[string]struct{ Repo, Note string }{
	"vertex":    {"https://github.com/bad-woman/vertex2openai", "快速V：填写本人的 Express API Key，使用内置 Google REST 协议。"},
	"codex":     {"https://github.com/openai/codex", "Codex：在官方页面完成设备码授权；内置 HTTP 协议，无需安装 CLI。"},
	"opencode":  {"https://github.com/zkygwxf-bot/opencode-proxy", "OpenCode：内置协议转换。服务方拒绝授权时直接报错，不伪造客户端身份。"},
	"workbuddy": {"https://github.com/hawklithm/workbuddy2api", "WorkBuddy：本人账号授权，内置 HTTP 连接，无需启动官方 CLI。"},
	"aps":       {"https://github.com/DeconstructedCube/APS2api", "无头V：内置核心重构中。"},
	"aistudio":  {"https://github.com/Mag1cFall/AIStudio2API", "AIS：Build 与 Playground；核对 DeconstructedCube/aistudio-api 的同源协议。"},
}

func Defaults() Config {
	return Config{Listen: "127.0.0.1:8765", Backends: []Backend{
		{ID: "vertex", Name: "快速V", Kind: "builtin", URL: builtinBase("vertex"), Auth: "none"},
		{ID: "codex", Name: "Codex", Kind: "builtin", URL: builtinBase("codex"), Auth: "none"},
		{ID: "opencode", Name: "OpenCode", Kind: "builtin", URL: builtinBase("opencode"), Auth: "none"},
		{ID: "workbuddy", Name: "WorkBuddy", Kind: "builtin", URL: builtinBase("workbuddy"), Auth: "none"},
		{ID: "aps", Name: "APS2api", URL: "http://127.0.0.1:2156/v1", Auth: "bearer"},
		{ID: "aistudio", Name: "AI Studio Build", URL: "http://127.0.0.1:7860/v1", Auth: "bearer"},
	}}
}
func newKey(prefix string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}
func Validate(c Config) error {
	if c.APIKey == "" || c.AdminKey == "" || c.APIKey == c.AdminKey {
		return errors.New("distinct nonempty api_key and admin_key required")
	}
	host, _, e := net.SplitHostPort(c.Listen)
	if e != nil {
		return errors.New("invalid listen host:port")
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errors.New("only loopback listen addresses allowed")
	}
	ids := map[string]bool{}
	for _, b := range c.Backends {
		if b.ID == "" || strings.ContainsAny(b.ID, "/\\?# ") {
			return fmt.Errorf("invalid backend id %q", b.ID)
		}
		if ids[b.ID] {
			return fmt.Errorf("duplicate backend id %q", b.ID)
		}
		ids[b.ID] = true
		if b.Kind != "" && b.Kind != "http" && !(b.Kind == "codex_cli" && b.ID == "codex") && !(b.Kind == "builtin" && knownBuiltin(b.ID)) {
			return fmt.Errorf("invalid backend kind for %s", b.ID)
		}
		if b.Auth != "bearer" && b.Auth != "x-api-key" && b.Auth != "none" {
			return fmt.Errorf("invalid auth for %s", b.ID)
		}
		if _, err := backendURL(b.URL); err != nil {
			return fmt.Errorf("backend %s: %w", b.ID, err)
		}
		if b.Kind == "builtin" && !isBuiltinURL(b) {
			return fmt.Errorf("builtin %s requires its official endpoint", b.ID)
		}
	}
	for alias, target := range c.Aliases {
		id, model, ok := strings.Cut(target, "/")
		if !aliasName.MatchString(alias) || !ok || model == "" || !ids[id] {
			return errors.New("aliases require a simple name and an existing backend/model target")
		}
	}
	return nil
}

var aliasName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func LoadOrCreate(path string) (Config, error) {
	data, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		c := Defaults()
		c.APIKey = newKey("sk-mix-")
		c.AdminKey = newKey("admin-")
		if e := Save(path, c); e != nil {
			return c, e
		}
		return c, nil
	}
	if e != nil {
		return Config{}, e
	}
	var c Config
	if e := json.Unmarshal(data, &c); e != nil {
		return c, e
	}
	// Keep existing user keys/config, but migrate the old ST-only Codex default.
	for i := range c.Backends {
		b := &c.Backends[i]
		if b.ID == "codex" && b.Kind == "" && b.URL == "http://127.0.0.1:8901/codex/v1" {
			b.Kind = "codex_cli"
			b.Name = "Codex CLI (无需 SillyTavern)"
		}
	}
	return c, Validate(c)
}

var writeMu sync.Mutex

func Save(path string, c Config) error {
	if e := Validate(c); e != nil {
		return e
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	d, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	d = append(d, '\n')
	dir := filepath.Dir(path)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".mixed-api-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = f.Chmod(0600); e != nil {
		return e
	}
	if _, e = f.Write(d); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
