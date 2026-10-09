package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// priorityDealWith retains the live roster used by the subsequent actual Take.
func priorityDealWith(w *world, seats ...FriendSeat) Plan {
	w.s.Friends = seats
	return dealWith(w, seats...)
}

// PriorityFlow.tla Admission: a changed producer level follows its already-dealt
// consumer to the next free slot; the currently running lease stays in place.
func TestQueuedWorkPriorityChangesTheNextTake(t *testing.T) {
	t.Parallel()
	for _, level := range []string{PriorityBlocker, PriorityCritical, PriorityFix, PriorityHigh} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
			seat := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
			priorityDealWith(w, seat)
			row := FriendRow(seat.Name)
			require.Len(t, w.s.Fleet.Cell(row, Ready), 2)
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: level, Reason: "the next slot is urgent"}))
			p := Take(w.s, TakeReq{As: row})
			require.Len(t, p.Units, 1)
			assert.Equal(t, "s1-2.w1", p.Units[0].Key, "the later urgent consumer takes the free lane")
			w.must(p)
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityBlocker, Reason: "another blocker"}))
			assert.Empty(t, Take(w.s, TakeReq{As: row}).Units, "priority never preempts an active lease")
			assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)
		})
	}
}

func TestQueuedReadAndWorkCompeteByInheritedLevel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, work, read, want string }{
		{"blocker work before normal read", PriorityBlocker, PriorityNormal, "s1-1.w1"},
		{"fix work before high read", PriorityFix, PriorityHigh, "s1-1.w1"},
		{"high work before urgent read role", PriorityHigh, PriorityHigh, "s1-1.w1"},
		{"reader floor above normal work", PriorityNormal, PriorityLow, ReadCardID("s2-1", 1, "amy")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := friendWorld(t, friendBrief("only friend amy"))
			seat := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro", Mode: config.FriendModeOneShot}
			priorityDealWith(w, seat)
			w.s.Work.SetProp(PropReadCards, ReadCardsOnWord)
			row := FriendRow(seat.Name)
			pr := &Card{ID: "s2-1", Row: "s2", Col: Review, Score: 0, Fields: map[string]string{"kind": "primary", "attempt": "1"}}
			w.s.Work.SetRows(append(w.s.Work.Rows(), "s2"))
			w.s.Work.Put(pr)
			read := ReadCardID(pr.ID, 1, seat.Name)
			w.s.Fleet.Put(&Card{ID: read, Row: row, Col: Ready, Score: 0, Fields: map[string]string{
				"kind": "read", PrimaryField: pr.ID, "stream": pr.Row, "gen": "1", FieldPriority: PriorityReader}})
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: tc.work, Reason: "work level"}))
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{pr.ID}, Level: tc.read, Reason: "read producer level"}))
			p := Take(w.s, TakeReq{As: row})
			require.Len(t, p.Units, 1)
			assert.Equal(t, tc.want, p.Units[0].Key)
		})
	}
}

func TestDemotedQueuedWorkKeepsStableTiesAndFinishOrder(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"), friendBrief("only friend amy"))
	seat := FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"}
	priorityDealWith(w, seat)
	row := FriendRow(seat.Name)
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-3"}, Level: PriorityBlocker, Reason: "urgent"}))
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-3"}, Level: PriorityNormal, Reason: "urgency ended"}))
	p := Take(w.s, TakeReq{As: row, Sel: Sel{Limit: 1}})
	require.Len(t, p.Units, 1)
	assert.Equal(t, "s1-1.w1", p.Units[0].Key, "demotion restores the stable work order")
	w.must(p)
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-3"}, Level: PriorityCritical, Reason: "release dependency"}))
	w.must(Finish(w.s, FinishReq{As: row, Sel: Sel{IDs: []string{"s1-1.w1"}}, Gens: gensOf(w.s, "s1-1.w1"), Head: "head", Report: "done"}))
	assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col, "finish promotes the urgent queued card")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-2.w1").Col)
}

func TestRedealInheritsPriorityChangedWhileWithdrawn(t *testing.T) {
	t.Parallel()
	for _, level := range []string{PriorityBlocker, PriorityNormal} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			w := friendWorld(t, friendBrief("friend"))
			seat := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityHigh, Reason: "original level"}))
			priorityDealWith(w, seat)
			wc := w.s.Fleet.Card("s1-1.w1")
			require.NotNil(t, wc)
			w.must(FriendTake(w.s, FriendTakeReq{Friend: seat.Name, IDs: []string{wc.ID}, Reason: "another seat"}))
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: level, Reason: "changed before redeal"}))
			priorityDealWith(w, FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "flash,pro"})
			wc = w.s.Fleet.Card(wc.ID)
			assert.Equal(t, FriendRow("bob"), wc.Row)
			assert.Equal(t, level, QueuePriority(wc), "the reused consumer inherits the current producer level")
		})
	}
}

