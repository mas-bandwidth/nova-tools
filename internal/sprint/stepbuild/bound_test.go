package stepbuild

import (
	"fmt"
	"strings"
	"testing"
)

// One test per bound of section 6. Each takes an input exactly at the bound,
// which is one step (or, for a bound on a wire entry, one wire entry), and an
// input one over, which is cut into two. The bounds a wire entry carries (its
// IDs, its generated line) cut into two wire entries of one step, as section 6
// asks ("split them into disjoint entries within the same step when the
// budgets all fit"); the bounds of a step cut into two steps. The item
// bounds no cut can meet (a name, a value) are refused instead, at the bound
// and one over.

func TestLimitsAreTheContractsNumbers(t *testing.T) {
	t.Parallel()
	// The values are typed here from the contract, not computed: a change to
	// a constant without the contract is a failing row.
	for _, r := range []struct {
		name      string
		got, want int
	}{
		{"encoded write request, 4 MiB", LimitRequestBytes, 4194304},
		{"entries per step", LimitEntries, 256},
		{"tables", LimitTables, 4},
		{"member mutation candidates", LimitCandidates, 2000},
		{"guard-only members", LimitGuardOnly, 4000},
		{"IDs in an entry", LimitEntryIDs, 2000},
		{"normal row add/delete names", LimitRowPairs, 100},
		{"notes", LimitNotes, 100},
		{"aligned about IDs", LimitAboutIDs, 4000},
		{"field-value observations per write, 128 x 6,000", LimitFieldObservations, 768000},
		{"log line, 1 MiB", LimitLineBytes, 1048576},
		{"IDs in one line", LimitLineIDs, 2000},
		{"planned argv bytes, 8 MiB", LimitPlannedArgvBytes, 8388608},
		{"stored done receipt, 32 KiB", LimitReceiptBytes, 32768},
		{"ID, row, column, field name", LimitNameBytes, 256},
		{"field value, 64 KiB", LimitFieldValueBytes, 65536},
		{"intent, 64 KiB", LimitIntentBytes, 65536},
		{"caller result, 4 KiB", LimitResultBytes, 4096},
		{"set/each effective fields per member", LimitFieldsPerMember, 128},
		{"unset names", LimitUnsetNames, 128},
		{"before_fields names", LimitBeforeFields, 128},
		{"JSON nesting", LimitNesting, 16},
	} {
		if r.got != r.want {
			t.Errorf("%s: the constant is %d, the contract says %d", r.name, r.got, r.want)
		}
	}
	c := Contract()
	if c.RequestBytes != LimitRequestBytes || c.Candidates != LimitCandidates || c.PlannedArgvBytes != LimitPlannedArgvBytes {
		t.Errorf("Contract() is not the constants: %+v", c)
	}
}

func TestBoundEntries(t *testing.T) {
	t.Parallel()
	one := func(n int) []Entry {
		out := make([]Entry, n)
		for i := range out {
			out[i] = mv("work", []string{fmt.Sprintf("m%d", i)})
		}
		return out
	}
	steps := must(t, cfg(), one(LimitEntries))
	if len(steps) != 1 || len(steps[0].Entries) != LimitEntries {
		t.Fatalf("256 entries: %d steps", len(steps))
	}
	steps = must(t, cfg(), one(LimitEntries+1))
	if len(steps) != 2 || len(steps[0].Entries) != LimitEntries || len(steps[1].Entries) != 1 {
		t.Fatalf("257 entries: %d steps", len(steps))
	}
}

func TestBoundTables(t *testing.T) {
	t.Parallel()
	tables := func(n int) []Entry {
		out := make([]Entry, n)
		for i := range out {
			out[i] = mv(fmt.Sprintf("t%d", i), []string{"m"})
		}
		return out
	}
	if steps := must(t, cfg(), tables(LimitTables)); len(steps) != 1 {
		t.Fatalf("4 tables: %d steps", len(steps))
	}
	steps := must(t, cfg(), tables(LimitTables+1))
	if len(steps) != 2 || len(steps[0].Entries) != LimitTables || steps[1].Entries[0].Table != "t4" {
		t.Fatalf("5 tables: %d steps", len(steps))
	}
	// A guard's table counts: a change in one table guarded on a second, a
	// third and a fourth is four; a fifth entry in a new table starts a step.
	owner := mv("a", []string{"x"})
	owner.Guards = []Entry{gd("b", []string{"g1"}), gd("c", []string{"g2"}), gd("d", []string{"g3"})}
	steps = must(t, cfg(), []Entry{owner, mv("e", []string{"y"})})
	if len(steps) != 2 {
		t.Fatalf("four tables through guards then a fifth: %d steps", len(steps))
	}
}

