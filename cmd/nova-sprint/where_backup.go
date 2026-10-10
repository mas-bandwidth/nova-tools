package main

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// where's backup state (docs/SPEC-SPRINT.md section 1, "Priority"; sprint.BackupOf): reads
// when the work table's review count exceeds its working count, merges when merging exceeds
// review and working together, none else, read from the work table's count cells (no card is
// read), with the reads waiting (sprint.ReadsWaiting) from the tick's where record. The
// tick raises one judgment at each edge (sprint.TickBackup).

// pipelineCounts is the work table's primaries working, in review and merging, summed over
// its rows from the count cells, an archived stream's row aside (it holds none of them).
func pipelineCounts(t ntable.Table) (working, review, merging int64) {
	for _, r := range t.Rows {
		if archivedRow(t, r) {
			continue
		}
		for _, x := range []struct {
			col string
			n   *int64
		}{{sprint.Working, &working}, {sprint.Review, &review}, {sprint.Merging, &merging}} {
			if j := t.Column(x.col); j >= 0 && j < len(r.Cells) {
				*x.n += r.Cells[j].Count
			}
		}
	}
	return working, review, merging
}

// backupLine is where's backup line under the summary, "backup: reads (review 279 > working
// 77, merging 44), 250 reads waiting"; "" while the state is none.
func backupLine(state string, working, review, merging int64, waiting int) string {
	switch state {
	case sprint.BackupReads:
		return fmt.Sprintf("backup: reads (review %d > working %d, merging %d), %d reads waiting", review, working, merging, waiting)
	case sprint.BackupMerges:
		return fmt.Sprintf("backup: merges (merging %d > review %d + working %d), %d reads waiting", merging, review, working, waiting)
	}
	return ""
}

// streamPriorityField is a work row's field in where --json: the stream's default level for
// cards added later (sprint.StreamPriority), normal when none is set.
const streamPriorityField = "priority"
