package sprint

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The property: after every change, the running indexes, updated by nothing
// but IndexOps (and the three intents' own writes to wait:<n>, which are the
// design's and not IndexOps's), equal IndexDefinition computed from scratch
// over every card. Three walks hold it:
//
//   - a random walk over a small population of cards in all four tables,
//     moved by the rows of Moves and by every field the indexes read
//     (indexSim);
//   - one card from any state to any state, with nothing lawful about it;
//   - a step of many cards, folded and cut, against its cards one by one.

const (
	indexChanges = 100_000 // steps of the random walk, over all its shards
	indexShards  = 8
)

// indexPlain is fields no index reads: a change that sets only these must
// return no operation.
var indexPlain = []string{"head", "brief", "result", "work", "gen", "member", "redeals", "admitted", "fix", "dealt", "asked", "state", "since"}

// indexCover counts what a walk did, so that a walk that never reaches a case
// cannot pass for one that does.
type indexCover struct {
	moves                    map[string]int // rows of Moves applied, by lifecycleKey
	added, removed, rescored map[string]int // members by index, a due kind as due:<kind>
	steps, calls, none       int
	needmet, waitfor         int
}

func newIndexCover() *indexCover {
	return &indexCover{moves: map[string]int{}, added: map[string]int{}, removed: map[string]int{}, rescored: map[string]int{}}
}

func (c *indexCover) sum(o *indexCover) {
	for k, v := range o.moves {
		c.moves[k] += v
	}
	for _, p := range [][2]map[string]int{{c.added, o.added}, {c.removed, o.removed}, {c.rescored, o.rescored}} {
		for k, v := range p[1] {
			p[0][k] += v
		}
	}
	c.steps, c.calls, c.none, c.needmet, c.waitfor = c.steps+o.steps, c.calls+o.calls, c.none+o.none, c.needmet+o.needmet, c.waitfor+o.waitfor
}

// label is the index a member belongs to: its name, and for a due entry its kind.
func label(k IndexKey, member string) string {
	if k.Index != IndexDue {
		return k.Index
	}
	kind, _, _ := strings.Cut(member, ":")
	return "due:" + kind
}

func (c *indexCover) assert(t *testing.T) {
	t.Helper()
	for _, m := range Moves {
		if c.moves[lifecycleKey(m)] == 0 {
			t.Errorf("the walk never made the move %s", lifecycleKey(m))
		}
	}
	labels := []string{IndexSent, IndexElig, IndexFresh, IndexAgain}
	for _, k := range DueKinds {
		labels = append(labels, "due:"+k.Kind)
	}
	for _, l := range labels {
		if c.added[l] == 0 || c.removed[l] == 0 || c.rescored[l] == 0 {
			t.Errorf("%s: %d members added, %d removed, %d re-scored; the walk must do each", l, c.added[l], c.removed[l], c.rescored[l])
		}
	}
	if c.removed[IndexWait] == 0 || c.needmet == 0 || c.waitfor == 0 {
		t.Errorf("wait: %d removals by IndexOps that took a member out, %d needmet, %d waitfor", c.removed[IndexWait], c.needmet, c.waitfor)
	}
	if c.none == 0 {
		t.Error("no change was one that touches no indexed field")
	}
}

func (c *indexCover) log(t *testing.T) {
	labels := []string{IndexSent, IndexElig, IndexFresh, IndexAgain, IndexWait}
	for _, k := range DueKinds {
		labels = append(labels, "due:"+k.Kind)
	}
	var ls []string
	for _, l := range labels {
		ls = append(ls, fmt.Sprintf("%s +%d -%d ~%d", l, c.added[l], c.removed[l], c.rescored[l]))
	}
	var ms []string
	for _, m := range Moves {
		ms = append(ms, fmt.Sprintf("%s %d", lifecycleKey(m), c.moves[lifecycleKey(m)]))
	}
	t.Logf("%d steps, %d IndexOps calls, %d touching no indexed field, %d needmet, %d waitfor; %s; moves: %s", c.steps, c.calls, c.none, c.needmet, c.waitfor, strings.Join(ls, "; "), strings.Join(ms, "; "))
}