func TestBoundCandidates(t *testing.T) {
	t.Parallel()
	steps := must(t, cfg(), []Entry{mv("work", names("m", LimitCandidates))})
	if len(steps) != 1 || countMembers(steps) != LimitCandidates {
		t.Fatalf("2,000 candidates: %d steps", len(steps))
	}
	steps = must(t, cfg(), []Entry{mv("work", names("m", LimitCandidates+1))})
	if len(steps) != 2 || countMembers(steps[:1]) != LimitCandidates || countMembers(steps[1:]) != 1 {
		t.Fatalf("2,001 candidates: %d steps", len(steps))
	}
	// Candidates are counted across entries and across tables.
	two := []Entry{mv("work", names("a", 1000)), mv("merge", names("b", 1000))}
	if steps := must(t, cfg(), two); len(steps) != 1 {
		t.Fatalf("1,000 + 1,000 candidates in two tables: %d steps", len(steps))
	}
	two[1] = mv("merge", names("b", 1001))
	if steps := must(t, cfg(), two); len(steps) != 2 || countMembers(steps[:1]) != LimitCandidates {
		t.Fatalf("1,000 + 1,001 candidates: %d steps", len(steps))
	}
}

func TestBoundGuardOnly(t *testing.T) {
	t.Parallel()
	steps := must(t, cfg(), []Entry{gd("work", names("g", LimitGuardOnly))})
	if len(steps) != 1 || countMembers(steps) != LimitGuardOnly {
		t.Fatalf("4,000 guard-only members: %d steps", len(steps))
	}
	steps = must(t, cfg(), []Entry{gd("work", names("g", LimitGuardOnly+1))})
	if len(steps) != 2 || countMembers(steps[:1]) != LimitGuardOnly || countMembers(steps[1:]) != 1 {
		t.Fatalf("4,001 guard-only members: %d steps", len(steps))
	}
	// Guard-only members are additional to the 2,000 candidates.
	both := []Entry{mv("work", names("m", LimitCandidates)), gd("merge", names("g", LimitGuardOnly))}
	if steps := must(t, cfg(), both); len(steps) != 1 {
		t.Fatalf("2,000 candidates and 4,000 guard-only members: %d steps", len(steps))
	}
}

func TestBoundEntryIDs(t *testing.T) {
	t.Parallel()
	steps := must(t, cfg(), []Entry{gd("work", names("g", LimitEntryIDs))})
	if len(steps) != 1 || len(steps[0].Entries) != 1 {
		t.Fatalf("2,000 IDs in an entry: %d wire entries", len(steps[0].Entries))
	}
	steps = must(t, cfg(), []Entry{gd("work", names("g", LimitEntryIDs+1))})
	if len(steps) != 1 || len(steps[0].Entries) != 2 || len(steps[0].Entries[0].IDs) != LimitEntryIDs || len(steps[0].Entries[1].IDs) != 1 {
		t.Fatalf("2,001 IDs in an entry: %d steps, %v", len(steps), kinds(steps[0]))
	}
}

func TestBoundRowPairs(t *testing.T) {
	t.Parallel()
	rows := func(n int) Entry { return Entry{Kind: KindRows, Table: "work", Add: names("r", n)} }
	steps := must(t, cfg(), []Entry{rows(LimitRowPairs)})
	if len(steps) != 1 {
		t.Fatalf("100 row pairs: %d steps", len(steps))
	}
	steps = must(t, cfg(), []Entry{rows(LimitRowPairs + 1)})
	if len(steps) != 2 || len(steps[0].Entries[0].Add) != LimitRowPairs || len(steps[1].Entries[0].Add) != 1 {
		t.Fatalf("101 row pairs: %d steps", len(steps))
	}
	// A pair is (table, row): the same names in two tables are twice as many.
	a, b := rows(50), rows(50)
	b.Table = "merge"
	if steps := must(t, cfg(), []Entry{a, b}); len(steps) != 1 {
		t.Fatalf("50 rows in each of two tables: %d steps", len(steps))
	}
	b.Add = names("r", 51)
	if steps := must(t, cfg(), []Entry{a, b}); len(steps) != 2 {
		t.Fatalf("50 + 51 rows in two tables: %d steps", len(steps))
	}
	// The names are counted before dedup: a row named again, in the same
	// direction, is a name of the step. Fifty rows named twice are a hundred
	// names and fit; a hundred named twice are two hundred and do not.
	again := func(n int) []Entry {
		return []Entry{rows(n), {Kind: KindRows, Table: "work", Add: names("r", n)}}
	}
	if steps := must(t, cfg(), again(LimitRowPairs/2)); len(steps) != 1 || len(steps[0].Entries) != 2 {
		t.Fatalf("50 rows named twice in the same direction: %d steps", len(steps))
	}
	if steps := must(t, cfg(), again(LimitRowPairs)); len(steps) != 2 {
		t.Fatalf("100 rows named twice in the same direction: %d steps", len(steps))
	}
	// One row named 250 times is 250 names, cut into steps of a hundred.
	repeat := make([]string, 250)
	for i := range repeat {
		repeat[i] = "r"
	}
	steps = must(t, cfg(), []Entry{{Kind: KindRows, Table: "work", Add: repeat}})
	if len(steps) != 3 || len(steps[0].Entries[0].Add) != 100 || len(steps[1].Entries[0].Add) != 100 || len(steps[2].Entries[0].Add) != 50 {
		t.Fatalf("one row named 250 times: %d steps", len(steps))
	}
	// Deletes count with adds.
	mixed := Entry{Kind: KindRows, Table: "work", Add: names("a", 60), Del: names("d", 40)}
	if steps := must(t, cfg(), []Entry{mixed}); len(steps) != 1 {
		t.Fatalf("60 adds and 40 dels: %d steps", len(steps))
	}
	mixed.Del = names("d", 41)
	if steps := must(t, cfg(), []Entry{mixed}); len(steps) != 2 {
		t.Fatalf("60 adds and 41 dels: %d steps", len(steps))
	}
}

