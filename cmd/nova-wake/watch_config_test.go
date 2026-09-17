package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The config file a line keeps for its repeated flags (docs/SPEC-WAKE.md, "A
// config file for the repeated flags") carries its defaults, so a bare watch
// reads --on-deadline and --receipt-max-words instead of typing them every
// call. The refusal stays: with neither the flag nor the file, the same
// refusal names the config file as a second remedy.

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

func TestWatchReadsReceiptMaxWordsFromConfig(t *testing.T) {
	rowan, _ := synthBus(t)
	state := filepath.Join(t.TempDir(), "wake.state")
	cfgPath := filepath.Join(t.TempDir(), "config")
	write(t, cfgPath, "bus="+rowan+"\nas=Rowan\nstate="+state+"\nreceipt-max-words=40\n")
	t.Setenv("NOVA_WAKE_CONFIG", cfgPath)

	r := wakeRun(t, "watch", "--max", "5s", "--on-deadline", "report", "--interval", "5s", "--baseline")
	if r.exit != 0 {
		t.Fatalf("watch exit %d, want 0 (--receipt-max-words should come from config); stderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(r.stdout, "nova-bus=") {
		t.Fatalf("bus not watched from config\nstdout=%s", r.stdout)
	}
}
