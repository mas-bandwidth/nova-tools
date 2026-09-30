package sprint

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Cards for the index tests: iw is a primary or a sentinel in the work table,
// ifl a work card in the fleet table, ird a read card in the readers table and
// ictl a stream's control card in the merge table. kv is field, value, field,
// value: a field given "" is left out.

func ifields(kv ...string) map[string]string {
	f := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			f[kv[i]] = kv[i+1]
		}
	}
	return f
}

func iw(id, stream, col string, score float64, kv ...string) *IndexCard {
	return &IndexCard{Table: Work, ID: id, Row: stream, Col: col, Score: score, Fields: ifields(kv...)}
}

func ifl(id, member, col string, kv ...string) *IndexCard {
	return &IndexCard{Table: Fleet, ID: id, Row: member, Col: col, Fields: ifields(kv...)}
}

func ird(id, reader, col string, kv ...string) *IndexCard {
	return &IndexCard{Table: Readers, ID: id, Row: reader, Col: col, Fields: ifields(kv...)}
}

func ictl(stream string, kv ...string) *IndexCard {
	return &IndexCard{Table: Merge, ID: CtlID(stream), Row: stream, Col: Ctl, Fields: ifields(kv...)}
}

// ixWith is a copy of the card with the fields changed; a field given "" goes.
func ixWith(c *IndexCard, kv ...string) *IndexCard {
	n := *c
	n.Fields = map[string]string{}
	for k, v := range c.Fields {
		n.Fields[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			delete(n.Fields, kv[i])
		} else {
			n.Fields[kv[i]] = kv[i+1]
		}
	}
	return &n
}

// ixAt is a copy of the card moved to a column.
func ixAt(c *IndexCard, col string, kv ...string) *IndexCard {
	n := ixWith(c, kv...)
	n.Col = col
	return n
}

// ixUnplaced is the card kept with no place, as a drop leaves it.
func ixUnplaced(c *IndexCard) *IndexCard {
	n := ixWith(c)
	n.Row, n.Col = "", ""
	return n
}

// opLines is the ops of the change, one line each; the test fails on a refusal.
func opLines(t *testing.T, before, after *IndexCard) []string {
	t.Helper()
	ops, err := IndexOps(before, after)
	if err != nil {
		t.Fatalf("IndexOps refused: %v", err)
	}
	out := []string{}
	for _, o := range ops {
		out = append(out, o.String())
	}
	return out
}