func TestBoundNotes(t *testing.T) {
	t.Parallel()
	notes := func(n int) []Entry {
		e := Entry{Kind: KindNote}
		for i := 0; i < n; i++ {
			e.Notes = append(e.Notes, Note{Meta: map[string]string{"n": fmt.Sprint(i)}, About: []string{"p"}})
		}
		return []Entry{e}
	}
	steps := must(t, cfg(), notes(LimitNotes))
	if len(steps) != 1 || len(steps[0].Notes) != LimitNotes {
		t.Fatalf("100 notes: %d steps", len(steps))
	}
	steps = must(t, cfg(), notes(LimitNotes+1))
	if len(steps) != 2 || len(steps[0].Notes) != LimitNotes || len(steps[1].Notes) != 1 || len(steps[1].Entries) != 0 {
		t.Fatalf("101 notes: %d steps", len(steps))
	}
	// The second step is a request with no entry and a note: allowed by
	// section 3, and it encodes an empty entries array.
	if !strings.Contains(string(steps[1].Encode()), `"entries":[],"notes":[`) {
		t.Fatalf("a notes-only step: %s", steps[1].Encode())
	}
}

func TestBoundAboutIDs(t *testing.T) {
	t.Parallel()
	note := func(n int) Note { return Note{About: names("p", n)} }
	steps := must(t, cfg(), []Entry{{Kind: KindNote, Notes: []Note{note(2000), note(2000)}}})
	if len(steps) != 1 {
		t.Fatalf("4,000 about IDs in two notes: %d steps", len(steps))
	}
	steps = must(t, cfg(), []Entry{{Kind: KindNote, Notes: []Note{note(2000), note(1000), note(1001)}}})
	if len(steps) != 2 || len(steps[0].Notes) != 2 || len(steps[1].Notes) != 1 {
		t.Fatalf("2,000 + 1,000 + 1,001 about IDs in three notes: %d steps", len(steps))
	}
	// A note is not cut: one over the about bound refuses the build.
	le := refused(t, cfg(), []Entry{{Kind: KindNote, Notes: []Note{note(LimitAboutIDs + 1)}}})
	if le.Bound != boundAbout.name || le.Actual != LimitAboutIDs+1 || le.Note != 0 || le.Entry != 0 {
		t.Fatalf("a note of 4,001 about IDs: %v", le)
	}
	// The members' own about IDs count: 2,000 members with one each and notes
	// for 2,000 more fill the step; one more note ID starts the next.
	e := mv("work", names("m", 2000))
	e.About = names("p", 2000)
	e.Notes = []Note{note(2000)}
	if steps := must(t, cfg(), []Entry{e}); len(steps) != 1 {
		t.Fatalf("2,000 member about IDs and 2,000 note about IDs: %d steps", len(steps))
	}
	e.Notes = []Note{note(2000), note(1)}
	steps = must(t, cfg(), []Entry{e})
	if len(steps) != 2 || len(steps[1].Entries) != 0 || len(steps[1].Notes) != 1 {
		t.Fatalf("2,000 member about IDs and 2,001 note about IDs: %d steps", len(steps))
	}
}

