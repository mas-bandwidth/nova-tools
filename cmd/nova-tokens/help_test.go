package main

import (
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

// TestHelpListsOneSumForm pins that the bare help banner carries the one `sum` form, the
// --out/--month report, on its own synopsis line (#3464: a bare continuation once made two
// calls read as one).
func TestHelpListsOneSumForm(t *testing.T) {
	t.Parallel()

	r := invoke(t, "help")
	wantExit(t, r, 0)
	wantContains(t, r.stdout, "--out <dir> --month <YYYY-MM>")

	// Cut the `usage:` block, which ends at the first blank line, so the pasteable
	// example lines below it are not mistaken for synopsis lines.
	inUsage := false
	n := 0
	for _, line := range strings.Split(r.stdout, "\n") {
		switch {
		case line == "usage:":
			inUsage = true
			continue
		case inUsage && line == "":
			inUsage = false
		case inUsage && strings.HasPrefix(line, "  nova-tokens sum "):
			n++
		}
	}
	assert.False(t, n != 1, "the help banner's usage block carries %d `nova-tokens sum` synopsis lines, want 1:\n%s", n, r.stdout)
}
