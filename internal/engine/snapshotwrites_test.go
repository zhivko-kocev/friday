package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhivko-kocev/friday/internal/drift"
	"github.com/zhivko-kocev/friday/internal/snapshot"
)

func TestSnapshotWritesSkipsNonWrites(t *testing.T) {
	writes := SnapshotWrites([]Change{
		{DestPath: "/a", Action: ActionInSync, NewContent: []byte("a")},
		{DestPath: "/b", Action: ActionConflict, NewContent: []byte("b")},
		{DestPath: "/c", Action: ActionUnsupported, NewContent: []byte("c")},
		{DestPath: "/d", Action: ActionMissingSource},
		{DestPath: "/e", Action: ActionCreate, NewContent: []byte("e")},
	})
	if len(writes) != 1 || writes[0].Path != "/e" {
		t.Fatalf("got %+v, want only the create of /e", writes)
	}
}

// TestSnapshotWritesCollapsesSharedTarget is the rollback regression for a
// push whose rules share one target file. Each later rule plans against the
// pending map — the earlier rule's own NewContent — so record N's Old is
// record N-1's New. snapshot.Restore replays records forward with no dedup of
// its own, so one record per Change would let the last one win and leave the
// file holding an intermediate state.
func TestSnapshotWritesCollapsesSharedTarget(t *testing.T) {
	dest := filepath.FromSlash("/tmp/settings.json")
	writes := SnapshotWrites([]Change{
		{Adapter: "claude", DestPath: dest, Action: ActionCreate, OldContent: nil, NewContent: []byte("1"), SrcContent: []byte("s1")},
		{Adapter: "claude", DestPath: dest, Action: ActionUpdate, OldContent: []byte("1"), NewContent: []byte("12"), SrcContent: []byte("s2")},
		{Adapter: "claude", DestPath: dest, Action: ActionUpdate, OldContent: []byte("12"), NewContent: []byte("123"), SrcContent: []byte("s3")},
		{Adapter: "claude", DestPath: dest, Action: ActionUpdate, OldContent: []byte("123"), NewContent: []byte("1234"), SrcContent: []byte("s4")},
	})
	if len(writes) != 1 {
		t.Fatalf("got %d records, want 1 per path: %+v", len(writes), writes)
	}
	if writes[0].Old != nil {
		t.Errorf("Old = %q, want nil — the first writer created the file, so rollback must delete it", writes[0].Old)
	}
	if string(writes[0].New) != "1234" {
		t.Errorf("New = %q, want the last writer's content", writes[0].New)
	}
	if string(writes[0].Src) != "s4" {
		t.Errorf("Src = %q, want the last writer's source, which is what New was derived from", writes[0].Src)
	}
}

func TestSnapshotWritesKeepsDistinctPathsInOrder(t *testing.T) {
	writes := SnapshotWrites([]Change{
		{DestPath: "/b", Action: ActionCreate, NewContent: []byte("b1")},
		{DestPath: "/a", Action: ActionCreate, NewContent: []byte("a1")},
		{DestPath: "/b", Action: ActionUpdate, OldContent: []byte("b1"), NewContent: []byte("b2")},
	})
	if len(writes) != 2 || writes[0].Path != "/b" || writes[1].Path != "/a" {
		t.Fatalf("got %+v, want /b then /a in first-appearance order", writes)
	}
	if string(writes[0].New) != "b2" {
		t.Errorf("/b New = %q, want b2", writes[0].New)
	}
}

// TestRollbackRestoresSharedTargetToPrePushBytes drives the real journal and
// restore path: four writers land in one settings.json, and `friday rollback`
// must put back exactly what was on disk before the push. This is the
// documented escape hatch for backing out a confirm-gated hook write, so a
// half-restored file that still carries friday's wired hook commands defeats
// the whole gate.
func TestRollbackRestoresSharedTargetToPrePushBytes(t *testing.T) {
	targetDir := t.TempDir()
	dest := filepath.Join(targetDir, "settings.json")
	const prePush = `{"model":"opus"}`
	if err := os.WriteFile(dest, []byte(prePush), 0o644); err != nil {
		t.Fatal(err)
	}

	// Four rules landing in one target, each planned against the previous
	// rule's NewContent — exactly what planPush's pending map produces.
	stages := []string{
		`{"model":"opus","permissions":{}}`,
		`{"model":"opus","permissions":{},"env":{}}`,
		`{"model":"opus","permissions":{},"env":{},"hooks":{"PreToolUse":"friday-hook"}}`,
		`{"model":"opus","permissions":{},"env":{},"hooks":{"PreToolUse":"friday-hook"},"statusLine":{}}`,
	}
	changes := make([]Change, 0, len(stages))
	old := prePush
	for _, next := range stages {
		changes = append(changes, Change{
			Adapter: "claude", DestPath: dest, Action: ActionUpdate,
			OldContent: []byte(old), NewContent: []byte(next),
		})
		old = next
	}
	if err := os.WriteFile(dest, []byte(stages[len(stages)-1]), 0o644); err != nil {
		t.Fatal(err)
	}

	snapDir := t.TempDir()
	snap, err := snapshot.Record(snapDir, SnapshotWrites(changes))
	if err != nil {
		t.Fatal(err)
	}
	ds, err := drift.Load(filepath.Join(t.TempDir(), "drift.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Restore(snapDir, *snap, ds); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != prePush {
		t.Errorf("rollback left an intermediate state:\n got %s\nwant %s", got, prePush)
	}
}
