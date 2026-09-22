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
