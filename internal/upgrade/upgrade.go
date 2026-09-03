// Package upgrade implements friday's self-update path (D27), chezmoi-style:
// detect the install method before touching anything, defer to the package
// manager when one owns the binary, else download the matching release
// asset, verify it against the release's own published checksum, and
// replace the running binary atomically. Every step here runs only when a
// user explicitly invokes `friday upgrade` — no background or implicit
// version checks (ROADMAP.md's no-daemon posture).
package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zhivko-kocev/friday/internal/atomicio"
)

// DefaultAPIBase is GitHub's own releases API host. A test points a
// different baseURL at a local fixture server instead; production callers
// pass this constant.
const DefaultAPIBase = "https://api.github.com"

// officialReleaseHosts is the allowlist every release URL — metadata and
// asset alike — must resolve to. Asset URLs are not friday's: they arrive
// verbatim in the release JSON's browser_download_url, and the expected
// checksum arrives through that same list, so whoever can name a host
// controls BOTH halves of the checksum comparison and the gate passes on
// tampered bytes. Pinning scheme+host is what makes GitHub's TLS the
// actual trust anchor it is assumed to be.
var officialReleaseHosts = []string{
	"api.github.com",
	"github.com",
	"objects.githubusercontent.com",
	"release-assets.githubusercontent.com",
}

// maxAssetBytes caps a single release download. A friday archive is a few
// megabytes; this is generous enough to never reject a real release and
// small enough that a hostile or wedged host cannot stream unbounded bytes
// into memory (both the archive and the extracted binary are buffered). A
// var rather than a const so a test can shrink it instead of streaming
// 64 MiB over loopback to reach the limit.
var maxAssetBytes int64 = 64 << 20

// downloadTimeout bounds the whole request/response. The ctx urfave/cli
// hands down carries no deadline, so without this a wedged host hangs
// `friday upgrade` forever after the "downloading ..." line.
const downloadTimeout = 5 * time.Minute

// maxRedirects matches net/http's own default hop limit; NewClient has to
// re-state it because supplying CheckRedirect replaces that default.
const maxRedirects = 10

// ValidateReleaseURL rejects a release URL friday must not fetch, before
// any bytes move. apiBase is the release-metadata origin: empty means
// production (the official host allowlist over https), non-empty means a
// test fixture server, and then a URL is accepted only if it shares that
// server's scheme and host — release assets always come from the same
// origin as the metadata that advertised them.
func ValidateReleaseURL(rawURL, apiBase string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("refusing to download %q: unparseable URL: %w", rawURL, err)
	}
	if apiBase != "" {
		base, err := url.Parse(apiBase)
		if err != nil {
			return fmt.Errorf("unparseable release API base %q: %w", apiBase, err)
		}
		if u.Scheme != base.Scheme || u.Host != base.Host {
			return fmt.Errorf("refusing to download %s: release assets must come from the same origin as the release metadata (%s://%s)", rawURL, base.Scheme, base.Host)
		}
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing to download %s: release downloads must use https, not %q", rawURL, u.Scheme)
	}
	if !slices.Contains(officialReleaseHosts, strings.ToLower(u.Hostname())) {
		return fmt.Errorf("refusing to download %s: %q is not an official friday release host (%s)", rawURL, u.Hostname(), strings.Join(officialReleaseHosts, ", "))
	}
	return nil
}

// NewClient returns the client every release fetch must use: bounded by
// downloadTimeout, and with a redirect policy that re-validates each hop,
// so a 302 out of an otherwise-valid asset URL cannot walk the download
// off https or off an official host.
func NewClient(apiBase string) *http.Client {
	return &http.Client{
		Timeout: downloadTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return ValidateReleaseURL(req.URL.String(), apiBase)
		},
	}
}

// Release is the subset of a GitHub release friday's upgrade flow needs.
type Release struct {
	Tag    string
	Assets []Asset
}

// Asset is one file attached to a release.
type Asset struct {
	Name string
	URL  string
}

// FindAsset returns the asset named exactly name, if the release has one.
func (r Release) FindAsset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// LatestRelease queries the GitHub releases API for owner/repo's newest
// published release. baseURL defaults to DefaultAPIBase when empty (a
// test's only seam).
func LatestRelease(ctx context.Context, client *http.Client, baseURL, owner, repo string) (Release, error) {
	return fetchRelease(ctx, client, releaseURL(baseURL, owner, repo, "latest"), "latest release")
}

// ReleaseByTag queries one specific published release, so `friday upgrade
// --version v0.5.0` can pin or roll back to a known-good tag after a bad
// release instead of only ever tracking latest.
func ReleaseByTag(ctx context.Context, client *http.Client, baseURL, owner, repo, tag string) (Release, error) {
	return fetchRelease(ctx, client, releaseURL(baseURL, owner, repo, "tags/"+url.PathEscape(tag)), "release "+tag)
}

