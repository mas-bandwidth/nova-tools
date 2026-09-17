package main

import (
	"path/filepath"
	"strings"
	"testing"
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

// A card at or over the ceiling is refused before it spends a token, and the finding says
// which check and how big.
func TestLintRefusesAnOversizeCard(t *testing.T) {
	body := lintGoodCard() + "RESULT: padding " + strings.Repeat("x", 12000) + "\n"
	card := writeLintCard(t, "big.card", body)
	exit, stdout, _ := runSwarm(t, "lint", "--card", card)
	if exit != 2 {
		t.Fatalf("an oversize card drifts at exit 2, got %d\nstdout: %s", exit, stdout)
	}
	if !strings.Contains(stdout, "LINT DRIFT card=big.card size:") {
		t.Fatalf("the size finding names the check: %q", stdout)
	}
}
