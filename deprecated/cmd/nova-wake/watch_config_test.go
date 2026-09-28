package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchReadsOnDeadlineFromConfig(t *testing.T) {
	state := filepath.Join(t.TempDir(), "wake.state")
	cfgPath := filepath.Join(t.TempDir(), "config")
	write(t, cfgPath, "on-deadline=report\n")
	t.Setenv("NOVA_WAKE_CONFIG", cfgPath)

	r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--interval", "5s",
		"--reports", exampleReports, "--baseline")
	if r.exit != 0 {
		t.Fatalf("watch exit %d, want 0 (--on-deadline should come from config); stderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(r.stdout, "on-deadline=report") {
		t.Fatalf("config on-deadline not read\nstdout=%s", r.stdout)
	}
}
