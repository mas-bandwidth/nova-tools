package stream

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The alarms in structure (nova-tools #4324; Glenn 2026-09-26 ~11:30 AM ET:
// "you don't watch and see when something is stuck or taking too long, or
// going one at a time"). Three shapes, each a typed line, none in the
// coordinator's attention:
//
//   - Every landing step prints one line with its wall: REBASED <member> ms=,
//     PUSHED <head> ms=, PR <n> opened ms=, CI <head> <result> ms=, MERGED
//     <sha> ms=, LANDED n= total_ms=. Steps is that clock; a test injects
//     Now.
//   - One at a time: land stream refuses to open a PR carrying fewer members
//     than the stream has in merging at that moment (LAND-SERIAL, the members
//     left out named with why), unless the caller says --partial, and then
//     the same line prints as allowed. A single-member PR to dev from a
//     stream branch is impossible without that line in the log.
//   - Stuck and too long (LAND-SLOW, LAND-WALL) are the reconciler's land
//     watch (internal/nsprint/reconcile/land_watch.go), from merging_at.

// Steps prints one line per landing step with the wall since the last step.
type Steps struct {
	W    io.Writer
	Now  func() time.Time
	t0   time.Time
	last time.Time
}

// NewSteps starts the clock at now. A nil w prints nothing; a nil now is the
// wall clock.
func NewSteps(w io.Writer, now func() time.Time) *Steps {
	if now == nil {
		now = time.Now
	}
	t := now()
	return &Steps{W: w, Now: now, t0: t, last: t}
}

// Line prints "<text> ms=<since the last step>" and marks the step.
func (s *Steps) Line(format string, a ...any) {
	if s == nil {
		return
	}
	t := s.Now()
	ms := t.Sub(s.last).Milliseconds()
	s.last = t
	if s.W != nil {
		fmt.Fprintf(s.W, format+" ms=%d\n", append(a, ms)...)
	}
}

// Total is the wall since the clock started.
func (s *Steps) Total() time.Duration {
	if s == nil {
		return 0
	}
	return s.Now().Sub(s.t0)
}

// LeftOut is the members of the stream's merging set a landing would not
// carry: every skip with a reason (no read at head, a hold, a low score, a
// closed record, no PR). Sorted by PR number, then task.
func LeftOut(skips []Skip) []Skip {
	out := append([]Skip(nil), skips...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N < out[j].N
		}
		return out[i].Task < out[j].Task
	})
	return out
}

// SerialLine is the LAND-SERIAL line: the stream, how many members the PR
// would carry, how many the stream has in merging, and each member left out
// with why.
func SerialLine(streams []string, carrying int, left []Skip) string {
	parts := make([]string, 0, len(left))
	for _, s := range left {
		who := "#" + fmt.Sprint(s.N)
		if s.N == 0 {
			who = s.Task
		}
		parts = append(parts, who+":"+s.Why)
	}
	return fmt.Sprintf("LAND-SERIAL stream=%s carrying=%d merging=%d left_out=%s",
		field(strings.Join(streams, "+")), carrying, carrying+len(left), strings.Join(parts, ","))
}

// field is a value on a key=value line (internal/oneline: whitespace and
// '=' escaped, one line).
func field(s string) string { return oneline.Field(s) }
