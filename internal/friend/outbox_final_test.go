package friend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A report is final only in the shape a session writes at the end
// (docs/SPEC-FRIEND.md, the daemon reads every outbox job).
func TestFinal(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name, first, second, why string
		ok                       bool
	}{
		{name: "pending", first: "Verdict: pending", second: "Head: -", why: "Verdict: pending"},
		{name: "land", first: "Verdict: LAND", second: "Head: " + sha, ok: true},
		{name: "hold", first: "Verdict: HOLD", second: "Head: -", ok: true},
		{name: "fail", first: "verdict: fail", second: "Head: -", ok: true},
		{name: "empty", first: "", second: "Head: -"},
		// the brief tells her line 2 is blank for HOLD and FAIL (docs/FRIENDS.md),
		// so a blank second line is final for those, and Head: - stays final.
		{name: "hold blank", first: "Verdict: HOLD", second: "", ok: true},
		{name: "fail blank", first: "Verdict: FAIL", second: "", ok: true},
		// the reading stays lenient (docs/FRIENDS.md): markdown marks around the
		// verdict and the head are trimmed, as friend sync reads them.
		{name: "markdown verdict", first: "**Verdict:** LAND", second: "Head: " + sha, ok: true},
		{name: "markdown head", first: "Verdict: LAND", second: "**Head:** " + sha, ok: true},
		// a parsed verdict does not bypass Final: a LAND whose second line is not
		// a Head (blank included), and a verdict that is not on the first line,
		// are not final.
		{name: "land blank", first: "Verdict: LAND", second: ""},
		{name: "bad head", first: "Verdict: LAND", second: "Head: pending", why: "Head: pending"},
		{name: "later line", first: "the notes first", second: "Head: " + sha, why: "the notes first"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, why := Final(tc.first, tc.second)
			assert.Equal(t, tc.ok, ok, "%q / %q", tc.first, tc.second)
			if tc.why != "" {
				assert.Equal(t, tc.why, why, "why names the offending line of %q / %q", tc.first, tc.second)
			} else if !tc.ok {
				assert.Empty(t, why)
			}
		})
	}
}

// A HOLD or FAIL whose second line is blank, as the brief tells her to write it
// (docs/FRIENDS.md), is final and finished --failed on the first pass with its
// verdict kept, never left to the deadline and rewritten as FAIL "report never
// became final" (docs/SPEC-FRIEND.md, the daemon reads every outbox job).
func TestAHoldOrFailWithABlankSecondLineIsFinishedNotLeft(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &finishes{}
	r.d.Finish = f.finish
	hold := workCard("holdblank.w1", "working")
	hold.Branch = "sprint/holdblank.w1.g1.e15"
	fail := workCard("failblank.w1", "working")
	fail.Branch = "sprint/failblank.w1.g1.e15"
	r.d.heldCards = []HeldCard{hold, fail}
	outboxReport(t, r.d.Dir, hold.Job, "Verdict: HOLD\n\nneeds the coordinator\n")
	outboxReport(t, r.d.Dir, fail.Job, "Verdict: FAIL\n\nno bench\n")
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}

	l.outboxStep(t0)

	got := map[string][]string{}
	for _, argv := range f.got() {
		got[argv[3]] = argv
	}
	require.Len(t, got, 2, "a HOLD and a FAIL with a blank second line are final on the first pass: %v", f.got())
	assert.Equal(t, []string{
		"finish", "--as", "friend.bob", "holdblank.w1@1", "--epoch", "15", "--failed",
		"--branch", "sprint/holdblank.w1.g1.e15", "--report",
		"friend bob HOLD: Verdict: HOLD needs the coordinator",
	}, got["holdblank.w1@1"])
	assert.Equal(t, []string{
		"finish", "--as", "friend.bob", "failblank.w1@1", "--epoch", "15", "--failed",
		"--branch", "sprint/failblank.w1.g1.e15", "--report",
		"friend bob FAIL: Verdict: FAIL no bench",
	}, got["failblank.w1@1"])
	assert.NotContains(t, r.recordText(), "report never became final")
}

