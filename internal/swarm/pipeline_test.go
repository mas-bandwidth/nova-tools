package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-SWARM, issue #856. A card whose STEPs exceed the pipeline bound of three
// model calls and that does not declare `MODE: explore` is refused with the
// remedy line, at admission, before any worker starts. The remedy names the
// keyword because a refusal a caller cannot act on is a refusal wasted.
func TestAdmissionRefusesAFourthCallWithoutExplore(t *testing.T) {
	four := "RESULT: fix the rule\nSTEP 1 read the test\nSTEP 2 write the red test\nSTEP 3 fix the source\nSTEP 4 write the result\n"
	why := admitWhyOf(t, t.TempDir(), "p4", "opencode/deepseek-v4-flash", four)
	if why == "" {
		t.Fatal("a card naming a fourth model call without `MODE: explore` is refused, got none")
	}
	if !strings.Contains(why, "MODE: explore") {
		t.Fatalf("the remedy names the keyword, got %q", why)
	}
	if !strings.Contains(why, "a card is a pipeline") {
		t.Fatalf("the remedy names the rule, got %q", why)
	}
	// The same card with `MODE: explore` is admitted: the loop is the exception,
	// and the exception is a word the card says.
	explore := "MODE: explore\n" + four
	if why := admitWhyOf(t, t.TempDir(), "p5", "opencode/deepseek-v4-flash", explore); why != "" {
		t.Fatalf("an explore card is admitted, got %q", why)
	}
}

// A card that stops at the pipeline bound needs no mode word: three calls are
// the shape, not the exception.
func TestTheFixCardRunsInThreeModelCalls(t *testing.T) {
	three := "RESULT: fix the rule\nSTEP 1 write the red test\nSTEP 2 write the fix\nSTEP 3 write the result\n"
	if why := admitWhyOf(t, t.TempDir(), "p3", "opencode/deepseek-v4-flash", three); why != "" {
		t.Fatalf("a three-step card is admitted, got %q", why)
	}
	if n := highestModelStep(three); n != 3 {
		t.Fatalf("a three-step card's highest call is 3, got %d", n)
	}
}

// The explore card carries a turn budget the harness enforces, and the budget is
// read from the card so the stop and the report can both name it.
func TestExploreOverTurnBudgetIsStoppedWithTheBudgetNamed(t *testing.T) {
	card := "MODE: explore\nTURNS: 4\nRESULT: find where the rule lives\nSTEP 1 grep\n"
	n, ok := exploreTurnBudget(card)
	if !ok || n != 4 {
		t.Fatalf("an explore card's turn budget is read from the card, got %d,%v", n, ok)
	}
	if _, ok := exploreTurnBudget("RESULT: fix\nSTEP 1 go\n"); ok {
		t.Fatal("a card with no `MODE: explore` has no turn budget")
	}
}

// THE CARD IS A PIPELINE (issue #856). These are the grammar's own tests: what a STEP is,
// which steps the harness runs itself and which cost one model call, what artifact a model
// step must answer with, and what a step is allowed to name as its input. Glenn, 2026-09-16:
// "The idea is for it to have no memory between calls. The idea is to just do work."

// fixCard is the shape of a real fix card (queue/done/card-8043.md, trimmed): a contract
// line, a role line, and STEPs that alternate between the harness's own shell and the model.
const fixCard = "RESULT: CARD-1 nova-tools #694 fixed with its red test first\n" +
	"You are a Go engineer. Work only inside your working directory. MODE: pipeline\n" +
	"STEP 1. mkdir -p scratch && { [ -d repo ] || git clone -q https://example.invalid/x repo; } && cd repo\n" +
	"STEP 2. Write the red test in `internal/merge/merge_test.go`: an injected clock, not the weather.\n" +
	"  The assertion is on the printed refusal line and the cleaned marker.\n" +
	"STEP T. go test ./internal/merge/ 2>&1 | tail -3\n" +
	"STEP 3. Implement the smallest fix in `internal/merge/merge.go` that turns the failing lines green.\n" +
	"STEP C. git add -A && git commit -q -m \"fix #694\"\n" +
	"STEP 4. Write RESULT.md: line 1 the RESULT line above, then red:, green:, one unsure: line.\n"

func TestParseCardSplitsShellStepsFromModelSteps(t *testing.T) {
	card, err := ParsePipelineCard([]byte(fixCard))
	if err != nil {
		t.Fatalf("the card did not parse: %v", err)
	}
	if got, want := card.Contract, "RESULT: CARD-1 nova-tools #694 fixed with its red test first"; got != want {
		t.Errorf("contract line is %q, want %q", got, want)
	}
	if got, want := len(card.Steps), 6; got != want {
		t.Fatalf("the card has %d steps, want %d", got, want)
	}
	wantKind := []StepKind{StepShell, StepModel, StepShell, StepModel, StepShell, StepModel}
	for i, step := range card.Steps {
		if step.Kind != wantKind[i] {
			t.Errorf("step %d (%q) is %v, want %v: %q", i+1, step.Label, step.Kind, wantKind[i], firstLine(step.Text))
		}
	}
	if n := card.ModelSteps(); n != 3 {
		t.Errorf("the fix card costs %d model calls, want exactly 3", n)
	}
	// The step's own continuation lines belong to the step, not to the next one.
	if !strings.Contains(card.Steps[1].Text, "cleaned marker") {
		t.Errorf("step 2 dropped its continuation line: %q", card.Steps[1].Text)
	}
}

