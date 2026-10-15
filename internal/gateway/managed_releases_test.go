package gateway

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleZip(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func TestManagedArchiveExtractionRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"../outside", "folder/../../outside", "/absolute/file", "C:/Windows/system32/test"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			zipfile := filepath.Join(root, "test.zip")
			if err := os.WriteFile(zipfile, sampleZip(t, name), 0600); err != nil {
				t.Fatal(err)
			}
			if err := safeZipExtract(zipfile, filepath.Join(root, "files")); err == nil {
				t.Fatalf("accepted unsafe name %q", name)
			}
		})
	}
}
func TestManagedPinnedDownloadAndVerification(t *testing.T) {
	data := sampleZip(t, "release/opencode2api.exe")
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "package.zip")
	if err := retrievePinnedZip(context.Background(), srv.Client(), srv.URL, digest, path); err != nil {
		t.Fatal(err)
	}
	if err := safeZipExtract(path, filepath.Join(dir, "files")); err != nil {
		t.Fatal(err)
	}
	if _, err := findManagedExe(filepath.Join(dir, "files"), "opencode2api.exe"); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(dir, "tampered.zip")
	if err := retrievePinnedZip(context.Background(), srv.Client(), srv.URL, strings.Repeat("0", 64), badPath); err == nil {
		t.Fatal("accepted mismatched checksum")
	}
}
func TestGeneratedManagedKeysAndConfig(t *testing.T) {
	root := t.TempDir()
	c := Defaults()
	if err := writeOpenCodeConfig(root, "sk-managed", c); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"anonymous": true`)) || !bytes.Contains(b, []byte("sk-managed")) {
		t.Fatalf("bad config %s", b)
	}
	for i := range c.Backends {
		if c.Backends[i].ID == "opencode" {
			c.Backends[i].ProviderKey = "zen-secret"
		}
	}
	if err := syncOpenCodeCredentials(root, c); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"anonymous": false`)) || !bytes.Contains(b, []byte("zen-secret")) {
		t.Fatalf("upstream key was not applied %s", b)
	}
	aps := t.TempDir()
	if err := writeAPSConfig(aps, "sk-apskey"); err != nil {
		t.Fatal(err)
	}
	k, err := os.ReadFile(filepath.Join(aps, "config", "api_keys.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(k) != "mixed:sk-apskey:MIXED-API\n" {
		t.Fatalf("bad APS keys %s", k)
	}
}
