package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1451 asks that an invocation this tool cannot run print, on the refusal
// line itself, what was wrong AND the door to the usage. cmd/nova-wake has
// three refusal printers: refuse and awakeRefused carry the door, and refused
// is the one without one. This call site -- an --advance-cursor invocation
// that names neither --remote nor --branch -- is a plain invocation refusal
// wearing the world-refusal shape, so the narrowest true fix is the message
// string rather than the printer: refused's other call sites really are world
// refusals, and docs/SPEC-WAKE.md:863/:874 quote one of those lines verbatim.
// The existing door test enumerates BARE verbs read from the help banner, so
// it never reaches a four-flag invocation and cannot see this site. The
// sibling site at main.go:791 is unreachable -- the required-flag collector at
// :757-767 and the return at :786 answer first with the generic printer, which
// already carries the door -- so it has no test here and its string is
// corrected only for consistency.
func TestTheAdvanceCursorRefusalNamesTheDoor(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(src)
	fn := strings.Index(text, "func refuse(")
	if fn < 0 {
		t.Fatal("func refuse( not found in main.go; the door literal cannot be read")
	}
	start := strings.Index(text[fn:], "; run: nova-wake ")
	if start < 0 {
		t.Fatal("the door literal was not found after func refuse( in main.go")
	}
	start += fn
	rest := text[start:]
	end := strings.Index(rest, `\n`)
	if end < 0 {
		t.Fatal(`the closing \n of the door format string was not found in main.go`)
	}
	door := rest[:end]

	state := filepath.Join(t.TempDir(), "wake.state")
	r := wakeRun(t, "watch", "--state", state, "--max", "5s", "--on-deadline", "report",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
		"--advance-cursor")

	if r.exit != 2 {
		t.Errorf("exit = %d, want 2:\n%s", r.exit, r.all())
	}

	lines := strings.Split(strings.TrimSuffix(r.stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("stderr = %d lines, want exactly 1:\n%s", len(lines), r.stderr)
	}
	line := lines[0]
	if !strings.HasPrefix(line, "WAKE REFUSED: ") {
		t.Errorf("refusal line does not begin %q: %q", "WAKE REFUSED: ", line)
	}
	if !strings.Contains(line, "--remote") {
		t.Errorf("refusal line no longer says what was wrong (--remote): %q", line)
	}
	if !strings.HasSuffix(line, door) {
		t.Errorf("refusal line does not end in the door %q: %q", door, line)
	}
}
