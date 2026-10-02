//go:build slow && unix

package update

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// SLOW: 6.0 s on hetzner at dev 64b9bec48, over the five-second line.
// 12. TestSnapshotToleratesTheFirstExecOfANeverSeenBinary.
//
// EVERY BINARY `snapshot` READS IS, BY CONSTRUCTION, ONE THIS MACHINE HAS NEVER
// EXECUTED. The documented sequence is `go install ./cmd/...` and then
// `nova-version snapshot` (docs/RELEASE-NOTES-1.0.0.md, "Install"), so the
// per-binary bound is charged for the platform's one-time assessment of a
// never-seen executable on every row of every run -- not as an edge case but as
// the verb's normal case. That is why `seen()` above, which pays the toll
// outside the bound for the tests that probe already-installed tools, is not
// the answer here: there is no "already" for this verb.
//
// The fixture is that toll made deterministic: slow on its FIRST invocation and
// immediate on every one after. Measured on the darwin/arm64 Studio over fresh
// `#!/bin/sh` fixtures of exactly this shape: cold 164-571 ms and warm 5 ms at
// load 121-151 on 32 cores; cold 140 ms median with a 7.03 s maximum and warm
// 5.3 ms while the tree compiled beside it -- which is the state `go install
// ./cmd/...` leaves the machine in one command before the snapshot (#890).
func TestSnapshotToleratesTheFirstExecOfANeverSeenBinary(t *testing.T) {
	t.Parallel()

	bin := t.TempDir()
	toll := filepath.Join(t.TempDir(), "assessed")
	specScript(t, bin, "nova-toll",
		"if [ ! -f '"+toll+"' ]; then : > '"+toll+"'; sleep 6; fi\n"+
			"printf '%s\\n' 'nova-toll v1.0.0 linux/amd64 go1.0'")
	out := filepath.Join(t.TempDir(), "s.tsv")
	code, stdout, stderr := specRun(t, Environment{}, "snapshot", "--bin", bin, "--out", out)
	if code != 0 {
		require.EqualValuesf(t, 0, code, "a binary costing six seconds on its first exec and nothing after was refused: exit %d stderr=%s", code, stderr)
	}
	need(t, stdout, "SNAPSHOT OK", "tools=1", "stamp=v1.0.0")
}