// A note's line carries the IDs it is about: they count against the IDs one
// line may hold, and in the bytes of the line and of the planned argv. A note
// is not cut, so one over the bound refuses the build, naming it.
func TestBoundNoteAboutIDsInTheLine(t *testing.T) {
	t.Parallel()
	note := func(n int) Note { return Note{About: names("p", n)} }
	if steps := must(t, cfg(), []Entry{{Kind: KindNote, Notes: []Note{note(LimitLineIDs)}}}); len(steps) != 1 {
		t.Fatalf("a note of 2,000 about IDs: %d steps", len(steps))
	}
	le := refused(t, cfg(), []Entry{{Kind: KindNote, Notes: []Note{note(1), note(LimitLineIDs + 1)}}})
	if le.Bound != boundLineIDs.name || le.Limit != LimitLineIDs || le.Actual != LimitLineIDs+1 || le.Note != 1 || le.Entry != 0 || le.Field != "notes" {
		t.Fatalf("a note of 2,001 about IDs: %+v", le)
	}
	// The same at a smaller measured chunk.
	c := cfg()
	c.Bounds = Contract()
	c.Bounds.LineIDs = 10
	if le := refused(t, c, []Entry{{Kind: KindNote, Notes: []Note{note(11)}}}); le.Bound != boundLineIDs.name || le.Limit != 10 || le.Actual != 11 {
		t.Fatalf("a note of 11 about IDs in a line of 10: %+v", le)
	}
	// The line's bytes carry the IDs: a note whose meta alone fits a line does
	// not once its IDs are in it.
	// The line is tuned to 1 MiB by the meta value's length, and counted by
	// the independent measure.
	ids := names("p", 1000)
	rest := len(lineEnvelope) + 1 + len(lineNoteOpen) + len(lineNoteAbout) + arrayBytes(ids) + 1 // all but the meta object
	fits := Note{Meta: map[string]string{"m": pad(LimitLineBytes - rest - len(`{"m":""}`))}, About: ids}
	steps := must(t, cfg(), []Entry{{Kind: KindNote, Notes: []Note{fits}}})
	if got := measure(t, steps[0].Encode()).maxLine; len(steps) != 1 || got != LimitLineBytes {
		t.Fatalf("a note whose line is exactly 1 MiB with its IDs: %d steps, %d bytes", len(steps), got)
	}
	fits.Meta["m"] += "p"
	if le := refused(t, cfg(), []Entry{{Kind: KindNote, Notes: []Note{fits}}}); le.Bound != boundLineBytes.name || le.Actual != LimitLineBytes+1 {
		t.Fatalf("a note whose line is 1 MiB + 1 with its IDs: %+v", le)
	}
}

// TestBoundFieldObservations fills a step to its 768,000 observations
// exactly: 1,000 candidates that set, unset and declare 128 fields each (384
// distinct names a member) and 3,000 guard-only members that declare 128
// before_fields, all inside the other bounds. The 768,001st observation starts
// the next step.
func TestBoundFieldObservations(t *testing.T) {
	t.Parallel()
	set, unset, before := map[string]string{}, names("u", 128), names("b", 128)
	for i := 0; i < 128; i++ {
		set[fmt.Sprintf("s%03d", i)] = "v"
	}
	owner := mv("work", names("m", 1000))
	owner.Set, owner.Unset, owner.BeforeFields = set, unset, before
	guard := gd("merge", names("g", 3000))
	guard.BeforeFields = before
	one := gd("fleet", []string{"one"})
	one.BeforeFields = []string{"only"}

	steps := must(t, cfg(), []Entry{owner, guard})
	if got := measure(t, steps[0].Encode()).obs; len(steps) != 1 || got != LimitFieldObservations {
		t.Fatalf("1,000 x 384 and 3,000 x 128 observations: %d steps, %d observations", len(steps), got)
	}
	steps = must(t, cfg(), []Entry{owner, guard, one})
	if len(steps) != 2 || len(steps[1].Entries) != 1 || steps[1].Entries[0].Kind != KindGuard {
		t.Fatalf("768,001 observations: %d steps", len(steps))
	}
	// A guard with no before_fields observes nothing and joins the full step.
	one.BeforeFields = nil
	if steps := must(t, cfg(), []Entry{owner, guard, one}); len(steps) != 1 {
		t.Fatalf("a guard that observes no field: %d steps", len(steps))
	}
}

// tune finds the size of a padded input that makes measure(build(n)) equal
// target: the input's measured size is linear in n (a string of n bytes with
// nothing to escape), so one probe and one correction land on it.
func tune(t *testing.T, target int, build func(n int) []Entry, size func([]Step) int) []Entry {
	t.Helper()
	const probe = 1000
	got := size(must(t, cfg(), build(probe)))
	n := probe + target - got
	if n < 0 {
		t.Fatalf("the probe is already past the target: %d > %d", got, target)
	}
	entries := build(n)
	if got := size(must(t, cfg(), entries)); got != target {
		t.Fatalf("tuned to %d, wanted %d", got, target)
	}
	return entries
}