func TestComputedCriticalDoesNotOrderAWorkConsumer(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
	w.s.Work.Card("s1-2").Fields[FieldBehind] = "12"
	priorityDealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"})
	assert.Equal(t, PriorityNormal, QueuePriority(w.s.Fleet.Card("s1-2.w1")), "computed critical remains display-only on the producer")
	p := Take(w.s, TakeReq{As: FriendRow("amy")})
	require.Len(t, p.Units, 1)
	assert.Equal(t, "s1-1.w1", p.Units[0].Key)
}

func TestUrgentPriorityPreservesDependencyEligibility(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-1", Brief: friendBrief("only friend amy")},
		{ID: "s1-2", Brief: friendBrief("only friend amy"), Needs: []string{"s1-1"}},
	}}))
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityBlocker, Reason: "urgent dependent"}))
	priorityDealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"})
	assert.Equal(t, Waiting, w.s.Work.Card("s1-2").Col)
	assert.Nil(t, w.s.Fleet.Card("s1-2.w1"), "a blocker cannot bypass its unfinished dependency")
	p := Take(w.s, TakeReq{As: FriendRow("amy")})
	require.Len(t, p.Units, 1)
	assert.Equal(t, "s1-1.w1", p.Units[0].Key)
}

func TestRepeatedPriorityRepairsAnOlderQueuedCopy(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
	priorityDealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"})
	// A primary set by the older implementation has a queued normal consumer.
	w.s.Work.Card("s1-2").Fields[FieldPriority] = PriorityBlocker
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityBlocker, Reason: "retain the level"}))
	p := Take(w.s, TakeReq{As: FriendRow("amy")})
	require.Len(t, p.Units, 1)
	assert.Equal(t, "s1-2.w1", p.Units[0].Key, "an idempotent request repairs inheritance too")
}

func TestUnstartedFriendLeaseReentersWithCurrentPriority(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
	seat := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
	priorityDealWith(w, seat)
	row := FriendRow(seat.Name)
	w.must(Take(w.s, TakeReq{As: row}))
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityHigh, Reason: "queued work"}))
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityBlocker, Reason: "active producer changed"}))
	assert.Equal(t, PriorityNormal, QueuePriority(w.s.Fleet.Card("s1-1.w1")), "an active lease is preserved")
	w.must(Plan{Units: friendUnstartedWorking(w.s, []FriendSeat{seat})})
	p := Take(w.s, TakeReq{As: row})
	require.Len(t, p.Units, 1)
	assert.Equal(t, "s1-1.w1", p.Units[0].Key, "a lease returned to Ready re-inherits its producer level")
}

func TestReturnedReadReinheritsItsChangedProducer(t *testing.T) {
	t.Parallel()
	for _, level := range []string{PriorityBlocker, PriorityLow} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			w := setup(t, 1)
			finished(w, "s1-1", false)
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityHigh, Reason: "original level"}))
			w.must(Ask(w.s, AskReq{}))
			rc := w.s.Readers.Of("s1-1")[0]
			w.must(Read(w.s, ReadReq{As: rc.Row, Begin: true, Sel: Sel{IDs: []string{rc.ID}}}))
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: level, Reason: "changed while reading"}))
			assert.Equal(t, PriorityReader, QueuePriority(w.s.Readers.Card(rc.ID)), "the active read keeps its reader role")
			assert.Equal(t, PriorityHigh, w.s.Readers.Card(rc.ID).F(FieldProducerPriority), "the active lease's urgency metadata is preserved")
			w.must(Read(w.s, ReadReq{As: rc.Row, Return: true, Reason: "another pass", Sel: Sel{IDs: []string{rc.ID}}}))
			rc = w.s.Readers.Card(rc.ID)
			require.Equal(t, Asked, rc.Col)
			assert.Equal(t, PriorityReader, QueuePriority(rc))
			assert.Equal(t, level, rc.F(FieldProducerPriority), "a returned read observes its producer's changed urgency")
		})
	}
}

func TestFixRoleRetainsProducerUrgencyAcrossChangesAndDeals(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"))
	seat := FriendSeat{Name: "amy", Width: 1, Status: Up, Class: "flash,pro"}
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1"}, Level: PriorityHigh, Reason: "original urgency"}))
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-1", "s1-2"}, Level: PriorityFix, Reason: "repair phase"}))
	priorityDealWith(w, seat)
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"s1-2"}, Level: PriorityBlocker, Reason: "urgent repair producer"}))
	for _, id := range []string{"s1-1", "s1-2"} {
		require.Equal(t, PriorityFix, w.s.Work.Card(id).F(FieldPriority))
		require.Equal(t, PriorityFix, w.s.Fleet.Card(id+".w1").F(FieldPriority))
	}
	require.Equal(t, PriorityHigh, ProducerPriority(w.s.Fleet.Card("s1-1.w1")))
	require.Equal(t, PriorityBlocker, ProducerPriority(w.s.Fleet.Card("s1-2.w1")))
	p := Take(w.s, TakeReq{As: FriendRow(seat.Name)})
	require.Len(t, p.Units, 1)
	require.Equal(t, "s1-2.w1", p.Units[0].Key, "producer urgency orders repairs within FIX")
	w.must(SetPriority(w.s, PriorityReq{Stream: "s1", Level: PriorityLow, Reason: "ordinary stream default"}))
	require.Equal(t, PriorityLow, w.s.StreamPriority("s1"))
	require.Equal(t, PriorityFix, w.s.Fleet.Card("s1-1.w1").F(FieldPriority))
	require.Equal(t, PriorityLow, ProducerPriority(w.s.Fleet.Card("s1-1.w1")))
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "new", Brief: friendBrief("only friend amy")}}}))
	require.Equal(t, PriorityLow, w.s.Work.Card("new").F(FieldPriority), "future ordinary cards use the stream default")
}

