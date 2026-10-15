//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/NLKASHEI/MIXED-API/internal/gateway"
)

// Windows native UI. No WebView, PowerShell, browser or external GUI runtime.
// The existing HTTP listener stays behind the application and serves client APIs.
var (
	user32          = syscall.NewLazyDLL("user32.dll")
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	gdi32           = syscall.NewLazyDLL("gdi32.dll")
	pRegister       = user32.NewProc("RegisterClassExW")
	pCreate         = user32.NewProc("CreateWindowExW")
	pDefProc        = user32.NewProc("DefWindowProcW")
	pDestroy        = user32.NewProc("DestroyWindow")
	pPostQuit       = user32.NewProc("PostQuitMessage")
	pGetMsg         = user32.NewProc("GetMessageW")
	pTranslate      = user32.NewProc("TranslateMessage")
	pDispatch       = user32.NewProc("DispatchMessageW")
	pSend           = user32.NewProc("SendMessageW")
	pSetText        = user32.NewProc("SetWindowTextW")
	pMove           = user32.NewProc("MoveWindow")
	pGetRect        = user32.NewProc("GetClientRect")
	pPost           = user32.NewProc("PostMessageW")
	pCreateFont     = gdi32.NewProc("CreateFontW")
	pGetModule      = kernel32.NewProc("GetModuleHandleW")
	pOpenClipboard  = user32.NewProc("OpenClipboard")
	pEmptyClipboard = user32.NewProc("EmptyClipboard")
	pSetClipboard   = user32.NewProc("SetClipboardData")
	pCloseClipboard = user32.NewProc("CloseClipboard")
	pGlobalAlloc    = kernel32.NewProc("GlobalAlloc")
	pGlobalLock     = kernel32.NewProc("GlobalLock")
	pGlobalUnlock   = kernel32.NewProc("GlobalUnlock")
	pMessageBox     = user32.NewProc("MessageBoxW")
)

const (
	wsOverlapped  = 0x00CF0000
	wsVisible     = 0x10000000
	wsChild       = 0x40000000
	wsVScroll     = 0x00200000
	esMultiline   = 0x0004
	esReadOnly    = 0x0800
	esAutoVScroll = 0x0040
	bsPushButton  = 0x00000000
	wmCommand     = 0x0111
	wmClose       = 0x0010
	wmDestroy     = 0x0002
	wmSize        = 0x0005
	wmSetFont     = 0x0030
	wmUserUpdate  = 0x8001
	cfUnicodeText = 13
)

type nativeClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	SmallIcon  uintptr
}
type nativeRect struct{ Left, Top, Right, Bottom int32 }
type nativePoint struct{ X, Y int32 }
type nativeMsg struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             nativePoint
	Private        uint32
}
type nativeView struct {
	hwnd                                 uintptr
	key, admin, base                     string
	buttons                              [6]uintptr
	labels                               [6]uintptr
	apiLabel, logBox, footer             uintptr
	font, heading                        uintptr
	mu                                   sync.Mutex
	logText, providerSummary, footerText string
	client                               *http.Client
	stop                                 chan struct{}
}

var desktop *nativeView
var providers = [...]struct{ ID, Label string }{
	{"codex", "Codex · ChatGPT"}, {"vertex", "Vertex · Gemini"}, {"opencode", "OpenCode"},
	{"workbuddy", "WorkBuddy"}, {"aps", "APS2api"}, {"aistudio", "AI Studio Build"},
}

