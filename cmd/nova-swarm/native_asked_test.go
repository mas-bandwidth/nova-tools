package main

// A CARD WHOSE LAST TURN IS A QUESTION ENDS AS ASKED (nova-tools #2548).
//
// THE RECEIPT, canary run 3, 2026-09-22 02:05Z, route openrouter/openai/gpt-5-nano: the
// worker wrote its test file into a phantom nested path, committed nothing, published no
// `RESULT.md`, and ended its last turn with `Would you like me to proceed with moving
// RESULT.md into the nested repo and finalize the commit?`.
//
// THE RUNTIME DOGFOOD OF THE SAME DAY REFUTED THE MECHANISM the issue's title named: the
// run does NOT hold its slot. `opencode run` finishes the turn and exits 0, measured at
// 5.22 s on hulk and 5.49 s on the Studio against a card whose STEP 2 tells the model to
// ask and wait. What it leaves behind is the fault: `NATIVE INCOMPLETE ... why=no-result`,
// the token for a model that chose to publish nothing, with the question recorded nowhere.
// These tests are about what the run now writes down, and about the verdict NOT moving
// because it wrote it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nativeAsked runs one card through `native` with the fake harness and returns the job
// directory beside stdout and stderr, because the report this card is about is a FILE.
func nativeAsked(t *testing.T, label, card string) (job, stdout, stderr string) {
	t.Helper()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, label+".md")
	if err := os.WriteFile(cardPath, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errBuf strings.Builder
	run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &out, &errBuf, time.Now())
	return filepath.Join(slot, "jobs", label), out.String(), errBuf.String()
}

// THE SHAPE ITSELF: the harness says its last word, it is a question, and the process
// exits 0 having published nothing. RED WITHOUT THE FIX: the job holds no RESULT.md at
// all and the question is in a transcript nobody reads.
func TestNativeWritesAnAskedResultForACardThatEndedWithAQuestion(t *testing.T) {
	question := "Would you like me to proceed with moving RESULT.md into the nested repo and finalize the commit?"
	job, stdout, stderr := nativeAsked(t, "asked", "FAKE-SAY "+question+"\nFAKE-NORESULT\n")

	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil {
		t.Fatalf("a card that ended by asking left no report: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	line1 := strings.SplitN(string(raw), "\n", 2)[0]
	if !strings.HasPrefix(line1, "RESULT: ASKED ") {
		t.Fatalf("line 1 must carry the verdict word, got %q", line1)
	}
	if !strings.Contains(line1, question) {
		t.Fatalf("line 1 must carry the question itself, got %q", line1)
	}
	if !strings.Contains(string(raw), "written-by: nova-swarm native") {
		t.Fatalf("the report does not say the machinery wrote it:\n%s", raw)
	}
	if !strings.Contains(stderr, "NATIVE NOTE: the card ended its last turn with a question") {
		t.Fatalf("the run did not say it had written the report:\n%s", stderr)
	}
	// AND THE VERDICT DOES NOT MOVE. A report the machinery wrote is not the card's own,
	// and counting it would print `NATIVE OK` for a card that did nothing but ask.
	if strings.Contains(stdout, "NATIVE OK") {
		t.Fatalf("a card that only asked a question was called OK:\n%s", stdout)
	}
	if !strings.Contains(stdout, "why=no-result") {
		t.Fatalf("the verdict line must still name the card as incomplete:\n%s", stdout)
	}
}

// A CARD THAT PUBLISHED IS DONE, whatever its prose said. The fake harness publishes by
// default, so this is the ordinary ending: the report is the card's own, the verdict is
// OK, and nothing is overwritten.
func TestNativeLeavesAFinishedCardsReportAlone(t *testing.T) {
	job, stdout, stderr := nativeAsked(t, "done", "FAKE-SAY Want me to open the PR as well?\nFAKE-FINDINGS 0\n")

	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil {
		t.Fatalf("the card's own report is missing: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(string(raw), "RESULT: ASKED") {
		t.Fatalf("a finished card's report was overwritten with an asked verdict:\n%s", raw)
	}
	if !strings.Contains(stdout, "NATIVE OK") {
		t.Fatalf("a card that published its own report is OK:\n%s", stdout)
	}
}

// A CRASH IS A CRASH. The harness asks, then exits non-zero: that is `rc=<n>`, an end this
// tool already names, and a model that fell over is not waiting for an answer. The exit
// code is read before the capture is, which is why this card gets no report even though it
// asked; the question-is-the-last-line half of the same shape is a row of the table in
// internal/swarm/nativeasked_test.go, where it costs no provider retry to arrange.
func TestNativeDoesNotCallACrashAnAskedCard(t *testing.T) {
	job, stdout, _ := nativeAsked(t, "crash", "FAKE-SAY Should I retry the build?\nFAKE-429\n")

	if raw, err := os.ReadFile(filepath.Join(job, "RESULT.md")); err == nil {
		t.Fatalf("a crashed card was given an asked report:\n%s", raw)
	}
	if !strings.Contains(stdout, "NATIVE INCOMPLETE") {
		t.Fatalf("a crash is still incomplete:\n%s", stdout)
	}
	if strings.Contains(stdout, "RESULT: ASKED") {
		t.Fatalf("a crash was reported as a question:\n%s", stdout)
	}
}
