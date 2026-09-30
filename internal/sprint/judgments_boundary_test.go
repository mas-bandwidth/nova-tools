package sprint

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests of what IT06's conditions do at their boundaries, and of a stopped
// stream after the coordinator has decided what to do with its cards: the
// states the first tests left unseeded (the cold read of the pull request).

// boundarySeeds are seeded states at the boundary of a condition: one step
// either side of what the row's words say.
var boundarySeeds = []seeded{
	// accept needs ok from two DIFFERENT readers at the primary's head, and the
	// primary in review (2.2).
	{"ci red, one ok read and one outstanding", "ci red on a primary", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok")
		return w, raise(w, "ci red on a primary", "s1-1", nil)
	}, []string{"rework --fix", "drop", "card", "ack"}},

	{"ci red, one ok read and one broken", "ci red on a primary", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "broken")
		return w, raise(w, "ci red on a primary", "s1-1", nil)
	}, []string{"rework --fix", "drop", "card", "ack"}},

	{"ci red, two ok reads, one of them at another head", "ci red on a primary", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "ok")
		readsAt(w.s, w.s.Work.Card("s1-1"), 1)[1].Fields["head"] = "moved"
		return w, raise(w, "ci red on a primary", "s1-1", nil)
	}, []string{"rework --fix", "drop", "card", "ack"}},

	{"returned to review, one ok read", "returned to review", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok")
		return w, raise(w, "returned to review", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"returned to review, two ok reads of an earlier attempt", "returned to review", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "ok")
		w.s.Work.Card("s1-1").Fields["attempt"] = "2"
		return w, raise(w, "returned to review", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	// ask is accepted for a primary with no read card at its attempt: one read
	// is a read.
	{"stranded, one read card at its attempt", "stranded in review", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1")
		retiredRead(w, "s1-1", readsAt(w.s, w.s.Work.Card("s1-1"), 1)[1].F("reader"))
		return w, raise(w, "stranded in review", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	// a reader with a card at the attempt, even a retired one, has read it
	// (Ask's own choice).
	{"reads exhausted, the reader left has a retired read", "reads exhausted", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "broken")
		w.s.Readers.Put(&Card{ID: ReadCardID("s1-1", 1, "reader-c"), Fields: map[string]string{
			"kind": "read", "primary": "s1-1", "reader": "reader-c", "attempt": "1", "retired": stamp(w.s.Now), "retired_by": "accept"}})
		return w, raise(w, "reads exhausted", "s1-1", nil)
	}, []string{"rework --fix", "drop"}},

	{"reads exhausted, fourteen read cards", "reads exhausted", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReview(w, "s1-1", false)
		verdicts(w, "s1-1", "ok", "broken")
		var cards []string
		for i := 1; i < maxReadCards; i++ {
			cards = append(cards, fmt.Sprintf("s1-1.r%d.reader-a", i))
		}
		w.s.Work.Card("s1-1").Fields["rcards"] = strings.Join(cards, ",")
		return w, raise(w, "reads exhausted", "s1-1", nil)
	}, []string{"ask --another", "rework --fix", "drop"}},

	// fleet down names the member only when it has held the card its own whole
	// deadline: from its own stamp, whatever the first take was.
	{"a work card is late, taken one second short of its own deadline", "a work card is past its deadline", lateTaken(DeadlineUnfinished - time.Second),
		[]string{"drop <p>", "wait"}},
	{"a work card is late, taken exactly its own deadline ago", "a work card is past its deadline", lateTaken(DeadlineUnfinished),
		[]string{"drop <p>", "wait"}},
	{"a work card is late, taken one second over its own deadline", "a work card is past its deadline", lateTaken(DeadlineUnfinished + time.Second),
		[]string{"fleet down <member>", "drop <p>", "wait"}},
	{"a work card is late, dealt one second short of its own deadline", "a work card is past its deadline", lateDealt(DeadlineUntaken - time.Second),
		[]string{"drop <p>", "wait"}},
	{"a work card is late, dealt one second over its own deadline", "a work card is past its deadline", lateDealt(DeadlineUntaken + time.Second),
		[]string{"fleet down <member>", "drop <p>", "wait"}},

	// a card is at its redeal bound at the bound, not before.
	{"a card reached its bound, one redeal short", "a card reached its bound", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
		w.s.Fleet.Card("s1-1.w1").Fields["redeals"] = itoa(MaxRedeals - 1)
		return w, raise(w, "a card reached its bound", "s1-1", nil)
	}, []string{"drop", "wait"}},

	// a waiter is blocked while any need it names is missing and not waived.
	{"blocked on something missing, one of two needs waived", "a primary is blocked on something missing", func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"later"}, Needs: []string{"s1-1"}}))
		w.s.Work.Card("later").Fields["needs"] = "ghost,ghost2"
		w.s.Work.Card("later").Fields["waived"] = "ghost"
		return w, raise(w, "a primary is blocked on something missing", "later", func(n *Note) { n.Needs, n.Primaries = []string{"ghost", "ghost2"}, []string{"later"} })
	}, []string{"add <n>", "ack", "drop w"}},

	// merge is accepted for a stream that merges, or waits with a card queued.
	{"no merge step past its deadline, waiting with a card queued", "a stream has had no merge step past its deadline", func(t *testing.T) (*world, Open) {
		w := setup(t, 2)
		accepted(w, "s1-1")
		w.s.StreamCtl("s1").Fields["state"] = StreamWaiting
		return w, raise(w, "a stream has had no merge step past its deadline", StreamSubject("s1"), judgedStream("s1", ""))
	}, []string{"merge --stream s", "card", "wait"}},
}