func wantLines(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got == nil {
		got = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

// lifecycleKey names a move of the lifecycle: "waiting->ready resolve".
func lifecycleKey(m Move) string { return fmt.Sprintf("%s->%s %s", m.From, m.To, m.Verb) }

// One row per index: the move that adds a card to it and the move that
// removes it, with the ops each makes. A move is a row of the lifecycle
// (named as lifecycleKey names it) or, for a change no lifecycle row makes, a
// named field change or a move of another table.
type indexMove struct {
	move          string
	before, after *IndexCard
	ops           []string
}

type indexRow struct {
	index         string // an index, or "due:<kind>"
	adds, removes indexMove
}

// opTouches says one of the lines is an op on the row's index that does what
// the sign says (+ adds a member, - removes one); a due kind is found by its
// member's prefix.
func opTouches(lines []string, index, sign string) bool {
	for _, l := range lines {
		if kind, due := strings.CutPrefix(index, "due:"); due {
			if strings.HasPrefix(l, "due ") && strings.Contains(l, " "+sign+kind+":") {
				return true
			}
		} else if strings.HasPrefix(l, index+":") && strings.Contains(l, " "+sign) {
			return true
		}
	}
	return false
}

var indexRows = []indexRow{
	{index: IndexSent,
		adds:    indexMove{"admit: a sentinel is created waiting", nil, iw("g1", "s1", Waiting, 50, "kind", "sentinel"), []string{"sent:s1 +g1@50"}},
		removes: indexMove{"waiting->landed release", iw("g1", "s1", Waiting, 50, "kind", "sentinel"), iw("g1", "s1", Landed, 50, "kind", "sentinel"), []string{"sent:s1 -g1"}}},
	{index: IndexElig,
		adds: indexMove{"ready->waiting add", iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "0"), iw("p1", "s1", Waiting, 10, "kind", "primary", "attempt", "0"),
			[]string{"elig:s1 +p1@10", "fresh:s1 -p1"}},
		removes: indexMove{"waiting->ready resolve", iw("p1", "s1", Waiting, 10, "kind", "primary"), iw("p1", "s1", Ready, 10, "kind", "primary"),
			[]string{"elig:s1 -p1", "fresh:s1 +p1@10"}}},
	{index: IndexFresh,
		adds: indexMove{"waiting->ready resolve", iw("p1", "s1", Waiting, 10, "kind", "primary"), iw("p1", "s1", Ready, 10, "kind", "primary"),
			[]string{"elig:s1 -p1", "fresh:s1 +p1@10"}},
		removes: indexMove{"ready->working deal", iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "0"), iw("p1", "s1", Working, 10, "kind", "primary", "attempt", "1"),
			[]string{"fresh:s1 -p1"}}},
	{index: IndexAgain,
		adds: indexMove{"working->ready fleet down", iw("p1", "s1", Working, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1"),
			[]string{"again:s1 +p1@10"}},
		removes: indexMove{"ready->working deal", iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Working, 10, "kind", "primary", "attempt", "2"),
			[]string{"again:s1 -p1"}}},
	// wait:<n>: IndexOps never adds (the intent waitfor does, at admission); it removes a card that leaves
	// waiting, from every need it names.
	{index: IndexWait,
		adds: indexMove{"admit: a card is created waiting on two needs (the intent waitfor adds, not IndexOps)", nil,
			iw("p3", "s1", Waiting, 30, "kind", "primary", "needs", "p1,p2", "open", "2"), nil},
		removes: indexMove{"drop: a waiting card with open needs leaves the table", iw("p3", "s1", Waiting, 30, "kind", "primary", "needs", "p1,p2", "open", "2"),
			ixUnplaced(iw("p3", "s1", Waiting, 30, "kind", "primary", "needs", "p1,p2", "open", "2")), []string{"wait:p1 -p3", "wait:p2 -p3"}}},
	{index: "due:untaken",
		adds: indexMove{"fleet: a work card is dealt to a member", nil, ifl("p1.w1", "m1", Ready, "kind", "work", "due_untaken", "900"),
			[]string{"due +untaken:p1.w1@900"}},
		removes: indexMove{"fleet: the member takes it", ifl("p1.w1", "m1", Ready, "kind", "work", "due_untaken", "900"),
			ifl("p1.w1", "m1", Working, "kind", "work", "due_untaken", "", "due_unfinished", "1200"),
			[]string{"due -untaken:p1.w1 +unfinished:p1.w1@1200"}}},
	{index: "due:unfinished",
		adds: indexMove{"fleet: the member takes a work card", ifl("p1.w1", "m1", Ready, "kind", "work", "due_untaken", "900"),
			ifl("p1.w1", "m1", Working, "kind", "work", "due_untaken", "", "due_unfinished", "1200"),
			[]string{"due -untaken:p1.w1 +unfinished:p1.w1@1200"}},
		removes: indexMove{"fleet: the member finishes it (its field is still there)", ifl("p1.w1", "m1", Working, "kind", "work", "due_unfinished", "1200"),
			ifl("p1.w1", "m1", DoneOK, "kind", "work", "due_unfinished", "1200"), []string{"due -unfinished:p1.w1"}}},
	{index: "due:unbegun",
		adds: indexMove{"readers: a read card is asked", nil, ird("p1.r1.ra", "ra", Asked, "kind", "read", "due_unbegun", "600"),
			[]string{"due +unbegun:p1.r1.ra@600"}},
		removes: indexMove{"readers: the reader begins it", ird("p1.r1.ra", "ra", Asked, "kind", "read", "due_unbegun", "600"),
			ird("p1.r1.ra", "ra", Reading, "kind", "read", "due_unbegun", "", "due_unreported", "1500"),
			[]string{"due -unbegun:p1.r1.ra +unreported:p1.r1.ra@1500"}}},
	{index: "due:unreported",
		adds: indexMove{"readers: the reader begins a read card", ird("p1.r1.ra", "ra", Asked, "kind", "read", "due_unbegun", "600"),
			ird("p1.r1.ra", "ra", Reading, "kind", "read", "due_unbegun", "", "due_unreported", "1500"),
			[]string{"due -unbegun:p1.r1.ra +unreported:p1.r1.ra@1500"}},
		removes: indexMove{"readers: the reader reports ok", ird("p1.r1.ra", "ra", Reading, "kind", "read", "due_unreported", "1500"),
			ird("p1.r1.ra", "ra", OK, "kind", "read", "due_unreported", ""), []string{"due -unreported:p1.r1.ra"}}},
	{index: "due:mergeidle",
		adds: indexMove{"merge: a card is accepted into the stream (the control card's field is set)", ictl("s1", "state", StreamWaiting),
			ictl("s1", "state", StreamMerging, "due_mergeidle", "1800"), []string{"due +mergeidle:s1@1800"}},
		removes: indexMove{"merge: nothing is queued or stuck (the field is unset)", ictl("s1", "state", StreamMerging, "due_mergeidle", "1800"),
			ictl("s1", "state", StreamWaiting, "due_mergeidle", ""), []string{"due -mergeidle:s1"}}},
}

