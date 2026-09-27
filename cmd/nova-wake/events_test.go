package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

func TestAStopIsAVerdict(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "wake.state")
	lockPath := filepath.Join(dir, "lane.lock")
	write(t, lockPath, "")
	r := wakeRunStopped(t, "watch", "--state", state, "--max", "20m",
		"--on-deadline", "report", "--interval", "5s", "--lock", lockPath)
	last := lastLine(r.stdout)
	if !strings.HasPrefix(last, "WAKE STOPPED ") || !strings.Contains(last, "stopped by the caller") {
		t.Errorf("a stop is the last line and the fourth verdict:\n%s", r.stdout)
	}
	if r.exit != 0 {
		t.Errorf("exit %d, want 0: the stop was the caller's decision", r.exit)
	}
	for _, field := range []string{"after=", "polls=", "pending="} {
		if !strings.Contains(last, field) {
			t.Errorf("WAKE STOPPED carries %s: %s", field, last)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(state)
	for _, e := range entries {
		name := e.Name()
		if name == filepath.Base(lockPath) || name == base ||
			name == filepath.Base(wake.TempName(state)) || name == filepath.Base(wake.LockName(state)) {
			continue
		}
		t.Errorf("the only paths afterwards are --state, its fixed-name temp file and <state>.lock; found %s", name)
	}
	// <state>.lock is free after the exit: a second watch over the same path runs.
	again := wakeRun(t, "watch", "--state", state, "--max", "5s",
		"--on-deadline", "report", "--interval", "5s", "--lock", lockPath)
	if again.exit != 0 {
		t.Errorf("the lock was not released with the process: exit %d\n%s", again.exit, again.all())
	}
}
