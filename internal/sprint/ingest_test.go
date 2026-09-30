package sprint

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// These tests hold ingest to the design's two tables (the upper design,
// version 2, sections 2.1 and 2.2): one case for each row, asserting the exact
// keys of the line, then what the design leaves open (which must queue
// nothing), then the properties of the whole: keys do not grow with the cards
// a line names, and ingest is deterministic and does not depend on the order,
// the pages or the repeats of the events it is given.

// ingestAt is the keys of one line at seq 100, the line taken through its
// JSON and ParseEvent as the tick reads it.
func ingestAt(t *testing.T, l Line) []string {
	t.Helper()
	return ingestAll(t, map[uint64]Line{100: l})
}

// ingestAll is the key names of the lines by their seqs, in Ingest's order.
func ingestAll(t *testing.T, lines map[uint64]Line) []string {
	t.Helper()
	var seqs []uint64
	for s := range lines {
		seqs = append(seqs, s)
	}
	slices.Sort(seqs)
	var events []Event
	for _, s := range seqs {
		e, err := ParseEvent(streamID(s), lineJSON(t, lines[s]))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	return keyNames(Ingest(events))
}

func keyNames(in Ingested) []string {
	var out []string
	for _, k := range in.Keys {
		out = append(out, k.Key)
	}
	return out
}

func sameKeys(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", name, got, want)
	}
}

// workLine is a line of one card of the work table.
func workLine(card, from, to string, set map[string]string) Line {
	return Line{Kind: LineMove, Table: Work, Card: card, Primary: card, Stream: "s1", From: from, To: to, Set: set}
}

// setOf is a line of a set of cards of one table, as GroupSets leaves it: no
// one primary.
func setOf(table, from, to string, set map[string]string, cards ...string) Line {
	return Line{Kind: LineMove, Table: table, Card: cards[0], Cards: cards, Stream: "s1", From: from, To: to, Set: set}
}

func TestIngestLinesOfTheWorkTable(t *testing.T) {
	t.Parallel()
	ok := map[string]string{"result": "ok", "head": "h"}
	failed := map[string]string{"result": "failed", "head": "h"}
	tests := []struct {
		name string
		line Line
		want []string
	}{
		// 2.1: work: any change in stream s
		{"a change in place", workLine("p1", "s1:waiting", "s1:waiting", map[string]string{"open": "1"}), []string{"held:p1", "resolve:s1"}},
		{"a rescore", workLine("p1", "s1:ready", "s1:ready", map[string]string{"score": "5"}), []string{"held:p1", "resolve:s1"}},
		{"an accept, review to merging", workLine("p1", "s1:review", "s1:merging", nil), []string{"held:p1", "resolve:s1"}},
		{"a take of the primary, ready to working", workLine("p1", "s1:ready", "s1:working", nil), []string{"held:p1", "resolve:s1"}},
		{"a change in another stream", Line{Kind: LineMove, Table: Work, Card: "p9", Primary: "p9", Stream: "s7", From: "s7:ready", To: "s7:ready"},
			[]string{"held:p9", "resolve:s7"}},
		// 2.1: work: a sentinel created, moved, re-scored, landed or removed in s
		{"a sentinel created", workLine("g1", "", "s1:waiting", map[string]string{"kind": "sentinel", "score": "3"}),
			[]string{"deal", "held:g1", "resolve:s1"}},
		{"a sentinel that says it is one, landed", workLine("g1", "s1:waiting", "s1:landed", map[string]string{"kind": "sentinel"}),
			[]string{"cross", "deal", "done", "needs:g1", "resolve:s1"}},
		// 2.1: work: cards enter ready
		{"a card into ready from waiting", workLine("p1", "s1:waiting", "s1:ready", nil), []string{"deal", "held:p1", "resolve:s1"}},
		{"a card created in ready", workLine("p1", "", "s1:ready", map[string]string{"score": "1"}), []string{"deal", "held:p1", "resolve:s1"}},
		{"a card back into ready from working", workLine("p1", "s1:working", "s1:ready", nil), []string{"deal", "held:p1", "resolve:s1"}},
		{"a set of cards into ready", setOf(Work, "s1:waiting", "s1:ready", nil, "p1", "p2", "p3"), []string{"deal", "held@100", "resolve:s1"}},
		// 2.1: work: cards enter review with result ok, failed
		{"a card into review with result ok", workLine("p1", "s1:working", "s1:review", ok), []string{"ask:p1", "held:p1", "resolve:s1"}},
		{"a set of cards into review with result ok", setOf(Work, "s1:working", "s1:review", ok, "p1", "p2"), []string{"ask@100", "held@100", "resolve:s1"}},
		{"a card into review with result failed", workLine("p1", "s1:working", "s1:review", failed), []string{"held:p1", "resolve:s1", "rework:p1"}},
		{"a set of cards into review with result failed", setOf(Work, "s1:working", "s1:review", failed, "p1", "p2"), []string{"held@100", "resolve:s1", "rework@100"}},
		{"a card into review with no result (sent back from merging)", workLine("p1", "s1:merging", "s1:review", nil), []string{"held:p1", "resolve:s1"}},
		{"a card already in review with a result set again", workLine("p1", "s1:review", "s1:review", ok), []string{"held:p1", "resolve:s1"}},
		// 2.1: work: cards land
		{"a card lands", workLine("p1", "s1:merging", "s1:landed", nil), []string{"cross", "done", "needs:p1", "resolve:s1"}},
		{"a set of cards lands", setOf(Work, "s1:merging", "s1:landed", nil, "p1", "p2"), []string{"cross", "done", "needs@100", "resolve:s1"}},
		// 2.1: work: cards removed
		{"a card removed, its stream from the row it left", Line{Kind: LineMove, Table: Work, Card: "p1", From: "s1:ready", Removed: true},
			[]string{"deal", "done", "needs:p1", "resolve:s1"}},
		{"a set of cards removed", Line{Kind: LineMove, Table: Work, Card: "p1", Cards: []string{"p1", "p2"}, From: "s1:working", Removed: true},
			[]string{"deal", "done", "needs@100", "resolve:s1"}},
		// 2.1: any line whose cards are open after it, and only then
		{"a card into every open state is held", workLine("p1", "", "s1:merging", nil), []string{"held:p1", "resolve:s1"}},
		{"a line that names no card queues no key of a card", Line{Kind: LineMove, Table: Work, Stream: "s1", From: "s1:ready", To: "s1:working"}, []string{"resolve:s1"}},
	}
	for _, tt := range tests {
		sameKeys(t, tt.name, ingestAt(t, tt.line), tt.want)
	}
}