// indexSim is the random walk's population, its running indexes, and the
// random source.
type indexSim struct {
	t       *testing.T
	rng     *rand.Rand
	cards   map[string]*IndexCard // by table/id: every record, placed or not
	order   []string              // the keys of cards, in the order they were made
	running Indexes
	issued  int     // primary ids issued: p1, p2, ...
	counter int     // ids of the other tables
	score   float64 // the last score given
	cover   *indexCover
	last    simTrace // what the step did, for a failure to say
	buf     []*IndexCard
}

// simTrace is what the step did, formatted only when a failure asks.
type simTrace struct {
	class         string
	before, after *IndexCard
}

func (s *indexSim) trace() string {
	return s.last.class + ": " + showCard(s.last.before) + " -> " + showCard(s.last.after)
}

func newIndexSim(t *testing.T, seed uint64) *indexSim {
	return &indexSim{t: t, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), cards: map[string]*IndexCard{}, running: Indexes{}, cover: newIndexCover()}
}

func (s *indexSim) chance(pct int) bool { return s.rng.IntN(100) < pct }

func (s *indexSim) some(xs ...string) string { return xs[s.rng.IntN(len(xs))] }

func cardKey(c *IndexCard) string { return c.Table + "/" + c.ID }

func showCard(c *IndexCard) string {
	if c == nil {
		return "<none>"
	}
	var fs []string
	for k, v := range c.Fields {
		fs = append(fs, k+"="+v)
	}
	sort.Strings(fs)
	return fmt.Sprintf("%s %s %s:%s@%s {%s}", c.Table, c.ID, c.Row, c.Col, fmtScore(c.Score), strings.Join(fs, " "))
}

// records is the cards of a table the test accepts, in the order they were made.
func (s *indexSim) records(table string, keep func(*IndexCard) bool) []*IndexCard {
	var out []*IndexCard
	for _, k := range s.order {
		if c := s.cards[k]; c.Table == table && (keep == nil || keep(c)) {
			out = append(out, c)
		}
	}
	return out
}

func (s *indexSim) pick(table string, keep func(*IndexCard) bool) *IndexCard {
	rs := s.records(table, keep)
	if len(rs) == 0 {
		return nil
	}
	return rs[s.rng.IntN(len(rs))]
}

func placed(c *IndexCard) bool { return c.placed() }

func (s *indexSim) isOpen(id string) bool {
	c := s.cards[Work+"/"+id]
	return c != nil && c.placed() && c.Col != Landed
}

func (s *indexSim) nextScore() float64 {
	s.score++
	return s.score
}

// One class of change: it returns the card before and after, whether it must
// touch no indexed field, and whether it could be made at all.
type simClass struct {
	name   string
	weight int
	run    func(*indexSim) (before, after *IndexCard, none, ok bool)
}

// needsFor is one to three needs for the card about to be made: mostly cards
// that are open now, and some that have landed, were dropped or were forgotten.
func (s *indexSim) needsFor() []string {
	var open []string
	for _, c := range s.records(Work, func(c *IndexCard) bool { return c.placed() && c.Col != Landed }) {
		open = append(open, c.ID)
	}
	var out []string
	for range 1 + s.rng.IntN(3) {
		switch {
		case len(open) > 0 && s.chance(85):
			out = append(out, open[s.rng.IntN(len(open))])
		case s.issued > 1:
			out = append(out, "p"+strconv.Itoa(1+s.rng.IntN(s.issued-1)))
		}
	}
	return out
}

func simWorkCreate(s *indexSim) (b, a *IndexCard, none, ok bool) {
	if len(s.records(Work, nil)) >= 10 {
		return nil, nil, false, false
	}
	s.issued++
	id := "p" + strconv.Itoa(s.issued)
	stream, sentinel := s.some("s1", "s2"), s.chance(20)
	col := Waiting
	if !sentinel && s.chance(45) {
		col = Ready
	}
	f := map[string]string{"kind": "primary", "stream": stream, "attempt": "0"}
	if sentinel {
		f["kind"] = "sentinel"
	}
	if col == Waiting && s.chance(60) {
		needs := s.needsFor()
		open := 0
		for _, n := range needs {
			if s.isOpen(n) {
				open++
			}
		}
		if len(needs) > 0 {
			f["needs"] = strings.Join(needs, ",")
		}
		if open > 0 || s.chance(20) {
			f["open"] = strconv.Itoa(open)
		}
	}
	if s.chance(5) {
		f["refused"] = "deal: a reason"
	}
	return nil, &IndexCard{Table: Work, ID: id, Row: stream, Col: col, Score: s.nextScore(), Fields: f}, false, true
}

