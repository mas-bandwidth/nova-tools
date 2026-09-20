package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// `nova-swarm lint --card` is the mechanical shape check that runs BEFORE any spend. A card
// defect is paid for in tokens, so the lint names it for free: the wrong clone step, a step
// out of order, no red test, no test command, no deadline, a file or package that is never
// named, a `../scratch` the wall refuses, a `nova-sandbox` probe, no final RESULT.md, an
// oversize card. These tests are red against the tree before the verb exists: `lint` is an
// unknown subcommand at exit 2 and prints nothing on stdout.

// lintGoodCard is a card that passes every check.
func lintGoodCard() string {
	return strings.Join([]string{
		"RESULT: CARD-0000 do the thing",
		"You are a Go engineer. Work in $(pwd).",
		"STEP 1. pwd && { [ -d repo ] || git clone -q https://github.com/mas-bandwidth/nova-tools.git repo; } && cd repo && git log --oneline -1",
		"STEP 2. Read docs/SPEC-SWARM.md first.",
		"STEP 3. Write a red test named TestCardLintPasses, run go test ./internal/swarm/, and record the failing output in <working directory>/scratch/red.txt using that absolute path.",
		"STEP 4. Run: export TMPDIR=\"$PWD/scratch\" && go test ./internal/swarm/ ./cmd/nova-swarm/",
		"STEP 5. finish within 20 minutes.",
		"STEP 6. Write RESULT.md: line 1 is the RESULT: line above.",
		"",
	}, "\n")
}

// writeLintCard writes one card under t.TempDir() and returns its path.
func writeLintCard(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	write(t, path, body)
	return path
}

