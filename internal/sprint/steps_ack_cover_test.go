package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStepsAckCoverSilenceRefusal pins silenceRefusal: the refusal names the
// primary an ack would leave held by nobody and the judgment's other
// decisions as the commands that make them; a judgment with no other
// decision names the verbs that end the primary.
func TestStepsAckCoverSilenceRefusal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		decisions []string
		want      []string
		absent    []string
	}{
		{
			name:      "the other decisions are commands",
			decisions: append([]string(nil), TickDecisions[NWorkLate]...),
			want:      []string{"n1 is the last judgment on s1-1", "fleet level", "fleet down", "wait", "drop"},
			absent:    []string{"rework, return or drop it"},
		},
		{
			name:      "no other decision names the ending verbs",
			decisions: []string{"ack"},
			want:      []string{"n1 is the last judgment on s1-1", "rework, return or drop it"},
			absent:    []string{"fleet level"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := Note{ID: "n1", Kind: Judgment, Type: NWorkLate, Stream: "s1", Primaries: []string{"s1-1"}, Count: 1, Decisions: tc.decisions, At: t0}
			u := Unit{Key: n.ID, Closes: []Open{{Key: OpenKey(n.ID, "s1-1"), Note: n}}}
			why := silenceRefusal(nil, u, "s1-1")
			for _, want := range tc.want {
				assert.Contains(t, why, want)
			}
			for _, absent := range tc.absent {
				assert.NotContains(t, why, absent)
			}
		})
	}
}

// TestStepsAckCoverWait pins Wait: a condition the tick keeps is closed and
// held until the time, and one the tick does not keep is refused.
func TestStepsAckCoverWait(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		typ    string
		refuse string
	}{
		{name: "a kept condition is held until the time", typ: NWorkLate},
		{name: "a condition the tick does not keep is refused", typ: NReadBroken, refuse: "not a condition the tick keeps"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := setup(t, 1)
			n := Note{ID: "n1", Kind: Judgment, Type: tc.typ, Stream: "s1", Primaries: []string{"s1-1"}, Count: 1,
				Decisions: append([]string(nil), Decisions[tc.typ]...), At: w.s.Now}
			w.s.Open = append(w.s.Open, Open{Key: OpenKey(n.ID, "s1-1"), Note: n})
			until := w.s.Now.Add(time.Hour)
			p := Wait(w.s, WaitReq{Note: n.ID, Until: until, Who: "coordinator"})
			if tc.refuse != "" {
				require.Len(t, p.Refused, 1, "wait: %+v", p)
				assert.Contains(t, p.Refused[0].Why, tc.refuse)
				return
			}
			require.Empty(t, p.Refused, "wait: %+v", p)
			w.do(p)
			require.Len(t, p.Units, 1, "wait: %+v", p)
			assert.Contains(t, p.Units[0].Moved, "held until")
			require.Len(t, p.Units[0].Notes, 2, "wait: %+v", p.Units[0].Notes)
			assert.Equal(t, until, p.Units[0].Notes[1].Review, "the hold does not carry the review time")
			assert.Empty(t, w.openOn("s1-1"), "wait left the judgment open")
		})
	}
}

// TestStepsAckCoverWaitStale pins waitStale through Wait: a stale judgment of
// this epoch quiets its stream until the time; one of another epoch, and one
// of no stream, are refused.
func TestStepsAckCoverWaitStale(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		note   func(*Snapshot) string
		refuse string
	}{
		{name: "a stale judgment quiets its stream", note: func(s *Snapshot) string { return StaleGroupID("s1", s.Epoch) }},
		{name: "a stale judgment of another epoch is refused", note: func(s *Snapshot) string { return StaleGroupID("s1", s.Epoch+3) }, refuse: "belongs to epoch"},
		{name: "a stale judgment of no stream is refused", note: func(s *Snapshot) string { return StaleGroupID("s9", s.Epoch) }, refuse: "no stream s9"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := setup(t, 1)
			until := w.s.Now.Add(time.Hour)
			p := Wait(w.s, WaitReq{Note: tc.note(w.s), Until: until, Who: "coordinator"})
			if tc.refuse != "" {
				require.Len(t, p.Refused, 1, "waitStale: %+v", p)
				assert.Contains(t, p.Refused[0].Why, tc.refuse)
				return
			}
			require.Empty(t, p.Refused, "waitStale: %+v", p)
			require.Len(t, p.Units, 1, "waitStale: %+v", p)
			assert.Equal(t, CtlID("s1"), p.Units[0].Key, "waitStale: %+v", p.Units[0])
			assert.Contains(t, p.Units[0].Moved, "not shown stale until")
			w.do(p)
			assert.Equal(t, until.UTC().Format(time.RFC3339), w.s.StreamCtl("s1").F(FieldStaleReview), "the stream's stale review time is not set")
		})
	}
}
