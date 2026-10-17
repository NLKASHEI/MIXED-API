//go:build windows && bundled

package desktop

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

//go:embed resources/webview2.cab resources/manifest.json
var rendererBundle embed.FS

type rendererManifest struct {
	Version, Root, SHA256 string
	Files                 map[string]string
}

func rendererPath() (string, error) {
	raw, err := rendererBundle.ReadFile("resources/manifest.json")
	if err != nil {
		return "", err
	}
	var manifest rendererManifest
	if json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), &manifest) != nil || manifest.Version == "" || !filepath.IsLocal(manifest.Root) || len(manifest.Files) == 0 {
		return "", errors.New("渲染资源清单无效，请重新安装完整包")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	cache = filepath.Join(cache, "MIXED-API", "renderer")
	if err = os.MkdirAll(cache, 0700); err != nil {
		return "", err
	}
	mutex, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr("Local\\MIXED-API-Renderer-"+manifest.Version))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(mutex)
	wait, err := windows.WaitForSingleObject(mutex, 120000)
	if err != nil || (wait != windows.WAIT_OBJECT_0 && wait != windows.WAIT_ABANDONED) {
		return "", errors.New("渲染资源正在准备，请稍后重新打开")
	}
	defer windows.ReleaseMutex(mutex)
	target := filepath.Join(cache, manifest.Root)
	if verifyRenderer(target, manifest) == nil {
		return target, nil
	}
	// A previous launch may have repaired a corrupt or still-in-use canonical
	// cache. Reuse only an independently verified repair.
	entries, err := os.ReadDir(cache)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), manifest.Root+"-repaired-") {
			candidate := filepath.Join(cache, entry.Name())
			if verifyRenderer(candidate, manifest) == nil {
				return candidate, nil
			}
		}
	}
	cab, err := rendererBundle.ReadFile("resources/webview2.cab")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(cab)
	if hex.EncodeToString(digest[:]) != manifest.SHA256 {
		return "", errors.New("应用内置渲染资源校验失败")
	}
	stage, err := os.MkdirTemp(cache, ".prepare-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage) // exact directory returned by MkdirTemp under our cache
	cabPath := filepath.Join(stage, "renderer.cab")
	if err = os.WriteFile(cabPath, cab, 0600); err != nil {
		return "", err
	}
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "expand.exe"), "-F:*", cabPath, stage)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = cmd.Run(); err != nil {
		return "", errors.New("无法展开应用内置渲染资源，请检查磁盘空间")
	}
	extracted := filepath.Join(stage, manifest.Root)
	if err = verifyRenderer(extracted, manifest); err != nil {
		return "", err
	}
	// AppContainer needs read/execute permission on renderer files on Windows 10.
	// Credentials and profiles are outside this directory and receive no grants.
	acl := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe"), extracted, "/grant", "*S-1-15-2-1:(OI)(CI)(RX)", "/grant", "*S-1-15-2-2:(OI)(CI)(RX)", "/T", "/Q")
	acl.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = acl.Run(); err != nil {
		return "", errors.New("无法设置应用渲染器读取权限")
	}
	// Never remove a previous directory. A damaged or in-use installation gets
	// a new verified directory; the running app continues using its own files.
	if _, e := os.Stat(target); e == nil {
		target, err = os.MkdirTemp(cache, manifest.Root+"-repaired-")
		if err != nil {
			return "", err
		}
		if err = os.Remove(target); err != nil {
			return "", err
		}
	}
	if err = os.Rename(extracted, target); err != nil {
		return "", errors.New("无法保存应用渲染资源")
	}
	return target, nil
}

func verifyRenderer(root string, m rendererManifest) error {
	for name, want := range m.Files {
		if !filepath.IsLocal(name) || len(want) != 64 {
			return errors.New("渲染资源路径无效")
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("渲染资源缺失或无效")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil || hex.EncodeToString(h.Sum(nil)) != want {
			return errors.New("渲染资源校验失败")
		}
	}
	return nil
}
