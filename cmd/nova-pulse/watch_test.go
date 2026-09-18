package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// SPEC-PULSE.md's "## Watch" section prints the verb line "as it will appear in help", so
// the tool's help carries it byte for byte.
func TestHelpHasTheWatchVerbLine(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d, stderr=%s", code, errb.String())
	}
	line := "nova-pulse watch --queue <dir> --bus <dir> --jobs <root> --until <event> --cap <duration>"
	if !strings.Contains(out.String(), line) {
		t.Fatalf("help does not print the watch verb line %q:\n%s", line, out.String())
	}
}

// The watch verb is wired, and a missing flag is its own refusal -- not the flag set's --
// because the spec fixes the message.
func TestWatchRefusesAMissingQueue(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"watch"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("watch with no flags exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "WATCH REFUSED: refusing to guess (queue is required)") {
		t.Fatalf("stderr=%q, want the missing --queue refusal", errb.String())
	}
}
