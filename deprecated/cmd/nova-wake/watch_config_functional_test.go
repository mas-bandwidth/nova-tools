//go:build functional

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

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