// simWorkMove makes a row of Moves on a card in its column, and what that row
// does to the card's fields; a sentinel makes only the release.
func simWorkMove(s *indexSim) (b, a *IndexCard, none, ok bool) {
	c := s.pick(Work, placed)
	if c == nil {
		return nil, nil, false, false
	}
	sentinel := c.Fields["kind"] == "sentinel"
	needOpen := false
	for _, n := range Split(c.Fields["needs"]) {
		needOpen = needOpen || s.isOpen(n)
	}
	var moves []Move
	for _, m := range Moves {
		switch {
		case m.From != c.Col:
		case c.Col == Waiting && sentinel != (m.To == Landed): // a sentinel only releases, and a primary never does
		case m.Verb == "resolve" && needOpen: // the needs rule: a card with a need still open does not become ready
		default:
			moves = append(moves, m)
		}
	}
	if len(moves) == 0 {
		return nil, nil, false, false
	}
	m := moves[s.rng.IntN(len(moves))]
	a = ixAt(c, m.To)
	attempt, _ := strconv.Atoi(c.Fields["attempt"])
	switch m.Verb {
	case "resolve":
		if s.chance(90) {
			a = ixWith(a, "open", "")
		}
	case "deal":
		a = ixWith(a, "attempt", strconv.Itoa(attempt+1), "result", "")
	case "finish":
		a = ixWith(a, "result", s.some("ok", "failed"))
	case "fleet down":
		if s.chance(30) {
			a = ixWith(a, "bound", "5")
		}
	case "rework":
		if m.To == Working {
			a = ixWith(a, "attempt", strconv.Itoa(attempt+1))
		}
		a = ixWith(a, "bound", "")
	}
	s.cover.moves[lifecycleKey(m)]++
	return c, a, false, true
}

func simWorkDrop(s *indexSim) (b, a *IndexCard, none, ok bool) {
	c := s.pick(Work, func(c *IndexCard) bool { return c.placed() && IsOpen(c.Col) })
	if c == nil {
		return nil, nil, false, false
	}
	return c, ixUnplaced(c), false, true
}

// simForget removes the record of a card that has landed or was dropped, which
// no index holds.
func simForget(table string) func(*indexSim) (b, a *IndexCard, none, ok bool) {
	return func(s *indexSim) (b, a *IndexCard, none, ok bool) {
		c := s.pick(table, func(c *IndexCard) bool {
			return !c.placed() || table == Work && c.Col == Landed || table != Work && (c.Col == DoneOK || c.Col == DoneFailed || c.Col == OK || c.Col == Broken)
		})
		if c == nil {
			return nil, nil, false, false
		}
		return c, nil, false, true
	}
}

func simWorkRank(s *indexSim) (b, a *IndexCard, none, ok bool) {
	c := s.pick(Work, placed)
	if c == nil {
		return nil, nil, false, false
	}
	a = ixWith(c)
	a.Score = float64(s.rng.IntN(4000)) / 4
	return c, a, false, true
}

// simWorkFlip changes a field the indexes read, on a card in any state, lawful or not.
func simWorkFlip(s *indexSim) (b, a *IndexCard, none, ok bool) {
	c := s.pick(Work, nil)
	if c == nil {
		return nil, nil, false, false
	}
	switch s.rng.IntN(7) {
	case 0, 1:
		a = ixWith(c, "open", s.some("", "0", "1", "2"))
	case 2:
		if c.Fields["refused"] == "" {
			a = ixWith(c, "refused", "resolve: "+s.some("no", "why"))
		} else {
			a = ixWith(c, "refused", "")
		}
	case 3:
		if c.Fields["bound"] == "" {
			a = ixWith(c, "bound", s.some("3", "5"))
		} else {
			a = ixWith(c, "bound", "")
		}
	case 4:
		a = ixWith(c, "attempt", s.some("", "0", "1", "2", "3"))
	case 5:
		if s.chance(30) {
			a = ixWith(c, "kind", s.some("primary", "sentinel"))
		} else {
			a = ixWith(c)
			a.Row = s.some("s1", "s2") // a stream is not changed in the design; the function must not care
		}
	default:
		a = ixWith(c)
	}
	return c, a, false, true
}

