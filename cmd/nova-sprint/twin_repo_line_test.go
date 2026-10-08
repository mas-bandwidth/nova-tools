package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoFirstBrief is a card brief that carries the store headers first, as a coordinator's
// own card does when it names no RESULT line: the REPO line is line 1, and the PATHS name a
// TLA+ model, the one card add tiers frontier (docs/SPEC-SPRINT.md, the card decides its
// model; sprint.ModelTier).
const repoFirstBrief = "REPO: mas-bandwidth/nova-tools\n" +
	"BASE: sprint/mechanical-2026-10-02\n" +
	"PATHS: tla/Lease.tla,internal/x/*.go\n" +
	"TEST: ./internal/x TestX\n" +
	"\nTHE TASK. Fix the lease model."

// repoLineOf is the one REPO: line of a brief, as it reads.
func repoLineOf(t *testing.T, brief string) string {
	t.Helper()
	for _, l := range strings.Split(brief, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "REPO:") {
			return strings.TrimSpace(l)
		}
	}
	require.Fail(t, "the brief names no REPO line", brief)
	return ""
}

// A twin keeps its parent's REPO line (docs/SPEC-SPRINT.md, the card decides its model; a
// friend's staging refuses a REPO that is no owner/name, internal/friend/stage.go): the
// tier a twin is pinned to is stamped on the card's own line, never appended to the REPO:
// header, so the REPO line the parent carried reads exactly as its parent's did.
func TestATwinKeepsItsParentsRepoLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("brief s1-1 --brief-file " + writeBrief(t, repoFirstBrief))
	parent := repoLineOf(t, ta.primary("s1-1").F("brief"))
	require.Equal(t, "REPO: mas-bandwidth/nova-tools", parent)

	out := ta.ok("recut s1-1 --tier frontier")
	assert.Contains(t, out, "tier frontier", "the twin is pinned to the tier")
	twin := ta.primary("s1-1b")
	assert.Equal(t, parent, repoLineOf(t, twin.F("brief")), "the twin's REPO line is its parent's, unchanged:\n%s", twin.F("brief"))
	assert.Equal(t, "frontier", twin.F("tier"), "the pin lives on the card, not the REPO line")
}

