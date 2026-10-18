//go:build windows

package gateway

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

func protectSecret(raw []byte) ([]byte, error)   { return cryptSecret(raw, true) }
func unprotectSecret(raw []byte) ([]byte, error) { return cryptSecret(raw, false) }
func cryptSecret(raw []byte, protect bool) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty credential")
	}
	in := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
	var out windows.DataBlob
	var err error
	if protect {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, errors.New("Windows 账户凭证保护失败")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
