package pulse

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coordinatorFake is a set of seams that drive the coordinator loop with known
// per-action counts and write one event per action to the events writer.
type coordinatorFake struct {
	harvest int // how many RESULT.md were disposed this tick
	sweep   int // how many pools were folded
	cuts    int // how many cards were cut
	refused int // how many cards were refused on dedup
	source  string
	dedup   string
}

func (f *coordinatorFake) Gate(tick int) (bool, string, error) {
	return false, "success", nil
}

func (f *coordinatorFake) Harvest(tick int, events io.Writer) (int, []Undecided, error) {
	for i := 0; i < f.harvest; i++ {
		fmt.Fprintf(events, "coordinator harvest tick=%d msg=%q\n", tick, fmt.Sprintf("disposed RESULT.md card-%d", i+1))
	}
	return f.harvest, nil, nil
}

func (f *coordinatorFake) Sweep(tick int, events io.Writer) (int, error) {
	for i := 0; i < f.sweep; i++ {
		fmt.Fprintf(events, "coordinator sweep tick=%d msg=%q\n", tick, fmt.Sprintf("folded pool %d", i))
	}
	return f.sweep, nil
}

func (f *coordinatorFake) Reap(tick int) (int, int, []Undecided, error) {
	return 0, 0, nil, nil
}

func (f *coordinatorFake) Refill(tick int, events io.Writer) (int, error) {
	for i := 0; i < f.cuts; i++ {
		fmt.Fprintf(events, "coordinator fill tick=%d source=%s msg=%q\n", tick, f.source, "cut")
	}
	for i := 0; i < f.refused; i++ {
		fmt.Fprintf(events, "coordinator fill tick=%d source=%s msg=%q\n", tick, f.source, f.dedup)
	}
	return f.cuts, nil
}

func (f *coordinatorFake) Launch(tick int) (int, int, error) {
	return 0, 0, nil
}

func TestIssue2186(t *testing.T) {
	f := &coordinatorFake{
		harvest: 2,
		sweep:   1,
		cuts:    2,
		refused: 1,
		source:  "nova-tools",
		dedup:   "dedup: PR42 at abc12345 already has a card in pending",
	}

	queue := t.TempDir()
	if err := createDirs(t, queue, "pending", "launched", "done", "failed"); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	Run(RunInput{
		Queue:    queue,
		Roots:    filepath.Join(queue, "root"),
		Once:     true,
		Locked:   true,
		Stdout:   &stdout,
		Stderr:   io.Discard,
		Gate:     f,
		Harvest:  f,
		Sweep:    f,
		Reap:     f,
		Refill:   f,
		Launcher: f,
	})

	out := stdout.String()

	// One harvest line per RESULT.md disposed.
	if n := linesWithPrefix(out, "coordinator harvest"); n != f.harvest {
		t.Errorf("wanted %d coordinator harvest lines, got %d", f.harvest, n)
	}

	// One sweep line per pool folded.
	if n := linesWithPrefix(out, "coordinator sweep"); n != f.sweep {
		t.Errorf("wanted %d coordinator sweep lines, got %d", f.sweep, n)
	}

	// One fill line per card, with source and the dedup in msg.
	if n := linesWithPrefix(out, "coordinator fill"); n != f.cuts+f.refused {
		t.Errorf("wanted %d coordinator fill lines, got %d", f.cuts+f.refused, n)
	}
	if !strings.Contains(out, f.source) {
		t.Errorf("fill line missing source %q", f.source)
	}
	if !strings.Contains(out, f.dedup) {
		t.Errorf("fill line missing dedup %q", f.dedup)
	}

	// The WIDTH line is unchanged.
	if !strings.Contains(out, "PULSE WIDTH ") {
		t.Errorf("WIDTH line is missing")
	}
}

func linesWithPrefix(out, prefix string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			n++
		}
	}
	return n
}

func createDirs(t *testing.T, base string, names ...string) error {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
			return err
		}
	}
	return nil
}
