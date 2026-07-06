// Package snapshot records the before/after state of every push so
// `friday rollback` can restore a target that a push ate, and so conflict
// prompts can recover the last-synced content for a 3-way merge.
//
// Layout under $UserCacheDir/friday/snapshots/:
//
//	objects/<hh>/<hash>   content-addressed blobs (newline-normalized)
//	journal.json          ordered list of snapshots, newest last
//
// Blobs are addressed by drift.Hash so a drift baseline hash doubles as a
// blob key. The journal keeps the last keepSnapshots entries; pruning drops
// older entries and any blob no surviving snapshot references.
package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zhivko-kocev/friday/internal/atomicio"
	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/textnorm"
)

const keepSnapshots = 10

// FileWrite is one file a push wrote, in neutral terms (no engine types) so
// the dependency arrow stays engine → snapshot capable.
type FileWrite struct {
	Adapter string
	Path    string // absolute target path
	Old     []byte // nil = the push created the file
	New     []byte
	Src     []byte // store-side source content, when the write had one
}

// FileState is the journaled form of a FileWrite.
type FileState struct {
	Adapter string `json:"adapter"`
	Path    string `json:"path"`
	OldHash string `json:"old_hash,omitempty"` // "" = created
	NewHash string `json:"new_hash"`
	SrcHash string `json:"src_hash,omitempty"`
}

type Snapshot struct {
	ID    string      `json:"id"`
	Time  time.Time   `json:"time"`
	Files []FileState `json:"files"`
}

// Dir returns the snapshot root beside the drift cache.
func Dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "friday", "snapshots"), nil
}

// Record persists one snapshot of the given writes. A nil error with a nil
// snapshot means there was nothing to record.
func Record(dir string, files []FileWrite) (*Snapshot, error) {
	if len(files) == 0 {
		return nil, nil
	}
	snap := &Snapshot{Time: time.Now().UTC()}
	for _, f := range files {
		st := FileState{Adapter: f.Adapter, Path: f.Path}
		var err error
		if st.NewHash, err = writeBlob(dir, f.New); err != nil {
			return nil, err
		}
		if f.Old != nil {
			if st.OldHash, err = writeBlob(dir, f.Old); err != nil {
				return nil, err
			}
		}
		if f.Src != nil {
			if st.SrcHash, err = writeBlob(dir, f.Src); err != nil {
				return nil, err
			}
		}
		snap.Files = append(snap.Files, st)
	}

	journal, err := List(dir)
	if err != nil {
		return nil, err
	}
	snap.ID = newID(snap.Time, journal)
	journal = append(journal, *snap)
	if len(journal) > keepSnapshots {
		journal = journal[len(journal)-keepSnapshots:]
	}
	if err := saveJournal(dir, journal); err != nil {
		return nil, err
	}
	return snap, pruneBlobs(dir, journal)
}

// List returns every journaled snapshot, oldest first.
func List(dir string) ([]Snapshot, error) {
	data, err := os.ReadFile(journalPath(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	return out, json.Unmarshal(data, &out)
}

// Get returns the snapshot with the given id, or the newest one when id is
// empty.
func Get(dir, id string) (Snapshot, error) {
	journal, err := List(dir)
	if err != nil {
		return Snapshot{}, err
	}
	if len(journal) == 0 {
		return Snapshot{}, fmt.Errorf("no snapshots recorded yet — snapshots are taken on every push")
	}
	if id == "" {
		return journal[len(journal)-1], nil
	}
	for _, s := range journal {
		if s.ID == id {
			return s, nil
		}
	}
	return Snapshot{}, fmt.Errorf("no snapshot %q (see `friday rollback --list`)", id)
}

// ReadBlob returns the content stored under hash, if present.
func ReadBlob(dir, hash string) ([]byte, bool) {
	data, err := os.ReadFile(blobPath(dir, hash))
	if err != nil {
		return nil, false
	}
	return data, true
}

// Restore puts every file in the snapshot back to its pre-push state:
// updated files get their old content, created files are deleted. Restored
// content is re-recorded as the drift baseline so the next push sees a clean
// tree instead of phantom drift. Returns the restored paths.
func Restore(dir string, snap Snapshot, ds *drift.Store) ([]string, error) {
	var restored []string
	for _, f := range snap.Files {
		if f.OldHash == "" {
			if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
				return restored, err
			}
			restored = append(restored, f.Path+" (deleted)")
			continue
		}
		old, ok := ReadBlob(dir, f.OldHash)
		if !ok {
			return restored, fmt.Errorf("blob %s missing for %s (pruned?)", f.OldHash[:12], f.Path)
		}
		if err := atomicio.WriteFile(f.Path, old, 0o644); err != nil {
			return restored, err
		}
		ds.Set(f.Adapter, f.Path, old)
		restored = append(restored, f.Path)
	}
	return restored, nil
}

func journalPath(dir string) string { return filepath.Join(dir, "journal.json") }

func blobPath(dir, hash string) string {
	return filepath.Join(dir, "objects", hash[:2], hash)
}

func writeBlob(dir string, content []byte) (string, error) {
	norm := textnorm.Newlines(content)
	h := drift.Hash(norm)
	path := blobPath(dir, h)
	if _, err := os.Stat(path); err == nil {
		return h, nil // content-addressed: already stored
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return h, atomicio.WriteFile(path, norm, 0o644)
}

func saveJournal(dir string, journal []Snapshot) error {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicio.WriteFile(journalPath(dir), data, 0o644)
}

// pruneBlobs deletes objects no surviving snapshot references.
func pruneBlobs(dir string, journal []Snapshot) error {
	referenced := map[string]bool{}
	for _, s := range journal {
		for _, f := range s.Files {
			referenced[f.NewHash] = true
			if f.OldHash != "" {
				referenced[f.OldHash] = true
			}
			if f.SrcHash != "" {
				referenced[f.SrcHash] = true
			}
		}
	}
	objects := filepath.Join(dir, "objects")
	entries, err := os.ReadDir(objects)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, sub := range entries {
		if !sub.IsDir() {
			continue
		}
		blobs, err := os.ReadDir(filepath.Join(objects, sub.Name()))
		if err != nil {
			continue
		}
		for _, b := range blobs {
			if !referenced[b.Name()] {
				_ = os.Remove(filepath.Join(objects, sub.Name(), b.Name()))
			}
		}
	}
	return nil
}

// newID derives a readable unique id from the timestamp, de-duplicating
// against the existing journal for same-second pushes.
func newID(t time.Time, journal []Snapshot) string {
	base := t.Format("20060102-150405")
	id := base
	for n := 2; ; n++ {
		taken := false
		for _, s := range journal {
			if s.ID == id {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}
