package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGateSecurityAuditFindings(t *testing.T) {
	t.Parallel()

	// Finding 1: Widened unencrypted_regex in .sops.yaml
	t.Run("Widened unencrypted_regex", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)

		baseContent := map[string]string{
			".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey) + "    unencrypted_regex: '^(GH_TOKEN)$'\n"),
			"rowan.yaml": gateSealedFile(),
		}
		base := gateCommit(t, dir, baseContent)

		headContent := map[string]string{
			".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey) + "    unencrypted_regex: '^(.*)$'\n"),
			"rowan.yaml": "GH_TOKEN: my-token\n" + gateMarkLine,
		}
		head := gateCommit(t, dir, headContent)

		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		assert.Equal(t, 1, code, "Should fail gate: %s", line)
	})

	// Finding 2: Nested seat file in sub/evil.yaml
	t.Run("Nested seat file", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)

		// Need a valid seat to have a rule
		baseContent := map[string]string{
			".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey)),
			"rowan.yaml": gateSealedFile(),
		}
		base := gateCommit(t, dir, baseContent)

		// Subdirectory file
		subDir := filepath.Join(dir, "sub")
		require.NoError(t, os.Mkdir(subDir, 0755))

		// Need a commit to add sub/evil.yaml
		head := gateCommit(t, dir, map[string]string{
			"sub/evil.yaml": "GH_TOKEN: my-token\n" + gateMarkLine,
		})

		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		assert.Equal(t, 1, code, "Should fail gate")
		assert.Contains(t, line, "only .sops.yaml, README.md and seat .yaml files may change", "Should refuse sub/evil.yaml")
	})

	// Finding 3: Cleartext under indented map key
	t.Run("Cleartext under indented map key", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)

		baseContent := map[string]string{
			".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey) + "    unencrypted_regex: '^(GH_TOKEN)$'\n"),
			"rowan.yaml": gateSealedFile(),
		}
		base := gateCommit(t, dir, baseContent)

		headContent := map[string]string{
			".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey) + "    unencrypted_regex: '^(GH_TOKEN)$'\n"),
			"rowan.yaml": "key:\n  GH_TOKEN: my-token\n" + gateMarkLine,
		}
		head := gateCommit(t, dir, headContent)

		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		assert.Equal(t, 1, code, "Should fail gate")
		assert.True(t, strings.Contains(line, "GH_TOKEN is a plain value") || strings.Contains(line, "missing sops metadata"), "Should fail on cleartext GH_TOKEN or missing metadata")
	})
}
