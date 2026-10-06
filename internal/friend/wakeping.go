package friend

import (
	"slices"
	"strings"
)

// WakeRow is one row of the friends table as the sprint server's coordinator view
// gives it: the friend's name and her status (up, down or held).
type WakeRow struct{ Name, Status string }

// WakeTargets is the friends a wake pass pings (docs/SPEC-FRIEND.md, "The wake ping
// loop"): the rows whose status is up, never the coordinator itself and never a
// friend in never, sorted. A friend held or down is not asked: a hold is the
// coordinator's word and a down friend has no daemon to push the ping in.
func WakeTargets(me string, rows []WakeRow, never []string) []string {
	var out []string
	for _, r := range rows {
		if r.Status == "up" && r.Name != me && !slices.Contains(never, r.Name) {
			out = append(out, r.Name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// DeafChange remembers who was last reported deaf, so the coordinator is told
// once per change and never once per pass.
type DeafChange struct{ last string }

// Report takes the friends whose session did not answer this pass and answers
// the sorted names to tell the coordinator, or none when the set is empty or
// the same as the one last reported. A set that empties is remembered, so the
// next deaf friend is a change again.
func (d *DeafChange) Report(deaf []string) []string {
	deaf = slices.Compact(slices.Sorted(slices.Values(deaf)))
	key := strings.Join(deaf, ",")
	if key == d.last {
		return nil
	}
	d.last = key
	return deaf
}
