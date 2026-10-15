package gateway

// Permanent, user-initiated update channel. Both installed and portable copies
// point to GitHub's /releases/latest, not to a version-specific release tag.
// The server never runs installers without an explicit authenticated click.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// BuildVersion is set at build time by the release pipeline using -ldflags -X.
// A local developer build is clearly marked as dev rather than pretending to
// be the latest released package.
var BuildVersion = "dev"

const latestReleasePage = "https://github.com/NLKASHEI/MIXED-API/releases/latest"
const latestReleaseAPI = "https://api.github.com/repos/NLKASHEI/MIXED-API/releases/latest"
const setupAssetName = "MIXED-API-Windows-x64-Setup.exe"

var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
var digestPattern = regexp.MustCompile(`^sha256:([a-fA-F0-9]{64})$`)

type releaseAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Digest      string `json:"digest"`
	Size        int64  `json:"size"`
}
type releaseInfo struct {
	Tag        string         `json:"tag_name"`
	URL        string         `json:"html_url"`
	Assets     []releaseAsset `json:"assets"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
}

func versionNumbers(version string) ([3]int, bool) {
	var nums [3]int
	if !versionPattern.MatchString(version) {
		return nums, false
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	for i, p := range parts {
		n, e := strconv.Atoi(p)
		if e != nil {
			return nums, false
		}
		nums[i] = n
	}
	return nums, true
}
func isNewerVersion(current, latest string) bool {
	old, ok1 := versionNumbers(current)
	newV, ok2 := versionNumbers(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if newV[i] != old[i] {
			return newV[i] > old[i]
		}
	}
	return false
}
func validAssetURL(tag, assetName, raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "/NLKASHEI/MIXED-API/releases/download/"+tag+"/"+assetName
}
func releaseSetupAsset(release releaseInfo) (releaseAsset, error) {
	if !versionPattern.MatchString(release.Tag) || release.Draft || release.Prerelease {
		return releaseAsset{}, errors.New("GitHub returned an invalid or prerelease version")
	}
	for _, asset := range release.Assets {
		if asset.Name != setupAssetName {
			continue
		}
		if !validAssetURL(release.Tag, setupAssetName, asset.DownloadURL) {
			return releaseAsset{}, errors.New("installer URL is not the official repository release asset")
		}
		if !digestPattern.MatchString(asset.Digest) {
			return releaseAsset{}, errors.New("installer SHA-256 digest unavailable; open Releases manually instead")
		}
		if asset.Size <= 0 || asset.Size > 125<<20 {
			return releaseAsset{}, errors.New("unexpected installer size")
		}
		return asset, nil
	}
	return releaseAsset{}, errors.New("this release has no Windows installer")
}
func currentRelease(ctx context.Context) (releaseInfo, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseAPI, nil)
	if e != nil {
		return releaseInfo{}, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "MIXED-API-Updater")
	client := &http.Client{Timeout: 12 * time.Second}
	r, e := client.Do(req)
	if e != nil {
		return releaseInfo{}, fmt.Errorf("无法连接 GitHub: %w", e)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return releaseInfo{}, fmt.Errorf("GitHub releases HTTP %d", r.StatusCode)
	}
	var release releaseInfo
	if e = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&release); e != nil {
		return releaseInfo{}, e
	}
	if !versionPattern.MatchString(release.Tag) || release.Draft || release.Prerelease || release.URL != "https://github.com/NLKASHEI/MIXED-API/releases/tag/"+release.Tag {
		return releaseInfo{}, errors.New("release metadata cannot be trusted")
	}
	return release, nil
}
func (s *Server) updateInfo(ctx context.Context) (map[string]any, error) {
	release, err := currentRelease(ctx)
	if err != nil {
		return nil, err
	}
	_, assetError := releaseSetupAsset(release)
	return map[string]any{
		"installed_version":       BuildVersion,
		"latest_version":          release.Tag,
		"update_available":        isNewerVersion(BuildVersion, release.Tag),
		"version_known":           versionPattern.MatchString(BuildVersion),
		"release_url":             latestReleasePage,
		"windows_setup_available": assetError == nil,
		"platform":                runtime.GOOS,
	}, nil
}
func (s *Server) downloadAndOpenUpdate() error {
	if runtime.GOOS != "windows" {
		return errors.New("安卓需要使用系统安装界面更新 APK；请打开 GitHub 最新版本页面")
	}
	s.mu.Lock()
	if strings.HasPrefix(s.status["update"], "downloading") {
		s.mu.Unlock()
		return errors.New("安装包正在下载，请稍候")
	}
	s.status["update"] = "downloading: checking latest release"
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		release, err := currentRelease(ctx)
		if err != nil {
			s.backendState("update", "failed: "+err.Error())
			return
		}
		asset, err := releaseSetupAsset(release)
		if err != nil {
			s.backendState("update", "failed: "+err.Error())
			return
		}
		if versionPattern.MatchString(BuildVersion) && !isNewerVersion(BuildVersion, release.Tag) {
			s.backendState("update", "already_latest: 本机已是最新正式版")
			return
		}
		temp, err := os.MkdirTemp("", "MIXED-API-Update-")
		if err != nil {
			s.backendState("update", "failed: "+err.Error())
			return
		}
		target := filepath.Join(temp, setupAssetName)
		s.backendState("update", "downloading: 正在下载并核验官方安装程序")
		digest := strings.TrimPrefix(asset.Digest, "sha256:")
		if err = downloadInstaller(ctx, asset.DownloadURL, digest, asset.Size, target); err != nil {
			os.RemoveAll(temp)
			s.backendState("update", "failed: "+err.Error())
			return
		}
		installer := exec.Command(target)
		if err = installer.Start(); err != nil {
			s.backendState("update", "failed: 安装包已下载，但无法启动安装器: "+err.Error())
			return
		}
		go func() { _ = installer.Wait() }()
		s.backendState("update", "launched: 已验证并打开安装向导。安装时请关闭旧版 MIXED-API，配置将被保留。")
	}()
	return nil
}
func downloadInstaller(ctx context.Context, raw, digest string, expectedSize int64, target string) error {
	if !digestPattern.MatchString("sha256:" + digest) {
		return errors.New("invalid SHA-256 digest")
	}
	if expectedSize <= 0 || expectedSize > 125<<20 {
		return errors.New("invalid download size")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 4 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_EXCL|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, expectedSize+1))
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != expectedSize {
		return fmt.Errorf("download size changed: expected %d, got %d", expectedSize, n)
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), digest) {
		return errors.New("installer SHA-256 verification failed")
	}
	return nil
}