func TestParseCardReadsTheNamedInputsAndTheDemandedArtifact(t *testing.T) {
	card, err := ParsePipelineCard([]byte(fixCard))
	if err != nil {
		t.Fatal(err)
	}
	if got := card.Steps[1].Inputs; len(got) != 1 || got[0] != "internal/merge/merge_test.go" {
		t.Errorf("step 2 names inputs %v, want the one backticked path", got)
	}
	if card.Steps[1].Artifact != ArtifactDiff {
		t.Errorf("step 2 demands %v, want a diff", card.Steps[1].Artifact)
	}
	if card.Steps[5].Artifact != ArtifactResult {
		t.Errorf("the RESULT.md step demands %v, want the result text", card.Steps[5].Artifact)
	}
}

func TestCardModeReadsTheContractLinesOnly(t *testing.T) {
	if got := CardMode([]byte(fixCard)); got != ModePipeline {
		t.Errorf("MODE on line 2 read as %q, want %q", got, ModePipeline)
	}
	explore := "RESULT: CARD-2 a read\nMODE: explore\nSTEP 1. find where the rule lives\n"
	if got := CardMode([]byte(explore)); got != ModeExplore {
		t.Errorf("MODE on line 2 read as %q, want %q", got, ModeExplore)
	}
	// A card that merely QUOTES the word deep in its body is not a mode: the mode is read
	// from the contract lines, the same three lines practice 17's word check reads.
	buried := "RESULT: CARD-3\nYou are a reader.\nSTEP 1. ls\nSTEP 2. the issue says MODE: explore verbatim\n"
	if got := CardMode([]byte(buried)); got != "" {
		t.Errorf("a MODE quoted in the body read as %q, want no mode", got)
	}
}

func TestDiffPathsRefusesAPathOutsideTheRepo(t *testing.T) {
	inside := "diff --git a/internal/x.go b/internal/x.go\n--- a/internal/x.go\n+++ b/internal/x.go\n@@ -1 +1 @@\n-a\n+b\n"
	if bad, ok := DiffOutside([]byte(inside)); ok {
		t.Errorf("a diff inside the repo was refused, naming %q", bad)
	}
	for _, escape := range []string{
		"diff --git a/../../etc/passwd b/../../etc/passwd\n--- a/../../etc/passwd\n+++ b/../../etc/passwd\n",
		"--- a/x\n+++ /etc/shadow\n",
		"diff --git a/x b/x\nrename from x\nrename to ../y\n",
	} {
		bad, ok := DiffOutside([]byte(escape))
		if !ok {
			t.Errorf("a diff leaving the repo was admitted:\n%s", escape)
			continue
		}
		if bad == "" {
			t.Errorf("the refusal named no path:\n%s", escape)
		}
	}
	// /dev/null is the one absolute path a unified diff carries by construction: it is the
	// other side of an added or a deleted file, never a path anything is written to.
	add := "diff --git a/n.go b/n.go\n--- /dev/null\n+++ b/n.go\n@@ -0,0 +1 @@\n+x\n"
	if bad, ok := DiffOutside([]byte(add)); ok {
		t.Errorf("an added file was refused, naming %q", bad)
	}
}

func TestHarnessTurnsCountsToolInvocationsAndNotProse(t *testing.T) {
	// The harness echoes one line per tool call, each beginning with its own sigil in
	// column 0 under the colour codes; the model's prose and the tools' own output are
	// indented or unmarked. This is the log the explore budget counts.
	log := "\x1b[0m\n> build · deepseek-v4-pro\n" +
		"\x1b[0m$ \x1b[0mmkdir -p scratch\n" +
		"total 168\n" +
		"          # a comment in a file the tool printed\n" +
		"\x1b[0m→ \x1b[0mRead internal/x.go\n" +
		"\x1b[0m$ \x1b[0mgo test ./...\n" +
		"ok  \tinternal/x\t0.2s\n"
	if got, want := HarnessTurns([]byte(log)), 3; got != want {
		t.Errorf("the log counted %d turns, want %d", got, want)
	}
	if got := HarnessTurns(nil); got != 0 {
		t.Errorf("an empty log counted %d turns, want 0", got)
	}
}

func TestHarnessTurnsInFileCountsWhatIsOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness-output.log")
	if err := os.WriteFile(path, []byte("\x1b[0m$ \x1b[0mls\n\x1b[0m$ \x1b[0mls\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := HarnessTurnsInFile(path); got != 2 {
		t.Errorf("the file counted %d turns, want 2", got)
	}
	if got := HarnessTurnsInFile(filepath.Join(dir, "absent.log")); got != 0 {
		t.Errorf("an absent log counted %d turns, want 0", got)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
