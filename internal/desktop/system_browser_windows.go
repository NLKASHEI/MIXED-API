//go:build windows

package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/page"
	cdruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"golang.org/x/sys/windows"
)

type systemBrowserPage struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	allocatorCancel     context.CancelFunc
	origin              string
	release             func()
	executable, profile string
	endpoint            string
	once                sync.Once
}

func defaultBrowserExecutable() (string, error) {
	protocol, _ := windows.UTF16PtrFromString("https")
	var size uint32 = 32768
	buf := make([]uint16, size)
	result, _, _ := windows.NewLazySystemDLL("shlwapi.dll").NewProc("AssocQueryStringW").Call(0, 2, uintptr(unsafe.Pointer(protocol)), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if result != 0 {
		return "", &googleweb.SetupError{Message: "无法读取系统默认浏览器，请先在 Windows 设置中配置默认浏览器"}
	}
	path := windows.UTF16ToString(buf)
	switch strings.ToLower(filepath.Base(path)) {
	case "msedge.exe", "chrome.exe":
	default:
		return "", &googleweb.SetupError{Message: "当前默认浏览器暂不支持会话连接；目前支持 Edge 或 Chrome，不会自动改用内嵌浏览器"}
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("invalid default browser")
	}
	if _, err := os.Stat(path); err != nil {
		return "", errors.New("default browser is missing")
	}
	return path, nil
}

func newSystemBrowserPage(ctx context.Context, id, root string) (googleweb.Page, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = absolute
	initial := "https://aistudio.google.com/prompts/new_chat"
	if id == "aps" {
		initial = "https://console.cloud.google.com/vertex-ai/studio/multimodal"
	} else if id != "aistudio" {
		return nil, errors.New("unknown source")
	}
	exe, err := defaultBrowserExecutable()
	if err != nil {
		return nil, err
	}
	p, err := launchSystemBrowser(ctx, exe, filepath.Join(root, id), initial)
	if err != nil {
		return nil, &googleweb.SetupError{Message: "默认浏览器连接失败；请关闭 MIXED 专用的来源浏览器窗口后重试，或检查浏览器策略是否禁用了本机调试连接"}
	}
	return p, nil
}

func launchSystemBrowser(ctx context.Context, exe, profile, initial string) (*systemBrowserPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		return nil, err
	}
	location, err := url.Parse(initial)
	if err != nil {
		return nil, err
	}
	// Edge can relaunch unelevated and close the launcher's stdout. Discover
	// only the endpoint written inside our own isolated, exclusively held profile.
	lockName, _ := windows.UTF16PtrFromString(fmt.Sprintf("Local\\MIXED.Browser.%x", sha256.Sum256([]byte(profile))))
	lock, lockErr := windows.CreateMutex(nil, false, lockName)
	if lockErr != nil {
		if lock != 0 {
			windows.CloseHandle(lock)
		}
		return nil, errors.New("profile already in use")
	}
	release := func() { windows.CloseHandle(lock) }
	portFile := filepath.Join(profile, "DevToolsActivePort")
	if raw, err := os.ReadFile(portFile); err == nil {
		if endpoint := browserEndpoint(raw); endpoint != "" && endpointAlive(endpoint) {
			release()
			return nil, errors.New("close the previous source browser first")
		}
		if err := os.Remove(portFile); err != nil {
			release()
			return nil, err
		}
	}
	cmd := exec.Command(exe, "--user-data-dir="+profile, "--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1", "--no-first-run", "--no-default-browser-check", "about:blank")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		release()
		return nil, err
	}
	go cmd.Wait()
	endpoint, err := awaitBrowserEndpoint(ctx, portFile, 45*time.Second)
	if err != nil {
		// Edge's launcher can exit before its unelevated child starts. Give that
		// owned child a bounded cleanup window rather than killing unrelated Edge.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		late, _ := awaitBrowserEndpoint(cleanup, portFile, 5*time.Second)
		cancel()
		if late != "" {
			closeBrowserEndpoint(late)
		} else {
			_ = cmd.Process.Kill()
		}
		stopOwnedBrowser(exe, profile)
		release()
		return nil, err
	}
	allocator, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), endpoint, chromedp.NoModifyURL)
	browserCtx, cancel := chromedp.NewContext(allocator, chromedp.WithErrorf(func(string, ...any) {}))
	p := &systemBrowserPage{ctx: browserCtx, cancel: cancel, allocatorCancel: allocatorCancel, origin: location.Scheme + "://" + location.Host, endpoint: endpoint, executable: exe, profile: profile, release: func() {
		if !endpointAlive(endpoint) {
			os.Remove(portFile)
		}
		release()
	}}
	// Allocate on the lifetime context; cancelling startup must not close a
	// successfully opened browser. Errors never include browser output or cookies.
	timeout := time.AfterFunc(45*time.Second, cancel)
	stop := context.AfterFunc(ctx, cancel)
	err = chromedp.Run(browserCtx, chromedp.ActionFunc(func(c context.Context) error {
		_, e := page.AddScriptToEvaluateOnNewDocument(googleweb.RuntimeScript).Do(c)
		return e
	}), chromedp.Navigate(initial))
	timeout.Stop()
	stop()
	if err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *systemBrowserPage) Eval(ctx context.Context, expression string) (json.RawMessage, error) {
	work, cancel := context.WithCancel(p.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	origin, _ := json.Marshal(p.origin)
	script := "location.origin===" + string(origin) + " ? (" + expression + ") : ({status:'waiting',message:'请在默认浏览器完成本人登录，再返回检查连接'})"
	var result json.RawMessage
	err := chromedp.Run(work, chromedp.Evaluate(script, &result, func(p *cdruntime.EvaluateParams) *cdruntime.EvaluateParams { return p.WithAwaitPromise(true) }))
	return result, err
}
func (p *systemBrowserPage) Show() {
	ctx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error { return page.BringToFront().Do(c) }))
}
func (p *systemBrowserPage) Alive() bool {
	if p.ctx.Err() != nil || !endpointAlive(p.endpoint) {
		return false
	}
	ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
	defer cancel()
	return chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		_, err := target.GetTargetInfo().WithTargetID(chromedp.FromContext(p.ctx).Target.TargetID).Do(c)
		return err
	})) == nil
}
func (p *systemBrowserPage) Close() error {
	p.once.Do(func() {
		closeBrowserEndpoint(p.endpoint)
		stopOwnedBrowser(p.executable, p.profile)
		p.cancel()
		p.allocatorCancel()
		p.release()
	})
	return nil
}