func TestIngestLinesOfTheOtherTables(t *testing.T) {
	t.Parallel()
	read := func(card, primary, from, to string) Line {
		return Line{Kind: LineMove, Table: Readers, Card: card, Primary: primary, Stream: "s1", From: from, To: to}
	}
	fleet := func(card, from, to string, set map[string]string) Line {
		return Line{Kind: LineMove, Table: Fleet, Card: card, Primary: "p1", Stream: "s1", From: from, To: to, Set: set}
	}
	ctl := func(from, to string, set map[string]string) Line {
		return Line{Kind: LineMove, Table: Fleet, Card: "ctl-m1", From: from, To: to, Set: set}
	}
	tests := []struct {
		name string
		line Line
		want []string
	}{
		// 2.1: readers: read cards enter ok
		{"a read card into ok", read("p1.r1.a", "p1", "a:reading", "a:ok"), []string{"accept:p1"}},
		{"a set of read cards into ok", setOf(Readers, "a:reading", "a:ok", nil, "p1.r1.a", "p2.r1.a"), []string{"accept@100"}},
		{"a read card into ok that does not say its primary", Line{Kind: LineMove, Table: Readers, Card: "p1.r1.a", From: "a:reading", To: "a:ok"},
			[]string{"accept@100"}},
		// 2.1: readers: read cards enter broken
		{"a read card into broken", read("p1.r1.a", "p1", "a:reading", "a:broken"), []string{"rework:p1"}},
		{"a set of read cards into broken", setOf(Readers, "a:reading", "a:broken", nil, "p1.r1.a", "p2.r1.a"), []string{"rework@100"}},
		{"a read card asked or begun wakes nothing", read("p1.r1.a", "p1", "", "a:asked"), nil},
		{"a read card begun wakes nothing", read("p1.r1.a", "p1", "a:asked", "a:reading"), nil},
		{"a read card retired wakes nothing", Line{Kind: LineMove, Table: Readers, Card: "p1.r1.a", Primary: "p1", From: "a:ok", Removed: true}, nil},
		// 2.1: fleet: cards leave a member's ready or working cell
		{"a take, ready to working", fleet("p1.w1", "m1:ready", "m1:working", nil), []string{"deal"}},
		{"a finish, working to ok", fleet("p1.w1", "m1:working", "m1:ok", nil), []string{"deal"}},
		{"a finish, working to failed", fleet("p1.w1", "m1:working", "m1:failed", nil), []string{"deal"}},
		{"a level move, ready to another member's ready", fleet("p1.w1", "m1:ready", "m2:ready", nil), []string{"deal"}},
		{"a withdrawal, ready to withdrawn", fleet("p1.w1", "m1:ready", "m1:withdrawn", nil), []string{"deal"}},
		{"a card taken off a member's working cell", Line{Kind: LineMove, Table: Fleet, Card: "p1.w1", From: "m1:working", Removed: true}, []string{"deal"}},
		{"a set of cards leaving", setOf(Fleet, "m1:ready", "m1:working", nil, "p1.w1", "p2.w1"), []string{"deal"}},
		{"a card dealt into a member's ready cell wakes nothing", fleet("p1.w1", "", "m1:ready", nil), nil},
		{"a card that stays in its cell wakes nothing", fleet("p1.w1", "m1:ready", "m1:ready", map[string]string{"gen": "2"}), nil},
		{"a card leaving a done cell wakes nothing", fleet("p1.w1", "m1:ok", "m1:withdrawn", nil), nil},
		// 2.1: fleet: a member's control card goes up
		{"a control card created up", ctl("", "m1:ctl", map[string]string{"kind": "member", "status": "up"}), []string{"deal", "level"}},
		{"a control card goes up", ctl("m1:ctl", "m1:ctl", map[string]string{"status": "up", "since": "t"}), []string{"deal", "level"}},
		// 2.1: fleet: a member's control card goes down or held
		{"a control card goes down", ctl("m1:ctl", "m1:ctl", map[string]string{"status": "down", "since": "t"}), []string{"down:m1"}},
		{"a control card is held down", ctl("m1:ctl", "m1:ctl", map[string]string{"status": "down", "held": "t"}), []string{"down:m1"}},
		{"a control card already down is held", ctl("m1:ctl", "m1:ctl", map[string]string{"held": "t"}), []string{"down:m1"}},
		{"a control card created down", ctl("", "m1:ctl", map[string]string{"kind": "member", "status": "down"}), []string{"down:m1"}},
		{"a control card that only changes its load wakes nothing", ctl("m1:ctl", "m1:ctl", map[string]string{"load": "3"}), nil},
		{"a control card with held set to nothing wakes nothing", ctl("m1:ctl", "m1:ctl", map[string]string{"held": ""}), nil},
		// the merge table has no row of 2.1: its work lines carry the change
		{"a merge card moves", Line{Kind: LineMove, Table: Merge, Card: "p1", Primary: "p1", Stream: "s1", From: "s1:queued", To: "s1:merged"}, nil},
		{"a stream's control card changes", Line{Kind: LineMove, Table: Merge, Card: "ctl-s1", From: "s1:ctl", To: "s1:ctl", Set: map[string]string{"state": "stopped"}}, nil},
	}
	for _, tt := range tests {
		sameKeys(t, tt.name, ingestAt(t, tt.line), tt.want)
	}
}

