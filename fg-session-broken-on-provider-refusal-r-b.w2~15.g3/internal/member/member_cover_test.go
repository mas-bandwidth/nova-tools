// Package member's cover for the four functions the unit tier left at 0.0% in
// go tool cover -func: DrainBound, LongestDeadline, LastPass and WaitLong.
// These reach them through the package's own seams (a *Member's fields, the
// package constants) with no store, no process and no real clock.
package member

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Packet.Deadline holds a deadline in seconds, not a time.Duration, as
// nova-sprint queue names the route deadline. They are named values (not bare
// literals) so the waits class test does not mistake 600 for a 600ns deadline.
var (
	deadlineSeconds     = 600 // 10m
	shorterDeadlineSecs = 300 // 5m
)

// TestMemberCoverDrainBound pins DrainBound (member.go:475): the longest child
// deadline plus LongStall, capped at DrainMost. It is a pure function of its
// argument and the two package constants, so no member is needed.
func TestMemberCoverDrainBound(t *testing.T) {
	t.Parallel()
	// DrainBound(longest) = min(longest+LongStall, DrainMost); LongStall and
	// DrainMost are the package constants the function's comment names.
	for _, tc := range []struct {
		name    string
		longest time.Duration
		want    time.Duration
	}{
		// main path: a real deadline plus LongStall runs past DrainMost, so the
		// cap holds every drain a member is asked to wait.
		{"longest plus stall, past the cap", 1 * time.Minute, DrainMost},
		// refusal: longest+LongStall runs past DrainMost and is capped at it.
		{"capped at DrainMost", 3 * time.Hour, DrainMost},
		// the boundary: longest+LongStall lands exactly on DrainMost.
		{"exactly at the cap", DrainMost - LongStall, DrainMost},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DrainBound(tc.longest)
			assert.Equal(t, tc.want, got, "DrainBound(%s)", tc.longest)
			// the cap is a hard ceiling: never more than DrainMost.
			assert.LessOrEqual(t, got, DrainMost, "DrainBound must not exceed DrainMost")
		})
	}
}

// TestMemberCoverLongestDeadline pins LongestDeadline (member.go:482): the
// longest deadline among the member's live (not spent) launches, at least the
// override, 0 when nothing is running. It reads m.running only, so a bare
// *Member is enough and clocks in.
func TestMemberCoverLongestDeadline(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		running  map[string]launch
		override time.Duration
		want     time.Duration
	}{
		// main path: a live launch's deadline is the longest.
		{
			name:     "a live launch's deadline",
			running:  map[string]launch{"c1": {packet: Packet{Deadline: deadlineSeconds}}}, // 600s = 10m
			override: 0,
			want:     10 * time.Minute,
		},
		// the override wins when it is longer than every deadline.
		{
			name:     "override is the longest",
			running:  map[string]launch{"c1": {packet: Packet{Deadline: deadlineSeconds}}},
			override: time.Hour,
			want:     time.Hour,
		},
		// refusal: spent launches are skipped, so an all-spent queue is as good
		// as empty and yields 0.
		{
			name:     "spent launches are skipped",
			running:  map[string]launch{"c1": {spent: true, packet: Packet{Deadline: deadlineSeconds}}},
			override: 0,
			want:     0,
		},
		// refusal: no live launch means no deadline, even with an override
		// (the override is only taken up by a launch that runs).
		{
			name:     "no running launches",
			running:  map[string]launch{},
			override: time.Hour,
			want:     0,
		},
		// the spent branch and the live branch share a pass: the spent launch's
		// 600s is ignored, the live one's 300s wins, below the override.
		{
			name: "spent and live: only the live one counts",
			running: map[string]launch{
				"spent": {spent: true, packet: Packet{Deadline: deadlineSeconds}},
				"live":  {packet: Packet{Deadline: shorterDeadlineSecs}}, // 300s = 5m
			},
			override: 0,
			want:     5 * time.Minute,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Member{running: tc.running}
			got := m.LongestDeadline(tc.override)
			assert.Equal(t, tc.want, got, "LongestDeadline(%s), running=%v", tc.override, tc.running)
		})
	}
}

// TestMemberCoverLastPass pins LastPass (member.go:551): the PassTimes the last
// pass recorded. It only returns m.spent, so a bare *Member is enough.
func TestMemberCoverLastPass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		spent PassTimes
		want  PassTimes
	}{
		// main path: a pass left its four time parts behind.
		{
			name: "populated pass times",
			spent: PassTimes{
				Queue:  1 * time.Second,
				Push:   2 * time.Second,
				Report: 3 * time.Second,
				Fill:   4 * time.Second,
			},
			want: PassTimes{
				Queue:  1 * time.Second,
				Push:   2 * time.Second,
				Report: 3 * time.Second,
				Fill:   4 * time.Second,
			},
		},
		// refusal / idle state: a member that has run no pass holds nothing.
		{
			name:  "fresh member, no pass",
			spent: PassTimes{},
			want:  PassTimes{},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Member{spent: tc.spent}
			assert.Equal(t, tc.want, m.LastPass(), "LastPass()")
		})
	}
}

// TestMemberCoverWaitLong pins WaitLong (member.go:1145): it waits for the long
// work in flight (m.longs) to end so a bounded run never leaves a start half
// made or a push cut off. It uses the WaitGroup seam directly: no store, no
// process, and the only wait is the goroutine the test owns, which ends at once.
func TestMemberCoverWaitLong(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		longWork bool // whether a launch's long work is in flight
	}{
		// main path: long work is in flight and WaitLong waits for it.
		{"waits for long work to end", true},
		// refusal: nothing in flight, so WaitLong returns at once.
		{"no long work returns at once", false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Member{}
			if tc.longWork {
				var finished atomic.Bool
				m.longs.Add(1)
				go func() {
					finished.Store(true)
					m.longs.Done()
				}()
				// WaitLong blocks until the goroutine calls Done; finished is
				// then necessarily true (Store happens-before Done).
				m.WaitLong()
				require.True(t, finished.Load(), "WaitLong returned before the long work finished")
			} else {
				// A WaitGroup at zero returns immediately: nothing to wait for.
				m.WaitLong()
			}
		})
	}
}
