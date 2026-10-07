package refmodel

import (
	"slices"
	"sort"
)

// The reads are read cards (docs/SPEC-SPRINT.md section 6, "A read is a consumer card";
// sprint read_cards.go; tla/ReadCards.tla): the tick's deal cuts every read a primary in
// review still needs at once, each a card on the fleet row of a different member that
// reads (State.Readers: its reader row names it a reader), never the attempt's own worker
// and never a reader that holds or spent a read of the attempt. The member takes it with
// its work and closes it with its verdict (Read); a read card the machine takes back (its
// reader down, its primary moved on) spends nothing of its reader's. The model's members
// are wide (Width): the room a read holds, half a slot, bounds no choice here, and counts
// in the member's load (Held).

// ReadsNeeded is how many ok reads from different readers a primary needs: two, the
// model's cards being pro cards (sprint.ReadsNeeded).
const ReadsNeeded = 2

// MaxReadGen is the most generations of one reader's read card at one attempt
// (sprint.MaxReadGen): the plain identity and .g1 to .g<MaxReadGen>.
const MaxReadGen = 2

// spentBy is what retires a read card and spends its reader at the attempt
// (sprint read_cards.go, spentBy).
var spentBy = []string{ByRead, ByReturned, ByLate}

// ReadIDs is every identity one reader's read card of an attempt may have
// (sprint.ReadCardGenIDs).
func ReadIDs(p string, a int, r string) []string {
	id := RC(p, a, r)
	out := []string{id}
	for n := 1; n <= MaxReadGen; n++ {
		out = append(out, id+".g"+itoa(n))
	}
	return out
}

// nextReadID is the reader's next read card id at the attempt: the first of ReadIDs not
// made; "" when every one is.
func (s State) nextReadID(p string, a int, r string) string {
	for _, id := range ReadIDs(p, a, r) {
		if _, made := s.Reads[id]; !made {
			return id
		}
	}
	return ""
}

// spent says the reader holds or closed a read card of p at the attempt: one placed, one
// retired with a verdict, or one it ended itself (spentBy). It is never dealt that read
// again at the attempt.
func (s State) spent(p string, a int, r string) bool {
	for _, id := range ReadIDs(p, a, r) {
		c, ok := s.Reads[id]
		if ok && (c.Place != Retired || c.Verdict != "" || slices.Contains(spentBy, c.By)) {
			return true
		}
	}
	return false
}

// worker is the member that worked p's attempt.
func (s State) worker(p string) string {
	return s.Work[WC(p, s.Primaries[p].Attempt)].Member
}

// MayRead says the reader may be dealt a read card of p now: a member up that reads, not
// the attempt's worker, holding or having spent no read of the attempt, with an identity
// left (sprint mayReadCard).
func (s State) MayRead(p, r string) bool {
	a := s.Primaries[p].Attempt
	return slices.Contains(s.Readers, r) && s.Members[r] == Up && r != s.worker(p) && !s.spent(p, a, r) && s.nextReadID(p, a, r) != ""
}