func TestIngestMachineStarted(t *testing.T) {
	t.Parallel()
	started := NoteLine(Note{ID: "n1", Kind: Happened, Type: NMachineStarted, Who: "u1"}, "op")
	sameKeys(t, "started", ingestAt(t, started), []string{"askwait", "deal", "done", "level"})
	// Only the happened line of starting is the row: a stop, and a line of
	// the same type of any other kind, wake nothing.
	sameKeys(t, "stopped", ingestAt(t, NoteLine(Note{ID: "n2", Kind: Happened, Type: NMachineStopped}, "op")), nil)
	for _, kind := range []string{Judgment, Decided, Acknowledged} {
		l := started
		n := *started.Note
		n.Kind = kind
		l.Kind, l.Note = kind, &n
		sameKeys(t, kind, ingestAt(t, l), nil)
	}
}

// closeCases is one case of 2.2 for each type the design names: the note,
// and the keys its close queues.
func closeCases() []struct {
	name string
	note Note
	want []string
} {
	one, many := []string{"p1"}, []string{"p1", "p2", "p3"}
	return []struct {
		name string
		note Note
		want []string
	}{
		{"sentinel reached", Note{Type: NSentinelReached, Stream: "s1", Primaries: []string{"g1"}}, []string{"resolve:s1"}},
		{"blocked on something dropped", Note{Type: NBlocked, Stream: "s1", Primaries: one}, []string{"held:p1"}},
		{"blocked on something dropped, many", Note{Type: NBlocked, Stream: "s1", Primaries: many}, []string{"held@100"}},
		{"blocked on something missing", Note{Type: NMissingNeed, Stream: "s1", Primaries: one}, []string{"held:p1"}},
		{"blocked on something missing, many", Note{Type: NMissingNeed, Stream: "s1", Primaries: many}, []string{"held@100"}},
		{"cannot ask", Note{Type: NCannotAsk, Stream: "s1", Primaries: one}, []string{"ask:p1"}},
		{"cannot ask, many", Note{Type: NCannotAsk, Stream: "s1", Primaries: many}, []string{"ask@100"}},
		{"no fleet member is up", Note{Type: NNoMember, StreamLevel: true}, []string{"deal"}},
		{"a card reached its bound", Note{Type: NBound, Stream: "s1", Primaries: one}, []string{"held:p1"}},
		{"a card reached its bound, many", Note{Type: NBound, Stream: "s1", Primaries: many}, []string{"held@100"}},
		{"no merge step past its deadline", Note{Type: NMergeLate, Stream: "s1", StreamLevel: true}, []string{"late:mergeidle:s1"}},
		{"stream stopped: conflict", Note{Type: NConflict, Stream: "s1", StreamLevel: true}, []string{"resolve:s1"}},
		{"stream stopped: red", Note{Type: NRed, Stream: "s1", StreamLevel: true}, []string{"resolve:s1"}},
		{"stream stopped: rejected", Note{Type: NRejected, Stream: "s1", StreamLevel: true}, []string{"resolve:s1"}},
		{"stream stopped: cross", Note{Type: NCross, Stream: "s1", StreamLevel: true}, []string{"cross", "resolve:s1"}},
		{"the machine could not move a card", Note{Type: typeCouldNotMove, Stream: "s1", Primaries: one}, []string{"held:p1"}},
		{"the machine could not move a card, many", Note{Type: typeCouldNotMove, Stream: "s1", Primaries: many}, []string{"held@100"}},
		{"an invariant is broken", Note{Type: NInvariant, Stream: "s1", Primaries: one}, []string{"held:p1"}},
		{"stalled", Note{Type: NStalled, Stream: "s1", Primaries: one}, []string{"held:p1"}},
		{"stalled, many", Note{Type: NStalled, Stream: "s1", Primaries: many}, []string{"held@100"}},
		{"stranded in review", Note{Type: NStranded, Stream: "s1", Primaries: one}, []string{"held:p1"}},
		{"reads exhausted", Note{Type: NReadsExhausted, Stream: "s1", Primaries: one}, []string{"held:p1"}},
		{"the sprint is done", Note{Type: NSprintDone, SprintLevel: true}, []string{"done"}},
		{"the machine is falling behind", Note{Type: typeFallingBehind, SprintLevel: true}, []string{"behind"}},
		{"the machine is STOPPED and moves are due", Note{Type: NStoppedWithDue, SprintLevel: true}, nil},
	}
}