// simTouch sets one field no index reads on a card of a table.
func simTouch(table string) func(*indexSim) (b, a *IndexCard, none, ok bool) {
	return func(s *indexSim) (b, a *IndexCard, none, ok bool) {
		c := s.pick(table, nil)
		if c == nil {
			return nil, nil, false, false
		}
		return c, ixWith(c, s.some(indexPlain...), strconv.Itoa(s.rng.IntN(1000))), true, true
	}
}

func (s *indexSim) due(pct int) string {
	if s.chance(pct) {
		return strconv.Itoa(100 + s.rng.IntN(9000))
	}
	return ""
}

func simFleetCreate(s *indexSim) (b, a *IndexCard, none, ok bool) {
	if len(s.records(Fleet, func(c *IndexCard) bool { return c.Col != Ctl })) >= 5 {
		return nil, nil, false, false
	}
	s.counter++
	return nil, &IndexCard{Table: Fleet, ID: "w" + strconv.Itoa(s.counter), Row: s.some("m1", "m2"), Col: Ready,
		Fields: ifields("kind", "work", "due_untaken", s.due(85))}, false, true
}

// simFleetMove is a member taking a card, finishing it, being marked down (its
// cards withdrawn), a card dealt again, one leveled or replaced, or retired.
func simFleetMove(s *indexSim) (b, a *IndexCard, none, ok bool) {
	c := s.pick(Fleet, placed)
	if c == nil {
		return nil, nil, false, false
	}
	switch c.Col {
	case Ready:
		switch s.rng.IntN(5) {
		case 0, 1:
			a = ixAt(c, Working, "due_untaken", func() string {
				if s.chance(70) {
					return ""
				}
				return c.Fields["due_untaken"]
			}(), "due_unfinished", s.due(85))
		case 2:
			a = ixAt(c, Withdrawn)
		case 3:
			a = ixWith(c, "due_untaken", s.due(80))
			a.Row = s.some("m1", "m2") // leveled, or replaced with a new due time
		default:
			a = ixUnplaced(c)
		}
	case Working:
		switch s.rng.IntN(4) {
		case 0, 1:
			a = ixAt(c, s.some(DoneOK, DoneFailed))
			if s.chance(70) {
				a = ixWith(a, "due_unfinished", "")
			}
		case 2:
			a = ixAt(c, Withdrawn)
		default:
			a = ixAt(c, Ready, "due_unfinished", "", "due_untaken", s.due(80))
			a.Row = s.some("m1", "m2")
		}
	case Withdrawn:
		a = ixAt(c, Ready, "due_untaken", s.due(60))
	default:
		a = ixUnplaced(c)
	}
	return c, a, false, true
}

func simFleetFlip(s *indexSim) (b, a *IndexCard, none, ok bool) {
	c := s.pick(Fleet, func(c *IndexCard) bool { return c.Col != Ctl })
	if c == nil {
		return nil, nil, false, false
	}
	return c, ixWith(c, s.some("due_untaken", "due_unfinished"), s.due(60)), false, true
}

func simReadersCreate(s *indexSim) (b, a *IndexCard, none, ok bool) {
	if len(s.records(Readers, nil)) >= 4 {
		return nil, nil, false, false
	}
	s.counter++
	return nil, &IndexCard{Table: Readers, ID: "r" + strconv.Itoa(s.counter), Row: s.some("ra", "rb"), Col: Asked,
		Fields: ifields("kind", "read", "due_unbegun", s.due(85))}, false, true
}

func simReadersMove(s *indexSim) (b, a *IndexCard, none, ok bool) {
	c := s.pick(Readers, placed)
	if c == nil {
		return nil, nil, false, false
	}
	switch c.Col {
	case Asked:
		switch s.rng.IntN(4) {
		case 0, 1:
			a = ixAt(c, Reading, "due_unbegun", func() string {
				if s.chance(70) {
					return ""
				}
				return c.Fields["due_unbegun"]
			}(), "due_unreported", s.due(85))
		case 2:
			a = ixAt(c, s.some(OK, Broken))
		default:
			a = ixUnplaced(c)
		}
	case Reading:
		a = ixAt(c, s.some(OK, Broken, Asked))
		if s.chance(70) {
			a = ixWith(a, "due_unreported", "")
		}
	default:
		a = ixUnplaced(c)
	}
	return c, a, false, true
}

