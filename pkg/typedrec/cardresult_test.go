package typedrec_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// A card result is read in the card contract's shape (docs/SPEC-CARD-CONTRACT.md
// section 3): the six keys, a known verdict, a commit or `-` for the head.
func TestParseCardResultHoldsTheShape(t *testing.T) {
	t.Parallel()
	whole := "head: 0123456789abcdef0123456789abcdef01234567\nbranch: b\nverdict: ok\ngate: go test ./x\noutput: -\nreport: done it\ntitle: T\n\n## Body\n\nline one\nhead: not a key here\n"
	r := typedrec.ParseCardResult([]byte(whole))
	assert.True(t, r.Shaped)
	assert.Equal(t, "line one\nhead: not a key here", r.Body)
	assert.Equal(t, "T", r.Title)
	for name, text := range map[string]string{
		"no report and no body": strings.Replace(strings.Replace(whole, "report: done it\n", "", 1), "line one\nhead: not a key here\n", "", 1),
		"empty report":          strings.Replace(whole, "report: done it", "report:", 1),
		"no gate":               strings.Replace(whole, "gate: go test ./x\n", "", 1),
		"a bad head":            strings.Replace(whole, "0123456789abcdef0123456789abcdef01234567", "HEAD", 1),
		"a bad verdict":         strings.Replace(whole, "verdict: ok", "verdict: done", 1),
		"the old shape":         "rev: 0123456789abcdef0123456789abcdef01234567\nverdict: ok\n\n## One line\n\nx\n",
		"nothing":               "",
	} {
		assert.False(t, typedrec.ParseCardResult([]byte(text)).Shaped, name)
	}
	assert.True(t, typedrec.ParseCardResult([]byte(strings.Replace(whole, "0123456789abcdef0123456789abcdef01234567", "-", 1))).Shaped, "a not-done result may name no head")
	assert.True(t, typedrec.ParseCardResult([]byte(strings.Replace(whole, "verdict: ok", "verdict: nothing", 1))).Shaped, "nothing is a verdict")
}

func TestTheShaPredicates(t *testing.T) {
	t.Parallel()
	full := "0123456789abcdef0123456789abcdef01234567"
	assert.True(t, typedrec.IsSha("0123456"))
	assert.True(t, typedrec.IsSha(full))
	assert.False(t, typedrec.IsSha("012345"), "under seven digits")
	assert.False(t, typedrec.IsSha("ABCDEF0"), "upper case")
	assert.False(t, typedrec.IsSha("a-1.w1"), "a card id")
	assert.True(t, typedrec.IsFullSha(full))
	assert.False(t, typedrec.IsFullSha("0123456"))
}

// A worker's own RESULT.md that left out the report line (fixed-cpp-appends-and-deprecations-read-clean.w1.g9.e20,
// read from hetzner1 on 2026-10-13): every other key was there, the head was a real commit
// and the verdict was ok, and the body said what was done. The report line guards nothing, so the
// body's first prose line stands in for it; every key that does guard something is still required.
const reportlessSample = "head: 03e2ea0c4e2f1e43ab9c021efd2eed4c4d8bd6c9\n" +
	"branch: sprint/fixed-cpp-appends-and-deprecations-read-clean.w1.g9.e20\n" +
	"verdict: ok\n" +
	"gate: go test -count=1 -timeout 600s ./internal/codegen/cpptable/ ./internal/ci/\n" +
	"output: -\n\n## Body\n\n" +
	"The fixed-tables cell \"Renaming, appending and deprecating fields\" was red for cpp,\n" +
	"the reference leg, because its cpp/E8 and cpp/R19 assertions lived only inside\n" +
	"TestFixedVersioningNewReadsOld, which skips without build/fixedform-corpus.\n"

func TestAResultWithNoReportLineTakesItsReportFromTheBody(t *testing.T) {
	t.Parallel()
	r := typedrec.ParseCardResult([]byte(reportlessSample))
	assert.True(t, r.Shaped)
	assert.True(t, r.ReportDerived)
	assert.Equal(t, "The fixed-tables cell \"Renaming, appending and deprecating fields\" was red for cpp,", r.Report)
	assert.Equal(t, "03e2ea0c4e2f1e43ab9c021efd2eed4c4d8bd6c9", r.Head)
	assert.Equal(t, "ok", r.Verdict)
	assert.Empty(t, r.Fault)

	// a body that opens with a heading or a fence still gives its first prose line
	h := strings.Replace(reportlessSample, "## Body\n\n", "## Body\n\n## Summary\n\n```\n", 1)
	assert.Equal(t, "The fixed-tables cell \"Renaming, appending and deprecating fields\" was red for cpp,", typedrec.ParseCardResult([]byte(h)).Report)
	// a long first line is cut to the report bound
	long := strings.Replace(reportlessSample, "The fixed-tables cell", strings.Repeat("x", 400), 1)
	assert.Len(t, typedrec.ParseCardResult([]byte(long)).Report, 300)
}

