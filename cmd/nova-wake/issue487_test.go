package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// #487 asked for two things, and only the second is still open. The first --
// awake reading max= from the config file -- is already true on dev, so this
// test leaves it alone. The defect is loadWakeConfig: it keeps every k=v line
// it can split and never asks whether k is a key this tool reads, so a typo
// like maxx=1 is read as silence and the verb runs on defaults.
//
// The refusal belongs at the verb, not inside loadWakeConfig, because help
// and version read the same file and a person with a broken config is exactly
// the person who needs to read help. So the check runs only for the verbs
// that consume the config's values.

func TestIssue487AnUnknownConfigKeyIsRefusedNotIgnored(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "config")
	write(t, bad, "bus="+awakeBus(t)+"\nmaxx=1\n")
	t.Setenv("NOVA_WAKE_CONFIG", bad)

	t.Run("awake refuses and names the key, file and line", func(t *testing.T) {
		r := wakeRun(t, "awake")
		if r.exit != 2 {
			t.Fatalf("awake exit %d, want 2; stderr=%s", r.exit, r.stderr)
		}
		for _, want := range []string{"maxx", bad, "line 2", "; run: nova-wake help"} {
			if !strings.Contains(r.stderr, want) {
				t.Fatalf("awake refusal missing %q\nstderr=%s", want, r.stderr)
			}
		}
	})

	t.Run("watch refuses the same config", func(t *testing.T) {
		r := wakeRun(t, "watch")
		if r.exit != 2 {
			t.Fatalf("watch exit %d, want 2; stderr=%s", r.exit, r.stderr)
		}
		if !strings.Contains(r.stderr, "maxx") {
			t.Fatalf("watch refusal did not name the key\nstderr=%s", r.stderr)
		}
	})

	t.Run("a config of known keys still runs", func(t *testing.T) {
		good := filepath.Join(t.TempDir(), "config")
		write(t, good, "bus="+awakeBus(t)+"\nwindow=300\n")
		t.Setenv("NOVA_WAKE_CONFIG", good)
		r := wakeRun(t, "awake")
		if r.exit != 0 {
			t.Fatalf("awake exit %d, want 0 with only known keys; stderr=%s", r.exit, r.stderr)
		}
	})

	t.Run("help still works with a bad config", func(t *testing.T) {
		t.Setenv("NOVA_WAKE_CONFIG", bad)
		r := wakeRun(t, "help")
		if r.exit != 0 {
			t.Fatalf("help exit %d, want 0 with a bad config; stderr=%s", r.exit, r.stderr)
		}
	})
}
