package main

// The nine rules of the last two days, one test per rule, named for the rule. Each came
// from a hurt, and each is written so that the hurt is what turns it red.
//
// Every clock here is INJECTED and so is every random source: a board's derivations are
// pure functions of (the log, now, the stale window), and a test that read the wall clock
// would be a test of the day it ran on.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// seq is the injected random source: distinct bytes per draw, so ids differ and a test can
// name one. It is deliberately NOT a hash of anything — the id is a draw.
//
// It is MUTEX-GUARDED because crypto/rand.Reader, the source the binary really uses, is
// safe for concurrent use: a fake that was not would fail the concurrency test for a
// reason that belongs to the fake.
type seq struct {
	mu sync.Mutex
	n  uint64
}

func (s *seq) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	for i := range p {
		p[i] = 0
	}
	for i := 0; i < 8 && i < len(p); i++ {
		p[len(p)-1-i] = byte(s.n >> (8 * i))
	}
	return len(p), nil
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	when, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return when.UTC()
}

type bench struct {
	t    *testing.T
	dir  string
	rnd  *seq
	now  time.Time
	last string // the id the most recent add printed
}

func newBench(t *testing.T) *bench {
	t.Helper()
	return &bench{t: t, dir: t.TempDir(), rnd: &seq{}, now: at(t, "2026-09-11T10:00:00Z")}
}

// run drives the binary as a function, with this bench's clock and source.
func (b *bench) run(args ...string) (int, string, string) {
	b.t.Helper()
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, b.now, b.rnd)
	return exit, out.String(), errb.String()
}

// at drives it at another instant without moving the bench's own clock.
func (b *bench) atTime(when time.Time, args ...string) (int, string, string) {
	b.t.Helper()
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, when, b.rnd)
	return exit, out.String(), errb.String()
}

func (b *bench) board(args ...string) []string {
	b.t.Helper()
	return append([]string{args[0], "--dir", b.dir}, args[1:]...)
}

// add files a card and returns its id.
func (b *bench) add(extra ...string) string {
	b.t.Helper()
	args := append([]string{"add", "--dir", b.dir}, extra...)
	exit, stdout, stderr := b.run(args...)
	if exit != 0 {
		b.t.Fatalf("add exit %d: %s", exit, stderr)
	}
	id := field(stdout, "id=")
	if id == "" {
		b.t.Fatalf("add printed no id: %s", stdout)
	}
	b.last = id
	return id
}

// field pulls one key=value from the first line that carries it.
func field(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		for _, tok := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(tok, key); ok {
				return v
			}
		}
	}
	return ""
}

func count(out, prefix string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// plain is a card with the two required fields and nothing else.
func plain(as, text string) []string {
	return []string{"--as", as, "--text", text, "--by", "4h", "--default", "the filer files it as a known gap"}
}

// --------------------------------------------------------------------------------- 1

// Rule 1: COUNTS, NOT LISTS. A listing is a context window spent on the good news.
func TestTheDefaultViewIsCountsNotCards(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	owners := []string{"rowan", "emma", "freddy", "stella", "johnny"}
	for i := 0; i < 50; i++ {
		b.add(plain(owners[i%len(owners)], fmt.Sprintf("thing number %d is owed", i))...)
	}
	exit, stdout, stderr := b.run(b.board("list", "--stale", "10m")...)
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if n := count(stdout, "BOARD CARD"); n != 0 {
		t.Errorf("the default view printed %d cards; rule 1 is counts, not lists:\n%s", n, stdout)
	}
	if n := count(stdout, "BOARD LINE"); n != len(owners) {
		t.Errorf("BOARD LINE count = %d, want one per owner (%d)", n, len(owners))
	}
	if n := count(stdout, "BOARD OK"); n != 1 {
		t.Errorf("BOARD OK count = %d, want 1", n)
	}
	if n := count(stdout, "BOARD NEXT"); n != 1 {
		t.Errorf("BOARD NEXT count = %d, want exactly one", n)
	}

	// A board with ONE fresh ordinary card and a future deadline HAS something to do
	// first. A tool that said "nothing owed" over it would be the count falling for the
	// wrong reason, said in words.
	one := newBench(t)
	id := one.add(plain("rowan", "the one fresh thing")...)
	_, stdout, _ = one.run(one.board("list", "--stale", "10m")...)
	if !strings.Contains(stdout, "BOARD NEXT") || !strings.Contains(stdout, id) {
		t.Errorf("BOARD NEXT does not name the one open card %s:\n%s", id, stdout)
	}
	if strings.Contains(stdout, "nothing owed") {
		t.Errorf("a board with one fresh card said `nothing owed`:\n%s", stdout)
	}

	// Two ordinary cards at ONE second: the smaller id goes first.
	two := newBench(t)
	first := two.add(plain("rowan", "one of two at one second")...)
	second := two.add(plain("rowan", "two of two at one second")...)
	small := first
	if second < first {
		small = second
	}
	_, stdout, _ = two.run(two.board("list", "--stale", "10m")...)
	if !strings.Contains(stdout, small) {
		t.Errorf("a tie at one second must name the lexically smaller id %s:\n%s", small, stdout)
	}

	// An empty board, and a board of only closed cards, print `nothing owed`.
	empty := newBench(t)
	_, stdout, _ = empty.run(empty.board("list", "--stale", "10m")...)
	if !strings.Contains(stdout, "BOARD NEXT nothing owed") {
		t.Errorf("an empty board does not say `nothing owed`:\n%s", stdout)
	}
	shut := newBench(t)
	closed := shut.add(plain("rowan", "a thing that got done")...)
	if exit, _, stderr := shut.run(shut.board("close", "--as", "rowan", "--card", closed, "--stale", "10m", "--landed", "mas-bandwidth/nova-tools#42")...); exit != 0 {
		t.Fatalf("close exit %d: %s", exit, stderr)
	}
	_, stdout, _ = shut.run(shut.board("list", "--stale", "10m")...)
	if !strings.Contains(stdout, "BOARD NEXT nothing owed") {
		t.Errorf("a board of only closed cards does not say `nothing owed`:\n%s", stdout)
	}

	// --list prints cards, capped at --max, and one MORE line reading "and <t-n> more".
	exit, stdout, stderr = b.run(b.board("list", "--stale", "10m", "--list", "--max", "7")...)
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	if n := count(stdout, "BOARD CARD"); n != 7 {
		t.Errorf("--max 7 printed %d cards", n)
	}
	if !strings.Contains(stdout, "BOARD MORE kind=card shown=7 total=50 and 43 more;") {
		t.Errorf("the MORE line does not read `and <t-n> more` with its remedy:\n%s", stdout)
	}
	// --owner prints one line's own batch, and the counts still hold the whole board.
	_, stdout, _ = b.run(b.board("list", "--stale", "10m", "--list", "--owner", "emma", "--max", "0")...)
	if n := count(stdout, "BOARD CARD"); n != 10 {
		t.Errorf("--owner emma printed %d cards, want emma's 10", n)
	}
	if !strings.Contains(stdout, "cards=50") {
		t.Errorf("the counts must be the truth about the BOARD, not about the output:\n%s", stdout)
	}
}

// --------------------------------------------------------------------------------- 2

// Rule 2: every card has an owner, a DEADLINE and a DEFAULT. Never wait forever.
func TestNoCardLivesWithoutADeadline(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	exit, _, stderr := b.run("add", "--dir", b.dir, "--as", "rowan", "--text", "a thing", "--default", "d")
	if exit != 2 || !strings.Contains(stderr, "--by") {
		t.Errorf("add without --by: exit %d, stderr %q", exit, stderr)
	}
	exit, _, stderr = b.run("add", "--dir", b.dir, "--as", "rowan", "--text", "a thing", "--by", "4h")
	if exit != 2 || !strings.Contains(stderr, "--default") {
		t.Errorf("add without --default: exit %d, stderr %q", exit, stderr)
	}
	// ONE RUN NAMES EVERY INDEPENDENT PROBLEM.
	exit, _, stderr = b.run("add", "--dir", b.dir, "--as", "rowan", "--text", "a thing")
	if exit != 2 {
		t.Fatalf("exit %d", exit)
	}
	for _, want := range []string{"--by", "--default"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("one run must name both missing flags; %q missing from:\n%s", want, stderr)
		}
	}

	// A card one second PAST its --by is overdue; one second before is not. The clock is
	// injected, so this is a fact about the rule and not about the day.
	id := b.add("--as", "rowan", "--text", "a dated thing", "--by", "2026-09-11T12:00:00Z", "--default", "d")
	_, stdout, _ := b.atTime(at(t, "2026-09-11T11:59:59Z"), b.board("list", "--stale", "10m", "--list")...)
	if !strings.Contains(stdout, "overdue=false") || !strings.Contains(stdout, "overdue=0") {
		t.Errorf("one second before the deadline is not overdue:\n%s", stdout)
	}
	_, stdout, _ = b.atTime(at(t, "2026-09-11T12:00:01Z"), b.board("list", "--stale", "10m", "--list")...)
	if !strings.Contains(stdout, "overdue=true") {
		t.Errorf("one second past the deadline is not overdue=true:\n%s", stdout)
	}
	if !strings.Contains(stdout, "overdue=1") {
		t.Errorf("the overdue card is not counted on BOARD OK:\n%s", stdout)
	}
	if !strings.Contains(stdout, id) {
		t.Errorf("the listing lost the card")
	}
}

