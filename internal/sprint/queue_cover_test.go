package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queue_cover_test.go covers the work table's queue (queue.go, tla/DirtyTick.tla):
// what a step queues (QueueOf, queuedChanges), what the pump's drain does with it
// (Drain, composeQueued, WithQueue), and what the queue holds a pump part back
// for (QueuedCards, LeaveQueued). Every case is in memory: the queue is the
// core's own, so no store, clock, subprocess or socket is reached.

// queueWorld is a snapshot whose work table holds the primary c1 at s1:review,
// rev 4, with the property deal_index at 7: the state the queue's changes are
// planned against (the store plans every step but the pump's on WithQueue).
func queueWorld() *Snapshot {
	s := &Snapshot{Now: t0, Work: NewTable(Work)}
	s.Work.SetRows([]string{"s1"})
	s.Work.Put(&Card{ID: "c1", Row: "s1", Col: Review, Rev: 4, Fields: map[string]string{"work": "c1.w1"}})
	s.Work.SetProp("deal_index", "7")
	s.Merge = NewTable(Merge)
	return s
}

// qEntry is a queued change's entry as QueueOf carries it: the batch entry by
// pointer.
func qEntry(e ntable.BatchMemberEntry) *ntable.BatchMemberEntry { return &e }

func TestQueueCoverQueueOfQueuesTheWorkTableAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		p          Plan
		keepTables []string // the tables the kept units' changes name, in order
		wantQueued int      // work changes and properties the queue holds
	}{
		{
			name: "a work change queues and the other tables apply now",
			p: Plan{Units: []Unit{{Key: "c1", Moved: "took c1", Changes: []Change{
				change(Work, moveEntry(queueWorld().Work.Card("c1"), "s1", Working, map[string]string{"member": "m1"})),
				change(Fleet, createEntry("c1.w1", "m1", Ready, 1, nil)),
			}}},
				Props: []PropWrite{{Table: Work, Name: "deal_index", Value: "8"}, {Table: Merge, Name: "cur", Value: "x"}}},
			keepTables: []string{Fleet},
			wantQueued: 2, // the change and the work property
		},
		{
			name: "a guard-only entry of the work table is dropped and the unit keeps its notes",
			p: Plan{Units: []Unit{{Key: "c1",
				Changes: []Change{change(Work, guardEntry(queueWorld().Work.Card("c1")))},
				Notes:   []Note{{Kind: Happened, Type: NWorkOK}}}}},
			keepTables: nil,
			wantQueued: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, q := QueueOf(tc.p, "take", "m1")
			var tables []string
			for _, u := range got.Units {
				for _, c := range u.Changes {
					tables = append(tables, c.Table)
				}
				for _, pw := range got.Props {
					assert.NotEqual(t, Work, pw.Table, "a work property stayed unqueued: %+v", pw)
				}
			}
			assert.Equal(t, tc.keepTables, tables, "the plan keeps: %v", tables)
			assert.Len(t, q, tc.wantQueued, "the queue holds: %+v", q)
			for _, w := range q {
				assert.Equal(t, "take", w.Verb, "queue: %+v", w)
				assert.Equal(t, "m1", w.Actor, "queue: %+v", w)
			}
		})
	}
	// One plan, split in the queue's order: the work changes with the unit's
	// words, the guard dropped, the unit left with none keeping its notes.
	s := queueWorld()
	p := Plan{Units: []Unit{{Key: "c1", Moved: "took c1", Notes: []Note{{Kind: Happened, Type: NWorkOK}}, Changes: []Change{
		change(Work, setEntry(s.Work.Card("c1"), map[string]string{"ci": "green"})),
		change(Work, guardEntry(s.Work.Card("c1"))),
	}}}}
	got, q := QueueOf(p, "grade", "m1")
	require.Len(t, q, 1, "the guard-only entry queues nothing: %+v", q)
	require.NotNil(t, q[0].Entry)
	assert.Equal(t, "c1", q[0].Entry.ID, "queued: %+v", q[0])
	assert.Equal(t, "took c1", q[0].Moved, "queued: %+v", q[0])
	require.Len(t, got.Units, 1, "the unit left with no change closes: %+v", got.Units)
	assert.Empty(t, got.Units[0].Changes, "the unit still holds its changes: %+v", got.Units[0])
	assert.Len(t, got.Units[0].Notes, 1, "the unit lost its notes: %+v", got.Units[0])
}