func TestIngestCloseOfAJudgmentQueuesItsOwnerKey(t *testing.T) {
	t.Parallel()
	for _, tt := range closeCases() {
		// Both lines that close a judgment queue the owner key.
		for _, kind := range []string{Decided, Acknowledged} {
			n := tt.note
			n.ID, n.Kind, n.Count = "n1", kind, len(n.Primaries)
			sameKeys(t, tt.name+" ("+kind+")", ingestAt(t, NoteLine(n, "op")), tt.want)
		}
		// A line that opens the judgment, or only says it happened, queues
		// nothing: the judgment is its own holder while it is open.
		for _, kind := range []string{Judgment, Happened} {
			n := tt.note
			n.ID, n.Kind, n.Count = "n1", kind, len(n.Primaries)
			sameKeys(t, tt.name+" ("+kind+")", ingestAt(t, NoteLine(n, "op")), nil)
		}
	}
}

func TestIngestEveryTypeOf22IsARowOrLeftOpenOnPurpose(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, r := range closeRows {
		for _, typ := range r.types {
			if seen[typ] {
				t.Errorf("type %q is in two rows", typ)
			}
			seen[typ] = true
		}
	}
	for _, tt := range closeCases() {
		if !seen[tt.note.Type] {
			t.Errorf("%s: type %q is in no row of closeRows", tt.name, tt.note.Type)
		}
	}
	// Types with no row are left out: unknown ones, and the four the design
	// does not settle (see the foot of the comment of ingest.go).
	for _, typ := range []string{NWorkLate, NReadLate, NRemindFailed, "a verb in parts stopped before its end", "a type no rule has", ""} {
		if seen[typ] {
			t.Errorf("type %q is a row", typ)
		}
		for _, kind := range []string{Decided, Acknowledged} {
			n := Note{ID: "n1", Kind: kind, Type: typ, Stream: "s1", Primaries: []string{"p1", "c1"}, Count: 2}
			sameKeys(t, typ+" ("+kind+")", ingestAt(t, NoteLine(n, "op")), nil)
		}
	}
	for _, r := range lineRows {
		if r.line == "" || r.when == nil || len(r.keys) == 0 {
			t.Errorf("a row of lineRows is not whole: %+v", r.line)
		}
	}
}