func TestEveryIndexHasAMoveThatAddsAndAMoveThatRemoves(t *testing.T) {
	t.Parallel()
	lifecycle := map[string]bool{}
	for _, m := range Moves {
		lifecycle[lifecycleKey(m)] = true
	}
	named := map[string]bool{}
	for _, row := range indexRows {
		named[row.index] = true
		for _, side := range []struct {
			name string
			mv   indexMove
		}{{"adds", row.adds}, {"removes", row.removes}} {
			mv := side.mv
			if !lifecycle[mv.move] && !strings.Contains(mv.move, ": ") {
				t.Errorf("%s %s: %q is neither a row of the lifecycle nor a named change", row.index, side.name, mv.move)
			}
			wantLines(t, row.index+" "+side.name+" by "+mv.move, opLines(t, mv.before, mv.after), mv.ops...)
			// Each move makes an op on its index, by its own sign: an add a +, a removal a -.
			// wait is the exception on adds: IndexOps never adds to it.
			sign := map[string]string{"adds": "+", "removes": "-"}[side.name]
			if row.index == IndexWait && side.name == "adds" {
				if len(mv.ops) != 0 {
					t.Errorf("wait adds: IndexOps made %q; the intent waitfor adds, never IndexOps", mv.ops)
				}
				continue
			}
			if !opTouches(mv.ops, row.index, sign) {
				t.Errorf("%s %s: %q has no %s on the index", row.index, side.name, mv.ops, sign)
			}
		}
	}
	if !named[IndexWait] {
		t.Error("no row for wait")
	}
	for _, d := range IndexDefs {
		if !named[d.Index] {
			t.Errorf("no row for the index %s", d.Index)
		}
	}
	for _, k := range DueKinds {
		if !named["due:"+k.Kind] {
			t.Errorf("no row for the due kind %s", k.Kind)
		}
	}
	if len(indexRows) != len(named) {
		t.Errorf("%d rows name %d indexes: one row each", len(indexRows), len(named))
	}
}

