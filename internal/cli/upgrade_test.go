package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zhivko-kocev/friday/internal/upgrade"
)

// fixtureRelease serves a GitHub-releases-API-shaped release plus the two
// asset URLs it advertises, all from in-memory bytes — the "local fixture
// server" the plan's own Phase 12 proof describes. No test in this file
// touches the network: doUpgrade's apiBase seam points every request here,
// and the hit counters make "this step was never reached" an assertion
// rather than an inference.
type fixtureRelease struct {
	srv *httptest.Server

	// Advertised asset URLs. Empty means "this fixture's own /asset and
	// /checksums.txt"; a test sets one to a foreign origin to exercise the
	// download pin.
	assetURL, checksumsURL string

	latestHits, tagHits, assetHits, checksumsHits int
}

func newFixtureRelease(t *testing.T, tag, assetName string, archive, checksums []byte) *fixtureRelease {
	t.Helper()
	f := &fixtureRelease{}
	release := func(w http.ResponseWriter, _ *http.Request) {
		assetURL, checksumsURL := f.assetURL, f.checksumsURL
		if assetURL == "" {
			assetURL = f.srv.URL + "/asset"
		}
		if checksumsURL == "" {
			checksumsURL = f.srv.URL + "/checksums.txt"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"assets": []map[string]string{
				{"name": assetName, "browser_download_url": assetURL},
				{"name": "checksums.txt", "browser_download_url": checksumsURL},
			},
		})
	}
	base := "/repos/" + upgradeRepoOwner + "/" + upgradeRepoName + "/releases/"
	mux := http.NewServeMux()
	mux.HandleFunc(base+"latest", func(w http.ResponseWriter, r *http.Request) {
		f.latestHits++
		release(w, r)
	})
	mux.HandleFunc(base+"tags/"+tag, func(w http.ResponseWriter, r *http.Request) {
		f.tagHits++
		release(w, r)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		f.assetHits++
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		f.checksumsHits++
		_, _ = w.Write(checksums)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// platformAssetName is this test process's own upgrade.AssetName — used to
// make a fixture release "match" so a test can reach past asset lookup
// into the steps it actually wants to exercise (download, checksum, ...).
func platformAssetName(t *testing.T) string {
	t.Helper()
	name, err := upgrade.AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("no release asset is published for %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	return name
}

// installedBinary writes a stand-in for the running friday binary and
// returns its path, so a test can assert on what upgrade.Replace did — or
// prove it never ran.
func installedBinary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), upgrade.BinaryName(runtime.GOOS))
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// releaseArchive builds a real goreleaser-shaped archive for this platform
// (zip on windows, tar.gz elsewhere) plus the matching checksums.txt body.
func releaseArchive(t *testing.T, assetName, binaryContent string) (archive, checksums []byte) {
	t.Helper()
	binaryName := upgrade.BinaryName(runtime.GOOS)
	var buf bytes.Buffer
	if strings.HasSuffix(assetName, ".zip") {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create(binaryName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(binaryContent)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		hdr := &tar.Header{Name: binaryName, Mode: 0o755, Size: int64(len(binaryContent))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(binaryContent)); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	archive = buf.Bytes()
	sum := sha256.Sum256(archive)
	return archive, []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")
}

// TestDoUpgradeInstallsAVerifiedRelease is the happy path end to end
// against a local fixture: metadata, pinned asset URLs, download, checksum,
// extract, and an atomic replace of the binary on disk.
func TestDoUpgradeInstallsAVerifiedRelease(t *testing.T) {
	assetName := platformAssetName(t)
	archive, checksums := releaseArchive(t, assetName, "brand-new-binary")
	f := newFixtureRelease(t, "v99.0.0", assetName, archive, checksums)
	exePath := installedBinary(t, "old-binary")

	err := doUpgrade(context.Background(), upgradeOpts{
		apiBase: f.srv.URL, exePath: exePath, currentVersion: "v0.5.0",
	})
	if err != nil {
		t.Fatalf("doUpgrade = %v, want nil", err)
	}
	got, readErr := os.ReadFile(exePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "brand-new-binary" {
		t.Errorf("installed content = %q, want brand-new-binary", got)
	}
	if info, err := os.Stat(exePath); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want the previous binary's 0755 preserved", info.Mode().Perm())
	}
}

// TestDoUpgradeAlreadyCurrent guards the plan's own Phase 12 proof: "friday
// upgrade on a current build prints 'already at <version>' and exits 0."
func TestDoUpgradeAlreadyCurrent(t *testing.T) {
	f := newFixtureRelease(t, "v0.5.0", "irrelevant.bin", nil, nil)
	err := doUpgrade(context.Background(), upgradeOpts{
		apiBase: f.srv.URL, exePath: "/usr/local/bin/friday", currentVersion: "v0.5.0",
	})
	if err != nil {
		t.Fatalf("doUpgrade = %v, want nil (already current)", err)
	}
	if f.assetHits != 0 {
		t.Errorf("an already-current version downloaded the asset %d time(s)", f.assetHits)
	}
}

// TestDoUpgradeRefusesSilentDowngrade covers a command named `upgrade`
// replacing a newer local build with whatever `latest` happens to be: the
// only signal used to be the tag in the "downloading"/"upgraded to" lines.
func TestDoUpgradeRefusesSilentDowngrade(t *testing.T) {
	assetName := platformAssetName(t)
	archive, checksums := releaseArchive(t, assetName, "older-release")
	f := newFixtureRelease(t, "v0.4.0", assetName, archive, checksums)
	exePath := installedBinary(t, "local-build")

	o := upgradeOpts{apiBase: f.srv.URL, exePath: exePath, currentVersion: "v0.9.0"}
	if err := doUpgrade(context.Background(), o); err != nil {
		t.Fatalf("doUpgrade = %v, want nil (refusal, not an error)", err)
	}
	if f.assetHits != 0 {
		t.Fatalf("a downgrade downloaded the asset %d time(s)", f.assetHits)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "local-build" {
		t.Fatalf("binary changed to %q on a refused downgrade", got)
	}

	// --force is the documented escape hatch, so the same call must install.
	o.force = true
	if err := doUpgrade(context.Background(), o); err != nil {
		t.Fatalf("doUpgrade --force = %v, want nil", err)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "older-release" {
		t.Errorf("--force did not install the older release; content = %q", got)
	}
}

// TestDoUpgradeVersionPinFetchesThatTag proves --version is a real pin (the
// only way to roll back to a known-good release after a bad one) and that a
// deliberate pin is not treated as an accidental downgrade.
func TestDoUpgradeVersionPinFetchesThatTag(t *testing.T) {
	assetName := platformAssetName(t)
	archive, checksums := releaseArchive(t, assetName, "pinned-release")
	f := newFixtureRelease(t, "v0.4.0", assetName, archive, checksums)
	exePath := installedBinary(t, "local-build")

	err := doUpgrade(context.Background(), upgradeOpts{
		apiBase: f.srv.URL, exePath: exePath, currentVersion: "v0.9.0", version: "v0.4.0",
	})
	if err != nil {
		t.Fatalf("doUpgrade --version = %v, want nil", err)
	}
	if f.tagHits != 1 {
		t.Errorf("release-by-tag endpoint hit %d time(s), want 1", f.tagHits)
	}
	if f.latestHits != 0 {
		t.Errorf("a pinned upgrade also queried latest %d time(s)", f.latestHits)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "pinned-release" {
		t.Errorf("installed content = %q, want pinned-release", got)
	}
}

// TestDoUpgradeCheckNeverDownloadsOrWrites: --check must be a dry run, so
// asking "what would I get?" never replaces the binary you are running.
func TestDoUpgradeCheckNeverDownloadsOrWrites(t *testing.T) {
	assetName := platformAssetName(t)
	archive, checksums := releaseArchive(t, assetName, "would-be-installed")
	f := newFixtureRelease(t, "v99.0.0", assetName, archive, checksums)
	exePath := installedBinary(t, "old-binary")

	err := doUpgrade(context.Background(), upgradeOpts{
		apiBase: f.srv.URL, exePath: exePath, currentVersion: "v0.5.0", check: true,
	})
	if err != nil {
		t.Fatalf("doUpgrade --check = %v, want nil", err)
	}
	if f.assetHits != 0 || f.checksumsHits != 0 {
		t.Errorf("--check downloaded (asset %d, checksums %d), want neither", f.assetHits, f.checksumsHits)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "old-binary" {
		t.Errorf("--check replaced the binary with %q", got)
	}
}

// TestDoUpgradeRefusesForeignAssetURLBeforeDownloading is the security
// regression: asset URLs and the checksum URL both come verbatim from
// release metadata, so if either can point off the release's own origin,
// whoever controls it supplies BOTH halves of the checksum comparison and
// the gate passes on tampered bytes. The refusal must happen before any
// bytes are fetched.
func TestDoUpgradeRefusesForeignAssetURLBeforeDownloading(t *testing.T) {
	assetName := platformAssetName(t)

	cases := []struct {
		name  string
		apply func(f *fixtureRelease, foreign string)
	}{
		{"archive url", func(f *fixtureRelease, foreign string) { f.assetURL = foreign }},
		{"checksums url", func(f *fixtureRelease, foreign string) { f.checksumsURL = foreign }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			archive, checksums := releaseArchive(t, assetName, "tampered")
			f := newFixtureRelease(t, "v99.0.0", assetName, archive, checksums)
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("the foreign host was contacted; the URL must be refused before any request")
			}))
			defer foreign.Close()
			c.apply(f, foreign.URL+"/asset")
			exePath := installedBinary(t, "original")

			err := doUpgrade(context.Background(), upgradeOpts{
				apiBase: f.srv.URL, exePath: exePath, currentVersion: "v0.5.0",
			})
			if err == nil {
				t.Fatal("doUpgrade = nil, want a refusal for an off-origin release URL")
			}
			if !strings.Contains(err.Error(), "refusing to download") {
				t.Errorf("error = %q, want it to name the refusal", err)
			}
			if f.assetHits != 0 || f.checksumsHits != 0 {
				t.Errorf("bytes were fetched before the refusal (asset %d, checksums %d)", f.assetHits, f.checksumsHits)
			}
			if got, _ := os.ReadFile(exePath); string(got) != "original" {
				t.Errorf("binary changed to %q", got)
			}
		})
	}
}

// TestDoUpgradePreflightsTheInstallDirBeforeDownloading: an install
// location friday cannot write must be a clear refusal up front, not a raw
// permission error from a temp-file create after a full download.
func TestDoUpgradePreflightsTheInstallDirBeforeDownloading(t *testing.T) {
	assetName := platformAssetName(t)
	archive, checksums := releaseArchive(t, assetName, "new-binary")
	f := newFixtureRelease(t, "v99.0.0", assetName, archive, checksums)
	missing := filepath.Join(t.TempDir(), "not-a-dir", upgrade.BinaryName(runtime.GOOS))

	err := doUpgrade(context.Background(), upgradeOpts{
		apiBase: f.srv.URL, exePath: missing, currentVersion: "v0.5.0",
	})
	if err == nil {
		t.Fatal("doUpgrade = nil, want a refusal for an unusable install path")
	}
	if f.assetHits != 0 {
		t.Errorf("the archive was downloaded %d time(s) before the install path was checked", f.assetHits)
	}
}

// TestDoUpgradeInstallMethodSkipsNetwork guards the plan's own proof: "With
// the binary placed under a path containing /homebrew/, it prints the brew
// command and exits without downloading." apiBase points at an address
// nothing is listening on — if InstallMethod's early return did not fire
// before the network call, LatestRelease would fail to connect and this
// test would see that error instead of a clean nil.
func TestDoUpgradeInstallMethodSkipsNetwork(t *testing.T) {
	const unreachable = "http://127.0.0.1:1" // reserved/unassigned port: connection refused, fast
	err := doUpgrade(context.Background(), upgradeOpts{
		apiBase: unreachable, exePath: "/opt/homebrew/bin/friday", currentVersion: "v0.5.0",
	})
	if err != nil {
		t.Fatalf("doUpgrade = %v, want nil: the homebrew path must return before ever reaching apiBase", err)
	}
}

// TestDoUpgradeInstallMethodForceOverrides: InstallMethod matches on a path
// substring, so "/snap/" or "/cellar/" also match a user directory such as
// ~/snap/bin/friday. Such a user must not be permanently stuck being told
// to run a package-manager command that will not work.
func TestDoUpgradeInstallMethodForceOverrides(t *testing.T) {
	assetName := platformAssetName(t)
	archive, checksums := releaseArchive(t, assetName, "installed-anyway")
	f := newFixtureRelease(t, "v99.0.0", assetName, archive, checksums)

	// A real directory that merely looks like snap's.
	dir := filepath.Join(t.TempDir(), "snap", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(dir, upgrade.BinaryName(runtime.GOOS))
	if err := os.WriteFile(exePath, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if manager, _ := upgrade.InstallMethod(exePath); manager == "" {
		t.Fatalf("fixture path %s no longer trips InstallMethod; the test is meaningless", exePath)
	}

	o := upgradeOpts{apiBase: f.srv.URL, exePath: exePath, currentVersion: "v0.5.0"}
	if err := doUpgrade(context.Background(), o); err != nil {
		t.Fatalf("doUpgrade = %v, want nil (defer to the manager)", err)
	}
	if f.latestHits != 0 {
		t.Fatalf("a package-manager-owned binary queried the release API %d time(s)", f.latestHits)
	}

	o.force = true
	if err := doUpgrade(context.Background(), o); err != nil {
		t.Fatalf("doUpgrade --force = %v, want nil", err)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "installed-anyway" {
		t.Errorf("--force did not install over the manager path; content = %q", got)
	}
}

// TestDoUpgradeChecksumMismatchAbortsBeforeWrite guards the plan's own
// proof: point the release-API base at a local fixture server whose
// checksums.txt does not match the asset and confirm it aborts before
// writing anything. The exePath file's content staying byte-identical
// after the failed call is the black-box proof upgrade.Replace was never
// reached — not just that some error happened to be returned.
func TestDoUpgradeChecksumMismatchAbortsBeforeWrite(t *testing.T) {
	const originalContent = "original-binary-bytes"
	exePath := installedBinary(t, originalContent)

	assetName := platformAssetName(t)
	archive := []byte("new-archive-bytes-that-will-never-be-installed")
	wrongChecksums := []byte("0000000000000000000000000000000000000000000000000000000000000000  " + assetName + "\n")
	f := newFixtureRelease(t, "v99.0.0", assetName, archive, wrongChecksums)

	err := doUpgrade(context.Background(), upgradeOpts{
		apiBase: f.srv.URL, exePath: exePath, currentVersion: "v0.5.0",
	})
	if err == nil {
		t.Fatal("doUpgrade = nil, want a checksum error")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error = %q, want it to name the checksum failure", err.Error())
	}

	got, readErr := os.ReadFile(exePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != originalContent {
		t.Fatalf("exePath content changed to %q after a checksum-abort — Replace must never have run", got)
	}
}

// TestDoUpgradeMissingAsset confirms a release that does not publish an
// asset for this platform is a real, named error rather than a silent
// no-op or a confusing lower-level failure.
func TestDoUpgradeMissingAsset(t *testing.T) {
	f := newFixtureRelease(t, "v99.0.0", "some-other-platform.bin", nil, nil)
	err := doUpgrade(context.Background(), upgradeOpts{
		apiBase: f.srv.URL, exePath: "/usr/local/bin/friday", currentVersion: "v0.1.0",
	})
	if err == nil {
		t.Fatal("doUpgrade = nil, want an error: the fixture release has no asset matching this platform")
	}
	if !strings.Contains(err.Error(), "no asset named") {
		t.Errorf("error = %q, want it to name the missing-asset condition", err.Error())
	}
}

// TestRunCleansUpStaleUpgradeFile proves upgrade.CleanupStale is actually
// wired into Run (cli.go), not merely correct in isolation — os.Executable
// inside a test binary resolves to the test binary's own path, so a
// ".old" sibling planted next to it is real, disk-backed bait for Run's
// unconditional cleanup call at the top of every invocation.
func TestRunCleansUpStaleUpgradeFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("CleanupStale is a no-op on non-windows; nothing to observe here")
	}
	exePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	oldPath := exePath + ".old"
	if err := os.WriteFile(oldPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(oldPath) }) // in case Run's cleanup fails, don't leak into other tests

	captureStdout(t, func() { Run([]string{"version"}, "test") })

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("Run did not clean up %s (stat err = %v) -- is upgrade.CleanupStale still wired in?", oldPath, err)
	}
}
