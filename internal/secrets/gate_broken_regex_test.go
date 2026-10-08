package secrets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A broken unencrypted_regex is the rule's own defect, so it is refused as one:
// plainValues returns the regexp.Compile error instead of discarding it, and the
// gate's line names the invalid unencrypted_regex rather than reporting a clear key as
// a plain value and sending the reader to fix the wrong thing. Fail-closed as before
// (exit 2, nothing judged against a regex that does not compile); the valid-regex path
// is unchanged.
func TestPlainValuesNamesABrokenRegex(t *testing.T) {
	t.Parallel()
	const broken = `^(space_user|`
	t.Run("plainValues returns the compile error naming the broken regex", func(t *testing.T) {
		t.Parallel()
		keys, err := plainValues([]byte("space_user: rowan\n"), broken)
		require.Error(t, err)
		assert.Contains(t, err.Error(), broken)
		assert.Empty(t, keys, "a broken regex still judges no value plain or permitted")
	})
	t.Run("the valid-regex path is unchanged", func(t *testing.T) {
		t.Parallel()
		keys, err := plainValues([]byte("space_user: rowan\n"), `^space_user$`)
		require.NoError(t, err)
		assert.Empty(t, keys, "a key the regex permits in the clear is not reported plain")
		keys, err = plainValues([]byte("GH_TOKEN: sk-live\n"), `^space_user$`)
		require.NoError(t, err)
		assert.Equal(t, []string{"GH_TOKEN"}, keys, "a key outside the regex is still reported plain")
	})
	t.Run("the gate refuses the seat on the line naming the invalid unencrypted_regex", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": gateSops(gateRule("rowan.yaml", gateSeatKey, gateRecoveryKey) + "    unencrypted_regex: " + broken + "\n"),
			"rowan.yaml": gateSealedFile() + "space_user: rowan\n",
		})
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		require.Equal(t, 1, code, "RunGate = (%q, %d), want exit 1 (verdict FAILED)", line, code)
		assert.Equal(t, "GATE FAILED rule=1 check=2 file=rowan.yaml: unencrypted_regex \""+broken+"\" is not a valid regular expression", line)
	})
}