func TestBoundRequestBytes(t *testing.T) {
	t.Parallel()
	// Four moves whose meta is 1,000,000 bytes and a fifth whose meta is
	// padded until the one step's request is exactly the bound.
	build := func(extra int) func(n int) []Entry {
		return func(n int) []Entry {
			var out []Entry
			for i := 0; i < 4; i++ {
				e := mv("work", []string{fmt.Sprintf("m%d", i)})
				e.Meta = map[string]string{"pad": pad(1000000)}
				out = append(out, e)
			}
			last := mv("work", []string{"last"})
			last.Meta = map[string]string{"pad": pad(n + extra)}
			return append(out, last)
		}
	}
	bytes := func(steps []Step) int {
		if len(steps) != 1 {
			return -1 << 30
		}
		return steps[0].Bytes
	}
	at := tune(t, LimitRequestBytes, build(0), bytes)
	steps := must(t, cfg(), at)
	if len(steps) != 1 || steps[0].Bytes != LimitRequestBytes || len(steps[0].Encode()) != LimitRequestBytes {
		t.Fatalf("a request of exactly 4 MiB: %d steps, %d bytes", len(steps), steps[0].Bytes)
	}
	if m := measure(t, steps[0].Encode()); len(m.within(Contract())) != 0 {
		t.Fatalf("the at-bound step is not inside the bounds: %v", m.within(Contract()))
	}
	over := at
	over[4].Meta = map[string]string{"pad": pad(len(at[4].Meta["pad"]) + 1)}
	steps = must(t, cfg(), over)
	if len(steps) != 2 || len(steps[0].Entries) != 4 || len(steps[1].Entries) != 1 || steps[1].Entries[0].IDs[0] != "last" {
		t.Fatalf("a request of 4 MiB + 1: %d steps", len(steps))
	}
	for _, s := range steps {
		if s.Bytes > LimitRequestBytes || len(s.Encode()) != s.Bytes {
			t.Fatalf("step %d: %d bytes, encoded %d", s.Part, s.Bytes, len(s.Encode()))
		}
	}
}

// TestBoundLineBytes fills one generated log line to exactly 1 MiB (two
// members whose fields are padded until the model line is the bound) and
// then one byte more: the second member is cut into a wire entry of its own,
// in the same step.
func TestBoundLineBytes(t *testing.T) {
	t.Parallel()
	// The first member carries 300,000 bytes in five fields, the second
	// 720,000 in twelve (no value passes 64 KiB) and a thirteenth of n bytes.
	build := func(n int) []Entry {
		first, second := map[string]string{}, map[string]string{}
		for i := 0; i < 5; i++ {
			first[fmt.Sprintf("f%d", i)] = pad(60000)
		}
		for i := 0; i < 12; i++ {
			second[fmt.Sprintf("f%d", i)] = pad(60000)
		}
		second["tuned"] = pad(n)
		e := mv("work", []string{"m0", "m1"})
		e.Each = []map[string]string{first, second}
		return []Entry{e}
	}
	line := func(steps []Step) int {
		if len(steps) != 1 || len(steps[0].Entries) != 1 {
			return -1 << 30
		}
		return measure(t, steps[0].Encode()).maxLine
	}
	at := tune(t, LimitLineBytes, build, line)
	steps := must(t, cfg(), at)
	if m := measure(t, steps[0].Encode()); m.maxLine != LimitLineBytes || len(m.within(Contract())) != 0 {
		t.Fatalf("a line of exactly 1 MiB: %d bytes, %v", m.maxLine, m.within(Contract()))
	}
	at[0].Each[1]["tuned"] += "p"
	steps = must(t, cfg(), at)
	if len(steps) != 1 || len(steps[0].Entries) != 2 || len(steps[0].Entries[0].IDs) != 1 || steps[0].Entries[1].IDs[0] != "m1" {
		t.Fatalf("a line of 1 MiB + 1: %d steps, %v", len(steps), kinds(steps[0]))
	}
	if m := measure(t, steps[0].Encode()); len(m.within(Contract())) != 0 {
		t.Fatalf("the cut step is not inside the bounds: %v", m.within(Contract()))
	}
}

// TestBoundLineIDs is the IDs bound of one line, at the contract's number and
// at a smaller measured chunk: a wire entry of 1,000 fills the chunk, 1,001
// make two wire entries of one step.
func TestBoundLineIDs(t *testing.T) {
	t.Parallel()
	c := cfg()
	c.Bounds = Contract()
	c.Bounds.LineIDs = 1000
	steps := must(t, c, []Entry{mv("work", names("m", 1000))})
	if len(steps) != 1 || len(steps[0].Entries) != 1 {
		t.Fatalf("1,000 IDs in a line of 1,000: %v", kinds(steps[0]))
	}
	steps = must(t, c, []Entry{mv("work", names("m", 1001))})
	if len(steps) != 1 || len(steps[0].Entries) != 2 || len(steps[0].Entries[1].IDs) != 1 {
		t.Fatalf("1,001 IDs in a line of 1,000: %d steps", len(steps))
	}
	// At the contract's number the line and the candidates bound coincide.
	steps = must(t, cfg(), []Entry{mv("work", names("m", LimitLineIDs))})
	if len(steps) != 1 || len(steps[0].Entries) != 1 {
		t.Fatalf("2,000 IDs in a line: %d steps", len(steps))
	}
}

