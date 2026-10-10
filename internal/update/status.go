// status is inspection: the reads of check; writes nothing.

package update

import (
	"context"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// statusOut renders the status read -- and check's, which is status with the
// current entries hidden: every selected entry's item, then the run's counts.
func statusOut(verb string, ctx context.Context, selected []Entry, o options, env Environment, entries []Entry, kinds []string, started time.Time) *tool.Out {
	// status is check's read with every entry's item shown, current ones included
	// (SPEC-UPDATE rule 9): the same reads, the same exit, its own first token.
	results := readEntries(ctx, selected, o, env, false)
	counts := map[string]int{}
	pins := 0
	res := &tool.Out{Verb: verb, Status: tool.OK}
	for _, r := range results {
		status, ahead := verdict(r)
		counts[status]++
		if r.Entry.Kind == "pin" && status == "DIFFERENT" {
			pins++
		}
		if status == "EQUAL" && verb != "status" {
			continue
		}
		e := r.Entry
		switch status {
		case "UNKNOWN":
			x := r.Installed
			if x.Known() {
				x = r.Latest
			}
			res.Item("unknown", "name", e.Name, "kind", e.Kind, "installed", r.Installed.Version, "path", r.Installed.Path, "source", r.Latest.Source, "reason", x.Reason, "remedy", x.Remedy)
		case "AHEAD":
			res.Item("ahead", "name", e.Name, "kind", e.Kind, "installed", r.Installed.Version, "latest", r.Latest.Version, "ahead", ahead, "path", r.Installed.Path, "source", r.Latest.Source, "owner", e.Owner)
		default:
			res.Item(strings.ToLower(status), "name", e.Name, "kind", e.Kind, "installed", r.Installed.Version, "latest", r.Latest.Version, "path", r.Installed.Path, "source", r.Latest.Source, "owner", e.Owner)
		}
	}
	if counts["EQUAL"] != len(selected) {
		res.Status, res.Exit = tool.Failed, 1
	}
	return res.Fact("checked", len(selected)).Fact("current", counts["EQUAL"]).Fact("stale", counts["STALE"]).Fact("newer", counts["NEWER"]).
		Fact("ahead", counts["AHEAD"]).Fact("differ", counts["DIFFERENT"]).Fact("unknown", counts["UNKNOWN"]).Fact("pins", pins).
		Fact("took", env.Now().Sub(started).Round(time.Millisecond).String()).Fact("file", o.file).Fact("entries", len(entries)).
		Fact("kinds", strings.Join(kinds, ",")).Fact("at", started.UTC().Format(time.RFC3339)).
		Fact("timeout", o.timeout.String()).Fact("budget", o.budget.String()).Fact("max", o.max)
}
