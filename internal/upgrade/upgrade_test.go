package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/zhivko-kocev/friday/releases/latest" {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.6.0",
			"assets": []map[string]string{
				{"name": "friday_linux_amd64.tar.gz", "browser_download_url": "https://example.com/friday_linux_amd64.tar.gz"},
				{"name": "checksums.txt", "browser_download_url": "https://example.com/checksums.txt"},
			},
		})
	}))
	defer srv.Close()

	rel, err := LatestRelease(context.Background(), nil, srv.URL, "zhivko-kocev", "friday")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v0.6.0" {
		t.Errorf("Tag = %q, want v0.6.0", rel.Tag)
	}
	asset, ok := rel.FindAsset("friday_linux_amd64.tar.gz")
	if !ok {
		t.Fatal("FindAsset did not find the linux asset")
	}
	if asset.URL != "https://example.com/friday_linux_amd64.tar.gz" {
		t.Errorf("asset URL = %q", asset.URL)
	}
	if _, ok := rel.FindAsset("checksums.txt"); !ok {
		t.Error("FindAsset did not find checksums.txt")
	}
	if _, ok := rel.FindAsset("does-not-exist"); ok {
		t.Error("FindAsset found a nonexistent asset")
	}
}

func TestLatestReleaseNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := LatestRelease(context.Background(), nil, srv.URL, "zhivko-kocev", "friday"); err == nil {
		t.Fatal("want error for a 404 response")
	}
}

func TestSameVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.5.0", "0.5.0", true},
		{"0.5.0", "v0.5.0", true},
		{"v0.5.0", "v0.5.0", true},
		{"v0.5.0", "v0.6.0", false},
		{"dev", "v0.5.0", false},
	}
	for _, c := range cases {
		if got := SameVersion(c.a, c.b); got != c.want {
			t.Errorf("SameVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
		wantErr            bool
	}{
		{"linux", "amd64", "friday_linux_amd64.tar.gz", false},
		{"linux", "arm64", "friday_linux_arm64.tar.gz", false},
		{"darwin", "amd64", "friday_darwin_amd64.tar.gz", false},
		{"windows", "amd64", "friday_windows_amd64.zip", false},
		{"windows", "arm64", "", true},
	}
	for _, c := range cases {
		got, err := AssetName(c.goos, c.goarch)
		if c.wantErr {
			if err == nil {
				t.Errorf("AssetName(%s, %s) = %q, want error (not built per .goreleaser.yaml)", c.goos, c.goarch, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("AssetName(%s, %s) error = %v", c.goos, c.goarch, err)
		}
		if got != c.want {
			t.Errorf("AssetName(%s, %s) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestBinaryName(t *testing.T) {
	if got := BinaryName("windows"); got != "friday.exe" {
		t.Errorf("BinaryName(windows) = %q, want friday.exe", got)
	}
	if got := BinaryName("linux"); got != "friday" {
		t.Errorf("BinaryName(linux) = %q, want friday", got)
	}
}

func TestDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("archive-bytes"))
	}))
	defer srv.Close()

	got, err := Download(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "archive-bytes" {
		t.Errorf("Download = %q, want archive-bytes", got)
	}
}

func TestDownloadNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := Download(context.Background(), nil, srv.URL); err == nil {
		t.Fatal("want error for a 500 response")
	}
}

func TestVerifyChecksumMatch(t *testing.T) {
	data := []byte("hello world")
	// sha256("hello world")
	const sum = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	checksums := []byte(sum + "  friday_linux_amd64.tar.gz\nabc  other_file.zip\n")
	if err := VerifyChecksum(data, "friday_linux_amd64.tar.gz", checksums); err != nil {
		t.Fatalf("VerifyChecksum: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	data := []byte("tampered content")
	const wrongSum = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	checksums := []byte(wrongSum + "  friday_linux_amd64.tar.gz\n")
	if err := VerifyChecksum(data, "friday_linux_amd64.tar.gz", checksums); err == nil {
		t.Fatal("want error for a checksum mismatch")
	}
}

func TestVerifyChecksumNoEntry(t *testing.T) {
	checksums := []byte("deadbeef  some_other_asset.tar.gz\n")
	if err := VerifyChecksum([]byte("data"), "friday_linux_amd64.tar.gz", checksums); err == nil {
		t.Fatal("want error when the asset has no checksums.txt entry")
	}
}

func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinaryTarGz(t *testing.T) {
	archive := buildTarGz(t, map[string]string{
		"friday":    "binary-content",
		"README.md": "readme",
		"LICENSE":   "license",
	})
	got, err := ExtractBinary(archive, "friday_linux_amd64.tar.gz", "friday")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary-content" {
		t.Errorf("ExtractBinary = %q, want binary-content", got)
	}
}

func TestExtractBinaryZip(t *testing.T) {
	archive := buildZip(t, map[string]string{
		"friday.exe": "windows-binary-content",
		"README.md":  "readme",
	})
	got, err := ExtractBinary(archive, "friday_windows_amd64.zip", "friday.exe")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "windows-binary-content" {
		t.Errorf("ExtractBinary = %q, want windows-binary-content", got)
	}
}

func TestExtractBinaryNotFound(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"README.md": "readme"})
	if _, err := ExtractBinary(archive, "friday_linux_amd64.tar.gz", "friday"); err == nil {
		t.Fatal("want error when the archive has no binary matching binaryName")
	}
}

