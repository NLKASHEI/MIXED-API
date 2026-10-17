//go:build windows

package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	kernel32        = windows.NewLazySystemDLL("kernel32.dll")
	ole32           = windows.NewLazySystemDLL("ole32.dll")
	shell32         = windows.NewLazySystemDLL("shell32.dll")
	windowHosts     sync.Map
	windowProcedure = windows.NewCallback(dispatchWindowMessage)
)

// A Go callback cannot be freed. Share one callback across replacement windows
// instead of retaining a new closure (and its host) after every recovery.
func dispatchWindowMessage(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	if h, ok := windowHosts.Load(hwnd); ok {
		return h.(*host).proc(hwnd, msg, wp, lp)
	}
	r, _, _ := user32.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), wp, lp)
	return r
}

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type point struct{ X, Y int32 }
type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type rectangle struct{ Left, Top, Right, Bottom int32 }
type host struct {
	window           uintptr
	base, initial    string
	controller, view *comObject
	err              error
	handlers         []*comHandler
	provider         *providerPage
	pumping          bool
}

func (h *host) EnvironmentCompleted(code webviewloader.HRESULT, env *webviewloader.ICoreWebView2Environment) webviewloader.HRESULT {
	if code < 0 || env == nil {
		h.fail(errors.New("无法初始化应用渲染环境"))
		return 0
	}
	handler := newHandler("{6c4819f3-c9b7-4260-8127-c9f5bde7f68c}", h.controllerReady)
	h.handlers = append(h.handlers, handler)
	// Both types represent the same Microsoft ICoreWebView2Environment ABI.
	environment := (*comObject)(unsafe.Pointer(env))
	if err := environment.call(3, h.window, uintptr(unsafe.Pointer(handler))); err != nil {
		h.fail(err)
	}
	return 0
}
func (h *host) controllerReady(code uintptr, c *comObject) uintptr {
	if int32(code) < 0 || c == nil {
		h.fail(errors.New("无法创建应用窗口内容"))
		return 0
	}
	_ = c.call(1)
	h.controller = c
	v, err := c.object(25)
	if err != nil {
		h.fail(err)
		return 0
	}
	h.view = v
	settings, err := v.object(3)
	if err != nil {
		h.fail(err)
		return 0
	}
	defer settings.release()
	// Disable web messages, status bar, devtools, context menu and host objects.
	for _, slot := range []int{6, 10, 12, 14, 16} {
		if err = settings.call(slot, 0); err != nil {
			h.fail(err)
			return 0
		}
	}
	for _, event := range []struct {
		slot     int
		iid      string
		callback func(uintptr, *comObject) uintptr
	}{{7, "{9adbe429-f36d-432b-9ddc-f8881fbd76e3}", h.navigation}, {17, "{9adbe429-f36d-432b-9ddc-f8881fbd76e3}", h.frameNavigation}, {44, "{d4c185fe-c81c-4989-97af-2d3fa7ab5651}", h.popup}, {23, "{15e1c6a3-c72a-4df3-91d7-d097fbec6bfd}", h.permission}} {
		handler := newHandler(event.iid, event.callback)
		h.handlers = append(h.handlers, handler)
		if err = v.on(event.slot, handler); err != nil {
			h.fail(err)
			return 0
		}
	}
	h.resize()
	if h.provider != nil {
		if err := h.provider.install(h); err != nil {
			h.fail(err)
			return 0
		}
	}
	if err = c.call(4, 1); err == nil {
		if h.provider == nil {
			err = v.navigate(h.initial)
		}
	}
	if err != nil {
		h.fail(err)
	}
	return 0
}
func (h *host) navigation(_ uintptr, args *comObject) uintptr {
	target, err := args.uri()
	if h.provider != nil {
		if err != nil || !providerNavigation(h.provider.id, target, false) {
			_ = args.call(8, 1)
		}
		return 0
	}
	if err == nil && localNavigation(h.base, target) {
		return 0
	}
	_ = args.call(8, 1) // NavigationStartingEventArgs.put_Cancel
	if err == nil && args.boolean(4) {
		h.openExternal(target)
	}
	return 0
}
func (h *host) frameNavigation(_ uintptr, args *comObject) uintptr {
	target, err := args.uri()
	if h.provider != nil {
		if err != nil || !providerNavigation(h.provider.id, target, true) {
			_ = args.call(8, 1)
		}
		return 0
	}
	if err != nil || !localNavigation(h.base, target) {
		_ = args.call(8, 1)
	}
	return 0
}
func (h *host) popup(_ uintptr, args *comObject) uintptr {
	_ = args.call(6, 1) // put_Handled
	target, err := args.uri()
	if err != nil || !args.boolean(8) {
		return 0
	}
	if h.provider != nil {
		if providerNavigation(h.provider.id, target, false) {
			_ = h.view.navigate(target)
		}
		return 0
	}
	if localNavigation(h.base, target) {
		_ = h.view.navigate(target)
	} else {
		h.openExternal(target)
	}
	return 0
}
func (*host) permission(_ uintptr, args *comObject) uintptr {
	_ = args.call(7, 2) // put_State(DENY)
	return 0
}
func (h *host) openExternal(target string) {
	if !externalLink(target) {
		return
	}
	verb, _ := windows.UTF16PtrFromString("open")
	link, _ := windows.UTF16PtrFromString(target)
	shell32.NewProc("ShellExecuteW").Call(h.window, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(link)), 0, 0, 1)
}
func (h *host) fail(err error) {
	h.err = err
	user32.NewProc("PostMessageW").Call(h.window, 0x10, 0, 0)
}
func (h *host) resize() {
	if h.controller == nil {
		return
	}
	var rect rectangle
	user32.NewProc("GetClientRect").Call(h.window, uintptr(unsafe.Pointer(&rect)))
	_ = h.controller.call(6, uintptr(unsafe.Pointer(&rect)))
}
func (h *host) proc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case providerEvalMessage:
		if h.provider != nil {
			h.provider.executeNext(h)
		}
		return 0
	case providerShowMessage:
		user32.NewProc("ShowWindow").Call(hwnd, 1)
		user32.NewProc("SetForegroundWindow").Call(hwnd)
		return 0
	case providerCloseMessage:
		user32.NewProc("DestroyWindow").Call(hwnd)
		return 0
	case 0x5:
		h.resize()
	case 0x7:
		if h.controller != nil {
			_ = h.controller.call(12, 0)
		}
	case 0x10:
		if h.provider != nil && h.err == nil {
			user32.NewProc("ShowWindow").Call(hwnd, 0)
			return 0
		}
		user32.NewProc("DestroyWindow").Call(hwnd)
		return 0
	case 0x2:
		if h.pumping {
			user32.NewProc("PostQuitMessage").Call(0)
		}
		return 0
	case 0x82: // WM_NCDESTROY: the HWND may be reused before COM cleanup finishes.
		windowHosts.CompareAndDelete(hwnd, h)
	}
	r, _, _ := user32.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

