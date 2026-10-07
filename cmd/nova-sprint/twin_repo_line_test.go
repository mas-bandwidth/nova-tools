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
