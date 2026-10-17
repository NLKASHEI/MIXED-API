package googleweb

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed runtime.js
var RuntimeScript string

// Page is implemented by the platform-owned official browser session.
// Eval is not reachable from an HTTP payload or the management web page.
type Page interface {
	Eval(context.Context, string) (json.RawMessage, error)
	Show()
	Close() error
}
type Factory func(context.Context, string) (Page, error)

// SessionError carries only fixed diagnostic codes, never browser exceptions.
type SessionError struct{ Code string }

func (e *SessionError) Error() string { return e.Code }
func sessionFailure(code string) error {
	switch code {
	case "authorization_required", "session_lost", "page_rejected", "page_preparation", "browser_network", "cancelled":
	default:
		code = "page_request_failed"
	}
	return &SessionError{Code: code}
}

type browserSession struct {
	page   Page
	gate   chan struct{}
	broken atomic.Bool
}
type Browser struct {
	mu       sync.Mutex
	factory  Factory
	sessions map[string]*browserSession
	starting map[string]chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
}

func New(factory Factory) *Browser {
	ctx, cancel := context.WithCancel(context.Background())
	return &Browser{factory: factory, sessions: map[string]*browserSession{}, starting: map[string]chan struct{}{}, ctx: ctx, cancel: cancel}
}
func (b *Browser) Open(ctx context.Context, id string) error {
	if id != "aps" && id != "aistudio" {
		return errors.New("unknown source")
	}
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return errors.New("browser closed")
		}
		if s := b.sessions[id]; s != nil && !s.broken.Load() {
			if live, ok := s.page.(interface{ Alive() bool }); ok && !live.Alive() {
				s.broken.Store(true)
				b.mu.Unlock()
				s.page.Close()
				continue
			}
			b.mu.Unlock()
			s.page.Show()
			return nil
		}
		if pending := b.starting[id]; pending != nil {
			b.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return ctx.Err()
			case <-b.ctx.Done():
				return b.ctx.Err()
			}
		}
		pending := make(chan struct{})
		b.starting[id] = pending
		b.mu.Unlock()
		openCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(b.ctx, cancel)
		page, err := b.factory(openCtx, id)
		stop()
		cancel()
		b.mu.Lock()
		closed := b.closed
		if closed && err == nil {
			err = errors.New("browser closed")
		}
		if err == nil {
			b.sessions[id] = &browserSession{page: page, gate: make(chan struct{}, 1)}
		}
		delete(b.starting, id)
		close(pending)
		b.mu.Unlock()
		if err != nil && page != nil {
			page.Close()
		}
		return err
	}
}
func (b *Browser) session(id string) *browserSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sessions[id]
	if s != nil && s.broken.Load() {
		return nil
	}
	return s
}
func (b *Browser) State(ctx context.Context, id string) State {
	s := b.session(id)
	if s == nil {
		return State{Status: "none", Message: "请打开来源授权窗口"}
	}
	raw, err := callPage(ctx, s.page, "state")
	var result State
	if err != nil || json.Unmarshal(raw, &result) != nil || result.Status == "" {
		return State{Status: "waiting", Message: "请在官方页面完成本人授权"}
	}
	return result
}

// Initialize prepares the official page; it does not certify model inference.
func (b *Browser) Initialize(ctx context.Context, id string) State {
	s := b.session(id)
	if s == nil {
		return State{Status: "none", Message: "请先打开来源页面"}
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return State{Status: "waiting", Message: "连接会话已取消"}
	case <-b.ctx.Done():
		return State{Status: "none", Message: "应用正在关闭"}
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if ack, err := callPage(cleanup, s.page, "cancelInitialize"); err != nil || string(ack) != "true" {
			s.broken.Store(true)
			s.page.Close()
		}
		<-s.gate
	}()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(b.ctx, cancel)
	defer stop()
	raw, err := callPage(work, s.page, "initialize")
	var result State
	if err != nil || json.Unmarshal(raw, &result) != nil || result.Status == "" {
		return State{Status: "waiting", Message: "无法连接来源页面，请重新打开"}
	}
	for result.Status == "initializing" {
		if wait(work) != nil {
			return State{Status: "waiting", Message: "连接会话超时或取消，请检查官方页面"}
		}
		result = b.State(work, id)
	}
	return result
}