// The admission lint refuses a REPO line that is not exactly owner/name, naming the card and
// the value (docs/SPEC-SPRINT.md, the card decides its model): the tier word a writer appends
// to the line is read by no one, and the friend's staging would refuse the card for hours.
func TestTheAdmissionLintRefusesARepoLineThatIsNotOwnerName(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	bad := "RESULT: c sha=0123456789ab tier: pro\n" +
		"REPO: mas-bandwidth/nova-tools tier: frontier\n" +
		"BASE: sprint/mechanical-2026-10-02\n" +
		"PATHS: internal/x/*.go\n" +
		"TEST: internal/x TestY\n" +
		"\nTHE TASK. Fix x."
	code, out, errs := ta.do("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, bad))
	require.Equal(t, 2, code, "a REPO that is no owner/name: exit %d\n%s%s", code, out, errs)
	assert.Contains(t, errs, "check=repo-line", "the lint names the check:\n%s", errs)
	assert.Contains(t, errs, "mas-bandwidth/nova-tools tier: frontier", "the lint names the value it read:\n%s", errs)
	assert.False(t, ta.placed("c"), "nothing was written")
}

// recut and rework refuse a REPO line that is not owner/name before anything is written: a
// card that reached the table with one (brief holds no card checks) is sent back by name.
func TestRecutAndReworkRefuseARepoLineThatIsNotOwnerName(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	bad := "REPO: mas-bandwidth/nova-tools tier: frontier\n" +
		"BASE: sprint/mechanical-2026-10-02\n" +
		"PATHS: internal/x/*.go\n" +
		"TEST: ./internal/x TestX\n" +
		"\nTHE TASK. Fix x."
	ta.ok("brief s1-1 --brief-file " + writeBrief(t, bad))
	code, _, errs := ta.do("recut s1-1 --tier pro")
	assert.Equal(t, 1, code, "recut a bad REPO line: exit %d\n%s", code, errs)
	assert.Contains(t, errs, "s1-1", "the refusal names the card:\n%s", errs)
	assert.Contains(t, errs, "mas-bandwidth/nova-tools tier: frontier", "the refusal names the value:\n%s", errs)
	ta.deal(1)
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	code, _, errs = ta.do("rework s1-1 --fix again")
	assert.Equal(t, 1, code, "rework a bad REPO line: exit %d\n%s", code, errs)
	assert.Contains(t, errs, "s1-1", "the refusal names the card:\n%s", errs)
	assert.Contains(t, errs, "mas-bandwidth/nova-tools tier: frontier", "the refusal names the value:\n%s", errs)
}

// The admission lint refuses every REPO value that is no owner/name, not only one with a
// word after it: a bare word, "-", "none", a clone URL and a local path are all no
// repository the friend's staging can take, so add refuses each one before anything is
// written, naming the value it read (cardhdr.IsRepoValue, internal/friend/stage.go).
func TestTheAdmissionLintRefusesEveryRepoValueThatIsNotOwnerName(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{
		"garbage",
		"-",
		"none",
		"mas-bandwidth/nova-tools tier: frontier",
		"https://example.com/mas-bandwidth/nova-tools.git",
		"git@example.com:mas-bandwidth/nova-tools.git",
		"/Users/glenn/nova-tools",
		"mas-bandwidth/nova-tools/extra",
	} {
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		brief := "RESULT: c sha=0123456789ab tier: pro\n" +
			"REPO: " + repo + "\n" +
			"BASE: sprint/mechanical-2026-10-02\n" +
			"PATHS: internal/x/*.go\n" +
			"TEST: internal/x TestY\n" +
			"\nTHE TASK. Fix x."
		code, out, errs := ta.do("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, brief))
		require.Equal(t, 2, code, "REPO %q: exit %d\n%s%s", repo, code, out, errs)
		assert.Contains(t, errs, "check=repo-line", "REPO %q: the lint names the check:\n%s", repo, errs)
		assert.Contains(t, errs, repo, "REPO %q: the lint names the value it read:\n%s", repo, errs)
		assert.False(t, ta.placed("c"), "REPO %q: nothing was written", repo)
	}
}

// A model brief whose first line is a header carries no card's own line, so the tier writer
// leaves the header as written: the leading PATHS line is never rewritten with " tier:
// frontier" and the path list the card's readers read stays whole (brief_tier.go; the review
// of attempt 3). The card is admitted with the PATHS line it carried.
func TestAModelBriefWithAPathsFirstLineKeepsItsPaths(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	pathsFirst := "PATHS: tla/Lease.tla,internal/x/*.go\n" +
		"REPO: mas-bandwidth/nova-tools\n" +
		"BASE: sprint/mechanical-2026-10-02\n" +
		"TEST: internal/x TestY\n" +
		"\nTHE TASK. Fix the lease model."
	code, out, errs := ta.do("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, pathsFirst))
	require.Equal(t, 0, code, "a header-first model brief: exit %d\n%s%s", code, out, errs)
	c := ta.primary("s1-1")
	require.NotNil(t, c, "the card is admitted:\n%s%s", out, errs)
	assert.True(t, strings.HasPrefix(c.F("brief"), "PATHS: tla/Lease.tla,internal/x/*.go\n"),
		"the PATHS line is the one the brief carried:\n%s", c.F("brief"))
	assert.NotContains(t, c.F("brief"), "tier: frontier",
		"the tier writer did not append the tier to the PATHS line:\n%s", c.F("brief"))
}

// A REPO value that only looks like the card template is no repository. Only the template
// itself -- a brief whose line 1 is one of the template's own unfilled lines -- is left to
// add's unfilled-lines note; a brief whose line 1 is filled is a card, and the first REPO:
// line, the value the friend's staging reads, must be exactly owner/name. A template line
// later in the brief never exempts a different first value (card.Checks, unfilledRepo; the
// review of attempt 4).
func TestTheAdmissionLintRefusesATemplateLookingRepoValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, repo, value string }{
		{"the template's own fill-in", "<owner>/<name>", "<owner>/<name>"},
		{"a first value before a later template line", "garbage\nREPO: <owner>/<name>", "garbage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.ok("init --readers reader-a,reader-b --members m1")
			brief := "RESULT: c sha=0123456789ab tier: pro\n" +
				"REPO: " + tc.repo + "\n" +
				"BASE: sprint/mechanical-2026-10-02\n" +
				"\nTHE TASK. A free task with a repository."
			code, out, errs := ta.do("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, brief))
			require.Equal(t, 2, code, "REPO %q: exit %d\n%s%s", tc.value, code, out, errs)
			assert.Contains(t, errs, "check=repo-line", "REPO %q: the lint names the check:\n%s", tc.value, errs)
			assert.Contains(t, errs, tc.value, "REPO %q: the lint names the value it read:\n%s", tc.value, errs)
			assert.False(t, ta.placed("s1-1"), "REPO %q: nothing was written", tc.value)
		})
	}
}