// allSeeds are every seeded state: the states each type is raised on, the
// states where a condition does not hold, and the boundaries.
func allSeeds() []seeded { return slices.Concat(seeds, conditionSeeds, boundarySeeds) }

// lateTaken is a work card taken by an up member an age ago, and first taken
// long before it (a redeal since): late from the first take, and held by its
// member the age given.
func lateTaken(age time.Duration) func(t *testing.T) (*world, Open) {
	return func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		toReviewTaken(w, "s1-1")
		w.tick(6 * time.Hour)
		c := w.s.Fleet.Card("s1-1.w1")
		c.Fields["first_taken"] = stamp(w.s.Now.Add(-5 * time.Hour))
		c.Fields["taken"] = stamp(w.s.Now.Add(-age))
		return w, raise(w, "a work card is past its deadline", "s1-1.w1", func(n *Note) { n.Card, n.Primaries = "s1-1.w1", []string{"s1-1"} })
	}
}

// lateDealt is a work card dealt to an up member an age ago, and first dealt
// long before it: late from the first deal, and held by its member the age
// given.
func lateDealt(age time.Duration) func(t *testing.T) (*world, Open) {
	return func(t *testing.T) (*world, Open) {
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		w.tick(6 * time.Hour)
		c := w.s.Fleet.Card("s1-1.w1")
		c.Fields["first_dealt"] = stamp(w.s.Now.Add(-5 * time.Hour))
		c.Fields["dealt"] = stamp(w.s.Now.Add(-age))
		return w, raise(w, "a work card is past its deadline", "s1-1.w1", func(n *Note) { n.Card, n.Primaries = "s1-1.w1", []string{"s1-1"} })
	}
}

// retiredRead takes the read card of the reader off its table as accept and
// rework do: its record stays, and it is placed nowhere.
func retiredRead(w *world, id, reader string) {
	w.t.Helper()
	c := w.s.Readers.Card(ReadCardID(id, w.s.Work.Card(id).Int("attempt"), reader))
	if c == nil {
		w.t.Fatalf("no read card of %s by %s", id, reader)
	}
	c.Row, c.Col = "", ""
	c.Fields["retired"] = stamp(w.s.Now)
	c.Fields["retired_by"] = "accept"
	w.s.Readers.Put(c)
}