// --------------------------------------------------------------------------------- 4

// Rule 4: the owed ledger. Done means the owed count is ZERO, and the tool prints that
// number rather than a word.
func TestOwedCountsPerLegAndDoneIsZero(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	var rows []string
	for _, leg := range []string{"cpp", "go", "rust"} {
		for i := 0; i < 2; i++ {
			rows = append(rows, b.add("--as", "rowan", "--text", fmt.Sprintf("%s row %d", leg, i),
				"--by", "4h", "--default", "rowan probes it", "--thing", fmt.Sprintf("field-%d", i), "--leg", leg))
		}
	}
	// A row is closed ONLY by probed: a row that was not probed was not done.
	exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", rows[0], "--stale", "10m", "--how", "I looked at it")...)
	if exit != 1 || !strings.Contains(stderr, "CLOSE REFUSED") {
		t.Errorf("closed on a row: exit %d, stderr %q", exit, stderr)
	}
	exit, _, stderr = b.run(b.board("close", "--as", "rowan", "--card", rows[0], "--stale", "10m", "--landed", "mas-bandwidth/schema#900")...)
	if exit != 1 {
		t.Errorf("landed on a row: exit %d, stderr %q", exit, stderr)
	}
	for _, id := range rows[:5] {
		if exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", id, "--stale", "10m", "--probed", "./reports/probe.txt")...); exit != 0 {
			t.Fatalf("probe exit %d: %s", exit, stderr)
		}
	}
	_, stdout, _ := b.run(b.board("list", "--stale", "10m")...)
	for _, want := range []string{"BOARD LEG leg=cpp owed=0 probed=2", "BOARD LEG leg=go owed=0 probed=2", "BOARD LEG leg=rust owed=1 probed=1", "owed=1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("want %q in:\n%s", want, stdout)
		}
	}
	if exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", rows[5], "--stale", "10m", "--probed", "./reports/probe.txt")...); exit != 0 {
		t.Fatalf("probe exit %d: %s", exit, stderr)
	}
	_, stdout, _ = b.run(b.board("list", "--stale", "10m")...)
	if !strings.Contains(stdout, "owed=0") {
		t.Errorf("probing the last row must print owed=0, the sentence a reader is waiting for:\n%s", stdout)
	}
	if !strings.Contains(stdout, "BOARD NEXT nothing owed") {
		t.Errorf("with every row probed there is nothing owed:\n%s", stdout)
	}
	// Half a row is neither.
	exit, _, stderr = b.run("add", "--dir", b.dir, "--as", "rowan", "--text", "half a row", "--by", "4h", "--default", "d", "--thing", "x")
	if exit != 2 || !strings.Contains(stderr, "--leg") {
		t.Errorf("--thing without --leg: exit %d, stderr %q", exit, stderr)
	}
}

// --------------------------------------------------------------------------------- 5

