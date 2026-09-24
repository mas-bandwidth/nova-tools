package pulse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue1820 reproduces nova-tools#1820: launch --queue writes a three-field
// row (label<TAB>model<TAB>card path). Harvest relaunch must read queue.tsv and
// admit those rows -- not drop them on a five-field reader and print PULSE POOL
// EMPTY (SPEC-PULSE rule 15, docs/CLI.md:1318-1319).
func TestIssue1820(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/1820")
	fakeTool(t, specs, "nova-pulse", fakeSpec{Log: arglog, Default: fakeRule{
		Stdout: "PULSE OK id=p2 n=1 free-before=1 queued=0 batches=1 deadline=300",
	}})

	addCard(t, root, "done", "1", "flash", "RESULT done sha=ddd",
		"RESULT done sha=ddd\nDONE\nBRANCH rowan/bd\nREPO owner/repo\n")
	card := queueCard(t, root, "g2")

	out, errs := runHarvest(t, root)
	if strings.Contains(errs, "HARVEST REFUSED") {
		t.Fatalf("harvest refused a queue that launch wrote:\n%s", errs)
	}
	if strings.Contains(out, "PULSE POOL EMPTY") {
		t.Fatalf("relaunch said the pool was empty over a queued card:\n%s\n%s", out, errs)
	}

	var launchLine string
	for _, l := range arglogLines(t, arglog) {
		if strings.HasPrefix(l, "nova-pulse launch") {
			launchLine = l
			if !strings.Contains(l, "--queue") {
				t.Fatalf("relaunch lost --queue: %s", l)
			}
		}
	}
	if launchLine == "" {
		t.Fatalf("relaunch did not invoke nova-pulse launch:\n%s\n%s", out, errs)
	}

	raw, err := os.ReadFile(filepath.Join(root, "cards.tsv"))
	if err != nil {
		t.Fatalf("relaunch must cut cards.tsv: %v", err)
	}
	rows := nonempty(string(raw))
	found := false
	for _, r := range rows {
		if strings.Contains(r, card) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the queued card is not in the relaunch's cards.tsv (queue first, rule 15): %q\nout=%slaunch=%s",
			rows, out, launchLine)
	}
	if len(rows) > 0 && !strings.Contains(rows[0], card) {
		t.Fatalf("the queued card is not the relaunch's first row (queue first, rule 15): %q", rows)
	}

	if _, err := os.Stat(filepath.Join(root, "queue.tsv")); err == nil {
		t.Fatal("the queue was handed to launch and must no longer be pending")
	}
	taken, _ := filepath.Glob(filepath.Join(root, "queue.tsv.taken-*"))
	if len(taken) != 1 {
		t.Fatalf("the taken queue must be parked, never deleted (rule 15): %v", taken)
	}

	fmt.Fprintf(os.Stderr, "issue1820: launch=%s taken=%v\n", launchLine, taken)
}