// The line of a member entry carries its members and their distinct about IDs,
// and both count against the IDs a line may hold, as a note's line counts its
// about IDs: 2,000 members with 2,000 distinct about IDs write a line of 4,000.
func TestBoundLineIDsCountsTheDistinctAboutIDsOfAMemberLine(t *testing.T) {
	t.Parallel()
	c := cfg()
	c.Bounds = Contract()
	c.Bounds.LineIDs = 1000
	withAbout := func(n int, about func(i int) string) []Entry {
		e := mv("work", names("m", n))
		e.About = make([]string, n)
		for i := range e.About {
			e.About[i] = about(i)
		}
		return []Entry{e}
	}
	distinct := func(i int) string { return fmt.Sprintf("p%d", i) }
	same := func(int) string { return "p" }
	// 500 members and 500 distinct about IDs are 1,000: one wire entry; 501 are
	// 1,002 and make two.
	steps := must(t, c, withAbout(500, distinct))
	if len(steps) != 1 || len(steps[0].Entries) != 1 {
		t.Fatalf("500 members with 500 abouts in a line of 1,000: %d steps, %v", len(steps), kinds(steps[0]))
	}
	steps = must(t, c, withAbout(501, distinct))
	if len(steps) != 1 || len(steps[0].Entries) != 2 || len(steps[0].Entries[0].IDs) != 500 || len(steps[0].Entries[1].IDs) != 1 {
		t.Fatalf("501 members with 501 abouts in a line of 1,000: %d steps, %v", len(steps), kinds(steps[0]))
	}
	// About IDs that are the same primary count once: 999 members and their one
	// about ID are 1,000, and a 1,000th member makes two wire entries.
	steps = must(t, c, withAbout(999, same))
	if len(steps) != 1 || len(steps[0].Entries) != 1 {
		t.Fatalf("999 members with one about in a line of 1,000: %v", kinds(steps[0]))
	}
	steps = must(t, c, withAbout(1000, same))
	if len(steps) != 1 || len(steps[0].Entries) != 2 || len(steps[0].Entries[0].IDs) != 999 {
		t.Fatalf("1,000 members with one about in a line of 1,000: %d steps, %v", len(steps), kinds(steps[0]))
	}
	// The distinct IDs of one wire entry, not of the step: a second wire entry
	// counts its own.
	steps = must(t, c, withAbout(1994, func(i int) string { return fmt.Sprintf("p%d", i%3) }))
	if len(steps) != 1 || len(steps[0].Entries) != 2 || len(steps[0].Entries[0].IDs) != 997 || len(steps[0].Entries[1].IDs) != 997 {
		t.Fatalf("1,994 members with three abouts in a line of 1,000: %v", kinds(steps[0]))
	}
	// At the contract's number: 2,000 members with 2,000 distinct about IDs are
	// two wire entries of 1,000, one step; the line of every one is inside its
	// bound counted from the encoded request.
	steps = must(t, cfg(), withAbout(LimitCandidates, distinct))
	if len(steps) != 1 || len(steps[0].Entries) != 2 || len(steps[0].Entries[0].IDs) != LimitLineIDs/2 {
		t.Fatalf("2,000 members with 2,000 abouts: %d steps, %v", len(steps), kinds(steps[0]))
	}
	if m := measure(t, steps[0].Encode()); m.maxLineIDs != LimitLineIDs || len(m.within(Contract())) != 0 {
		t.Fatalf("the widest line has %d IDs: %v", m.maxLineIDs, m.within(Contract()))
	}
	// A member and its about ID cannot be a line of one: no step holds it.
	c.Bounds.LineIDs = 1
	le := refused(t, c, withAbout(3, distinct))
	if le.Bound != boundLineIDs.name || le.Member != "m0" || le.Actual != 2 || le.Limit != 1 {
		t.Fatalf("%+v", le)
	}
	// Without an about ID a member is a line of one.
	if steps := must(t, c, []Entry{mv("work", names("m", 3))}); len(steps) != 1 || len(steps[0].Entries) != 3 {
		t.Fatalf("members without about IDs in a line of 1: %v", kinds(steps[0]))
	}
}

// TestBoundPlannedArgvBytes takes the planned argv bytes of a padded step as
// the independent count gives them, and sets the bound to that number, then to
// one less: the step fits the first and is cut for the second.
func TestBoundPlannedArgvBytes(t *testing.T) {
	t.Parallel()
	entries := []Entry{mv("work", names("m", 20))}
	entries[0].Set = map[string]string{"f": pad(20000)}
	entries[0].Unset = []string{"gone"}
	c := cfg()
	c.Bounds = Contract()
	steps := must(t, c, entries)
	if len(steps) != 1 {
		t.Fatalf("the whole in one step: %d steps", len(steps))
	}
	model := measure(t, steps[0].Encode()).argv
	if want := 20 * (20000 + 1 + len("gone")); model < want {
		t.Fatalf("the count is short of the record writes alone: %d < %d", model, want)
	}
	argv := charged(model) // the count with the builder's margin on top
	c.Bounds.PlannedArgvBytes = argv
	if steps := must(t, c, entries); len(steps) != 1 {
		t.Fatalf("a bound of exactly %d: %d steps", argv, len(steps))
	}
	c.Bounds.PlannedArgvBytes = argv - 1
	steps = must(t, c, entries)
	if len(steps) != 2 {
		t.Fatalf("a bound of %d: %d steps", argv-1, len(steps))
	}
	for _, s := range steps {
		if got := charged(measure(t, s.Encode()).argv); got > argv-1 {
			t.Fatalf("step %d plans %d > %d", s.Part, got, argv-1)
		}
	}
	// A step with an op reserves the receipt's 32 KiB.
	c.Bounds.PlannedArgvBytes = argv
	c.Ident = func(part int) Ident { return Ident{Op: fmt.Sprintf("op%d", part), Intent: "i"} }
	if steps := must(t, c, entries); len(steps) < 2 {
		t.Fatalf("the receipt's reserve did not count: %d steps", len(steps))
	}
}

