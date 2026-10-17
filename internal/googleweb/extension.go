package googleweb

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

//go:embed extension/*
var extensionFiles embed.FS

// ExtensionPage connects only to the bundled extension in the user's ordinary
// browser. Credentials remain in that browser; the bridge carries operations.
type ExtensionPage struct {
	mu                          sync.Mutex
	server                      *http.Server
	host, token, source, folder string
	open                        func(string) error
	peer                        *extensionPeer
	connected, closed           bool
	sequence                    uint64
	pending                     map[uint64]chan extensionReply
}
type extensionPeer struct {
	conn      net.Conn
	writeGate chan struct{}
	ready     bool
}
type extensionReply struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
	Ready  bool            `json:"ready"`
}

var extensionOrigin = regexp.MustCompile(`^chrome-extension://[a-p]{32}$`)

var extensionExportMu sync.Mutex

func exportExtension(folder string) error {
	extensionExportMu.Lock()
	defer extensionExportMu.Unlock()
	files, _ := extensionFiles.ReadDir("extension")
	content := map[string][]byte{"google-session.js": []byte(RuntimeScript)}
	for _, file := range files {
		data, err := extensionFiles.ReadFile("extension/" + file.Name())
		if err != nil {
			return err
		}
		content[file.Name()] = data
	}
	for name, data := range content {
		path := filepath.Join(folder, name)
		old, _ := os.ReadFile(path)
		if bytes.Equal(old, data) {
			continue
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
	}
	return nil
}

func NewExtensionPage(folder, source string, open func(string) error) (*ExtensionPage, error) {
	if source != "aps" && source != "aistudio" {
		return nil, errors.New("unknown source")
	}
	folder, err := filepath.Abs(folder)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(folder, 0700); err != nil {
		return nil, err
	}
	if err = exportExtension(folder); err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &ExtensionPage{host: listener.Addr().String(), token: hex.EncodeToString(secret), source: source, folder: folder, open: open, pending: map[uint64]chan extensionReply{}}
	p.server = &http.Server{Handler: http.HandlerFunc(p.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go p.server.Serve(listener)
	if err = open(p.setupURL()); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}
func (p *ExtensionPage) setupURL() string {
	return "http://" + p.host + "/mixed-browser/connect#" + p.token
}
func (p *ExtensionPage) serve(w http.ResponseWriter, r *http.Request) {
	if r.Host != p.host || r.Method != "GET" {
		http.Error(w, "Invalid request", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/mixed-browser/connect" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = extensionSetup.Execute(w, p.folder)
		return
	}
	if r.URL.Path != "/mixed-browser/socket" {
		http.NotFound(w, r)
		return
	}
	if !extensionOrigin.MatchString(r.Header.Get("Origin")) || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(p.token)) != 1 {
		http.Error(w, "Forbidden", 403)
		return
	}
	p.mu.Lock()
	if p.closed || p.peer != nil {
		p.mu.Unlock()
		http.Error(w, "Already connected", 409)
		return
	}
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		p.mu.Unlock()
		return
	}
	peer := &extensionPeer{conn: conn, writeGate: make(chan struct{}, 1)}
	p.peer = peer
	p.mu.Unlock()
	defer func() {
		conn.Close()
		p.mu.Lock()
		if p.peer == peer {
			p.peer = nil
			for id, ch := range p.pending {
				ch <- extensionReply{Error: "disconnected"}
				delete(p.pending, id)
			}
		}
		p.mu.Unlock()
	}()
	if peer.send(context.Background(), map[string]any{"kind": "hello", "source": p.source}) != nil {
		return
	}
	reader := wsutil.NewServerSideReader(conn)
	reader.MaxFrameSize = 128 << 10
	reader.CheckUTF8 = true
	for {
		conn.SetReadDeadline(time.Now().Add(55 * time.Second))
		header, err := reader.NextFrame()
		if err != nil || header.OpCode != ws.OpText {
			return
		}
		raw, err := io.ReadAll(io.LimitReader(reader, (128<<10)+1))
		if err != nil || len(raw) > 128<<10 {
			return
		}
		var reply extensionReply
		if json.Unmarshal(raw, &reply) != nil {
			return
		}
		p.mu.Lock()
		if reply.Ready {
			peer.ready = true
			p.connected = true
		}
		if ch := p.pending[reply.ID]; ch != nil {
			ch <- reply
			delete(p.pending, reply.ID)
		}
		p.mu.Unlock()
	}
}
func (p *extensionPeer) send(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	select {
	case p.writeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.writeGate }()
	deadline := time.Now().Add(5 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	p.conn.SetWriteDeadline(deadline)
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { p.conn.SetWriteDeadline(time.Now()); close(cancelled) })
	defer func() {
		if !stop() {
			<-cancelled
		}
	}()
	return wsutil.WriteServerText(p.conn, raw)
}
func (p *ExtensionPage) Call(ctx context.Context, method string, args ...any) (json.RawMessage, error) {
	switch method {
	case "state", "initialize", "cancelInitialize", "start", "read", "cancel", "show":
	default:
		return nil, errors.New("unsupported browser command")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	peer := p.peer
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("browser bridge closed")
	}
	if peer == nil || !peer.ready {
		p.mu.Unlock()
		switch method {
		case "state", "initialize":
			return json.RawMessage(`{"status":"waiting","message":"请在当前浏览器按连接页指引加载 MIXED 扩展，再刷新连接页"}`), nil
		case "cancelInitialize", "cancel":
			return json.RawMessage(`true`), nil
		}
		return nil, errors.New("browser extension not connected")
	}
	p.sequence++
	id := p.sequence
	ch := make(chan extensionReply, 1)
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	if err := peer.send(ctx, map[string]any{"id": id, "method": method, "args": args}); err != nil {
		return nil, err
	}
	select {
	case reply := <-ch:
		if reply.Error != "" {
			return nil, errors.New("browser command failed")
		}
		return reply.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Legacy Eval is deliberately unavailable: this transport accepts typed calls.
func (p *ExtensionPage) Eval(context.Context, string) (json.RawMessage, error) {
	return nil, errors.New("use browser command interface")
}
func (p *ExtensionPage) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && (!p.connected || p.peer != nil)
}
func (p *ExtensionPage) Show() {
	p.mu.Lock()
	ready := p.peer != nil && p.peer.ready
	p.mu.Unlock()
	if !ready {
		_ = p.open(p.setupURL())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = p.Call(ctx, "show")
}
func (p *ExtensionPage) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	peer := p.peer
	p.mu.Unlock()
	if peer != nil {
		peer.conn.Close()
	}
	return p.server.Close()
}

var extensionSetup = template.Must(template.New("setup").Parse(`<!doctype html><meta charset="utf-8"><title>MIXED 浏览器连接</title><style>body{font:18px/1.7 system-ui;max-width:780px;margin:64px auto;padding:24px;color:#172b26;background:#f4f6f4}h1{font-size:30px}code{overflow-wrap:anywhere;background:white;padding:8px}li{margin:16px 0}#status{padding:16px;background:white;border-radius:12px}</style><h1>MIXED 浏览器连接</h1><p id="status">首次使用请加载内置扩展。已经加载过时，刷新此页。</p><ol><li>在当前 Edge 地址栏打开 <code>edge://extensions</code>（Chrome 使用 <code>chrome://extensions</code>）。</li><li>打开“开发人员模式”，选择“加载解压缩的扩展”，选择这个文件夹：<p><code>{{.}}</code></p></li><li>返回本页并刷新。扩展将打开 Google 官方页面，使用这个浏览器现有的账号。</li><li>本人完成官方页面的登录、条款或验证后，回到 MIXED 点击“连接会话”。</li></ol><p>扩展随 EXE 提供，不下载上游。请保持来源标签页打开；不要在该标签页同时输入聊天。无需手动发送测试消息。</p>`))
