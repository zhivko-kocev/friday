package snapshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/drift"
)

func TestRecordStoresBlobsByDriftHash(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "f.md")
	if err := os.WriteFile(target, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := Record(dir, []FileWrite{
		{Adapter: "test", Path: target, Old: []byte("v1"), New: []byte("v2"), Src: []byte("store-src")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil || snap.ID == "" {
		t.Fatalf("snap = %+v", snap)
	}
	// Blobs are addressed by drift.Hash, so a drift baseline hash resolves
	// straight to last-synced content (the 3-way merge base).
	for name, content := range map[string]string{"old": "v1", "new": "v2", "src": "store-src"} {
		if blob, ok := ReadBlob(dir, drift.Hash([]byte(content))); !ok || string(blob) != content {
			t.Errorf("%s blob = %q, %v", name, blob, ok)
		}
	}
	if snap2, err := Record(dir, nil); err != nil || snap2 != nil {
		t.Errorf("empty Record = %+v, %v — want nil, nil", snap2, err)
	}
}

func TestRestore(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	updated := filepath.Join(target, "updated.md")
	created := filepath.Join(target, "created.md")
	if err := os.WriteFile(updated, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := Record(dir, []FileWrite{
		{Adapter: "test", Path: updated, Old: []byte("v1"), New: []byte("v2")},
		{Adapter: "test", Path: created, New: []byte("new")},
	})
	if err != nil {
		t.Fatal(err)
	}

	ds, _ := drift.Load(filepath.Join(t.TempDir(), "state.json"))
	if _, err := Restore(dir, *snap, ds); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(updated)
	if err != nil || string(blob) != "v1" {
		t.Errorf("updated = %q, %v — want v1 back", blob, err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Errorf("created file should be deleted (err=%v)", err)
	}
	// Drift baseline points at the restored content — next push is clean.
	if drifted, exists := ds.Check("test", updated); !exists || drifted {
		t.Errorf("baseline after restore: drifted=%v exists=%v", drifted, exists)
	}
}

func TestJournalPruneKeepsLastN(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "f.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range keepSnapshots + 3 {
		content := []byte{byte('a' + i)}
		if _, err := Record(dir, []FileWrite{{Adapter: "t", Path: target, New: content}}); err != nil {
			t.Fatal(err)
		}
	}
	snaps, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != keepSnapshots {
		t.Errorf("journal has %d snapshots, want %d", len(snaps), keepSnapshots)
	}
	// Every surviving snapshot's blobs must still resolve.
	for _, s := range snaps {
		for _, f := range s.Files {
			if _, ok := ReadBlob(dir, f.NewHash); !ok {
				t.Errorf("blob %s missing after prune", f.NewHash[:12])
			}
		}
	}
}

func TestGetLatestAndByID(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "f.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s1, err := Record(dir, []FileWrite{{Adapter: "t", Path: target, New: []byte("1")}})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Record(dir, []FileWrite{{Adapter: "t", Path: target, New: []byte("2")}})
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID == s2.ID {
		t.Fatalf("same-second snapshots share id %s", s1.ID)
	}
	latest, err := Get(dir, "")
	if err != nil || latest.ID != s2.ID {
		t.Errorf("latest = %+v, %v", latest, err)
	}
	byID, err := Get(dir, s1.ID)
	if err != nil || byID.ID != s1.ID {
		t.Errorf("by id = %+v, %v", byID, err)
	}
	if _, err := Get(dir, "nope"); err == nil {
		t.Error("unknown id accepted")
	}
}