// The boundary seeds are printed as their rows say and are answerable; the
// states seeded here were reached by no test before.
func TestConditionsAtTheirBoundaries(t *testing.T) {
	t.Parallel()
	for _, sd := range boundarySeeds {
		t.Run(sd.name, func(t *testing.T) {
			t.Parallel()
			w, o := sd.build(t)
			if got := decisionsOf(Printed(w.s, o)); !slices.Equal(got, sd.want) {
				t.Fatalf("printed %q, want %q", got, sd.want)
			}
			if v := Answerable(w.s, w.s.Open); len(v) != 0 {
				t.Fatalf("a decision printed is one its verb refuses: %v", v)
			}
		})
	}
}

// ci red on a card in merging: the present build leaves the card in merging
// (the design's ci moves it back to review and its merge card to returned in
// the same step, F2-12; IT20 and IT21 do it). Accept is not offered for it,
// though its reads stand; and rework --fix, which the row offers, is refused
// by the verb, so Answerable names it. When ci moves the card, this state is
// not reached, and this test seeds the state after it.
func TestCIRedOnACardInMerging(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	accepted(w, "s1-1")
	o := raise(w, "ci red on a primary", "s1-1", nil)
	if pr := w.s.Work.Card("s1-1"); pr.Col != Merging || len(okReaders(w.s, pr)) != 2 {
		t.Fatalf("the seed is not a card in merging with two ok reads: %s", pr.Col)
	}
	if got := decisionsOf(Printed(w.s, o)); slices.Contains(got, "accept") {
		t.Fatalf("accept is offered for a card in merging: %q", got)
	}
	v := Answerable(w.s, []Open{o})
	if len(v) != 1 || v[0].Rule != RuleAnswerable || !strings.Contains(v[0].Detail, `offers "rework --fix": rework refuses s1-1: merging: return it first`) {
		t.Fatalf("the present build's ci red on a card in merging: %v", v)
	}
}

// The verbs an accept is held to: two DIFFERENT readers at the head, however
// the reads came about.
func TestAcceptNeedsTwoDifferentReaders(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"ci red on a primary", "returned to review"} {
		for _, tt := range []struct {
			name   string
			says   []string
			accept bool
		}{
			{"no read", nil, false},
			{"one ok", []string{"ok"}, false},
			{"one ok, one broken", []string{"ok", "broken"}, false},
			{"two ok", []string{"ok", "ok"}, true},
		} {
			t.Run(typ+", "+tt.name, func(t *testing.T) {
				t.Parallel()
				w := setup(t, 1)
				toReview(w, "s1-1", false)
				if tt.says != nil {
					verdicts(w, "s1-1", tt.says...)
				}
				o := raise(w, typ, "s1-1", nil)
				if got := slices.Contains(decisionsOf(Printed(w.s, o)), "accept"); got != tt.accept {
					t.Fatalf("accept offered %v, want %v: %q", got, tt.accept, decisionsOf(Printed(w.s, o)))
				}
				if v := Answerable(w.s, w.s.Open); len(v) != 0 {
					t.Fatalf("a decision printed is one its verb refuses: %v", v)
				}
			})
		}
	}
}

// Fleet down is offered for the member that held the card its own whole
// deadline: not for a member that took it a second short of it, whoever first
// took it, and not for one that is not up.
func TestFleetDownNeedsTheMembersOwnWholeDeadline(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		age  time.Duration
		down bool
	}{
		{"one second short", DeadlineUnfinished - time.Second, false},
		{"exactly", DeadlineUnfinished, false},
		{"one second over", DeadlineUnfinished + time.Second, true},
		{"the first taker's whole deadline, the member's own not run", DeadlineUnfinished / 4, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, o := lateTaken(tt.age)(t)
			got := slices.Contains(decisionsOf(Printed(w.s, o)), "fleet down <member>")
			if got != tt.down {
				t.Fatalf("fleet down offered %v, want %v: %q", got, tt.down, decisionsOf(Printed(w.s, o)))
			}
			if v := Answerable(w.s, w.s.Open); len(v) != 0 {
				t.Fatalf("a decision printed is one its verb refuses: %v", v)
			}
			// a member that is not up is never named, whatever its age
			w.s.MemberCtl(w.s.Fleet.Card("s1-1.w1").Row).Fields["status"] = Down
			if slices.Contains(decisionsOf(Printed(w.s, o)), "fleet down <member>") {
				t.Fatalf("fleet down offered for a member that is down")
			}
		})
	}
}

