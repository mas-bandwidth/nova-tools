package main

// The snapshot continuation and the read cursor, from docs/SPEC-BUS-REPLY.md draft 8:
// the opaque token and --after, the gap lines, the cursor rule, and the refusals.
//
// The frame and the bounds are in readhalf_test.go, which also holds the fixtures and the
// consume-and-assert frame reader these tests use.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// busState is every file a read must not touch, plus the commit both sides of the remote
// stand at, so that "writes nothing" is asserted against the bus and not against one file.
type busState struct {
	head, origin, status string
	files                map[string]string
}

func readBusState(t *testing.T, checkout string) busState {
	t.Helper()
	s := busState{
		head:   strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD")),
		origin: strings.TrimSpace(gitIn(t, checkout, "rev-parse", "origin/main")),
		status: gitIn(t, checkout, "status", "--porcelain"),
		files:  map[string]string{},
	}
	for _, lane := range []string{"from-ada", "from-bo"} {
		for _, name := range []string{"CURSOR", "OPEN", "RECEIPTS", "INDEX"} {
			p := filepath.Join(checkout, lane, name)
			raw, err := os.ReadFile(p)
			if err != nil {
				s.files[lane+"/"+name] = "<absent>"
				continue
			}
			s.files[lane+"/"+name] = string(raw)
		}
	}
	return s
}

func (s busState) mustEqual(t *testing.T, other busState, what string) {
	t.Helper()
	if s.head != other.head || s.origin != other.origin || s.status != other.status {
		t.Fatalf("%s moved the checkout: head %s->%s origin %s->%s status %q->%q",
			what, s.head, other.head, s.origin, other.origin, s.status, other.status)
	}
	for name, was := range s.files {
		if other.files[name] != was {
			t.Fatalf("%s changed %s", what, name)
		}
	}
}

// tokenKeys is every key the one schema writes, so that a token can be asserted to carry no
// id, no body, no subject and no unbounded item list.
var tokenKeys = map[string]bool{"v": true, "b": true, "h": true, "r": true, "s": true, "l": true, "g": true, "n": true, "f": true, "e": true}

func assertTokenShape(t *testing.T, token string) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for k := range keys {
		if !tokenKeys[k] {
			t.Fatalf("token carries %q, which is not in the one schema: %s", k, raw)
		}
	}
}

// ----------------------------------------------------------------- items, pages and resume

// TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither: one commit adds two notes and a page
// cut inside it loses neither of them, and never advances past the commit it cut.
func TestTwoNotesInOneCommitWithMaxNotesOneLosesNeither(t *testing.T) {
	t.Parallel()
	checkout := settledBus(t)
	commitFiles(t, checkout, "two notes in one commit",
		busFile{"from-bo/a-note.md", noteFrom("nA", "a", fill(140))},
		busFile{"from-bo/b-note.md", noteFrom("nB", "b", fill(155))})

	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1").mustCode(t, 0)
	const page1 = "INBOX BODIES printed=1 bytes=140 oversize=0 gaps=0 drained=false complete=false next="
	if !strings.Contains(first.stdout, page1) {
		t.Fatalf("page 1 is not %q:\n%s", page1, first.stdout)
	}
	if n := strings.Count(first.stdout, "INBOX NOTE id="); n != 1 {
		t.Fatalf("page 1 printed %d INBOX NOTE lines, want exactly one:\n%s", n, first.stdout)
	}
	token := bodyNext(t, first.stdout)
	if decoded := decodeToken(t, token); decoded.Last == nil || decoded.Last.Path != "from-bo/a-note.md" {
		t.Fatalf("page 1's token does not decode to A: %+v", decoded)
	}
	second := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1", "--after", token).mustCode(t, 0)
	const page2 = "INBOX BODIES printed=1 bytes=155 oversize=0 gaps=0 drained=true complete=true next=-"
	if !strings.Contains(second.stdout, page2) {
		t.Fatalf("page 2 is not %q:\n%s", page2, second.stdout)
	}
	if frames := readFrames(t, second.stdout); len(frames) != 1 || frames[0].id != "nB" {
		t.Fatalf("page 2 did not print B whole:\n%s", second.stdout)
	}

	t.Run("FinalPartialCommit", func(t *testing.T) {
		// c1 is given a parent that is itself a whole eligible commit, so that a page cut
		// inside c1 has a frontier to name that is not merely the cursor it started at.
		cut := settledBus(t)
		commitFiles(t, cut, "c0", busFile{"from-bo/z-note.md", noteFrom("nZ", "z", fill(100))})
		parent := strings.TrimSpace(gitIn(t, cut, "rev-parse", "HEAD"))
		commitFiles(t, cut, "c1",
			busFile{"from-bo/a-note.md", noteFrom("nA", "a", fill(140))},
			busFile{"from-bo/b-note.md", noteFrom("nB", "b", fill(155))})
		c1 := strings.TrimSpace(gitIn(t, cut, "rev-parse", "HEAD"))
		r := invoke(t, "", "inbox", "--bus", cut, "--as", "Ada", "--receipt-max-words", "40",
			"--bodies", "--max-notes", "2", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
		want := "INBOX CURSOR commit=" + parent
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("safe frontier is not c1's parent; want %q:\n%s", want, r.stdout)
		}
		if strings.Contains(r.stdout, "INBOX CURSOR commit="+c1) {
			t.Fatalf("the cursor crossed the commit the page cut inside:\n%s", r.stdout)
		}
	})

	t.Run("CE3LegacyIdsAndDisplayOrder", func(t *testing.T) {
		// Two legacy notes in one commit whose display order is not their scan order: the
		// receipt-shaped one sorts first by path and prints last by display group.
		legacy := settledBus(t)
		commitFiles(t, legacy, "two legacy notes in one commit",
			busFile{"from-bo/a-legacy.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:00:00 UTC 2026\nSubject: a legacy receipt\nKind: receipt\n\nreceived"},
			busFile{"from-bo/b-legacy.md", legacyNoteFrom("b legacy", fill(120))})
		seen := map[string]int{}
		args := []string{"inbox", "--bus", legacy, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1"}
		run := append([]string{}, args...)
		for page := 1; page <= 4; page++ {
			r := invoke(t, "", run...).mustCode(t, 0)
			for _, line := range strings.Split(r.stdout, "\n") {
				for _, p := range []string{"from-bo/a-legacy.md", "from-bo/b-legacy.md"} {
					if strings.Contains(line, "path="+p) && strings.HasPrefix(line, "INBOX ") {
						seen[p]++
					}
				}
			}
			next := ""
			for _, field := range strings.Fields(r.stdout) {
				if tok, ok := strings.CutPrefix(field, "next="); ok {
					next = tok
				}
			}
			if next == "" {
				t.Fatalf("page %d carried no next= field:\n%s", page, r.stdout)
			}
			if next == "-" {
				break
			}
			assertTokenShape(t, next)
			decoded := decodeToken(t, next)
			if decoded.Last == nil || decoded.Last.Path == "" {
				t.Fatalf("a token's item identity is not a path: %+v", decoded)
			}
			if strings.Contains(string(mustDecode(t, next)), `"-"`) {
				t.Fatalf("a token carries an id=- identity: %s", mustDecode(t, next))
			}
			run = append(append([]string{}, args...), "--after", next)
			if page == 4 {
				t.Fatalf("the chain did not drain in four pages")
			}
		}
		for _, p := range []string{"from-bo/a-legacy.md", "from-bo/b-legacy.md"} {
			if seen[p] != 1 {
				t.Fatalf("%s appeared %d times across the chain, want exactly once", p, seen[p])
			}
		}
	})
}

// mustPrintNoBodies asserts that a refused continuation delivered nothing: no summary
// line, no frame, no receipt. The INBOX SCOPE line that stands above it is the run saying
// what it was asked, and printing it is not delivering a note.
func mustPrintNoBodies(t *testing.T, r result) {
	t.Helper()
	for _, token := range []string{"INBOX NOTE id=", "INBOX BODY", "INBOX BODIES"} {
		if strings.Contains(r.stdout, token) {
			t.Fatalf("a refused continuation printed %s:\n%s", token, r.stdout)
		}
	}
}

func mustDecode(t *testing.T, token string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestContinuationSurvivesOrdinaryCursorAdvance (CE1): a cursor advanced by page 1 does not
// invalidate page 2's token; a cursor changed by somebody else does, and rewinds nothing.
func TestContinuationSurvivesOrdinaryCursorAdvance(t *testing.T) {
	t.Parallel()
	checkout := settledBus(t)
	commitFiles(t, checkout, "c1", busFile{"from-bo/a.md", noteFrom("nA", "a", fill(140))})
	c1 := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	commitFiles(t, checkout, "c2", busFile{"from-bo/b.md", noteFrom("nB", "b", fill(155))})

	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	if !strings.Contains(first.stdout, "INBOX CURSOR commit="+c1) {
		t.Fatalf("page 1 did not move CURSOR to c1:\n%s", first.stdout)
	}
	token := bodyNext(t, first.stdout)
	second := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1", "--after", token).mustCode(t, 0)
	const page2 = "INBOX BODIES printed=1 bytes=155 oversize=0 gaps=0 drained=true complete=true next=-"
	if !strings.Contains(second.stdout, page2) {
		t.Fatalf("page 2 is not %q after an ordinary cursor advance:\n%s", page2, second.stdout)
	}

	// A distinct external cursor change instead refuses, and never rewinds state.
	other := settledBus(t)
	commitFiles(t, other, "c1", busFile{"from-bo/a.md", noteFrom("nA", "a", fill(140))})
	otherC1 := strings.TrimSpace(gitIn(t, other, "rev-parse", "HEAD"))
	commitFiles(t, other, "c2", busFile{"from-bo/b.md", noteFrom("nB", "b", fill(155))})
	page1 := invoke(t, "", "inbox", "--bus", other, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	stale := bodyNext(t, page1.stdout)
	invoke(t, "", "inbox", "--bus", other, "--as", "Ada", "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	moved, err := bus.ReadCursor(other, "from-ada")
	if err != nil {
		t.Fatal(err)
	}
	before := readBusState(t, other)
	refused := invoke(t, "", "inbox", "--bus", other, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1", "--after", stale).mustCode(t, 2)
	want := fmt.Sprintf("INBOX REFUSED: --after names cursor %s and this reader's cursor is %s; rerun without --after", otherC1, moved.Commit)
	if got := strings.TrimRight(refused.stderr, "\n"); got != want {
		t.Fatalf("external cursor change refusal is\n  %q\nwant\n  %q", got, want)
	}
	readBusState(t, other).mustEqual(t, before, "a refused continuation")
}

// TestRetryAfterAPartialResumesAtNext: a chain drains a fixed snapshot, each accounted item
// appears once in it, a tip that grows waits for a fresh chain, and a token that names no
// item in the range refuses and writes nothing.
func TestRetryAfterAPartialResumesAtNext(t *testing.T) {
	t.Parallel()
	for _, advance := range []bool{false, true} {
		name := "ReadOnly"
		if advance {
			name = "WithAdvance"
		}
		t.Run(name, func(t *testing.T) {
			checkout := settledBus(t)
			for i := 1; i <= 3; i++ {
				commitFiles(t, checkout, fmt.Sprintf("r%d", i),
					busFile{fmt.Sprintf("from-bo/r%d.md", i), noteFrom(fmt.Sprintf("nR%d", i), fmt.Sprintf("r%d", i), fill(100+i))})
			}
			args := []string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies", "--max-notes", "1"}
			if advance {
				args = append(args, "--advance", "--remote", "origin", "--branch", "main")
			}
			seen := map[string]int{}
			var last result
			run := append([]string{}, args...)
			for page := 1; ; page++ {
				if page > 5 {
					t.Fatalf("the chain did not drain")
				}
				last = invoke(t, "", run...).mustCode(t, 0)
				for _, f := range readFrames(t, last.stdout) {
					seen[f.id]++
				}
				if page == 1 {
					// The tip grows under the chain: a commit after H is not in it.
					commitFiles(t, checkout, "after H",
						busFile{"from-bo/r4.md", noteFrom("nR4", "r4", fill(104))})
				}
				next := bodyNext2(t, last.stdout)
				if next == "-" {
					break
				}
				run = append(append([]string{}, args...), "--after", next)
			}
			if !strings.Contains(last.stdout, "drained=true complete=true next=-") {
				t.Fatalf("the chain's last page is not drained and complete:\n%s", last.stdout)
			}
			if strings.Contains(last.stdout, "nR4") {
				t.Fatalf("a commit pushed after H appeared in this chain:\n%s", last.stdout)
			}
			for _, id := range []string{"nR1", "nR2", "nR3"} {
				if seen[id] != 1 {
					t.Fatalf("%s appeared %d times in the chain, want once", id, seen[id])
				}
			}
			if seen["nR4"] != 0 {
				t.Fatalf("the post-H note was delivered by the old chain")
			}
			// It is on the fresh chain that follows -- and where the chain advanced the
			// cursor it is the FIRST thing on it, while a read-only chain moved nothing
			// and so starts again from the persisted cursor, which may re-show a body.
			fresh := invoke(t, "", args...).mustCode(t, 0)
			frames := readFrames(t, fresh.stdout)
			if len(frames) == 0 {
				t.Fatalf("the post-H note is on no chain at all:\n%s", fresh.stdout)
			}
			if advance {
				if len(frames) != 1 || frames[0].id != "nR4" {
					t.Fatalf("the post-H note was not first on the fresh chain:\n%s", fresh.stdout)
				}
			} else if frames[0].id != "nR1" {
				t.Fatalf("a read-only chain moved the cursor: the fresh chain starts at %s\n%s", frames[0].id, fresh.stdout)
			}
		})
	}

	t.Run("MismatchedTokenRefusesAndWritesNothing", func(t *testing.T) {
		checkout := settledBus(t)
		commitFiles(t, checkout, "m1", busFile{"from-bo/m1.md", noteFrom("nM1", "m1", fill(100))})
		commitFiles(t, checkout, "m2", busFile{"from-bo/m2.md", noteFrom("nM2", "m2", fill(100))})
		first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
			"--bodies", "--max-notes", "1").mustCode(t, 0)
		stray := retoken(t, bodyNext(t, first.stdout), "from-bo/m1.md", "from-bo/gone.md")
		before := readBusState(t, checkout)
		r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
			"--bodies", "--max-notes", "1", "--after", stray).mustCode(t, 2)
		const want = "INBOX REFUSED: --after <token> names no item in this range; rerun without --after"
		if got := strings.TrimRight(r.stderr, "\n"); got != want {
			t.Fatalf("refusal is\n  %q\nwant\n  %q", got, want)
		}
		mustPrintNoBodies(t, r)
		readBusState(t, checkout).mustEqual(t, before, "a refused continuation")
	})
}

// bodyNext2 is bodyNext that also answers "-", which is what a terminal page carries.
func bodyNext2(t *testing.T, stdout string) string {
	t.Helper()
	for _, field := range strings.Fields(stdout) {
		if token, ok := strings.CutPrefix(field, "next="); ok {
			return token
		}
	}
	t.Fatalf("no next= field:\n%s", stdout)
	return ""
}

// TestBodiesWithoutAdvanceMovesNoCursor: complete, partial, empty and gapped returns write
// nothing at all, and the partial one still carries a usable continuation.
func TestBodiesWithoutAdvanceMovesNoCursor(t *testing.T) {
	t.Parallel()
	checkout := settledBus(t)
	commitFiles(t, checkout, "n1", busFile{"from-bo/n1.md", noteFrom("nN1", "n1", fill(100))})
	commitFiles(t, checkout, "n2", busFile{"from-bo/n2.md", noteFrom("nN2", "n2", fill(100))})
	commitFiles(t, checkout, "big", busFile{"from-bo/big.md", noteFrom("nBigger", "big", fill(4096))})

	base := []string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies"}
	before := readBusState(t, checkout)

	complete := invoke(t, "", append(append([]string{}, base...), "--max-notes", "10")...).mustCode(t, 0)
	if !strings.Contains(complete.stdout, "drained=true complete=true next=-") {
		t.Fatalf("the complete return is not complete:\n%s", complete.stdout)
	}
	readBusState(t, checkout).mustEqual(t, before, "a complete read-only return")

	partial := invoke(t, "", append(append([]string{}, base...), "--max-notes", "1")...).mustCode(t, 0)
	token := bodyNext(t, partial.stdout)
	if !strings.Contains(partial.stdout, "next="+token) {
		t.Fatalf("the partial return does not carry its token:\n%s", partial.stdout)
	}
	frames := readFrames(t, partial.stdout)
	if len(frames) != 1 {
		t.Fatalf("the partial return printed %d frames, want 1", len(frames))
	}
	decoded := decodeToken(t, token)
	if decoded.Last == nil || decoded.Last.Path != "from-bo/n1.md" {
		t.Fatalf("the token does not decode to the last accounted item: %+v", decoded)
	}
	readBusState(t, checkout).mustEqual(t, before, "a partial read-only return")

	gapped := invoke(t, "", append(append([]string{}, base...), "--max-bytes", "1024")...).mustCode(t, 0)
	if !strings.Contains(gapped.stdout, "INBOX BODY OVERSIZE id=nBigger") {
		t.Fatalf("the gapped return named no gap:\n%s", gapped.stdout)
	}
	readBusState(t, checkout).mustEqual(t, before, "a gapped read-only return")

	empty := settledBus(t)
	emptyBefore := readBusState(t, empty)
	quiet := invoke(t, "", "inbox", "--bus", empty, "--as", "Ada", "--receipt-max-words", "40", "--bodies").mustCode(t, 0)
	if !strings.Contains(quiet.stdout, "INBOX BODIES printed=0 bytes=0 oversize=0 gaps=0 drained=true complete=true next=-") {
		t.Fatalf("an empty snapshot is drained and complete:\n%s", quiet.stdout)
	}
	readBusState(t, empty).mustEqual(t, emptyBefore, "an empty read-only return")

	t.Run("ReadOnlyResume", func(t *testing.T) {
		cursorBefore := before.files["from-ada/CURSOR"]
		run := append(append([]string{}, base...), "--max-notes", "1", "--after", token)
		var last result
		for page := 2; ; page++ {
			if page > 6 {
				t.Fatalf("the read-only chain did not drain")
			}
			last = invoke(t, "", run...).mustCode(t, 0)
			next := bodyNext2(t, last.stdout)
			if next == "-" {
				break
			}
			run = append(append(append([]string{}, base...), "--max-notes", "1"), "--after", next)
		}
		if !strings.Contains(last.stdout, "drained=true") || !strings.Contains(last.stdout, "complete=true") || !strings.Contains(last.stdout, "next=-") {
			t.Fatalf("the read-only chain did not reach a terminal page:\n%s", last.stdout)
		}
		after := readBusState(t, checkout)
		if after.files["from-ada/CURSOR"] != cursorBefore {
			t.Fatalf("a read-only chain changed CURSOR")
		}
		after.mustEqual(t, before, "a read-only chain")
	})
}

// ------------------------------------------------------------------------------ the gaps

// TestASingleOversizeBodyIsANamedGapAndNeverALoop: the only item's body is over the hard
// ceiling, so no --max-bytes carries it; it is named once and the return is terminal.
func TestASingleOversizeBodyIsANamedGapAndNeverALoop(t *testing.T) {
	t.Parallel()
	checkout := settledBus(t)
	commitFiles(t, checkout, "one very large note",
		busFile{"from-x/2026-09-13-big.md", noteFrom("nBig", "big", fill(2097152))})

	before := readBusState(t, checkout)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--bodies").mustCode(t, 0)
	for _, want := range []string{
		"INBOX BODY OVERSIZE id=nBig bytes=2097152 max-bytes=65536 path=from-x/2026-09-13-big.md",
		"INBOX BODIES GAP id=nBig kind=over-ceiling retry-max-bytes=- path=from-x/2026-09-13-big.md",
		"INBOX BODIES printed=0 bytes=0 oversize=1 gaps=1 drained=true complete=false next=-",
	} {
		if !strings.Contains(r.stdout, want+"\n") {
			t.Fatalf("stdout does not carry\n  %q\n%s", want, r.stdout)
		}
	}
	gap := strings.Index(r.stdout, "INBOX BODIES GAP ")
	receipt := strings.Index(r.stdout, "INBOX BODIES printed=")
	if gap < 0 || receipt < 0 || gap > receipt {
		t.Fatalf("the remedy is not immediately before the receipt:\n%s", r.stdout)
	}
	if between := r.stdout[gap:receipt]; strings.Count(between, "\n") != 1 {
		t.Fatalf("a line stands between the remedy and the receipt:\n%s", r.stdout)
	}
	if len(readFrames(t, r.stdout)) != 0 {
		t.Fatalf("a frame was opened for an item over the ceiling:\n%s", r.stdout)
	}
	if strings.Count(r.stdout, "INBOX BODIES GAP") != 1 {
		t.Fatalf("the gap was named more than once:\n%s", r.stdout)
	}
	readBusState(t, checkout).mustEqual(t, before, "a gapped return")

	// No cursor advance, and no drain call to repeat: next=- is the end of the chain.
	adv := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	if strings.Contains(adv.stdout, "INBOX CURSOR") {
		t.Fatalf("a return holding only a gap advanced the cursor:\n%s", adv.stdout)
	}
}

// TestEarlierGapSurvivesLaterPages (CE2): a gap on page 1 is still named on the terminal
// page, and the cursor never crosses the commit it is in.
func TestEarlierGapSurvivesLaterPages(t *testing.T) {
	t.Parallel()
	checkout := settledBus(t)
	commitFiles(t, checkout, "c1", busFile{"from-bo/g-a.md", noteFrom("nA", "a", fill(2048))})
	c1 := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	commitFiles(t, checkout, "c2", busFile{"from-bo/g-b.md", noteFrom("nB", "b", fill(150))})
	commitFiles(t, checkout, "c3", busFile{"from-bo/g-c.md", noteFrom("nC", "c", fill(150))})

	base := []string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1", "--max-bytes", "1024", "--advance", "--remote", "origin", "--branch", "main"}
	page1 := invoke(t, "", base...).mustCode(t, 0)
	if !strings.Contains(page1.stdout, "INBOX BODY OVERSIZE id=nA bytes=2048 max-bytes=1024 path=from-bo/g-a.md\n") {
		t.Fatalf("page 1 did not name the gap:\n%s", page1.stdout)
	}
	if !strings.Contains(page1.stdout, "INBOX BODIES printed=0 bytes=0 oversize=1 gaps=1 drained=false complete=false next=") {
		t.Fatalf("page 1's receipt is wrong:\n%s", page1.stdout)
	}
	page2 := invoke(t, "", append(append([]string{}, base...), "--after", bodyNext(t, page1.stdout))...).mustCode(t, 0)
	const want2 = "INBOX BODIES printed=1 bytes=150 oversize=0 gaps=1 drained=false complete=false next="
	if !strings.Contains(page2.stdout, want2) {
		t.Fatalf("page 2 is not %q:\n%s", want2, page2.stdout)
	}
	page3 := invoke(t, "", append(append([]string{}, base...), "--after", bodyNext(t, page2.stdout))...).mustCode(t, 0)
	const want3 = "INBOX BODIES printed=1 bytes=150 oversize=0 gaps=1 drained=true complete=false next=-"
	if !strings.Contains(page3.stdout, want3) {
		t.Fatalf("the terminal page is not %q:\n%s", want3, page3.stdout)
	}
	if !strings.Contains(page3.stdout, "INBOX BODIES GAP id=nA kind=over-budget retry-max-bytes=2048 path=from-bo/g-a.md\n") {
		t.Fatalf("the terminal page did not keep the earliest gap:\n%s", page3.stdout)
	}
	if strings.Count(page3.stdout, "INBOX BODIES GAP") != 1 {
		t.Fatalf("a chain holding a gap printed a list of them:\n%s", page3.stdout)
	}
	for _, r := range []result{page1, page2, page3} {
		if strings.Contains(r.stdout, "INBOX CURSOR commit="+c1) {
			t.Fatalf("CURSOR crossed the gap's own commit:\n%s", r.stdout)
		}
	}
	cursor, err := bus.ReadCursor(checkout, "from-ada")
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Commit == c1 {
		t.Fatalf("CURSOR stands at the gap's commit")
	}

	// A fresh raised-budget run re-shows A; it was never silently consumed.
	fresh := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-bytes", "65536").mustCode(t, 0)
	got := readFrames(t, fresh.stdout)
	if len(got) == 0 || got[0].id != "nA" {
		t.Fatalf("a fresh raised-budget run did not re-show A:\n%s", fresh.stdout)
	}

	t.Run("SeveralGapsKeepConstantSizedState", func(t *testing.T) {
		many := settledBus(t)
		for i := 1; i <= 3; i++ {
			commitFiles(t, many, fmt.Sprintf("gap %d", i),
				busFile{fmt.Sprintf("from-bo/gap-%d.md", i), noteFrom(fmt.Sprintf("nG%d", i), fmt.Sprintf("g%d", i), fill(2048))})
		}
		commitFiles(t, many, "small", busFile{"from-bo/gap-small.md", noteFrom("nGs", "gs", fill(150))})
		args := []string{"inbox", "--bus", many, "--as", "Ada", "--receipt-max-words", "40",
			"--bodies", "--max-notes", "1", "--max-bytes", "1024"}
		sizes := map[int]bool{}
		run := append([]string{}, args...)
		var last result
		for page := 1; ; page++ {
			if page > 6 {
				t.Fatalf("the chain did not drain")
			}
			last = invoke(t, "", run...).mustCode(t, 0)
			next := bodyNext2(t, last.stdout)
			if next == "-" {
				break
			}
			assertTokenShape(t, next)
			decoded := decodeToken(t, next)
			if decoded.Gap == nil || decoded.Gap.Path != "from-bo/gap-1.md" {
				t.Fatalf("a later token forgot the earliest gap: %+v", decoded)
			}
			sizes[len(mustDecode(t, next))] = true
			run = append(append([]string{}, args...), "--after", next)
		}
		if !strings.Contains(last.stdout, "gaps=3 drained=true complete=false next=-") {
			t.Fatalf("the terminal page did not carry all three gaps:\n%s", last.stdout)
		}
		if strings.Count(last.stdout, "INBOX BODIES GAP") != 1 {
			t.Fatalf("three gaps printed more than one remedy:\n%s", last.stdout)
		}
		if !strings.Contains(last.stdout, "INBOX BODIES GAP id=nG1 ") {
			t.Fatalf("the remedy does not name the earliest gap:\n%s", last.stdout)
		}
		if len(sizes) > 2 {
			t.Fatalf("token size grew with the gap count: %v", sizes)
		}
	})
}

// makeNonAncestorToken creates a token whose base is not an ancestor of its head by using
// two divergent branches from a common ancestor.
func makeNonAncestorToken(t *testing.T, checkout string) string {
	t.Helper()
	gitIn(t, checkout, "checkout", "-b", "divergent")
	commitFiles(t, checkout, "div-v1", busFile{"from-bo/div-v1.md", noteFrom("nD1", "div-v1", fill(100))})
	divHead := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	gitIn(t, checkout, "checkout", "main")
	commitFiles(t, checkout, "main-v1", busFile{"from-bo/main-v1.md", noteFrom("nM1", "main-v1", fill(100))})
	mainHead := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1").mustCode(t, 0)
	good := bodyNext(t, first.stdout)
	tok := decodeToken(t, good)
	return retoken(t, retoken(t, good, `"h":"`+tok.Head+`"`, `"h":"`+divHead+`"`), `"b":"`+tok.Base+`"`, `"b":"`+mainHead+`"`)
}

// TestSnapshotTokenValidationAndBound: every way a token can be wrong refuses at exit 2 in
// one shape, and none of them confers any authority.
func TestSnapshotTokenValidationAndBound(t *testing.T) {
	t.Parallel()
	checkout := settledBus(t)
	commitFiles(t, checkout, "v1", busFile{"from-bo/v1.md", noteFrom("nV1", "v1", fill(100))})
	commitFiles(t, checkout, "v2", busFile{"from-bo/v2.md", noteFrom("nV2", "v2", fill(100))})
	first := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1").mustCode(t, 0)
	good := bodyNext(t, first.stdout)
	absent := strings.Repeat("0", 40)

	for _, tc := range []struct{ name, token string }{
		{"unknown version", retoken(t, good, `"v":1`, `"v":2`)},
		{"over 8 KiB", base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"s":"` + strings.Repeat("x", 9000) + `"}`))},
		{"not base64", "!!!not a token!!!"},
		{"wrong reader", retoken(t, good, `"r":"Ada"`, `"r":"Bo"`)},
		{"wrong selector", retoken(t, good, `"s":"inbox-new"`, `"s":"other"`)},
		{"unavailable snapshot", retoken(t, good, decodeToken(t, good).Head, absent)},
		{"non-ancestor base", makeNonAncestorToken(t, checkout)},
		{"invalid item path", retoken(t, good, `"p":"from-bo/v1.md"`, `"p":"../../etc/passwd"`)},
		{"invalid item offset", retoken(t, good, `"o":0`, `"o":-1`)},
		{"frontier past an unaccounted partial commit", retoken(t, good, `"f":"`+decodeToken(t, good).Frontier+`"`, `"f":"`+decodeToken(t, good).Head+`"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := readBusState(t, checkout)
			r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
				"--bodies", "--max-notes", "1", "--after", tc.token).mustCode(t, 2)
			got := strings.TrimRight(r.stderr, "\n")
			if !strings.HasPrefix(got, "INBOX REFUSED: --after <token> is not a continuation for this read: ") ||
				!strings.HasSuffix(got, "; rerun without --after") {
				t.Fatalf("refusal is not the one shape:\n  %q", got)
			}
			mustPrintNoBodies(t, r)
			readBusState(t, checkout).mustEqual(t, before, "a refused token")
		})
	}

	// The other half of the frontier rule: a token may not claim a frontier past its own
	// GAP either, and the fixture for that needs a gap in it.
	t.Run("frontier past a gap", func(t *testing.T) {
		gapped := settledBus(t)
		commitFiles(t, gapped, "g1", busFile{"from-bo/g1.md", noteFrom("nGa", "g1", fill(2048))})
		commitFiles(t, gapped, "g2", busFile{"from-bo/g2.md", noteFrom("nGb", "g2", fill(150))})
		page1 := invoke(t, "", "inbox", "--bus", gapped, "--as", "Ada", "--receipt-max-words", "40",
			"--bodies", "--max-notes", "1", "--max-bytes", "1024").mustCode(t, 0)
		token := bodyNext(t, page1.stdout)
		if decodeToken(t, token).Gap == nil {
			t.Fatalf("the fixture produced no gap:\n%s", page1.stdout)
		}
		bad := retoken(t, token, `"f":""`, `"f":"`+decodeToken(t, token).Head+`"`)
		before := readBusState(t, gapped)
		r := invoke(t, "", "inbox", "--bus", gapped, "--as", "Ada", "--receipt-max-words", "40",
			"--bodies", "--max-notes", "1", "--max-bytes", "1024", "--after", bad).mustCode(t, 2)
		got := strings.TrimRight(r.stderr, "\n")
		if !strings.HasPrefix(got, "INBOX REFUSED: --after <token> is not a continuation for this read: ") ||
			!strings.HasSuffix(got, "; rerun without --after") {
			t.Fatalf("refusal is not the one shape:\n  %q", got)
		}
		mustPrintNoBodies(t, r)
		readBusState(t, gapped).mustEqual(t, before, "a refused token")
	})

	// A token is client-supplied query state: it opens no lane it did not already open.
	t.Run("no new authority", func(t *testing.T) {
		r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Bo", "--receipt-max-words", "40",
			"--bodies", "--max-notes", "1", "--after", good).mustCode(t, 2)
		if !strings.Contains(r.stderr, "another reader or selector") {
			t.Fatalf("Ada's token was not refused for Bo: %s", r.stderr)
		}
		if strings.Contains(r.stdout, "from-bo/v1.md") {
			t.Fatalf("a token handed another reader a body:\n%s", r.stdout)
		}
	})
}

