package drift

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhivko-kocev/friday/internal/atomicio"
)

// Owned records the hook entries friday last merged into a co-owned target
// (e.g. settings.json), keyed by "adapter:absPath" → the canonical source JSON
// it wrote. The merge-json strategy consults it to remove friday's own stale
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

// LoadOwned reads the owned-state file; a missing file is an empty store.
func LoadOwned(path string) (*Owned, error) {
	o := &Owned{path: path, m: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return o, nil
		}
		return nil, err
	}
	return o, json.Unmarshal(data, &o.m)
}

// Get returns friday's last-written source for a target, or nil when none is
// recorded (first push, or the cache was cleared — the merge then degrades to
// exact-content dedup, still idempotent for unchanged hooks).
func (o *Owned) Get(adapter, absPath string) []byte {
	if v, ok := o.m[o.key(adapter, absPath)]; ok {
		return []byte(v)
	}
	return nil
}

// Set records friday's current source for a target.
func (o *Owned) Set(adapter, absPath string, content []byte) {
	o.m[o.key(adapter, absPath)] = string(content)
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
		// Split on the first colon: the "adapter:" separator precedes any
		// Windows drive-letter colon in the absolute path, so the path survives
		// intact (mirrors drift.Store.vacuum).
		i := strings.IndexByte(k, ':')
		if i < 0 {
			continue
		}
		if _, err := os.Stat(k[i+1:]); os.IsNotExist(err) {
			delete(o.m, k)
		}
	}
}

func (o *Owned) key(adapter, absPath string) string { return adapter + ":" + absPath }
