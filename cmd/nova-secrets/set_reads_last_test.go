package main

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reads is a stdin that counts how often it is read.
type reads struct{ n int }

func (r *reads) Read([]byte) (int, error) { r.n++; return 0, io.EOF }

// A set that could not finish refuses before it reads the value, on a node with a store and on a fresh one, and
// names the fix: the value is asked for once.
func TestSetRefusesBeforeReadingWhatItCouldNotFinish(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, want string
		breakIt    func(t *testing.T, h *harness, dir string)
	}{
		{"no commit identity", "SECRETS SET REFUSED reason=git: git has no commit identity, and set commits what it writes; nothing was read: run: git config --global user.name 'Your Name' && git config --global user.email you@example.com\n",
			func(_ *testing.T, h *harness, _ string) { h.noIdent = true }},
		{"no git", "SECRETS SET REFUSED reason=git: git not found on PATH; install it: ", func(_ *testing.T, h *harness, _ string) { h.noGit = true }},
		{"no sops", "SECRETS SET REFUSED reason=no-sops: sops not found at sops; install sops 3.x: ", func(_ *testing.T, h *harness, _ string) { h.sopsMissing = true }},
		{"no age-keygen", "SECRETS SET REFUSED reason=other: age-keygen not found on PATH; install age: ", func(_ *testing.T, h *harness, _ string) { h.noAgeKeygen = true }},
		{"read-only", " cannot be written, and the store is kept there: permission denied; run: ls -ld ", func(t *testing.T, h *harness, dir string) {
			if os.Getuid() == 0 {
				t.Skip("root writes a read-only directory")
			}
			require.NoError(t, os.Chmod(dir, 0o500))
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // ignored: only so the test's directory can be removed
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t) // a node with a store
			in := &reads{}
			h.in = in
			c.breakIt(t, h, h.store)
			assert.Equal(t, 2, h.main(h.set("NEW")...))
			assert.Contains(t, h.stderr.String(), c.want)
			assert.Zero(t, in.n, "the value was read before the refusal")
			assert.Empty(t, h.stdout.String())
			assert.NoFileExists(t, h.store+"/ada.yaml")

			h, dir := fresh(t) // a fresh node: nothing is made either
			h.in = in
			home := envMap(h.environ)["HOME"]
			c.breakIt(t, h, home)
			assert.Equal(t, 2, h.main("set", "TAILSCALE_API_KEY", "--sops", "sops"))
			assert.Contains(t, h.stderr.String(), c.want)
			assert.Zero(t, in.n, "the value was read before the refusal")
			assert.Empty(t, h.stdout.String(), "no key was made")
			assert.NoDirExists(t, dir)
		})
	}
}
