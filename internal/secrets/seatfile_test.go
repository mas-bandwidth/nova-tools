package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
			"sops failed: exit 128, transcript withheld (it may quote the file); to see it, run: SOPS_AGE_KEY_FILE=" + s.key + " " + sharedSopsOpensNone + " -d " + file},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecryptFile(tc.sops, tc.key, file)
			assert.EqualError(t, err, tc.want)
		})
	}
}

// The remedy is run, so its words must be the words meant: a key path or a
// file path holding a blank or an apostrophe reaches sops as that one path when
// the suggested command runs through sh, and a program path holding = (which
// stands after an assignment) runs as the program, not as another assignment.
func TestTheDecryptRemedyRunsAsWritten(t *testing.T) {
	t.Parallel()
	skipPOSIXFakesOnWindows(t)

	for _, tc := range []struct{ name, dir, sops string }{
		{"a key path with a space", "keys with space", sharedSopsEchoes},
		{"a key path with an apostrophe", "bo's keys", sharedSopsEchoes},
		{"a program path holding =", "keys", "S=x/sops"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, tc.dir)
			require.NoError(t, os.MkdirAll(root, 0o700))
			require.NoError(t, os.MkdirAll(filepath.Join(base, "S=x"), 0o755))
			require.NoError(t, os.Symlink(sharedSopsEchoes, filepath.Join(base, "S=x", "sops")))
			key := filepath.Join(root, "rowan.key")
			require.NoError(t, os.WriteFile(key, []byte("AGE-SECRET-KEY-1ROWAN\n# public key: "+pubRowan+"\n"), 0o600))
			file := filepath.Join(root, "rowan.yaml")
			require.NoError(t, os.WriteFile(file, []byte(sealedFor([]string{pubRowan, pubRecovery})), 0o644))

			_, err := DecryptFile(tc.sops, key, file)
			require.Error(t, err)
			_, remedy, found := strings.Cut(err.Error(), "; to see it, run: ")
			require.True(t, found, "no remedy in %q", err)
			sh := exec.Command("sh", "-c", remedy)
			sh.Dir = base
			out, _ := sh.Output() // ignored: the fake exits 128 by design; its stdout is the check
			assert.Equal(t, "key="+key+"\nfile="+file+"\n", string(out), "the remedy %q", remedy)
		})
	}
}
