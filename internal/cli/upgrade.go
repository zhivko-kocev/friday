package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"

	ucli "github.com/urfave/cli/v3"

	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/upgrade"
)

// friday's own GitHub coordinates — the release repo .goreleaser.yaml
// already publishes to, and the same owner/repo doUpgrade queries.
const upgradeRepoOwner = "zhivko-kocev"
const upgradeRepoName = "friday"

// upgradeOpts is the upgrade command's decoded flag set plus the two values
// only the real process can supply. apiBase is the single test seam (empty
// means upgrade.DefaultAPIBase; a test points it at a local httptest
// fixture server instead).
type upgradeOpts struct {
	apiBase        string
	exePath        string
	currentVersion string

	check   bool
	force   bool
	version string
}

// newUpgradeCommand is chezmoi-style self-update (D27): detect the install
// method before touching anything, defer to the package manager when one
// owns the binary, else download the matching release asset, verify it
// against the release's own checksums.txt, and replace the running binary
// atomically. Runs only when invoked — no background or implicit version
// checks (ROADMAP.md's no-daemon posture).
func newUpgradeCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "upgrade",
		Category: "advanced",
		Usage:    "download and install the latest friday release",
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "check", Usage: "report what would be installed without downloading or writing anything"},
			&ucli.BoolFlag{Name: "force", Usage: "install even when the release is not newer, or when a package manager appears to own this binary"},
			&ucli.StringFlag{Name: "version", Usage: "install this exact release tag instead of the latest (e.g. --version v0.5.0)"},
		},
		Action: func(ctx context.Context, cmd *ucli.Command) error {
			exePath, err := os.Executable()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			err = doUpgrade(ctx, upgradeOpts{
				exePath:        exePath,
				currentVersion: cmd.Root().Version,
				check:          cmd.Bool("check"),
				force:          cmd.Bool("force"),
				version:        cmd.String("version"),
			})
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			return nil
		},
	}
}

// doUpgrade is the upgrade command's testable core. Every early return
// happens before the step it would have skipped: an install-method match
// returns before any network call, an already-current or newer-than-latest
// version returns before any download, --check returns before any
// download, an unpinnable asset URL is refused before any bytes move, a
// non-writable install directory is refused before the download rather
// than after it, and a checksum mismatch returns before upgrade.Replace
// ever runs.
func doUpgrade(ctx context.Context, o upgradeOpts) error {
	if manager, upgradeCmd := upgrade.InstallMethod(o.exePath); manager != "" {
		if !o.force {
			output.Info("friday was installed via %s -- run: %s", manager, upgradeCmd)
			output.Dim("if that is wrong (a directory that merely looks like %s's), pass --force", manager)
			return nil
		}
		output.Warn("%s appears to own %s; --force given, installing over it anyway", manager, o.exePath)
	}

	// One client for both the metadata and the assets: bounded by a timeout,
	// and with a redirect policy that keeps every hop on an official host.
	client := upgrade.NewClient(o.apiBase)

	rel, err := o.fetchRelease(ctx, client)
	if err != nil {
		return err
	}
	if done := reportNoNewerRelease(rel.Tag, o); done {
		return nil
	}

	assetName, err := upgrade.AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	asset, ok := rel.FindAsset(assetName)
	if !ok {
		return fmt.Errorf("release %s has no asset named %s", rel.Tag, assetName)
	}
	checksumsAsset, ok := rel.FindAsset("checksums.txt")
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt", rel.Tag)
	}
	// The archive URL and the checksum URL both arrive verbatim from the
	// release JSON, so both must be pinned before either is fetched —
	// otherwise whoever names the hosts supplies both halves of the
	// comparison VerifyChecksum is about to make.
	for _, a := range []upgrade.Asset{asset, checksumsAsset} {
		if err := upgrade.ValidateReleaseURL(a.URL, o.apiBase); err != nil {
			return err
		}
	}

	if o.check {
		output.OK("%s is available (current: %s)", rel.Tag, o.currentVersion)
		output.Dim("would install %s from %s", assetName, asset.URL)
		output.Dim("run `friday upgrade` to install it")
		return nil
	}

	// Resolve symlinks and prove the install directory is writable now, so
	// a root-owned or read-only location is a clear refusal instead of a
	// raw permission error after a full download.
	installPath, err := upgrade.EnsureInstallable(o.exePath)
	if err != nil {
		return err
	}

	output.Info("downloading %s (%s)...", rel.Tag, assetName)
	archive, err := upgrade.Download(ctx, client, asset.URL)
	if err != nil {
		return err
	}
	checksums, err := upgrade.Download(ctx, client, checksumsAsset.URL)
	if err != nil {
		return err
	}
	if err := upgrade.VerifyChecksum(archive, assetName, checksums); err != nil {
		return err
	}

	newBinary, err := upgrade.ExtractBinary(archive, assetName, upgrade.BinaryName(runtime.GOOS))
	if err != nil {
		return err
	}
	if err := upgrade.Replace(installPath, newBinary); err != nil {
		return err
	}
	output.OK("upgraded to %s", rel.Tag)
	return nil
}

func (o upgradeOpts) fetchRelease(ctx context.Context, client *http.Client) (upgrade.Release, error) {
	if o.version != "" {
		return upgrade.ReleaseByTag(ctx, client, o.apiBase, upgradeRepoOwner, upgradeRepoName, o.version)
	}
	return upgrade.LatestRelease(ctx, client, o.apiBase, upgradeRepoOwner, upgradeRepoName)
}

// reportNoNewerRelease reports whether the flow should stop because tag is
// not an upgrade. Equality is the ordinary "already up to date" case; the
// ordering check exists because a command named `upgrade` must not silently
// DOWNgrade a locally built or prerelease binary to whatever `latest`
// happens to be. An explicit --version pin means the user chose that tag,
// so only equality stops there.
func reportNoNewerRelease(tag string, o upgradeOpts) bool {
	if o.force {
		return false
	}
	if upgrade.SameVersion(tag, o.currentVersion) {
		output.OK("already at %s", o.currentVersion)
		return true
	}
	if o.version != "" {
		return false
	}
	if cmp, ok := upgrade.CompareVersions(tag, o.currentVersion); ok && cmp < 0 {
		output.OK("already at %s -- newer than the latest release %s", o.currentVersion, tag)
		output.Dim("pass --force to install %s anyway", tag)
		return true
	}
	return false
}
