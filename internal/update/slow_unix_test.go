//go:build slow && unix

package update

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// SLOW: 6.0 s on hetzner at dev 64b9bec48, over the five-second line.
// TestSnapshotToleratesTheFirstExecOfANeverSeenBinary models a platform that
// assesses a newly installed executable on its first run. The fixture is slow
// on its first invocation and immediate thereafter. It must complete within
// snapshot's deadline without an unbounded warm-up invocation.
func TestSnapshotToleratesTheFirstExecOfANeverSeenBinary(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	toll := filepath.Join(t.TempDir(), "assessed")
	specScript(t, bin, "nova-toll",
		"if [ ! -f '"+toll+"' ]; then : > '"+toll+"'; sleep 6; fi\n"+
			"printf '%s\\n' 'nova-toll v1.0.0 linux/amd64 go1.0'")
	out := filepath.Join(t.TempDir(), "s.tsv")
	code, stdout, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	require.EqualValuesf(t, 0, code, "a binary costing six seconds on its first exec and nothing after was refused: exit %d stderr=%s", code, stderr)
	need(t, stdout, "SNAPSHOT OK", "tools=1", "stamp=v1.0.0")
}