// What every move of the lifecycle does to the indexes, one row for each row
// of Moves, with the card as it stands before and after. A row of Moves
// without a row here, or a row here without a move, fails.
var lifecycleEffects = map[string]struct {
	before, after *IndexCard
	ops           []string
}{
	"waiting->ready resolve": {iw("p1", "s1", Waiting, 10, "kind", "primary"), iw("p1", "s1", Ready, 10, "kind", "primary"),
		[]string{"elig:s1 -p1", "fresh:s1 +p1@10"}},
	"ready->working deal": {iw("p1", "s1", Ready, 10, "kind", "primary"), iw("p1", "s1", Working, 10, "kind", "primary", "attempt", "1", "work", "p1.w1"),
		[]string{"fresh:s1 -p1"}},
	"working->review finish": {iw("p1", "s1", Working, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Review, 10, "kind", "primary", "attempt", "1", "result", "ok"),
		nil},
	"working->ready fleet down": {iw("p1", "s1", Working, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1"),
		[]string{"again:s1 +p1@10"}},
	"review->merging accept": {iw("p1", "s1", Review, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Merging, 10, "kind", "primary", "attempt", "1"),
		nil},
	"review->working rework": {iw("p1", "s1", Review, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Working, 10, "kind", "primary", "attempt", "2"),
		nil},
	"review->ready rework": {iw("p1", "s1", Review, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1"),
		[]string{"again:s1 +p1@10"}},
	"merging->review return": {iw("p1", "s1", Merging, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Review, 10, "kind", "primary", "attempt", "1"),
		nil},
	"merging->landed merge": {iw("p1", "s1", Merging, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Landed, 10, "kind", "primary", "attempt", "1"),
		nil},
	"waiting->landed release": {iw("g1", "s1", Waiting, 50, "kind", "sentinel", "needs", "p9"), iw("g1", "s1", Landed, 50, "kind", "sentinel", "needs", "p9"),
		[]string{"sent:s1 -g1", "wait:p9 -g1"}},
	"ready->waiting add": {iw("p1", "s1", Ready, 10, "kind", "primary"), iw("p1", "s1", Waiting, 10, "kind", "primary"),
		[]string{"elig:s1 +p1@10", "fresh:s1 -p1"}},
}

func TestEveryLifecycleMoveHasItsEffectOnTheIndexes(t *testing.T) {
	t.Parallel()
	used := map[string]bool{}
	for _, m := range Moves {
		k := lifecycleKey(m)
		used[k] = true
		e, ok := lifecycleEffects[k]
		if !ok {
			t.Errorf("the lifecycle has the move %q and this table does not say what it does to the indexes", k)
			continue
		}
		if e.before.Col != m.From || e.after.Col != m.To {
			t.Errorf("%s: the row moves %s -> %s", k, e.before.Col, e.after.Col)
		}
		wantLines(t, k, opLines(t, e.before, e.after), e.ops...)
	}
	for k := range lifecycleEffects {
		if !used[k] {
			t.Errorf("this table has %q and the lifecycle does not", k)
		}
	}
}

func TestAChangeThatTouchesNoIndexedFieldReturnsNoOperation(t *testing.T) {
	t.Parallel()
	indexed := map[string]bool{}
	for _, f := range IndexFields() {
		indexed[f] = true
	}
	// Fields no index reads, with a value to set: setting each on a card in every
	// state that has a member, and one that has none, changes nothing an index holds.
	plain := []string{"head", "brief", "result", "work", "gen", "member", "redeals", "admitted", "fix", "dealt", "asked", "state", "since", "stream"}
	for _, f := range plain {
		if indexed[f] {
			t.Fatalf("%s is a field the derivation reads", f)
		}
	}
	cards := []*IndexCard{
		iw("g1", "s1", Waiting, 50, "kind", "sentinel", "needs", "p1", "open", "1"),
		iw("p1", "s1", Waiting, 10, "kind", "primary"),
		iw("p2", "s1", Waiting, 20, "kind", "primary", "needs", "p1", "open", "1"),
		iw("p3", "s1", Ready, 30, "kind", "primary", "attempt", "0"),
		iw("p4", "s1", Ready, 40, "kind", "primary", "attempt", "2"),
		iw("p5", "s1", Working, 50, "kind", "primary", "attempt", "1"),
		iw("p6", "s1", Review, 60, "kind", "primary", "attempt", "1"),
		iw("p7", "s1", Merging, 70, "kind", "primary", "attempt", "1"),
		iw("p8", "s1", Landed, 80, "kind", "primary", "attempt", "1"),
		ixUnplaced(iw("p9", "s1", Waiting, 90, "kind", "primary")),
		ifl("p5.w1", "m1", Ready, "kind", "work", "due_untaken", "900"),
		ifl("p5.w1", "m1", Working, "kind", "work", "due_unfinished", "1200"),
		ifl("p5.w1", "m1", Withdrawn, "kind", "work"),
		ird("p6.r1.ra", "ra", Asked, "kind", "read", "due_unbegun", "600"),
		ird("p6.r1.ra", "ra", Reading, "kind", "read", "due_unreported", "1500"),
		ird("p6.r1.ra", "ra", Broken, "kind", "read"),
		ictl("s1", "due_mergeidle", "1800"),
		ictl("s1"),
	}
	for _, c := range cards {
		for _, f := range plain {
			got := opLines(t, c, ixWith(c, f, "changed"))
			wantLines(t, fmt.Sprintf("%s card %s at %s: %s set", c.Table, c.ID, c.Col, f), got)
			wantLines(t, fmt.Sprintf("%s card %s at %s: %s unset", c.Table, c.ID, c.Col, f), opLines(t, ixWith(c, f, "changed"), c))
		}
	}
	// No card at all, and a card that stays out of every index.
	wantLines(t, "nothing to nothing", opLines(t, nil, nil))
	wantLines(t, "a kept record with no place, unchanged", opLines(t, cards[9], ixWith(cards[9], "head", "abc")))
	wantLines(t, "a card that is created with no place", opLines(t, nil, cards[9]))
	wantLines(t, "a landed card is forgotten", opLines(t, cards[8], nil))
}

// A change that touches an indexed field but leaves every membership and score
// as it was returns no operation either.
func TestAnIndexedFieldThatChangesNoMembershipReturnsNoOperation(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		what          string
		before, after *IndexCard
	}{
		{"open counts down and is still above 0", iw("p2", "s1", Waiting, 20, "kind", "primary", "open", "2"), iw("p2", "s1", Waiting, 20, "kind", "primary", "open", "1")},
		{"a sentinel's open changes (it is in sent, and elig reads no sentinel)", iw("g1", "s1", Waiting, 50, "kind", "sentinel", "open", "1"), iw("g1", "s1", Waiting, 50, "kind", "sentinel", "open", "0")},
		{"attempt rises on a card already dealt before", iw("p4", "s1", Ready, 40, "kind", "primary", "attempt", "1"), iw("p4", "s1", Ready, 40, "kind", "primary", "attempt", "2")},
		{"bound is set on a card that is waiting", iw("p2", "s1", Waiting, 20, "kind", "primary", "open", "1"), iw("p2", "s1", Waiting, 20, "kind", "primary", "open", "1", "bound", "3")},
		{"refused is set on a card that is in review", iw("p6", "s1", Review, 60, "kind", "primary"), iw("p6", "s1", Review, 60, "kind", "primary", "refused", "deal: no room")},
		{"refused is set on a sentinel: it stays in sent", iw("g1", "s1", Waiting, 50, "kind", "sentinel"), iw("g1", "s1", Waiting, 50, "kind", "sentinel", "refused", "resolve: why")},
		{"a card is rescored while it is in review", iw("p6", "s1", Review, 60, "kind", "primary"), iw("p6", "s1", Review, 61, "kind", "primary")},
		{"a fleet card moves to another member, its due entry unchanged", ifl("p5.w1", "m1", Ready, "kind", "work", "due_untaken", "900"), ifl("p5.w1", "m2", Ready, "kind", "work", "due_untaken", "900")},
		{"a fleet card's due_unfinished is set while it is ready", ifl("p5.w1", "m1", Ready, "kind", "work"), ifl("p5.w1", "m1", Ready, "kind", "work", "due_unfinished", "1200")},
		{"a read card's due_unreported is set while it is asked", ird("p6.r1.ra", "ra", Asked, "kind", "read"), ird("p6.r1.ra", "ra", Asked, "kind", "read", "due_unreported", "1500")},
		{"a control card's other fields change", ictl("s1", "state", StreamWaiting), ictl("s1", "state", StreamMerging)},
		{"a due field is set on a control card that is not in the merge table's ctl column", func() *IndexCard { c := ictl("s1"); c.Col = Queued; return c }(), func() *IndexCard { c := ictl("s1", "due_mergeidle", "5"); c.Col = Queued; return c }()},
		{"a waiting card moves from stream to stream: its needs are still named, and it stays waiting on them", iw("p2", "s1", Waiting, 20, "kind", "primary", "needs", "p1", "open", "1"), iw("p2", "s2", Waiting, 20, "kind", "primary", "needs", "p1", "open", "1")},
	} {
		wantLines(t, row.what, opLines(t, row.before, row.after))
	}
}

func TestAScoreChangeRescoresEveryIndexTheCardIsIn(t *testing.T) {
	t.Parallel()
	wantLines(t, "elig", opLines(t, iw("p1", "s1", Waiting, 10, "kind", "primary"), iw("p1", "s1", Waiting, 15, "kind", "primary")), "elig:s1 +p1@15")
	wantLines(t, "sent", opLines(t, iw("g1", "s1", Waiting, 50, "kind", "sentinel"), iw("g1", "s1", Waiting, 5, "kind", "sentinel")), "sent:s1 +g1@5")
	wantLines(t, "fresh", opLines(t, iw("p1", "s1", Ready, 10, "kind", "primary"), iw("p1", "s1", Ready, 12.5, "kind", "primary")), "fresh:s1 +p1@12.5")
	wantLines(t, "again", opLines(t, iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Ready, 3, "kind", "primary", "attempt", "1")), "again:s1 +p1@3")
	wantLines(t, "a due field", opLines(t, ifl("w1", "m1", Ready, "due_untaken", "900"), ifl("w1", "m1", Ready, "due_untaken", "1800")), "due +untaken:w1@1800")
}

