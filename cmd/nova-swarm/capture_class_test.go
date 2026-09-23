package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A CARD CANNOT REWRITE THE CAPTURE THE PARENT CLASSIFIES (issue #1892).
//
// The job directory is a --write. After the child exits, native classified
// <job>/harness-output.log by name. O_NOFOLLOW on open defends a symlink planted
// by an earlier run; it does not defend unlink-and-replace of that name by THIS
// run. The child's stdout is a pipe into io.MultiWriter(native.log, harness-output.log):
// the parent's fd keeps the old inode, ReadFile after Wait follows the new name.
//
// The receipt, Studio 2026-09-19: a real shell denial, RESULT.md published, then
// os.Remove + rewrite of harness-output.log, and the line still said
// `NATIVE OK ... rc=0 harness=ok`. The unread copy in <slot>/native.log still
// held `Permission denied`. A coordinator reading OK ships a commit nobody compiled.
func TestCardRewriteOfCaptureDoesNotSpoofNativeOK(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	denied := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(denied, []byte("#!/bin/sh\necho ran\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-REWRITE-CAPTURE "+denied+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "go-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())

	jobDir := filepath.Join(slot, "jobs", "go-card")
	rewritten, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
	if err != nil {
		t.Fatalf("the rewritten capture is missing: %v", err)
	}
	if !strings.Contains(string(rewritten), "card rewrote the capture") {
		t.Fatalf("the child did not replace the capture; the test is not the rewrite:\n%s", rewritten)
	}
	if strings.Contains(string(rewritten), "Permission denied") {
		t.Fatalf("the rewritten capture still holds the denial, so a by-name classify cannot be spoofed:\n%s", rewritten)
	}
	parent, err := os.ReadFile(filepath.Join(slot, "native.log"))
	if err != nil {
		t.Fatalf("native.log (the parent tee, --read on the slot) is missing: %v", err)
	}
	if !strings.Contains(string(parent), "Permission denied") {
		t.Fatalf("the parent tee does not hold the real denial; the rewrite is not covering a denial:\n%s", parent)
	}

	if strings.Contains(stdout.String(), "NATIVE OK") {
		t.Errorf("a denial the card rewrote out of the job file still reported OK:\n%s", stdout.String())
	}
	if rc == 0 {
		t.Errorf("a run whose capture held a real denial exits non-zero, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "NATIVE REFUSED") {
		t.Errorf("the run owes one refusal line naming the class:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), denied) {
		t.Errorf("the refusal names the denied path %s; it reads:\n%s", denied, stderr.String())
	}
}

// TestNativeGateThatCouldNotRunIsNeverOK is the honest-denial control: the #1465
// line is still in the capture, nobody rewrote it, and the run is refused rather
// than OK. A rewrite-only fence that left an unread denial as OK would still be
// the silent-green class.
func TestNativeGateThatCouldNotRunIsNeverOK(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	const refused = "/opt/sdk/go1.26.5/bin/go"
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-EXEC-REFUSED "+refused+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "go-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())

	if strings.Contains(stdout.String(), "NATIVE OK") {
		t.Errorf("a denial nobody read and the run still reported OK:\n%s", stdout.String())
	}
	if rc == 0 {
		t.Errorf("a run whose capture holds a denial exits non-zero, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	line := stderr.String()
	if !strings.Contains(line, "NATIVE REFUSED") {
		t.Fatalf("the run owes one refusal line naming the class:\n%s", line)
	}
	for _, want := range []string{refused, "step=3", "operation=unverified", "Permission denied"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal names %q; it reads:\n%s", want, line)
		}
	}
	for _, forbidden := range []string{"never executed", "nothing compiled", "the gate never"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("the refusal asserts %q, which the shell's line cannot establish:\n%s", forbidden, line)
		}
	}
	if !strings.Contains(line, filepath.Join(slot, "jobs", "go-card")) {
		t.Errorf("the refusal names the job directory so the spend is harvestable; it reads:\n%s", line)
	}
	if _, err := os.Stat(filepath.Join(slot, "jobs", "go-card", "RESULT.md")); err != nil {
		t.Errorf("the card's own result is left where it was published: %v", err)
	}
}

// TestNativeOversizedCaptureIsNeverOK is the overflow control (Stella's HOLD on
// #2073): a noisy worker that fills the parent tee past the classification bound
// cannot be silently OK. Truncated classification is unverifiable and refused.
func TestNativeOversizedCaptureIsNeverOK(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte(fmt.Sprintf("FAKE-CAPTURE-BYTES %d\n", swarm.ClassifiedCaptureMax+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "loud-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())

	if strings.Contains(stdout.String(), "NATIVE OK") {
		t.Errorf("an overflowed classification still reported OK:\n%s", stdout.String())
	}
	if rc == 0 {
		t.Errorf("an overflowed classification exits non-zero, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	line := stderr.String()
	if !strings.Contains(line, "NATIVE REFUSED") {
		t.Fatalf("the run owes one refusal line naming the class:\n%s", line)
	}
	for _, want := range []string{"unverifiable", fmt.Sprintf("%d", swarm.ClassifiedCaptureMax)} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal names %q; it reads:\n%s", want, line)
		}
	}
}

// TestNativeOrdinaryRunIsStillOK: a card that merely prints another program's
// refusal and carries on is a finished run and still says OK.
func TestNativeOrdinaryRunIsStillOK(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-REFUSE 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "read-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	if rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now()); rc != 0 {
		t.Fatalf("a card that was refused a READ and carried on is a finished run, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK ") {
		t.Fatalf("the NATIVE OK line is printed for a run whose capture holds no shell denial:\n%s", stdout.String())
	}
}
