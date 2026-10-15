package gateway

// Direct Codex CLI app-server adapter. Unlike SillyTavern-Subscriptions this
// integration does not need SillyTavern or copy any CLI authentication tokens.
// It deliberately rejects tool/MCP execution and non-ChatGPT billing modes.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type codexMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}
type codexClient struct {
	stdin    io.WriteCloser
	incoming chan codexMessage
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	stderr   bytes.Buffer
	nextID   int
}

var codexFeatures = []string{"shell_tool", "unified_exec", "view_image", "sleep_tool", "multi_agent", "apps", "plugins", "hooks", "image_generation", "browser_use", "computer_use", "skill_search", "tool_suggest", "memories"}

func codexCommand(ctx context.Context, args []string) (*exec.Cmd, error) {
	name, err := exec.LookPath("codex")
	if err != nil {
		return nil, errors.New("Codex CLI 未安装：Windows 请先安装 Node.js，然后运行 npm install -g @openai/codex@latest")
	}
	if runtime.GOOS == "windows" && (strings.HasSuffix(strings.ToLower(name), ".cmd") || strings.HasSuffix(strings.ToLower(name), ".bat")) {
		// Windows npm installs codex.cmd as a trusted shim. All arguments here
		// are fixed strings produced by this package, not user-controlled shell.
		cmdline := `"` + strings.ReplaceAll(name, `"`, ``) + `"`
		for _, a := range args {
			cmdline += " " + a
		}
		return exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", cmdline), nil
	}
	return exec.CommandContext(ctx, name, args...), nil
}

