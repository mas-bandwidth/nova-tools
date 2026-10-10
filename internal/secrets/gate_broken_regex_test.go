package secrets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstPlainValue returns the first root-level key whose value is not encrypted
// and not permitted in the clear by unencryptedRegex. An unencryptedRegex that does
// not compile is the rule's own defect: it is returned as an error, and nothing is
// judged against a regex that does not compile, so the gate refuses on the real cause
// instead of reporting a permitted key as a plain value.
func firstPlainValue(data []byte, unencryptedRegex string) (string, bool, error) {
	keys, err := plainValues(data, unencryptedRegex)
	if err != nil {
		return "", false, err
	}
	if len(keys) == 0 {
		return "", false, nil
	}
	return keys[0], true, nil
}

// A broken unencrypted_regex is the rule's own defect, so it is refused as one:
// firstPlainValue returns the regexp.Compile error instead of discarding it, and the
// gate's line names the invalid unencrypted_regex rather than reporting a clear key as
// a plain value and sending the reader to fix the wrong thing. Fail-closed as before
// (exit 2, nothing judged against a regex that does not compile); the valid-regex path
// is unchanged.
func TestFirstPlainValueNamesABrokenRegex(t *testing.T) {
	t.Parallel()
	const broken = `^(space_user|`
	t.Run("firstPlainValue returns the compile error naming the broken regex", func(t *testing.T) {
		t.Parallel()
		key, plain, err := firstPlainValue([]byte("space_user: rowan\n"), broken)
		require.Error(t, err)
		assert.Contains(t, err.Error(), broken)
		assert.False(t, plain, "a broken regex still judges no value plain or permitted")
		assert.Empty(t, key)
	})
	t.Run("the valid-regex path is unchanged", func(t *testing.T) {
		t.Parallel()
		_, plain, err := firstPlainValue([]byte("space_user: rowan\n"), `^space_user$`)
		require.NoError(t, err)
		assert.False(t, plain, "a key the regex permits in the clear is not reported plain")
		key, plain, err := firstPlainValue([]byte("GH_TOKEN: sk-live\n"), `^space_user$`)
		require.NoError(t, err)
		assert.True(t, plain, "a key outside the regex is still reported plain")
		assert.Equal(t, "GH_TOKEN", key)
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
