package main

// `NATIVE OK` was printed for a run that produced nothing (nova-tools #1844).
//
// THE RECEIPT, from the tools12 shift, 2026-09-19T18:00:28Z. Card `tools12c18` on bench
// `vision`: rc=1 on both attempts, zero tokens, zero dollars, no RESULT.md, no repo, and no
// NATIVE line in the slot's own native.log. The provider was the cause -- attempt 1
// `Error: {"name":"UnknownError","data":{"message":"Unexpected server error. Check server
// logs for details.","ref":"err_fb35c63e"}}`, attempt 2 `Cannot connect to API`. And the
// launcher's one log line for it read:
//
//	vision tools12c18 attempt=1 wall=159s NATIVE OK label=tools12c18 job=/home/glenn/...
//
// A fill loop or a manager counting its in-flight cards by that line counts a card that
// never ran as delivered, and a card that made zero paid calls as done. The shift caught it
// only because the returned bundle was zero bytes.
//
// So the word is earned. Every field on the line is unchanged; only the verdict word is,
// plus a `why=` naming which of the three conditions failed.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// nativeVerdict runs one card through `native` with the fake harness and returns stdout.
func nativeVerdict(t *testing.T, label, card string) string {
	t.Helper()
	stdout, _, _ := nativeVerdictRun(t, label, card)
	return stdout
}

// nativeVerdictRun is nativeVerdict plus stderr and the process exit, so a test can
// pin that a finished card never exits 255 (#2058). Local ssh(1) 255 is any error,
// not proof the remote command never started.
func nativeVerdictRun(t *testing.T, label, card string) (stdout, stderr string, code int) {
	t.Helper()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, label+".md")
	if err := os.WriteFile(cardPath, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code = run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &out, &errb, time.Now())
	return out.String(), errb.String(), code
}

// THE C18 SHAPE: the provider answers 5xx at request start, the harness exits 1, and the
// job directory holds no RESULT.md. RED WITHOUT THE FIX: `NATIVE OK ... rc=1`.
func TestNativeRefusesToSayOKForAProviderFailureThatProducedNothing(t *testing.T) {
	out := nativeVerdict(t, "c18", "FAKE-5XX\n")

	if strings.Contains(out, "NATIVE OK") {
		t.Fatalf("a run with rc!=0 and no RESULT.md said OK -- a fill loop counts that as delivered:\n%s", out)
	}
	if !strings.Contains(out, "NATIVE INCOMPLETE ") {
		t.Fatalf("the verdict line must still be printed, and say what it is:\n%s", out)
	}
	if !strings.Contains(out, "why=no-result") {
		t.Fatalf("the verdict must name why it is incomplete (no RESULT.md):\n%s", out)
	}
	// Every other field a reader parses is exactly where it was.
	for _, want := range []string{"label=c18", "job=", "rc=1", "wall=", "sandbox=", "card_sha256=", "harness="} {
		if !strings.Contains(out, want) {
			t.Fatalf("the incomplete line dropped %q, which every reader of this line parses:\n%s", want, out)
		}
	}
}

// A harness that exits 0, says nothing and writes nothing is the same class: the exit code
// was clean and the card produced no result, and nothing downstream should read that as a
// delivered card. Here the first condition to fail is the harness's own silence, which
// #591 already made a token on this line and which the verdict now acts on.
func TestNativeRefusesToSayOKWhenTheHarnessSaidNothing(t *testing.T) {
	out := nativeVerdict(t, "noresult", "FAKE-NORESULT\n")

	if strings.Contains(out, "NATIVE OK") {
		t.Fatalf("a clean exit that said nothing and wrote no RESULT.md said OK:\n%s", out)
	}
	if !strings.Contains(out, "why=harness-silent") {
		t.Fatalf("the verdict must name the silence:\n%s", out)
	}
	if !strings.Contains(out, " rc=0 ") {
		t.Fatalf("the exit code is still reported as it was:\n%s", out)
	}
}

// AND THE OTHER DIRECTION, so the fix is not "never say OK": a card that ran, answered and
// wrote its RESULT.md still gets the word, with no why= tail at all.
func TestNativeStillSaysOKForARunThatProducedItsResult(t *testing.T) {
	out := nativeVerdict(t, "green", "FAKE-RESULT ok\n")

	if !strings.Contains(out, "NATIVE OK ") {
		t.Fatalf("a run that produced its RESULT.md must still be OK:\n%s", out)
	}
	if strings.Contains(out, "why=") {
		t.Fatalf("an OK verdict carries no why= tail:\n%s", out)
	}
}

