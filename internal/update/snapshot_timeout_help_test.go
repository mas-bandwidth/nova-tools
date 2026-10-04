package update

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSnapshotHelpStatesShapeTimeouts pins the snapshot --timeout help to the
// two real shape defaults: the --bin shape reads one binary under 30s
// (snapshotChildTimeout, SPEC-VERSION rule 11), and the --file shape probes one
// adopted tool under 5s (snapshotAdoptedTimeout, SPEC-UPDATE) unless the caller
// gives --timeout. One unqualified deadline line describes neither shape
// truthfully (docs/ratings/snapshots/0c5803c2de40/version-read.md finding 4).
func TestSnapshotHelpStatesShapeTimeouts(t *testing.T) {
	t.Parallel()

	var out, errs bytes.Buffer
	code := Run("nova-version", []string{"snapshot", "-h"}, "", &out, &errs, Environment{})
	require.EqualValuesf(t, 0, code, "snapshot -h must answer at exit 0:\n%s", errs.String())

	line := timeoutHelpLine(out.String())
	require.NotEmptyf(t, line, "snapshot -h names no --timeout flag:\n%s", out.String())
	assert.Containsf(t, line, "--bin", "the --timeout help must name the --bin shape:\n%s", line)
	assert.Containsf(t, line, "30s", "the --timeout help must state the --bin default of 30s:\n%s", line)
	assert.Containsf(t, line, "--file", "the --timeout help must name the --file shape:\n%s", line)
	assert.Containsf(t, line, "5s", "the --timeout help must state the --file default of 5s:\n%s", line)
}

// timeoutHelpLine is the rendered --timeout flag line from a verb's help.
func timeoutHelpLine(help string) string {
	for l := range strings.SplitSeq(help, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "--timeout") {
			return l
		}
	}
	return ""
}
