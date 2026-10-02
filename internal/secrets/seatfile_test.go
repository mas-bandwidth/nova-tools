package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A decrypt that fails names its cause where it can: a --key whose public half
// is not among the seat file's recipients is said to be that, with the
// recipients it would need, rather than a bare `sops failed: exit 128`; a key
// that is a recipient and still fails keeps the withheld transcript, with a
// remedy that reproduces the decrypt (the key's SOPS_AGE_KEY_FILE set), since a
// bare `sops -d` fails for another reason (no identity at all).
func TestADecryptThatFailsNamesItsCause(t *testing.T) {
	t.Parallel()

	s := newCheckStore(t, nil)
	file := filepath.Join(s.dir, "rowan.yaml")
	stranger := filepath.Join(t.TempDir(), "stranger.key")
	require.NoError(t, os.WriteFile(stranger, []byte("AGE-SECRET-KEY-1STRANGER\n# public key: "+pubStranger+"\n"), 0o600))
	for _, tc := range []struct {
		name, key, sops, want string
	}{
		{"a key that is not a recipient", stranger, s.sops,
			"sops failed: exit 128: --key " + stranger + " is " + pubStranger + ", not a recipient of " + file +
				" (its recipients: " + pubRowan + ", " + pubRecovery + "); pass --key the private key of one of them"},
		{"a recipient's key that still fails", s.key, sharedSopsOpensNone,
			"sops failed: exit 128 (transcript withheld: run 'SOPS_AGE_KEY_FILE=" + s.key + " " + sharedSopsOpensNone + " -d " + file + "' to inspect)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecryptFile(tc.sops, tc.key, file)
			assert.EqualError(t, err, tc.want)
		})
	}
}