func TestInstallMethod(t *testing.T) {
	cases := []struct {
		path        string
		wantManager string
	}{
		{"/home/linuxbrew/.linuxbrew/bin/friday", "brew"},
		{"/opt/homebrew/bin/friday", "brew"},
		{"/usr/local/Cellar/friday/0.5.0/bin/friday", "brew"},
		{"/snap/friday/current/bin/friday", "snap"},
		{`C:\Users\me\scoop\shims\friday.exe`, "scoop"},
		{"/usr/local/bin/friday", ""},
		{`C:\Users\me\.friday\bin\friday.exe`, ""},
	}
	for _, c := range cases {
		manager, cmd := InstallMethod(c.path)
		if manager != c.wantManager {
			t.Errorf("InstallMethod(%q) manager = %q, want %q", c.path, manager, c.wantManager)
		}
		if manager != "" && cmd == "" {
			t.Errorf("InstallMethod(%q) returned a manager but no upgrade command", c.path)
		}
	}
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "friday")
	if runtime.GOOS == "windows" {
		exePath += ".exe"
	}
	if err := os.WriteFile(exePath, []byte("old-content"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Replace(exePath, []byte("new-content")); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-content" {
		t.Errorf("content after Replace = %q, want new-content", got)
	}

	if runtime.GOOS == "windows" {
		if _, err := os.Stat(exePath + ".old"); err != nil {
			t.Errorf("want a %s.old left behind on windows, stat error: %v", exePath, err)
		}
		if got, err := os.ReadFile(exePath + ".old"); err != nil || string(got) != "old-content" {
			t.Errorf(".old content = %q, err %v; want old-content", got, err)
		}
	}
}

func TestReplaceMissingTarget(t *testing.T) {
	dir := t.TempDir()
	if err := Replace(filepath.Join(dir, "does-not-exist"), []byte("x")); err == nil {
		t.Fatal("want error replacing a binary that does not exist")
	}
}

func TestCleanupStale(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "friday.exe")
	oldPath := exePath + ".old"
	if err := os.WriteFile(oldPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	CleanupStale(exePath)

	_, err := os.Stat(oldPath)
	if runtime.GOOS == "windows" {
		if !os.IsNotExist(err) {
			t.Errorf("want %s removed on windows, stat error = %v", oldPath, err)
		}
	} else if err != nil {
		t.Errorf("non-windows CleanupStale must be a no-op, but %s: %v", oldPath, err)
	}
}

