package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDashHPrintsHelpAtExitZero pins the tool standard's help rule (docs/STANDARD.md,
// "Help is never a refusal") for a tool whose exit 0 means CLEAR: `<verb> -h` and
// `<verb> --help` print that verb's help on stdout at exit 0 and read no box, exactly
// as `<verb> help <verb>` does. This tool refused -h at exit 2 by design, so a reader
// taught that -h always prints help met a refusal instead; the refusal is gone and
// this pins what replaced it. A help branch that answers REFUSED is the defect
// tools/analyzers/cmd/vetlaw's checkHelpRefused names (law #2575).
func TestDashHPrintsHelpAtExitZero(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		verb  string
		usage string
	}{
		{"check", "usage: nova-fuse check --box <path> [--json] [--] [surface]"},
		{"init", "usage: nova-fuse init --box <path> [--dry-run]"},
	} {
		t.Run(row.verb, func(t *testing.T) {
			t.Parallel()
			for _, flag := range []string{"-h", "--help"} {
				code, out, errOut := capture(t, []string{row.verb, flag}, nowish())
				require.Equal(t, 0, code, "`nova-fuse %s %s`: exit = %d, want 0\nstdout: %q\nstderr: %q", row.verb, flag, code, out, errOut)
				assert.Empty(t, errOut, "`nova-fuse %s %s`: a help answer writes nothing to stderr: %q", row.verb, flag, errOut)
				assert.True(t, strings.HasPrefix(out, row.usage), "`nova-fuse %s %s`: stdout = %q, want it to open the verb's usage line %q", row.verb, flag, out, row.usage)
			}
		})
	}
}