// RuleAnswerable is none of the numbers section 9 of the spec gives its rules:
// the number a violation carries says which rule it broke.
func TestRuleAnswerableIsNoRuleOfTheSpec(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("../../docs/SPEC-SPRINT.md")
	if err != nil {
		t.Fatalf("docs/SPEC-SPRINT.md: %v", err)
	}
	_, section, ok := strings.Cut(string(body), "\n## 9. What is always true\n")
	if !ok {
		t.Fatalf("docs/SPEC-SPRINT.md has no section 9")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	numbered := regexp.MustCompile(`(?m)^(\d+)\. `)
	var rules []int
	for _, m := range numbered.FindAllStringSubmatch(section, -1) {
		n, _ := strconv.Atoi(m[1])
		rules = append(rules, n)
	}
	if len(rules) < 14 {
		t.Fatalf("section 9 of the spec has %d numbered rules, want at least 14: %v", len(rules), rules)
	}
	if slices.Contains(rules, RuleAnswerable) {
		t.Fatalf("RuleAnswerable is %d, a rule of section 9 of the spec: %v", RuleAnswerable, rules)
	}
	if RuleAnswerable != slices.Max(rules)+1 {
		t.Fatalf("RuleAnswerable is %d, the next number after the spec's rules is %d", RuleAnswerable, slices.Max(rules)+1)
	}
	// and every violation Answerable makes carries it
	w, o := seedNamed(t, "sentinel reached")
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"first"}, Before: "g1"}))
	if v := Answerable(w.s, []Open{o}); len(v) == 0 || v[0].Rule != RuleAnswerable {
		t.Fatalf("a violation of Answerable: %v", v)
	}
}

// Every owner key of table 2.2 is served by a rule of the tick: the rule its
// word names, or the rule that serves keys of that word (ServingRule), which
// has a priority. A judgment that closes queues a key nothing would serve
// otherwise.
func TestOwnerKeysReachARule(t *testing.T) {
	t.Parallel()
	for _, want := range table22 {
		key := Judgments[want.typ].OwnerKey(want.ownerArg)
		if key == "" {
			continue
		}
		rule := ServingRule(key)
		if _, ok := PriorityOf(rule); !ok {
			t.Errorf("%q: owner key %q is served by %q, which is no rule of the tick", want.typ, key, rule)
		}
	}
}

// A judgment marked for a primary that came back a second time for the same
// cause gets "stop and look", which is card, as its last decision, unless the
// row lists card already (2.2, as today).
func TestAMarkedJudgmentAddsStopAndLook(t *testing.T) {
	t.Parallel()
	for _, sd := range allSeeds() {
		t.Run(sd.name, func(t *testing.T) {
			t.Parallel()
			w, o := sd.build(t)
			base := decisionsOf(Printed(w.s, o))
			o.Note.Marked = true
			want := slices.Clone(base)
			if !slices.Contains(base, "card") {
				want = append(want, "card")
			}
			if got := decisionsOf(Printed(w.s, o)); !slices.Equal(got, want) {
				t.Fatalf("a marked judgment prints %q, want %q", got, want)
			}
			if v := Answerable(w.s, []Open{o}); len(v) != 0 {
				t.Fatalf("a decision printed is one its verb refuses: %v", v)
			}
		})
	}
}

