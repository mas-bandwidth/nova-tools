package main

// The three verbs pit stop 3 added, run the way a coordinator runs them: one line out, no
// network, and the card number never in a hand (issue #828, classes B, D, E and F).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sweep-verb-replays-a-source-file: an APPROVE row whose checks are QUEUED is not enqueued
// and one whose checks are green is, exactly once, and the whole run is one SWEEP line.
func TestSweepVerbEnqueuesOnlyTheGreenRow(t *testing.T) {
	queue := t.TempDir()
	ledger := "# pr\thead\tcard\tverdict\tat\tenqueued_at\tclosed_at\n" +
		"7\th7\tcard-9.md\tAPPROVE\t2026-09-16T17:00:00Z\t-\t-\n" +
		"8\th8\tcard-10.md\tAPPROVE\t2026-09-16T17:00:00Z\t-\t-\n"
	writeMainFile(t, queue, "ledger.tsv", ledger)
	source := writeMainFile(t, queue, "prs.tsv", strings.Join([]string{
		"7\tOPEN\tfalse\th7\t-\tfix #601\tfast:QUEUED,vet:SUCCESS\tfalse",
		"8\tOPEN\tfalse\th8\t-\tfix #602\tfast:SUCCESS,vet:SUCCESS\tfalse",
	}, "\n")+"\n")

	var out, errb bytes.Buffer
	code := run([]string{"sweep", "--repo", "mas-bandwidth/nova-tools", "--queue", queue, "--source", source}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("sweep exit = %d, stderr=%s", code, errb.String())
	}
	if n := len(strings.Split(strings.TrimRight(out.String(), "\n"), "\n")); n != 1 {
		t.Errorf("sweep printed %d lines, want one SWEEP line:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "enqueued=1") || !strings.Contains(out.String(), "pending=1") {
		t.Errorf("SWEEP line = %q, want enqueued=1 and pending=1", out.String())
	}
	raw, err := os.ReadFile(filepath.Join(queue, "enqueued.tsv"))
	if err != nil {
		t.Fatalf("nothing was enqueued: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != "mas-bandwidth/nova-tools\t8" {
		t.Errorf("enqueued.tsv = %q, want the green PR alone", got)
	}

	// the second sweep enqueues nothing: the row carries its enqueued_at now.
	out.Reset()
	if code := run([]string{"sweep", "--repo", "mas-bandwidth/nova-tools", "--queue", queue, "--source", source}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("second sweep exit = %d, stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "enqueued=0") {
		t.Errorf("second SWEEP line = %q, want enqueued=0 (never twice)", out.String())
	}
}

// a sweep without --repo or --queue is a refusal that names the remedy.
func TestSweepVerbRefusesWithoutItsFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"sweep", "--queue", t.TempDir()}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--repo is required") {
		t.Errorf("the refusal names no remedy: %q", errb.String())
	}
}

// reap-verb-is-one-line: --dry-run prints the REAP line and changes nothing on the bench.
func TestReapVerbPrintsOneLineAndDryRunChangesNothing(t *testing.T) {
	root, queue := t.TempDir(), t.TempDir()
	card := writeMainFile(t, filepath.Join(queue, "launched"), "card-100.md", "RESULT: CARD-100 orphan\n")
	// Class I (#828): the disposal follows the harness log, so the fixture carries one.
	writeMainFile(t, filepath.Join(queue, "harness"), "card-100.log", "supervise: deadline exceeded\nrc=143\n")
	writeMainFile(t, queue, "state.tsv", "next_card\t200\n")
	old := time.Now().Add(-4 * time.Hour)
	if err := os.Chtimes(card, old, old); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"reap", "--roots", root, "--queue", queue, "--deadline", "1800", "--dry-run"}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("reap exit = %d, stderr=%s", code, errb.String())
	}
	if n := len(strings.Split(strings.TrimRight(out.String(), "\n"), "\n")); n != 1 {
		t.Errorf("reap printed %d lines, want one REAP line:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "dry-run=true") || !strings.Contains(out.String(), "requeued=1") {
		t.Errorf("REAP line = %q, want dry-run=true and requeued=1", out.String())
	}
	if _, err := os.Stat(card); err != nil {
		t.Errorf("--dry-run moved the card: %v", err)
	}
}

// a reap without --deadline is a refusal that says what the deadline is for.
func TestReapVerbRefusesWithoutADeadline(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"reap", "--roots", t.TempDir(), "--queue", t.TempDir()}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--deadline is required") {
		t.Errorf("the refusal names no remedy: %q", errb.String())
	}
}

// cut --kind writes the card under the queue's own number, and --number is not a flag.
func TestCutKindVerbNumbersFromTheStateFile(t *testing.T) {
	dir := t.TempDir()
	queue, out := filepath.Join(dir, "queue"), filepath.Join(dir, "queue", "pending")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"cut", "--kind", "read", "--repo", "mas-bandwidth/nova-tools", "--pr", "812",
		"--head", "5f544272a1b0", "--title", "the reaper", "--out", out, "--queue", queue}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, time.Now().UTC()); code != 0 {
		t.Fatalf("cut --kind exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "CUT CARD card=card-1.md kind=read") {
		t.Errorf("CUT line = %q", stdout.String())
	}
	raw, err := os.ReadFile(filepath.Join(out, "card-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "RESULT: CARD-1 read of nova-tools PR812 at 5f544272a1b0 (the reaper)"
	if got := strings.SplitN(string(raw), "\n", 2)[0]; got != want {
		t.Errorf("line 1 = %q\nwant      %q", got, want)
	}

	// the next cut takes the next number, from the state file and nowhere else.
	stdout.Reset()
	if code := run(append(args, "--pr", "813"), &stdout, &stderr, time.Now().UTC()); code != 0 {
		t.Fatalf("second cut exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "card=card-2.md") {
		t.Errorf("second CUT line = %q, want card-2.md", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(append(args, "--number", "8130"), &stdout, &stderr, time.Now().UTC()); code != 2 {
		t.Fatalf("cut --number exit = %d, want 2 (--number is not a flag)", code)
	}
	if !strings.Contains(stderr.String(), "--number is not a flag") {
		t.Errorf("the refusal does not say --number is not a flag: %q", stderr.String())
	}
}

// cut without --kind is the pool-driven cutter, and pit stop 3 did not touch it.
func TestCutWithoutKindIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	pool := writeMainFile(t, dir, "pool.tsv", "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"cut", "--pool", pool, "--templates", templates, "--out", filepath.Join(dir, "out"), "--root", filepath.Join(dir, "root")}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2 (the models.tsv refusal is the old cutter's, unchanged)", code)
	}
	if !strings.Contains(errb.String(), "models.tsv") {
		t.Errorf("the old cutter's refusal has changed: %q", errb.String())
	}
}

// help carries the two new verbs and the typed cut, and marks neither unimplemented.
func TestHelpCarriesSweepReapAndCutKind(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	for _, want := range []string{"nova-pulse sweep   --repo", "nova-pulse reap    --roots", "nova-pulse cut     --kind"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help does not carry %q", want)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "nova-pulse" && (f[1] == "sweep" || f[1] == "reap") {
			if strings.Contains(line, "not yet implemented") {
				t.Errorf("a shipped verb is marked unimplemented: %q", line)
			}
		}
	}
}
