package engine

import "github.com/zhivko-kocev/friday/internal/snapshot"

// SnapshotWrites projects the created/updated changes of a completed apply into
// snapshot.FileWrite records for journaling, so `friday rollback` can undo them.
// Shared by every write command's snapshot step (CLI and control room) so the
// captured fields can't drift between the two front-ends.
func SnapshotWrites(changes []Change) []snapshot.FileWrite {
	var writes []snapshot.FileWrite
	for _, ch := range changes {
		if ch.Action != ActionCreate && ch.Action != ActionUpdate {
			continue
		}
		writes = append(writes, snapshot.FileWrite{
			Adapter: ch.Adapter,
			Path:    ch.DestPath,
			Old:     ch.OldContent,
			New:     ch.NewContent,
			Src:     ch.SrcContent,
		})
	}
	return writes
}