func releaseURL(baseURL, owner, repo, suffix string) string {
	if baseURL == "" {
		baseURL = DefaultAPIBase
	}
	return fmt.Sprintf("%s/repos/%s/%s/releases/%s", strings.TrimSuffix(baseURL, "/"), owner, repo, suffix)
}

func fetchRelease(ctx context.Context, client *http.Client, url, what string) (Release, error) {
	if client == nil {
		client = defaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("query %s: %w", what, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("query %s: unexpected status %s", what, resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAssetBytes)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("query %s: decode response: %w", what, err)
	}

	rel := Release{Tag: body.TagName}
	for _, a := range body.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL})
	}
	return rel, nil
}

// SameVersion compares two version strings ignoring an optional leading
// "v" — friday's git tags and GitHub release tags spell it "v0.5.0", the
// -ldflags-stamped build version spells it "0.5.0". It is deliberately
// string equality: it answers "is this the same build?" for any version
// spelling, including a non-numeric one like "dev". Use CompareVersions to
// ask which of two versions is newer.
func SameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// CompareVersions orders two versions by their dotted numeric components,
// ignoring an optional leading "v"; a prerelease suffix ("-rc1") sorts
// before the same release without one, per semver precedence. ok is false
// when either side is not dotted-numeric — a locally built "dev" binary,
// say — in which case cmp is meaningless and the caller must fall back to
// SameVersion. Ordering matters because a command named `upgrade` must not
// silently install an older release over a newer local build.
func CompareVersions(a, b string) (cmp int, ok bool) {
	av, aPre, aOK := parseVersion(a)
	bv, bPre, bOK := parseVersion(b)
	if !aOK || !bOK {
		return 0, false
	}
	for i := 0; i < len(av) || i < len(bv); i++ {
		x, y := 0, 0
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x != y {
			if x < y {
				return -1, true
			}
			return 1, true
		}
	}
	switch {
	case aPre == bPre:
		return 0, true
	case aPre && !bPre:
		return -1, true
	default:
		return 1, true
	}
}

func parseVersion(v string) (parts []int, prerelease, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v, prerelease = v[:i], true
	}
	if v == "" {
		return nil, false, false
	}
	for _, field := range strings.Split(v, ".") {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return nil, false, false
		}
		parts = append(parts, n)
	}
	return parts, prerelease, true
}

// AssetName returns the goreleaser archive filename for goos/goarch,
// matching .goreleaser.yaml's archive name_template
// "{{.Binary}}_{{.Os}}_{{.Arch}}" (tar.gz everywhere, zip on windows).
// windows/arm64 is excluded from the build matrix, so it errors rather than
// returning a name that can never exist.
func AssetName(goos, goarch string) (string, error) {
	if goos == "windows" && goarch == "arm64" {
		return "", fmt.Errorf("no windows/arm64 release is published")
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("friday_%s_%s.%s", goos, goarch, ext), nil
}

// BinaryName returns the binary's filename inside its own archive.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "friday.exe"
	}
	return "friday"
}

// defaultClient is the nil-client fallback for every fetch in this package.
// It exists so no request can ever inherit http.DefaultClient's absence of
// a timeout; a caller that also needs redirect pinning passes NewClient.
var defaultClient = &http.Client{Timeout: downloadTimeout}

// Download fetches url's full body, capped at maxAssetBytes. client
// defaults to defaultClient when nil; production callers pass NewClient so
// redirects stay pinned to an official host.
func Download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = defaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	// Read one byte past the cap so an oversized body is a named refusal
	// rather than a silently truncated archive that fails the checksum.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if int64(len(data)) > maxAssetBytes {
		return nil, fmt.Errorf("download %s: response exceeds the %d-byte release asset limit", url, maxAssetBytes)
	}
	return data, nil
}

// VerifyChecksum checks data's sha256 against assetName's entry in a
// checksums.txt body — the "<hex>  <filename>" lines both sha256sum and
// goreleaser's checksum block produce. A non-200 response or a network
// failure upstream of this call already stops the flow before any bytes
// reach here; a checksum mismatch is the last gate before Replace, so it
// aborts before any write ever happens.
func VerifyChecksum(data []byte, assetName string, checksumsTxt []byte) error {
	want := ""
	for _, line := range strings.Split(string(checksumsTxt), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("no checksum entry for %s", assetName)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, want)
	}
	return nil
}

// ExtractBinary reads binaryName out of a goreleaser archive — zip for a
// ".zip" archiveName (windows), tar.gz otherwise. The archive layout has no
// subdirectory to strip: archives.files in .goreleaser.yaml puts the
// binary, README.md and LICENSE all at the archive root.
func ExtractBinary(archive []byte, archiveName, binaryName string) ([]byte, error) {
	if strings.HasSuffix(archiveName, ".zip") {
		return extractFromZip(archive, binaryName)
	}
	return extractFromTarGz(archive, binaryName)
}

