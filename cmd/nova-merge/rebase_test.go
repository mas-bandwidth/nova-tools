package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// fakeLauncher is the injected Launcher: it records the cards it was handed and reaches
// nothing, so a rebase test proves the order (cut, mark, launch) without a bench.
type fakeLauncher struct {
	cards []string
	err   error
}

func (f *fakeLauncher) Launch(card string) error {
	if f.err != nil {
		return f.err
	}
	f.cards = append(f.cards, card)
	return nil
}

// rebase-9319: one pass cuts exactly one card and writes one marker for each open
// rowan/* pull request that is DIRTY and has no marker yet, and launches each; a clean
// pull request, a non-rowan one and a rowan/replays- one are left alone. A second pass
// makes no duplicate.
func TestRebaseCutsOneCardPerDirtyUnmarkedPR(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	dir := t.TempDir()
	markers, out, queue := filepath.Join(dir, "markers"), filepath.Join(dir, "out"), filepath.Join(dir, "queue")
	l.host.Open = []merge.RebasePR{
		{Number: 11, HeadRef: "rowan/one", Title: "one", MergeState: "DIRTY"},
		{Number: 12, HeadRef: "rowan/two", Title: "two", MergeState: "CLEAN"},
		{Number: 13, HeadRef: "codex/three", Title: "three", MergeState: "DIRTY"},
		{Number: 14, HeadRef: "rowan/replays-four", Title: "four", MergeState: "DIRTY"},
	}
	args := []string{"rebase", "--once", "--repo", "mas-bandwidth/nova-tools", "--markers", markers, "--out", out, "--queue", queue}

	exit, stdout, stderr := l.run(args...)
	if exit != 0 {
		t.Fatalf("rebase: exit %d\nstdout=%s\nstderr=%s", exit, stdout, stderr)
	}
	if stdout != "REBASE tick cards=1\n" {
		t.Errorf("stdout = %q, want one line %q", stdout, "REBASE tick cards=1\n")
	}
	if _, err := os.Stat(filepath.Join(markers, "pr-11")); err != nil {
		t.Errorf("a DIRTY unmarked pull request got no marker: %v", err)
	}
	for _, n := range []string{"pr-12", "pr-13", "pr-14"} {
		if _, err := os.Stat(filepath.Join(markers, n)); err == nil {
			t.Errorf("a clean, non-rowan or replays pull request was marked: %s", n)
		}
	}
	cards, _ := filepath.Glob(filepath.Join(out, "card-*.md"))
	if len(cards) != 1 {
		t.Fatalf("cards written = %d, want 1: %v", len(cards), cards)
	}
	raw, err := os.ReadFile(cards[0])
	if err != nil {
		t.Fatal(err)
	}
	if line := strings.SplitN(string(raw), "\n", 2)[0]; !strings.HasPrefix(line, "RESULT: CARD-1 nova-tools PR #11 rebased onto dev") {
		t.Errorf("card line 1 = %q", line)
	}
	if len(l.launcher.cards) != 1 || l.launcher.cards[0] != cards[0] {
		t.Errorf("launched = %v, want [%s]", l.launcher.cards, cards[0])
	}

	// The second pass: the marker is the whole memory, so nothing is cut and nothing is
	// launched twice.
	exit, stdout, stderr = l.run(args...)
	if exit != 0 {
		t.Fatalf("second rebase: exit %d\nstderr=%s", exit, stderr)
	}
	if stdout != "REBASE tick cards=0\n" {
		t.Errorf("second pass stdout = %q, want %q", stdout, "REBASE tick cards=0\n")
	}
	if again, _ := filepath.Glob(filepath.Join(out, "card-*.md")); len(again) != 1 {
		t.Errorf("second pass wrote a duplicate card: %v", again)
	}
	if len(l.launcher.cards) != 1 {
		t.Errorf("second pass launched again: %v", l.launcher.cards)
	}
}