// THE SUPERMAN SHAPE (nova-tools #2058). Darwin, harness v1.18.20, 24 cards at once:
// 23 NATIVE OK, 1 launch exited 255 twice. Both job dirs held a complete RESULT.md;
// harness-output.log showed `error: Error starting FSEvents stream` right after
// `> build · <model>`, then the card's commands and "Wrote file successfully".
// The launcher printed the CAPACITY line and then nothing: no attempt=, no
// NATIVE OK/INCOMPLETE, exit 255. rr-run.sh retried in place and ran the card
// twice. Local ssh(1) exits 255 for any error, which is not proof the remote
// command never started: the outcome is potentially UNKNOWN, and a retry waits
// on reconciliation. A native that finishes a card and then exits 255 is a
// finished card a launcher can misread as a transport failure and retry.
//
// RED WITHOUT THE FIX: process exit 255 (the child's code passed through), or
// any verdict other than exactly one NATIVE INCOMPLETE with rc=255 why=rc.
func TestNativeHarnessExit255PrintsAVerdictAndDoesNotExit255(t *testing.T) {
	stdout, stderr, code := nativeVerdictRun(t, "fsevents", "FAKE-FSEVENTS\n")
	combined := stdout + stderr
	if strings.Contains(combined, "NATIVE OK") {
		t.Fatalf("a harness that exited 255 after writing RESULT.md must not say OK:\nstdout:\n%s\nstderr:\n%s\nexit %d", stdout, stderr, code)
	}
	if strings.Contains(combined, "NATIVE REFUSED") {
		t.Fatalf("a harness that exited 255 after writing RESULT.md must not say REFUSED:\nstdout:\n%s\nstderr:\n%s\nexit %d", stdout, stderr, code)
	}
	var incomplete []string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "NATIVE INCOMPLETE ") {
			incomplete = append(incomplete, line)
		}
	}
	if len(incomplete) != 1 {
		t.Fatalf("want exactly one NATIVE INCOMPLETE line, got %d:\nstdout:\n%s\nstderr:\n%s\nexit %d", len(incomplete), stdout, stderr, code)
	}
	line := incomplete[0]
	hasRC, hasWhy := false, false
	for _, f := range strings.Fields(line) {
		if f == "rc=255" {
			hasRC = true
		}
		if f == "why=rc" {
			hasWhy = true
		}
	}
	if !hasRC || !hasWhy {
		t.Fatalf("the INCOMPLETE line must carry rc=255 and why=rc:\n%s", line)
	}
	if code == 255 {
		t.Fatalf("native exited 255; local ssh(1) 255 is any error and is potentially UNKNOWN, so a launcher may retry a finished card. The child's 255 belongs on the line as rc=255:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if code != 1 {
		t.Fatalf("the verb ran and said NO, want exit 1, got %d:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

// THE NEGATIVE, so the 255 clamp is not "never say OK/INCOMPLETE": a card that
// produced its RESULT.md is still NATIVE OK at exit 0, and a silent harness is
// still NATIVE INCOMPLETE, and neither process exits 255.
func TestNativeOrdinaryCardsStillPrintOKAndIncomplete(t *testing.T) {
	stdout, stderr, code := nativeVerdictRun(t, "green255", "FAKE-RESULT ok\n")
	if !strings.Contains(stdout, "NATIVE OK ") {
		t.Fatalf("a run that produced its RESULT.md must still be OK:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if strings.Contains(stdout, "NATIVE INCOMPLETE ") {
		t.Fatalf("an OK card must not also be INCOMPLETE:\n%s", stdout)
	}
	if code == 255 {
		t.Fatalf("a normal OK card must not exit 255:\n%s", stdout)
	}
	if code != 0 {
		t.Fatalf("a run that produced its RESULT.md still exits 0, got %d:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	stdout, stderr, code = nativeVerdictRun(t, "quiet255", "FAKE-NORESULT\n")
	if !strings.Contains(stdout, "NATIVE INCOMPLETE ") {
		t.Fatalf("a silent harness must still be INCOMPLETE:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if strings.Contains(stdout, "NATIVE OK ") {
		t.Fatalf("a silent harness must not be OK:\n%s", stdout)
	}
	if code == 255 {
		t.Fatalf("an incomplete card must not exit 255:\n%s", stdout)
	}
}
