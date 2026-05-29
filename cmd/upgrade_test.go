package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type upgradeFixture struct {
	server     *httptest.Server
	assetBytes []byte
	checksums  string
	latestTag  string
	assetName  string
	goos       string
	goarch     string
	hasSig     bool
	sigBytes   []byte
	certBytes  []byte
	requestLog []string
}

func newUpgradeFixture(t *testing.T, latestTag, goos, goarch string) *upgradeFixture {
	t.Helper()
	f := &upgradeFixture{
		latestTag:  latestTag,
		assetBytes: []byte("fake-binary-payload"),
		goos:       goos,
		goarch:     goarch,
	}
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	f.assetName = fmt.Sprintf("kubolt-%s-%s-%s%s", latestTag, goos, goarch, ext)
	f.checksums = fmt.Sprintf("%s  %s\n", sha256Hex(f.assetBytes), f.assetName)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+repoOwner+"/"+repoName+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.requestLog = append(f.requestLog, r.URL.Path)
		fmt.Fprintf(w, `{"tag_name":%q}`, f.latestTag)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		f.requestLog = append(f.requestLog, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+f.assetName):
			w.Write(f.assetBytes)
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			w.Write([]byte(f.checksums))
		case strings.HasSuffix(r.URL.Path, ".sig"):
			if !f.hasSig {
				http.NotFound(w, r)
				return
			}
			w.Write(f.sigBytes)
		case strings.HasSuffix(r.URL.Path, ".pem"):
			if !f.hasSig {
				http.NotFound(w, r)
				return
			}
			w.Write(f.certBytes)
		default:
			http.NotFound(w, r)
		}
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *upgradeFixture) latestURL() string {
	return f.server.URL + "/repos/" + repoOwner + "/" + repoName + "/releases/latest"
}

func (f *upgradeFixture) downloadBase() string {
	return f.server.URL + "/dl"
}

func withStubs(t *testing.T) (restore func(), replaceCalls *[]string) {
	t.Helper()
	prevReplace := replaceBinary
	prevLook := lookPath
	prevStderr := Stderr
	prevRequireSig := upgradeRequireSignature
	prevSkipSum := upgradeSkipChecksum

	calls := []string{}
	replaceBinary = func(src, dst string) error {
		calls = append(calls, src+"->"+dst)
		return nil
	}
	lookPath = func(file string) (string, error) {
		return "", errors.New("not found")
	}
	Stderr = &strings.Builder{}

	return func() {
		replaceBinary = prevReplace
		lookPath = prevLook
		Stderr = prevStderr
		upgradeRequireSignature = prevRequireSig
		upgradeSkipChecksum = prevSkipSum
	}, &calls
}

func TestUpgrade_AlreadyUpToDate(t *testing.T) {
	f := newUpgradeFixture(t, "v1.2.3", "linux", "amd64")
	restore, calls := withStubs(t)
	defer restore()

	err := runUpgradeFromURLs("v1.2.3", f.latestURL(), f.downloadBase(), "linux", "amd64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("expected no replaceBinary calls, got %v", *calls)
	}
	out := Stderr.(*strings.Builder).String()
	if !strings.Contains(out, "Already up to date") {
		t.Fatalf("expected up-to-date message, got: %s", out)
	}
}

func TestUpgrade_DownloadsAndReplaces(t *testing.T) {
	f := newUpgradeFixture(t, "v2.0.0", "linux", "amd64")
	restore, calls := withStubs(t)
	defer restore()

	err := runUpgradeFromURLs("v1.0.0", f.latestURL(), f.downloadBase(), "linux", "amd64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 replaceBinary call, got %v", *calls)
	}
	out := Stderr.(*strings.Builder).String()
	if !strings.Contains(out, "Upgraded kubolt from v1.0.0 to v2.0.0") {
		t.Fatalf("missing upgrade confirmation in: %s", out)
	}
	if !strings.Contains(out, "Checksum OK") {
		t.Fatalf("expected checksum verification, got: %s", out)
	}
}

func TestUpgrade_ChecksumMismatch(t *testing.T) {
	f := newUpgradeFixture(t, "v2.0.0", "linux", "amd64")
	f.checksums = fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), f.assetName)
	restore, calls := withStubs(t)
	defer restore()

	err := runUpgradeFromURLs("v1.0.0", f.latestURL(), f.downloadBase(), "linux", "amd64")
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("replaceBinary must not be called on checksum failure, got %v", *calls)
	}
}

func TestUpgrade_RequireSignature_CosignMissing(t *testing.T) {
	f := newUpgradeFixture(t, "v2.0.0", "linux", "amd64")
	restore, calls := withStubs(t)
	defer restore()

	upgradeRequireSignature = true
	lookPath = func(file string) (string, error) {
		return "", errors.New("not found")
	}

	err := runUpgradeFromURLs("v1.0.0", f.latestURL(), f.downloadBase(), "linux", "amd64")
	if err == nil {
		t.Fatal("expected error when cosign missing and --require-signature set")
	}
	if !strings.Contains(err.Error(), "cosign") {
		t.Fatalf("expected cosign-related error, got: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("replaceBinary must not be called when signature requirement fails, got %v", *calls)
	}
}

func TestAssetName_Windows(t *testing.T) {
	name, err := assetNameFor("v1.2.3", "windows", "amd64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(name, ".exe") {
		t.Fatalf("expected .exe suffix, got %q", name)
	}
	want := "kubolt-v1.2.3-windows-amd64.exe"
	if name != want {
		t.Fatalf("got %q, want %q", name, want)
	}
}

func TestAssetName_UnsupportedOS(t *testing.T) {
	if _, err := assetNameFor("v1.0.0", "plan9", "amd64"); err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

func TestAtomicReplace_Unix(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplace(src, dst); err != nil {
		t.Fatalf("atomicReplace: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "new" {
		t.Fatalf("dst content = %q, want %q", got, "new")
	}
}