func TestIngestLeavesOutWhatTheDesignLeavesOpen(t *testing.T) {
	t.Parallel()
	// The reader row added: no line is its event, since a rows entry writes
	// no line (1.6); a line of the kind the lower layer's contract names for
	// it queues nothing here.
	rows := Line{Kind: "rows", Table: Readers, Card: "", Set: map[string]string{"added": "r2"}}
	sameKeys(t, "a reader row added", ingestAt(t, rows), nil)
	// A sentinel landed, removed or re-scored by a line that does not say it
	// is one: the line is a change in its stream and a card landed or removed,
	// and it queues no deal on account of the sentinel.
	for _, l := range []Line{
		workLine("g1", "s1:waiting", "s1:landed", map[string]string{"reached": "t"}),
		workLine("g1", "s1:waiting", "s1:waiting", map[string]string{"score": "9"}),
	} {
		if slices.Contains(ingestAt(t, l), "deal") {
			t.Errorf("a line that does not say its card is a sentinel queued deal: %+v", l)
		}
	}
	// A line of a work card's lateness is not its owner key either way.
	for _, typ := range []string{NWorkLate, NReadLate} {
		for _, k := range ingestAt(t, NoteLine(Note{ID: "n1", Kind: Decided, Type: typ, Primaries: []string{"p1.w1"}, Count: 1}, "op")) {
			if RuleOf(k) == ruleLate {
				t.Errorf("%s queued %s", typ, k)
			}
		}
	}
}

// bulkShape is a shape of line that names n cards.
type bulkShape struct {
	name string
	line func(n int) Line
}

func cardIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("c%05d", i)
	}
	return ids
}

func bulkShapes() []bulkShape {
	set := func(table, from, to string, m map[string]string) func(int) Line {
		return func(n int) Line {
			ids := cardIDs(n)
			l := setOf(table, from, to, m, ids...)
			if n == 1 {
				l.Cards, l.Primary = nil, ids[0]
			}
			return l
		}
	}
	note := func(typ string, kind string) func(int) Line {
		return func(n int) Line {
			ids := cardIDs(n)
			return NoteLine(Note{ID: "n1", Kind: kind, Type: typ, Stream: "s1", Primaries: ids, Count: n}, "op")
		}
	}
	var out []bulkShape
	out = append(out,
		bulkShape{"work: into ready", set(Work, "s1:waiting", "s1:ready", nil)},
		bulkShape{"work: into review ok", set(Work, "s1:working", "s1:review", map[string]string{"result": "ok"})},
		bulkShape{"work: into review failed", set(Work, "s1:working", "s1:review", map[string]string{"result": "failed"})},
		bulkShape{"work: land", set(Work, "s1:merging", "s1:landed", nil)},
		bulkShape{"work: change in place", set(Work, "s1:waiting", "s1:waiting", map[string]string{"open": "0"})},
		bulkShape{"work: removed", func(n int) Line {
			l := set(Work, "s1:ready", "", nil)(n)
			l.Removed = true
			return l
		}},
		bulkShape{"readers: ok", set(Readers, "a:reading", "a:ok", nil)},
		bulkShape{"readers: broken", set(Readers, "a:reading", "a:broken", nil)},
		bulkShape{"fleet: leave", set(Fleet, "m1:ready", "m1:working", nil)},
	)
	for _, typ := range []string{NBlocked, NCannotAsk, NBound, NStalled, NStranded, NInvariant, NReadsExhausted, typeCouldNotMove} {
		out = append(out, bulkShape{"close " + typ, note(typ, Decided)}, bulkShape{"acknowledge " + typ, note(typ, Acknowledged)})
	}
	return out
}