func simReadersFlip(s *indexSim) (b, a *IndexCard, none, ok bool) {
	c := s.pick(Readers, nil)
	if c == nil {
		return nil, nil, false, false
	}
	return c, ixWith(c, s.some("due_unbegun", "due_unreported"), s.due(60)), false, true
}

// simCtl makes a stream's control card, then sets and unsets its idle deadline.
func simCtl(s *indexSim) (b, a *IndexCard, none, ok bool) {
	for _, st := range []string{"s1", "s2"} {
		if s.cards[Merge+"/"+CtlID(st)] == nil {
			return nil, ictl(st, "state", StreamWaiting), false, true
		}
	}
	c := s.pick(Merge, nil)
	a = ixWith(c, "due_mergeidle", s.due(65))
	if s.chance(20) {
		a = ixWith(a, "state", s.some(StreamWaiting, StreamMerging, StreamStopped))
	}
	return c, a, false, true
}

var simClasses = []simClass{
	{"work create", 9, simWorkCreate},
	{"work move", 30, simWorkMove},
	{"work drop", 2, simWorkDrop},
	{"work forget", 4, simForget(Work)},
	{"work rank", 4, simWorkRank},
	{"work flip", 12, simWorkFlip},
	{"work touch", 3, simTouch(Work)},
	{"fleet create", 4, simFleetCreate},
	{"fleet move", 10, simFleetMove},
	{"fleet flip", 4, simFleetFlip},
	{"fleet forget", 2, simForget(Fleet)},
	{"fleet touch", 1, simTouch(Fleet)},
	{"readers create", 3, simReadersCreate},
	{"readers move", 7, simReadersMove},
	{"readers flip", 3, simReadersFlip},
	{"readers forget", 2, simForget(Readers)},
	{"readers touch", 1, simTouch(Readers)},
	{"control", 4, simCtl},
}

func (s *indexSim) pickClass() simClass {
	total := 0
	for _, c := range simClasses {
		total += c.weight
	}
	n := s.rng.IntN(total)
	for _, c := range simClasses {
		if n < c.weight {
			return c
		}
		n -= c.weight
	}
	return simClasses[0]
}