func TestAMoveToAnotherStreamMovesTheMemberBetweenKeys(t *testing.T) {
	t.Parallel()
	wantLines(t, "elig", opLines(t, iw("p1", "s1", Waiting, 10, "kind", "primary"), iw("p1", "s2", Waiting, 10, "kind", "primary")),
		"elig:s1 -p1", "elig:s2 +p1@10")
}

func TestARefusedCardIsOutOfEligFreshAndAgainAndStaysInSent(t *testing.T) {
	t.Parallel()
	r := []string{"refused", "deal: no room"}
	wantLines(t, "elig", opLines(t, iw("p1", "s1", Waiting, 10, "kind", "primary"), ixWith(iw("p1", "s1", Waiting, 10, "kind", "primary"), r...)), "elig:s1 -p1")
	wantLines(t, "fresh", opLines(t, iw("p1", "s1", Ready, 10, "kind", "primary"), ixWith(iw("p1", "s1", Ready, 10, "kind", "primary"), r...)), "fresh:s1 -p1")
	wantLines(t, "again", opLines(t, iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1"), ixWith(iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1"), r...)), "again:s1 -p1")
	wantLines(t, "cleared: back in again", opLines(t, ixWith(iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1"), r...), iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1")), "again:s1 +p1@10")
}

func TestABoundCardIsOutOfAgain(t *testing.T) {
	t.Parallel()
	before := iw("p1", "s1", Working, 10, "kind", "primary", "attempt", "3")
	wantLines(t, "withdrawn at its bound", opLines(t, before, ixAt(ixWith(before, "bound", "5"), Ready)))
	wantLines(t, "bound is unset by a rework with a fix", opLines(t, ixAt(ixWith(before, "bound", "3"), Ready), ixAt(before, Ready)), "again:s1 +p1@10")
}

func TestASentinelIsNeverInEligOrFresh(t *testing.T) {
	t.Parallel()
	wantLines(t, "a sentinel with no needs", opLines(t, nil, iw("g1", "s1", Waiting, 50, "kind", "sentinel")), "sent:s1 +g1@50")
	wantLines(t, "a sentinel in ready is out of fresh by kind", opLines(t, nil, iw("g1", "s1", Ready, 50, "kind", "sentinel")))
}

func TestOnlyACardWithNeedsLeavingWaitingIsRemovedFromWait(t *testing.T) {
	t.Parallel()
	c := iw("p3", "s1", Waiting, 30, "kind", "primary", "needs", "p1, p2,p1", "open", "2")
	wantLines(t, "left waiting for ready, a need named twice once", opLines(t, c, ixAt(c, Ready)), "fresh:s1 +p3@30", "wait:p1 -p3", "wait:p2 -p3")
	wantLines(t, "staying waiting", opLines(t, c, ixWith(c, "open", "1")))
	wantLines(t, "entering waiting from ready: no wait member, the intent waitfor adds it", opLines(t, ixAt(c, Ready), c), "fresh:s1 -p3")
	wantLines(t, "a card that is not in the work table has no waiters", opLines(t,
		&IndexCard{Table: Fleet, ID: "p3", Row: "m1", Col: Waiting, Fields: c.Fields}, nil))
	wantLines(t, "a waiting card with no needs", opLines(t, iw("p4", "s1", Waiting, 40, "kind", "primary"), nil), "elig:s1 -p4")
}