// Rule 5: a machine's finding becomes a card by ONE command, and the tool does not open
// the path it carries.
func TestAMachineFindingIsOneCommandWithEvidence(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	missing := filepath.Join(b.dir, "nowhere", "crash-176549.bin")
	id := b.add("--as", "fuzzer", "--text", "a crash seed nobody has triaged", "--by", "4h",
		"--default", "rowan triages it", "--evidence", missing)
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("the fixture path exists; this test is about a path that does not")
	}
	_, stdout, _ := b.run(b.board("list", "--stale", "10m", "--list")...)
	if !strings.Contains(stdout, "evidence="+strings.ReplaceAll(missing, " ", `\x20`)) {
		t.Errorf("the evidence path is not on the BOARD CARD line:\n%s", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(b.dir, id+".board"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "evidence=") {
		t.Errorf("the event does not carry the evidence:\n%s", raw)
	}
}

// --------------------------------------------------------------------------------- 6

// Rule 6: SILENCE IS A STATE. A card taken by a line that then stops is owed by nobody and
// looks owed by somebody, which is worse than unowned.
func TestSilenceIsAStateAndIsCounted(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add(plain("rowan", "a thing that goes quiet")...)
	ten := at(t, "2026-09-11T10:09:59Z")
	_, stdout, _ := b.atTime(ten, b.board("list", "--stale", "10m", "--list")...)
	if !strings.Contains(stdout, "stale=false") || !strings.Contains(stdout, "stale=0") {
		t.Errorf("one second under --stale is not stale:\n%s", stdout)
	}
	past := at(t, "2026-09-11T10:10:01Z")
	_, stdout, _ = b.atTime(past, b.board("list", "--stale", "10m", "--list", "--open")...)
	if !strings.Contains(stdout, "stale=true") {
		t.Errorf("past --stale the card is not stale=true:\n%s", stdout)
	}
	if !strings.Contains(stdout, "stale=1") {
		t.Errorf("the stale card is not counted on BOARD OK:\n%s", stdout)
	}
	if !strings.Contains(stdout, id) {
		t.Errorf("a stale card must never be hidden; --open lost it:\n%s", stdout)
	}

	// close without --stale is exit 2 naming it: this decision is made FROM a duration, so
	// the duration comes from a flag.
	exit, _, stderr := b.run(b.board("close", "--as", "bo", "--card", id, "--how", "done")...)
	if exit != 2 || !strings.Contains(stderr, "--stale") {
		t.Errorf("close without --stale: exit %d, stderr %q", exit, stderr)
	}
	if n := len(strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")); n != 1 {
		t.Errorf("one missing flag cost %d lines:\n%s", n, stderr)
	}

	// A close over a LIVE take by another line is refused; --anyway records the override;
	// past --stale it goes through without --anyway and records override=false.
	held := b.add(plain("rowan", "a thing two lines both reach for")...)
	if exit, _, stderr := b.atTime(at(t, "2026-09-11T10:00:01Z"), b.board("take", "--as", "ada", "--card", held, "--stale", "10m")...); exit != 0 {
		t.Fatalf("take exit %d: %s", exit, stderr)
	}
	exit, _, stderr = b.atTime(at(t, "2026-09-11T10:00:02Z"), b.board("close", "--as", "bo", "--card", held, "--stale", "10m", "--how", "landed under another number")...)
	if exit != 1 || !strings.Contains(stderr, "CLOSE REFUSED") || !strings.Contains(stderr, "ada") {
		t.Errorf("a close over a live take must be exit 1 naming the holder: exit %d, %q", exit, stderr)
	}
	exit, stdout, stderr = b.atTime(at(t, "2026-09-11T10:00:03Z"), b.board("close", "--as", "bo", "--card", held, "--stale", "10m", "--how", "landed under another number", "--anyway")...)
	if exit != 0 || !strings.Contains(stdout, "override=true") {
		t.Errorf("--anyway must close and record the override: exit %d, %q %q", exit, stdout, stderr)
	}
	// And against an ELEVEN MINUTE old take, on a card nobody has closed, no --anyway.
	old := b.add(plain("rowan", "a thing whose holder went offline")...)
	if exit, _, stderr := b.atTime(at(t, "2026-09-11T10:00:01Z"), b.board("take", "--as", "ada", "--card", old, "--stale", "10m")...); exit != 0 {
		t.Fatalf("take exit %d: %s", exit, stderr)
	}
	exit, stdout, stderr = b.atTime(at(t, "2026-09-11T10:11:01Z"), b.board("close", "--as", "bo", "--card", old, "--stale", "10m", "--how", "bo finished it")...)
	if exit != 0 || !strings.Contains(stdout, "override=false") {
		t.Errorf("past --stale a close needs no --anyway and overrides nothing: exit %d, %q %q", exit, stdout, stderr)
	}
}

// The example pair under Exit codes, run VERBATIM against a fresh board: an example that
// does not run is not an example.
func TestTheShellGuardPairRunsAgainstAFreshBoard(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	exit, stdout, _ := b.run(b.board("check", "--words", "windows runner skips")...)
	if exit != 0 {
		t.Fatalf("check on a fresh board must find nothing and exit 0, got %d", exit)
	}
	if !strings.Contains(stdout, "CHECK OK matched=0") {
		t.Errorf("check printed %q", stdout)
	}
	// The guard's arm: exit 0 from check means go on and file.
	exit, stdout, stderr := b.run(b.board("add", "--as", "rowan", "--text", "the Windows runner skips three steps",
		"--by", "4h", "--default", "rowan files it on the schema board as a known gap")...)
	if exit != 0 {
		t.Fatalf("the add of the pair does not run: exit %d, %s", exit, stderr)
	}
	if !strings.Contains(stdout, "ADD OK") {
		t.Errorf("add printed %q", stdout)
	}
	_, stdout, _ = b.run(b.board("list", "--stale", "10m")...)
	if !strings.Contains(stdout, "cards=1") {
		t.Errorf("the pair filed %q, want one card", stdout)
	}
	// THE GUARD EXITS 2, NOT 0, WHEN check IS GIVEN NO BACKEND: a guard that read every
	// non-zero as "already filed" would turn a broken board into a quiet one.
	exit, _, stderr = b.run("check", "--words", "windows")
	if exit != 2 {
		t.Errorf("check with no backend exits %d, want 2 (could not run); stderr %q", exit, stderr)
	}
}

// Work list 7's test, named for the mnemonic.
func TestCheckExitsOneOnMatchSoTheShellGuardReads(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add(plain("rowan", "the Windows runner skips three steps")...)
	exit, stdout, _ := b.run(b.board("check", "--words", "windows SKIPS")...)
	if exit != 1 {
		t.Fatalf("check that MATCHED exits %d, want 1 — the inverted reading would make the natural && chain file exactly the duplicates", exit)
	}
	if !strings.Contains(stdout, "CHECK HIT id="+id) || !strings.Contains(stdout, "CHECK OK matched=1 cards=1 scanned=OPEN words=2") {
		t.Errorf("check printed:\n%s", stdout)
	}
	// EVERY word must appear: matching is deliberately dumb so a filer can predict it.
	exit, stdout, _ = b.run(b.board("check", "--words", "windows elephant")...)
	if exit != 0 || !strings.Contains(stdout, "matched=0") {
		t.Errorf("a word that is not there must not match: exit %d, %q", exit, stdout)
	}
	// A closed card is not scanned by default and is scanned under --all, because
	// "somebody already fixed this" is as good a reason not to file.
	if exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", id, "--stale", "10m", "--landed", "mas-bandwidth/nova-tools#9")...); exit != 0 {
		t.Fatalf("close exit %d: %s", exit, stderr)
	}
	if exit, _, _ := b.run(b.board("check", "--words", "windows")...); exit != 0 {
		t.Errorf("a closed card matched a default check, exit %d", exit)
	}
	exit, stdout, _ = b.run(b.board("check", "--words", "windows", "--all")...)
	if exit != 1 || !strings.Contains(stdout, "scanned=ALL") {
		t.Errorf("--all must scan closed cards and still exit 1 on a match: exit %d, %q", exit, stdout)
	}
}