// derive is one IndexOps call on one card: the ops are held to be exactly the
// difference (every member removed was there, every member added is new or
// re-scored; wait:<n> alone may remove a member that is not there), applied,
// and the card stored.
func (s *indexSim) derive(before, after *IndexCard, none bool) {
	s.t.Helper()
	s.cover.calls++
	ops, err := IndexOps(before, after)
	if err != nil {
		s.t.Fatalf("IndexOps refused %s -> %s: %v\n%s", showCard(before), showCard(after), err, s.trace())
	}
	if none && len(ops) > 0 {
		s.t.Fatalf("a change that touches no indexed field returned %v\n%s -> %s\n%s", ops, showCard(before), showCard(after), s.trace())
	}
	if len(ops) == 0 {
		s.cover.none++
	}
	for _, o := range ops {
		m := s.running[o.Key]
		for _, r := range o.Rem {
			_, in := m[r]
			switch {
			case in:
				s.cover.removed[label(o.Key, r)]++
			case o.Key.Index != IndexWait:
				s.t.Fatalf("%s removes %s, which is not in the index\n%s -> %s\n%s", o.Key, r, showCard(before), showCard(after), s.trace())
			}
		}
		for _, a := range o.Add {
			old, in := m[a.Member]
			switch {
			case in && old == a.Score:
				s.t.Fatalf("%s adds %s at %s, where it already is\n%s -> %s\n%s", o.Key, a.Member, fmtScore(a.Score), showCard(before), showCard(after), s.trace())
			case in:
				s.cover.rescored[label(o.Key, a.Member)]++
			default:
				s.cover.added[label(o.Key, a.Member)]++
			}
		}
	}
	s.running.Apply(ops)
	if s.cover.calls%8 == 0 {
		// The exact ops again change nothing (ReplayNoop): each is a member set or taken out, never a count.
		again := Indexes{}
		for _, o := range ops {
			again[o.Key] = cloneIndexes(Indexes{o.Key: s.running[o.Key]})[o.Key]
		}
		s.running.Apply(ops)
		for _, o := range ops {
			if diff := (Indexes{o.Key: s.running[o.Key]}).Diff(Indexes{o.Key: again[o.Key]}); len(diff) > 0 {
				s.t.Fatalf("the ops applied a second time changed %s: %v\n%s -> %s", o.Key, diff, showCard(before), showCard(after))
			}
		}
	}
	switch {
	case after == nil:
		delete(s.cards, cardKey(before))
		for i, k := range s.order {
			if k == cardKey(before) {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	case before == nil:
		s.cards[cardKey(after)] = after
		s.order = append(s.order, cardKey(after))
	default:
		s.cards[cardKey(after)] = after
	}
}

// intents is what the three intents of section 1.3.3 do about the change, as
// the design says and not through IndexOps: waitfor adds a card created waiting
// to the wait:<n> of each of its needs that is open; a card that lands lowers
// the open of every waiter in its wait:<n> and takes them out (needmet); a card
// that leaves the table takes them out and leaves their open (needgone). The
// waiter's open is a field change like any other, and is derived like one.
func (s *indexSim) intents(before, after *IndexCard) {
	if before == nil && after != nil && after.Table == Work && after.Col == Waiting {
		for _, n := range Split(after.Fields["needs"]) {
			if s.isOpen(n) {
				s.running.Apply([]IndexOp{{Key: IndexKey{IndexWait, n}, Add: []Scored{{after.ID, 0}}}})
				s.cover.waitfor++
			}
		}
	}
	if before == nil || before.Table != Work || !before.placed() || before.Col == Landed {
		return
	}
	landed := after != nil && after.placed() && after.Col == Landed
	gone := after == nil || !after.placed()
	if !landed && !gone {
		return
	}
	key := IndexKey{IndexWait, before.ID}
	var waiters []string
	for w := range s.running[key] {
		waiters = append(waiters, w)
	}
	sort.Strings(waiters)
	for _, w := range waiters {
		wc := s.cards[Work+"/"+w]
		if wc == nil || wc.Col != Waiting {
			s.t.Fatalf("%s holds %s, which is not a waiting card: %s\n%s", key, w, showCard(wc), s.trace())
		}
		if landed {
			open, _ := strconv.Atoi(wc.Fields["open"])
			next := ""
			if open > 1 {
				next = strconv.Itoa(open - 1)
			}
			s.cover.needmet++
			s.derive(wc, ixWith(wc, "open", next), false)
		}
	}
	delete(s.running, key)
}

// check holds the running indexes to the definition over every card.
func (s *indexSim) check() {
	s.t.Helper()
	s.buf = s.buf[:0]
	for _, k := range s.order {
		s.buf = append(s.buf, s.cards[k])
	}
	want, err := IndexDefinition(s.buf)
	if err != nil {
		s.t.Fatalf("IndexDefinition refused: %v", err)
	}
	if diff := s.running.Diff(want); len(diff) > 0 {
		s.t.Fatalf("after step %d the running indexes differ from their definition:\n  %s\nthe step:\n%s", s.cover.steps, strings.Join(diff, "\n  "), s.trace())
	}
}

func (s *indexSim) step() {
	s.t.Helper()
	for {
		c := s.pickClass()
		before, after, none, ok := c.run(s)
		if !ok {
			continue
		}
		s.last = simTrace{c.name, before, after}
		s.derive(before, after, none)
		s.intents(before, after)
		break
	}
	s.cover.steps++
	s.check()
}

func TestIndexOpsEqualTheDefinitionAfterEveryChange(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	total := newIndexCover()
	t.Run("shards", func(t *testing.T) {
		for shard := range indexShards {
			t.Run(fmt.Sprintf("seed%d", shard+1), func(t *testing.T) {
				t.Parallel()
				s := newIndexSim(t, uint64(shard+1))
				for range indexChanges / indexShards {
					s.step()
				}
				mu.Lock()
				defer mu.Unlock()
				total.sum(s.cover)
			})
		}
	})
	if t.Failed() {
		return
	}
	if total.steps < indexChanges {
		t.Errorf("%d steps, want %d", total.steps, indexChanges)
	}
	total.assert(t)
	total.log(t)
}

// randomIndexCard is a card of any table in any state with any values of the
// fields the indexes read, lawful or not.
func randomIndexCard(rng *rand.Rand, table, id string) *IndexCard {
	return randomCard(rng, table, id, false)
}

// randomCard is randomIndexCard; a stream's control card names the stream by
// its own id when ownRow is set, so that no two of them share the member
// mergeidle:<stream>, as no two control cards share a stream.
func randomCard(rng *rand.Rand, table, id string, ownRow bool) *IndexCard {
	some := func(xs ...string) string { return xs[rng.IntN(len(xs))] }
	c := &IndexCard{Table: table, ID: id, Score: float64(rng.IntN(400)) / 4, Fields: map[string]string{}}
	set := func(k, v string) {
		if v != "" {
			c.Fields[k] = v
		}
	}
	due := func() string {
		if rng.IntN(3) == 0 {
			return ""
		}
		return strconv.Itoa(rng.IntN(5000))
	}
	switch table {
	case Work:
		c.Row, c.Col = some("s1", "s2", "s3"), some(append([]string{""}, States...)...)
		set("kind", some("primary", "sentinel", ""))
		set("open", some("", "0", "1", "2"))
		set("attempt", some("", "0", "1", "2", "3"))
		set("refused", some("", "", "", "x: y"))
		set("bound", some("", "", "", "5"))
		set("needs", some("", "p1", "p1,p2", "p2, p3,p1", ",p4,"))
	case Fleet:
		c.Row, c.Col = some("m1", "m2"), some("", Ready, Working, Withdrawn, DoneOK, DoneFailed, Ctl)
		set("due_untaken", due())
		set("due_unfinished", due())
	case Readers:
		c.Row, c.Col = some("ra", "rb"), some("", Asked, Reading, OK, Broken)
		set("due_unbegun", due())
		set("due_unreported", due())
	default:
		c.Row, c.Col = some("s1", "s2"), some("", Ctl, Queued, Merged, Stuck)
		if ownRow {
			c.Row = id
		}
		set("due_mergeidle", due())
	}
	for range rng.IntN(3) {
		set(some(indexPlain...), "v")
	}
	if c.Col == "" {
		c.Row = ""
	}
	return c
}

func withoutWait(x Indexes) Indexes {
	out := Indexes{}
	for k, m := range x {
		if k.Index != IndexWait {
			out[k] = m
		}
	}
	return out
}

func cloneIndexes(x Indexes) Indexes {
	out := Indexes{}
	for k, m := range x {
		out[k] = map[string]float64{}
		for member, score := range m {
			out[k][member] = score
		}
	}
	return out
}

// One card from any state to any state, and from nothing, and to nothing: the
// indexes of the card alone, updated by the ops, are the indexes of the card
// after; and the ops are the difference and no more.
func TestIndexOpsFromAnyStateToAnyState(t *testing.T) {
	t.Parallel()
	tables := []string{Work, Work, Work, Fleet, Readers, Merge}
	var wg sync.WaitGroup
	for shard := range indexShards {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(100+shard), 7))
			for i := range indexChanges / indexShards {
				table := tables[rng.IntN(len(tables))]
				var before, after *IndexCard
				if rng.IntN(8) != 0 {
					before = randomIndexCard(rng, table, "c")
				}
				if rng.IntN(8) != 0 {
					after = randomIndexCard(rng, table, "c")
				}
				ops, err := IndexOps(before, after)
				if err != nil {
					t.Errorf("shard %d change %d: %v: %s -> %s", shard, i, err, showCard(before), showCard(after))
					return
				}
				start, err := IndexDefinition([]*IndexCard{before})
				if err != nil {
					t.Errorf("shard %d change %d: %v", shard, i, err)
					return
				}
				got := cloneIndexes(start)
				got.Apply(ops)
				want, err := IndexDefinition([]*IndexCard{after})
				if err != nil {
					t.Errorf("shard %d change %d: %v", shard, i, err)
					return
				}
				// The needs are open in neither definition (no other card is there), so wait is empty
				// in both; the ops' removals of a card leaving waiting are held by the walk above.
				if diff := got.Diff(want); len(diff) > 0 {
					t.Errorf("shard %d change %d: %s -> %s\nops %v\n  %s", shard, i, showCard(before), showCard(after), ops, strings.Join(diff, "\n  "))
					return
				}
				for _, o := range ops {
					if o.Key.Index == IndexWait {
						if len(o.Add) > 0 || len(o.Rem) != 1 || before == nil || !inWaiting(before) || inWaiting(after) {
							t.Errorf("shard %d change %d: a wait op that is not a card leaving waiting: %v; %s -> %s", shard, i, o, showCard(before), showCard(after))
							return
						}
						continue
					}
					for _, r := range o.Rem {
						if _, in := start[o.Key][r]; !in {
							t.Errorf("shard %d change %d: %s removes %s, which was not there; %s -> %s", shard, i, o.Key, r, showCard(before), showCard(after))
							return
						}
					}
					for _, a := range o.Add {
						if old, in := start[o.Key][a.Member]; in && old == a.Score {
							t.Errorf("shard %d change %d: %s adds %s where it is; %s -> %s", shard, i, o.Key, a.Member, showCard(before), showCard(after))
							return
						}
					}
				}
			}
		}()
	}
	wg.Wait()
}

