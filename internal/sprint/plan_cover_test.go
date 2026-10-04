package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The plan's post-passes and helpers (plan.go): Tables, otherEpochAnswer,
// Applied, OnePerCause and PlanRows read only their arguments and a Snapshot
// built by hand, so these tests build both by hand: no store, no clock, no
// subprocess, no sleep.

func TestPlanCoverTables(t *testing.T) {
	t.Parallel()
	full := Plan{
		Units: []Unit{{
			Key: "u", Stream: "s1",
			Changes: []Change{
				change(Merge, guardEntry(&Card{ID: "ctl-s1", Row: "s1", Col: Ctl, Rev: 1})),
				change(Work, setEntry(&Card{ID: "s1-2", Row: "s1", Col: Working, Rev: 1}, map[string]string{"head": "abc"})),
			},
			Bumps: []Bump{{Table: Fleet, ID: "m1", Field: DoneOK, Delta: 1}},
		}},
		Props: []PropWrite{{Table: Readers, Name: "hint", Value: "two reads"}},
	}
	twice := Plan{
		Units: []Unit{{
			Key: "u",
			Changes: []Change{
				change(Merge, guardEntry(&Card{ID: "ctl-s1", Row: "s1", Col: Ctl, Rev: 1})),
				change(Merge, setEntry(&Card{ID: "ctl-s1", Row: "s1", Col: Ctl, Rev: 1}, map[string]string{StateCol: StreamMerging})),
				change(Work, guardEntry(&Card{ID: "s1-2", Row: "s1", Col: Working, Rev: 1})),
			},
		}},
	}
	off := Plan{
		Units: []Unit{{
			Key:     "u",
			Changes: []Change{change(Friends, guardEntry(&Card{ID: "f1", Row: "f1", Rev: 1}))},
		}},
	}
	cases := []struct {
		name string
		p    Plan
		want []string
	}{
		{"the plan's tables come out in ApplyOrder, from changes, bumps and props", full, []string{Fleet, Readers, Merge, Work}},
		{"a table named twice is named once", twice, []string{Merge, Work}},
		{"a table outside ApplyOrder is not listed", off, nil},
		{"a plan that writes nothing names no table", Plan{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, c.p.Tables())
		})
	}
}

func TestPlanCoverOtherEpochAnswer(t *testing.T) {
	t.Parallel()
	cleared := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		s    *Snapshot
		id   string
		want string
	}{
		{"a later epoch is unknown to this sprint",
			&Snapshot{Epoch: 3}, "j~7",
			"judgment j~7 belongs to epoch 7, which is unknown to this sprint (its epoch is 3); nothing was changed; run: nova-sprint inbox"},
		{"an earlier epoch names when the sprint was cleared",
			&Snapshot{Epoch: 3, Cleared: cleared}, "j~2",
			"--answers j~2 names a judgment of epoch 2; the sprint was cleared at " + cleared.UTC().Format(time.RFC3339) + " and its epoch is now 3; the whole step is refused and nothing was changed; run: nova-sprint inbox"},
		{"an earlier epoch with no clear on record says an earlier one",
			&Snapshot{Epoch: 3}, "j~2",
			"--answers j~2 names a judgment of epoch 2; the sprint was cleared at an earlier clear and its epoch is now 3; the whole step is refused and nothing was changed; run: nova-sprint inbox"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, otherEpochAnswer(c.s, c.id, IDEpoch(c.id)))
		})
	}
}

func TestPlanCoverApplied(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	working := &Card{ID: "s1-1", Row: "s1", Col: Working, Rev: 1, Fields: map[string]string{}}
	t.Run("a lawful plan is applied with its judgments kept once per cause", func(t *testing.T) {
		t.Parallel()
		s := &Snapshot{Now: now, Epoch: 1, Open: []Open{
			{Key: "n1|s1-1", Note: Note{ID: "n1", Kind: Judgment, Type: NCIRed, Stream: "s1"}},
		}}
		p := Applied(s, Plan{
			Units: []Unit{{Key: "s1-1", Stream: "s1", Changes: []Change{change(Work, moveEntry(working, working.Row, Review, nil))}}},
			Notes: []Note{judgment(NCIRed, "s1", now, 0, "s1-1", "s1-2")},
		})
		require.Len(t, p.Units, 1, "Applied: %+v", p)
		assert.Equal(t, "s1-1", p.Units[0].Key, "Applied: %+v", p)
		assert.Empty(t, p.Refused, "Applied: %+v", p)
		require.Len(t, p.Notes, 1, "Applied: %+v", p)
		assert.Equal(t, []string{"s1-2"}, p.Notes[0].Primaries, "the cause already open on s1-1 leaves it out: %+v", p.Notes[0])
		assert.Equal(t, 1, p.Notes[0].Count, "the count follows the primaries kept: %+v", p.Notes[0])
	})
	t.Run("a move outside the lifecycle is refused and nothing is applied", func(t *testing.T) {
		t.Parallel()
		p := Applied(&Snapshot{Now: now, Epoch: 1}, Plan{
			Units: []Unit{{Key: "jump", Changes: []Change{change(Work, createEntry("jump", "s1", Review, 1, nil))}}},
		})
		assert.Empty(t, p.Units, "Applied: %+v", p)
		require.Len(t, p.Refused, 1, "Applied: %+v", p)
		assert.Equal(t, "jump", p.Refused[0].Key, "Applied: %+v", p)
		assert.Contains(t, p.Refused[0].Why, "the lifecycle admits a primary waiting or ready, not review", "Applied: %+v", p)
	})
}

