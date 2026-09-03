package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	ucli "github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/git"
	"github.com/zhivko-kocev/friday/internal/initcmd"
	"github.com/zhivko-kocev/friday/internal/output"
	"github.com/zhivko-kocev/friday/internal/ui"
)

// newSourceCommand manages the additional git-backed content sources
// declared under `sources:` in friday.yaml (Phase 2's multi-source store) —
// separate from `remote`, which operates on the primary store's own git
// remote.
func newSourceCommand() *ucli.Command {
	return &ucli.Command{
		Name:     "source",
		Category: "advanced",
		Usage:    "manage additional git-backed content sources (add/list/update/rm)",
		Commands: []*ucli.Command{
			newSourceAddCommand(),
			newSourceListCommand(),
			newSourceUpdateCommand(),
			newSourceRmCommand(),
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			return unknownSubcommand(cmd, "source", "add|list|update|rm")
		},
	}
}

func newSourceAddCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "add",
		Usage:     "clone a git-backed content source into the store",
		ArgsUsage: "<name> <url>",
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "ref", Usage: "branch, tag, or sha to check out (default: the remote's default branch)"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if cmd.Args().Len() != 2 {
				output.Err("usage: friday source add <name> <url> [--ref <ref>]")
				return ucli.Exit("", 1)
			}
			spec := &config.SourceSpec{
				Name: cmd.Args().Get(0),
				URL:  cmd.Args().Get(1),
				Ref:  cmd.String("ref"),
			}
			if err := validateSourceSpec(spec); err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if !git.Available() {
				output.Err("git not found in PATH")
				return ucli.Exit("", 1)
			}

			cfg, err := loadUserOrDefault()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if sourceIndex(cfg, spec.Name) >= 0 {
				output.Err("source %q already exists", spec.Name)
				return ucli.Exit("", 1)
			}

			dest := filepath.Join(cfg.SourcesDir(), spec.Name)
			if nonEmptyDir(dest) {
				output.Err("%s already exists and is not empty", dest)
				return ucli.Exit("", 1)
			}
			if err := initcmd.EnsureSourcesIgnored(cfg.StoreDir); err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if err := cloneSource(dest, spec); err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}

			cfg.Sources = append(cfg.Sources, spec)
			if err := persistSources(cfg); err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			output.OK("added source %q at %s", spec.Name, dest)
			return nil
		},
	}
}

func newSourceListCommand() *ucli.Command {
	return &ucli.Command{
		Name:  "list",
		Usage: "list configured sources",
		Action: func(_ context.Context, cmd *ucli.Command) error {
			cfg, err := loadUserOrDefault()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			if len(cfg.Sources) == 0 {
				output.Dim("no sources configured — add one with `friday source add <name> <url>`")
				return nil
			}
			rows := make([][]string, 0, len(cfg.Sources))
			for _, spec := range cfg.Sources {
				dir := filepath.Join(cfg.SourcesDir(), spec.Name)
				ref := spec.Ref
				if ref == "" {
					ref = "default branch"
				}
				sha := git.HeadSHA(dir)
				switch {
				case sha == "":
					sha = "not cloned"
				case len(sha) > 12:
					sha = sha[:12]
				}
				rows = append(rows, []string{spec.Name, spec.URL, ref, sha, strconv.Itoa(fileCount(dir))})
			}
			lines := alignTable(
				[]string{"NAME", "URL", "REF", "HEAD", "FILES"}, rows,
				lipgloss.Left, lipgloss.Left, lipgloss.Left, lipgloss.Left, lipgloss.Right,
			)
			for _, line := range lines {
				output.Line(output.LevelInfo, "%s", line)
			}
			return nil
		},
	}
}

func newSourceUpdateCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "update",
		Usage:     "clone missing sources, then fetch and fast-forward ref-less ones",
		ArgsUsage: "[name...]",
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if !git.Available() {
				output.Err("git not found in PATH")
				return ucli.Exit("", 1)
			}
			cfg, err := loadUserOrDefault()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			targets := cfg.Sources
			if names := cmd.Args().Slice(); len(names) > 0 {
				targets = nil
				for _, n := range names {
					i := sourceIndex(cfg, n)
					if i < 0 {
						output.Err("unknown source: %s", n)
						return ucli.Exit("", 1)
					}
					targets = append(targets, cfg.Sources[i])
				}
			}
			if len(targets) > 0 {
				if err := initcmd.EnsureSourcesIgnored(cfg.StoreDir); err != nil {
					output.Err("%v", err)
					return ucli.Exit("", 1)
				}
			}
			failed := false
			for _, spec := range targets {
				dir := filepath.Join(cfg.SourcesDir(), spec.Name)
				// A `sources:` entry that arrived with a cloned store was
				// never fetched on this machine. Nothing else clones it —
				// `source add` refuses the name as a duplicate — so without
				// materializing it here every glob under .sources/<name>
				// silently matches nothing and the source is unreachable.
				if !git.IsRepo(dir) {
					if nonEmptyDir(dir) {
						output.Err("%s: %s exists but is not a git repo — remove it and re-run", spec.Name, dir)
						failed = true
						continue
					}
					if err := cloneSource(dir, spec); err != nil {
						output.Err("%s: %v", spec.Name, err)
						failed = true
						continue
					}
					output.OK("%s: cloned", spec.Name)
					continue
				}
				if spec.Ref != "" {
					output.Skip("%s: pinned at %s", spec.Name, spec.Ref)
					continue
				}
				if err := git.Fetch(dir); err != nil {
					output.Err("%s: %v", spec.Name, err)
					failed = true
					continue
				}
				if err := git.Pull(dir); err != nil {
					output.Err("%s: %v", spec.Name, err)
					failed = true
					continue
				}
				output.OK("%s: updated", spec.Name)
			}
			if failed {
				return ucli.Exit("", 1)
			}
			return nil
		},
	}
}

func newSourceRmCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "rm",
		Usage:     "remove a configured source",
		ArgsUsage: "<name>",
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: "force", Usage: "remove without prompting"},
		},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if cmd.Args().Len() != 1 {
				output.Err("usage: friday source rm <name>")
				return ucli.Exit("", 1)
			}
			name := cmd.Args().First()
			cfg, err := loadUserOrDefault()
			if err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			i := sourceIndex(cfg, name)
			if i < 0 {
				output.Err("unknown source: %s", name)
				return ucli.Exit("", 1)
			}
			dir := filepath.Join(cfg.SourcesDir(), name)
			if !cmd.Bool("force") && !confirmYesNo(fmt.Sprintf("remove source %q (%s)?", name, dir)) {
				output.Info("aborted — nothing removed")
				return nil
			}
			// Directory first: dropping the manifest entry before a
			// RemoveAll that can fail (a file locked by an editor, routine
			// on Windows) orphans the clone — `source list` stops showing
			// it and re-adding the same name hits the non-empty guard, with
			// no friday command left to clean it up.
			if err := os.RemoveAll(dir); err != nil {
				output.Err("failed to delete %s: %v — friday.yaml left unchanged", dir, err)
				return ucli.Exit("", 1)
			}
			cfg.Sources = slices.Delete(cfg.Sources, i, i+1)
			if err := persistSources(cfg); err != nil {
				output.Err("%v", err)
				return ucli.Exit("", 1)
			}
			output.OK("removed source %q", name)
			return nil
		},
	}
}

// validateSourceSpec rejects every `source add` argument git or a later
// config.load would choke on, before anything is cloned or persisted.
//
// The name check is config's own: validating it only at load time let
// `friday source add Team_Rules <url>` write a manifest friday could no
// longer open, taking down every subsequent command including the `source
// rm` that would have undone it — and filepath.Join cleans away "../..", so
// an unvalidated name escaped .sources/ entirely.
func validateSourceSpec(spec *config.SourceSpec) error {
	if err := config.ValidateSourceName(spec.Name); err != nil {
		return err
	}
	if err := git.ValidateURL(spec.URL); err != nil {
		return err
	}
	// git.CheckoutRef passes the ref positionally with no `--` separator, and
	// friday.yaml keeps it for every later `source update`. An empty ref is
	// legal here — it means "track the remote's default branch" — so only a
	// non-empty one is validated.
	if spec.Ref != "" {
		if err := git.ValidateRef(spec.Ref); err != nil {
			return err
		}
	}
	return nil
}

// cloneSource clones spec into dir and checks out its pinned ref. Shared by
// `source add` and by `source update`'s materialization of a source that
// arrived in a cloned friday.yaml.
func cloneSource(dir string, spec *config.SourceSpec) error {
	if err := ui.WithSpinner("cloning "+spec.URL, func() error { return git.Clone(spec.URL, dir) }); err != nil {
		return err
	}
	if err := checkoutPinnedRef(dir, spec.Ref); err != nil {
		// A clone whose ref never resolved is not the source the user asked
		// for, and leaving it behind trips the "already exists and is not
		// empty" guard on the retry.
		_ = os.RemoveAll(dir)
		return err
	}
	return nil
}

// checkoutPinnedRef moves a freshly cloned source onto spec.Ref. A fresh
// clone is shallow and single-branch (git.Clone's --depth=1); Fetch widens
// the refspec so a ref on any branch, not just the default, is checkout-able.
func checkoutPinnedRef(dir, ref string) error {
	if ref == "" {
		return nil
	}
	if err := git.Fetch(dir); err != nil {
		return err
	}
	return git.CheckoutRef(dir, ref)
}

// persistSources writes cfg.Sources back to friday.yaml.
//
// An existing manifest is edited in place through config.EditManifest, so
// every comment, key order, and untouched preset rule survives: cfg.Save()
// re-marshals the whole struct, turning one `source add` into a hundred-line
// reformat of a file the user hand-edits and `friday remote push` commits. A
// store with no manifest yet has nothing to preserve, so it gets the
// generated one — announced first, because that file pins all six presets.
func persistSources(cfg *config.Config) error {
	if _, err := os.Stat(cfg.ManifestPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		output.Info("creating %s seeded with %d built-in presets", cfg.ManifestPath, len(cfg.Adapters))
		return cfg.Save()
	}
	return config.EditManifest(cfg.ManifestPath, func(doc *yaml.Node) error {
		if len(cfg.Sources) == 0 {
			config.DeleteMappingKey(doc, "sources")
			return nil
		}
		node, err := config.EncodeNode(cfg.Sources)
		if err != nil {
			return err
		}
		config.SetMappingValue(doc, "sources", node)
		return nil
	})
}

// sourceIndex finds a configured source by name, or -1.
func sourceIndex(cfg *config.Config, name string) int {
	for i, s := range cfg.Sources {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// nonEmptyDir reports whether path exists and has at least one entry —
// the state git clone refuses to clone into.
func nonEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// fileCount counts dir's files (not directories), skipping dot-dirs like
// .git — a quick sense of a source's size for `source list`. Zero for an
// uncloned source rather than an error.
func fileCount(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		return nil
	})
	return count
}
