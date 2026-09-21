package swarm

import "testing"

// Each case is one line of a card and what the rule must say about it. The `want` cases
// are the shape practice 25 exists for; the `no` cases are lines measured on real cards
// of the 2026-09-19 shift, every one of which drew a finding before this rule.

func lines(l ...string) []string { return l }

// A path the card asks the worker to WALK above the job is a finding, wherever it sits.
func TestAWalkedParentPathIsAFinding(t *testing.T) {
	for _, l := range []string{
		"cd ../scratch",
		"  cd ../..",
		"mkdir -p ../notes && cd ../notes",
		"cp RESULT.md ../RESULT.md",
		"rm -rf ../repo",
		"git -C ../other rev-parse HEAD",
		"nova-swarm gather --root ../queue",
		"go test ./... > ../out.txt",
		"pushd ../..",
		"mv scratch ../scratch",
		"run `cd ../..` to get out of the job", // a backtick span does not excuse a walk
	} {
		got := CardParentPaths(lines("RESULT: c sha=0", l))
		if len(got) != 1 || got[0] != 2 {
			t.Errorf("a walked parent path is a finding on its own line: %q -> %v", l, got)
		}
	}
}

// A `../` the card QUOTES -- a code span, a fenced block, a markdown link target, a
// `go test` ellipsis -- is not a path the worker walks, and is not a finding. Every line
// below is verbatim from a card cut on 2026-09-19.
func TestAQuotedParentPathIsNotAFinding(t *testing.T) {
	for _, l := range []string{
		"| the test package you add to | `internal/docs/`, package `docs`. See `internal/docs/agents_md_test.go` -- its paths to repo files are relative to the package directory, in the form `\"../../AGENTS.md\"` (that file, line 34). Follow that convention. |",
		"3. **`docs/SPEC-CI.md:515`, one link,** `[pit-stop ledger item 20](../reports/pitstop-tests-2026-09-17.md)`.",
		"  `SPEC-SWARM.md`, `WORKER-CARDS.md`, `SPEC-MERGE.md` or `PIT-STOP.md` gains a `../` prefix.",
		"drop the `../` prefix you added. Run the named test again. It must go RED naming that file, that",
		"and the coordinator saw it green after this exact correction by hand at 16:03Z (`ok .../internal/pulse 1.813s`).",
		"see [the ledger](../reports/pitstop.md) for the counts",
		"| the fix reverted | one `../` dropped again in 08-rate-and-convergence.md:41 | <paste> |",
	} {
		if got := CardParentPaths(lines("RESULT: c sha=0", l)); len(got) != 0 {
			t.Errorf("a quoted parent path is not a path the worker walks: %q -> %v", l, got)
		}
	}
}

// A fenced block is a quotation: the four lines of pasted `go test` output a card shows
// its worker are not four instructions to climb out of the job (issue #1494).
func TestAFencedQuotationIsNotAFinding(t *testing.T) {
	got := CardParentPaths(lines(
		"RESULT: c sha=0",
		"Its refusal, verbatim:",
		"```",
		"spec_ci_index_test.go:104: ../../docs/SPEC-CI.md: no entry for the class test",
		"serialize, _ := filepath.Abs(\"../../../../serialize\")",
		"```",
		"That is the shape to reproduce.",
	))
	if len(got) != 0 {
		t.Fatalf("a fenced quotation is not an instruction: %v", got)
	}
}

// ...but a walk inside a fence is still a walk: a card's commands live in fences, which is
// exactly where practice 25's hurt was written.
func TestAWalkInsideAFenceIsStillAFinding(t *testing.T) {
	got := CardParentPaths(lines(
		"RESULT: c sha=0",
		"```",
		"cd repo",
		"cd ../../elsewhere",
		"```",
	))
	if len(got) != 1 || got[0] != 4 {
		t.Fatalf("a `cd ..` inside a fenced command block is still the thing the wall refuses: %v", got)
	}
}

// An ellipsis is three dots, not a parent path. `ok .../internal/pulse 1.813s` is what
// `go test` prints and what a card pastes.
func TestAnEllipsisIsNotAParentPath(t *testing.T) {
	for _, l := range []string{
		"ok .../internal/pulse 1.813s",
		"run it from .../nova-tools and paste what it printed",
	} {
		if got := CardParentPaths(lines("RESULT: c sha=0", l)); len(got) != 0 {
			t.Errorf("an ellipsis is not a parent path: %q -> %v", l, got)
		}
	}
}

// Every finding is reported once per line, in line order, 1-based, so the DRIFT lines a
// manager reads are in the order the card reads.
func TestFindingsAreOneToALineInOrder(t *testing.T) {
	got := CardParentPaths(lines(
		"RESULT: c sha=0",
		"cd ../a ../b",
		"nothing here",
		"cd ../c",
	))
	if len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Fatalf("one finding per line, in line order: %v", got)
	}
}
