package engine

import "github.com/zhivko-kocev/friday/internal/snapshot"

// SnapshotWrites projects the created/updated changes of a completed apply into
// snapshot.FileWrite records for journaling, so `friday rollback` can undo them.
// The one function every write command's snapshot step calls, so the captured
// fields can't drift between commands.
//
// Several rules may land in one target file (claude's preset alone has four
// writers of settings.json), and each later rule's OldContent is the earlier
// rule's NewContent — the pending baseline, not what was on disk before the
// push. snapshot.Restore replays records forward with no dedup of its own, so
// emitting one record per Change would let the last one win and leave the file
// holding an intermediate state. Collapsing per path — first record's Old, last
// record's New — is what makes `friday rollback` restore the true pre-push
// bytes, which is the documented escape hatch for backing out a hook write.
func SnapshotWrites(changes []Change) []snapshot.FileWrite {
	var writes []snapshot.FileWrite
	index := map[string]int{}
	for _, ch := range changes {
		if ch.Action != ActionCreate && ch.Action != ActionUpdate {
			continue
		}
		if i, seen := index[ch.DestPath]; seen {
			writes[i].New = ch.NewContent
			writes[i].Src = ch.SrcContent
			continue
		}
		index[ch.DestPath] = len(writes)
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
