package pulse

// The fixture is testdata/cards-done-2680.txt. On 2026-09-22 studio's cards are:
//
//	card-ok        ok, not useful (a pr number on an ok entry is not a landing)
//	card-landed    ok, landed, useful (also receipted an issue; still one card)
//	card-open-pr   ok, not useful (pullreq opened, not merged)
//	card-defect    useful, not landed (verified defect, stated twice)
//	card-issue     useful, not landed (receipted issue)
//	card-old-land  ok today, landed yesterday, so not useful today
//	card-raw-*     not useful (defect and issue without the words that earn it)
//	card-fail, card-queued  neither
//
// studio is therefore ok=4 landed=1 useful=3. hulk is one card, delivered
// twice: ok=1 landed=1 useful=1. card-nowhere is not a row.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func cardsDoneFixture(t *testing.T) []CardDone {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cards-done-2680.txt"))
	if err != nil {
		t.Fatalf("reading the cards:done fixture: %v", err)
	}
	entries, err := ParseCardsDone(string(raw))
	if err != nil {
		t.Fatalf("parsing the cards:done fixture: %v", err)
	}
	return entries
}

func benchRow(t *testing.T, rows []BenchUse, name string) BenchUse {
	t.Helper()
	for _, row := range rows {
		if row.Bench == name {
			return row
		}
	}
	t.Fatalf("no bench %q in %+v", name, rows)
	return BenchUse{}
}

// TestAnOKCardThatDidNotLandIsNotUseful is the score: useful is not the ok
// column. Four cards ended OK on studio; only the one that landed is useful,
// together with the verified defect and the receipted issue.
func TestAnOKCardThatDidNotLandIsNotUseful(t *testing.T) {
	day := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	rows := CountBenchUse(cardsDoneFixture(t), day)
	if len(rows) != 2 {
		t.Fatalf("benches = %+v, want hulk and studio only (a landing with no bench is not a row)", rows)
	}
	studio := benchRow(t, rows, "studio")
	if studio.OK != 4 || studio.Landed != 1 || studio.Useful != 3 {
		t.Fatalf("studio = %+v, want ok=4 landed=1 useful=3 (card-ok, card-open-pr and card-old-land are OK and not useful)", studio)
	}
	if studio.Useful == studio.OK {
		t.Fatalf("useful %d equals ok; an OK card that did not land was counted as useful", studio.Useful)
	}
	hulk := benchRow(t, rows, "hulk")
	if hulk.OK != 1 || hulk.Landed != 1 || hulk.Useful != 1 {
		t.Fatalf("hulk = %+v, want ok=1 landed=1 useful=1 (the second landed entry is the same card)", hulk)
	}
}

// TestYesterdaysLandingIsNotTodaysUseful: the merge counts on the day it
// landed. The card's OK entry is the next day and does not pull the landing
// forward, and it does not make that next day useful.
func TestYesterdaysLandingIsNotTodaysUseful(t *testing.T) {
	day := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	rows := CountBenchUse(cardsDoneFixture(t), day)
	if len(rows) != 1 {
		t.Fatalf("benches = %+v, want studio only", rows)
	}
	studio := rows[0]
	if studio.Bench != "studio" || studio.OK != 0 || studio.Landed != 1 || studio.Useful != 1 {
		t.Fatalf("2026-09-21 studio = %+v, want ok=0 landed=1 useful=1", studio)
	}
}

// TestBenchUseOnNoDayCountsNothing: no day is not "every card".
func TestBenchUseOnNoDayCountsNothing(t *testing.T) {
	rows := CountBenchUse(cardsDoneFixture(t), time.Time{})
	if len(rows) != 0 {
		t.Fatalf("a zero day counted %+v", rows)
	}
}

// TestCardsDoneRefusesALineThatCannotBeCounted: a line with no label, no
// event, a stamp that is not a stamp, or a token that is not a field is not
// silently dropped into the useful column.
func TestCardsDoneRefusesALineThatCannotBeCounted(t *testing.T) {
	lines := []string{
		"bench=studio event=ok at=2026-09-22T00:00:00Z",
		"label=c bench=studio at=2026-09-22T00:00:00Z",
		"label=c bench=studio event=ok at=yesterday",
		"label=c this-is-not-a-field",
	}
	for _, line := range lines {
		if _, err := ParseCardsDone(line + "\n"); err == nil {
			t.Fatalf("line %q was accepted", line)
		}
	}
}

