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
	"strings"
	"sync"
)

type Backend struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Key     string `json:"key,omitempty"`
	Auth    string `json:"auth"`
	Enabled bool   `json:"enabled"`
	Command string `json:"command,omitempty"`
	Workdir string `json:"workdir,omitempty"`
}
type Config struct {
	Listen   string    `json:"listen"`
	APIKey   string    `json:"api_key"`
	AdminKey string    `json:"admin_key"`
	Backends []Backend `json:"backends"`
}

var Guide = map[string]struct{ Repo, Note string }{
	"vertex":    {"https://github.com/bad-woman/vertex2openai", "Express Key 或 Google Cloud Cookie + Project ID；原版默认端口 8050；Cookie 模式依赖非公开接口。"},
	"codex":     {"https://github.com/openai/codex", "Windows 直接使用官方 Codex CLI，不需要 SillyTavern：安装 Node.js → npm install -g @openai/codex@latest → codex login。MIXED-API 通过本地 app-server 通信。"},
	"opencode":  {"https://github.com/jasonxu114514/opencode2api", "启动 Go 程序，设置本地 server_keys；API 默认 8080，管理页 8081。"},
	"workbuddy": {"https://github.com/sitingge/workboddy-proxy", "Windows 上先安装并登录 WorkBuddy 客户端；node wb-relay.js；默认 8790。"},
	"aps":       {"https://github.com/DeconstructedCube/APS2api", "启动 APS2api，进入 2156/admin 创建 sk- 开头的本地 Key；可能依赖匿名端点且不保证稳定。"},
	"aistudio":  {"https://github.com/iBUHub/AIStudioToAPI", "电脑上先运行 npm run setup-auth，通过 Camoufox 登录，再 npm start；默认 7860。含非商业许可。"},
}

func Defaults() Config {
	return Config{Listen: "127.0.0.1:8765", Backends: []Backend{
		{ID: "vertex", Name: "Vertex Express / Cookie", URL: "http://127.0.0.1:8050/v1", Auth: "bearer"},
		{ID: "codex", Name: "Codex CLI (无需 SillyTavern)", Kind: "codex_cli", URL: "http://127.0.0.1:8901/codex/v1", Auth: "none"},
		{ID: "opencode", Name: "OpenCode 2API", URL: "http://127.0.0.1:8080/v1", Auth: "bearer"},
		{ID: "workbuddy", Name: "WorkBuddy Proxy", URL: "http://127.0.0.1:8790/v1", Auth: "bearer"},
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
		if b.Kind != "" && b.Kind != "http" && !(b.Kind == "codex_cli" && b.ID == "codex") {
			return fmt.Errorf("invalid backend kind for %s", b.ID)
		}
		if b.Auth != "bearer" && b.Auth != "x-api-key" && b.Auth != "none" {
			return fmt.Errorf("invalid auth for %s", b.ID)
		}
		if _, err := backendURL(b.URL); err != nil {
			return fmt.Errorf("backend %s: %w", b.ID, err)
		}
	}
	return nil
}
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
