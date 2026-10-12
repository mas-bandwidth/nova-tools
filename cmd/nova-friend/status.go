// The lane fault a provider's refusal leaves, as nova-friend status and
// nova-friend lanes report it: lane-fault=<n> refusal="<first line>", read
// from the fault file the daemon writes in the state directory
// (docs/SPEC-FRIEND.md, the lanes). A provider refusal is terminal: the daemon
// holds the lane and says so here until a person clears it or the daemon
// restarts.
package main

import (
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// laneFaultFact adds one lane's provider-refusal fault to a status or lanes
// output, when report is a lane fault report: lane_fault=<n>
// refusal="<first line>" (docs/SPEC-FRIEND.md, the lanes). An empty or foreign
// report adds nothing.
func laneFaultFact(o *tool.Out, report string) {
	n, line, ok := friend.LaneFaultParts(report)
	if !ok {
		return
	}
	o.Fact("lane_fault", n).Fact("refusal", tool.Text(line))
}
