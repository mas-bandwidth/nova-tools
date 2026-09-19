package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// EVERY CALLER OF `native` PASSES THE WORD, AND NONE INVENTS IT (SPEC-SWARM rule 13d,
// demanded test 13d, issue #1545).
//
// The clauses this file holds:
//
//	"`batch --cards` takes `--tokens <n>|unmetered`, required, exit 2 naming the flag
//	 before any card starts."
//	"It is each card's own budget, the same for every card as `batch --tasks` has it, never
//	 divided among the cards and never a total for the batch."
//	"The batch puts it verbatim into every `native` argv it builds, the local one under
//	 `--harness` and the remote one under **Benches**."
//	"A `--runner` ... is handed the word as a sixth argument after those five, and a runner
//	 that reaches `native` without passing it on meets `native`'s own refusal."

// TestBatchCardsRefusesWithoutTheBudgetWord: a runnerless batch with no budget word is
// exit 2 BEFORE ANY CARD STARTS. The proof that no card started is the fixture's own argv
// record: the stand-in for nova-swarm records every invocation it is handed, and after
// this refusal there is no record at all.
func TestBatchCardsRefusesWithoutTheBudgetWord(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	harness := filepath.Join(dir, "harness")
	if err := os.WriteFile(harness, []byte("#!/bin/sh\necho FAKE-HARNESS \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tsv := card636(t, dir, "card-t")
	argvRecord := filepath.Join(dir, "self-argv")
	self := runnerDoing(t, dir, "nova-swarm",
		runnerStep{Op: "record", Path: argvRecord},
		runnerStep{Op: "stdout", Body: "NATIVE OK label=card-t"},
	)
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "TB1", Deadline: 10 * time.Second, Cards: tsv, Root: root,
		Harness: harness, Self: self,
		SlotsStore: aBenchSlotStore(t), SlotOwner: "fake-1",
		Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("a runnerless batch with no --tokens is exit 2, got %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "--tokens") {
		t.Errorf("the refusal names --tokens:\n%s", errb.String())
	}
	if _, err := os.Stat(argvRecord); err == nil {
		raw, _ := os.ReadFile(argvRecord)
		t.Errorf("a card started before the refusal; the fixture recorded:\n%s", raw)
	}
}

// TestBatchCardsPutsTheWordInTheLocalNativeArgv: the batch's own word, verbatim, in the
// argv it builds for a local card's `nova-swarm native`. Verbatim is the assertion: the
// batch neither parses the number nor divides it among the cards.
func TestBatchCardsPutsTheWordInTheLocalNativeArgv(t *testing.T) {
	for _, word := range []string{"250000", "unmetered"} {
		t.Run(word, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "root")
			harness := filepath.Join(dir, "harness")
			if err := os.WriteFile(harness, []byte("#!/bin/sh\necho FAKE-HARNESS \"$@\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			tsv := card636(t, dir, "card-t")
			argvRecord := filepath.Join(dir, "self-argv")
			self := runnerDoing(t, dir, "nova-swarm",
				runnerStep{Op: "record", Path: argvRecord},
				runnerStep{Op: "stdout", Body: "NATIVE OK label=card-t"},
			)
			var out, errb strings.Builder
			code := Batch(BatchInput{
				ID: "TB2", Deadline: 10 * time.Second, Cards: tsv, Root: root,
				Harness: harness, Self: self, Tokens: word,
				SlotsStore: aBenchSlotStore(t), SlotOwner: "fake-1",
				Stdout: &out, Stderr: &errb,
			})
			if code == 2 {
				t.Fatalf("the batch refused admission: %s", errb.String())
			}
			// The `record` step writes the WHOLE argv, one element per line; joining them
			// with a space is what makes "--tokens <word>" one adjacent pair rather than
			// two flags that merely both appear.
			argv := strings.Join(readLines(t, argvRecord), " ")
			if strings.TrimSpace(argv) == "" {
				t.Fatalf("the stand-in for nova-swarm recorded no argv\nstderr:\n%s", errb.String())
			}
			if !strings.Contains(argv, "--tokens "+word) {
				t.Fatalf("the local native argv carries --tokens %s:\n%s", word, argv)
			}
		})
	}
}

// TestBatchCardsPutsTheWordInTheRemoteNativeArgv: the same word in the argv ssh runs on a
// bench. The fake ssh records every argv it is handed and runs nothing.
func TestBatchCardsPutsTheWordInTheRemoteNativeArgv(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	benchRoot := filepath.Join(dir, "benchroot")
	bench := writeBenches(t, dir, "b2\tb2\t"+benchRoot+"\t1-15\t/home/me/.local/bin/opencode\t/home/me/.config/nova/auth\tnone")
	sshLog, _ := fakeBin(t, dir)
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\tb2:3\tmodel\t"+a+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "TB3", Deadline: 30 * time.Second, Cards: tsv, Root: root,
		Benches: bench, Bench: "b2", Tokens: "175000", Stdout: &out, Stderr: &errb,
		SlotsStore: aBenchSlotStore(t), SlotOwner: "fake-1",
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	var runs []string
	for _, l := range readLines(t, sshLog) {
		if strings.Contains(l, "nova-swarm native") {
			runs = append(runs, l)
		}
	}
	if len(runs) == 0 {
		t.Fatalf("the fake ssh saw no run; the remote card never ran")
	}
	for _, l := range runs {
		if !strings.Contains(l, "--tokens 175000") {
			t.Fatalf("the remote native argv carries --tokens 175000, got %q", l)
		}
	}
}

// TestBatchCardsHandsTheRunnerTheWordAsItsSixthArgument: rule 13d, "A `--runner` is the
// caller's own command, started once per card with the label, the slot, the model, the
// card's path and the root; it is handed the word as a sixth argument after those five."
//
// THE POSITION IS THE CONTRACT. A runner is somebody else's program reading argv by index,
// so the word going SIXTH -- after the five that were already there, never among them --
// is what keeps every runner written before this rule still correct about its first five.
func TestBatchCardsHandsTheRunnerTheWordAsItsSixthArgument(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := card636(t, dir, "card-r")
	argvRecord := filepath.Join(dir, "runner-argv")
	runner := runnerDoing(t, dir, "runner",
		runnerStep{Op: "record", Path: argvRecord},
	)
	var out, errb strings.Builder
	code := Batch(BatchInput{
		ID: "TB4", Deadline: 10 * time.Second, Cards: tsv, Root: root,
		Runner: runner, Tokens: "90000",
		Stdout: &out, Stderr: &errb,
	})
	if code == 2 {
		t.Fatalf("the batch refused admission: %s", errb.String())
	}
	// The `record` step writes os.Args, one element per line, so line 1 is the program
	// itself and the arguments are what follows it.
	lines := readLines(t, argvRecord)
	if len(lines) == 0 {
		t.Fatalf("the runner recorded no argv\nstderr:\n%s", errb.String())
	}
	args := lines[1:]
	if len(args) != 6 {
		t.Fatalf("the runner is handed six arguments (label slot model card root tokens), got %d:\n%v", len(args), args)
	}
	if args[5] != "90000" {
		t.Fatalf("the sixth argument is the batch's own word, got %q:\n%v", args[5], args)
	}
}