// TestBrokenOutputCannotAcknowledgeUnprintedBodies: a cursor never crosses data that did
// not reach stdout, whether the writer fails before a frame or inside one.
func TestBrokenOutputCannotAcknowledgeUnprintedBodies(t *testing.T) {
	t.Parallel()
	for _, after := range []int{0, 1, 200} {
		t.Run(fmt.Sprintf("BreaksAfter%dBytes", after), func(t *testing.T) {
			checkout := settledBus(t)
			commitFiles(t, checkout, "b1", busFile{"from-bo/b1.md", noteFrom("nB1", "b1", fill(204))})
			commitFiles(t, checkout, "b2", busFile{"from-bo/b2.md", noteFrom("nB2", "b2", fill(204))})
			was, err := bus.ReadCursor(checkout, "from-ada")
			if err != nil {
				t.Fatal(err)
			}
			var stderr strings.Builder
			code := run([]string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
				"--bodies", "--advance", "--remote", "origin", "--branch", "main"},
				strings.NewReader(""), &breakingWriter{limit: after}, &stderr, now())
			if code != 1 || !strings.Contains(stderr.String(), "INBOX FAIL output") {
				t.Fatalf("broken stdout did not refuse safely: code=%d stderr=%s", code, stderr.String())
			}
			now, err := bus.ReadCursor(checkout, "from-ada")
			if err != nil {
				t.Fatal(err)
			}
			if now.Commit != was.Commit {
				t.Fatalf("cursor advanced across unprinted output: %s -> %s", was.Commit, now.Commit)
			}
		})
	}

	// The other half of the same rule: when output SUCCEEDS, the cursor names the last
	// fully emitted safe prefix and nothing past it.
	checkout := settledBus(t)
	commitFiles(t, checkout, "c1", busFile{"from-bo/p1.md", noteFrom("nP1", "p1", fill(204))})
	c1 := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	commitFiles(t, checkout, "c2", busFile{"from-bo/p2.md", noteFrom("nP2", "p2", fill(204))})
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--bodies", "--max-notes", "1", "--advance", "--remote", "origin", "--branch", "main").mustCode(t, 0)
	if !strings.Contains(r.stdout, "INBOX CURSOR commit="+c1) {
		t.Fatalf("the cursor did not name the last fully emitted safe prefix:\n%s", r.stdout)
	}
}

// breakingWriter accepts limit bytes and then fails, so that a failure can be placed before
// a frame, on its opening line, or inside its body bytes.
type breakingWriter struct {
	limit   int
	written int
}

func (w *breakingWriter) Write(p []byte) (int, error) {
	if w.written >= w.limit {
		return 0, fmt.Errorf("broken pipe")
	}
	n := len(p)
	if w.written+n > w.limit {
		n = w.limit - w.written
	}
	w.written += n
	if n < len(p) {
		return n, fmt.Errorf("broken pipe")
	}
	return n, nil
}