// TestValidateReleaseURL is the security gate for the whole download path:
// an asset URL arrives verbatim from release metadata, and so does the
// checksum used to verify it, so anything but https-to-an-official-host
// hands one party control of both halves of the comparison.
func TestValidateReleaseURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		apiBase string
		wantErr bool
	}{
		{"official github asset", "https://github.com/zhivko-kocev/friday/releases/download/v1/friday_linux_amd64.tar.gz", "", false},
		{"official cdn asset", "https://objects.githubusercontent.com/blob/1234", "", false},
		{"release-assets cdn", "https://release-assets.githubusercontent.com/blob/1234", "", false},
		{"api host", "https://api.github.com/repos/o/r/releases/latest", "", false},
		{"cleartext official host", "http://github.com/zhivko-kocev/friday/releases/download/v1/x.tar.gz", "", true},
		{"https but foreign host", "https://evil.example.com/friday_linux_amd64.tar.gz", "", true},
		{"host suffix trick", "https://github.com.evil.example.com/x.tar.gz", "", true},
		{"file scheme", "file:///etc/passwd", "", true},
		{"unparseable", "https://exa mple.com/\x7f", "", true},
		{"fixture origin matches", "http://127.0.0.1:8080/asset", "http://127.0.0.1:8080", false},
		{"fixture origin differs", "http://127.0.0.1:9999/asset", "http://127.0.0.1:8080", true},
		{"fixture scheme differs", "https://127.0.0.1:8080/asset", "http://127.0.0.1:8080", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateReleaseURL(c.url, c.apiBase)
			if c.wantErr && err == nil {
				t.Fatalf("ValidateReleaseURL(%q, %q) = nil, want a refusal", c.url, c.apiBase)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("ValidateReleaseURL(%q, %q) = %v, want nil", c.url, c.apiBase, err)
			}
		})
	}
}

// TestNewClientRejectsRedirectOffAllowedOrigin proves the pin survives a
// redirect: validating only the URL taken from the release JSON would still
// let a 302 walk the download to any host, and net/http's default policy
// follows up to ten hops with no scheme or host check at all.
func TestNewClientRejectsRedirectOffAllowedOrigin(t *testing.T) {
	var assetHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	mux.HandleFunc("/elsewhere", func(w http.ResponseWriter, r *http.Request) {
		assetHits++
		_, _ = w.Write([]byte("payload the client must never accept"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// apiBase empty = production rules: the hop target is http://127.0.0.1,
	// which is neither https nor an official host.
	if _, err := Download(context.Background(), NewClient(""), srv.URL+"/redirect"); err == nil {
		t.Fatal("Download followed a redirect off the allowed origin")
	}
	if assetHits != 0 {
		t.Errorf("the redirect target was fetched %d time(s); it must never be read", assetHits)
	}

	// Positive control: pinned to this fixture's own origin, the same
	// redirect is allowed, so the refusal above is the policy and not a
	// blanket "no redirects".
	got, err := Download(context.Background(), NewClient(srv.URL), srv.URL+"/redirect")
	if err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	if !strings.Contains(string(got), "payload") {
		t.Errorf("same-origin redirect returned %q", got)
	}
}

func TestDownloadRejectsOversizedBody(t *testing.T) {
	original := maxAssetBytes
	maxAssetBytes = 16
	t.Cleanup(func() { maxAssetBytes = original })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 64))
	}))
	defer srv.Close()

	_, err := Download(context.Background(), nil, srv.URL)
	if err == nil {
		t.Fatal("Download accepted a body past the asset size limit")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %q, want it to name the size limit", err)
	}
}

func TestNewClientHasATimeout(t *testing.T) {
	if NewClient("").Timeout == 0 {
		t.Error("NewClient returned a client with no Timeout: a wedged host would hang friday upgrade forever")
	}
	if defaultClient.Timeout == 0 {
		t.Error("the nil-client fallback has no Timeout")
	}
}

func TestReleaseByTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/zhivko-kocev/friday/releases/tags/v0.4.0" {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.4.0"})
	}))
	defer srv.Close()

	rel, err := ReleaseByTag(context.Background(), nil, srv.URL, "zhivko-kocev", "friday", "v0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v0.4.0" {
		t.Errorf("Tag = %q, want v0.4.0", rel.Tag)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b    string
		wantCmp int
		wantOK  bool
	}{
		{"v0.6.0", "0.5.0", 1, true},
		{"v0.5.0", "v0.6.0", -1, true},
		{"0.5.0", "v0.5.0", 0, true},
		{"v0.5.1", "v0.5", 1, true},
		{"v0.5", "v0.5.0", 0, true},
		{"v1.0.0-rc1", "v1.0.0", -1, true},
		{"v1.0.0", "v1.0.0-rc1", 1, true},
		{"v0.6.0", "dev", 0, false},
		{"dev", "dev", 0, false},
		{"v0.6.0", "", 0, false},
	}
	for _, c := range cases {
		gotCmp, gotOK := CompareVersions(c.a, c.b)
		if gotOK != c.wantOK {
			t.Errorf("CompareVersions(%q, %q) ok = %v, want %v", c.a, c.b, gotOK, c.wantOK)
			continue
		}
		if gotOK && gotCmp != c.wantCmp {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, gotCmp, c.wantCmp)
		}
	}
}

