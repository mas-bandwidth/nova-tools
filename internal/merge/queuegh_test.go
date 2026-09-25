package merge

// EDGE 12, 2026-09-18: `nova-merge queue sweep` could never run against a real
// repository. QueuePRs, PoisonFailures, ChangedPackages and IssueFor existed only on
// FakeHost, so the two type assertions in the sweep missed on every real invocation and
// the verb answered `QUEUE REFUSED: ... no host, no sweep`. The whole verb passed its
// tests and had never once run.
//
// These tests hold the shape of the fix: the production host satisfies the same two
// seams the fake does, and the decode of what gh answers is a function a test can drive
// with no gh, no subprocess and no network.

import (
	"testing"
	"time"
)

// timeParseStamp is the one stamp this package parses, so a test can say that what the
// forge answered is in it.
func timeParseStamp(s string) (time.Time, error) { return time.Parse(Stamp, s) }

// The two seams, spelled as the sweep spells them in cmd/nova-merge/queue.go. A method
// renamed or dropped on *GH is a compile failure here rather than a refusal at 3 a.m.
type queueLister interface{ QueuePRs() ([]PR, error) }

type poisonReader interface {
	PoisonFailures(pr int) []Failure
	ChangedPackages(pr int) []string
	IssueFor(pr int) string
}

func TestTheProductionHostAnswersTheSweepsSeams(t *testing.T) {
	t.Parallel()
	var h interface{} = NewGH("o/n", 0, nil)
	if _, ok := h.(queueLister); !ok {
		t.Error("*GH does not answer QueuePRs, so `queue sweep` refuses on every real repository: no host, no sweep")
	}
	if _, ok := h.(poisonReader); !ok {
		t.Error("*GH does not answer the poison detector's three reads, so a real sweep parks nothing")
	}
	// And the FAKE answers them too, which is what kept the gap invisible -- both must,
	// for the sweep to be the same verb in a test and on a bench.
	var f interface{} = NewFakeHost()
	if _, ok := f.(queueLister); !ok {
		t.Error("FakeHost does not answer QueuePRs")
	}
	if _, ok := f.(poisonReader); !ok {
		t.Error("FakeHost does not answer the poison detector's three reads")
	}
}

// The arrival point of the forge's open list: its JSON becomes the rows the sweep walks.
func TestDecodeQueuePRsReadsTheFieldsTheSweepJudges(t *testing.T) {
	t.Parallel()
	out := `[
	  {"number":11,"author":{"login":"pat"},"baseRefName":"dev","headRefName":"rowan/a",
	   "headRepositoryOwner":{"login":"o"},"headRefOid":"aaaa","mergeable":"MERGEABLE",
	   "isDraft":false,"url":"https://example.invalid/11","title":"a","state":"OPEN",
	   "updatedAt":"2026-09-18T12:00:00Z"},
	  {"number":12,"author":{"login":"sam"},"baseRefName":"dev","headRefName":"rowan/b",
	   "headRepositoryOwner":{"login":"someone-else"},"headRefOid":"bbbb","mergeable":"CONFLICTING",
	   "isDraft":true,"url":"https://example.invalid/12","title":"b","state":"CLOSED",
	   "updatedAt":"2026-09-18T12:00:00.123Z"}
	]`
	prs, err := decodeQueuePRs(out, "o/n")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 {
		t.Fatalf("decoded %d rows, want 2", len(prs))
	}
	if prs[0].Number != 11 || prs[0].HeadOID != "aaaa" || prs[0].Mergeable != "MERGEABLE" {
		t.Errorf("row 0 = %+v", prs[0])
	}
	// The window is measured against UpdatedAt, so it must parse with the one stamp this
	// package parses. A fractional-second instant is trimmed to it rather than dropped.
	for _, pr := range prs {
		if _, err := timeParseStamp(pr.UpdatedAt); err != nil {
			t.Errorf("pull request %d's updated_at %q does not parse with merge.Stamp: %v", pr.Number, pr.UpdatedAt, err)
		}
	}
	if !prs[1].Closed {
		t.Error("a CLOSED pull request must come back closed; the sweep skips it")
	}
	if !prs[1].Fork {
		t.Error("a head on another owner's repository is a fork")
	}
	if prs[0].Fork {
		t.Error("a head on this repository is not a fork")
	}
}

// Lesson 48: a head branch becomes an argument to git and gh on a later verb, so it is
// refused WHERE IT ARRIVES and never handed on.
func TestDecodeQueuePRsRefusesAHeadBranchGitWouldReadAsAnOption(t *testing.T) {
	t.Parallel()
	out := `[{"number":11,"headRefName":"--upload-pack=touch /evil","headRefOid":"aaaa","state":"OPEN"}]`
	if _, err := decodeQueuePRs(out, "o/n"); err == nil {
		t.Fatal("a head branch git would read as an option was handed on")
	}
}

// The instant the window is measured against, in the one spelling this package parses.
func TestNormalizeStampTrimsToTheOneSpellingThisToolParses(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"2026-09-18T12:00:00Z":     "2026-09-18T12:00:00Z",
		"2026-09-18T12:00:00.5Z":   "2026-09-18T12:00:00Z",
		"  2026-09-18T12:00:00Z  ": "2026-09-18T12:00:00Z",
		"":                         "",
	} {
		if got := normalizeStamp(raw); got != want {
			t.Errorf("normalizeStamp(%q) = %q, want %q", raw, got, want)
		}
	}
}