// A report whose verdict is not LAND, HOLD or FAIL is left, and past the card's
// deadline is collected once as FAIL. The outbox is the test's temp directory.
func TestAReportThatIsNotFinalIsLeftUntilTheCardsDeadline(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &finishes{}
	r.d.Finish = f.finish
	const sha = "0123456789abcdef0123456789abcdef01234567"
	pending := workCard("pending.w1", "working")
	pending.Branch = "sprint/pending.w1.g1.e15"
	deadline := t0.Add(time.Minute)
	pending.Brief += "DEADLINE: " + deadline.Format(time.RFC3339) + "\n"
	land := workCard("landed.w1", "working")
	land.Branch = "sprint/landed.w1.g1.e15"
	r.d.heldCards = []HeldCard{pending, land}
	const pendingReport = "Verdict: pending\n"
	outboxReport(t, r.d.Dir, pending.Job, pendingReport)
	outboxReport(t, r.d.Dir, land.Job, "Verdict: LAND\nHead: "+sha+"\n\nDone.\n")
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}

	l.outboxStep(t0)
	l.outboxStep(t0.Add(time.Second))

	require.Len(t, f.got(), 1, "a pending report is not finished before the deadline: %v", f.got())
	assert.Equal(t, "landed.w1@1", f.got()[0][3])
	assert.Contains(t, r.recordText(), "report not final yet: pending.w1: Verdict: pending")
	assert.Equal(t, 1, strings.Count(r.recordText(), "report not final yet: pending.w1:"), "noted once")

	l.outboxStep(deadline)

	require.Len(t, f.got(), 2, "past the deadline the report is collected as FAIL: %v", f.got())
	assert.Equal(t, []string{
		"finish", "--as", "friend.bob", "pending.w1@1", "--epoch", "15", "--failed",
		"--branch", "sprint/pending.w1.g1.e15", "--report",
		"friend bob FAIL: report never became final: first line Verdict: pending",
	}, f.got()[1])
	sum := sha256.Sum256([]byte(pendingReport))
	assert.Contains(t, r.recordText(), "sha256="+hex.EncodeToString(sum[:]))
	raw, err := os.ReadFile(filepath.Join(r.d.Dir, "outbox", pending.Job, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, pendingReport, string(raw), "the file is left as written")

	outboxReport(t, r.d.Dir, pending.Job, "Verdict: LAND\nHead: "+sha+"\n\nlate.\n")
	l.outboxStep(deadline.Add(time.Minute))
	assert.Len(t, f.got(), 2, "a report that changes after collection is not collected again")
}

// A report with no Verdict line is pending too: the first line, even when
// empty, is named by the same once-only note as every other non-final report.
func TestAMissingVerdictUsesTheNotFinalNote(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &finishes{}
	r.d.Finish = f.finish
	empty := workCard("empty.w1", "working")
	missing := workCard("missing.w1", "working")
	r.d.heldCards = []HeldCard{empty, missing}
	outboxReport(t, r.d.Dir, empty.Job, "")
	outboxReport(t, r.d.Dir, missing.Job, "notes only\n")
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}

	l.outboxStep(t0)
	l.outboxStep(t0.Add(time.Second))

	assert.Empty(t, f.got(), "neither incomplete report is collected")
	assert.Equal(t, 1, strings.Count(r.recordText(), "report not final yet: empty.w1: "), "the empty first line is noted once")
	assert.Equal(t, 1, strings.Count(r.recordText(), "report not final yet: missing.w1: notes only"), "the missing verdict is noted once with its first line")
}

// A parsed LAND, HOLD or FAIL does not bypass Final: a verdict whose second line is
// not a Head, and a verdict that is not the first line, are not final, are left before
// the card's deadline, and are collected as FAIL past it
// (docs/SPEC-FRIEND.md, the daemon reads every outbox job).
func TestFinalGatesEveryReportAndTheDeadlineCollectsTheRest(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &finishes{}
	r.d.Finish = f.finish
	const sha = "0123456789abcdef0123456789abcdef01234567"
	deadline := t0.Add(time.Minute)
	badHead := workCard("badhead.w1", "working")
	badHead.Branch = "sprint/badhead.w1.g1.e15"
	badHead.Brief += "DEADLINE: " + deadline.Format(time.RFC3339) + "\n"
	laterLine := workCard("laterline.w1", "working")
	laterLine.Branch = "sprint/laterline.w1.g1.e15"
	laterLine.Brief += "DEADLINE: " + deadline.Format(time.RFC3339) + "\n"
	r.d.heldCards = []HeldCard{badHead, laterLine}
	outboxReport(t, r.d.Dir, badHead.Job, "Verdict: LAND\nHead: pending\n")
	outboxReport(t, r.d.Dir, laterLine.Job, "the notes first\nHead: "+sha+"\nVerdict: LAND\n")
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}

	l.outboxStep(t0)

	assert.Empty(t, f.got(), "a LAND whose second line is not a Head, and a verdict not on the first line, are not final: %v", f.got())
	assert.Contains(t, r.recordText(), "report not final yet: badhead.w1: Verdict: LAND")
	assert.Contains(t, r.recordText(), "report not final yet: laterline.w1: the notes first")

	l.outboxStep(deadline)

	require.Len(t, f.got(), 2, "past the deadline each is collected as FAIL: %v", f.got())
	assert.Equal(t, []string{
		"finish", "--as", "friend.bob", "badhead.w1@1", "--epoch", "15", "--failed",
		"--branch", "sprint/badhead.w1.g1.e15", "--report",
		"friend bob FAIL: report never became final: first line Verdict: LAND",
	}, f.got()[0])
	assert.Equal(t, []string{
		"finish", "--as", "friend.bob", "laterline.w1@1", "--epoch", "15", "--failed",
		"--branch", "sprint/laterline.w1.g1.e15", "--report",
		"friend bob FAIL: report never became final: first line the notes first",
	}, f.got()[1])
}

