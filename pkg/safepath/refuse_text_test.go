package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefuseUnsafeTextsStayByteForByte pins the exact refusal sentences of
// refuseUnsafeRoot and refuseUnsafePath. Folding the two into refuseUnsafe (noun, copula)
// must not change a single byte a caller reads back: each sentence here is one door's
// contract, and the shared helper reproduces it through the noun and copula it is handed.
func TestRefuseUnsafeTextsStayByteForByte(t *testing.T) {
	t.Parallel()

	fakeHome := t.TempDir()
	real, err := filepath.EvalSymlinks(fakeHome)
	require.NoError(t, err, "could not resolve the fixture's fake home: %v", err)
	policy := Policy{UserHomeDir: func() (string, error) { return real, nil }}
	sep := string(os.PathSeparator)

	for _, c := range []struct {
		name string
		call func() error
		want string
	}{
		{
			name: "root is the whole disk",
			call: func() error { return policy.refuseUnsafeRoot(sep) },
			want: fmt.Sprintf("refusing to remove an unsafe path: the root is %q, the whole disk", sep),
		},
		{
			name: "root is the user's home",
			call: func() error { return policy.refuseUnsafeRoot(real) },
			want: fmt.Sprintf("refusing to remove an unsafe path: the root is the user's home %q", real),
		},
		{
			name: "path resolves to the whole disk",
			call: func() error { return policy.refuseUnsafePath(sep) },
			want: fmt.Sprintf("refusing to remove an unsafe path: the path resolves to %q, the whole disk", sep),
		},
		{
			name: "path resolves to the user's home",
			call: func() error { return policy.refuseUnsafePath(real) },
			want: fmt.Sprintf("refusing to remove an unsafe path: the path resolves to the user's home %q", real),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := c.call()
			require.Error(t, err)
			assert.EqualError(t, err, c.want)
		})
	}
}