// --------------------------------------------------------------------------------- 7

// Rule 7: BOUNDED AT THE LARGEST PLAUSIBLE STATE — 500 cards across 20 lines. The output
// of the default view never grows with the number of cards.
func TestBoundedAtFiveHundredCardsAcrossTwentyLines(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	seed(t, b.dir, 0, 500)
	exit, stdout, stderr := b.run(b.board("list", "--stale", "10m")...)
	if exit != 0 {
		t.Fatalf("exit %d: %s", exit, stderr)
	}
	lines := len(strings.Split(strings.TrimSuffix(stdout, "\n"), "\n"))
	if lines > 27 {
		t.Errorf("the default view is %d lines at 500 cards, want at most 20 lines + 5 legs + BOARD NEXT + BOARD OK = 27:\n%s", lines, stdout)
	}
	if n := len(stdout) + len(stderr); n > 4096 {
		t.Errorf("the default view is %d bytes, want under 4 KB", n)
	}
	if !strings.Contains(stdout, "cards=500") {
		t.Errorf("the counting is never capped:\n%s", stdout)
	}
	// 1,000 cards print the SAME number of lines: the default view grows with owners and
	// legs, never with cards.
	seed(t, b.dir, 500, 1000)
	_, twice, _ := b.run(b.board("list", "--stale", "10m")...)
	if got := len(strings.Split(strings.TrimSuffix(twice, "\n"), "\n")); got != lines {
		t.Errorf("1,000 cards print %d lines where 500 printed %d", got, lines)
	}
	if !strings.Contains(twice, "cards=1000") {
		t.Errorf("the count did not follow the board:\n%s", twice)
	}
}

// seed writes card files straight into a board directory, which is what a board of five
// hundred cards looks like after five hundred adds. It is written rather than filed
// because what is under test here is the SHAPE OF THE OUTPUT at the largest plausible
// state, and five hundred adds is five hundred whole-log reads: the O(all events) trade
// this tool makes deliberately, paid in a test that asserts nothing about it. The add path
// itself is pinned by the tests above.
func seed(t *testing.T, dir string, from, to int) {
	t.Helper()
	legs := []string{"cpp", "go", "rust", "csharp", "python"}
	for i := from; i < to; i++ {
		id := fmt.Sprintf("%032x", i+1)
		row := ""
		if i%5 == 0 && i < 500 {
			row = fmt.Sprintf(" thing=field-%d leg=%s", i, legs[(i/5)%len(legs)])
		}
		line := fmt.Sprintf("card %s as=line-%02d at=2026-09-11T10:00:00Z override=false hash=%012x owner=line-%02d by=2026-09-11T14:00:00Z default=the\\x20filer\\x20files\\x20it%s: owed thing number %d on this board",
			id, i%20, i, i%20, row, i)
		if err := os.WriteFile(filepath.Join(dir, id+".board"), []byte("BOARD v1\n"+line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// --------------------------------------------------------------------------------- 8

// Rule 8: NO DEFAULT PATHS AND NO DEFAULT DURATIONS. A board found through $HOME is a
// board a line writes to by accident.
func TestEveryPathAndDurationIsAFlag(t *testing.T) {
	b := newBench(t)
	exit, stdout, stderr := b.run("list", "--dir", b.dir)
	if exit != 2 || !strings.Contains(stderr, "--stale") {
		t.Errorf("list without --stale: exit %d, stderr %q", exit, stderr)
	}
	if n := len(strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")); n != 1 {
		t.Errorf("a missing --stale cost %d lines, want one:\n%s", n, stderr)
	}
	if stdout != "" {
		t.Errorf("a refusal wrote to stdout: %q", stdout)
	}
	exit, _, stderr = b.run("list", "--stale", "10m")
	if exit != 2 || !strings.Contains(stderr, "--issue") || !strings.Contains(stderr, "--dir") {
		t.Errorf("list with no backend: exit %d, stderr %q", exit, stderr)
	}
	if n := len(strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")); n != 1 {
		t.Errorf("no backend cost %d lines, want one:\n%s", n, stderr)
	}
	exit, _, stderr = b.run("list", "--dir", b.dir, "--issue", "mas-bandwidth/schema#876", "--stale", "10m")
	if exit != 2 || !strings.Contains(stderr, "two boards with one name") {
		t.Errorf("both backends: exit %d, stderr %q", exit, stderr)
	}

	// EVERY prototype variable, set in the environment, changes NOTHING. Setting one to
	// prove the tool IGNORES it is required rather than suspect (CONTRIBUTING).
	b.add(plain("rowan", "a thing on the real board")...)
	_, want, _ := b.run(b.board("list", "--stale", "10m")...)
	for _, name := range []string{"BOARD_REPO", "BOARD_ISSUE", "BOARD_OWNER", "BOARD_WINDOW", "BOARD_CACHE_TTL", "BOARD_MAXROWS", "BOARD_NO_CACHE"} {
		t.Setenv(name, "a value this tool must not read")
	}
	_, got, _ := b.run(b.board("list", "--stale", "10m")...)
	if got != want {
		t.Errorf("the environment changed the output:\n%s\n%s", want, got)
	}
	if exit, _, _ := b.run("list", "--stale", "10m"); exit != 2 {
		t.Error("BOARD_REPO and BOARD_ISSUE in the environment supplied a backend; no environment variable configures anything here")
	}

	// --max is the one flag with a default, and a negative one is a typo with two readings.
	if exit, _, stderr := b.run(b.board("list", "--stale", "10m", "--max", "-1")...); exit != 2 || !strings.Contains(stderr, "--max") {
		t.Errorf("--max -1: exit %d, stderr %q", exit, stderr)
	}
}

// The source tripwire behind rule 8: nothing here reaches for a temporary directory, and
// nothing here takes a lock (rule 3).
func TestTheSourceHoldsNoTempDirAndNoLock(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "board")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		read := 0
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			read++
			src := string(raw)
			for _, banned := range []string{"os.TempDir", `"/tmp`, "TMPDIR", "syscall.Flock", "LockFile", ".Lock()", "os.O_EXLOCK"} {
				if strings.Contains(src, banned) {
					t.Errorf("%s/%s holds %q; rule 8 forbids a temporary path and rule 3 says the tool holds no lock because it needs none", dir, e.Name(), banned)
				}
			}
			// Rule 9's and work list 1's tripwires, STRUCTURALLY: the shapes, not one
			// spelling of them, so a rename cannot walk past either.
			for _, found := range bannedShapes(t, filepath.Join(dir, e.Name()), src) {
				t.Error(found)
			}
		}
		if read == 0 {
			t.Fatalf("no source read in %s; the tripwire is looking in the wrong place", dir)
		}
	}
}

// --------------------------------------------------------------------------------- 9

// Rule 9: THE TOOL STAMPS, and add has no --at. A person stamped notes two hours ahead of
// the clock, and every list that ordered by the typed time put them in the future.
func TestTheToolStampsAndAddHasNoAt(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	for _, flag := range []string{"--at", "--since", "--stamp"} {
		exit, _, stderr := b.run("add", "--dir", b.dir, "--as", "rowan", "--text", "a thing",
			"--by", "4h", "--default", "d", flag, "2026-09-11T08:00:00Z")
		if exit != 2 {
			t.Errorf("add %s exits %d, want 2 (an unknown flag)", flag, exit)
		}
		if !strings.Contains(stderr, "not defined") && !strings.Contains(stderr, flag) {
			t.Errorf("add %s: stderr %q", flag, stderr)
		}
	}
	// A text that BEGINS with a stamp two hours ahead is text: carried, never parsed.
	id := b.add("--as", "rowan", "--text", "2026-09-11T12:00:00Z the thing I am filing", "--by", "4h", "--default", "d")
	raw, err := os.ReadFile(filepath.Join(b.dir, id+".board"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "at=2026-09-11T10:00:00Z") {
		t.Errorf("the event's stamp is not this run's clock:\n%s", raw)
	}
	_, stdout, _ := b.run(b.board("list", "--stale", "10m", "--list")...)
	if !strings.Contains(stdout, "since=2026-09-11T10:00:00Z") {
		t.Errorf("since= is not the tool's clock:\n%s", stdout)
	}
	// --by given as a stamp is STORED AS GIVEN and does not change since.
	other := b.add("--as", "rowan", "--text", "a dated thing", "--by", "2026-09-30T09:00:00Z", "--default", "d")
	_, stdout, _ = b.run(b.board("list", "--stale", "10m", "--list")...)
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, other) {
			if !strings.Contains(line, "by=2026-09-30T09:00:00Z") || !strings.Contains(line, "since=2026-09-11T10:00:00Z") {
				t.Errorf("--by as a stamp is stored as given and is never `since`:\n%s", line)
			}
		}
	}
	// And the text a filer wrote is still the text.
	if !strings.Contains(stdout, "2026-09-11T12:00:00Z the thing I am filing") {
		t.Errorf("the text was rewritten:\n%s", stdout)
	}
}

