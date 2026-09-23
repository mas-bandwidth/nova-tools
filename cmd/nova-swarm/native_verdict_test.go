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
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, label+".md")
	if err := os.WriteFile(cardPath, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	return stdout.String()
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
