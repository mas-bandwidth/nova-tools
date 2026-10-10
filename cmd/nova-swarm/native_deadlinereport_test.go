package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/stretchr/testify/require"
)

// The deadline report is written only for a run the deadline ended and no other end owns.
// Each row that is not due differs from the due row in one field, so dropping a condition
// from deadlineReportDue turns a row red -- the provider hand-back and the provider end
// included, which a report written first would hide (cold read on #5598).
func TestDeadlineReportIsDueOnlyWhenNoOtherEndOwnsTheRun(t *testing.T) {
	t.Parallel()

	due := nativeRunResult{deadlined: true, rc: -1}
	for _, tc := range []struct {
		name          string
		res           nativeRunResult
		handedBack    bool
		providerEnded bool
		want          bool
	}{
		{"the deadline ended it and nothing else", due, false, false, true},
		{"the child ended itself", nativeRunResult{rc: -1}, false, false, false},
		{"handed back to the next route", due, true, false, false},
		{"a provider end was printed", due, false, true, false},
		{"lost response", deadlineCase(due, func(r *nativeRunResult) { r.lost = true }), false, false, false},
		{"idle end", deadlineCase(due, func(r *nativeRunResult) { r.idled = true }), false, false, false},
		{"TERM", deadlineCase(due, func(r *nativeRunResult) { r.terminated = true }), false, false, false},
		{"budget stop", deadlineCase(due, func(r *nativeRunResult) { r.stopped = "budget" }), false, false, false},
		{"wall death", deadlineCase(due, func(r *nativeRunResult) { r.wallReport = "WALL task=x path=/p" }), false, false, false},
		{"wall refusal", deadlineCase(due, func(r *nativeRunResult) { r.wallRefusal = swarm.WallRefusal{Path: "/p"} }), false, false, false},
		{"shell denial", deadlineCase(due, func(r *nativeRunResult) { r.shellDenial = swarm.ShellDenial{Path: "/p"} }), false, false, false},
	} {
		require.Equal(t, tc.want, deadlineReportDue(tc.res, tc.handedBack, tc.providerEnded), tc.name)
	}
}

func deadlineCase(r nativeRunResult, f func(*nativeRunResult)) nativeRunResult {
	f(&r)
	return r
}
