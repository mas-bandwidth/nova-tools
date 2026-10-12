package main

import (
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// w.lanes reads one friend's lane fault and clears it. A guard's denial is
// terminal: a lane whose turn the provider refused faults and takes no card
// until the daemon restarts or a person runs this verb with --clear-faults,
// which removes the fault file the loop watches and releases the lanes
// (docs/SPEC-FRIEND.md, the lanes). Without --clear-faults it reports the
// standing fault as lane_fault=<n> refusal="<first line>" and writes nothing.
func (w world) lanes(c *tool.Call) *tool.Out {
	state := w.stateDir(c, c.Str("dir"))
	report := friend.ReadLaneFault(state)
	if !c.Bool("clear-faults") {
		if report == "" {
			return tool.Done().Fact("lane_fault", "none")
		}
		o := tool.Done()
		laneFaultFact(o, report)
		return o
	}
	if report == "" {
		return tool.Done().Fact("cleared", "none")
	}
	if c.DryRun() {
		o := tool.Done().Fact("cleared", "would")
		laneFaultFact(o, report)
		return o
	}
	cleared, err := friend.ClearLaneFault(state)
	if err != nil {
		return tool.Refuse("the lane fault file cannot be removed: " + err.Error())
	}
	if !cleared {
		return tool.Done().Fact("cleared", "none")
	}
	o := tool.Done().Fact("cleared", "yes")
	laneFaultFact(o, report)
	return o
}
