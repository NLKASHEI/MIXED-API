//go:build windows

package desktop

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The old STA may still own its class when cancellation requires a fresh page.
// Exercise Win32 registration without loading a renderer or a provider account.
func TestProviderWindowCanReopenBeforeOldWindowExits(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	proc := windows.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		r, _, _ := user32.NewProc("DefWindowProcW").Call(hwnd, uintptr(msg), wp, lp)
		return r
	})
	for range 2 {
		name, _ := windows.UTF16PtrFromString(providerWindowClass("aps"))
		wc := windowClass{Proc: proc, Instance: instance, Name: name}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if atom, _, err := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
			t.Fatalf("new source window collided with the closing window: %v", err)
		}
		defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	}
}