func openCodex(parent context.Context) (*codexClient, error) {
	ctx, cancel := context.WithCancel(parent)
	args := []string{"app-server", "--stdio", "-c", "notify=[]", "-c", "project_doc_max_bytes=0", "-c", "include_environment_context=false", "-c", "skills.include_instructions=false", "-c", "skills.bundled.enabled=false"}
	for _, f := range codexFeatures {
		args = append(args, "-c", "features."+f+"=false")
	}
	cmd, err := codexCommand(ctx, args)
	if err != nil {
		cancel()
		return nil, err
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	c := &codexClient{stdin: input, incoming: make(chan codexMessage, 256), cancel: cancel, cmd: cmd}
	cmd.Stderr = &c.stderr
	if err = cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("Codex 无法启动: %w", err)
	}
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 16*1024), 16<<20)
		for scanner.Scan() {
			var msg codexMessage
			if json.Unmarshal(scanner.Bytes(), &msg) == nil {
				select {
				case c.incoming <- msg:
				case <-ctx.Done():
					return
				}
			}
		}
		close(c.incoming)
	}()
	go func() { _ = cmd.Wait() }()
	if _, err = c.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "MIXED-API", "version": "0.2.0"}}, 20*time.Second, nil); err != nil {
		c.close()
		return nil, err
	}
	if err = c.send(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}); err != nil {
		c.close()
		return nil, err
	}
	// Never use a custom base URL that could capture ChatGPT authentication.
	config, err := c.request(ctx, "config/read", map[string]any{"includeLayers": false}, 12*time.Second, nil)
	if err != nil {
		c.close()
		return nil, fmt.Errorf("无法核实 Codex 工具隔离设置: %w", err)
	}
	var conf struct {
		Config struct {
			MCP map[string]struct {
				Enabled *bool `json:"enabled"`
			} `json:"mcp_servers"`
			OpenAIBase  string `json:"openai_base_url"`
			ChatGPTBase string `json:"chatgpt_base_url"`
		} `json:"config"`
	}
	if json.Unmarshal(config, &conf) != nil {
		c.close()
		return nil, errors.New("Codex 配置格式无法验证")
	}
	for id, mcp := range conf.Config.MCP {
		if mcp.Enabled == nil || *mcp.Enabled {
			c.close()
			return nil, fmt.Errorf("请先在 ~/.codex/config.toml 禁用 MCP %s（避免聊天意外执行工具）", id)
		}
	}
	for _, host := range []string{conf.Config.OpenAIBase, conf.Config.ChatGPTBase} {
		if host != "" && !strings.HasPrefix(host, "https://api.openai.com/") && !strings.HasPrefix(host, "https://chatgpt.com/") {
			c.close()
			return nil, errors.New("Codex 配置了第三方 API 端点；拒绝向其他服务器发送 ChatGPT 授权信息")
		}
	}
	account, err := c.request(ctx, "account/read", map[string]any{"refreshToken": false}, 15*time.Second, nil)
	if err != nil {
		c.close()
		return nil, fmt.Errorf("无法读取 Codex 登录状态: %w", err)
	}
	var auth struct {
		Account struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if json.Unmarshal(account, &auth) != nil || auth.Account.Type != "chatgpt" {
		c.close()
		return nil, errors.New("Codex 尚未通过 ChatGPT 订阅登录：请执行 codex login；本模式不会使用付费 API Key")
	}
	return c, nil
}
func (c *codexClient) close() { c.cancel(); _ = c.stdin.Close() }
func (c *codexClient) send(value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = c.stdin.Write(b)
	return err
}
func (c *codexClient) request(ctx context.Context, method string, params any, timeout time.Duration, onEvent func(codexMessage) error) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf("Codex %s 超时（%s）", method, timeout)
		case msg, ok := <-c.incoming:
			if !ok {
				return nil, fmt.Errorf("Codex 进程退出（%s）", strings.TrimSpace(c.stderr.String()))
			}
			if len(msg.ID) > 0 {
				var received int
				if json.Unmarshal(msg.ID, &received) == nil && received == id {
					if len(msg.Error) > 0 && string(msg.Error) != "null" {
						return nil, fmt.Errorf("Codex %s: %s", method, string(msg.Error))
					}
					return msg.Result, nil
				}
				if msg.Method != "" { // The gateway never approves tool execution.
					_ = c.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "error": map[string]any{"code": -32601, "message": "MIXED-API refuses Codex tool requests"}})
				}
			} else if msg.Method != "" && onEvent != nil {
				if err := onEvent(msg); err != nil {
					return nil, err
				}
			}
		}
	}
}
func codexMessages(messages json.RawMessage) (string, string, error) {
	var arr []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	if err := json.Unmarshal(messages, &arr); err != nil {
		return "", "", errors.New("Codex expects an array of messages")
	}
	sys := []string{}
	body := []string{}
	for _, m := range arr {
		role := strings.ToLower(m.Role)
		if role != "system" && role != "developer" && role != "user" && role != "assistant" && role != "tool" {
			continue
		}
		txt := ""
		switch v := m.Content.(type) {
		case string:
			txt = v
		case []any:
			for _, p := range v {
				if obj, ok := p.(map[string]any); ok {
					if obj["type"] == "text" {
						if t, ok := obj["text"].(string); ok {
							txt += t
						}
					}
				}
			}
		}
		if strings.TrimSpace(txt) == "" {
			continue
		}
		if role == "system" || role == "developer" {
			sys = append(sys, txt)
		} else {
			body = append(body, "["+role+"]\n"+txt)
		}
	}
	if len(body) == 0 {
		return "", "", errors.New("Codex needs at least one user/assistant text message")
	}
	return strings.Join(sys, "\n\n"), strings.Join(body, "\n\n"), nil
}
func (c *codexClient) models(ctx context.Context) ([]string, error) {
	result, err := c.request(ctx, "model/list", map[string]any{"includeHidden": false}, 20*time.Second, nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Slug  string `json:"slug"`
		} `json:"data"`
	}
	if err = json.Unmarshal(result, &list); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, item := range list.Data {
		id := item.Model
		if id == "" {
			id = item.ID
		}
		if id == "" {
			id = item.Slug
		}
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func (c *codexClient) chat(ctx context.Context, model string, messages json.RawMessage, effort string, emit func(string) error) (string, error) {
	system, text, err := codexMessages(messages)
	if err != nil {
		return "", err
	}
	cwd, err := os.MkdirTemp("", "mixed-codex-cwd-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(cwd)
	fullSystem := "You are a conversational assistant in a chat application. Do not run tools, write files, or execute commands. Follow the supplied chat instructions."
	if system != "" {
		fullSystem = system + "\n\n" + fullSystem
	}
	thread := map[string]any{"model": model, "modelProvider": "openai", "cwd": filepath.Clean(cwd), "ephemeral": true, "approvalPolicy": "never", "sandbox": "read-only", "baseInstructions": fullSystem, "developerInstructions": "", "personality": "none", "config": map[string]any{"model_verbosity": "medium"}}
	result, err := c.request(ctx, "thread/start", thread, 60*time.Second, nil)
	if err != nil {
		return "", err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model              string   `json:"model"`
		InstructionSources []string `json:"instructionSources"`
	}
	if err = json.Unmarshal(result, &started); err != nil || started.Thread.ID == "" {
		return "", errors.New("Codex did not create a thread")
	}
	if started.Model != "" && started.Model != model {
		return "", fmt.Errorf("Codex substituted model %s instead of %s", started.Model, model)
	}
	for _, p := range started.InstructionSources {
		b, e := os.ReadFile(p)
		if e != nil || len(bytes.TrimSpace(b)) > 0 {
			return "", errors.New("Codex is loading global AGENTS.md; disable it for isolated chat")
		}
	}
	id := started.Thread.ID
	defer func() {
		_, _ = c.request(context.Background(), "thread/unsubscribe", map[string]any{"threadId": id}, time.Second, nil)
	}()
	p := map[string]any{"threadId": id, "input": []any{map[string]any{"type": "text", "text": text}}, "summary": "none"}
	switch effort {
	case "minimal", "low", "medium", "high", "xhigh":
		p["effort"] = effort
	default:
		p["effort"] = "medium"
	}
	// turn/start can emit streaming notifications BEFORE its RPC result.
	deltas := strings.Builder{}
	seenDelta := map[string]bool{}
	complete := false
	onEvent := func(msg codexMessage) error {
		var m map[string]json.RawMessage
		if json.Unmarshal(msg.Params, &m) != nil {
			return nil
		}
		var tid string
		_ = json.Unmarshal(m["threadId"], &tid)
		if tid != "" && tid != id {
			return nil
		}
		switch msg.Method {
		case "item/started":
			var item struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(m["item"], &item)
			switch item.Type {
			case "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall", "webSearch", "imageView", "imageGeneration", "collabAgentToolCall", "subAgentActivity":
				return errors.New("Codex attempted a disabled tool; stopped")
			}
		case "item/agentMessage/delta":
			var itemID, delta string
			_ = json.Unmarshal(m["itemId"], &itemID)
			_ = json.Unmarshal(m["delta"], &delta)
			if delta != "" {
				seenDelta[itemID] = true
				deltas.WriteString(delta)
				if emit != nil {
					return emit(delta)
				}
			}
		case "item/completed":
			var item struct {
				ID    string `json:"id"`
				Type  string `json:"type"`
				Text  string `json:"text"`
				Phase string `json:"phase"`
			}
			_ = json.Unmarshal(m["item"], &item)
			if item.Type == "agentMessage" && !seenDelta[item.ID] && !seenDelta[""] && item.Phase != "commentary" && item.Text != "" {
				deltas.WriteString(item.Text)
				if emit != nil {
					return emit(item.Text)
				}
			}
		case "turn/completed":
			var turn struct {
				Status string `json:"status"`
				Error  any    `json:"error"`
			}
			_ = json.Unmarshal(m["turn"], &turn)
			if turn.Status == "failed" || turn.Error != nil {
				return errors.New("Codex turn failed")
			}
			complete = true
		case "error":
			var willRetry bool
			_ = json.Unmarshal(m["willRetry"], &willRetry)
			if !willRetry {
				return fmt.Errorf("Codex error: %s", string(m["error"]))
			}
		case "model/rerouted":
			return errors.New("Codex rerouted the model; response refused")
		}
		return nil
	}
	_, err = c.request(ctx, "turn/start", p, 60*time.Second, onEvent)
	if err != nil {
		return "", err
	}
	// Consume events until turn/completed; there may be a very long silent think.
	deadline := time.NewTimer(12 * time.Minute)
	defer deadline.Stop()
	for !complete {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", errors.New("Codex chat timed out after 12 minutes")
		case msg, ok := <-c.incoming:
			if !ok {
				return "", errors.New("Codex process exited during chat")
			}
			if len(msg.ID) > 0 && msg.Method != "" {
				_ = c.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "error": map[string]any{"code": -32601, "message": "Tools refused"}})
				continue
			}
			if err := onEvent(msg); err != nil {
				return "", err
			}
		}
	}
	return deltas.String(), nil
}
