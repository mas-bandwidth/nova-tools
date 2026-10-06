package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"os"
	"path/filepath"
)

// capacityArgs is the complete volume report sent by each beat (SPEC-FRIEND, jobs capacity).
func capacityArgs(args []string, capacity friend.JobCapacity) []string {
	if capacity.Jobs >= 0 {
		args = append(args, "--jobs-bytes", fmt.Sprint(capacity.Jobs))
	}
	if capacity.Free >= 0 {
		args = append(args, "--free-bytes", fmt.Sprint(capacity.Free))
	}
	if capacity.Error != "" {
		args = append(args, "--capacity-error", capacity.Error)
	}
	if capacity.Inodes != nil {
		args = append(args, "--free-inodes", fmt.Sprint(*capacity.Inodes))
	}
	return args
}

// jobGCVerb uses the daemon's same guarded collector (SPEC-FRIEND, jobs capacity).
func jobGCVerb(w world) tool.Verb {
	return tool.Verb{Name: "gc", Usage: "gc --as <me> [--dir <d>] [--jobs-cap <bytes>] [--dry-run]", Example: "gc --as bob --dir ./bob --dry-run", Effect: tool.LocalWrite + ": remove published job scratch; preserve reports and mirrors", DryRun: true,
		Detail: "Inspect jobs oldest first. A dry run still reads local jobs, reports and git state and asks origin for each eligible head; it removes nothing. Only a clean owned checkout with a report and matching origin head is removed. Active, unreported, dirty and unpushed jobs stay. Removal refused by a harness is deferred.",
		Flags: func(f *tool.Flags) {
			f.Required("as", "your name, the friend whose current working directory is inspected")
			f.String("dir", "", "the friend's working directory (default: the current directory)")
			f.Int("jobs-cap", int(friend.DefaultJobsCap), "jobs scratch cap in bytes, a positive whole number (default: 20 GiB)")
			f.Check(func(c *tool.Call) {
				if c.Int("jobs-cap") <= 0 {
					c.Problem("--jobs-cap wants a positive whole number of bytes")
				}
			})
			f.Prints()
		},
		Run: func(c *tool.Call) *tool.Out {
			dir := c.Str("dir")
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return tool.Refuse(err.Error())
				}
			}
			dir, err := filepath.Abs(dir)
			if err != nil {
				return tool.Refuse(err.Error())
			}
			// The inbox and fresh lane marks protect running work, including batch sessions.
			stager := w.stager(dir)
			if stager.s == nil {
				return tool.Refuse("no job collector is configured; run nova-friend help gc")
			}
			stager.s.JobsCap = int64(c.Int("jobs-cap"))
			stager.s.Now = w.now
			result, err := stager.s.GC(context.Background(), nil, c.DryRun(), 0)
			if err != nil {
				return tool.Refuse(err.Error())
			}
			out := tool.Done().Fact("freed", result.Freed).Fact("jobs", result.Jobs).Fact("cap", result.Cap)
			if c.DryRun() {
				out.Fact("planned", result.Planned).Fact("planned_jobs", len(result.Removed))
			}
			for _, class := range []string{"active", "unowned", "unpublished", "removed", "deferred"} {
				out.Item("class", "name", class, "jobs", result.Classes[class])
			}
			for _, note := range result.Notes {
				out.Note(note)
			}
			return out
		}}
}
