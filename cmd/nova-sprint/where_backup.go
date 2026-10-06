package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// whereBackup uses the work shape already read by where: no card reads, even
// before the first tick. Its properties retain each observer's side at equality.
func whereBackup(work ntable.Table) string {
	c := sprint.PipelineCounts{
		Working: int(cellCount(work, sprint.Working)),
		Review:  int(cellCount(work, sprint.Review)),
		Merging: int(cellCount(work, sprint.Merging)),
	}
	return c.Backup(work.Props)
}