func TestQueueCoverQueuedChangesIsAnyWriteOfTheEntry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		e     ntable.BatchMemberEntry
		queue bool
	}{
		{"a guard alone queues nothing", ntable.BatchMemberEntry{ID: "c1", Expect: &ntable.MemberExpect{Revision: "4"}}, false},
		{"a create queues", ntable.BatchMemberEntry{ID: "c1", Create: &ntable.MemberCreateOp{Row: "s1", Col: Ready}}, true},
		{"a move queues", ntable.BatchMemberEntry{ID: "c1", Move: &ntable.MemberMoveOp{Row: "s1", Col: Working}}, true},
		{"a removal queues", ntable.BatchMemberEntry{ID: "c1", Remove: true}, true},
		{"a set queues", ntable.BatchMemberEntry{ID: "c1", Set: map[string]string{"ci": "green"}}, true},
		{"an unset queues", ntable.BatchMemberEntry{ID: "c1", Unset: []string{"ci"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.queue, queuedChanges(tc.e), "queuedChanges(%+v)", tc.e)
		})
	}
}

func TestQueueCoverDrainComposesOneCardAndRefusesWhatDoesNotLineUp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		q       []QueuedChange
		units   int
		requeue int
		wantWhy string // "" is no refusal
	}{
		{
			name: "two changes of one card compose into one unit",
			q: []QueuedChange{
				{Entry: qEntry(setEntry(queueWorld().Work.Card("c1"), map[string]string{"ci": "green"})), Verb: "grade", Actor: "m1"},
				{Entry: qEntry(moveEntry(queueWorld().Work.Card("c1"), "s1", Merging, nil)), Verb: "accept", Actor: "machine"},
			},
			units: 1,
		},
		{
			name: "a change expecting another place is refused, named",
			q: []QueuedChange{{Entry: &ntable.BatchMemberEntry{ID: "c1",
				Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: "s1", Col: Ready}},
				Move:   &ntable.MemberMoveOp{Row: "s1", Col: Working}}, Verb: "take", Actor: "m1"}},
			units:   0,
			wantWhy: "it expects the card at s1:ready and the queue leaves it at s1:review",
		},
		{
			name:    "a change of a card that is not on the table is refused",
			q:       []QueuedChange{{Entry: &ntable.BatchMemberEntry{ID: "gone", Move: &ntable.MemberMoveOp{Row: "s1", Col: Working}}, Verb: "take"}},
			units:   0,
			wantWhy: "the card is not on the table",
		},
		{
			name:    "a create of a card that is already there is refused",
			q:       []QueuedChange{{Entry: qEntry(createEntry("c1", "s1", Waiting, 0, nil)), Verb: "add"}},
			units:   0,
			wantWhy: "it creates the card and the card is there already",
		},
		{
			name: "a card created and taken off again requeues the removal",
			q: []QueuedChange{
				{Entry: qEntry(createEntry("c2", "s1", Ready, 1, nil)), Verb: "add"},
				{Entry: &ntable.BatchMemberEntry{ID: "c2", Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: "s1", Col: Ready}}, Remove: true}, Verb: "drop"},
			},
			units:   1,
			requeue: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := queueWorld()
			p := Drain(s, tc.q, "machine")
			assert.Len(t, p.Units, tc.units, "units: %+v", p.Units)
			assert.Len(t, p.Requeue, tc.requeue, "requeue: %+v", p.Requeue)
			if tc.wantWhy == "" {
				require.Empty(t, p.Refused, "refused: %+v", p.Refused)
				return
			}
			require.Len(t, p.Refused, 1, "refused: %+v", p.Refused)
			assert.Contains(t, p.Refused[0].Why, tc.wantWhy, "why: %s", p.Refused[0].Why)
			require.Len(t, p.Notes, 1, "the refusal raises no invariant judgment: %+v", p.Notes)
			assert.Equal(t, NInvariant, p.Notes[0].Type, "note: %+v", p.Notes[0])
			assert.Equal(t, "machine", p.Notes[0].Who, "note: %+v", p.Notes[0])
			assert.Equal(t, TickDecisions[NInvariant], p.Notes[0].Decisions, "note: %+v", p.Notes[0])
		})
	}
	// The composed unit: one entry carrying both writes, guarded at the place
	// the card stands, its line naming every queued change behind it; a
	// property keeping its last value, guarded on what the table holds.
	s := queueWorld()
	p := Drain(s, []QueuedChange{
		{Entry: qEntry(setEntry(s.Work.Card("c1"), map[string]string{"ci": "green"})), Verb: "grade", Actor: "m1"},
		{Entry: qEntry(moveEntry(s.Work.Card("c1"), "s1", Merging, nil)), Verb: "accept", Actor: "machine"},
		{Prop: &PropWrite{Table: Work, Name: "deal_index", Value: "8"}, Verb: "deal"},
		{Prop: &PropWrite{Table: Work, Name: "deal_index", Value: "9"}, Verb: "deal"},
	}, "machine")
	require.Len(t, p.Units, 1, "units: %+v", p.Units)
	u := p.Units[0]
	assert.Equal(t, "c1", u.Key, "unit: %+v", u)
	assert.Equal(t, "s1", u.Stream, "unit: %+v", u)
	assert.Equal(t, "grade (grade by m1); accept (accept by machine)", u.Moved, "unit: %+v", u)
	require.Len(t, u.Changes, 1, "unit: %+v", u.Changes)
	e := u.Changes[0].Entry
	assert.Equal(t, "green", e.Set["ci"], "entry: %+v", e)
	require.NotNil(t, e.Move, "entry: %+v", e)
	assert.Equal(t, Merging, e.Move.Col, "entry: %+v", e)
	assert.Equal(t, at(s.Work.Card("c1")), e.Expect, "the drain guards at the place the card stands: %+v", e)
	require.Len(t, p.Props, 1, "a property keeps its last value: %+v", p.Props)
	assert.Equal(t, "9", p.Props[0].Value, "props: %+v", p.Props)
	assert.Equal(t, "7", p.Props[0].Was, "props: %+v", p.Props)
	assert.False(t, p.Props[0].WasAbsent, "props: %+v", p.Props)

	// Two refused cards are one invariant judgment, naming the first refusal's
	// reason and counting the rest.
	p = Drain(queueWorld(), []QueuedChange{
		{Entry: &ntable.BatchMemberEntry{ID: "gone", Move: &ntable.MemberMoveOp{Row: "s1", Col: Working}}, Verb: "take"},
		{Entry: &ntable.BatchMemberEntry{ID: "also-gone", Move: &ntable.MemberMoveOp{Row: "s1", Col: Working}}, Verb: "take"},
	}, "machine")
	require.Len(t, p.Refused, 2, "refused: %+v", p.Refused)
	require.Len(t, p.Notes, 1, "the refusals raise one judgment: %+v", p.Notes)
	assert.Contains(t, p.Notes[0].What, "; and 1 more", "what: %s", p.Notes[0].What)
	assert.Equal(t, []string{"also-gone", "gone"}, p.Notes[0].Primaries, "the judgment names both cards, sorted: %+v", p.Notes[0])
}