// --------------------------------------------------------------------------- the door

// ONBOARDING point 1: a bare invocation costs ONE line and names the door; the banner is
// behind `help`, on stdout, at exit 0.
func TestABareInvocationCostsOneLineAndNamesTheDoor(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	exit := run(nil, &out, &errb, time.Now().UTC(), &seq{})
	if exit != 2 {
		t.Errorf("a bare nova-board exits %d, want 2", exit)
	}
	if out.String() != "" {
		t.Errorf("a refusal wrote to stdout: %q", out.String())
	}
	if lines := strings.Split(strings.TrimSuffix(errb.String(), "\n"), "\n"); len(lines) != 1 {
		t.Errorf("a bare nova-board printed %d lines:\n%s", len(lines), errb.String())
	}
	if !strings.Contains(errb.String(), "run: nova-board help") {
		t.Errorf("the refusal names no door: %q", errb.String())
	}
	out.Reset()
	errb.Reset()
	if exit := run([]string{"help"}, &out, &errb, time.Now().UTC(), &seq{}); exit != 0 {
		t.Errorf("`nova-board help` exits %d, want 0", exit)
	}
	if !strings.HasPrefix(out.String(), "nova-board:") {
		t.Errorf("the banner is not on stdout: %q", out.String()[:min(80, len(out.String()))])
	}
	out.Reset()
	errb.Reset()
	if exit := run([]string{"lst", "--dir", "."}, &out, &errb, time.Now().UTC(), &seq{}); exit != 2 {
		t.Errorf("an unknown verb exits %d, want 2", exit)
	}
	if lines := strings.Split(strings.TrimSuffix(errb.String(), "\n"), "\n"); len(lines) != 1 {
		t.Errorf("an unknown verb printed %d lines:\n%s", len(lines), errb.String())
	}
}

// A guard against a test helper that silently stopped driving the tool.
var _ io.Writer = (*bytes.Buffer)(nil)