func TestIngestKeysDoNotGrowWithTheCardsALineNames(t *testing.T) {
	t.Parallel()
	// finding B3: a line of 2,000 cards queues what a line of 2 queues, one
	// key a rule, and no key holds a card id.
	const maxKeys = 6
	for _, b := range bulkShapes() {
		want := ingestAt(t, b.line(2))
		if len(want) == 0 || len(want) > maxKeys {
			t.Errorf("%s: %d keys for a line of 2 cards: %v", b.name, len(want), want)
		}
		for _, n := range []int{3, 50, 2000, 20000} {
			got := ingestAt(t, b.line(n))
			if !slices.Equal(got, want) {
				t.Errorf("%s: %d cards queued %d keys, %d cards queued %d:\n%v\n%v", b.name, n, len(got), 2, len(want), got, want)
			}
		}
		for _, k := range want {
			if strings.Contains(k, "c0") {
				t.Errorf("%s: a bulkShape line queued a key that names a card: %s", b.name, k)
			}
		}
		// A line of one card names the card: the same line, cut to one card,
		// names it in the allRules that name their subject.
		one := ingestAt(t, b.line(1))
		named := false
		for _, k := range one {
			named = named || strings.Contains(k, ":c00000")
		}
		if !named && !strings.HasPrefix(b.name, "fleet") {
			t.Errorf("%s: a line of one card names no card: %v", b.name, one)
		}
	}
}

func TestIngestAPageOfTwoThousandCardLines(t *testing.T) {
	t.Parallel()
	// A page of 2,000 set lines of 10 cards each, 20,000 ids: the most ids a
	// page takes. The keys are the lines', two a line and the stream's, never
	// the cards'.
	lines := map[uint64]Line{}
	for i := uint64(1); i <= 2000; i++ {
		lines[i] = setOf(Work, "s1:working", "s1:review", map[string]string{"result": "ok"}, cardIDs(10)...)
	}
	got := ingestAll(t, lines)
	// ask@seq and held@seq for each line, and resolve:s1 once.
	if want := 2*2000 + 1; len(got) != want {
		t.Fatalf("%d keys for 2,000 lines of 10 cards, want %d", len(got), want)
	}
}

func TestIngestOrdersKeysByTheSeqThatQueuedThemFirst(t *testing.T) {
	t.Parallel()
	events := []Event{
		{Seq: 30, Kind: LineMove, Table: Work, Cards: []string{"p1"}, Primary: "p1", Stream: "s1", From: "s1:ready", To: "s1:working"},
		{Seq: 10, Kind: LineMove, Table: Work, Cards: []string{"p2"}, Primary: "p2", Stream: "s1", From: "s1:ready", To: "s1:working"},
		{Seq: 20, Kind: LineMove, Table: Work, Cards: []string{"p1"}, Primary: "p1", Stream: "s2", From: "s2:waiting", To: "s2:ready"},
	}
	in := Ingest(events)
	want := []AgendaKey{
		{"held:p2", 10}, {"resolve:s1", 10},
		{"deal", 20}, {"held:p1", 20}, {"resolve:s2", 20},
	}
	// held:p1 is queued by seq 20 and 30, and its first is 20.
	if !reflect.DeepEqual(in.Keys, want) {
		t.Fatalf("\n got %v\nwant %v", in.Keys, want)
	}
	if !reflect.DeepEqual(in.Touched, []string{"held:p2", "held:p1"}) {
		t.Fatalf("touched %v", in.Touched)
	}
	if got := Ingest(nil); got.Keys != nil || got.Touched != nil {
		t.Fatalf("no events queued %+v", got)
	}
}

func TestRuleOf(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]string{
		"deal": "deal", "ask:p17": "ask", "ask@48213": "ask", "late:mergeidle:s1": "late", "held@1": "held", "": "", "@1": "",
	} {
		if got := RuleOf(key); got != want {
			t.Errorf("RuleOf(%q) = %q, want %q", key, got, want)
		}
	}
}

