package secrets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGateRefusalNamesTheCheck verifies that gate refusals include the check number that
// failed, named `check=<k>` between `rule=` and `file=`, so a reader knows which rule the
// gate checked when a refusal is printed.
func TestGateRefusalNamesTheCheck(t *testing.T) {
	t.Parallel()

	// Check 2: Seat file's encryption and its rule. A seat file in the clear (plain value).
	t.Run("seat file with plain value is check 2", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)),
			"rowan.yaml": gateSealedFile() + "GH_TOKEN: sk-live-notencrypted\n",
		})
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		require.Equal(t, 1, code, line)
		assert.Contains(t, line, "GATE FAILED rule=1 check=2 file=rowan.yaml:", line)
	})

	// Check 3: No other file changes. A stray file (not .sops.yaml, not README.md, not a seat file).
	t.Run("stray file is check 3", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)),
			"rowan.yaml": gateSealedFile(),
			"notes.txt":  "a stray file\n",
		})
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		require.Equal(t, 1, code, line)
		assert.Contains(t, line, "GATE FAILED rule=0 check=3 file=notes.txt:", line)
	})
}
