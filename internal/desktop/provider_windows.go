//go:build windows

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
	"golang.org/x/sys/windows"
)

const (
	providerEvalMessage  = 0x8001
	providerShowMessage  = 0x8002
	providerCloseMessage = 0x8003
)

type evaluation struct {
	ctx    context.Context
	source string
	result chan evaluationResult
}
type evaluationResult struct {
	raw json.RawMessage
	err error
}
type providerPage struct {
	id          string
	window      atomic.Uintptr
	closed      atomic.Bool
	ready, done chan struct{}
	readyOnce   sync.Once
	jobs        chan *evaluation
	active      *evaluation
	completed   *comHandler
}

var providerWindowSequence atomic.Uint64

func providerWindowClass(id string) string {
	return "MIXED-API.Provider." + id + "." + strconv.FormatUint(providerWindowSequence.Add(1), 10)
}

func newProviderPage(ctx context.Context, id string) (*providerPage, error) {
	p := &providerPage{id: id, ready: make(chan struct{}), done: make(chan struct{}), jobs: make(chan *evaluation, 16)}
	initial := "https://aistudio.google.com/prompts/new_chat"
	title := "MIXED · AIS 授权"
	if id == "aps" {
		initial = "https://console.cloud.google.com/vertex-ai/studio/multimodal"
		title = "MIXED · 无头V 授权"
	}
	go func() {
		defer close(p.done)
		runHost(&host{initial: initial, provider: p}, providerWindowClass(id), title, "provider-"+id)
	}()
	select {
	case <-p.ready:
		return p, nil
	case <-p.done:
		return nil, errors.New("来源窗口无法启动")
	case <-ctx.Done():
		p.Close()
		return nil, ctx.Err()
	}
}
func providerNavigation(id, raw string, frame bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := u.Hostname()
	if host == "accounts.google.com" {
		return true
	}
	if id == "aistudio" && host == "aistudio.google.com" {
		return true
	}
	if id == "aps" && host == "console.cloud.google.com" {
		return true
	}
	return frame && (host == "www.google.com" || host == "www.gstatic.com" || host == "recaptcha.google.com" || host == "www.recaptcha.net")
}
func (p *providerPage) install(h *host) error {
	// Register on document creation before navigation so capture observes only
	// the official page's own requests; no localhost bridge is ever installed.
	source, err := windows.UTF16PtrFromString(googleweb.RuntimeScript)
	if err != nil {
		return err
	}
	installed := newHandler("{b99369f3-9b11-47b5-bc6f-8e14f60785a8}", func(code uintptr, _ *comObject) uintptr {
		if int32(code) < 0 {
			h.fail(errors.New("无法初始化来源会话"))
		} else {
			if err := h.view.navigate(h.initial); err != nil {
				h.fail(err)
				return 0
			}
			p.readyOnce.Do(func() { close(p.ready) })
		}
		return 0
	})
	h.handlers = append(h.handlers, installed)
	p.completed = newHandler("{49511172-cc67-4bca-9923-137112f4c4cc}", func(code uintptr, value *comObject) uintptr {
		job := p.active
		p.active = nil
		if job != nil {
			result := evaluationResult{}
			if int32(code) < 0 || value == nil {
				result.err = errors.New("来源页面执行失败")
			} else {
				result.raw = json.RawMessage(windows.UTF16PtrToString((*uint16)(unsafe.Pointer(value))))
			}
			job.result <- result
		}
		user32.NewProc("PostMessageW").Call(h.window, providerEvalMessage, 0, 0)
		return 0
	})
	h.handlers = append(h.handlers, p.completed)
	return h.view.call(27, uintptr(unsafe.Pointer(source)), uintptr(unsafe.Pointer(installed)))
}
func (p *providerPage) executeNext(h *host) {
	if p.active != nil || h.view == nil {
		return
	}
	for {
		select {
		case job := <-p.jobs:
			if err := job.ctx.Err(); err != nil {
				job.result <- evaluationResult{err: err}
				continue
			}
			// Recheck exact provider origin inside the evaluated expression as well as
			// navigation policy, so a pending command cannot run on a login document.
			origin := "https://aistudio.google.com"
			if p.id == "aps" {
				origin = "https://console.cloud.google.com"
			}
			source, err := windows.UTF16PtrFromString("location.origin === '" + origin + "' ? (" + job.source + ") : null")
			if err != nil {
				job.result <- evaluationResult{err: err}
				continue
			}
			p.active = job
			if err = h.view.call(29, uintptr(unsafe.Pointer(source)), uintptr(unsafe.Pointer(p.completed))); err != nil {
				p.active = nil
				job.result <- evaluationResult{err: errors.New("来源窗口不可用")}
				continue
			}
			return
		default:
			return
		}
	}
}
func (p *providerPage) Eval(ctx context.Context, source string) (json.RawMessage, error) {
	if strings.ContainsRune(source, 0) {
		return nil, errors.New("invalid script")
	}
	job := &evaluation{ctx: ctx, source: source, result: make(chan evaluationResult, 1)}
	select {
	case p.jobs <- job:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, errors.New("来源窗口已关闭")
	}
	user32.NewProc("PostMessageW").Call(p.window.Load(), providerEvalMessage, 0, 0)
	select {
	case r := <-job.result:
		return r.raw, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, errors.New("来源窗口已关闭")
	}
}
func (p *providerPage) Show() {
	user32.NewProc("PostMessageW").Call(p.window.Load(), providerShowMessage, 0, 0)
}
func (p *providerPage) Close() error {
	p.closed.Store(true)
	if hwnd := p.window.Load(); hwnd != 0 {
		user32.NewProc("PostMessageW").Call(hwnd, providerCloseMessage, 0, 0)
	}
	return nil
}