// The brief's deadline is read from the issued spellings: the key in any case (`Deadline:`
// is what an issued brief writes), and a value that ends in a period or carries the
// template's sentence after the duration (docs/SPEC-FRIEND.md, the daemon reads every
// outbox job).
func TestTheCardDeadlineIsReadFromTheIssuedBrief(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		brief  string
		within time.Duration
		abs    bool
		ok     bool
	}{
		{name: "mixed case, a period", brief: "Deadline: finish within 240 minutes.\n", within: 240 * time.Minute, ok: true},
		{name: "the template's sentence", brief: "Deadline: finish within 90 minutes; the judgment of a card that runs past it is the coordinator's.\n", within: 90 * time.Minute, ok: true},
		{name: "upper case, no period", brief: "DEADLINE: finish within 90 minutes\n", within: 90 * time.Minute, ok: true},
		{name: "a Go duration", brief: "Deadline: 2h\n", within: 2 * time.Hour, ok: true},
		{name: "an absolute time", brief: "Deadline: " + t0.Add(time.Hour).Format(time.RFC3339) + "\n", abs: true, ok: true},
		{name: "none", brief: "no deadline here\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			at, within, ok := cardDeadline(c.brief)
			assert.Equal(t, c.ok, ok, "%q", c.brief)
			assert.Equal(t, c.within, within, "%q", c.brief)
			if c.abs {
				assert.Equal(t, t0.Add(time.Hour), at, "%q", c.brief)
			} else {
				assert.True(t, at.IsZero(), "%q has no absolute time", c.brief)
			}
		})
	}
}

// A duration deadline runs from the card's start, not from the pass that first reads its
// report: a report written near the end of an issued card's 90 minutes is collected when
// the card's 90 minutes are up, not 90 minutes after the report appeared
// (docs/SPEC-FRIEND.md, the daemon reads every outbox job).
func TestACardDeadlineRunsFromTheCardsStart(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &finishes{}
	r.d.Finish = f.finish
	card := workCard("late.w1", "working")
	card.Branch = "sprint/late.w1.g1.e15"
	card.Brief += "Deadline: finish within 90 minutes.\n"
	r.d.heldCards = []HeldCard{card}
	l := &loop{d: r.d, ctx: context.Background(), lanes: &laneSet{}}

	l.outboxStep(t0) // the card is working with no report yet: its start is this pass
	assert.Empty(t, f.got(), "no report, nothing collected")

	outboxReport(t, r.d.Dir, card.Job, "Verdict: pending\n") // the report comes 80 minutes in
	l.outboxStep(t0.Add(80 * time.Minute))
	assert.Empty(t, f.got(), "80 of the card's 90 minutes: the report is left")
	assert.Contains(t, r.recordText(), "report not final yet: late.w1: Verdict: pending")

	l.outboxStep(t0.Add(89 * time.Minute))
	assert.Empty(t, f.got(), "one minute inside the card's deadline: still left")

	l.outboxStep(t0.Add(90 * time.Minute))
	require.Len(t, f.got(), 1, "at the card's deadline the report is collected as FAIL: %v", f.got())
	assert.Equal(t, []string{
		"finish", "--as", "friend.bob", "late.w1@1", "--epoch", "15", "--failed",
		"--branch", "sprint/late.w1.g1.e15", "--report",
		"friend bob FAIL: report never became final: first line Verdict: pending",
	}, f.got()[0])
}
