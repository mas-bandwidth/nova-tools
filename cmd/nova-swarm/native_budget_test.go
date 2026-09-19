package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestNativeBudgetEndsTheCardAndKeepsFindings is demanded test 13d (SPEC-SWARM.md:3324,
// issue #1545). It is cut in slices, and this file holds the FIRST: the word is required
// on every native launch, and every in-repo constructor of a native argv passes it along.
//
// THE CLAUSES OF 13d THIS FILE HOLDS:
//
//	"`native` without `--tokens` is exit 2 naming the flag and makes no directory,
//	 `--tokens 0` is refused, and `batch --cards` without it is exit 2 before any card
//	 starts"
//	"`--tokens unmetered` prints `budget=unmetered`"
//	"the batch's local and remote `native` argv both carry `--tokens` with the batch's own
//	 word, and a `--runner` receives it as its sixth argument"
//
// The rest of 13d -- the sampler, the stop, the two-launch accounting, the packet -- comes
// in the slices after this one, and each is red from its own clause before it is green.

// budgetNativeArgs is one complete native argv with the budget word the caller names, and
// with it omitted entirely when the word is empty: the two shapes every case here wants.
func budgetNativeArgs(t *testing.T, bin, card, slot, root, tokens string) []string {
	t.Helper()
	args := []string{"native",
		"--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", card, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	if tokens != "" {
		args = append(args, "--tokens", tokens)
	}
	return args
}

// budgetCard writes a card the fake harness will run through without spending anything.
func budgetCard(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "card.md")
	if err := os.WriteFile(path, []byte("a card\nFAKE-FINDINGS 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestNativeRefusesWithoutTheBudgetWord: rule 13d, "The word is required on every launch."
// `native` takes `--tokens <n>` or `--tokens unmetered`; without it the verb is exit 2
// naming the flag, and `0` is refused, exactly as on `add`.
//
// AND IT MAKES NO DIRECTORY. The refusal is a flag check and it stands above everything
// `native` does to the disk -- the job directory at native.go's MkdirAll, the lease it
// takes there, the data home, the temp directory. A refusal that had already made a
// directory would leave the bench's hygiene pass a job that never ran.
func TestNativeRefusesWithoutTheBudgetWord(t *testing.T) {
	bin := nativeHarness(t)
	for _, tc := range []struct {
		name   string
		tokens string
		words  []string
	}{
		{"absent", "", []string{"--tokens"}},
		{"zero", "0", []string{"--tokens"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			card := budgetCard(t, root)
			var stdout, stderr bytes.Buffer
			rc := run(budgetNativeArgs(t, bin, card, slot, root, tc.tokens),
				strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != 2 {
				t.Fatalf("a native launch with tokens=%q is exit 2, got %d\nstdout:\n%s\nstderr:\n%s",
					tc.tokens, rc, stdout.String(), stderr.String())
			}
			for _, w := range tc.words {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("the refusal names %q:\n%s", w, stderr.String())
				}
			}
			// NO DIRECTORY WAS MADE. The job directory, the data home and the temp
			// directory are all the run's own, and none of them exists after a refusal
			// this early.
			for _, made := range []string{
				filepath.Join(slot, "jobs"),
				filepath.Join(slot, "data"),
				filepath.Join(slot, "tmp"),
			} {
				if _, err := os.Stat(made); err == nil {
					t.Errorf("the refusal made %s; rule 13d refuses before any directory is made", made)
				}
			}
		})
	}
}

// TestNativeUnmeteredPrintsTheWordOnTheLine: rule 13d, "`NATIVE OK` always carries
// `budget=`, which is `budget=unmetered`, or rule 13's three spellings against the
// number." A card launched `--tokens unmetered` runs to its deadline and says so.
func TestNativeUnmeteredPrintsTheWordOnTheLine(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := budgetCard(t, root)
	var stdout, stderr bytes.Buffer
	rc := run(budgetNativeArgs(t, bin, card, slot, root, "unmetered"),
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("an unmetered native launch exits 0, got %d:\n%s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), " budget=unmetered ") {
		t.Fatalf("NATIVE OK carries budget=unmetered:\n%s", stdout.String())
	}
}

// TestNativeNumericBudgetPrintsAgainstTheNumber: the same line under a number. Until a
// sample has observed anything the spelling is rule 13's own dash -- "`budget=-/<n>` for a
// job whose usage was never observed" -- and it is never silence and never `unmetered`.
func TestNativeNumericBudgetPrintsAgainstTheNumber(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := budgetCard(t, root)
	var stdout, stderr bytes.Buffer
	rc := run(budgetNativeArgs(t, bin, card, slot, root, "50000"),
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("a numeric native launch exits 0, got %d:\n%s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "/50000 ") {
		t.Fatalf("NATIVE OK carries budget=<spent|n+|->/50000:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), " budget=unmetered") {
		t.Fatalf("a numeric budget never prints unmetered:\n%s", stdout.String())
	}
}

// TestNativeBudgetSitsWhereTheGrammarPutsIt: the output grammar (SPEC-SWARM.md:1946) puts
// `budget=` immediately after `harness=<ok|silent>` and ahead of every optional tail, so a
// reader parses one fixed line. The position is the contract, not merely the presence.
func TestNativeBudgetSitsWhereTheGrammarPutsIt(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := budgetCard(t, root)
	var stdout, stderr bytes.Buffer
	rc := run(budgetNativeArgs(t, bin, card, slot, root, "unmetered"),
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("the launch exits 0, got %d:\n%s", rc, stderr.String())
	}
	line := nativeOKLine(t, stdout.String())
	fields := strings.Fields(line)
	for i, f := range fields {
		if !strings.HasPrefix(f, "harness=") {
			continue
		}
		if i+1 >= len(fields) || !strings.HasPrefix(fields[i+1], "budget=") {
			t.Fatalf("budget= follows harness= on the NATIVE OK line (grammar, SPEC-SWARM.md:1946):\n%s", line)
		}
		return
	}
	t.Fatalf("the NATIVE OK line carries no harness= field:\n%s", line)
}

// nativeOKLine is the one NATIVE OK line in a capture, failed for if there is none.
func nativeOKLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "NATIVE OK ") {
			return line
		}
	}
	t.Fatalf("no NATIVE OK line in:\n%s", out)
	return ""
}