func (b *Browser) Send(ctx context.Context, id string, op Operation) (*http.Response, error) {
	if (id == "aps" && op.Kind != "aps") || (id == "aistudio" && op.Kind != "build" && op.Kind != "playground" && op.Kind != "models" && op.Kind != "build_models") {
		return nil, errors.New("operation does not match source")
	}
	s := b.session(id)
	if s == nil {
		return nil, sessionFailure("authorization_required")
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.ctx.Done():
		return nil, b.ctx.Err()
	}
	if s.broken.Load() {
		<-s.gate
		return nil, errors.New("provider window must be reopened")
	}
	data := make([]byte, 24)
	if _, err := rand.Read(data); err != nil {
		<-s.gate
		return nil, err
	}
	key := hex.EncodeToString(data)
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(b.ctx, cancel)
	body := &browserBody{ctx: requestCtx, cancel: func() { stop(); cancel() }, session: s, key: key}
	raw, err := callPage(requestCtx, s.page, "start", key, op)
	if err != nil || string(raw) != "true" {
		body.Close()
		return nil, sessionFailure("page_rejected")
	}
	for {
		state, err := body.poll()
		if err != nil {
			body.Close()
			return nil, err
		}
		if state.Status > 0 {
			body.pending = state.Chunk
			body.done = state.Done
			body.failed = state.Error != ""
			header := http.Header{}
			for k, v := range state.Headers {
				header.Set(k, v)
			}
			return &http.Response{StatusCode: state.Status, Header: header, Body: body}, nil
		}
		if state.Done || state.Error != "" {
			body.Close()
			return nil, sessionFailure(state.Error)
		}
		if err := wait(ctx); err != nil {
			body.Close()
			return nil, err
		}
	}
}
func (b *Browser) Close() error {
	b.mu.Lock()
	b.closed = true
	b.cancel()
	pages := []Page{}
	for _, s := range b.sessions {
		pages = append(pages, s.page)
	}
	b.sessions = map[string]*browserSession{}
	b.mu.Unlock()
	for _, p := range pages {
		p.Close()
	}
	return nil
}

type browserRead struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Chunk   string            `json:"chunk"`
	Done    bool              `json:"done"`
	Error   string            `json:"error"`
}
type browserBody struct {
	ctx          context.Context
	cancel       context.CancelFunc
	session      *browserSession
	key          string
	pending      string
	decoded      []byte
	done, failed bool
	once         sync.Once
}

func (b *browserBody) poll() (browserRead, error) {
	var state browserRead
	raw, err := callPage(b.ctx, b.session.page, "read", b.key)
	if err != nil {
		return state, sessionFailure("session_lost")
	}
	if json.Unmarshal(raw, &state) != nil {
		return state, errors.New("invalid browser frame")
	}
	if len(state.Chunk) > 48<<10 {
		return state, errors.New("browser frame too large")
	}
	return state, nil
}
func (b *browserBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(b.decoded) == 0 {
		if err := b.ctx.Err(); err != nil {
			return 0, err
		}
		if b.pending != "" {
			var err error
			b.decoded, err = base64.StdEncoding.DecodeString(b.pending)
			b.pending = ""
			if err != nil {
				return 0, errors.New("invalid browser encoding")
			}
			break
		}
		if b.done {
			if b.failed {
				return 0, sessionFailure("page_request_failed")
			}
			return 0, io.EOF
		}
		state, err := b.poll()
		if err != nil {
			return 0, err
		}
		b.pending, b.done, b.failed = state.Chunk, state.Done, state.Error != ""
		if b.pending == "" && !b.done {
			if err = wait(b.ctx); err != nil {
				return 0, err
			}
		}
	}
	n := copy(p, b.decoded)
	b.decoded = b.decoded[n:]
	return n, nil
}
func (b *browserBody) Close() error {
	b.once.Do(func() {
		b.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if ack, err := callPage(ctx, b.session.page, "cancel", b.key); err != nil || string(ack) != "true" {
			// Do not leave an unbounded provider fetch alive when its UI cannot
			// process the abort. A subsequent explicit Open creates a new page.
			b.session.broken.Store(true)
			b.session.page.Close()
		}
		<-b.session.gate
	})
	return nil
}
func wait(ctx context.Context) error {
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Native legacy pages use private evaluation; the extension accepts only typed
// commands and never exposes an arbitrary evaluation endpoint.
func callPage(ctx context.Context, p Page, method string, args ...any) (json.RawMessage, error) {
	if typed, ok := p.(interface {
		Call(context.Context, string, ...any) (json.RawMessage, error)
	}); ok {
		return typed.Call(ctx, method, args...)
	}
	input := ""
	for i, arg := range args {
		raw, err := json.Marshal(arg)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			input += ","
		}
		input += string(raw)
	}
	return p.Eval(ctx, "window.__mixedGoogleWeb?."+method+"("+input+")")
}
