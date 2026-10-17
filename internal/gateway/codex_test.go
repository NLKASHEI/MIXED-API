package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Minimal fake app-server. This verifies the external RPC seam rather than
// importing/copying internals from the original SillyTavern plugin.
const mockAppServer = `#!/usr/bin/env python3
import json,sys,os
account=os.getenv('MOCK_CODEX_ACCOUNT','chatgpt')
for line in sys.stdin:
 try:m=json.loads(line)
 except:continue
 method=m.get('method');rid=m.get('id');p=m.get('params') or {}
 def reply(data):
  print(json.dumps({'jsonrpc':'2.0','id':rid,'result':data}),flush=True)
 if rid is None: continue
 if method=='initialize':reply({'codexHome':'/tmp'})
 elif method=='config/read':reply({'config':{'mcp_servers':{}}})
 elif method=='account/read':reply({'account':{'type':account}})
 elif method=='model/list':reply({'data':[{'model':'gpt-mock-1'},{'model':'gpt-mock-2'}]})
 elif method=='thread/start':reply({'thread':{'id':'threadA'},'model':p['model'],'instructionSources':[]})
 elif method=='turn/start':
  reply({'turn':{'id':'turnA'}})
  print(json.dumps({'method':'item/agentMessage/delta','params':{'threadId':'threadA','itemId':'m1','delta':'hello '}}),flush=True)
  print(json.dumps({'method':'item/agentMessage/delta','params':{'threadId':'threadA','itemId':'m1','delta':'world'}}),flush=True)
  print(json.dumps({'method':'turn/completed','params':{'threadId':'threadA','turn':{'status':'completed'}}}),flush=True)
 elif method=='thread/unsubscribe':reply({})
 else:print(json.dumps({'id':rid,'error':{'message':'unsupported'}}),flush=True)
`

func setupFakeCodex(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mock shell launcher is Unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	if err := os.WriteFile(path, []byte(mockAppServer), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
func nativeFixture() (*Server, Config) {
	c := Defaults()
	c.APIKey = "sk-test"
	c.AdminKey = "admin-test"
	for i := range c.Backends {
		c.Backends[i].Enabled = c.Backends[i].ID == "codex"
	}
	return NewServer("", c), c
}
func TestCodexDirectModelsAndChat(t *testing.T) {
	setupFakeCodex(t)
	s, _ := nativeFixture()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "codex/gpt-mock-1") {
		t.Fatalf("models: %d %s", w.Code, w.Body.String())
	}
	for _, stream := range []bool{false, true} {
		p := map[string]any{"model": "codex/gpt-mock-1", "messages": []any{map[string]string{"role": "system", "content": "Be brief"}, map[string]string{"role": "user", "content": "say hello"}}, "stream": stream}
		body, _ := json.Marshal(p)
		req = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer sk-test")
		w = httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") || !strings.Contains(w.Body.String(), "world") {
			t.Fatalf("chat stream %v: %d %s", stream, w.Code, w.Body.String())
		}
		if stream && !strings.Contains(w.Body.String(), "data: [DONE]") {
			t.Fatalf("missing SSE terminator %s", w.Body.String())
		}
	}
}
func TestCodexRefusesAPIKeyLogin(t *testing.T) {
	setupFakeCodex(t)
	t.Setenv("MOCK_CODEX_ACCOUNT", "apiKey")
	s, _ := nativeFixture()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "订阅登录") {
		t.Fatalf("should reject paid API login: %s", w.Body.String())
	}
}
func TestCodexInputSeparation(t *testing.T) {
	system, body, err := codexMessages(json.RawMessage(`[{"role":"system","content":"You are helpful"},{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"https://secret"}}]}]`))
	if err != nil || system != "You are helpful" || !strings.Contains(body, "hello") || strings.Contains(body, "secret") {
		t.Fatalf("wrong parsed content: %s %s %v", system, body, err)
	}
}
func TestDefaultCodexUsesBuiltinHTTP(t *testing.T) {
	c := Defaults()
	for _, b := range c.Backends {
		if b.ID == "codex" && b.Kind != "builtin" {
			t.Fatal("Codex must use the in-process HTTP implementation by default")
		}
	}
}