func TestLintGoodCardPasses(t *testing.T) {
	card := writeLintCard(t, "good.card", lintGoodCard())
	exit, stdout, stderr := runSwarm(t, "lint", "--card", card)
	if exit != 0 {
		t.Fatalf("a good card lints clean at exit 0, got %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "LINT OK card=good.card checks=") {
		t.Fatalf("the OK line names the card and the checks: %q", stdout)
	}
	if lines := strings.Count(strings.TrimSuffix(stdout, "\n"), "\n") + 1; lines != 1 {
		t.Fatalf("LINT OK is one line, got %d:\n%s", lines, stdout)
	}
	if stderr != "" {
		t.Fatalf("a clean lint writes nothing to stderr: %q", stderr)
	}
}

// A card that writes its scratch above the job is the defect that killed cards tonight: the
// `../scratch` is refused by the wall, and the lint names the line before any spend.
func TestLintNamesTheParentPathLine(t *testing.T) {
	body := strings.Join([]string{
		"RESULT: CARD-1111 do the thing",
		"STEP 1. pwd && git clone -q https://github.com/mas-bandwidth/nova-tools.git repo && cd repo",
		"STEP 2. Write a red test named TestThing and record the failing output in ../scratch/red.txt.",
		"STEP 3. Run: go test ./internal/swarm/",
		"STEP 4. finish within 20 minutes.",
		"STEP 5. Write RESULT.md: line 1 is the RESULT: line above.",
		"",
	}, "\n")
	card := writeLintCard(t, "parent.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	if exit != 2 {
		t.Fatalf("a card with a ../ path drifts at exit 2, got %d\nstdout: %s", exit, stdout)
	}
	if !strings.Contains(stdout, "LINT DRIFT card=parent.card no-parent-path: 3:") {
		t.Fatalf("the no-parent-path finding names check, line and card: %q", stdout)
	}
	if !strings.Contains(stdout, "../scratch/red.txt") {
		t.Fatalf("the finding quotes the offending line: %q", stdout)
	}
}

// A card that names no red test is the defect the work order says to catch: the check name
// itself is the answer, so the reader does not have to find the missing piece.
func TestLintNamesTheMissingRedTest(t *testing.T) {
	body := strings.Join([]string{
		"RESULT: CARD-2222 change a constant",
		"STEP 1. pwd && git clone -q https://github.com/mas-bandwidth/nova-tools.git repo && cd repo",
		"STEP 2. Open internal/thing.go and change the constant 3 to 4.",
		"STEP 3. Run: go test ./internal/thing/",
		"STEP 4. finish within 10 minutes.",
		"STEP 5. Write RESULT.md: line 1 is the RESULT: line above.",
		"",
	}, "\n")
	card := writeLintCard(t, "nored.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	if exit != 2 {
		t.Fatalf("a card with no red test drifts at exit 2, got %d\nstdout: %s", exit, stdout)
	}
	if !strings.Contains(stdout, "LINT DRIFT card=nored.card red-test:") {
		t.Fatalf("the red-test finding names the check: %q", stdout)
	}
}

// A card that reaches for the sandbox is a probe this tool does not run, and its defect is
// named as plainly as the others.
func TestLintRefusesNovaSandbox(t *testing.T) {
	body := strings.Replace(lintGoodCard(), "STEP 2. Read docs/SPEC-SWARM.md first.",
		"STEP 2. nova-sandbox probe --secret /root/.ssh/id_rsa", 1)
	card := writeLintCard(t, "sandbox.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	if exit != 2 {
		t.Fatalf("a card invoking nova-sandbox drifts at exit 2, got %d\nstdout: %s", exit, stdout)
	}
	if !strings.Contains(stdout, "LINT DRIFT card=sandbox.card no-sandbox: 4:") {
		t.Fatalf("the no-sandbox finding names check and line: %q", stdout)
	}
}

// issue #1471: a new friend who follows the help (`nova-swarm template --name <t>` then
// `nova-swarm lint --card`) meets a result-first refusal before writing a word. Every card
// template the tool ships must lint clean, and a template that is not a card (result, worker,
// setup, capacity) must answer by name rather than as a result-first drift.
func TestTemplateThenLintPasses(t *testing.T) {
	cardTemplates := map[string]bool{
		"read-pr":   true,
		"probe-row": true,
		"fix-card":  true,
	}
	for _, name := range swarm.TemplateNames() {
		t.Run(name, func(t *testing.T) {
			exit, body, stderr := runSwarm(t, "template", "--name", name)
			if exit != 0 {
				t.Fatalf("template --name %s exited %d: %s", name, exit, stderr)
			}
			card := writeLintCard(t, name+".md", body)
			exit, stdout, _ := runSwarm(t, "lint", "--card", card)
			if cardTemplates[name] {
				if exit != 0 {
					t.Fatalf("the %s card template lints clean at exit 0, got %d\nstdout: %s", name, exit, stdout)
				}
				if !strings.Contains(stdout, "LINT OK") {
					t.Fatalf("the %s card template reports LINT OK: %q", name, stdout)
				}
				return
			}
			if strings.Contains(stdout, "result-first") {
				t.Fatalf("the %s template is not a card; lint says so by name, not as a result-first drift: %q", name, stdout)
			}
			if !strings.Contains(stdout, "not a card") || !strings.Contains(stdout, name) {
				t.Fatalf("the %s template gets a named not-a-card answer: %q", name, stdout)
			}
		})
	}
}

// A card over the ceiling is ADVISED and never refused (issues #1494, #1527). This test
// wanted exit 2 and a `LINT DRIFT` until 2026-09-19, which is the reading that made two
// managers trim good cards to reach a number while two others shipped over it on purpose.
// The ceiling is a reading budget, not an input limit -- a 12422-byte card was measured
// through the harness untruncated -- so the finding says how big, says `advisory`, and
// leaves the verdict alone.
func TestLintAdvisesAnOversizeCardAndDoesNotRefuseIt(t *testing.T) {
	body := lintGoodCard() + "RESULT: padding " + strings.Repeat("x", 12000) + "\n"
	card := writeLintCard(t, "big.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	if exit != 0 {
		t.Fatalf("an oversize card is advice, not a defect, so the lint exits 0, got %d\nstdout: %s", exit, stdout)
	}
	if !strings.Contains(stdout, "LINT NOTE card=big.card size:") {
		t.Fatalf("the size finding is a NOTE naming the check: %q", stdout)
	}
	if strings.Contains(stdout, "LINT DRIFT card=big.card size:") {
		t.Fatalf("the size finding is never a DRIFT, which is the line a caller refuses on: %q", stdout)
	}
	if !strings.Contains(stdout, "advisory") {
		t.Fatalf("the word a manager needs is in the line: advisory, not a limit: %q", stdout)
	}
	if !strings.Contains(stdout, "bytes=") || !strings.Contains(stdout, "cap=12000") {
		t.Fatalf("a clean card still carries its size and the cap: %q", stdout)
	}
}

// A card that is BOTH over the ceiling and drifting is refused for the drift alone, and the
// closing size line answers the advisory question in the bytes.
func TestAnOversizeDriftingCardIsRefusedForTheDriftAndSaysTheCeilingIsAdvisory(t *testing.T) {
	body := lintGoodCard() + "STEP 6. cd ../elsewhere\n" + "RESULT: padding " + strings.Repeat("x", 12000) + "\n"
	card := writeLintCard(t, "bigdrift.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	if exit != 2 {
		t.Fatalf("a card that walks above the job is refused, got %d\nstdout: %s", exit, stdout)
	}
	if !strings.Contains(stdout, "LINT DRIFT card=bigdrift.card no-parent-path:") {
		t.Fatalf("the drift it is refused for is named: %q", stdout)
	}
	if strings.Contains(stdout, "LINT DRIFT card=bigdrift.card size:") {
		t.Fatalf("the size is still never a DRIFT, even on a card that has one: %q", stdout)
	}
	if !strings.Contains(stdout, "LINT SIZE card=bigdrift.card bytes=") || !strings.Contains(stdout, "advisory=true") {
		t.Fatalf("the closing size line says advisory=true: %q", stdout)
	}
}