func TestQueueCoverComposeQueuedTakesTheLaterChangeAndRefusesTheImpossible(t *testing.T) {
	t.Parallel()
	scored, kept := 5.0, 2.0
	cases := []struct {
		name      string
		a, b      ntable.BatchMemberEntry
		wantWhy   string // "" is composed
		wantCol   string // of the composed entry's create or move
		wantSet   map[string]string
		wantUnset []string
		wantFold  bool // the removal folded the move away
	}{
		{
			name:    "a move moves the create it follows, keeping its score",
			a:       ntable.BatchMemberEntry{ID: "c2", Create: &ntable.MemberCreateOp{Row: "s1", Col: Ready, Score: kept}},
			b:       ntable.BatchMemberEntry{ID: "c2", Move: &ntable.MemberMoveOp{Row: "s1", Col: Working}},
			wantCol: Working,
		},
		{
			name:    "a later move keeps the score the earlier one set",
			a:       ntable.BatchMemberEntry{ID: "c1", Move: &ntable.MemberMoveOp{Row: "s1", Col: Review, Score: &scored}},
			b:       ntable.BatchMemberEntry{ID: "c1", Move: &ntable.MemberMoveOp{Row: "s1", Col: Merging}},
			wantCol: Merging,
		},
		{
			name:      "fields compose: the later value wins, an unset clears, a re-set unsets",
			a:         ntable.BatchMemberEntry{ID: "c1", Set: map[string]string{"x": "1", "y": "2"}, Unset: []string{"z"}},
			b:         ntable.BatchMemberEntry{ID: "c1", Set: map[string]string{"y": "3", "z": "9"}, Unset: []string{"x"}},
			wantSet:   map[string]string{"y": "3", "z": "9"},
			wantUnset: []string{"x"},
		},
		{
			name:     "a removal folds the move away",
			a:        ntable.BatchMemberEntry{ID: "c1", Move: &ntable.MemberMoveOp{Row: "s1", Col: Landed}},
			b:        ntable.BatchMemberEntry{ID: "c1", Remove: true},
			wantFold: true,
		},
		{
			name:    "a card taken off the table cannot take a later change",
			a:       ntable.BatchMemberEntry{ID: "c1", Remove: true},
			b:       ntable.BatchMemberEntry{ID: "c1", Set: map[string]string{"x": "1"}},
			wantWhy: "it was taken off the table by an earlier queued change",
		},
		{
			name:    "a card cannot be created twice",
			a:       ntable.BatchMemberEntry{ID: "c2", Create: &ntable.MemberCreateOp{Row: "s1", Col: Ready}},
			b:       ntable.BatchMemberEntry{ID: "c2", Create: &ntable.MemberCreateOp{Row: "s1", Col: Working}},
			wantWhy: "it is created twice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, why := composeQueued(tc.a, tc.b)
			if tc.wantWhy != "" {
				assert.Equal(t, tc.wantWhy, why, "composed: %+v", out)
				assert.Equal(t, tc.a, out, "a refusal returns the first entry unchanged: %+v", out)
				return
			}
			require.Empty(t, why, "composed: %+v", out)
			switch {
			case tc.wantFold:
				assert.True(t, out.Remove, "composed: %+v", out)
				assert.Nil(t, out.Move, "a removal folds the move away: %+v", out)
			case out.Create != nil:
				assert.Equal(t, tc.wantCol, out.Create.Col, "composed: %+v", out)
				assert.InDelta(t, kept, out.Create.Score, 1e-9, "the create keeps its score: %+v", out)
			case out.Move != nil:
				assert.Equal(t, tc.wantCol, out.Move.Col, "composed: %+v", out)
				if tc.a.Move != nil && tc.a.Move.Score != nil {
					require.NotNil(t, out.Move.Score, "the later move takes the score the card has: %+v", out)
					assert.Equal(t, scored, *out.Move.Score, "composed: %+v", out)
				}
			}
			if tc.wantSet != nil {
				assert.Equal(t, tc.wantSet, out.Set, "composed: %+v", out)
			}
			assert.Equal(t, tc.wantUnset, out.Unset, "composed: %+v", out)
		})
	}
}

