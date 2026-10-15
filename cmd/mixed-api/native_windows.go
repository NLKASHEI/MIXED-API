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
	"slices"
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
	pGetText        = user32.NewProc("GetWindowTextW")
	pGetTextLength  = user32.NewProc("GetWindowTextLengthW")
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
	pShow           = user32.NewProc("ShowWindow")
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
	swHide        = 0
	swShow        = 5
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
	hwnd                                                                      uintptr
	key, admin, base                                                          string
	buttons                                                                   [6]uintptr
	labels                                                                    [6]uintptr
	apiLabel, logBox, footer                                                  uintptr
	providerChoice, officialKey, officialHelp, officialSave                   uintptr
	guideTitle, guideStage, guideText, modelLabel, modelButton, refreshModels uintptr
	navTitle, logTitle, retryButton, authButton, header, modelChoice          uintptr
	font, heading                                                             uintptr
	mu                                                                        sync.Mutex
	logText, footerText, guideBody, guideHeader, guideState, modelText        string
	selected                                                                  string
	states                                                                    map[string]string
	rowStatus                                                                 [6]string
	modelIDs                                                                  []string
	shownModels                                                               []string
	rendered                                                                  map[uintptr]string
	modelError                                                                string
	modelFor                                                                  string
	modelPull                                                                 time.Time
	client                                                                    *http.Client
	stop                                                                      chan struct{}
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
	// Minimum resize is visually bounded; all content stays in scrollable multiline fields.
	if w < 850 {
		w = 850
	}
	if h < 650 {
		h = 650
	}
	margin := 24
	width := w - 2*margin
	pMove.Call(v.header, uintptr(margin), 10, uintptr(width), 46, 1)
	pMove.Call(v.apiLabel, uintptr(margin), 61, uintptr(width), 46, 1)
	// Buttons copied from the first-start screen; no admin login is required.
	topButtons := []struct{ id, x, width int }{{100, 0, 140}, {101, 152, 170}, {102, 334, 135}, {103, 481, 150}, {104, 643, 120}}
	for _, b := range topButtons {
		hnd := v.byButtonID(b.id)
		if hnd != 0 {
			pMove.Call(hnd, uintptr(margin+b.x), 114, uintptr(b.width), 38, 1)
		}
	}
	left := margin
	navW := 300
	detailX := left + navW + 24
	detailW := width - navW - 24
	pMove.Call(v.navTitle, uintptr(left), 161, uintptr(navW), 31, 1)
	for i := range v.buttons {
		y := 200 + i*59
		pMove.Call(v.buttons[i], uintptr(left), uintptr(y), 190, 40, 1)
		pMove.Call(v.labels[i], uintptr(left+198), uintptr(y+1), uintptr(navW-198), 40, 1)
	}
	pMove.Call(v.guideTitle, uintptr(detailX), 160, uintptr(detailW), 36, 1)
	pMove.Call(v.guideStage, uintptr(detailX), 200, uintptr(detailW), 72, 1)
	pMove.Call(v.guideText, uintptr(detailX), 277, uintptr(detailW), 199, 1)
	// Only providers needing a vendor credential display this area.
	pMove.Call(v.officialHelp, uintptr(detailX), 485, uintptr(detailW), 27, 1)
	pMove.Call(v.officialKey, uintptr(detailX), 516, uintptr(max(120, detailW-145)), 39, 1)
	pMove.Call(v.officialSave, uintptr(detailX+detailW-138), 516, 138, 39, 1)
	pMove.Call(v.modelLabel, uintptr(detailX), 485, uintptr(detailW), 27, 1)
	pMove.Call(v.modelChoice, uintptr(detailX), 515, uintptr(detailW), 200, 1)
	pMove.Call(v.retryButton, uintptr(detailX), 558, 150, 38, 1)
	pMove.Call(v.refreshModels, uintptr(detailX+162), 558, 145, 38, 1)
	pMove.Call(v.modelButton, uintptr(detailX+319), 558, 160, 38, 1)
	pMove.Call(v.authButton, uintptr(detailX+491), 558, uintptr(max(125, detailW-491)), 38, 1)
	bottomStart := 620
	pMove.Call(v.logTitle, uintptr(left), uintptr(bottomStart), uintptr(width), 30, 1)
	pMove.Call(v.logBox, uintptr(left), uintptr(bottomStart+34), uintptr(width), uintptr(max(95, h-bottomStart-102)), 1)
	pMove.Call(v.footer, uintptr(left), uintptr(h-46), uintptr(width), 31, 1)
}
func (v *nativeView) byButtonID(id int) uintptr { return nativeButtons[id] }

