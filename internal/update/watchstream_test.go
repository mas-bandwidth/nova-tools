package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWatchKeepsItsClosingCountOnStdout pins the closing count line's stream: a
// refused check's findings go to stderr, but ADOPT DONE is the pass's closing
// count and stays on stdout (skeleton contract 1.6), so a caller reading stdout
// sees the sha and the counts of a pass that ran.
func TestWatchKeepsItsClosingCountOnStdout(t *testing.T) {
	t.Parallel()

	checks := filepath.Join(t.TempDir(), "checks.tsv")
	rows := []string{
		"check\tcommand\towner",
		"versions-agree\t" + printer(t, "1.0.0") + "\towner",
		"snapshot-report\t" + command(t, "fail") + "\towner",
	}
	require.NoError(t, os.WriteFile(checks, []byte(strings.Join(rows, "\n")+"\n"), 0o600))
	c, out, errs := run(t, Environment{}, "watch", "--adopt", checks)
	require.EqualValuesf(t, 1, c, "want exit 1 with one refusal, got %d:\nstdout:\n%s\nstderr:\n%s", c, out, errs)
	assert.Contains(t, out, "ADOPT DONE sha=", "the closing count line is not on stdout:\nstdout:\n%s\nstderr:\n%s", out, errs)
	assert.Contains(t, errs, "ADOPT REFUSED check=snapshot-report", "the refusal is not on stderr:\nstdout:\n%s\nstderr:\n%s", out, errs)
	assert.NotContains(t, errs, "ADOPT DONE", "the closing count line is on stderr:\n%s", errs)
	assert.NotContains(t, out, "ADOPT REFUSED check=snapshot-report", "the refusal is on stdout:\n%s", out)
}