// The design removes a card that leaves waiting from every wait:<n> it is in,
// which are the needs it had before the change: a change that also rewrites the
// card's needs takes it out of the wait of each of its old needs, and of none
// of the new.
func TestACardLeavingWaitingIsRemovedFromTheWaitOfItsNeedsBeforeTheChange(t *testing.T) {
	t.Parallel()
	before := iw("p5", "s1", Waiting, 50, "kind", "primary", "needs", "p1,p2", "open", "2")
	wantLines(t, "leaves for ready with other needs", opLines(t, before, ixAt(before, Ready, "needs", "p7,p8", "open", "")),
		"fresh:s1 +p5@50", "wait:p1 -p5", "wait:p2 -p5")
	wantLines(t, "leaves for ready with one of its needs and a new one", opLines(t, before, ixAt(before, Ready, "needs", "p2,p9", "open", "")),
		"fresh:s1 +p5@50", "wait:p1 -p5", "wait:p2 -p5")
	wantLines(t, "leaves for ready with no needs", opLines(t, before, ixAt(before, Ready, "needs", "", "open", "")),
		"fresh:s1 +p5@50", "wait:p1 -p5", "wait:p2 -p5")
	wantLines(t, "leaves the table, kept with no place and other needs", opLines(t, before, ixWith(ixUnplaced(before), "needs", "p7")),
		"wait:p1 -p5", "wait:p2 -p5")
	wantLines(t, "leaves the table, kept with no place and no needs", opLines(t, before, ixWith(ixUnplaced(before), "needs", "")),
		"wait:p1 -p5", "wait:p2 -p5")
	sentinel := iw("g1", "s1", Waiting, 60, "kind", "sentinel", "needs", "p3", "open", "1")
	wantLines(t, "a sentinel released with other needs", opLines(t, sentinel, ixAt(sentinel, Landed, "needs", "p4")),
		"sent:s1 -g1", "wait:p3 -g1")
	// A card that has no needs before the change has no wait to leave, whatever it names after.
	bare := iw("p6", "s1", Waiting, 70, "kind", "primary")
	wantLines(t, "no needs before, needs after", opLines(t, bare, ixAt(bare, Ready, "needs", "p1", "open", "1")),
		"elig:s1 -p6", "fresh:s1 +p6@70")
}

func TestIndexOpsRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		what          string
		before, after *IndexCard
		want          string
	}{
		{"open is not a number, on a waiting card", nil, iw("p1", "s1", Waiting, 10, "kind", "primary", "open", "two"), "field open is \"two\""},
		{"open is not a number, on a waiting sentinel", nil, iw("g1", "s1", Waiting, 10, "kind", "sentinel", "open", "x"), "field open is \"x\""},
		{"attempt is not a number, on a ready card", iw("p1", "s1", Ready, 10, "kind", "primary", "attempt", "1.5"), nil, "field attempt is \"1.5\""},
		{"a due field is not a number", ifl("w1", "m1", Working, "due_unfinished", "soon"), nil, "field due_unfinished is \"soon\""},
		{"a due field on a control card", nil, ictl("s1", "due_mergeidle", "1e3"), "field due_mergeidle is \"1e3\""},
		{"a waiting card placed with no row", nil, iw("p1", "", Waiting, 10, "kind", "primary"), "card p1 is placed at waiting with no row"},
		{"a control card with its deadline and no row", nil, ictl("", "due_mergeidle", "5"), "is placed at ctl with no row"},
		{"two different cards", iw("p1", "s1", Waiting, 10), iw("p2", "s1", Waiting, 10), "one card is changed at a time"},
		{"the same id in two tables", iw("p1", "s1", Waiting, 10), ifl("p1", "m1", Ready), "one card is changed at a time"},
	} {
		ops, err := IndexOps(row.before, row.after)
		if err == nil || !strings.Contains(err.Error(), row.want) {
			t.Errorf("%s: error %v, want one naming %q", row.what, err, row.want)
		}
		if ops != nil {
			t.Errorf("%s: ops %v returned beside the refusal", row.what, ops)
		}
	}
	// A field no definition reads is never parsed: a malformed open on a card in review is not read.
	wantLines(t, "open on a card in review", opLines(t, iw("p1", "s1", Review, 10, "kind", "primary", "open", "many"), nil))
	// A card placed with no row that no index has is not refused; one that is in none of them has no key to make.
	wantLines(t, "a card in review with no row", opLines(t, nil, iw("p1", "", Review, 10, "kind", "primary")))
	wantLines(t, "a fleet card with no row and no due field", opLines(t, nil, ifl("w1", "", Ready)))
}