func wz(s string) *uint16               { return syscall.StringToUTF16Ptr(s) }
func putText(hwnd uintptr, text string) { pSetText.Call(hwnd, uintptr(unsafe.Pointer(wz(text)))) }
func createChild(class, txt string, id int, parent uintptr, style uintptr) uintptr {
	h, _, _ := pCreate.Call(0, uintptr(unsafe.Pointer(wz(class))), uintptr(unsafe.Pointer(wz(txt))), wsVisible|wsChild|style, 0, 0, 100, 30, parent, uintptr(id), 0, 0)
	if desktop != nil && desktop.font != 0 {
		pSend.Call(h, wmSetFont, desktop.font, 1)
	}
	return h
}
func (v *nativeView) position() {
	if v.hwnd == 0 {
		return
	}
	var r nativeRect
	pGetRect.Call(v.hwnd, uintptr(unsafe.Pointer(&r)))
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w < 700 {
		w = 700
	}
	if h < 550 {
		h = 550
	}
	margin := 24
	available := w - margin*2
	half := (available - 16) / 2
	pMove.Call(v.apiLabel, uintptr(margin), 86, uintptr(available), 50, 1)
	for i := range v.buttons {
		col := i % 2
		row := i / 2
		x := margin + col*(half+16)
		y := 176 + row*58
		pMove.Call(v.labels[i], uintptr(x), uintptr(y), uintptr(half-132), 36, 1)
		pMove.Call(v.buttons[i], uintptr(x+half-126), uintptr(y), 124, 36, 1)
	}
	pMove.Call(v.logBox, uintptr(margin), 396, uintptr(available), uintptr(max(100, h-485)), 1)
	pMove.Call(v.footer, uintptr(margin), uintptr(h-71), uintptr(available), 36, 1)
}
func nativeCallback(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	v := desktop
	switch msg {
	case wmSize:
		if v != nil && v.hwnd != 0 {
			v.position()
		}
		return 0
	case wmCommand:
		if v == nil {
			return 0
		}
		id := int(wp & 0xffff)
		switch {
		case id == 100:
			copyNative(v.base)
			putText(v.footer, "已复制统一 API 地址")
		case id == 101:
			copyNative(v.key)
			putText(v.footer, "已复制客户端 API Key · 只发给自己的应用")
		case id == 102:
			go v.executeUpdate()
		case id == 103:
			_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", v.base[:len(v.base)-3]).Start()
		case id >= 200 && id < 206:
			go v.connectProvider(providers[id-200].ID)
		case id == 104:
			go v.refresh()
		}
		return 0
	case wmUserUpdate:
		if v != nil {
			v.mu.Lock()
			logs, summary, footer := v.logText, v.providerSummary, v.footerText
			v.mu.Unlock()
			if len(logs) > 0 {
				putText(v.logBox, logs)
			}
			if footer != "" {
				putText(v.footer, footer)
			}
			if summary != "" {
				statuses := strings.Split(summary, "\n")
				for i := range v.labels {
					if i < len(statuses) {
						putText(v.labels[i], statuses[i])
					}
				}
			}
		}
		return 0
	case wmClose:
		pDestroy.Call(hwnd)
		return 0
	case wmDestroy:
		if v != nil {
			close(v.stop)
		}
		pPostQuit.Call(0)
		return 0
	}
	value, _, _ := pDefProc.Call(hwnd, uintptr(msg), wp, lp)
	return value
}
func copyNative(s string) {
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	wide := syscall.StringToUTF16(s)
	size := uintptr(len(wide) * 2)
	h, _, _ := pGlobalAlloc.Call(2, size) // GMEM_MOVEABLE
	if h == 0 {
		return
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(wide)), wide)
	pGlobalUnlock.Call(h)
	pSetClipboard.Call(cfUnicodeText, h)
}
func (v *nativeView) request(method, path string) ([]byte, error) {
	req, e := http.NewRequest(method, "http://127.0.0.1:8765"+path, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+v.admin)
	resp, e := v.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, 400000))
	if e != nil {
		return nil, e
	}
	if resp.StatusCode > 299 {
		return body, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
func (v *nativeView) notify(msg string) {
	v.mu.Lock()
	v.footerText = msg
	v.mu.Unlock()
	pPost.Call(v.hwnd, wmUserUpdate, 0, 0)
}
func (v *nativeView) connectProvider(id string) {
	v.notify("正在接入 " + id + "…… 软件会自动配置；需要授权时会提示你。")
	_, e := v.request("POST", "/api/assist/"+id+"/quickstart")
	if e != nil {
		v.notify(id + " 接入操作失败：" + e.Error())
		return
	}
	v.notify(id + " 正在处理。下方日志会显示真正的请求结果。")
	time.Sleep(700 * time.Millisecond)
	v.refresh()
}
func (v *nativeView) executeUpdate() {
	v.notify("正在检查 GitHub 正式版更新……")
	body, e := v.request("GET", "/api/update")
	if e != nil {
		v.notify("更新检查失败：" + e.Error())
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		v.notify("已检查最新版：" + string(body)[:min(240, len(body))])
	}
	// Give user a permanent official download entry; updating is never silent.
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", "https://github.com/NLKASHEI/MIXED-API/releases/latest").Start()
}
func (v *nativeView) refresh() {
	var logs struct {
		Entries []struct {
			Time    string `json:"time"`
			Method  string `json:"method"`
			Path    string `json:"path"`
			Backend string `json:"backend"`
			Status  int    `json:"status"`
			Ms      int64  `json:"duration_ms"`
			Error   string `json:"error"`
		} `json:"entries"`
	}
	raw, e := v.request("GET", "/api/logs")
	if e == nil {
		_ = json.Unmarshal(raw, &logs)
	}
	var lines []string
	for i := len(logs.Entries) - 1; i >= 0 && len(lines) < 45; i-- {
		z := logs.Entries[i]
		stamp := z.Time
		if len(stamp) > 19 {
			stamp = stamp[11:19]
		}
		err := z.Error
		if len([]rune(err)) > 140 {
			err = string([]rune(err)[:140])
		}
		line := fmt.Sprintf("%s  %d  %-7s  %s  [%s]  %dms", stamp, z.Status, z.Method, z.Path, z.Backend, z.Ms)
		if err != "" {
			line += "  错误: " + err
		}
		lines = append(lines, line)
	}
	statusRaw, statusErr := v.request("GET", "/api/status")
	providerStates := map[string]string{}
	if statusErr == nil {
		var parsed struct {
			Processes map[string]string `json:"processes"`
		}
		_ = json.Unmarshal(statusRaw, &parsed)
		providerStates = parsed.Processes
	}
	var ss []string
	for _, p := range providers {
		state := providerStates["setup:"+p.ID]
		if state == "" {
			state = providerStates[p.ID]
		}
		if state == "" {
			state = "未接入"
		}
		if len([]rune(state)) > 24 {
			state = string([]rune(state)[:24]) + "…"
		}
		ss = append(ss, p.Label+"  ·  "+state)
	}
	v.mu.Lock()
	if len(lines) == 0 {
		v.logText = "暂无请求。连接任意客户端后，这里会显示状态码及错误原因。"
	} else {
		v.logText = strings.Join(lines, "\r\n")
	}
	v.providerSummary = strings.Join(ss, "\n")
	v.mu.Unlock()
	pPost.Call(v.hwnd, wmUserUpdate, 0, 0)
}
func startNative(cfg gateway.Config, s *gateway.Server, lan bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	listener, e := net.Listen("tcp", cfg.Listen)
	if e != nil {
		return fmt.Errorf("本机端口已被使用：%w", e)
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 12 * time.Second, IdleTimeout: 90 * time.Second}
	defer srv.Close()
	go srv.Serve(listener)
	if lan {
		ln, err := net.Listen("tcp4", "0.0.0.0:8766")
		if err != nil {
			return err
		}
		defer ln.Close()
		share := &http.Server{Handler: gateway.APIOnly(s), ReadHeaderTimeout: 12 * time.Second, IdleTimeout: 90 * time.Second}
		defer share.Close()
		go share.Serve(ln)
	}
	c := &nativeView{key: cfg.APIKey, admin: cfg.AdminKey, base: "http://127.0.0.1:8765/v1", stop: make(chan struct{}), client: &http.Client{Timeout: 12 * time.Second}}
	desktop = c
	hinst, _, _ := pGetModule.Call(0)
	cl := wz("MIXEDAPINativeWin")
	wc := nativeClass{Size: uint32(unsafe.Sizeof(nativeClass{})), WndProc: syscall.NewCallback(nativeCallback), Instance: hinst, Background: 6, ClassName: cl}
	if result, _, er := pRegister.Call(uintptr(unsafe.Pointer(&wc))); result == 0 {
		return fmt.Errorf("窗口初始化失败：%w", er)
	}
	hwnd, _, e := pCreate.Call(0, uintptr(unsafe.Pointer(cl)), uintptr(unsafe.Pointer(wz("MIXED-API · 本地 AI 聚合器"))), wsOverlapped|wsVisible, 160, 100, 1050, 790, 0, 0, hinst, 0)
	if hwnd == 0 {
		return fmt.Errorf("创建窗口失败：%w", e)
	}
	c.hwnd = hwnd
	fontName := wz("Microsoft YaHei UI")
	normalSize := int32(-19)
	titleSize := int32(-28)
	c.font, _, _ = pCreateFont.Call(uintptr(normalSize), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(fontName)))
	c.heading, _, _ = pCreateFont.Call(uintptr(titleSize), 0, 0, 0, 700, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(fontName)))
	head := createChild("STATIC", "MIXED-API     本地聚合中转站", 1, hwnd, 0)
	pMove.Call(head, 24, 18, 580, 46, 1)
	pSend.Call(head, wmSetFont, c.heading, 1)
	c.apiLabel = createChild("STATIC", "● 服务运行中     地址：http://127.0.0.1:8765/v1\r\n只需一个地址、一个 Key；选择来源即可接入。", 2, hwnd, 0)
	buttons := []struct {
		label string
		id    int
		x     int
	}{{"复制地址", 100, 24}, {"复制 Key", 101, 155}, {"检查更新", 102, 286}, {"高级设置（可选）", 103, 418}, {"刷新日志", 104, 629}}
	for _, b := range buttons {
		h := createChild("BUTTON", b.label, b.id, hwnd, bsPushButton)
		pMove.Call(h, uintptr(b.x), 139, uintptr(map[bool]int{true: 176, false: 120}[b.id == 103]), 34, 1)
	}
	for i, p := range providers {
		c.labels[i] = createChild("STATIC", p.Label+" · 未接入", 300+i, hwnd, 0)
		c.buttons[i] = createChild("BUTTON", "一键接入", 200+i, hwnd, 0)
	}
	title := createChild("STATIC", "实时请求日志（失败请求显示状态码和原因，无需打开网页）", 10, hwnd, 0)
	pMove.Call(title, 24, 371, 800, 24, 1)
	c.logBox = createChild("EDIT", "暂无请求", 11, hwnd, wsVScroll|esMultiline|esAutoVScroll|esReadOnly|0x00800000) // WS_BORDER
	c.footer = createChild("STATIC", "● 已启动 · 从上方选择需要的来源；登录授权由你本人完成。", 12, hwnd, 0)
	c.position()
	go func() {
		c.refresh()
		t := time.NewTicker(2800 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.refresh()
			case <-c.stop:
				return
			}
		}
	}()
	var msg nativeMsg
	for {
		rv, _, _ := pGetMsg.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(rv) <= 0 {
			break
		}
		pTranslate.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatch.Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}
