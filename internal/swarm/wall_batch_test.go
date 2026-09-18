package swarm

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The harness's own words for a permission it answers with a reject (fence.go), written into
// the card's own log the way OpenCode writes it.
const wallAutoRejectLine = "! permission requested: external_directory (/jobs/scratch/*); auto-rejecting"

// TestAWallDeathIsNamedEndWall: a card whose own log carries the auto-reject line and which
// published no RESULT.md ends reason=wall with the refused path, and the report line names the
// wall, its path and the last STEP it reached. RED WITHOUT THE CLASS: the gather read the
// absence of a result and scored `no-result`, blaming the model for a wall the machinery shut.
func TestAWallDeathIsNamedEndWall(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{{"a", "RESULT: a\nMISSING"}})
	runner := runnerDoing(t, dir, "wall-death",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/{slot}/native.log",
			Body: "STEP 1\npwd && mkdir -p scratch\nSTEP 2\n" + wallAutoRejectLine + "\nError: The user rejected permission to use this specific tool call.\n"},
		runnerStep{Op: "exit", N: 0},
	)
	code, out, errs := runBatch(t, tsv, root, runner, 5*time.Second) // wall-ok: the fake runner exits on its own; the 5s is the outer stop, not a bet on the machine
	if code != 1 {
		t.Fatalf("a walled card abstains and exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=0 abstain=1") {
		t.Fatalf("a wall death is an abstain, never folded:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: ABSTAIN reason=wall") {
		t.Fatalf("a wall death is named reason=wall, never no-result:\n%s", out)
	}
	if !strings.Contains(out, "path=/jobs/scratch/*") {
		t.Fatalf("the wall abstain carries the refused path:\n%s", out)
	}
	if !strings.Contains(errs, "WALL task=a path=/jobs/scratch/* step=2") {
		t.Fatalf("the report line names the wall, its path and the step:\n%s", errs)
	}
}

// TestAWallSurvivedIsDone: a card that published its RESULT.md despite the harness printing
// the auto-reject line is done. The wall is named only when there is no result to read: a
// wall that was worked around is not the card's end. RED WITHOUT THE ORDERING: the rejection
// line alone scored the card an abstain over a report it had already published.
func TestAWallSurvivedIsDone(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{{"a", "RESULT: a\nall green"}})
	runner := runnerDoing(t, dir, "wall-survived",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/{slot}/native.log", Body: "STEP 1\n" + wallAutoRejectLine + "\n"},
		publishCard("{job}"),
	)
	code, out, _ := runBatch(t, tsv, root, runner, 5*time.Second) // wall-ok: the fake runner exits on its own; the 5s is the outer stop, not a bet on the machine
	if code != 0 {
		t.Fatalf("a card that published despite the wall is done, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "a slot=1: all green") {
		t.Fatalf("the published line 2 is gathered verbatim:\n%s", out)
	}
	if strings.Contains(out, "reason=wall") {
		t.Fatalf("a wall that was survived is never an abstain:\n%s", out)
	}
}
