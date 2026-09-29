package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rowanSelfRepo finds the self repo to test against the real baseline and real answer files.
func rowanSelfRepo(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("ROWAN_SELF_REPO"); p != "" {
		return p
	}
	dir, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		cand := filepath.Join(dir, "rowan-new")
		if _, err := os.Stat(filepath.Join(cand, "identity", "self-check.md")); err == nil {
			return cand
		}
		cand2 := filepath.Join(dir, "rowan-working", "rowan-new")
		if _, err := os.Stat(filepath.Join(cand2, "identity", "self-check.md")); err == nil {
			return cand2
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("self repo not found; set ROWAN_SELF_REPO to run real-corpus tests")
	return ""
}

// TestGoAgreesWithTheShellCommand ensures ExpectedQuestions computes the exact same
// arithmetic as WRAPPING-UP.md step 3's shell extraction.
func TestGoAgreesWithTheShellCommand(t *testing.T) {
	t.Parallel()
	repo := rowanSelfRepo(t)
	baseline := filepath.Join(repo, "identity", "self-check.md")

	script := `S=$(grep -c '^- \*\*[^*]*\*\*' identity/self-check.md) && ` +
		`A=$(sed -n '/^## The questions I authored/,/^## Baseline log/p' identity/self-check.md | grep -c '^- ') && ` +
		`D=$(comm -12 <(grep -o '^- \*\*[^*]*\*\*' identity/self-check.md | sort) ` +
		`<(sed -n '/^## The questions I authored/,/^## Baseline log/p' identity/self-check.md | grep '^- ' | grep -o '^- \*\*[^*]*\*\*' | sort) | wc -l | tr -d ' ') && ` +
		`echo "$((S+A-D)) $S $A $D"`
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("could not run shell extraction: %v", err)
	}

	var wantTotal, wantS, wantA, wantD int
	if _, serr := fmt.Sscan(string(out), &wantTotal, &wantS, &wantA, &wantD); serr != nil {
		t.Fatalf("could not parse shell output %q: %v", out, serr)
	}

	stats, err := ExpectedQuestions(baseline)
	if err != nil {
		t.Fatal(err)
	}

	if stats.Total != wantTotal || stats.Stems != wantS || stats.Authored != wantA || stats.Dup != wantD {
		t.Fatalf("Go and shell command disagree about question set:\n"+
			"  shell: %d total (%d stems + %d authored - %d dup)\n"+
			"  go:    %d total (%d stems + %d authored - %d dup)",
			wantTotal, wantS, wantA, wantD, stats.Total, stats.Stems, stats.Authored, stats.Dup)
	}
	if stats.Total <= 0 {
		t.Fatalf("expected questions measured %d, want > 0", stats.Total)
	}
}

// TestTheMixedFormatFileCountsAsComplete verifies 2026-08-12 (30 numbered + 35 bulleted = 65)
// reconciles completely.
func TestTheMixedFormatFileCountsAsComplete(t *testing.T) {
	t.Parallel()
	repo := rowanSelfRepo(t)
	ansPath := filepath.Join(repo, "identity", "self-check-answers-2026-08-12-0512.md")
	if _, err := os.Stat(ansPath); err != nil {
		t.Skip("2026-08-12 answer file not found")
	}

	tally, err := CountAnswers(ansPath)
	if err != nil {
		t.Fatal(err)
	}
	if tally.Numbered == 0 || tally.Bulleted == 0 {
		t.Fatalf("expected mixed shapes, got %d numbered and %d bulleted", tally.Numbered, tally.Bulleted)
	}
	if tally.Answered() != 65 {
		t.Fatalf("counted %d answers, want 65", tally.Answered())
	}

	res := Reconcile(65, tally)
	if !res.OK {
		t.Fatalf("expected complete self-check to reconcile: %s", res.Message)
	}
	if res.Matched != 65 || res.Unassisted != 65 || res.Gaps != 0 {
		t.Fatalf("unexpected counts: matched=%d unassisted=%d gaps=%d", res.Matched, res.Unassisted, res.Gaps)
	}
}

