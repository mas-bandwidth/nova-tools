//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE CARD THAT HUNG, AND WHAT IT COST. `js-under-20-bytes` (2026-09-19) took a refusal,
// stopped making progress, and was reaped by its --deadline 1200s twenty minutes later:
// rc=-1, wall=1200.04s, NO RESULT.md, $0.0244 spent, a bench slot held, and a coordinator
// with nothing at all to read for eighteen of those minutes. `batch` has watched its cards
// for exactly this since issue #593; `native`, the verb every card on every bench runs
// through, had no watch -- its wait ended only when the child exited, at the deadline, or on
// a TERM from outside.

// TestNativeIdleEndsAStillCardLongBeforeItsDeadline: a harness that names a refusal and then
// goes still ends at --idle 2s, not at --deadline 60s, and the run says what it saw.
// RED WITHOUT THE FIX: the run returns at 60s with no report and no line.
func TestNativeIdleEndsAStillCardLongBeforeItsDeadline(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	card := "FAKE-SAY sh: 1: cannot create /etc/hosts: Permission denied\nFAKE-SLEEP 60\n"
	if err := os.WriteFile(cardPath, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin,
		"--model", "fake/fake-model", "--label", "stillcard", "--card", cardPath, "--slot", slot,
		"--root", root, "--deadline", "30s", "--idle", "2s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	_ = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	// THE ASSERTION IS THE EVENT, NOT THE CLOCK. Only the idle watch ends a run this way:
	// the deadline path writes no blocked report and prints no such line, so a run that sat
	// to its deadline fails here whatever the wall clock says.
	if !strings.Contains(stdout.String(), "NATIVE NOTE: the card published no report of its own") {
		t.Fatalf("the watch ended the card and wrote the report it owed:\n%s\n%s", stdout.String(), stderr.String())
	}
	// THE REFUSAL IS NAMED THE MOMENT IT ARRIVES, on the run's own stderr, while the card
	// is still alive -- not at the reap.
	if !strings.Contains(stderr.String(), "WALL REFUSED write /etc/hosts task=stillcard") {
		t.Fatalf("the typed line is announced as the refusal arrives:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("a card the watch ended still prints the NATIVE OK line:\n%s", stdout.String())
	}
	// AND THE CARD OWES A REPORT. `no-result` is the token for a model that chose to
	// publish nothing; this card never had the chance, and the run says so for it.
	job := filepath.Join(slot, "jobs", "stillcard")
	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil {
		t.Fatalf("a card the machinery ended is given a report naming the block: %v\n%s", err, stdout.String())
	}
	for _, want := range []string{"RESULT: BLOCKED stillcard", "WALL REFUSED write /etc/hosts", "written-by: nova-swarm native"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the blocked report carries %q:\n%s", want, raw)
		}
	}
	if _, err := os.Stat(filepath.Join(slot, "usage.tsv")); err != nil {
		t.Fatalf("usage.tsv absent after an idle end: %v", err)
	}
}

// TestNativeIdleZeroWatchesNothing: --idle 0 is the behaviour every run had before the watch
// existed, and a caller can still type it. The card runs to its own end.
func TestNativeIdleZeroWatchesNothing(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-SAY sh: 1: cannot create /etc/hosts: Permission denied\nFAKE-SLEEP 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin,
		"--model", "fake/fake-model", "--label", "unwatched", "--card", cardPath, "--slot", slot,
		"--root", root, "--deadline", "60s", "--idle", "0", "--no-wall"}
	var stdout, stderr bytes.Buffer
	_ = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if strings.Contains(stdout.String(), "CARD IDLE") {
		t.Fatalf("a run with no idle window ends nothing for idleness:\n%s", stdout.String())
	}
	raw, err := os.ReadFile(filepath.Join(slot, "jobs", "unwatched", "RESULT.md"))
	if err == nil && strings.Contains(string(raw), "RESULT: BLOCKED") {
		t.Fatalf("a run that was never ended by the watch writes no blocked report:\n%s", raw)
	}
	// The refusal is still NAMED, because naming it costs the card nothing.
	if !strings.Contains(stderr.String(), "WALL REFUSED write /etc/hosts") {
		t.Fatalf("a refusal is announced whether or not anything acts on it:\n%s", stderr.String())
	}
}