func TestTheDerivationTableIsWellFormed(t *testing.T) {
	t.Parallel()
	tables := map[string][]string{
		Work:    {Waiting, Ready, Working, Review, Merging, Landed},
		Readers: {Asked, Reading, OK, Broken},
		Merge:   {Queued, Merged, Stuck, Ctl},
		Fleet:   {Ready, Working, Withdrawn, DoneOK, DoneFailed, Ctl},
	}
	col := func(table, c string) bool {
		for _, x := range tables[table] {
			if x == c {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	for _, d := range IndexDefs {
		if seen[d.Index] {
			t.Errorf("two rows for the index %s", d.Index)
		}
		seen[d.Index] = true
		if !col(d.Table, d.Col) {
			t.Errorf("%s: no column %s in the %s table", d.Index, d.Col, d.Table)
		}
		if d.Index == IndexWait || d.Index == IndexDue {
			t.Errorf("%s is not derived from one card's fields, and has no row here", d.Index)
		}
		for _, w := range d.Where {
			// A row's tests all run: a card built to satisfy nothing and one built to satisfy everything both read.
			if _, err := w.holds(iw("p", "s", d.Col, 1)); err != nil {
				t.Errorf("%s: condition on %s: %v", d.Index, w.Field, err)
			}
		}
	}
	kinds := map[string]bool{}
	for _, k := range DueKinds {
		if kinds[k.Kind] {
			t.Errorf("two rows for the due kind %s", k.Kind)
		}
		kinds[k.Kind] = true
		if !col(k.Table, k.Col) {
			t.Errorf("%s: no column %s in the %s table", k.Kind, k.Col, k.Table)
		}
		if k.Field != "due_"+k.Kind {
			t.Errorf("%s reads %s, not due_%s", k.Kind, k.Field, k.Kind)
		}
	}
	if _, err := (IndexCond{"x", "sideways", "1"}).holds(iw("p", "s", Waiting, 1)); err == nil {
		t.Error("a test the table does not know was not refused")
	}
	if _, err := (IndexCond{"x", FieldNum, "one"}).holds(iw("p", "s", Waiting, 1)); err == nil {
		t.Error("a value that is not a number was not refused")
	}
	want := []string{"attempt", "bound", "due_mergeidle", "due_unbegun", "due_unfinished", "due_unreported", "due_untaken", "kind", "needs", "open", "refused"}
	if got := IndexFields(); !reflect.DeepEqual(got, want) {
		t.Errorf("IndexFields = %v, want %v", got, want)
	}
}

func TestAKeyIsStoredUnderTheDeploymentPrefixAndTheEpoch(t *testing.T) {
	t.Parallel()
	n := Names{Prefix: "dev-"}
	for _, row := range []struct {
		key   IndexKey
		epoch uint64
		want  string
	}{
		{IndexKey{IndexElig, "s1"}, 0, "dev-sprint:elig:s1"},
		{IndexKey{IndexWait, "p1"}, 3, "dev-sprint:wait:p1@3"},
		{IndexKey{IndexDue, ""}, 0, "dev-sprint:due"},
		{IndexKey{IndexDue, ""}, 2, "dev-sprint:due@2"},
	} {
		if got := row.key.Stored(n, row.epoch); got != row.want {
			t.Errorf("%v at epoch %d: %q, want %q", row.key, row.epoch, got, row.want)
		}
	}
}

func TestNewIndexCardReadsACard(t *testing.T) {
	t.Parallel()
	if NewIndexCard(Work, nil) != nil {
		t.Error("a nil card is not a card")
	}
	c := &Card{ID: "p1", Row: "s1", Col: Ready, Score: 7, Rev: 4, Fields: map[string]string{"kind": "primary"}}
	got := NewIndexCard(Work, c)
	want := &IndexCard{Table: Work, ID: "p1", Row: "s1", Col: Ready, Score: 7, Fields: map[string]string{"kind": "primary"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	wantLines(t, "a card of the snapshot's table", opLines(t, nil, got), "fresh:s1 +p1@7")
}

// The ops of a step: every card's ops folded into one op per key, and cut at a
// thousand members.
func TestAStepFoldsItsCardsIntoOneOpPerKey(t *testing.T) {
	t.Parallel()
	changes := []IndexChange{
		{iw("p2", "s1", Waiting, 20, "kind", "primary"), iw("p2", "s1", Ready, 20, "kind", "primary")},
		{iw("p1", "s1", Waiting, 10, "kind", "primary"), iw("p1", "s1", Ready, 10, "kind", "primary")},
		{iw("p3", "s2", Waiting, 30, "kind", "primary"), iw("p3", "s2", Ready, 30, "kind", "primary")},
		{nil, ifl("w1", "m1", Ready, "due_untaken", "900")},
		{ifl("w2", "m1", Ready, "due_untaken", "800"), nil},
		{nil, nil},
	}
	ops, err := StepIndexOps(changes)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range ops {
		got = append(got, o.String())
	}
	wantLines(t, "step", got,
		"due -untaken:w2 +untaken:w1@900",
		"elig:s1 -p1 -p2",
		"elig:s2 -p3",
		"fresh:s1 +p1@10 +p2@20",
		"fresh:s2 +p3@30")
	if _, err := StepIndexOps([]IndexChange{{iw("p1", "s1", Waiting, 1), iw("p1", "s1", Ready, 1)}, {iw("p1", "s1", Ready, 1), nil}}); err == nil || !strings.Contains(err.Error(), "changed twice") {
		t.Errorf("a card changed twice in a step: %v", err)
	}
	if _, err := StepIndexOps([]IndexChange{{nil, iw("p1", "s1", Waiting, 1, "open", "x")}}); err == nil {
		t.Error("a malformed field in a step was not refused")
	}
}

// A step names a card by its table and its id. Real steps change two cards of
// one id in two tables (accept moves the primary p1 in the work table and
// creates the merge card p1 in the merge table), so that is one step of two
// cards; the same card of one table named twice is one card changed twice.
func TestAStepThatNamesOneIdInTwoTablesIsAcceptedAndOneCardTwiceIsRefused(t *testing.T) {
	t.Parallel()
	stepLines := func(changes []IndexChange) ([]string, error) {
		ops, err := StepIndexOps(changes)
		var out []string
		for _, o := range ops {
			out = append(out, o.String())
		}
		return out, err
	}
	mergeCard := func(id string) *IndexCard {
		return &IndexCard{Table: Merge, ID: id, Row: "s1", Col: Queued, Score: 10, Fields: ifields("kind", "merge", "primary", id, "stream", "s1")}
	}

	// The accept of p1: the work card p1 moves review -> merging, the merge card p1 is created, and the
	// stream's control card starts merging.
	accept := []IndexChange{
		{iw("p1", "s1", Review, 10, "kind", "primary", "attempt", "1"), iw("p1", "s1", Merging, 10, "kind", "primary", "attempt", "1")},
		{nil, mergeCard("p1")},
		{ictl("s1", "state", StreamWaiting), ictl("s1", "state", StreamMerging, "due_mergeidle", "1800")},
	}
	got, err := stepLines(accept)
	if err != nil {
		t.Fatalf("an accept step, the work card p1 and the merge card p1 in one step, was refused: %v", err)
	}
	wantLines(t, "accept", got, "due +mergeidle:s1@1800")

	// The same id in two tables, each card with ops of its own: both cards' ops are in the step, in either order.
	work := IndexChange{iw("p1", "s1", Waiting, 10, "kind", "primary"), iw("p1", "s1", Ready, 10, "kind", "primary")}
	fleet := IndexChange{nil, ifl("p1", "m1", Ready, "kind", "work", "due_untaken", "900")}
	readers := IndexChange{nil, ird("p1", "ra", Asked, "kind", "read", "due_unbegun", "600")}
	for _, order := range [][]IndexChange{{work, fleet, readers}, {readers, fleet, work}} {
		got, err := stepLines(order)
		if err != nil {
			t.Fatalf("one id in three tables was refused: %v", err)
		}
		wantLines(t, "one id in the work, fleet and readers tables", got,
			"due +unbegun:p1@600 +untaken:p1@900", "elig:s1 -p1", "fresh:s1 +p1@10")
	}

	// The same card of one table named twice is refused, whatever else the step holds and whichever of its
	// changes come first, and the refusal names the table it is in.
	for _, row := range []struct {
		what    string
		changes []IndexChange
		want    string
	}{
		{"a merge card created twice", []IndexChange{{nil, mergeCard("p1")}, {nil, mergeCard("p1")}}, "merge card p1 is changed twice"},
		{"a merge card created and then removed", []IndexChange{{nil, mergeCard("p1")}, {mergeCard("p1"), nil}}, "merge card p1 is changed twice"},
		{"a fleet card twice", []IndexChange{fleet, fleet}, "fleet card p1 is changed twice"},
		{"a work card twice with another table's card of its id between", []IndexChange{work, fleet, {iw("p1", "s1", Ready, 10, "kind", "primary"), nil}}, "work card p1 is changed twice"},
		{"the accept with its merge card named twice", append(append([]IndexChange{}, accept...), IndexChange{mergeCard("p1"), nil}), "merge card p1 is changed twice"},
	} {
		ops, err := StepIndexOps(row.changes)
		if err == nil || !strings.Contains(err.Error(), row.want) {
			t.Errorf("%s: error %v, want one naming %q", row.what, err, row.want)
		}
		if ops != nil {
			t.Errorf("%s: ops %v returned beside the refusal", row.what, ops)
		}
	}
}

func TestAStepCutsEveryKeyAtAThousandMembers(t *testing.T) {
	t.Parallel()
	const n = 2*IndexPiece + 500
	var changes []IndexChange
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%05d", i)
		changes = append(changes, IndexChange{iw(id, "s1", Waiting, float64(i), "kind", "primary"), iw(id, "s1", Ready, float64(i), "kind", "primary")})
	}
	ops, err := StepIndexOps(changes)
	if err != nil {
		t.Fatal(err)
	}
	// elig:s1 loses n members and fresh:s1 gains n: three pieces each, in key order.
	var shape []string
	for _, o := range ops {
		shape = append(shape, fmt.Sprintf("%s -%d +%d", o.Key, len(o.Rem), len(o.Add)))
		if len(o.Rem) > IndexPiece || len(o.Add) > IndexPiece {
			t.Errorf("%s: a piece of %d removals and %d adds", o.Key, len(o.Rem), len(o.Add))
		}
	}
	wantLines(t, "shape", shape,
		"elig:s1 -1000 +0", "elig:s1 -1000 +0", "elig:s1 -500 +0",
		"fresh:s1 -0 +1000", "fresh:s1 -0 +1000", "fresh:s1 -0 +500")
	x := Indexes{}
	for i := 0; i < n; i++ {
		x.Apply([]IndexOp{{Key: IndexKey{IndexElig, "s1"}, Add: []Scored{{fmt.Sprintf("p%05d", i), float64(i)}}}})
	}
	x.Apply(ops)
	if len(x) != 1 || len(x[IndexKey{IndexFresh, "s1"}]) != n {
		t.Errorf("after the step: %d keys, %d fresh members", len(x), len(x[IndexKey{IndexFresh, "s1"}]))
	}
}
