// Package source composes the primary store with any additional git-backed
// sources declared under `sources:` in friday.yaml into one ordered Set.
// Every consumer that used to walk cfg.StoreDir alone (the engine planner,
// lint) walks a Set's Roots instead, so an unconfigured store — the common
// case — still resolves to exactly one root and behaves byte for byte as
// before Phase 2.
package source

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zhivko-kocev/friday/internal/config"
	"github.com/zhivko-kocev/friday/internal/rules"
)

// Source is one content root composed into the logical store.
type Source struct {
	Name    string // "local" for the primary, else the manifest's source name
	Dir     string // absolute: primary = cfg.StoreDir, else <StoreDir>/.sources/<name>
	URL     string // git remote; "" for the local-only primary
	Ref     string // branch | tag | sha; "" = the remote's default branch
	Primary bool
	// Cloned reports whether Dir exists. False is the normal state on a
	// second machine — .sources/ is gitignored by design, so a manifest
	// always travels without its clones — and every root resolves to zero
	// matches when absent, which is indistinguishable from an empty source.
	// See Set.Uncloned.
	Cloned bool
}

// Set is every source composed into the store, lowest precedence first —
// the primary is always last, so it wins any path collision with a
// configured source.
type Set []Source

// Load translates friday.yaml's Sources into a Set, appending the primary
// last. cfg.Sources is already validated (unique names, non-empty urls, none
// named "local") by config.load, so the only work left is recording whether
// each source is actually on disk; the error is reserved for a stat that
// fails for a reason other than absence, since a source friday cannot even
// interrogate must not be treated as merely uncloned.
func Load(cfg *config.Config) (Set, error) {
	set := make(Set, 0, len(cfg.Sources)+1)
	for _, spec := range cfg.Sources {
		dir := filepath.Join(cfg.SourcesDir(), spec.Name)
		cloned := false
		if info, err := os.Stat(dir); err == nil {
			cloned = info.IsDir()
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("source %q: %w", spec.Name, err)
		}
		set = append(set, Source{
			Name:   spec.Name,
			Dir:    dir,
			URL:    spec.URL,
			Ref:    spec.Ref,
			Cloned: cloned,
		})
	}
	set = append(set, Source{
		Name:    "local",
		Dir:     cfg.StoreDir,
		Primary: true,
		Cloned:  true, // the manifest was just read out of it
	})
	return set, nil
}

// Uncloned returns the names of configured sources with no directory on
// disk, in manifest order. Callers that render content must surface this:
// a missing source contributes nothing and looks exactly like an empty one,
// so pushing anyway overwrites targets with a partial store while reporting
// success.
func (s Set) Uncloned() []string {
	var names []string
	for _, src := range s {
		if !src.Primary && !src.Cloned {
			names = append(names, src.Name)
		}
	}
	return names
}

// Roots projects the Set to the plain name/dir pairs internal/rules expands
// globs against, preserving precedence order.
func (s Set) Roots() []rules.Root {
	roots := make([]rules.Root, len(s))
	for i, src := range s {
		roots[i] = rules.Root{Name: src.Name, Dir: src.Dir}
	}
	return roots
}
