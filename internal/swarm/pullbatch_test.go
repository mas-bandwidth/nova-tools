package swarm

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePullCard lays one card in a queue directory under its own label.
func writePullCard(t *testing.T, queue, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(queue, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write card %s: %v", name, err)
	}
}

// a-batch-clips-between-cards (docs/SPEC-JOBS.md section 6): every card on the shared
// clone is clipped before the next card runs, so card n+1 never sees card n's uncommitted
// diff and every card keeps its own RESULT.md.
func TestABatchClipsBetweenCards(t *testing.T) {
	queue := t.TempDir()
	clone := t.TempDir()
	harvest := t.TempDir()
	writePullCard(t, queue, "one.card", ":kind go\n:repo r\n:effort 5\n")
	writePullCard(t, queue, "two.card", ":kind go\n:repo r\n:effort 5\n")

	dirty := false
	var ran, clipped []string
	var stdout, stderr bytes.Buffer
	code := PullBatch(PullInput{
		Queue:   queue,
		Clone:   clone,
		Harvest: harvest,
		Batch:   2,
		Run: func(c PullBatchCard) (int, error) {
			if dirty {
				return 0, fmt.Errorf("card %s saw card n's uncommitted diff", c.Label)
			}
			ran = append(ran, c.Label)
			dirty = true
			return 1, nil
		},
		Clip: func(c PullBatchCard, result string) error {
			clipped = append(clipped, c.Label)
			dirty = false
			return os.WriteFile(result, []byte("RESULT: "+c.Label+"\n"), 0o644)
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("PullBatch exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if strings.Join(ran, ",") != "one,two" {
		t.Fatalf("run order = %q, want one,two", strings.Join(ran, ","))
	}
	if strings.Join(clipped, ",") != "one,two" {
		t.Fatalf("clip order = %q, want one,two (a clip after each card)", strings.Join(clipped, ","))
	}
	for _, label := range []string{"one", "two"} {
		if _, err := os.Stat(filepath.Join(harvest, label, "RESULT.md")); err != nil {
			t.Fatalf("card %s kept no RESULT.md: %v", label, err)
		}
	}
	if strings.Count(stdout.String(), "\n") != 1 || !strings.HasPrefix(stdout.String(), "PULL OK") {
		t.Fatalf("pull output = %q, want one PULL OK line", stdout.String())
	}
}

// a-card-over-effort-returns-the-remainder (docs/SPEC-JOBS.md section 6): a card past its
// :effort stops the batch, and every card not done -- that card and the ones behind it --
// goes back to queue/.
func TestACardOverEffortReturnsTheRemainder(t *testing.T) {
	queue := t.TempDir()
	clone := t.TempDir()
	harvest := t.TempDir()
	writePullCard(t, queue, "a.card", ":kind go\n:repo r\n:effort 5\n")
	writePullCard(t, queue, "b.card", ":kind go\n:repo r\n:effort 1\n")
	writePullCard(t, queue, "c.card", ":kind go\n:repo r\n:effort 5\n")

	var ran []string
	var stdout, stderr bytes.Buffer
	code := PullBatch(PullInput{
		Queue:   queue,
		Clone:   clone,
		Harvest: harvest,
		Batch:   3,
		Run: func(c PullBatchCard) (int, error) {
			ran = append(ran, c.Label)
			if c.Label == "b" {
				return 2, nil // past b's :effort 1
			}
			return 1, nil
		},
		Clip: func(c PullBatchCard, result string) error {
			return os.WriteFile(result, []byte("RESULT: "+c.Label+"\n"), 0o644)
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("PullBatch exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if strings.Join(ran, ",") != "a,b" {
		t.Fatalf("run order = %q; a past-effort card stops the batch and c must not run", strings.Join(ran, ","))
	}
	for _, label := range []string{"b", "c"} {
		if _, err := os.Stat(filepath.Join(queue, label+".card")); err != nil {
			t.Fatalf("remainder card %s did not return to queue/: %v", label, err)
		}
	}
	if _, err := os.Stat(filepath.Join(queue, "a.card")); !os.IsNotExist(err) {
		t.Fatalf("the done card a stayed in queue/: %v", err)
	}
}
