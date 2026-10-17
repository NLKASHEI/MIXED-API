//go:build windows

package desktop

import (
	"errors"
	"golang.org/x/sys/windows"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// Narrow WebView2 ABI binding. Only stable base interfaces are used; slots
// follow Microsoft's WebView2.idl. Generated callbacks using Go strings are
// deliberately excluded because they are not valid Windows ABI arguments.
type comObject struct{ table *[64]uintptr }

func (c *comObject) call(slot int, args ...uintptr) error {
	call := append([]uintptr{uintptr(unsafe.Pointer(c))}, args...)
	hr, _, _ := syscall.SyscallN(c.table[slot], call...)
	if int32(hr) < 0 {
		return syscall.Errno(hr)
	}
	return nil
}
func (c *comObject) release() {
	if c != nil {
		_ = c.call(2)
	}
}
func (c *comObject) object(slot int) (*comObject, error) {
	var result *comObject
	err := c.call(slot, uintptr(unsafe.Pointer(&result)))
	if err == nil && result == nil {
		err = errors.New("empty COM object")
	}
	return result, err
}
func (c *comObject) uri() (string, error) {
	var ptr *uint16
	if err := c.call(3, uintptr(unsafe.Pointer(&ptr))); err != nil {
		return "", err
	}
	if ptr == nil {
		return "", errors.New("empty navigation URI")
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(ptr))
	return windows.UTF16PtrToString(ptr), nil
}
func (c *comObject) boolean(slot int) bool {
	var v int32
	return c.call(slot, uintptr(unsafe.Pointer(&v))) == nil && v != 0
}
func (c *comObject) navigate(target string) error {
	p, e := windows.UTF16PtrFromString(target)
	if e != nil {
		return e
	}
	return c.call(5, uintptr(unsafe.Pointer(p)))
}

type comHandler struct {
	table    *[4]uintptr
	callback func(uintptr, *comObject) uintptr
	iid      windows.GUID
	refs     atomic.Uint32
}

func newHandler(id string, callback func(uintptr, *comObject) uintptr) *comHandler {
	h := &comHandler{callback: callback}
	h.iid, _ = windows.GUIDFromString(id)
	h.refs.Store(1)
	h.table = handlerTable()
	return h
}

var sharedHandlerTable *[4]uintptr
var handlerTableOnce sync.Once

func handlerTable() *[4]uintptr {
	handlerTableOnce.Do(func() {
		unknown, _ := windows.GUIDFromString("{00000000-0000-0000-C000-000000000046}")
		sharedHandlerTable = &[4]uintptr{
			windows.NewCallback(func(this *comHandler, iid *windows.GUID, out *unsafe.Pointer) uintptr {
				if out == nil || iid == nil {
					return 0x80004003
				}
				*out = nil
				if *iid != unknown && *iid != this.iid {
					return 0x80004002
				}
				*out = unsafe.Pointer(this)
				this.refs.Add(1)
				return 0
			}),
			windows.NewCallback(func(this *comHandler) uintptr { return uintptr(this.refs.Add(1)) }),
			windows.NewCallback(func(this *comHandler) uintptr { return uintptr(this.refs.Add(^uint32(0))) }),
			windows.NewCallback(func(h *comHandler, a uintptr, b *comObject) uintptr { return h.callback(a, b) }),
		}
	})
	return sharedHandlerTable
}

func (v *comObject) on(slot int, h *comHandler) error {
	var token int64
	return v.call(slot, uintptr(unsafe.Pointer(h)), uintptr(unsafe.Pointer(&token)))
}
