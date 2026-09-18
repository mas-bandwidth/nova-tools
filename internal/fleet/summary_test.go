package fleet

// The certification column, read from the record and from nothing else.

import (
	"testing"
	"time"
)

func TestSummarizeCountsWhatTheRecordSays(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	certs := []Certificate{
		{Machine: "air", Class: "go-test", Verdict: VerdictOK, Build: "v0.17.0", Hash: "h1", At: at(5 * time.Minute)},
		{Machine: "air", Class: "diag-size", Verdict: VerdictWarn, Build: "v0.17.0", Hash: "h1", At: at(6 * time.Minute)},
		{Machine: "air", Class: "sbcl", Verdict: VerdictFail, Build: "v0.17.0", Hash: "h1", At: at(7 * time.Minute)},
		// Written before the adopt: the pair moved, so it is not current although it is well
		// inside the age. The pair is the machine's OWN newest, because the fleet does not
		// take a release at one minute and two machines mid-release disagree on purpose.
		{Machine: "air", Class: "c-build", Verdict: VerdictOK, Build: "v0.16.0", Hash: "h1", At: at(3 * time.Hour)},
		// Current pair, aged out on its own.
		{Machine: "air", Class: "git-push", Verdict: VerdictOK, Build: "v0.17.0", Hash: "h1", At: at(30 * time.Hour)},
		{Machine: "hulk", Class: "go-test", Verdict: VerdictOK, Build: "v0.17.0", Hash: "h1", At: at(time.Minute)},
	}
	got := Summarize(certs, "air", now, DefaultMaxAge)
	// WARN is a note on a pass and counts as certified, exactly as Certified counts it.
	if got.Certified != 2 || got.Total != 5 || got.Stale != 3 {
		t.Errorf("Summarize = certified=%d/%d stale=%d, want 2/5 stale=3", got.Certified, got.Total, got.Stale)
	}
	if got.Build != "v0.17.0" {
		t.Errorf("the pair is read off the machine's own newest row, got build %q", got.Build)
	}
	if got.Column() != "certified=2/5 stale=3" {
		t.Errorf("Column() = %q", got.Column())
	}
	// A machine with no row at all is a dash, never a zero: "nobody ever certified it here"
	// is not the claim "nothing about it is certified".
	none := Summarize(certs, "vision", now, DefaultMaxAge)
	if none.Known || none.Column() != "-" {
		t.Errorf("a machine with no row = %+v, column %q", none, none.Column())
	}
	// The newest row per class wins, so a machine that failed and was repaired is current.
	repaired := append(certs, Certificate{Machine: "air", Class: "sbcl", Verdict: VerdictOK, Build: "v0.17.0", Hash: "h1", At: now})
	if g := Summarize(repaired, "air", now, DefaultMaxAge); g.Certified != 3 {
		t.Errorf("a repaired class did not become current: %+v", g)
	}
}