func TestQueueCoverWithQueueIsTheSnapshotTheDrainLeaves(t *testing.T) {
	t.Parallel()
	s := queueWorld()
	assert.Same(t, s, WithQueue(s, nil), "an empty queue is the same snapshot")
	graded := setEntry(s.Work.Card("c1"), map[string]string{"ci": "green"})
	cases := []struct {
		name string
		q    []QueuedChange
		id   string // the card the case reads back
		col  string
		ci   string
		rev  uint64
	}{
		{
			name: "every applied change shows: the field, the move, one revision each",
			q: []QueuedChange{
				{Entry: &graded, Verb: "grade", Actor: "m1"},
				{Entry: qEntry(moveEntry(s.Work.Card("c1"), "s1", Merging, nil)), Verb: "accept", Actor: "machine"},
				{Prop: &PropWrite{Table: Work, Name: "deal_index", Value: "9"}, Verb: "deal"},
			},
			id: "c1", col: Merging, ci: "green", rev: 5,
		},
		{
			name: "a change the drain refuses is left out, as the drain leaves it out",
			q: []QueuedChange{
				{Entry: &ntable.BatchMemberEntry{ID: "c1", Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: "s1", Col: Ready}}, Move: &ntable.MemberMoveOp{Row: "s1", Col: Working}}, Verb: "take"},
			},
			id: "c1", col: Review, ci: "", rev: 4,
		},
		{
			name: "a create and its requeued removal apply over two passes",
			q: []QueuedChange{
				{Entry: qEntry(createEntry("c2", "s1", Ready, 1, nil)), Verb: "add"},
				{Entry: &ntable.BatchMemberEntry{ID: "c2", Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: "s1", Col: Ready}}, Remove: true}, Verb: "drop"},
			},
			id: "c2", col: "", ci: "", rev: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s2 := WithQueue(s, tc.q)
			require.NotSame(t, s, s2, "the queue was applied to the snapshot it read")
			c := s2.Work.Card(tc.id)
			require.NotNil(t, c, "the card is gone from the queue's snapshot")
			assert.Equal(t, tc.col, c.Col, "card: %+v", c)
			assert.Equal(t, tc.ci, c.F("ci"), "card: %+v", c)
			assert.Equal(t, tc.rev, c.Rev, "card: %+v", c)
			assert.Equal(t, Review, s.Work.Card("c1").Col, "the snapshot the queue was read from stands as it was")
			was, had := s.Work.Prop("deal_index")
			require.True(t, had, "the pre-state lost its property")
			assert.Equal(t, "7", was, "the pre-state's properties stand as they were")
			if tc.q[len(tc.q)-1].Prop != nil {
				v, ok := s2.Work.Prop("deal_index")
				require.True(t, ok, "the property write was lost")
				assert.Equal(t, "9", v, "properties: %v", s2.Work.Props())
			}
			assert.Nil(t, s2.Queue, "the drained snapshot keeps no queue")
			assert.Zero(t, s2.QueueLen, "the dirty bit of the drained snapshot is its queue's length")
			assert.Same(t, s.Merge, s2.Merge, "the other tables are shared, not copied")
		})
	}
}