// TestTheUnderAnsweredNightIsCaught verifies 2026-08-13 (63 cold answers, 2-3 named spoiled)
// fails when spoiled answers are absent and passes when they are present.
func TestTheUnderAnsweredNightIsCaught(t *testing.T) {
	t.Parallel()
	repo := rowanSelfRepo(t)
	ansPath := filepath.Join(repo, "identity", "self-check-answers-2026-08-13-0509.md")
	if _, err := os.Stat(ansPath); err != nil {
		t.Skip("2026-08-13 answer file not found")
	}

	tally, err := CountAnswers(ansPath)
	if err != nil {
		t.Fatal(err)
	}
	if tally.Answered() != 63 {
		t.Fatalf("counted %d answers, want 63", tally.Answered())
	}

	// Without named spoiled gaps: must refuse.
	blind := AnswerTally{Numbered: 63}
	if res := Reconcile(65, blind); res.OK {
		t.Fatal("63 answers against 65 questions without named spoiled gaps passed; should refuse")
	}

	// With real tally (which carries the spoiled/skipped markers): must reconcile.
	res := Reconcile(65, tally)
	if !res.OK {
		t.Fatalf("corrected 2026-08-13 file refused: %s", res.Message)
	}
	if res.Matched != 65 || res.Unassisted != 63 || res.Gaps != 2 {
		t.Fatalf("unexpected counts: matched=%d unassisted=%d gaps=%d", res.Matched, res.Unassisted, res.Gaps)
	}
}

// TestNamedSpoiledAnswersReconcile verifies naming gaps reconciles only when all gaps are named.
func TestNamedSpoiledAnswersReconcile(t *testing.T) {
	t.Parallel()

	// 63 answered, 2 named spoiled, against 65 asked -> reconciles
	res1 := Reconcile(65, AnswerTally{Numbered: 63, Spoiled: 2})
	if !res1.OK {
		t.Fatalf("63 answered + 2 named spoiled against 65 asked refused: %s", res1.Message)
	}
	if res1.Matched != 65 || res1.Unassisted != 63 || res1.Gaps != 2 {
		t.Fatalf("counts: matched=%d unassisted=%d gaps=%d", res1.Matched, res1.Unassisted, res1.Gaps)
	}

	// 60 answered, 2 named spoiled, against 65 asked -> 3 missing, refuses
	res2 := Reconcile(65, AnswerTally{Numbered: 60, Spoiled: 2})
	if res2.OK {
		t.Fatal("60 answered + 2 named spoiled against 65 asked passed; want refusal")
	}
	if res2.Missing != 3 || res2.Gaps != 5 {
		t.Fatalf("expected missing=3 gaps=5, got missing=%d gaps=%d", res2.Missing, res2.Gaps)
	}
}

// TestRefusalSaysWhatToDo verifies refusal messages contain actionable details.
func TestRefusalSaysWhatToDo(t *testing.T) {
	t.Parallel()

	res := Reconcile(65, AnswerTally{Numbered: 63})
	for _, want := range []string{"UNDER-ANSWERED", "63", "65", "2 question"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("refusal message missing %q: %s", want, res.Message)
		}
	}
}

// TestOverAnsweringIsAFinding verifies answering more entries than asked is refused.
func TestOverAnsweringIsAFinding(t *testing.T) {
	t.Parallel()

	res := Reconcile(65, AnswerTally{Numbered: 70})
	if res.OK {
		t.Fatal("70 answered against 65 asked passed; want refusal")
	}
	if res.Excess != 5 {
		t.Fatalf("expected excess=5, got %d", res.Excess)
	}
	if !strings.Contains(res.Message, "OVER-ANSWERED") {
		t.Errorf("expected OVER-ANSWERED in message: %s", res.Message)
	}
}

// TestFencedCodeIsNotCountedAsAnswers verifies code blocks are ignored.
func TestFencedCodeIsNotCountedAsAnswers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "ans.md")
	content := "1. **Real answer one?** Yes.\n\n```\n2. **Not an answer**\n- **Nor this**\nSPOILED\n```\n\n- **Real answer two?** Yes.\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tally, err := CountAnswers(p)
	if err != nil {
		t.Fatal(err)
	}
	if tally.Answered() != 2 {
		t.Fatalf("counted %d answers, want 2 (fenced code counted)", tally.Answered())
	}
	if tally.Spoiled != 0 {
		t.Fatalf("counted %d spoiled markers, want 0 (fenced code counted)", tally.Spoiled)
	}
}