func extractFromTarGz(data []byte, binaryName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open tar.gz: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar.gz: %w", err)
		}
		if hdr.Name == binaryName {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binaryName)
}

func extractFromZip(data []byte, binaryName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name == binaryName {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binaryName)
}

// InstallMethod inspects exePath for a package-manager prefix, chezmoi-
// style: a friday installed via brew/snap/scoop should be upgraded through
// that manager, not by this package downloading over it — the manager
// would just see its own file mysteriously changed, and its next update
// pass would silently revert it. An empty manager means no known manager
// owns exePath, so the download path applies.
func InstallMethod(exePath string) (manager, upgradeCommand string) {
	// ReplaceAll, not filepath.ToSlash, which is a no-op off Windows: this
	// is pure string analysis of a path that may have been recorded on
	// another OS, so the separator rewrite has to happen everywhere.
	p := strings.ToLower(strings.ReplaceAll(exePath, `\`, "/"))
	switch {
	case strings.Contains(p, "/homebrew/"), strings.Contains(p, "/cellar/"), strings.Contains(p, "/.linuxbrew/"):
		return "brew", "brew upgrade friday"
	case strings.Contains(p, "/snap/"):
		return "snap", "snap refresh friday"
	case strings.Contains(p, "/scoop/shims/"):
		return "scoop", "scoop update friday"
	default:
		return "", ""
	}
}

// EnsureInstallable resolves exePath through any symlinks and proves the
// resulting directory accepts a new file, returning the resolved path to
// install into.
//
// Both halves matter. InstallMethod only recognises brew/snap/scoop, so an
// apt/dnf/pacman friday at /usr/bin/friday — or any root-owned
// /usr/local/bin/friday — takes the download path and would otherwise fail
// at the very last step, inside atomicio's temp-file create, with a raw
// "permission denied" after a full download. And a symlinked install
// (/usr/local/bin/friday -> /opt/friday/bin/friday) must have its real
// file replaced, not the link overwritten by a regular file that orphans
// the actual install.
func EnsureInstallable(exePath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("resolve install path %s: %w", exePath, err)
	}
	dir := filepath.Dir(resolved)
	probe, err := os.CreateTemp(dir, ".friday-upgrade-probe-*")
	if err != nil {
		return "", fmt.Errorf("cannot install into %s (%v) — rerun with the privileges that own %s, or upgrade friday through the package manager that installed it", dir, err, resolved)
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return resolved, nil
}

// Replace atomically installs newContent as the binary at exePath,
// preserving exePath's own executable permission bits.
//
// Windows cannot overwrite a currently-running executable in place — an
// open-for-execution file may be renamed to a new name, but not
// overwritten at its existing name — so on Windows the current binary is
// first renamed to exePath+".old" (freeing the original path for the
// write below); any stale ".old" left by an interrupted previous upgrade
// is best-effort removed first so consecutive upgrades never collide.
// Unix allows replacing a running executable's path directly (the process
// keeps its already-open inode mapped), so atomicio.WriteFile alone
// suffices there.
func Replace(exePath string, newContent []byte) error {
	info, err := os.Stat(exePath)
	if err != nil {
		return err
	}
	write := func() error { return atomicio.WriteFile(exePath, newContent, info.Mode().Perm()) }
	if runtime.GOOS == "windows" {
		return replaceRenamingAside(exePath, write)
	}
	return write()
}

// replaceRenamingAside is Replace's Windows path, split out so its recovery
// behaviour is testable on any OS with an injected failing write. Between
// the rename and a successful write nothing exists at exePath, so a write
// that fails there (an AV lock or quarantine on a freshly created .exe,
// ENOSPC, an ACL on the temp create) would leave the user with no friday
// at all — the original is renamed back rather than abandoned, and if even
// that fails the error names both paths so the file can be recovered by
// hand.
func replaceRenamingAside(exePath string, write func() error) error {
	oldPath := exePath + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("rename running binary aside: %w", err)
	}
	if err := write(); err != nil {
		if restoreErr := os.Rename(oldPath, exePath); restoreErr != nil {
			return fmt.Errorf("install new binary: %w; restoring %s from %s also failed (%v) — rename that file back by hand", err, exePath, oldPath, restoreErr)
		}
		return fmt.Errorf("install new binary: %w (the previous %s was restored)", err, exePath)
	}
	return nil
}

// CleanupStale best-effort removes a leftover exePath+".old" from a
// previous Windows upgrade (Replace's own rename-aside step). Called once
// at the start of every friday invocation regardless of subcommand — it is
// purely local disk hygiene (no network, no version comparison), so it
// does not conflict with D27's "no background or implicit version checks":
// there is nothing to check, only a stale file this package itself may
// have left behind to tidy up.
func CleanupStale(exePath string) {
	if runtime.GOOS != "windows" {
		return
	}
	_ = os.Remove(exePath + ".old")
}