// --- properties ---

// allRules is every rule a key may belong to.
var allRules = []string{ruleResolve, ruleDeal, ruleAsk, ruleAccept, ruleRework, ruleNeeds, ruleCross, ruleDone, ruleHeld, ruleLevel, ruleAskwait, ruleDown, ruleBehind, ruleLate}

// randomEvent is an event drawn from small alphabets, so that the rows of
// both tables fire often: any table, kind, place, field and note type, and
// lines that name no card, one and several.
func randomEvent(r *rand.Rand, seq uint64) Event {
	pick := func(xs ...string) string { return xs[r.IntN(len(xs))] }
	e := Event{Seq: seq, Kind: pick(LineMove, LineMove, LineMove, LineMove, Judgment, Happened, Decided, Acknowledged, "rows", "")}
	place := func() string {
		return pick("", "s1:waiting", "s1:ready", "s1:working", "s1:review", "s1:merging", "s1:landed", "s2:ready",
			"m1:ready", "m1:working", "m1:ok", "m1:ctl", "a:reading", "a:ok", "a:broken")
	}
	if e.Kind == LineMove {
		e.Table = pick(Work, Work, Work, Readers, Fleet, Merge, "")
		e.From, e.To = place(), place()
		if r.IntN(6) == 0 {
			e.To, e.Removed = "", true
		}
		e.Stream = pick("", "s1", "s2")
		e.Primary = pick("", "p1", "p2")
		for range r.IntN(4) {
			e.Cards = append(e.Cards, pick("p1", "p2", "p3", "g1"))
		}
		for _, f := range EventFields {
			if r.IntN(3) == 0 {
				if e.Set == nil {
					e.Set = map[string]string{}
				}
				e.Set[f] = pick("ok", "failed", "up", "down", "t", "", "sentinel", "work")
			}
		}
		return e
	}
	e.NoteType = pick(NSentinelReached, NBlocked, NMissingNeed, NCannotAsk, NNoMember, NBound, NMergeLate, NConflict, NRed, NRejected,
		NCross, typeCouldNotMove, NInvariant, NStalled, NStranded, NReadsExhausted, NSprintDone, typeFallingBehind, NStoppedWithDue,
		NWorkLate, NReadLate, NRemindFailed, NMachineStarted, NMachineStopped, "unknown", "")
	e.Stream = pick("", "s1", "s2")
	for range r.IntN(4) {
		e.Subjects = append(e.Subjects, pick("p1", "p2", "p3", "stream:s1", SprintSubject))
	}
	e.Cards = append([]string(nil), e.Subjects...)
	e.Opens, e.Closes = e.Kind == Judgment, e.Kind == Decided || e.Kind == Acknowledged
	return e
}

func randomPage(r *rand.Rand) []Event {
	seq := 1 + uint64(r.IntN(1000))
	var out []Event
	for range r.IntN(40) {
		out = append(out, randomEvent(r, seq))
		seq += 1 + uint64(r.IntN(3))
	}
	return out
}

// cloneEvents is a copy that shares nothing with events.
func cloneEvents(events []Event) []Event {
	b, _ := json.Marshal(events)
	var out []Event
	_ = json.Unmarshal(b, &out)
	return out
}

func keySet(in Ingested) map[string]uint64 {
	out := map[string]uint64{}
	for _, k := range in.Keys {
		out[k.Key] = k.Seq
	}
	return out
}

