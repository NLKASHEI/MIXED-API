//go:build windows

package desktop

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Edge may replace its launcher with an unelevated process. Cleanup therefore
// checks the exact private --user-data-dir and executable on a held process
// handle, never a process name alone. No daily browser profile is targeted.
func stopOwnedBrowser(executable, profile string) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(executable)) {
			continue
		}
		handle, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, entry.ProcessID)
		if e != nil {
			continue
		}
		if ownsBrowserProcess(handle, executable, profile) {
			_ = windows.TerminateProcess(handle, 0)
			_, _ = windows.WaitForSingleObject(handle, 2000)
		}
		windows.CloseHandle(handle)
	}
}
func ownsBrowserProcess(handle windows.Handle, executable, profile string) bool {
	var path [32768]uint16
	size := uint32(len(path))
	if windows.QueryFullProcessImageName(handle, 0, &path[0], &size) != nil || !strings.EqualFold(windows.UTF16ToString(path[:size]), executable) {
		return false
	}
	var needed uint32
	_ = windows.NtQueryInformationProcess(handle, 60, nil, 0, &needed)
	if needed < uint32(unsafe.Sizeof(windows.NTUnicodeString{})) || needed > 1<<20 {
		return false
	}
	buffer := make([]byte, needed)
	if windows.NtQueryInformationProcess(handle, 60, unsafe.Pointer(&buffer[0]), needed, &needed) != nil {
		return false
	}
	value := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
	base := uintptr(unsafe.Pointer(&buffer[0]))
	start := uintptr(unsafe.Pointer(value.Buffer))
	if value.Length%2 != 0 || start < base || start+uintptr(value.Length) > base+uintptr(len(buffer)) {
		return false
	}
	args, err := windows.DecomposeCommandLine(windows.UTF16ToString(unsafe.Slice(value.Buffer, int(value.Length)/2)))
	if err != nil {
		return false
	}
	match := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "--type=") {
			return false
		}
		if strings.HasPrefix(arg, "--user-data-dir=") {
			match = strings.EqualFold(filepath.Clean(strings.TrimPrefix(arg, "--user-data-dir=")), filepath.Clean(profile))
		}
	}
	return match
}