// TestReplaceRestoresOriginalWhenWriteFails is the Windows-path recovery
// proof. It drives replaceRenamingAside directly with a failing write so it
// runs on every OS: without the rename-back, a write that fails after the
// binary was moved aside leaves nothing at all at exePath, i.e. no working
// friday on PATH, with the only copy in a .old file the error never named.
func TestReplaceRestoresOriginalWhenWriteFails(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "friday.exe")
	const originalContent = "original-running-binary"
	if err := os.WriteFile(exePath, []byte(originalContent), 0o755); err != nil {
		t.Fatal(err)
	}

	writeErr := errors.New("antivirus quarantined the new binary")
	err := replaceRenamingAside(exePath, func() error { return writeErr })
	if err == nil {
		t.Fatal("replaceRenamingAside = nil, want the write error")
	}
	if !errors.Is(err, writeErr) {
		t.Errorf("error %v does not wrap the write failure", err)
	}
	if !strings.Contains(err.Error(), exePath) {
		t.Errorf("error %q must name %s so the user knows what was recovered", err, exePath)
	}

	got, readErr := os.ReadFile(exePath)
	if readErr != nil {
		t.Fatalf("nothing at %s after a failed replace: %v — the original was abandoned", exePath, readErr)
	}
	if string(got) != originalContent {
		t.Errorf("content at %s = %q, want the original %q restored", exePath, got, originalContent)
	}
	if _, err := os.Stat(exePath + ".old"); !os.IsNotExist(err) {
		t.Errorf("%s.old still exists after a successful rollback (stat err = %v)", exePath, err)
	}
}

func TestReplaceRenamingAsideSucceeds(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "friday.exe")
	if err := os.WriteFile(exePath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	written := false
	if err := replaceRenamingAside(exePath, func() error {
		written = true
		return os.WriteFile(exePath, []byte("new"), 0o755)
	}); err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("the write was never attempted")
	}
	if got, _ := os.ReadFile(exePath); string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}
	// The rename-aside copy is what CleanupStale tidies on the next run.
	if got, err := os.ReadFile(exePath + ".old"); err != nil || string(got) != "old" {
		t.Errorf(".old = %q, err %v; want the previous binary kept aside", got, err)
	}
}

func TestEnsureInstallableAcceptsAWritableDir(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "friday")
	if err := os.WriteFile(exePath, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := EnsureInstallable(exePath)
	if err != nil {
		t.Fatalf("EnsureInstallable: %v", err)
	}
	same, err := sameFile(resolved, exePath)
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Errorf("EnsureInstallable resolved to %q, which is not %q", resolved, exePath)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the writability probe left %d entries behind, want only the binary", len(entries))
	}
}

// TestEnsureInstallableFollowsASymlink guards the other half: replacing the
// link instead of its target turns a
// /usr/local/bin/friday -> /opt/friday/bin/friday layout into a regular
// file and orphans the real install.
func TestEnsureInstallableFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real", "friday")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "friday")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err) // unprivileged Windows
	}

	resolved, err := EnsureInstallable(link)
	if err != nil {
		t.Fatalf("EnsureInstallable: %v", err)
	}
	same, err := sameFile(resolved, real)
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Errorf("EnsureInstallable(%q) = %q, want the symlink target %q", link, resolved, real)
	}
}

func TestEnsureInstallableRefusesAMissingDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "friday")
	if _, err := EnsureInstallable(missing); err == nil {
		t.Fatal("EnsureInstallable accepted a path that does not exist")
	}
}

func TestEnsureInstallableRefusesAReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory mode of 0o500 does not deny creation on windows")
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "friday")
	if err := os.WriteFile(exePath, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // let t.TempDir clean up

	_, err := EnsureInstallable(exePath)
	if err == nil {
		t.Fatal("EnsureInstallable accepted a directory it cannot write to")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q must name the directory it cannot install into", err)
	}
}

func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}