// A step of many cards, folded into one op per key and cut into pieces, applied
// to the indexes of the cards before it, gives what the cards' ops give one
// after the other, and (but for wait) the indexes of the cards after.
func TestAStepOfManyCardsIsItsCardsOneByOne(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(9, 9))
	tables := []string{Work, Work, Work, Fleet, Readers, Merge}
	for step := range 400 {
		n := 1 + rng.IntN(40)
		if step%100 == 0 {
			n = 2*IndexPiece + 300 + rng.IntN(500)
		}
		var changes []IndexChange
		var before, after []*IndexCard
		for i := range n {
			table := tables[rng.IntN(len(tables))]
			id := fmt.Sprintf("c%05d", i)
			b, a := randomCard(rng, table, id, true), randomCard(rng, table, id, true)
			if n > 100 { // a big step is many cards making the same move, so that keys grow past a piece
				b, a = randomCard(rng, Work, id, true), randomCard(rng, Work, id, true)
				b.Row, a.Row, b.Col, a.Col = "s1", "s1", Waiting, Ready
				b.Fields["kind"], a.Fields["kind"] = "primary", "primary"
				delete(b.Fields, "open")
				delete(b.Fields, "refused")
				delete(a.Fields, "refused")
				delete(a.Fields, "attempt")
			}
			if rng.IntN(10) == 0 {
				b = nil
			} else if rng.IntN(10) == 0 {
				a = nil
			}
			changes = append(changes, IndexChange{b, a})
			before, after = append(before, b), append(after, a)
		}
		start, err := IndexDefinition(before)
		if err != nil {
			t.Fatal(err)
		}
		one := cloneIndexes(start)
		for _, ch := range changes {
			ops, err := IndexOps(ch.Before, ch.After)
			if err != nil {
				t.Fatal(err)
			}
			one.Apply(ops)
		}
		ops, err := StepIndexOps(changes)
		if err != nil {
			t.Fatal(err)
		}
		folded := cloneIndexes(start)
		folded.Apply(ops)
		if diff := folded.Diff(one); len(diff) > 0 {
			t.Fatalf("step %d of %d cards: the folded ops differ from the cards' ops one by one:\n  %s", step, n, strings.Join(diff, "\n  "))
		}
		afterDef, err := IndexDefinition(after)
		if err != nil {
			t.Fatal(err)
		}
		if diff := withoutWait(folded).Diff(withoutWait(afterDef)); len(diff) > 0 {
			t.Fatalf("step %d of %d cards: the indexes after the step differ from their definition:\n  %s", step, n, strings.Join(diff, "\n  "))
		}
		perKey := map[IndexKey]int{}
		for _, o := range ops {
			perKey[o.Key]++
			if len(o.Rem) > IndexPiece || len(o.Add) > IndexPiece {
				t.Fatalf("step %d: a piece of %s holds %d removals and %d adds", step, o.Key, len(o.Rem), len(o.Add))
			}
		}
		if n > 2*IndexPiece && perKey[IndexKey{IndexFresh, "s1"}] < 3 {
			t.Fatalf("step %d of %d cards: fresh:s1 was written in %d pieces", step, n, perKey[IndexKey{IndexFresh, "s1"}])
		}
	}
}