// openOnStream is the open judgment of the type on the stream, as merge wrote it.
func openOnStream(w *world, typ, stream string) Open {
	w.t.Helper()
	for _, o := range w.s.Open {
		if o.Note.Type == typ && o.Subject() == StreamSubject(stream) {
			return o
		}
	}
	w.t.Fatalf("no open %q on stream %s: %v", typ, stream, w.s.Open)
	return Open{}
}

func stoppedForConflict(t *testing.T) *world {
	w := setup(t, 3)
	accepted(w, "s1-1", "s1-2", "s1-3")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-2"}))
	return w
}

func stoppedForRed(suspects ...string) func(t *testing.T) *world {
	return func(t *testing.T) *world {
		w := setup(t, 3)
		accepted(w, "s1-1", "s1-2", "s1-3")
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Red: true, Suspects: suspects}))
		return w
	}
}

func stoppedForCross(t *testing.T) *world {
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", Count: 1}))
	accepted(w, "s1-1")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 1, Cross: "s1-1=s2-1"}))
	return w
}

func stoppedForRejection(t *testing.T) *world {
	w := setup(t, 2)
	accepted(w, "s1-1", "s1-2")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Rejected: true}))
	return w
}

func returnCards(ids ...string) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: ids}, Reason: "suspect"}))
	}
}

func dropCards(ids ...string) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		w.must(Drop(w.s, DropReq{Sel: Sel{IDs: ids}, Reason: "obsolete"}))
	}
}

func reworkCards(ids ...string) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: ids}, Fix: "a fix"}))
	}
}

// orphaned leaves the card in review with its merge card still queued: the
// state a repair that skipped accept's work entry leaves, which return takes
// back (an orphan).
func orphaned(id string) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		c := w.s.Work.Card(id)
		c.Col = Review
		w.s.Work.Put(c)
	}
}

// withoutMergeCard takes the merge card of a card in merging away: return
// takes such a card back (it had no merge card).
func withoutMergeCard(id string) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		delete(w.s.Merge.Cards, id)
		w.s.Merge.cells, w.s.Merge.byPrimary = nil, nil
	}
}

// atRedealBound takes every member down, so that the card in working is
// withdrawn and back in ready, and sets the redeals its work card has had.
func atRedealBound(id string, redeals int) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
		w.s.Fleet.Card(WorkCardID(id, w.s.Work.Card(id).Int("attempt"))).Fields["redeals"] = itoa(redeals)
	}
}

// landed puts the card in landed: the needed card of a cross stop lands.
func landed(id string) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		c := w.s.Work.Card(id)
		c.Col = Landed
		w.s.Work.Put(c)
	}
}

func inOrder(steps ...func(w *world)) func(w *world) {
	return func(w *world) {
		for _, step := range steps {
			step(w)
		}
	}
}

// offeredAnyway is the row edited to offer a decision in every state, as it
// stood before: the decision just taken, printed as accepted.
func offeredAnyway(verb string, args ...string) func(*JudgmentType) {
	return func(r *JudgmentType) {
		r.Decisions = slices.DeleteFunc(r.Decisions, func(d Decision) bool { return d.Verb == verb })
		r.Decisions = append(r.Decisions, decide(verb, args...))
	}
}