var nativeButtons = map[int]uintptr{}

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
			v.notify("已复制统一 API 地址，请粘贴到客户端 Base URL。")
		case id == 101:
			copyNative(v.key)
			v.notify("已复制客户端 Key，请仅填给自己的应用。")
		case id == 102:
			go v.executeUpdate()
		case id == 103:
			_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", v.base[:len(v.base)-3]).Start()
		case id == 104:
			go v.refresh()
		case id >= 200 && id < 206:
			v.selectProvider(providers[id-200].ID)
			go v.connectProvider(providers[id-200].ID)
		case id == 107:
			v.mu.Lock()
			provider := v.selected
			v.mu.Unlock()
			if provider != "vertex" && provider != "opencode" {
				v.notify("当前来源不需要粘贴官方 Key")
				break
			}
			key := strings.TrimSpace(getNativeText(v.officialKey))
			go v.saveOfficialKey(provider, key)
		case id == 109:
			v.mu.Lock()
			id := v.selected
			v.mu.Unlock()
			if id != "" {
				go v.connectProvider(id)
			}
		case id == 110:
			go v.forceCheckModels()
		case id == 111:
			v.mu.Lock()
			model := ""
			idx, _, _ := pSend.Call(v.modelChoice, 0x0147, 0, 0) // CB_GETCURSEL
			if int(idx) < len(v.modelIDs) {
				model = v.modelIDs[int(idx)]
			}
			if model == "" && len(v.modelIDs) > 0 {
				model = v.modelIDs[0]
			}
			v.mu.Unlock()
			if model != "" {
				copyNative(model)
				v.notify("已复制模型 ID：" + model)
			} else {
				v.notify("尚未发现该来源模型，先完成授权，再点击『检查模型』")
			}
		case id == 112:
			v.mu.Lock()
			id := v.selected
			v.mu.Unlock()
			if id == "codex" || id == "aistudio" {
				go v.openLogin(id)
			} else if id == "vertex" {
				_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", "https://console.cloud.google.com/expressmode").Start()
			} else {
				v.notify("该来源无软件代办的网页登录：请按照右侧第二步处理。")
			}
		}
		return 0
	case 0x8002:
		if v != nil {
			putText(v.officialKey, "")
		}
		return 0
	case wmUserUpdate:
		if v != nil {
			v.paint()
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
	ret, _, _ := pDefProc.Call(hwnd, uintptr(msg), wp, lp)
	return ret
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
	return v.requestBody(method, path, nil)
}
func (v *nativeView) requestBody(method, path string, input []byte) ([]byte, error) {
	req, e := http.NewRequest(method, "http://127.0.0.1:8765"+path, bytes.NewReader(input))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+v.admin)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