func TestPlanCoverOnePerCause(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	streamLevel := judgment(NCIRed, "s1", now, 0, "a")
	streamLevel.StreamLevel = true
	open := func(typ string) []Open {
		return []Open{{Key: "n1|a", Note: Note{ID: "n1", Kind: Judgment, Type: typ, Stream: "s1"}}}
	}
	cases := []struct {
		name string
		s    *Snapshot
		p    Plan
		want []Note
	}{
		{"a subject whose cause is open is left out, and a note left no subject is not written",
			&Snapshot{Now: now, Epoch: 1, Open: open(NWorkFailed)},
			Plan{Notes: []Note{
				judgment(NWorkFailed, "s1", now, 0, "a", "b"),
				judgment(NWorkFailed, "s1", now, 0, "a"),
			}},
			[]Note{judgment(NWorkFailed, "s1", now, 0, "b")}},
		{"a judgment the plan closes keeps its subject",
			&Snapshot{Now: now, Epoch: 1, Open: open(NWorkFailed)},
			Plan{
				Closes: []Open{{Key: "n1|a"}},
				Notes:  []Note{judgment(NWorkFailed, "s1", now, 0, "a")},
			},
			[]Note{judgment(NWorkFailed, "s1", now, 0, "a")}},
		{"a stream-level judgment is written as it is",
			&Snapshot{Now: now, Epoch: 1, Open: open(NCIRed)},
			Plan{Notes: []Note{streamLevel}},
			[]Note{streamLevel}},
		{"a note that is no judgment is kept as it is",
			&Snapshot{Now: now, Epoch: 1, Open: open(NWorkOK)},
			Plan{Notes: []Note{happened(NWorkOK, "s1", now, "a")}},
			[]Note{happened(NWorkOK, "s1", now, "a")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, OnePerCause(c.s, c.p).Notes)
		})
	}
}

func TestPlanCoverPlanRows(t *testing.T) {
	t.Parallel()
	writes := Plan{
		Rows: []RowAdd{{Table: Merge, Row: "s2"}},
		Units: []Unit{{
			Key: "u", Stream: "s1",
			Changes: []Change{
				change(Work, moveEntry(&Card{ID: "s1-1", Row: "s1", Col: Working, Rev: 1}, "z9", Ready, nil)),
				change(Work, moveEntry(&Card{ID: "s1-2", Row: "s1", Col: Working, Rev: 1}, "z9", Review, nil)),
				change(Work, createEntry("s1-9", "a1", Waiting, 1, nil)),
				change(Readers, setEntry(&Card{ID: "s1-1.r1.reader-a", Row: "reader-a", Col: Asked, Rev: 2}, map[string]string{OK: "1"})),
				change(Merge, guardEntry(&Card{ID: "ctl-s1", Row: "s1", Col: Ctl, Rev: 1})),
			},
		}},
	}
	writesNothing := Plan{
		Rows: []RowAdd{{Table: Merge, Row: ""}},
		Units: []Unit{{
			Key:     "u",
			Changes: []Change{change(Merge, guardEntry(&Card{ID: "ctl-s1", Row: "s1", Col: Ctl, Rev: 1}))},
		}},
	}
	cases := []struct {
		name string
		p    Plan
		want map[string][]string
	}{
		{"the rows a plan writes, by table, named once and sorted", writes,
			map[string][]string{Work: {"a1", "s1", "z9"}, Readers: {"reader-a"}, Merge: {"s2"}}},
		{"a plan that writes nothing names no row, and a row of no name is skipped", writesNothing,
			map[string][]string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, PlanRows(c.p))
		})
	}
}