// Rule 1, the other half: --open AND --owner ARE FILTERS ON --list, NEVER AN IMPLICIT
// LISTING. "`list` without `--list` prints no card" and "Cards print only under `--list`,
// capped at `--max`". A tool that printed a board's cards because a filter was named would
// spend a reader's context window on the good news at exactly the moment they asked a
// counting question.
func TestOpenAndOwnerAreFiltersOnTheListing(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	for i := 0; i < 3; i++ {
		b.add(plain("rowan", fmt.Sprintf("thing number %d is owed", i))...)
	}
	// One card rowan took and closed: `--owner` is one line's own batch of OWED decisions,
	// and a closed card is not owed.
	done := b.add(plain("rowan", "a thing rowan has already finished")...)
	for _, verb := range [][]string{
		b.board("take", "--as", "rowan", "--card", done, "--stale", "10m"),
		b.board("close", "--as", "rowan", "--card", done, "--stale", "10m", "--how", "it is done"),
	} {
		if exit, _, stderr := b.run(verb...); exit != 0 {
			t.Fatalf("%v: exit %d %s", verb[0], exit, stderr)
		}
	}
	// Without --list they print no card at all: the refusal that says so is
	// TestAFilterWithoutAListingIsRefused, and the default view is the counts.
	if _, stdout, _ := b.run(b.board("list", "--stale", "10m")...); count(stdout, "BOARD CARD") != 0 || !strings.Contains(stdout, "BOARD OK cards=4") {
		t.Errorf("list without --list prints no card and all the counts:\n%s", stdout)
	}
	// Under --list the same flags are the filter they are documented as.
	_, stdout, _ := b.run(b.board("list", "--stale", "10m", "--list", "--open")...)
	if n := count(stdout, "BOARD CARD"); n != 3 {
		t.Errorf("--list --open printed %d cards, want the 3 open ones:\n%s", n, stdout)
	}
	// "--list --owner <name> prints only that owner's OPEN cards, so one line reads its own
	// batch of OWED decisions in one command and never the board" (rule 1). A closed card
	// in that batch is work already done read as work still owed.
	_, mine, _ := b.run(b.board("list", "--stale", "10m", "--list", "--owner", "rowan")...)
	if n := count(mine, "BOARD CARD"); n != 3 {
		t.Errorf("--list --owner rowan printed %d cards, want that owner's 3 OPEN cards:\n%s", n, mine)
	}
	for _, forbidden := range []string{"state=CLOSED", "BOARD CLOSE", done} {
		if strings.Contains(mine, forbidden) {
			t.Errorf("one line's own batch holds %q, which is not owed:\n%s", forbidden, mine)
		}
	}
	if !strings.Contains(mine, "BOARD OK cards=4 open=3 closed=1") {
		t.Errorf("the filtered listing changed the board's counts; the listing is capped and filtered, the counting never is:\n%s", mine)
	}
}

// A PROBED CLOSE NAMES ITS EVIDENCE ON BOTH LINES. Spec, Output grammar: `CLOSE OK id=<id>
// how=<closed|landed|probed> where=<repo#n|path|-> ...` and `BOARD CLOSE id=<id> by=<name>
// at=<stamp> how=<closed|landed|probed> override=<true|false> where=<repo#n|path|->: <how>`
// — the same alternation on both, so the `path` arm is the probe's evidence wherever the
// close is printed. A row that was not probed was not done, and a listing that dropped the
// evidence would say a row was probed without saying by what.
func TestAProbedCloseNamesItsEvidenceOnBoardCloseToo(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	row := b.add(append(plain("rowan", "a row of the owed ledger"), "--thing", "every-field", "--leg", "cpp")...)
	exit, stdout, stderr := b.run(b.board("close", "--as", "rowan", "--card", row, "--stale", "10m", "--probed", "./reports/probe.txt")...)
	if exit != 0 {
		t.Fatalf("probe: exit %d %s", exit, stderr)
	}
	if !strings.Contains(stdout, "how=probed where=./reports/probe.txt") {
		t.Fatalf("CLOSE OK does not name the evidence: %q", stdout)
	}
	_, listing, _ := b.run(b.board("list", "--stale", "10m", "--list")...)
	var closeLine string
	for _, line := range strings.Split(listing, "\n") {
		if strings.HasPrefix(line, "BOARD CLOSE") {
			closeLine = line
		}
	}
	if !strings.Contains(closeLine, "how=probed") || !strings.Contains(closeLine, "where=./reports/probe.txt") {
		t.Errorf("BOARD CLOSE for a probed close: %q\nwant where= the evidence, as CLOSE OK printed it", closeLine)
	}
	// The other two arms of the same alternation stay what they are.
	landedID := b.add(plain("rowan", "a card that lands under a number")...)
	if exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", landedID, "--stale", "10m", "--landed", "mas-bandwidth/nova-tools#9")...); exit != 0 {
		t.Fatalf("land: exit %d %s", exit, stderr)
	}
	plainID := b.add(plain("rowan", "a card closed with a sentence")...)
	if exit, _, stderr := b.run(b.board("close", "--as", "rowan", "--card", plainID, "--stale", "10m", "--how", "it was already done")...); exit != 0 {
		t.Fatalf("close: exit %d %s", exit, stderr)
	}
	_, listing, _ = b.run(b.board("list", "--stale", "10m", "--list")...)
	for _, want := range []string{
		"BOARD CLOSE id=" + landedID + " by=rowan",
		"how=landed override=false where=mas-bandwidth/nova-tools#9",
		"how=closed override=false where=-",
	} {
		if !strings.Contains(listing, want) {
			t.Errorf("the listing does not hold %q:\n%s", want, listing)
		}
	}
}

// THE COUNTS PRINT ON FAILURE AS WELL AS SUCCESS. Spec, Output grammar: "**The counts
// print on failure as well as success** and they are the truth about the **board**, not
// about the output"; work list 6 asks for it by name: "the count line printed on failure
// as well as success". A refusal that printed no counts would leave the one number a
// reader acts on unsaid at exactly the moment the board said no, and `shown` is how many
// lines this run printed, the count line among them.
func TestTheCountsPrintOnFailureAsWellAsSuccess(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	id := b.add(plain("rowan", "a card another line holds")...)
	other := b.add(plain("rowan", "a card to close under a live take")...)
	for _, card := range []string{id, other} {
		if exit, _, stderr := b.run(b.board("take", "--as", "ada", "--card", card, "--stale", "10m")...); exit != 0 {
			t.Fatalf("take: exit %d %s", exit, stderr)
		}
	}
	cases := []struct {
		name string
		args []string
	}{
		{"a take over a live take", b.board("take", "--as", "bo", "--card", id, "--stale", "10m")},
		{"a close over a live take", b.board("close", "--as", "bo", "--card", other, "--stale", "10m", "--how", "done")},
		{"an --id that exists with different fields", append(b.board("add", "--id", id), plain("rowan", "a different filing entirely")...)},
		{"an id that names no card", b.board("take", "--as", "bo", "--card", "0000000000000000000000000000000f", "--stale", "10m")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := b.run(tc.args...)
			if exit == 0 {
				t.Fatalf("this case must fail: exit 0\n%s%s", stdout, stderr)
			}
			if !strings.Contains(stdout, "BOARD OK cards=2 open=2") {
				t.Errorf("a failing run printed no count line; the counts print on failure as well as success:\nstdout: %q\nstderr: %q", stdout, stderr)
			}
			if !strings.Contains(stdout, "shown=1") {
				t.Errorf("shown is how many lines this run printed:\nstdout: %q", stdout)
			}
		})
	}
	// And on success `shown` counts the count line too: the default view over this board
	// prints one BOARD LINE, one BOARD NEXT and one BOARD OK.
	_, stdout, _ := b.run(b.board("list", "--stale", "10m")...)
	if n := len(strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")); !strings.Contains(stdout, fmt.Sprintf("shown=%d", n)) {
		t.Errorf("the run printed %d lines and says shown= something else:\n%s", n, stdout)
	}
}

