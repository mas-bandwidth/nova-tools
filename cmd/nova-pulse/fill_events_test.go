package main

// The fill verb's own edge on the structured stream: the sink is opened BEFORE the loop
// starts, so a --log nobody can write is a refusal before a single ssh rather than a tick
// whose lines went nowhere. The tick's own lines are asserted in internal/pulse, where the
// capacity and the launcher are injected seams and no test opens a connection.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFillRefusesALogItCannotOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := cmdFill([]string{
		"--ready", filepath.Join(dir, "ready"),
		"--launched", filepath.Join(dir, "launched"),
		"--log", filepath.Join(dir, "no-such-directory", "nova-events.log"),
		"--once",
	}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("a --log that cannot be opened is exit 2, got %d; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--log") {
		t.Fatalf("the refusal does not name --log: %q", errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("a refused run wrote a verdict on stdout: %q", out.String())
	}
}
