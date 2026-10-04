package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHeldCoverFindingString pins Finding.String: a stall's what and why, as
// the check's judgment detail reads it.
func TestHeldCoverFindingString(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		f    Finding
		want string
	}{
		{
			"a primary stall names what it is and why",
			Finding{Subject: "s1-1", What: "s1-1 review",
				Why: "no read is outstanding before its deadline, it is not acceptable, and no judgment is open on it"},
			"stalled: s1-1 review: no read is outstanding before its deadline, it is not acceptable, and no judgment is open on it",
		},
		{
			"a stopped stream stall names the stream",
			Finding{Subject: StreamSubject("s1"), Stream: "s1", What: "stream s1 stopped (conflict)",
				Why: "no judgment is open on it"},
			"stalled: stream s1 stopped (conflict): no judgment is open on it",
		},
		{
			"an empty finding still renders both separators",
			Finding{},
			"stalled: : ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.f.String())
		})
	}
}

// TestHeldCoverCheckHeld pins the check's rule 12 mapping: every stall is one
// violation whose detail is Finding.String, and a state every primary is held
// in writes none.
func TestHeldCoverCheckHeld(t *testing.T) {
	t.Parallel()
	stalled := inReviewFailed(t)
	stalled.s.Open = nil // the judgment closed with no step writing what it needs next
	held := inReviewFailed(t)
	for _, tc := range []struct {
		name string
		h    HeldState
		want []Violation
	}{
		{
			"a stalled primary is one rule 12 violation",
			running(stalled),
			[]Violation{{Rule: 12, Detail: "stalled: s1-1 review: its work came back failed, and no judgment is open on it"}},
		},
		{
			"a primary an open judgment holds writes no violation",
			running(held),
			nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, CheckHeld(tc.h, tc.h.Snap.Now))
		})
	}
}
