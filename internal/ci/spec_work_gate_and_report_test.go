package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecWorkCarriesTheDependencyGateAndTheHandReport pins that docs/SPEC-WORK.md
// states the dependency gate of nova-tools#785 and the hand report of nova-tools#854
// item 7 as ONE section whose two halves agree, ahead of the code, which is the order
// this repository works in: spec, then reads, then cards.
//
// It replaces the two pins the earlier drafts carried (#1563's launch gate and
// #1568's hand actions), which the coordinator's cold read held: the first hung every
// rule on a `launch` verb the document did not have, and the second promised a verb
// per hand action and spelled none. The decisions this test holds in place are the
// ones those reads asked for, so a later card cannot quietly move them:
//
//   - launch is NOT a verb of nova-work; the gate is the tree's answer at its own
//     admission verbs, and a launcher must ask;
//   - terminal accepted is verified, not recorded, and names no forge;
//   - the refusal is exit 1, by the document's own exit table;
//   - one report verb that changes no tree state, typed verbs for everything that does;
//   - a hand launch has one answer: refused when asked, recorded when told, and the
//     record is never an override.
//
// It does not check that any of this WORKS: no code implements it, and a test that
// pretended otherwise would be the manufactured green this repo refuses.
//
// Whitespace is normalised before matching, so a pinned phrase may wrap a source line
// the way the rest of the spec wraps at the margin without reddening this test.
func TestSpecWorkCarriesTheDependencyGateAndTheHandReport(t *testing.T) {
	t.Parallel()

	raw := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC-WORK.md"))
	spec := strings.Join(strings.Fields(raw), " ")

	if !strings.Contains(raw, "\n## The dependency gate and the hand report ") {
		t.Fatal("docs/SPEC-WORK.md does not carry the section `## The dependency gate and the hand report`; #785 and #854 item 7 are specified there, together, ahead of their code")
	}

	for _, want := range []string{
		// (a) what launch is, and whose the gate is.
		"Launch is not a verb of this grammar and does not become one",
		"The gate is the tree's answer at its own admission verbs, and a launcher must ask",
		// (b) every path goes through the one gate, at the exit the table gives it.
		"Every admission verb refuses a node that is not needs-met, by one predicate, at exit 1",
		"The needs check is a precondition of the candidate gate and is no validator rule",
		"The order of refusals is fixed",
		"A later verb is caught by an instrument and not by a promise",
		"`goal update --progress`",
		"A `removed` need can occur",
		"A done need that is then `correct`ed is unmet",
		"What an attested-only need protects is said plainly",
		"unmet need <need-id> <reason>",
		// (d) verified, never merely recorded, and forge-neutral.
		"Terminal accepted is the need's own acceptance, verified.",
		"Recorded is not verified.",
		"Stale does not unmeet a need",
		"Nothing here names a forge, a branch or a job.",
		// (c) the reasons and the edge cases.
		"`need-open`", "`need-reverted`", "`need-unavailable`", "`need-closed-unaccepted`", "`need-unverified`",
		"superseded or removed need is never met and never becomes met.",
		"A need that is a container",
		"a node that needs itself being a cycle of length one",
		"An edge added under work is admitted and flagged",
		"every way past a need is a recorded act",
		"A container need has other recorded roads",
		"A revert reaches the gate as a recorded act and no other way",
		// needs-met is derived and needs-broken is a reading.
		"Needs-met is read, never stored, and no event releases a dependent.",
		"It is a count and never a `WORK FAIL <id>` line",
		// (g) the hand report.
		"A hand act is told apart by where its effect lives",
		"`report` records a hand act and changes nothing.",
		"nova-work report --session <path> <write flags> --act <launched|stopped|other>",
		"`:act`, `:subject`, `:what`, `:acted-at`, `:instead-of`, `:reason`",
		"No field restates the envelope",
		"It changes no tree state",
		// (h) one answer, stated once.
		"Asked, it is refused",
		"told afterwards, it is recorded and never refused",
		"The record is evidence of a breach and never an override",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-WORK.md no longer carries the #785/#854 rule %q; the gate and the hand report are specified ahead of their code and a card is cut from this text", want)
		}
	}

	// Every rule is ahead of its code and says so on its first line, by the
	// document's own SPEC-AHEAD convention: six for #785, five for #854.
	if got := strings.Count(raw, "SPEC-AHEAD: #785\n"); got != 6 {
		t.Errorf("docs/SPEC-WORK.md has %d rule first-lines `SPEC-AHEAD: #785`, want 6 (the gate's rules 1 to 6)", got)
	}
	if got := strings.Count(raw, "SPEC-AHEAD: #854\n"); got != 5 {
		t.Errorf("docs/SPEC-WORK.md has %d rule first-lines `SPEC-AHEAD: #854`, want 5 (the hand report's rules 7 to 11)", got)
	}

	// The replay names ARE the acceptance list a card reads, and each is named
	// twice: by its rule, and again in *Acceptance replays* where its test is
	// written out.
	for _, replay := range []string{
		"a-done-need-on-unverified-evidence-admits-nothing",
		"stale-evidence-does-not-unmeet-a-need",
		"every-unmet-need-has-one-reason",
		"a-container-need-is-met-with-its-members",
		"every-admission-verb-refuses-an-unmet-need",
		"ready-true-implies-needs-met-and-not-the-reverse",
		"needs-met-is-read-not-released",
		"reverting-a-need-flags-an-engaged-dependent-and-kills-nothing",
		"an-edge-added-under-work-flags-and-every-way-past-a-need-is-recorded",
		"report-records-and-changes-nothing",
		"report-refuses-by-name",
		"a-hand-launch-is-refused-when-asked-and-recorded-when-told",
		"a-report-of-a-stop-releases-nothing",
		"reports-are-read-in-one-ask",
		"a-reopened-need-under-a-doing-dependent-leaves-the-set-green",
		"a-verb-that-can-admit-declares-its-needs-gate",
		"a-removed-or-corrected-need-is-unmet",
		"an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less",
	} {
		if got := strings.Count(spec, "`"+replay+"`"); got < 2 {
			t.Errorf("docs/SPEC-WORK.md names the replay %q %d time(s), want it named by its rule and written out in *Acceptance replays*", replay, got)
		}
	}

	// What the cold reads held must stay out. `held-by=` is SPEC-SWARM's slot-lock
	// holder and no line of this amendment may print it as a field of its own; a gate
	// refusal is exit 1 and never the exit 2 the first draft gave it; and the `:deps`
	// paragraph must point at the section rather than still listing the gate among
	// the unwritten rest of #785.
	for _, banned := range []string{
		"carries `held-by=<need-id>`",
		"is refused, exit 2, naming the need",
		"rule 10: unmet need",
		"Disposition `removed` cannot occur",
		"there is no flag for it",
		"no held-in-tree launch gate",
		"a finding filed the same hour",
		"tell a considered override from a habit",
	} {
		if strings.Contains(spec, banned) {
			t.Errorf("docs/SPEC-WORK.md carries %q, a sentence of the held drafts (#1563, #1568) that the merged amendment replaced", banned)
		}
	}
}