// readsOf is p's read cards at its attempt, by id.
func (s State) readsOf(p string) []string {
	a := s.Primaries[p].Attempt
	var out []string
	for id, c := range s.Reads {
		if c.Primary == p && c.Attempt == a {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// OutOf is p's read cards placed, at any attempt.
func (s State) OutOf(p string) []string {
	var out []string
	for id, c := range s.Reads {
		if c.Primary == p && c.Place != Retired {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// standing is p's reads that stand at its attempt: placed, or retired with a verdict; and
// whether one of them found it broken (sprint readCardsStanding).
func (s State) standing(p string) (n int, broken bool) {
	for _, id := range s.readsOf(p) {
		if c := s.Reads[id]; c.Place != Retired || c.Verdict != "" {
			n++
			broken = broken || c.Verdict == Broken
		}
	}
	return n, broken
}

// ReadsWanted is how many read cards the deal cuts for p now (sprint readCardsWanted): the
// rest of the reads it needs, at once, none once a read found it broken, none for work
// that failed.
func (s State) ReadsWanted(p string) int {
	if !s.InWork(p, Review) || s.Failed(p) {
		return 0
	}
	n, broken := s.standing(p)
	if broken {
		return 0
	}
	return max(0, ReadsNeeded-n)
}

// OkReaders is the readers whose read of p's attempt at its head closed ok.
func (s State) OkReaders(p string) []string {
	pr := s.Primaries[p]
	var out []string
	for _, r := range s.Readers {
		for _, id := range ReadIDs(p, pr.Head, r) {
			if c, ok := s.Reads[id]; ok && pr.Head > 0 && c.Verdict == OK {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// Acceptable is SprintTables.tla Acceptable(p): ok reads from as many readers as it needs.
func (s State) Acceptable(p string) bool { return len(s.OkReaders(p)) >= ReadsNeeded }

// AskedNow is SprintTables.tla AskedNow(p): a read card of p's attempt was made (placed or
// retired).
func (s State) AskedNow(p string) bool { return len(s.readsOf(p)) > 0 }

// ReadChoice is the readers the deal may give p's read cards now, in name order: every
// reader that MayRead it, and how many it gives (the reads wanted, at most as many as may).
func (s State) ReadChoice(p string) (may []string, n int) {
	for _, r := range sorted(s.Readers) {
		if s.MayRead(p, r) {
			may = append(may, r)
		}
	}
	return may, min(s.ReadsWanted(p), len(may))
}

// takeBack retires every placed read card whose primary is no longer in review at the
// card's attempt (sprint readCardsTakeBack, RetiredByPrimary): it spends nothing.
func (n *State) takeBack() {
	for _, id := range Keys(n.Reads) {
		c := n.Reads[id]
		if c.Place == Retired {
			continue
		}
		if pr, ok := n.Primaries[c.Primary]; !ok || pr.State != Review || pr.Attempt != c.Attempt {
			c.Place, c.By = Retired, ByPrimary
			n.Reads[id] = c
		}
	}
}

// retireReads retires p's placed read cards, by the cause given.
func (n *State) retireReads(p, by string) {
	for _, id := range n.OutOf(p) {
		c := n.Reads[id]
		c.Place, c.By = Retired, by
		n.Reads[id] = c
	}
}

// cutReads is the read-card cut of the tick's deal (sprint readCardsAsk, run first in
// TickDeal): the cards whose primary moved on taken back, then for every primary in review
// that wants reads, its read cards, each to a different reader that may read it, ready on
// the reader's row. choice is the readers each primary was dealt; a primary it does not
// name takes the first that may, in name order. A choice must be as many readers as the
// primary wants and may be given, each one that may. Each primary dealt closes its
// stranded judgment.
func (n *State) cutReads(choice map[string][]string) error {
	n.takeBack()
	for _, p := range Keys(n.Primaries) {
		want := n.ReadsWanted(p)
		if want == 0 {
			continue
		}
		may, k := n.ReadChoice(p)
		if k == 0 {
			continue
		}
		readers, given := choice[p]
		if !given {
			readers = may[:k]
		}
		if len(readers) != k || len(addSorted(nil, readers...)) != k {
			return badChoice("%s's read cards dealt to %v, not %d different readers of %v", p, readers, k, may)
		}
		a := n.Primaries[p].Attempt
		for _, r := range readers {
			if !slices.Contains(may, r) {
				return badChoice("%s's read card dealt to %s, which may not read it (of %v)", p, r, may)
			}
			id := n.nextReadID(p, a, r)
			n.Reads[id] = ReadCard{Primary: p, Attempt: a, Reader: r, Place: FReady}
		}
		delete(n.Open, Judgment{JStranded, p})
	}
	return nil
}

// readersSpent says no reader, up or down, may ever read p at its attempt: none is there,
// or every one worked its attempt or holds or spent a read of it (sprint readersOf).
func (s State) readersSpent(p string) bool {
	a := s.Primaries[p].Attempt
	for _, r := range s.Readers {
		if _, member := s.Members[r]; !member {
			continue // a reader row of a machine not in the fleet reads nothing (sprint readersOf)
		}
		if r != s.worker(p) && !s.spent(p, a, r) && s.nextReadID(p, a, r) != "" {
			return false
		}
	}
	return true
}

// cannotAsk is the tick's cannot-ask judgment (sprint readCardsAskPart, cannotAskCond): a
// primary in review that wants reads no reader, up or down, may ever give it at its attempt
// (none there, or every one spent) is a judgment on it; one that no longer is closes,
// acknowledged or not.
func (n *State) cannotAsk() {
	stuck := map[string]bool{}
	for _, p := range Keys(n.Primaries) {
		if n.ReadsWanted(p) > 0 && n.readersSpent(p) {
			stuck[p] = true
		}
	}
	for j := range n.Open {
		if j.Type == JCannotAsk && !stuck[j.Subject] {
			delete(n.Open, j)
		}
	}
	for j := range n.Acked {
		if j.Type == JCannotAsk && !stuck[j.Subject] {
			delete(n.Acked, j)
		}
	}
	for p := range stuck {
		if j := (Judgment{JCannotAsk, p}); !n.Acked[j] {
			n.Open[j] = true
		}
	}
}

// Read is a reader closing a read card it holds with its verdict (sprint readCardVerb,
// friendReadCloseUnit): the card retires with it (ByRead). A card of an attempt its
// primary has left is refused. Broken opens the broken judgment; an ok that leaves the
// primary acceptable while the pump holds it is ready to accept (acceptNote); else the
// review's judgment, when one is due (judgeReview).
func Read(s State, r, c string, ok bool) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	rc, found := s.Reads[c]
	if !found || rc.Reader != r || rc.Place == Retired {
		return s, refuse("%s is not on %s's row", c, r)
	}
	pr, placed := s.Primaries[rc.Primary]
	if !placed || pr.State == Off || pr.Attempt != rc.Attempt {
		return s, refuse("%s is a read of an attempt its primary has left", c)
	}
	n := s.Clone()
	p := rc.Primary
	v := OK
	if !ok {
		v = Broken
	}
	rc.Place, rc.Verdict, rc.By = Retired, v, ByRead
	n.Reads[c] = rc
	if !ok {
		n.open(JBroken, p)
	}
	n.judgeReview(p, false)
	return n, nil
}

// judgeReview is the review's judgment after a read closed (sprint reviewJudgment): ok
// reads from as many readers as it needs are the pump's to accept, or ready to accept
// while it holds them (acceptNote); a judgment open on it, or a read outstanding, holds
// it; work that failed with nothing open is stranded; no read at its attempt is stranded
// when the step closed a judgment on it (closed); reads that stand ok and fewer than it
// needs are the deal's to complete; else its reads are exhausted.
func (n *State) judgeReview(p string, closed bool) {
	if !n.InWork(p, Review) {
		return
	}
	if n.Acceptable(p) {
		n.acceptNote(p)
		return
	}
	reads, broken, outstanding := 0, false, false
	for _, id := range n.readsOf(p) {
		c := n.Reads[id]
		if c.Place == Retired && c.Verdict == "" {
			continue // taken back or handed back: no read
		}
		reads++
		broken = broken || c.Verdict == Broken
		outstanding = outstanding || c.Place != Retired
	}
	switch {
	case n.OpenOn(p) || outstanding:
	case n.Failed(p):
		n.open(JStranded, p)
	case reads == 0 && !closed:
	case reads == 0 && n.ReadsWanted(p) > 0 && !n.readersSpent(p):
		// the reads it wants wait for a reader that may yet read it (sprint readWaitHeld)
	case reads == 0:
		n.open(JStranded, p)
	case !broken && reads < ReadsNeeded:
	default:
		n.open(JReads, p)
	}
}

// TakeRead is a member taking a read card on its row: ready -> working.
func TakeRead(s State, m, c string) (State, error) {
	if err := free(s); err != nil {
		return s, err
	}
	rc, ok := s.Reads[c]
	if !ok || s.Members[m] != Up || rc.Reader != m || rc.Place != FReady {
		return s, refuse("%s is not in %s's ready cell", c, m)
	}
	n := s.Clone()
	rc.Place = FWorking
	n.Reads[c] = rc
	return n, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