func TestIngestProperties(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, r := range allRules {
		known[r] = true
	}
	for seed := uint64(0); seed < 400; seed++ {
		r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		events := randomPage(r)
		before := cloneEvents(events)
		in := Ingest(events)

		// It does not change what it is given.
		if !reflect.DeepEqual(cloneEvents(events), before) {
			t.Fatalf("seed %d: Ingest changed its events", seed)
		}
		// It is deterministic.
		if again := Ingest(events); !reflect.DeepEqual(again, in) {
			t.Fatalf("seed %d: two runs differ:\n%v\n%v", seed, in, again)
		}
		// Ingesting the same events twice yields the same keys.
		if twice := Ingest(append(append([]Event(nil), events...), events...)); !reflect.DeepEqual(twice, in) {
			t.Fatalf("seed %d: the events twice differ from once:\n%v\n%v", seed, in, twice)
		}
		// The order of the events does not matter.
		shuffled := append([]Event(nil), events...)
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got := Ingest(shuffled); !reflect.DeepEqual(got, in) {
			t.Fatalf("seed %d: a shuffle of the events differs:\n%v\n%v", seed, in, got)
		}
		// Neither does the cut of the page: the keys of two pages are the
		// keys of the two together, each by the first line that queued it.
		cut := 0
		if len(events) > 0 {
			cut = r.IntN(len(events) + 1)
		}
		a, b := keySet(Ingest(events[:cut])), keySet(Ingest(events[cut:]))
		joined := map[string]uint64{}
		for k, s := range a {
			joined[k] = s
		}
		for k, s := range b {
			if have, ok := joined[k]; !ok || s < have {
				joined[k] = s
			}
		}
		if !reflect.DeepEqual(joined, keySet(in)) {
			t.Fatalf("seed %d: two pages cut at %d differ from the whole:\n%v\n%v", seed, cut, joined, keySet(in))
		}

		// Its keys are unique, in order, of a known rule, and bounded by the
		// lines: a line queues at most one key of each rule (E6).
		perLine := len(allRules) + 2
		if len(in.Keys) > perLine*len(events) {
			t.Fatalf("seed %d: %d keys for %d lines", seed, len(in.Keys), len(events))
		}
		seen := map[string]bool{}
		for i, k := range in.Keys {
			if seen[k.Key] || !known[RuleOf(k.Key)] {
				t.Fatalf("seed %d: key %q repeated or of no known rule", seed, k.Key)
			}
			seen[k.Key] = true
			if i > 0 && (in.Keys[i-1].Seq > k.Seq || in.Keys[i-1].Seq == k.Seq && in.Keys[i-1].Key >= k.Key) {
				t.Fatalf("seed %d: keys out of order at %d: %v", seed, i, in.Keys)
			}
		}
		var held []string
		for _, k := range in.Keys {
			if RuleOf(k.Key) == ruleHeld {
				held = append(held, k.Key)
			}
		}
		if !slices.Equal(held, in.Touched) {
			t.Fatalf("seed %d: touched %v, held keys %v", seed, in.Touched, held)
		}
		for _, e := range events {
			perRule := map[string]int{}
			for _, k := range Ingest([]Event{e}).Keys {
				perRule[RuleOf(k.Key)]++
			}
			for rule, n := range perRule {
				if n > 1 {
					t.Fatalf("seed %d: line %d queued %d keys of the rule %s: %+v", seed, e.Seq, n, rule, e)
				}
			}
		}
	}
}

func FuzzParseEventAndIngest(f *testing.F) {
	for _, l := range []Line{
		workLine("p1", "s1:working", "s1:review", map[string]string{"result": "ok"}),
		setOf(Work, "s1:merging", "s1:landed", nil, "p1", "p2"),
		{Kind: LineMove, Table: Fleet, Card: "ctl-m1", From: "m1:ctl", To: "m1:ctl", Set: map[string]string{"status": "down"}},
		NoteLine(Note{ID: "n1", Kind: Decided, Type: NBlocked, Stream: "s1", Primaries: []string{"p1", "p2"}, Count: 2}, "op"),
		NoteLine(Note{ID: "n2", Kind: Happened, Type: NMachineStarted}, "op"),
	} {
		b, _ := json.Marshal(l)
		f.Add("7-0", b)
	}
	f.Add("1-0", []byte(`{"kind":"move","table":"work","card":"p1","from":"s1:ready","removed":true}`))
	f.Add("x", []byte(`{`))
	f.Fuzz(func(t *testing.T, id string, data []byte) {
		e, err := ParseEvent(id, data)
		if err != nil {
			return
		}
		again, err := ParseEvent(id, data)
		if err != nil || !reflect.DeepEqual(again, e) {
			t.Fatalf("ParseEvent is not deterministic: %+v %+v %v", e, again, err)
		}
		in := Ingest([]Event{e})
		if twice := Ingest([]Event{e, e}); !reflect.DeepEqual(twice, in) {
			t.Fatalf("a line seen twice queued more: %v %v", in, twice)
		}
		for _, k := range in.Keys {
			if k.Seq != e.Seq || RuleOf(k.Key) == "" {
				t.Fatalf("key %+v of line %d", k, e.Seq)
			}
		}
	})
}
