//go:build unix

package swarm

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit tier never spawns a subprocess, so the kill calls below cannot be observed
// ending a real group here; that effect is the functional tier's (timeout_functional_test.go,
// build tag functional). What a unit test can pin is the branch each function takes -- the
// kernel call for a positive pgid, the refusal for a non-positive one -- and, for
// GroupAlive, the answer the kernel gives for a group that exists and for one that does not.
// absentGroup is a pgid past Linux's pid_max, so no group can hold it and the ignored ESRCH
// is the positive branch's call with nothing touched.

// absentGroup names a process group the kernel cannot have handed out.
const absentGroup = 1 << 30

// TestProcUnixCoverTerminateGroup pins that TerminateGroup asks the kernel for a positive
// group and refuses a non-positive one without panicking.
func TestProcUnixCoverTerminateGroup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		pgid int
	}{
		{"positive group asks the kernel", absentGroup},
		{"zero refuses", 0},
		{"negative refuses", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.NotPanics(t, func() { TerminateGroup(tc.pgid, "started") },
				"TerminateGroup(%d, started) must ask the kernel and never panic", tc.pgid)
		})
	}
}

// TestProcUnixCoverKillGroup pins that KillGroup asks the kernel for a positive group and
// refuses a non-positive one without panicking.
func TestProcUnixCoverKillGroup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		pgid int
	}{
		{"positive group asks the kernel", absentGroup},
		{"zero refuses", 0},
		{"negative refuses", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.NotPanics(t, func() { KillGroup(tc.pgid, "started") },
				"KillGroup(%d, started) must ask the kernel and never panic", tc.pgid)
		})
	}
}

// TestProcUnixCoverGroupAlive pins the liveness answer for a live group, an absent group,
// and the non-positive refusal. The live group is the test process's own: signal 0 asks the
// kernel and sends nothing.
func TestProcUnixCoverGroupAlive(t *testing.T) {
	t.Parallel()

	self := syscall.Getpgrp()
	require.Positive(t, self, "the test process must belong to a process group")

	cases := []struct {
		name string
		pgid int
		want bool
	}{
		{"a live group is alive", self, true},
		{"an absent group is not alive", absentGroup, false},
		{"zero is not a group", 0, false},
		{"negative is not a group", -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, GroupAlive(tc.pgid, "started"),
				"GroupAlive(%d, started)", tc.pgid)
		})
	}
}
