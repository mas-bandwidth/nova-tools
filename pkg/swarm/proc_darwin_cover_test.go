package swarm

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// proc_darwin_cover_test.go reaches StartStamp in proc_darwin.go, the one
// function the unit tier's per-function coverage table held at 0.0%: no unit
// test called it. proc_darwin.go compiles only under GOOS=darwin, so the
// assertions run on the darwin leg and skip elsewhere with the reason written,
// the shape pkg/sandbox/wrap_darwin_cover_test.go uses for its darwin
// body. Every test is named TestProcDarwinCoverSomething so
// `-run TestProcDarwinCover` selects them.
//
// StartStamp has one path and no refusal branch: the standard library hands
// darwin no raw start stamp (the value lives behind sysctl in a record only
// golang.org/x/sys returns, and running ps is the one thing this tool must
// never do), so the function states the absence as a dash rather than inventing
// a value. The rows pin that answer for this process's live pid, for a
// non-positive pid and for a pid no process can hold; the dash is the same on
// each because the comparison that reads it compares dash with dash (rule 17,
// slot.go). Nothing here sleeps, reads a real clock, opens a socket, starts a
// process or touches a store.

// TestProcDarwinCoverStartStamp pins StartStamp's one answer, the dash, on the
// darwin body: this process's live pid, a non-positive pid and an absent pid
// all state the absence rather than inventing a stamp this platform cannot
// honestly produce.
func TestProcDarwinCoverStartStamp(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skipf("skipped on %s: proc_darwin.go's StartStamp is the darwin body's; %s builds its own", runtime.GOOS, runtime.GOOS)
	}

	for _, tc := range []struct {
		name string
		pid  int
	}{
		{"main path: this process's live pid states the absence", os.Getpid()},
		{"refusal: a non-positive pid invents nothing", 0},
		{"refusal: an absent pid invents nothing", 1 << 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "-", StartStamp(tc.pid),
				"StartStamp(%d): darwin has no raw start stamp in the standard library, so the dash is the stated absence", tc.pid)
		})
	}
}
