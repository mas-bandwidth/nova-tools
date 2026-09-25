package main

import (
	"strings"
	"testing"

	unusedverbs "github.com/mas-bandwidth/nova-tools/internal/nsprint/verbs"
)

// verbs is registered: a bad flag is refused with the help door, and help
// lists the verb with its summary.
func TestVerbsRegistered(t *testing.T) {
	code, stdout, stderr := runSprint("verbs", "unused", "--bogus")
	if code != 2 || stdout != "" || !strings.HasSuffix(stderr, "; run: nova-sprint help\n") {
		t.Fatalf("verbs unused --bogus: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runSprint("help")
	if code != 0 || !strings.Contains(stdout, "  verbs  "+unusedverbs.VerbSummary) {
		t.Fatalf("help: exit %d stderr %q; no verbs line in\n%s", code, stderr, stdout)
	}
}