func TestBoundNameBytes(t *testing.T) {
	t.Parallel()
	ok, long := strings.Repeat("n", LimitNameBytes), strings.Repeat("n", LimitNameBytes+1)
	member := func(name string) []Entry { return []Entry{mv("work", []string{name})} }
	for _, tc := range []struct {
		what  string
		build func(name string) []Entry
		field string
	}{
		{"an ID", member, "ids"},
		{"a table", func(n string) []Entry { return []Entry{mv(n, []string{"m"})} }, "t"},
		{"a row of a cell", func(n string) []Entry {
			e := mv("work", []string{"m"})
			e.From = n + ":col"
			return []Entry{e}
		}, "from"},
		{"a column of a cell", func(n string) []Entry {
			e := mv("work", []string{"m"})
			e.To = "row:" + n
			return []Entry{e}
		}, "to"},
		{"a field name of set", func(n string) []Entry {
			e := mv("work", []string{"m"})
			e.Set = map[string]string{n: "v"}
			return []Entry{e}
		}, "set"},
		{"a field name of each", func(n string) []Entry {
			e := mv("work", []string{"m"})
			e.Each = []map[string]string{{n: "v"}}
			return []Entry{e}
		}, "each"},
		{"an unset name", func(n string) []Entry {
			e := mv("work", []string{"m"})
			e.Unset = []string{n}
			return []Entry{e}
		}, "unset"},
		{"a before_fields name", func(n string) []Entry {
			e := mv("work", []string{"m"})
			e.BeforeFields = []string{n}
			return []Entry{e}
		}, "before_fields"},
		{"an about ID", func(n string) []Entry {
			e := mv("work", []string{"m"})
			e.About = []string{n}
			return []Entry{e}
		}, "about"},
		{"a row name", func(n string) []Entry {
			return []Entry{{Kind: KindRows, Table: "work", Add: []string{n}}}
		}, "add"},
		{"a note's about ID", func(n string) []Entry {
			return []Entry{{Kind: KindNote, Notes: []Note{{About: []string{n}}}}}
		}, "notes"},
		{"a guard's ID", func(n string) []Entry {
			e := mv("work", []string{"m"})
			e.Guards = []Entry{gd("work", []string{n})}
			return []Entry{e}
		}, "guards[0].ids"},
	} {
		if steps := must(t, cfg(), tc.build(ok)); len(steps) != 1 {
			t.Errorf("%s of %d bytes: %d steps", tc.what, LimitNameBytes, len(steps))
		}
		le := refused(t, cfg(), tc.build(long))
		if le.Bound != boundName.name || le.Limit != LimitNameBytes || le.Actual != LimitNameBytes+1 || le.Field != tc.field {
			t.Errorf("%s of %d bytes: %+v", tc.what, LimitNameBytes+1, le)
		}
	}
	// The op is a name too, and part of the header.
	c := cfg()
	c.Ident = func(int) Ident { return Ident{Op: ok, Intent: "i"} }
	if steps := must(t, c, member("m")); len(steps) != 1 {
		t.Errorf("an op of 256 bytes: %d steps", len(steps))
	}
	c.Ident = func(int) Ident { return Ident{Op: long, Intent: "i"} }
	if le := refused(t, c, member("m")); le.Entry != -1 || le.Field != "op" || le.Bound != boundName.name {
		t.Errorf("an op of 257 bytes: %+v", le)
	}
	// A refusal names the member it is about.
	if le := refused(t, cfg(), member(long)); le.Entry != 0 || le.Table != "work" {
		t.Errorf("the ID's refusal names its entry: %+v", le)
	}
}

func TestBoundFieldValueBytes(t *testing.T) {
	t.Parallel()
	ok, long := pad(LimitFieldValueBytes), pad(LimitFieldValueBytes+1)
	inSet := func(v string) []Entry {
		e := mv("work", []string{"m"})
		e.Set = map[string]string{"f": v}
		return []Entry{e}
	}
	inEach := func(v string) []Entry {
		e := mv("work", []string{"m0", "m1"})
		e.Each = []map[string]string{nil, {"f": v}}
		return []Entry{e}
	}
	for _, tc := range []struct {
		what   string
		build  func(string) []Entry
		field  string
		member string
	}{{"set", inSet, "set", ""}, {"each", inEach, "each", "m1"}} {
		if steps := must(t, cfg(), tc.build(ok)); len(steps) != 1 {
			t.Errorf("a value of 64 KiB in %s: %d steps", tc.what, len(steps))
		}
		le := refused(t, cfg(), tc.build(long))
		if le.Bound != boundFieldValue.name || le.Actual != LimitFieldValueBytes+1 || le.Field != tc.field || le.Member != tc.member {
			t.Errorf("a value of 64 KiB + 1 in %s: %+v", tc.what, le)
		}
	}
	// A field value may be empty (section 1.2).
	if steps := must(t, cfg(), inSet("")); len(steps) != 1 {
		t.Errorf("an empty value: %d steps", len(steps))
	}
}

