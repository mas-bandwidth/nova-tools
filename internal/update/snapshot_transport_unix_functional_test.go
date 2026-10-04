//go:build unix && functional

package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The real adapter must stop a hung version child; the unit fake separately
// checks which deadline is propagated and how the refusal is classified.
func TestSnapshotRealChildTimeoutAndBudget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"child timeout", []string{"--timeout", "20ms"}, "20ms"},
		{"run budget", []string{"--timeout", "30s", "--budget", "30ms"}, "budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			specScript(t, bin, "nova-slow", "sleep 30\nprintf 'nova-slow v1.0.0 linux/amd64 go1.0\\n'")
			out := filepath.Join(t.TempDir(), "s.tsv")
			args := append([]string{"snapshot", "--bin", bin, "--out", out}, tc.args...)
			code, _, stderr := specRun(t, Environment{}, args...)
			require.Equal(t, 2, code, "exit %d stderr=%s", code, stderr)
			need(t, stderr, tc.want)
			_, err := os.Stat(out)
			require.Error(t, err, "a partial --out was written")
		})
	}
}
