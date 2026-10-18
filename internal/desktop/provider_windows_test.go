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

func TestCancelledStartupDoesNotQuitNextWindowOnSameThread(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	name, _ := windows.UTF16PtrFromString(providerWindowClass("cancel-test"))
	wc := windowClass{Proc: windowProcedure, Instance: instance, Name: name}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if atom, _, err := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		t.Fatal(err)
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	for range 2 {
		hwnd, _, err := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 100, 100, 0, 0, instance, 0)
		if hwnd == 0 {
			t.Fatal(err)
		}
		h := &host{window: hwnd}
		windowHosts.Store(hwnd, h)
		user32.NewProc("DestroyWindow").Call(hwnd)
		if _, ok := windowHosts.Load(hwnd); ok {
			t.Fatal("destroyed HWND retained its host")
		}
		var msg message
		if found, _, _ := user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0x12, 0x12, 1); found != 0 {
			t.Fatal("startup cancellation left WM_QUIT for the next window")
		}
	}
}