// TestAnUnbenchedLandingJoinsThePullRequestItNames is the control for one
// label opened on two benches. The landing joins the pull request it names
// (pr, head or attempt), not the first bench in the stream. An identity two
// benches claim, a landing that names nothing while two pull requests are
// open, and a head that is not that pull request's head are not a row.
func TestAnUnbenchedLandingJoinsThePullRequestItNames(t *testing.T) {
	day := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	head := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	text := "" +
		"label=retry bench=studio event=pr at=2026-09-22T16:00:00Z pr=100 attempt=1\n" +
		"label=retry bench=space event=pr at=2026-09-22T16:01:00Z pr=101 attempt=2 head=" + head + "\n" +
		"label=retry event=landed at=2026-09-22T17:00:00Z pr=101 head=" + head + "\n"
	entries, err := ParseCardsDone(text)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	var landing CardDone
	for _, e := range entries {
		if e.Label == "retry" && e.Event == "landed" {
			landing = e
		}
	}
	if landing.PR != "101" || landing.Head != head || landing.Attempt != "" || landing.Bench != "" {
		t.Fatalf("landing dropped its identity: %+v", landing)
	}
	rows := CountBenchUse(entries, day)
	space := benchRow(t, rows, "space")
	if space.OK != 0 || space.Landed != 1 || space.Useful != 1 || len(rows) != 1 {
		t.Fatalf("rows = %+v, want only space landed=1 useful=1 (pr 101 is not studio's pr 100)", rows)
	}

	// The other order. pr 200 is studio's and is written second.
	rows = countBenchDay(t, day, `
label=flip bench=space event=pullreq at=2026-09-22T16:00:00Z pr=201
label=flip bench=studio event=pullreq at=2026-09-22T16:01:00Z pr=200
label=flip event=landed at=2026-09-22T17:00:00Z pr=200
`)
	studio := benchRow(t, rows, "studio")
	if studio.Landed != 1 || studio.Useful != 1 || len(rows) != 1 {
		t.Fatalf("rows = %+v, want only studio (the second pullreq is the one that landed)", rows)
	}

	// attempt alone, and head alone, are the same join.
	rows = countBenchDay(t, day, `
label=by-attempt bench=studio event=pr at=2026-09-22T16:00:00Z pr=300 attempt=7
label=by-attempt bench=space event=pr at=2026-09-22T16:01:00Z pr=301 attempt=8
label=by-attempt event=landed at=2026-09-22T17:00:00Z attempt=8
`)
	if got := benchRow(t, rows, "space"); got.Landed != 1 || got.Useful != 1 || len(rows) != 1 {
		t.Fatalf("attempt join = %+v, want space", rows)
	}
	rows = countBenchDay(t, day, `
label=by-head bench=studio event=pr at=2026-09-22T16:00:00Z pr=400 head=dddddddddddddddddddddddddddddddddddddddd
label=by-head bench=space event=pr at=2026-09-22T16:01:00Z pr=401 head=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
label=by-head event=landed at=2026-09-22T17:00:00Z head=eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
`)
	if got := benchRow(t, rows, "space"); got.Landed != 1 || got.Useful != 1 || len(rows) != 1 {
		t.Fatalf("head join = %+v, want space", rows)
	}

	// Ambiguous, or not this pull request: no row to credit.
	for _, text := range []string{
		`
label=ambiguous bench=studio event=pr at=2026-09-22T16:00:00Z pr=500
label=ambiguous bench=space event=pr at=2026-09-22T16:01:00Z pr=500
label=ambiguous event=landed at=2026-09-22T17:00:00Z pr=500
`,
		`
label=no-id bench=studio event=pr at=2026-09-22T16:00:00Z pr=600
label=no-id bench=space event=pr at=2026-09-22T16:01:00Z pr=601
label=no-id event=landed at=2026-09-22T17:00:00Z
`,
		`
label=disagree bench=studio event=pr at=2026-09-22T16:00:00Z pr=800 head=ffffffffffffffffffffffffffffffffffffffff
label=disagree event=landed at=2026-09-22T17:00:00Z pr=800 head=0000000000000000000000000000000000000000
`,
	} {
		rows = countBenchDay(t, day, text)
		if len(rows) != 0 {
			t.Fatalf("ambiguous landing counted %+v", rows)
		}
	}

	// No identity, and exactly one pull request: that bench. Not a guess
	// across a label that opened two.
	rows = countBenchDay(t, day, `
label=only bench=hulk event=pullreq at=2026-09-22T16:00:00Z pr=700
label=only event=landed at=2026-09-22T17:00:00Z
`)
	hulk := benchRow(t, rows, "hulk")
	if hulk.Landed != 1 || hulk.Useful != 1 || len(rows) != 1 {
		t.Fatalf("sole pullreq = %+v, want hulk landed=1 useful=1", rows)
	}
}

// TestOneCardIsUsefulOnceAcrossBenches: verified on one bench and receipted
// on another is one card. The count does not follow input order. A landing
// keeps the count, so the other bench's defect does not add a second one
// and does not take it off the landing.
func TestOneCardIsUsefulOnceAcrossBenches(t *testing.T) {
	day := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	orders := []string{
		`
label=once bench=studio event=verified-defect at=2026-09-22T18:00:00Z
label=once bench=space event=receipted-issue at=2026-09-22T18:30:00Z
`,
		`
label=once bench=space event=receipted-issue at=2026-09-22T18:30:00Z
label=once bench=studio event=verified-defect at=2026-09-22T18:00:00Z
`,
	}
	for _, text := range orders {
		rows := countBenchDay(t, day, text)
		space := benchRow(t, rows, "space")
		if len(rows) != 1 || space.Useful != 1 || space.Landed != 0 || space.OK != 0 {
			t.Fatalf("rows = %+v, want space useful=1 and no second bench (one card once)", rows)
		}
	}
	rows := countBenchDay(t, day, `
label=once bench=space event=verified-defect at=2026-09-22T18:00:00Z
label=once bench=studio event=landed at=2026-09-22T18:30:00Z pr=9
`)
	studio := benchRow(t, rows, "studio")
	if len(rows) != 1 || studio.Landed != 1 || studio.Useful != 1 {
		t.Fatalf("rows = %+v, want the landing's bench useful once, not space as well", rows)
	}
}

func countBenchDay(t *testing.T, day time.Time, text string) []BenchUse {
	t.Helper()
	entries, err := ParseCardsDone(text)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	return CountBenchUse(entries, day)
}
