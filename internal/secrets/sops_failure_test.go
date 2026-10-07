package secrets

// A wrong key must name its cause. sops writes the class to stderr and exits 128, and
// DecryptFile used to discard the transcript and print only "sops failed: exit 128". The
// fake below is the subprocess seam: it returns the stderr the real sops writes, typed so
// the decrypt can name the class without echoing a transcript that may hold a value.

import (
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSopsStderr is one scripted sops -d failure: the exit code and the stderr the real
// binary wrote. It is what the subprocess seam carries to the decrypt.
type fakeSopsStderr struct {
	code   int
	stderr string
}

func (e fakeSopsStderr) Error() string  { return fmt.Sprintf("exit status %d", e.code) }
func (e fakeSopsStderr) ExitCode() int  { return e.code }
func (e fakeSopsStderr) Stderr() string { return e.stderr }

// fakeSopsDecrypt is the seam's fake: every sops child fails with err.
func fakeSopsDecrypt(err error) execCommand {
	return func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
		return nil, err
	}
}

// TestDecryptFileNamesTheSopsFailureClass pins a wrong key giving its cause: the class
// sops's stderr names (no matching key, an absent identity file, a file that is not sops
// metadata, a file no creation rule names) comes back with the path to check and a one-turn
// remedy, and the transcript itself -- which can hold a value -- never does.
func TestDecryptFileNamesTheSopsFailureClass(t *testing.T) {
	t.Parallel()
	const keyPath = "/keys/rowan.key"
	const filePath = "/store/rowan.yaml"
	for _, tc := range []struct {
		name   string
		stderr string
		want   []string
	}{
		{
			name:   "a key that matches none of the recipients",
			stderr: "no identity matched any of the recipients\n",
			want:   []string{"sops failed", filePath, "matches none of the recipients", "check the key path", "nova-secrets keygen"},
		},
		{
			name:   "a key file that is absent or unreadable",
			stderr: "no identity file\n",
			want:   []string{"sops failed", keyPath, "absent or unreadable", "nova-secrets keygen"},
		},
		{
			name:   "a file that carries no sops metadata",
			stderr: "sops metadata not found in " + filePath + "\n",
			want:   []string{"sops failed", filePath, "not a sops-encrypted file", "nova-secrets seal"},
		},
		{
			name:   "a file no creation rule names",
			stderr: "error: no matching creation rules found\n",
			want:   []string{"sops failed", filePath, "no creation rule", "sops updatekeys"},
		},
		{
			name:   "an unknown transcript keeps the inspect remedy and is never echoed",
			stderr: "Fatal error: sk-live-value\n",
			want:   []string{"sops failed", filePath, "transcript withheld", "sops -d"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecryptFile(fakeSopsDecrypt(fakeSopsStderr{code: 128, stderr: tc.stderr}), "/no/such/sops", keyPath, filePath)
			require.Error(t, err)
			for _, w := range tc.want {
				assert.Contains(t, err.Error(), w, "err = %q, want %q", err.Error(), w)
			}
			assert.NotContains(t, err.Error(), "sk-live-value", "the transcript reached the error: %q", err.Error())
		})
	}
}