// THE --id RETRY COMPARES THE CARD LINE'S OWN owner=, NOT THE DERIVED OWNER. Spec, A retry
// after an uncertain append reuses the id it drew: "an existing card whose creation fields
// are identical (`as`, `hash`, `owner`, `by`, `default`, `thing`, `leg`, `evidence` —
// everything on the `card` line but `at`) is `ADD OK … existed=true` and nothing is
// written". The derived owner is the latest take in the fold, which another line moves by
// taking the card; the card line's owner= never changes. A retry compared against the
// TAKER's name is a wrong refusal of the one append whose outcome was unknown, which is
// the only case --id exists for.
func TestAnIdRetryComparesTheCardLineNotTheDerivedOwner(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	filing := plain("rowan", "a filing whose outcome nobody saw")
	id := b.add(filing...)
	// Another line takes the card between the uncertain append and the retry.
	if exit, _, stderr := b.run(b.board("take", "--as", "ada", "--card", id, "--stale", "10m")...); exit != 0 {
		t.Fatalf("take: exit %d %s", exit, stderr)
	}
	exit, stdout, stderr := b.run(append(b.board("add", "--id", id), filing...)...)
	if exit != 0 || !strings.Contains(stdout, "existed=true") {
		t.Errorf("the identical retry of a card another line has taken: exit %d, stdout %q, stderr %q", exit, stdout, stderr)
	}
	if n := len(eventLines(t, filepath.Join(b.dir, id+".board"))); n != 2 {
		t.Errorf("the retry appended: the card file holds %d events, want the card and the take", n)
	}
	// The same --id with a different --owner is still a refusal: that field is on the card
	// line and a different one is a different filing.
	exit, _, stderr = b.run(append(b.board("add", "--id", id, "--owner", "emma"), filing...)...)
	if exit != 1 || !strings.Contains(stderr, "different fields") {
		t.Errorf("a retry with a different owner=: exit %d, stderr %q", exit, stderr)
	}
}

// The tripwires behind rule 9 and work list 1 are STRUCTURAL, not literal. A tripwire that
// matched one spelling of the line it forbids is green the moment somebody renames a
// variable, and a check that cannot fail is not a check (SPEC.md). The mutation below is
// the same rule broken under different names, and the tripwire must find it.
func TestTheTripwiresAreStructuralAndCatchARename(t *testing.T) {
	t.Parallel()
	mutated := `package board

import "time"

type row struct {
	Seq	int
	Body	string
}

func (r row) since() (time.Time, error) {
	return time.Parse(time.RFC3339, r.Body)
}
`
	found := bannedShapes(t, "mutated.go", mutated)
	for _, want := range []string{"parses a time out of a card's text", "sequence"} {
		hit := false
		for _, f := range found {
			hit = hit || strings.Contains(f, want)
		}
		if !hit {
			t.Errorf("the tripwire missed %q under a renamed field; it found %v", want, found)
		}
	}
	// And it does not fire on the source as it stands, which is the other half of a
	// tripwire being worth keeping.
	if got := bannedShapes(t, "main.go", readFile(t, "main.go")); len(got) != 0 {
		t.Errorf("the tripwire fires on main.go as it stands: %v", got)
	}
}

// bannedShapes is the tripwire itself: the SHAPES rule 9 and work list 1 forbid, found in
// one file's syntax tree rather than in one spelling of them.
//
// Rule 9: no time is parsed out of a card's free text, whatever the variable holding that
// text is called — the shape is a time parse whose argument is a card's text or tail.
// Work list 1: no sequence anywhere near a creation identity — no field, no variable and
// no rendered key called seq, however it is spelled, because a creation identity may
// depend on nothing two writers can both observe before either writes.
func bannedShapes(t *testing.T, name, src string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	var found []string
	textish := func(e ast.Expr) bool {
		switch v := e.(type) {
		case *ast.SelectorExpr:
			return isTextName(v.Sel.Name)
		case *ast.Ident:
			return isTextName(v.Name)
		case *ast.CallExpr:
			for _, a := range v.Args {
				if id, ok := a.(*ast.Ident); ok && isTextName(id.Name) {
					return true
				}
				if sel, ok := a.(*ast.SelectorExpr); ok && isTextName(sel.Sel.Name) {
					return true
				}
			}
		}
		return false
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Parse" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time" {
					for _, arg := range v.Args {
						if textish(arg) {
							found = append(found, name+" parses a time out of a card's text; a time a filer writes inside --text is TEXT")
						}
					}
				}
			}
		case *ast.Field:
			for _, id := range v.Names {
				if isSeqName(id.Name) {
					found = append(found, name+" holds a sequence field "+id.Name+"; a creation identity may depend on nothing two writers can both observe before either writes")
				}
			}
		case *ast.Ident:
			if isSeqName(v.Name) {
				found = append(found, name+" names a sequence "+v.Name+"; a creation identity may depend on nothing two writers can both observe before either writes")
			}
		case *ast.BasicLit:
			if v.Kind == token.STRING && strings.Contains(strings.ToLower(v.Value), "seq=") {
				found = append(found, name+" renders a seq= field; a creation identity may depend on nothing two writers can both observe before either writes")
			}
		}
		return true
	})
	return found
}

// isTextName is a card's free text under any name a rename could give it.
func isTextName(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(name, "card")) {
	case "text", "tail", "body", "words", "freetext":
		return true
	}
	return false
}

// isSeqName is a sequence under any name a rename could give it.
func isSeqName(name string) bool {
	switch strings.ToLower(name) {
	case "seq", "sequence", "seqno", "serial", "counter", "ordinal":
		return true
	}
	return false
}

// ------------------------------------------------------- the new-user audit, F1 and F2

