//go:build windows

package desktop

import (
	"context"
	"errors"
	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

func defaultBrowserExecutable() (string, error) {
	protocol, _ := windows.UTF16PtrFromString("http")
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := defaultBrowserExecutable(); err != nil {
		return nil, err
	}
	return googleweb.NewExtensionPage(root, id, func(target string) error {
		verb, _ := windows.UTF16PtrFromString("open")
		link, _ := windows.UTF16PtrFromString(target)
		result, _, _ := shell32.NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(link)), 0, 0, 1)
		if result <= 32 {
			return &googleweb.SetupError{Message: "无法打开默认浏览器，请检查 Windows 默认应用设置"}
		}
		return nil
	})
}