func TestQueueCoverQueuedCardsNamesOnlyTheEntryCards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		q    []QueuedChange
		want []string
	}{
		{"an empty queue names no card", nil, nil},
		{"entries name their cards, properties name none", []QueuedChange{
			{Entry: &ntable.BatchMemberEntry{ID: "c1"}, Verb: "grade"},
			{Prop: &PropWrite{Table: Work, Name: "deal_index", Value: "9"}, Verb: "deal"},
			{Entry: &ntable.BatchMemberEntry{ID: "c2", Remove: true}, Verb: "drop"},
		}, []string{"c1", "c2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := QueuedCards(tc.q)
			if tc.want == nil {
				assert.Empty(t, got, "cards: %v", got)
				return
			}
			require.Len(t, got, len(tc.want), "cards: %v", got)
			for _, id := range tc.want {
				assert.True(t, got[id], "cards: %v", got)
			}
		})
	}
}

func TestQueueCoverLeaveQueuedHoldsEveryUnitNamingAQueuedCard(t *testing.T) {
	t.Parallel()
	workC1 := change(Work, ntable.BatchMemberEntry{ID: "c1", Remove: true})
	fleetC1 := change(Fleet, ntable.BatchMemberEntry{ID: "c1", Move: &ntable.MemberMoveOp{Row: "m1", Col: Working}})
	fleetF2 := change(Fleet, ntable.BatchMemberEntry{ID: "f2"})
	cases := []struct {
		name   string
		p      Plan
		held   map[string]bool
		keep   []string // unit keys, in order
		wantId string   // the refused unit's key, "" is no refusal
	}{
		{
			name: "nothing held: the pump part keeps every unit",
			p:    Plan{Units: []Unit{{Key: "take", Changes: []Change{workC1}}, {Key: "beat", Changes: []Change{fleetF2}}}},
			held: map[string]bool{},
			keep: []string{"take", "beat"},
		},
		{
			name: "a work change of a held card waits; other tables and unheld cards move",
			p: Plan{Units: []Unit{{Key: "take", Changes: []Change{workC1}}, {Key: "grade", Changes: []Change{fleetC1}}, {Key: "beat", Changes: []Change{fleetF2}}},
				Props: []PropWrite{{Table: Fleet, Name: "cur", Value: "x"}}},
			held:   map[string]bool{"c1": true},
			keep:   []string{"grade", "beat"},
			wantId: "take",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := LeaveQueued(tc.p, tc.held)
			var keys []string
			for _, u := range got.Units {
				keys = append(keys, u.Key)
			}
			assert.Equal(t, tc.keep, keys, "units kept: %+v", got.Units)
			if tc.wantId == "" {
				require.Empty(t, got.Refused, "refused: %+v", got.Refused)
				return
			}
			require.Len(t, got.Refused, 1, "refused: %+v", got.Refused)
			assert.Equal(t, tc.wantId, got.Refused[0].Key, "refused: %+v", got.Refused)
			assert.Contains(t, got.Refused[0].Why, "c1 has a change queued after this tick's drain", "why: %s", got.Refused[0].Why)
			assert.Equal(t, tc.p.Props, got.Props, "a plan with no round records keeps its properties: %+v", got.Props)
		})
	}
}