func browserEndpoint(raw []byte) string {
	rows := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(rows) != 2 {
		return ""
	}
	port, err := strconv.Atoi(strings.TrimSpace(rows[0]))
	path := strings.TrimSpace(rows[1])
	if err != nil || port < 1 || port > 65535 || !strings.HasPrefix(path, "/devtools/browser/") || strings.ContainsAny(path, "?#\\") {
		return ""
	}
	return fmt.Sprintf("ws://127.0.0.1:%d%s", port, path)
}
func endpointAlive(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return false
	}
	c, err := net.DialTimeout("tcp", u.Host, 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
func awaitBrowserEndpoint(ctx context.Context, path string, limit time.Duration) (string, error) {
	timeout := time.NewTimer(limit)
	defer timeout.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		raw, _ := os.ReadFile(path)
		if endpoint := browserEndpoint(raw); endpoint != "" {
			return endpoint, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errors.New("browser endpoint unavailable")
		case <-tick.C:
		}
	}
}
func closeBrowserEndpoint(endpoint string) {
	if endpoint == "" || !endpointAlive(endpoint) {
		return
	}
	alloc, stop := chromedp.NewRemoteAllocator(context.Background(), endpoint, chromedp.NoModifyURL)
	defer stop()
	ctx, cancel := chromedp.NewContext(alloc, chromedp.WithErrorf(func(string, ...any) {}))
	defer cancel()
	timer := time.AfterFunc(3*time.Second, cancel)
	defer timer.Stop()
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error { return browser.Close().Do(c) }))
	for deadline := time.Now().Add(2 * time.Second); endpointAlive(endpoint) && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
	}
}