func TestReadCardDealReservesUrgentWorkBeforeReaderAndNormalWork(t *testing.T) {
	t.Parallel()
	for _, level := range []string{PriorityFix, PriorityBlocker, PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		t.Run(level, func(t *testing.T) {
			t.Parallel()
			w := readCardsWorld(t, 1)
			seat := FriendSeat{Name: "amy", Width: 1, Status: Up, Mode: config.FriendModeOneShot,
				Class: "flash,pro", Tiers: []string{"flash", "pro"}, Roles: []string{"builder", RoleReader}}
			w.s.Friends = []FriendSeat{seat}
			w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), FriendRow(seat.Name)))
			w.must(Add(w.s, AddReq{Stream: "work", Cards: []CardAdd{{ID: "urgent", Brief: friendBrief("only friend amy")}}}))
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{"urgent"}, Level: level, Reason: "admission level"}))
			putReviewBy(w, "read", "read: work (s1) tier: flash\n", "zoe", 1)
			w.s.Work.Card("read").Fields[FieldPriority] = PriorityBlocker
			w.s.Work.SetRows([]string{"s1", "work"}) // putReviewBy installs its own review row
			dealReads(t, w, []FriendSeat{seat})
			if priorityRank(level) < priorityRank(PriorityReader) {
				require.NotNil(t, w.s.Fleet.Placed("urgent.w1"), "eligible urgent work gets the sole shared lane")
				require.Empty(t, readCardsOf(w, "read"))
			} else {
				require.Nil(t, w.s.Fleet.Placed("urgent.w1"))
				reads := readCardsOf(w, "read")
				require.Len(t, reads, 1, "READER gets the lane ahead of normal and low work")
				require.Equal(t, PriorityReader, reads[0].F(FieldPriority))
				require.Equal(t, PriorityBlocker, ProducerPriority(reads[0]))
			}
			work, reads := rowLoad(w.s, FriendRow(seat.Name))
			require.LessOrEqual(t, work+reads, 1, "one-shot work and reads share physical capacity")
		})
	}
}

func TestReadCardPriorityPassKeepsHeldPlacementsOutOfIndexes(t *testing.T) {
	t.Parallel()
	w := readCardsWorld(t, 2, "m1")
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{{ID: "urgent"}, {ID: "ordinary"}}}))
	w.must(SetPriority(w.s, PriorityReq{IDs: []string{"urgent"}, Level: PriorityHigh, Reason: "first pass"}))
	p, _ := TickDeal(w.s, TickReq{})
	require.NotEmpty(t, p.Units)
	kept := LeaveQueued(p, map[string]bool{"urgent": true, "ordinary": true})
	require.Empty(t, kept.Units, "both planned work placements wait for their queued updates")
	for _, prop := range kept.Props {
		require.NotContains(t, []string{PropStreamIndex, PropDealIndex}, prop.Name, "a held placement consumes no index turn")
	}
}

func TestReadCardPriorityPassSharesTheMachineDealBound(t *testing.T) {
	t.Parallel()
	for _, urgent := range []int{TickMaxDeal / 2, TickMaxDeal + 1} {
		t.Run(itoa(urgent), func(t *testing.T) {
			t.Parallel()
			w := readCardsWorld(t, MaxWidth, "m1", "m2")
			w.must(Add(w.s, AddReq{Stream: "s1", Count: TickMaxDeal + 1}))
			ready := w.s.Work.Column(Ready)
			for _, pr := range ready[:urgent] {
				pr.Fields[FieldPriority] = PriorityHigh
			}
			p, due := TickDeal(w.s, TickReq{})
			placements := 0
			for _, u := range p.Units {
				for _, ch := range u.Changes {
					if ch.Table == Fleet && ch.Entry.Create != nil && ch.Entry.Set["kind"] == "work" {
						placements++
					}
				}
			}
			require.Equal(t, TickMaxDeal, placements, "urgent and remaining work share one bounded deal")
			require.Equal(t, 1, due, "the remaining eligible card stays due, including all-urgent overflow")
		})
	}
}
