package drift

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/config"
)

// legacyAdapterNames maps a canonical adapter name back to the names older
// releases keyed on, derived from config's one rename table so doctor,
// migrate and the caches can never disagree about what a stale name is. A
// user who renames `antigravity:` to `gemini:` in friday.yaml keeps the
// state friday recorded under the old name; without the fallback every
// previously-synced file looks untracked, prev goes nil, and friday appends
// its wiring beside the entry it can no longer recognise.
var legacyAdapterNames = func() map[string][]string {
	m := make(map[string][]string, len(config.LegacyAdapterNames))
	for legacy, canonical := range config.LegacyAdapterNames {
		m[canonical] = append(m[canonical], legacy)
	}
	for _, names := range m {
		slices.Sort(names)
	}
	return m
}()

// Owned records the hook entries friday last merged into a co-owned target
// (e.g. settings.json), keyed by "adapter:source:absPath" → the canonical
// source JSON it wrote. source (the rule's own From path, e.g. "mcp.yaml" or
// "hooks/hooks.json") is part of the key — not just adapter+absPath —
// because more than one rule can legitimately merge into the same target
// (a render rule's mcp and permissions surfaces both landing in claude's
// settings.json); without it, the second rule's Set would silently
// overwrite the first's prev record, and the first rule's next push would
// retract the second rule's keys as if they were its own stale entries.
// The merge-json strategy consults this to remove friday's own stale
// entries after a store edit, without disturbing the user's entries. It is
// machine-local target state — a sibling of the drift store, never synced into
// ~/.friday — stored separately so the released state.json format is untouched.
type Owned struct {
	path string
	m    map[string]string
}

// OwnedPath returns the owned-state file that sits beside the drift store, so it
// inherits the same location and any test/compile path override.
func OwnedPath(driftPath string) string {
	return filepath.Join(filepath.Dir(driftPath), "hooks-owned.json")
}

// LoadOwned reads the owned-state file; a missing one is an empty store, and
// so is a corrupt one. This is a machine-local cache, not user data: failing
// the load would brick push, pull and status until the user found and deleted
// the file, whereas an empty store only degrades the merge to exact-content
// dedup — still idempotent — and the next Save rewrites it cleanly.
func LoadOwned(path string) (*Owned, error) {
	o := &Owned{path: path, m: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return o, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &o.m); err != nil {
		o.m = map[string]string{}
	}
	return o, nil
}

// Get returns friday's last-written source for a target, or nil when none is
// recorded (first push, or the cache was cleared — the merge then degrades to
// exact-content dedup, still idempotent for unchanged hooks). An entry
// recorded by an older release under an adapter name friday has since renamed
// is found through the fallback below; that fallback is safe because source is
// still part of the key, so it can only ever match the same rule's record.
func (o *Owned) Get(adapter, source, absPath string) []byte {
	if v, ok := o.m[ownedKey(adapter, source, absPath)]; ok {
		return []byte(v)
	}
	for _, legacy := range legacyAdapterNames[adapter] {
		if v, ok := o.m[ownedKey(legacy, source, absPath)]; ok {
			return []byte(v)
		}
	}
	return nil
}

// Set records friday's current source for a target, retiring every shape the
// same record could have been written under before — including the pre-v1.0
// source-less one, which Get deliberately never reads: with several rules
// legitimately merging into one target, a record that does not name its rule
// cannot be attributed to any of them, and handing it to the wrong one would
// let that rule retract a sibling's live content. Retiring it here costs one
// missed cleanup on the first push after an upgrade and self-heals from the
// next one, which is the same degradation an absent cache already has.
func (o *Owned) Set(adapter, source, absPath string, content []byte) {
	delete(o.m, ownedKeyV1(adapter, absPath))
	for _, legacy := range legacyAdapterNames[adapter] {
		delete(o.m, ownedKey(legacy, source, absPath))
		delete(o.m, ownedKeyV1(legacy, absPath))
	}
	o.m[ownedKey(adapter, source, absPath)] = string(content)
}

// Save writes the store atomically after dropping entries whose target file no
// longer exists.
func (o *Owned) Save() error {
	o.vacuum()
	data, err := json.MarshalIndent(o.m, "", "  ")
	if err != nil {
		return err
	}
	return atomicio.WriteFile(o.path, data, 0o644)
}

func (o *Owned) vacuum() {
	for k := range o.m {
		absPath, ok := ownedKeyPath(k)
		if !ok {
			// No release friday shipped wrote a key this shape, so nothing can
			// ever match it — drop it instead of leaking it forever.
			delete(o.m, k)
			continue
		}
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			delete(o.m, k)
		}
	}
}

func ownedKey(adapter, source, absPath string) string {
	return adapter + ":" + source + ":" + absPath
}

// ownedKeyV1 is the key shape shipped before v1.0, without the rule's source.
func ownedKeyV1(adapter, absPath string) string {
	return adapter + ":" + absPath
}

// ownedKeyPath extracts the target path from a key of either shape. An
// adapter name and a rule's From path never contain a colon, but a Windows
// path carries a drive-letter one, so counting colons cannot tell the two
// shapes apart; the boundary is instead the first colon-delimited suffix that
// is an absolute path, which every target path is.
func ownedKeyPath(k string) (string, bool) {
	for i := strings.IndexByte(k, ':'); i >= 0; {
		rest := k[i+1:]
		if filepath.IsAbs(rest) {
			return rest, true
		}
		next := strings.IndexByte(rest, ':')
		if next < 0 {
			return "", false
		}
		i += 1 + next
	}
	return "", false
}
