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
// judgment raised at the edge is card a-backup-transition-pushes-one-judgment-b's.

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
// 77, merging 44), 250 reads waiting; review 279: 9 reads out, 250 want a reader, 20 found
// broken (16 brief defects), 0 failed (0 brief defects), 0 acceptable"; "" while the state
// is none. The account after the semicolon (sprint.ReviewWaits) is what review waits on,
// each primary once, so a review count is never read as reads to do; it is left out while
// the record has none (an older tick's).
func backupLine(state string, working, review, merging int64, waiting int, waits sprint.ReviewWaits) string {
	tail := ""
	if waits.Review > 0 {
		tail = "; " + waits.String()
	}
	switch state {
	case sprint.BackupReads:
		return fmt.Sprintf("backup: reads (review %d > working %d, merging %d), %d reads waiting%s", review, working, merging, waiting, tail)
	case sprint.BackupMerges:
		return fmt.Sprintf("backup: merges (merging %d > review %d + working %d), %d reads waiting%s", merging, review, working, waiting, tail)
	}
	return ""
}

// streamPriorityField is a work row's field in where --json: the stream's default level for
// cards added later (sprint.StreamPriority), normal when none is set.
const streamPriorityField = "priority"
