package sprint

import (
	"strconv"
	"time"
)

// The drift between the base and the development branch (docs/SPEC-SPRINT.md, "Dev sync
// every cycle"; the owner, 2026-10-04: "promote every cycle or drift causes a big
// fuckup"), as the merge table's properties record it and the dashboard's merge row and
// `where --json` read it (DevDriftOf). Nothing writes them yet: the dev sync of each land
// cycle that was to merge dev into the base and record them was never called by the land
// round, and went in the dead code sweep of 2026-10-07 (commit 901463230 wrote it).

// The merge table's properties of the dev sync: the last sync (when, and the base's sha
// after it) and the drift the last cycle measured.
const (
	PropDevSyncAt        = "dev_sync_at"
	PropDevSyncSha       = "dev_sync_sha"
	PropDevSyncBaseLacks = "dev_sync_base_lacks"
	PropDevSyncDevLacks  = "dev_sync_dev_lacks"
)

// DevDrift is the drift between the base and the development branch: the commits each side
// lacks, and the minutes since the last sync.
type DevDrift struct {
	BaseLacks int       `json:"base_lacks"` // commits dev has that the base lacks
	DevLacks  int       `json:"dev_lacks"`  // commits the base has that dev lacks
	Minutes   int       `json:"minutes"`    // since the last sync; 0 when none is recorded
	LastSync  time.Time `json:"last_sync,omitzero"`
	LastSha   string    `json:"last_sha,omitempty"`
}

// LastDevSync is the last dev sync the merge table records: when, and the base's sha after
// it; ok false when none is recorded.
func LastDevSync(s *Snapshot) (at time.Time, sha string, ok bool) {
	if s == nil {
		return time.Time{}, "", false
	}
	v, has := s.Merge.Prop(PropDevSyncAt)
	if !has {
		return time.Time{}, "", false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, "", false
	}
	sha, _ = s.Merge.Prop(PropDevSyncSha)
	return t, sha, true
}

// DevDriftOf is the drift the merge table records, the minutes counted to the snapshot's
// clock: what `where --json` and the dashboard show.
func DevDriftOf(s *Snapshot) DevDrift {
	var d DevDrift
	if s == nil {
		return d
	}
	d.LastSync, d.LastSha, _ = LastDevSync(s)
	if !d.LastSync.IsZero() && s.Now.After(d.LastSync) {
		d.Minutes = int(s.Now.Sub(d.LastSync).Minutes())
	}
	d.BaseLacks = propInt(s.Merge, PropDevSyncBaseLacks)
	d.DevLacks = propInt(s.Merge, PropDevSyncDevLacks)
	return d
}

func propInt(t *Table, name string) int {
	v, _ := t.Prop(name)
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}
