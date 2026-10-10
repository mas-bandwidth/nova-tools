//go:build darwin

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rule 1: a machine with no backend REFUSES, and the command does not run. The seam is
// the `available` variable, because the behaviour under test is the refusal, not the
// platform — on a Mac sandbox-exec is always there, so without the seam this rule has
// no test at all on the only platform whose body is built.
func TestNoSandboxRefusesOnDarwin(t *testing.T) {
	t.Parallel()

	write := t.TempDir()
	home := filepath.Join(write, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	p, bad := Build(in(t, write, t.TempDir(), home, "/bin/echo"))
	require.Empty(t, bad, "refused at build: %v", bad)
	p.Available = func() (string, bool) { return "", false }
	var out, errb bytes.Buffer
	code, err := Run(p, os.Environ(), strings.NewReader(""), &out, &errb, nil)
	assert.Equal(t, ExitRefused, code, "exit %d, want %d", code, ExitRefused)
	var r Refusal
	ok := asRef(err, &r)
	require.True(t, ok, "err = %v, want a no_sandbox refusal", err)
	require.Equal(t, "no_sandbox", r.Reason, "err = %v, want a no_sandbox refusal", err)
	assert.Zero(t, out.Len(), "the command produced output; it must not have run: %q", out.String())
}

func asRef(err error, out *Refusal) bool {
	r, ok := err.(Refusal)
	if ok {
		*out = r
	}
	return ok
}

// Revision 7, test 20 rewritten: the wrap writes NO profile file. The first --write holds
// no .nova-sandbox-*.sb at any point, because the text goes to sandbox-exec with -p.
func TestNoProfileFileIsWritten(t *testing.T) {
	t.Parallel()

	write := t.TempDir()
	home := filepath.Join(write, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	p, bad := Build(in(t, write, t.TempDir(), home, "/bin/echo"))
	require.Empty(t, bad, "refused: %v", bad)
	var out, errb bytes.Buffer
	_, err := Run(p, ChildEnv(append(os.Environ(), "HOME="+home), p.Tmp), strings.NewReader(""), &out, &errb, nil)
	require.NoError(t, err, "run: %v (%s)", err, errb.String())
	entries, err := os.ReadDir(write)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), profileFilePrefx) && strings.HasSuffix(e.Name(), ".sb"), "the wrap left a profile file in the write set: %s", e.Name())
	}
}