// After a card decision is taken the stream stays stopped and its judgment
// open until resume (spec rule 9), so the judgment must not print the decision
// just taken as accepted, and must print what is left: resume, and the
// decisions the cards allow now. Each state is the real one, made by the
// merge step and the coordinator's verbs, and the row edited to print the
// decision just taken is found by Answerable.
func TestAStoppedStreamOffersWhatIsLeftAfterADecision(t *testing.T) {
	t.Parallel()
	const (
		conflict = "stream stopped: conflict on a card"
		red      = "stream stopped: stream branch red"
		cross    = "stream stopped: needs a card of another stream first"
		rejected = "stream stopped: the merge queue rejected"
	)
	for _, tt := range []struct {
		name    string
		typ     string
		build   func(t *testing.T) *world
		decided func(w *world)
		want    []string
		// the row edited to print the decision just taken, and what its verb
		// says of it; none where the decision printed is still accepted
		edit    func(*JudgmentType)
		refused string
	}{
		{"conflict, nothing decided", conflict, stoppedForConflict, nil,
			[]string{"resume --stream s --did", "drop <card>"}, nil, ""},
		{"conflict, the card returned", conflict, stoppedForConflict, returnCards("s1-2"),
			[]string{"resume --stream s --did", "rework <card>", "drop <card>"}, nil, ""},
		{"conflict, the card dropped", conflict, stoppedForConflict, dropCards("s1-2"),
			[]string{"resume --stream s --did"}, offeredAnyway("drop", "<card>"), `offers "drop <card>": drop refuses s1-2: not on the table`},
		{"conflict, the card returned and reworked", conflict, stoppedForConflict, inOrder(returnCards("s1-2"), reworkCards("s1-2")),
			[]string{"resume --stream s --did", "drop <card>"}, offeredAnyway("rework", "<card>"), `offers "rework <card>": rework refuses s1-2: not review (it is working)`},
		{"conflict, the card returned and dropped", conflict, stoppedForConflict, inOrder(returnCards("s1-2"), dropCards("s1-2")),
			[]string{"resume --stream s --did"}, offeredAnyway("rework", "<card>"), `offers "rework <card>": rework refuses s1-2`},

		{"conflict, the card returned, reworked, and at its redeal bound", conflict, stoppedForConflict,
			inOrder(returnCards("s1-2"), reworkCards("s1-2"), atRedealBound("s1-2", MaxRedeals)),
			[]string{"resume --stream s --did", "rework <card>", "drop <card>"}, nil, ""},
		{"conflict, the card returned, reworked, and one redeal short of its bound", conflict, stoppedForConflict,
			inOrder(returnCards("s1-2"), reworkCards("s1-2"), atRedealBound("s1-2", MaxRedeals-1)),
			[]string{"resume --stream s --did", "drop <card>"}, offeredAnyway("rework", "<card>"), `offers "rework <card>": rework refuses s1-2`},

		{"red, nothing decided", red, stoppedForRed("s1-1"), nil,
			[]string{"return <suspect>", "resume --did"}, nil, ""},
		{"red, the suspect returned", red, stoppedForRed("s1-1"), returnCards("s1-1"),
			[]string{"resume --did", "rework <suspect>"}, offeredAnyway("return", "<suspect>"), `offers "return <suspect>": return refuses s1-1: not merging (it is review)`},
		{"red, the suspect returned and reworked", red, stoppedForRed("s1-1"), inOrder(returnCards("s1-1"), reworkCards("s1-1")),
			[]string{"resume --did"}, offeredAnyway("rework", "<suspect>"), `offers "rework <suspect>": rework refuses s1-1: not review (it is working)`},
		{"red, the suspect dropped", red, stoppedForRed("s1-1"), dropCards("s1-1"),
			[]string{"resume --did"}, offeredAnyway("return", "<suspect>"), `offers "return <suspect>": return refuses s1-1: not on the table`},
		{"red, two suspects, one returned", red, stoppedForRed("s1-1", "s1-2"), returnCards("s1-1"),
			[]string{"return <suspect>", "resume --did", "rework <suspect>"}, nil, ""},
		{"red, no suspect named, the batch returned", red, stoppedForRed(), returnCards("s1-1", "s1-2"),
			[]string{"resume --did", "rework <suspect>"}, offeredAnyway("return", "<suspect>"), `offers "return <suspect>": return refuses s1-1: not merging (it is review)`},

		{"cross, nothing decided", cross, stoppedForCross, nil,
			[]string{"rank <needed card>", "card", "return <card>", "drop <card>", "wait"}, nil, ""},
		{"cross, the card returned", cross, stoppedForCross, returnCards("s1-1"),
			[]string{"rank <needed card>", "card", "drop <card>", "wait"}, offeredAnyway("return", "<card>"), `offers "return <card>": return refuses s1-1: not merging (it is review)`},
		{"cross, the card dropped", cross, stoppedForCross, dropCards("s1-1"),
			[]string{"rank <needed card>", "card", "wait"}, offeredAnyway("drop", "<card>"), `offers "drop <card>": drop refuses s1-1: not on the table`},
		{"cross, the needed card dropped", cross, stoppedForCross, dropCards("s2-1"),
			[]string{"card", "return <card>", "drop <card>", "wait"}, offeredAnyway("rank", "<needed card>"), `offers "rank <needed card>": rank refuses s2-1: not on the table`},

		{"cross, the needed card landed", cross, stoppedForCross, landed("s2-1"),
			[]string{"card", "return <card>", "drop <card>", "wait"}, offeredAnyway("rank", "<needed card>"), `offers "rank <needed card>": rank refuses s2-1: landed; landed is final`},

		{"rejected, nothing decided", rejected, stoppedForRejection, nil,
			[]string{"resume --did", "return", "drop"}, nil, ""},
		{"rejected, the batch returned", rejected, stoppedForRejection, returnCards("s1-1", "s1-2"),
			[]string{"resume --did", "drop"}, offeredAnyway("return"), `offers "return": return refuses s1-1: not merging (it is review)`},
		{"rejected, one card of the batch returned", rejected, stoppedForRejection, returnCards("s1-1"),
			[]string{"resume --did", "return", "drop"}, nil, ""},
		{"rejected, a card of the batch an orphan, the other returned", rejected, stoppedForRejection, inOrder(orphaned("s1-1"), returnCards("s1-2")),
			[]string{"resume --did", "return", "drop"}, nil, ""},
		{"rejected, a card of the batch with no merge card, the other returned", rejected, stoppedForRejection, inOrder(withoutMergeCard("s1-1"), returnCards("s1-2")),
			[]string{"resume --did", "return", "drop"}, nil, ""},
		{"rejected, the batch dropped", rejected, stoppedForRejection, dropCards("s1-1", "s1-2"),
			[]string{"resume --did"}, offeredAnyway("drop"), `offers "drop": drop refuses s1-1: not on the table`},
		{"rejected, the batch returned and dropped", rejected, stoppedForRejection, inOrder(returnCards("s1-1", "s1-2"), dropCards("s1-1", "s1-2")),
			[]string{"resume --did"}, offeredAnyway("return"), `offers "return": return refuses s1-1`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := tt.build(t)
			o := openOnStream(w, tt.typ, "s1")
			if tt.decided != nil {
				tt.decided(w)
			}
			// the premise (spec rule 9): the stream is stopped, and the judgment
			// is open, until resume
			if got := w.s.StreamCtl("s1").F("state"); got != StreamStopped {
				t.Fatalf("the stream is %q after the decision, want stopped", got)
			}
			if !slices.ContainsFunc(w.s.Open, func(x Open) bool { return x.Key == o.Key }) {
				t.Fatalf("the judgment is closed after the decision: %v", w.s.Open)
			}
			if got := decisionsOf(Printed(w.s, o)); !slices.Equal(got, tt.want) {
				t.Fatalf("printed %q, want %q", got, tt.want)
			}
			if v := Answerable(w.s, []Open{o}); len(v) != 0 {
				t.Fatalf("a decision printed is one its verb refuses: %v", v)
			}
			if tt.edit == nil {
				return
			}
			v := answerableIn(withRow(tt.typ, tt.edit), w.s, []Open{o})
			if len(v) != 1 || v[0].Rule != RuleAnswerable || !strings.Contains(v[0].Detail, tt.refused) {
				t.Fatalf("the row printing the decision just taken: %v, want one violation with %q", v, tt.refused)
			}
		})
	}
}