// EVERY WORD MUST APPEAR, AND THE TOOL SAYS SO. A new line ran `check --words "the Windows
// CI skips steps"` against the open card *the windows runner skips three steps*, read
// `CHECK OK matched=0`, and filed the duplicate the tool exists to prevent — because
// matching is an AND and the only place that was written for a person was the refusal for
// an empty --words. A dumb matcher a filer cannot predict is the clever matcher this tool
// refused to build, wearing a different hat.
func TestCheckSaysThatEveryWordMustAppear(t *testing.T) {
	t.Parallel()
	if !strings.Contains(usage, "EVERY WORD APPEARS") {
		t.Error("the banner does not say that a card matches only when EVERY word appears")
	}
	readme := readFile(t, filepath.Join("..", "..", "README.md"))
	nova := readme[strings.Index(readme, "## nova-board"):]
	if !strings.Contains(nova, "every word") && !strings.Contains(nova, "EVERY word") {
		t.Error("the README's nova-board section does not say that every word must appear")
	}
	b := newBench(t)
	b.add(plain("bo", "the windows runner skips three steps")...)
	exit, stdout, stderr := b.run(b.board("check", "--words", "the Windows CI skips steps")...)
	if exit != 0 || !strings.Contains(stdout, "matched=0") {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
	if !strings.Contains(stdout, "BOARD NOTE") || !strings.Contains(stdout, "EVERY word") {
		t.Errorf("matched=0 over five words says nothing about why:\n%s%s", stdout, stderr)
	}
	// Two words is a query a person can hold in their head; the remedy line is for the
	// long phrase that cannot match, not for every miss.
	_, stdout, _ = b.run(b.board("check", "--words", "windows elephant")...)
	if strings.Contains(stdout, "EVERY word") {
		t.Errorf("a two-word miss carries the remedy line:\n%s", stdout)
	}
}

// A TOO-BROAD --words MUST NEVER LOOK LIKE A NO NOBODY MEANT. `check --words the` matched
// every card on the board and exited 1; the guard reads 1 as "already filed" and the
// finding is never filed — the same lost card as a false duplicate, reached from the other
// side. Exit 2 is the guard's could-not-run arm, which is what a query that answered
// nothing actually is.
func TestABroadCheckIsACouldNotRunAndNeverANo(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.add(plain("bo", "the windows runner skips three steps")...)
	b.add(plain("emma", "the token ledger has no September rows yet")...)
	exit, stdout, stderr := b.run(b.board("check", "--words", "the")...)
	if exit != 2 {
		t.Errorf("a match carried only by a word in more than half the board: exit %d, want 2\n%s%s", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "CHECK REFUSED") {
		t.Errorf("the refusal is not named on stderr: %q", stderr)
	}
	if !strings.Contains(stdout, "CHECK OK matched=2 cards=2") {
		t.Errorf("the counts do not print on failure:\n%s", stdout)
	}
	// A rare word still says NO, and that is the sentence the whole tool rests on.
	exit, _, _ = b.run(b.board("check", "--words", "windows")...)
	if exit != 1 {
		t.Errorf("a real match: exit %d, want 1", exit)
	}
	// A common word beside a rare one is a real query: the rare word is what matched.
	exit, _, _ = b.run(b.board("check", "--words", "the windows")...)
	if exit != 1 {
		t.Errorf("a query with one rare word in it: exit %d, want 1", exit)
	}
}

// A VALUE THAT WAS GIVEN AND WOULD NOT PARSE IS NOT A MISSING FLAG. Lesson 13: telling
// somebody their To: line is absent when it is there and misspelled is a refusal about
// nothing — and the new line who passed `--stale 10` re-passed `--stale 10` and was refused
// the same way, because the parse failure was folded into the missing-flag hint.
func TestAMalformedValueIsRefusedForWhatItIsNotForBeingAbsent(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.add(plain("rowan", "a thing on the board")...)
	for _, tc := range []struct{ value, want string }{
		{"10", "is not a duration"},
		{"abc", "is not a duration"},
		{"0", "is not a window"},
		{"-5m", "is not a window"},
	} {
		exit, _, stderr := b.run(b.board("list", "--stale", tc.value)...)
		if exit != 2 {
			t.Errorf("--stale %s: exit %d, want 2", tc.value, exit)
		}
		if strings.Contains(stderr, "is required") {
			t.Errorf("--stale %s was given and is refused as absent: %q", tc.value, stderr)
		}
		if !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, tc.value) {
			t.Errorf("--stale %s: stderr %q, want it to name the value and say %q", tc.value, stderr, tc.want)
		}
		if !strings.Contains(stderr, "10m") {
			t.Errorf("--stale %s: the refusal does not show what a duration looks like: %q", tc.value, stderr)
		}
	}
	// A malformed --card is the same shape: it was given, and it is not thirty-two hex.
	exit, _, stderr := b.run(b.board("take", "--as", "rowan", "--card", "6b8ab31", "--stale", "10m")...)
	if exit != 2 || strings.Contains(stderr, "is required") {
		t.Errorf("--card 6b8ab31: exit %d, stderr %q", exit, stderr)
	}
	if !strings.Contains(stderr, "6b8ab31") || !strings.Contains(stderr, "is not a card id") {
		t.Errorf("--card 6b8ab31: stderr %q", stderr)
	}
	// The flags are still REQUIRED when they are absent, in the words that say so.
	if _, _, stderr := b.run(b.board("list")...); !strings.Contains(stderr, "--stale is required") {
		t.Errorf("a missing --stale: %q", stderr)
	}
}

// --open AND --owner WITHOUT --list ARE A REFUSAL, NOT A SILENT NO-OP. A line asking "what
// is open" got the counts and no cards, byte-identical to the run without the flag, and
// read that as "nothing is open". Rule 1 keeps cards behind --list; a filter for a listing
// nobody asked for is an invocation that cannot mean what it says.
func TestAFilterWithoutAListingIsRefused(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	b.add(plain("rowan", "a thing on the board")...)
	for _, args := range [][]string{
		{"list", "--stale", "10m", "--open"},
		{"list", "--stale", "10m", "--owner", "rowan"},
		{"list", "--stale", "10m", "--open", "--owner", "rowan"},
	} {
		exit, stdout, stderr := b.run(b.board(args...)...)
		if exit != 2 {
			t.Errorf("%v: exit %d, want 2\n%s%s", args, exit, stdout, stderr)
		}
		if !strings.Contains(stderr, "--list") {
			t.Errorf("%v: the refusal does not name --list: %q", args, stderr)
		}
		if stdout != "" {
			t.Errorf("%v: a refusal wrote to stdout: %q", args, stdout)
		}
	}
	// With --list they are the filters they are documented as, and without either the
	// default view is the counts.
	if exit, stdout, _ := b.run(b.board("list", "--stale", "10m", "--list", "--open")...); exit != 0 || count(stdout, "BOARD CARD") != 1 {
		t.Errorf("--list --open: exit %d\n%s", exit, stdout)
	}
	if exit, stdout, _ := b.run(b.board("list", "--stale", "10m")...); exit != 0 || count(stdout, "BOARD CARD") != 0 {
		t.Errorf("the default view: exit %d\n%s", exit, stdout)
	}
}