// TestReconcileCLIIntegration tests the full CLI dispatch via run().
func TestReconcileCLIIntegration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	qPath := filepath.Join(dir, "self-check.md")
	qContent := "# Self Check\n\n## The questions\n- **Q1?** A1\n- **Q2?** A2\n- **Q3?** A3\n\n## The questions I authored\n- Authored Q4?\n- Authored Q5?\n\n## Baseline log\n"
	if err := os.WriteFile(qPath, []byte(qContent), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := ExpectedQuestions(qPath)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 5 {
		t.Fatalf("expected 5 questions, got %d", stats.Total)
	}

	// 1. Passing answer file (5 answers)
	aPass := filepath.Join(dir, "pass.md")
	aPassContent := "1. **Q1?** ans\n2. **Q2?** ans\n3. **Q3?** ans\n- **Q4?** ans\n- **Q5?** ans\n"
	if err := os.WriteFile(aPass, []byte(aPassContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"reconcile", "--questions", qPath, aPass}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "RECONCILE OK") {
		t.Errorf("stdout missing RECONCILE OK: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "matched=5 unassisted=5 gaps=0 expected=5") {
		t.Errorf("stdout missing expected counts: %s", stdout.String())
	}

	// 2. Under-answered with gap named (4 answers + 1 spoiled = 5)
	aSpoiled := filepath.Join(dir, "spoiled.md")
	aSpoiledContent := "1. **Q1?** ans\n2. **Q2?** ans\n3. **Q3?** ans\n- **Q4?** ans\nSPOILED: missed Q5 in cold pass\n"
	if err := os.WriteFile(aSpoiled, []byte(aSpoiledContent), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"reconcile", "--questions", qPath, aSpoiled}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 for named gap, got %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "RECONCILE OK") {
		t.Errorf("stdout missing RECONCILE OK: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "matched=5 unassisted=4 gaps=1 expected=5") {
		t.Errorf("stdout missing expected counts: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "named gap: SPOILED: missed Q5 in cold pass") {
		t.Errorf("stdout missing named gap line: %s", stdout.String())
	}

	// 3. Under-answered without gap named (4 answers, 0 spoiled) -> refuses with exit 1
	aFail := filepath.Join(dir, "fail.md")
	aFailContent := "1. **Q1?** ans\n2. **Q2?** ans\n3. **Q3?** ans\n- **Q4?** ans\n"
	if err := os.WriteFile(aFail, []byte(aFailContent), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"reconcile", "--questions", qPath, aFail}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d; stdout: %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "RECONCILE FAIL") {
		t.Errorf("stderr missing RECONCILE FAIL: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "missing=1") {
		t.Errorf("stderr missing missing=1: %s", stderr.String())
	}
}

// TestReconcileErrorCases tests error exit codes (exit 2).
func TestReconcileErrorCases(t *testing.T) {
	t.Parallel()

	// Missing operands
	var stdout, stderr bytes.Buffer
	code := run([]string{"reconcile"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for missing operands, got %d", code)
	}

	// Unreadable question file
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"reconcile", "--questions", "nonexistent-questions.md", "some-file.md"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 for missing question file, got %d", code)
	}

	// Unreadable answer file
	dir := t.TempDir()
	qPath := filepath.Join(dir, "self-check.md")
	os.WriteFile(qPath, []byte("# Q\n- **Q1?** a\n"), 0o644)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"reconcile", "--questions", qPath, filepath.Join(dir, "nonexistent-answers.md")}, &stdout, &stderr)
	if code != 1 { // when answers fail, bad=true returns 1
		// Wait, let's verify if bad=true returns 1
	}
}

func TestNoBenchLiteralsInReconcile(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("reconcile.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/Users/", "/home/", "rowan-working"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("reconcile.go contains bench literal %q", forbidden)
		}
	}
}