// The derived report relaxes the report line and nothing else: each of these is still refused.
func TestAResultWithNoReportLineIsStillRefusedWhenAnythingElseFails(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no body to read a report from": reportlessSample[:strings.Index(reportlessSample, "\n\n## Body")],
		"a body of a heading only":      reportlessSample[:strings.Index(reportlessSample, "The fixed")] + "## Only a heading\n",
		"no verdict":                    strings.Replace(reportlessSample, "verdict: ok\n", "", 1),
		"an unknown verdict":            strings.Replace(reportlessSample, "verdict: ok", "verdict: done", 1),
		"no head":                       strings.Replace(reportlessSample, "head: 03e2ea0c4e2f1e43ab9c021efd2eed4c4d8bd6c9\n", "", 1),
		"a head that is no commit":      strings.Replace(reportlessSample, "03e2ea0c4e2f1e43ab9c021efd2eed4c4d8bd6c9", "HEAD", 1),
		"no gate":                       strings.Replace(reportlessSample, "gate: go test -count=1 -timeout 600s ./internal/codegen/cpptable/ ./internal/ci/\n", "", 1),
		"no output":                     strings.Replace(reportlessSample, "output: -\n", "", 1),
		"no branch":                     strings.Replace(reportlessSample, "branch: sprint/fixed-cpp-appends-and-deprecations-read-clean.w1.g9.e20\n", "", 1),
		"a report line that is empty":   strings.Replace(reportlessSample, "output: -\n", "output: -\nreport:\n", 1),
	}
	for name, text := range cases {
		r := typedrec.ParseCardResult([]byte(text))
		assert.False(t, r.Shaped, name)
		assert.False(t, r.ReportDerived, name)
	}
}

// A worker that ignored the shape (main-report-schema-new-leg-2.w4.g1.e20, read from hetzner2
// on 2026-10-13): a RESULT line, the word DONE and prose. It stays refused, its head named
// the staged commit and not the one it made; the fault says what to write instead.
const freeFormSample = "RESULT: main-report-schema-new-leg sha=121f606fb9d9 tier: pro\nDONE\n" +
	"Attempt 1 had already ported 52a83b545 onto BASE in BASE's shape, but left commit 56920e7fd half-ported.\n"

func TestAFreeFormResultIsRefusedAndTheFaultSaysWhy(t *testing.T) {
	t.Parallel()
	r := typedrec.ParseCardResult([]byte(freeFormSample))
	assert.False(t, r.Shaped)
	assert.Contains(t, r.Fault, "none of the six")
	assert.Contains(t, r.Fault, "`head: <full sha>` on line 1")
}

func TestAShapeFaultNamesWhatIsWrong(t *testing.T) {
	t.Parallel()
	whole := "head: 0123456789abcdef0123456789abcdef01234567\nbranch: b\nverdict: ok\ngate: g\noutput: -\nreport: r\n"
	for name, c := range map[string]struct{ text, want string }{
		"missing keys":     {strings.Replace(strings.Replace(whole, "gate: g\n", "", 1), "output: -\n", "", 1), "no gate:, output: line"},
		"a head no commit": {strings.Replace(whole, "0123456789abcdef0123456789abcdef01234567", "HEAD", 1), "head is `head`, not a commit sha"},
		"a bad verdict":    {strings.Replace(whole, "verdict: ok", "verdict: done", 1), "verdict is `done`, not one of ok, not-done, nothing, broken"},
		"an empty report":  {strings.Replace(whole, "report: r", "report:", 1), "report: is empty"},
	} {
		r := typedrec.ParseCardResult([]byte(c.text))
		assert.False(t, r.Shaped, name)
		assert.Contains(t, r.Fault, c.want, name)
	}
	assert.Empty(t, typedrec.ParseCardResult([]byte(whole)).Fault, "a shaped result has no fault")
	assert.Empty(t, typedrec.ParseCardResult(nil).Fault, "no file is no result, not a faulty one")
	assert.Empty(t, typedrec.ParseCardResult([]byte("  \n\n")).Fault, "a blank file is no result")
}

// The RESULT.md native writes for a card it ended itself (an idle reap; read from hetzner1,
// main-report-schema-slow-gate-2.w1.g13.e20) is native's word and not the worker's: it is
// not shaped and it carries no fault, so the failed finish does not blame a shape nobody wrote.
func TestNativesOwnResultCarriesNoShapeFault(t *testing.T) {
	t.Parallel()
	idle := "RESULT: BLOCKED main-report-schema-slow-gate-2.w1\nCARD IDLE task=main-report-schema-slow-gate-2.w1 step=- idle=300s\n" +
		"blocked: the card's log and its process tree were both still for 300s; the run ended it rather than holding the slot to its deadline\n" +
		"written-by: nova-swarm native (the card published no report of its own)\n"
	r := typedrec.ParseCardResult([]byte(idle))
	assert.False(t, r.Shaped)
	assert.Empty(t, r.Fault)
	assert.True(t, typedrec.NativeWritten([]byte(idle)))
	assert.False(t, typedrec.NativeWritten([]byte(freeFormSample)))
}
