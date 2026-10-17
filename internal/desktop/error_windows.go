//go:build windows

package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ShowError does not depend on the embedded renderer or start a console.
// Messages supplied by the launcher are fixed strings, never config contents.
func ShowError(message string) {
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	title := windows.StringToUTF16Ptr("MIXED-API · 无法启动")
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10|0x10000)
}