func TestBoundIntentAndResultBytes(t *testing.T) {
	t.Parallel()
	entries := []Entry{mv("work", []string{"m"})}
	with := func(id Ident) Config {
		c := cfg()
		c.Ident = func(int) Ident { return id }
		return c
	}
	if steps := must(t, with(Ident{Op: "o", Intent: pad(LimitIntentBytes)}), entries); len(steps) != 1 {
		t.Errorf("an intent of 64 KiB: %d steps", len(steps))
	}
	if le := refused(t, with(Ident{Op: "o", Intent: pad(LimitIntentBytes + 1)}), entries); le.Bound != boundIntent.name || le.Field != "intent" || le.Entry != -1 {
		t.Errorf("an intent of 64 KiB + 1: %+v", le)
	}
	if steps := must(t, with(Ident{Result: pad(LimitResultBytes)}), entries); len(steps) != 1 {
		t.Errorf("a result of 4 KiB: %d steps", len(steps))
	}
	if le := refused(t, with(Ident{Result: pad(LimitResultBytes + 1)}), entries); le.Bound != boundResult.name || le.Field != "result" {
		t.Errorf("a result of 4 KiB + 1: %+v", le)
	}
}

func TestBoundFieldsPerMember(t *testing.T) {
	t.Parallel()
	fields := func(n int) map[string]string {
		m := map[string]string{}
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("f%03d", i)] = "v"
		}
		return m
	}
	e := mv("work", []string{"m"})
	e.Set = fields(LimitFieldsPerMember)
	if steps := must(t, cfg(), []Entry{e}); len(steps) != 1 {
		t.Fatalf("128 set fields: %d steps", len(steps))
	}
	e.Set = fields(LimitFieldsPerMember + 1)
	if le := refused(t, cfg(), []Entry{e}); le.Bound != boundFields.name || le.Field != "set" || le.Actual != 129 {
		t.Fatalf("129 set fields: %+v", le)
	}
	// The bound is on the effective fields: set with each laid over it. An
	// each field that overrides a set field adds none; a new one adds one.
	e.Set = fields(LimitFieldsPerMember)
	e.IDs = []string{"m0", "m1", "m2"}
	e.Each = []map[string]string{{"f000": "x"}, nil, {"new": "x"}}
	le := refused(t, cfg(), []Entry{e})
	if le.Field != "each" || le.Member != "m2" || le.Actual != 129 {
		t.Fatalf("128 set fields and one new each field: %+v", le)
	}
	e.Each[2] = map[string]string{"f127": "x"}
	if steps := must(t, cfg(), []Entry{e}); len(steps) != 1 {
		t.Fatalf("128 effective fields through an override: %d steps", len(steps))
	}
}

func TestBoundUnsetAndBeforeNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		field string
		set   func(*Entry, []string)
		bound bound
	}{
		{"unset", "unset", func(e *Entry, v []string) { e.Unset = v }, boundUnset},
		{"before_fields", "before_fields", func(e *Entry, v []string) { e.BeforeFields = v }, boundBeforeNames},
	} {
		e := mv("work", []string{"m"})
		tc.set(&e, names("n", 128))
		if steps := must(t, cfg(), []Entry{e}); len(steps) != 1 {
			t.Errorf("128 %s names: %d steps", tc.name, len(steps))
		}
		// The arrays are bounded before dedup: 129 names of one field are 129.
		tc.set(&e, append(names("n", 127), "n0", "n0"))
		le := refused(t, cfg(), []Entry{e})
		if le.Bound != tc.bound.name || le.Field != tc.field || le.Actual != 129 || le.Limit != 128 {
			t.Errorf("129 %s names: %+v", tc.name, le)
		}
	}
}

// TestNestingNeverPassesFive holds the contract's depth bound of 16 by
// measuring the deepest request the encoding writes: a step with every
// nested part in it is five deep.
func TestNestingNeverPassesFive(t *testing.T) {
	t.Parallel()
	e := mv("work", []string{"m"})
	e.Set = map[string]string{"a": "b"}
	e.Each = []map[string]string{{"c": "d"}}
	e.Meta = map[string]string{"e": "f"}
	e.Notes = []Note{{Meta: map[string]string{"g": "h"}, About: []string{"p"}}}
	steps := must(t, cfg(), []Entry{e})
	if d := depthOf(steps[0].Encode()); d != 5 || d > LimitNesting {
		t.Fatalf("nesting depth %d", d)
	}
}