func (v *nativeView) selectProvider(id string) {
	v.mu.Lock()
	v.selected = id
	putText(v.officialKey, "") // Never carry an unsaved vendor credential to another source.
	v.modelFor = ""
	v.modelIDs = nil
	v.modelError = ""
	v.modelPull = time.Time{}
	v.mu.Unlock()
	pPost.Call(v.hwnd, wmUserUpdate, 0, 0)
}
func (v *nativeView) openLogin(id string) {
	v.notify("正在打开 " + id + " 官方授权窗口……你只需要本人确认登录。")
	_, err := v.request("POST", "/api/assist/"+id+"/login")
	if err != nil {
		v.notify("无法打开授权窗口：" + err.Error())
	} else {
		v.notify("授权窗口已请求打开；登录结束请点『检查模型』。")
	}
	go v.refresh()
}
func (v *nativeView) forceCheckModels() {
	v.mu.Lock()
	v.modelPull = time.Time{}
	v.mu.Unlock()
	v.notify("正在读取统一模型列表……")
	v.refresh()
}
func (v *nativeView) requestModels() ([]string, string) {
	req, err := http.NewRequest(http.MethodGet, v.base+"/models", nil)
	if err != nil {
		return nil, err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+v.key)
	rsp, err := v.client.Do(req)
	if err != nil {
		return nil, err.Error()
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("模型接口 HTTP %d", rsp.StatusCode)
	}
	var doc struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Errors map[string]string `json:"backend_errors"`
	}
	err = json.NewDecoder(io.LimitReader(rsp.Body, 2<<20)).Decode(&doc)
	if err != nil {
		return nil, "模型列表格式异常：" + err.Error()
	}
	v.mu.Lock()
	selected := v.selected
	v.mu.Unlock()
	result := make([]string, 0, 12)
	for _, m := range doc.Data {
		if strings.HasPrefix(m.ID, selected+"/") && len(result) < 12 {
			result = append(result, m.ID)
		}
	}
	errorText := ""
	if len(result) == 0 && doc.Errors != nil {
		errorText = doc.Errors[selected]
	}
	return result, errorText
}
func (v *nativeView) setIfChanged(hwnd uintptr, text string) {
	if v.rendered == nil {
		v.rendered = make(map[uintptr]string)
	}
	if v.rendered[hwnd] == text {
		return
	}
	v.rendered[hwnd] = text
	putText(hwnd, text)
}
func (v *nativeView) updateModelList(models []string) {
	if slices.Equal(v.shownModels, models) {
		return
	}
	oldIndex, _, _ := pSend.Call(v.modelChoice, 0x0147, 0, 0)
	oldModel := ""
	if int(oldIndex) >= 0 && int(oldIndex) < len(v.shownModels) {
		oldModel = v.shownModels[int(oldIndex)]
	}
	pSend.Call(v.modelChoice, 0x014B, 0, 0) // CB_RESETCONTENT
	selected := 0
	for i, name := range models {
		pSend.Call(v.modelChoice, 0x0143, 0, uintptr(unsafe.Pointer(wz(name)))) // CB_ADDSTRING
		if name == oldModel {
			selected = i
		}
	}
	if len(models) > 0 {
		pSend.Call(v.modelChoice, 0x014E, uintptr(selected), 0)
	}
	v.shownModels = append([]string(nil), models...)
}
func (v *nativeView) paint() {
	v.mu.Lock()
	selected := v.selected
	state := v.states[selected]
	ids := append([]string(nil), v.modelIDs...)
	modelProblem := v.modelError
	logText := v.logText
	foot := v.footerText
	states := v.states
	v.mu.Unlock()
	for i, p := range providers {
		short := "未接入"
		if states != nil {
			g := guideFor(p.ID, states[p.ID], nil)
			short = g.StageLabel
		}
		if p.ID == selected && len(ids) > 0 && strings.HasPrefix(state, "ready:") {
			short = "可用"
		}
		putText(v.labels[i], short)
		shortNames := map[string]string{"codex": "Codex", "vertex": "Vertex", "opencode": "OpenCode", "workbuddy": "WorkBuddy", "aps": "APS2api", "aistudio": "AI Studio"}
		caption := shortNames[p.ID] + " · 接入"
		if p.ID == selected {
			caption = "▶ " + caption
		}
		putText(v.buttons[i], caption)
	}
	if selected == "" {
		selected = "codex"
	}
	guide := guideFor(selected, state, ids)
	putText(v.guideTitle, "当前来源："+guide.Name)
	stageText := "【" + guide.StageLabel + "】" + guide.Detail
	putText(v.guideStage, stageText)
	v.setIfChanged(v.guideText, guideSteps(guide)+"\r\n\r\n"+"下一步："+guide.Next+"\r\n\r\n"+"常见问题："+guide.Recovery)
	if guide.NeedsKey && guide.Stage != "ready" {
		putText(v.officialHelp, "可选／必要时填写官方 Key（不是 MIXED-API 客户端 Key）")
		pShow.Call(v.officialHelp, swShow)
		pShow.Call(v.officialKey, swShow)
		pShow.Call(v.officialSave, swShow)
		pShow.Call(v.modelLabel, swHide)
		pShow.Call(v.modelChoice, swHide)
	} else {
		pShow.Call(v.officialHelp, swHide)
		pShow.Call(v.officialKey, swHide)
		pShow.Call(v.officialSave, swHide)
		pShow.Call(v.modelLabel, swShow)
		if len(ids) > 0 {
			pShow.Call(v.modelChoice, swShow)
		} else {
			pShow.Call(v.modelChoice, swHide)
		}
	}
	modelLine := "尚无可复制的模型：完成授权后点『检查模型』。"
	if len(ids) > 0 {
		modelLine = "已发现 " + fmt.Sprint(len(ids)) + " 个模型；首个：" + ids[0]
	}
	if modelProblem != "" {
		modelLine += "\r\n模型检查：" + modelProblem
	}
	putText(v.modelLabel, modelLine)
	v.updateModelList(ids)
	switch selected {
	case "codex", "aistudio":
		putText(v.authButton, "打开登录窗口")
		pShow.Call(v.authButton, swShow)
	case "vertex":
		putText(v.authButton, "申请 Express Key")
		pShow.Call(v.authButton, swShow)
	case "opencode":
		putText(v.authButton, "Zen 服务官网")
		pShow.Call(v.authButton, swShow)
	default:
		pShow.Call(v.authButton, swHide)
	}
	if len(logText) > 0 {
		v.setIfChanged(v.logBox, logText)
	}
	if foot != "" {
		putText(v.footer, foot)
	}
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
func getNativeText(h uintptr) string {
	n, _, _ := pGetTextLength.Call(h)
	if n == 0 || n > 8192 {
		return ""
	}
	buf := make([]uint16, n+1)
	pGetText.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}
func (v *nativeView) saveOfficialKey(id, key string) {
	if key == "" {
		v.notify("请先粘贴官方 Express Key / Zen Key。其他来源无需在此填写。")
		return
	}
	body, err := v.request("GET", "/api/config")
	if err != nil {
		v.notify("读取配置失败：" + err.Error())
		return
	}
	var cfg gateway.Config
	if err = json.Unmarshal(body, &cfg); err != nil {
		v.notify("配置文件解析失败")
		return
	}
	found := false
	for i := range cfg.Backends {
		if cfg.Backends[i].ID == id {
			cfg.Backends[i].ProviderKey = key
			found = true
		}
	}
	if !found {
		v.notify("未找到来源 " + id)
		return
	}
	next, err := json.Marshal(cfg)
	if err != nil {
		v.notify("配置保存失败")
		return
	}
	_, err = v.requestBody("PUT", "/api/config", next)
	if err != nil {
		v.notify("配置保存失败：" + err.Error())
		return
	}
	// Clear the credential entry immediately after saving.
	pPost.Call(v.hwnd, 0x8002, 0, 0)
	v.notify("官方凭证已保存。正在重新接入 " + id + "……")
	_, _ = v.request("POST", "/api/process/"+id+"/stop")
	time.Sleep(1300 * time.Millisecond)
	v.connectProvider(id)
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
	if raw, err := v.request("GET", "/api/logs"); err == nil {
		_ = json.Unmarshal(raw, &logs)
	}
	lines := []string{}
	for i := len(logs.Entries) - 1; i >= 0 && len(lines) < 10; i-- {
		z := logs.Entries[i]
		stamp := z.Time
		if len(stamp) > 19 {
			stamp = stamp[11:19]
		}
		line := fmt.Sprintf("%s   HTTP %-3d   %-10s   %s", stamp, z.Status, z.Backend, z.Path)
		if z.Error != "" {
			errText := []rune(z.Error)
			if len(errText) > 130 {
				errText = errText[:130]
			}
			line += "\r\n    原因：" + string(errText)
		}
		lines = append(lines, line)
	}
	states := map[string]string{}
	if raw, err := v.request("GET", "/api/status"); err == nil {
		var result struct {
			Processes map[string]string `json:"processes"`
		}
		if json.Unmarshal(raw, &result) == nil {
			for _, p := range providers {
				value := result.Processes["setup:"+p.ID]
				if value == "" {
					value = result.Processes[p.ID]
				}
				states[p.ID] = value
			}
		}
	}
	v.mu.Lock()
	selected := v.selected
	pull := selected != "" && (v.modelFor != selected || time.Since(v.modelPull) > 20*time.Second)
	if pull {
		v.modelFor = selected
		v.modelPull = time.Now()
	}
	v.mu.Unlock()
	var ids []string
	var modelErr string
	if pull {
		ids, modelErr = v.requestModels()
	}
	v.mu.Lock()
	v.states = states
	if len(lines) == 0 {
		v.logText = "暂无请求。配置来源并在你的 AI 客户端发送消息后，这里会显示请求及错误。"
	} else {
		v.logText = strings.Join(lines, "\r\n")
	}
	if pull && v.selected == selected {
		v.modelIDs = ids
		v.modelError = modelErr
	}
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
	view := &nativeView{key: cfg.APIKey, admin: cfg.AdminKey, base: "http://127.0.0.1:8765/v1", selected: "codex", states: map[string]string{}, stop: make(chan struct{}), client: &http.Client{Timeout: 10 * time.Second}}
	desktop = view
	hinst, _, _ := pGetModule.Call(0)
	cl := wz("MIXEDAPINativeWinGuide")
	wc := nativeClass{Size: uint32(unsafe.Sizeof(nativeClass{})), WndProc: syscall.NewCallback(nativeCallback), Instance: hinst, Background: 6, ClassName: cl}
	if result, _, er := pRegister.Call(uintptr(unsafe.Pointer(&wc))); result == 0 {
		return fmt.Errorf("窗口初始化失败：%w", er)
	}
	hwnd, _, er := pCreate.Call(0, uintptr(unsafe.Pointer(cl)), uintptr(unsafe.Pointer(wz("MIXED-API · 一键接入 AI"))), wsOverlapped|wsVisible, 90, 60, 1170, 900, 0, 0, hinst, 0)
	if hwnd == 0 {
		return fmt.Errorf("创建窗口失败：%w", er)
	}
	view.hwnd = hwnd
	fontName := wz("Microsoft YaHei UI")
	normalSize, titleSize := int32(-19), int32(-29)
	view.font, _, _ = pCreateFont.Call(uintptr(normalSize), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(fontName)))
	view.heading, _, _ = pCreateFont.Call(uintptr(titleSize), 0, 0, 0, 700, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(fontName)))
	view.header = createChild("STATIC", "MIXED-API      一键接入 · 分步引导", 1, hwnd, 0)
	pSend.Call(view.header, wmSetFont, view.heading, 1)
	view.apiLabel = createChild("STATIC", "● 服务已启动     http://127.0.0.1:8765/v1\r\n先选来源 → 按提示完成授权 → 复制 Key 和模型就能用", 2, hwnd, 0)
	for _, b := range []struct {
		id   int
		name string
	}{{100, "复制 API 地址"}, {101, "复制客户端 Key"}, {102, "检查更新"}, {103, "高级设置"}, {104, "刷新状态"}} {
		nativeButtons[b.id] = createChild("BUTTON", b.name, b.id, hwnd, bsPushButton)
	}
	view.navTitle = createChild("STATIC", "第 1 步   选择要接入的来源", 3, hwnd, 0)
	for i, p := range providers {
		view.buttons[i] = createChild("BUTTON", p.Label+" · 一键接入", 200+i, hwnd, bsPushButton)
		view.labels[i] = createChild("STATIC", "未接入", 300+i, hwnd, 0)
	}
	view.guideTitle = createChild("STATIC", "第 2 步   当前来源：Codex", 4, hwnd, 0)
	pSend.Call(view.guideTitle, wmSetFont, view.heading, 1)
	view.guideStage = createChild("STATIC", "【尚未接入】点击左侧一键接入，软件负责准备。", 5, hwnd, 0)
	view.guideText = createChild("EDIT", "", 6, hwnd, wsVScroll|esMultiline|esAutoVScroll|esReadOnly|0x00800000)
	view.officialHelp = createChild("STATIC", "", 20, hwnd, 0)
	view.officialKey = createChild("EDIT", "", 108, hwnd, 0x00000020|0x00800000)
	view.officialSave = createChild("BUTTON", "保存并继续", 107, hwnd, bsPushButton)
	view.modelLabel = createChild("STATIC", "等待检查模型", 21, hwnd, 0)
	view.modelChoice = createChild("COMBOBOX", "", 115, hwnd, 0x0003|wsVScroll) // select discovered model
	view.retryButton = createChild("BUTTON", "重新接入", 109, hwnd, bsPushButton)
	view.refreshModels = createChild("BUTTON", "检查模型", 110, hwnd, bsPushButton)
	view.modelButton = createChild("BUTTON", "复制模型 ID", 111, hwnd, bsPushButton)
	view.authButton = createChild("BUTTON", "打开官方授权", 112, hwnd, bsPushButton)
	view.logTitle = createChild("STATIC", "第 3 步   请求记录 / 排错（自动刷新；不保存聊天内容）", 10, hwnd, 0)
	view.logBox = createChild("EDIT", "暂无请求。完成接入后可在下方查看错误。", 11, hwnd, wsVScroll|esMultiline|esAutoVScroll|esReadOnly|0x00800000)
	view.footer = createChild("STATIC", "先点左侧的一键接入；中间会自动告诉你还需要做什么。", 12, hwnd, 0)
	view.position()
	go func() {
		view.refresh()
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				view.refresh()
			case <-view.stop:
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