// Run owns one STA window; its caller owns the HTTP gateway lifetime. No
// JavaScript/native bridge is installed. The token is sent only to the exact
// local origin in a fragment, removed by the shared UI immediately on load.
func Run(base, admin string) error {
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return errors.New("应用窗口只能连接本机网关")
	}
	if admin == "" {
		return errors.New("missing admin credential")
	}
	return runHost(&host{base: base, initial: base + "/#admin=" + url.QueryEscape(admin)}, "MIXED-API.Desktop", "MIXED · 模型工作台", "desktop-profile")
}

func runHost(h *host, class, caption, profileName string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	renderer, err := rendererPath()
	if err != nil {
		return err
	}
	profile, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	profile = filepath.Join(profile, "MIXED-API", profileName)
	if err = os.MkdirAll(profile, 0700); err != nil {
		return err
	}
	hr, _, _ := ole32.NewProc("CoInitializeEx").Call(0, 2)
	if int32(hr) < 0 {
		return fmt.Errorf("COM initialization failed: 0x%x", hr)
	}
	defer ole32.NewProc("CoUninitialize").Call()
	if p := user32.NewProc("SetProcessDpiAwarenessContext"); p.Find() == nil {
		p.Call(^uintptr(3))
	}
	className, _ := windows.UTF16PtrFromString(class)
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
	wc := windowClass{Proc: windowProcedure, Instance: instance, Cursor: cursor, Name: className, Background: 6}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if atom, _, e := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return e
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(className)), instance)
	title, _ := windows.UTF16PtrFromString(caption)
	hwnd, _, err := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), 0x00cf0000, 0x80000000, 0x80000000, 1220, 860, 0, 0, instance, 0)
	if hwnd == 0 {
		return err
	}
	h.window = hwnd
	windowHosts.Store(hwnd, h)
	defer windowHosts.CompareAndDelete(hwnd, h)
	if h.provider != nil {
		h.provider.window.Store(hwnd)
		if h.provider.closed.Load() {
			user32.NewProc("DestroyWindow").Call(hwnd)
			return context.Canceled
		}
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)
	user32.NewProc("ShowWindow").Call(hwnd, 1)
	user32.NewProc("UpdateWindow").Call(hwnd)
	if err = webviewloader.CreateCoreWebView2EnvironmentWithOptions(h, webviewloader.WithBrowserExecutableFolder(renderer), webviewloader.WithUserDataFolder(profile)); err != nil {
		return errors.New("应用渲染器无法启动，请使用包含渲染资源的完整发布包")
	}
	var msg message
	h.pumping = true
	for {
		ret, _, e := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) == -1 {
			h.err = e
			break
		}
		if ret == 0 {
			break
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	h.pumping = false
	if h.controller != nil {
		_ = h.controller.call(24)
		h.controller.release()
	}
	h.view.release()
	runtime.KeepAlive(h)
	return h.err
}

// NewGoogleSessions creates provider windows lazily, only after the user opens
// the corresponding authorization step. It never opens a terminal/process.
func NewGoogleSessions() *googleweb.Browser {
	return googleweb.New(func(ctx context.Context, id string) (googleweb.Page, error) { return newProviderPage(ctx, id) })
}
