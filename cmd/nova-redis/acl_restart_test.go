//go:build !functional

package main

// acl_restart_test.go is the unit twin of the functional tier's
// TestAclUsersSurviveARestart (card redis-serve-keeps-acls). The users
// nova-redis acl apply sets survive a restart because serve names an ACL file
// under --dir, acl apply's ACL SAVE writes them into it, and the next serve
// keeps every line of it but the default user's, which it writes from the
// password. The launch is the deps seam, and the ACL SAVE is the fake launch
// writing the file the way redis-server does (mode 0644, every user a hash),
// so no process starts and no wall clock is read; the functional tier proves
// the same against a throwaway redis-server.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// TestAclUsersSurviveARestart drives serve three times on one --dir through
// run(): the first run names <dir>/users.acl in the config and writes it 0600
// with the default user's password hash; the store then saves its users there
// (ACL SAVE, as acl apply does), and a restart with --users naming them keeps
// each user's line as saved, puts the file back to 0600 and the default user
// back on the password; a restart whose --users names a user the file lacks is
// refused, launches nothing and leaves the file as it was. No secret is in the
// file, the output or the child's environment.
func TestAclUsersSurviveARestart(t *testing.T) {
	t.Parallel()

	const pw = "pw!acl#7Qx%v2@Lr^m"
	sum := sha256.Sum256([]byte(pw))
	hash := "#" + hex.EncodeToString(sum[:])
	h := newServeHarness(t, pw)
	acl := filepath.Join(h.dir, "users.acl")
	secret := fixtureSecret(t, pw, h.dir, acl, fakeRedisServer)
	saved := []string{
		"user coordinator on sanitize-payload #" + strings.Repeat("a", 64) + " ~sprint:* resetchannels -@all +fcall",
		"user bench on sanitize-payload #" + strings.Repeat("b", 64) + " ~table:* resetchannels -@all +fcall",
	}
	// The first launch is the store running while acl apply sets two users
	// and saves them: redis-server rewrites the whole file, umask 022, and
	// writes the default user as it holds it.
	h.onLaunch = func(spec launchSpec) error {
		h.onLaunch = nil
		text := "user default on sanitize-payload " + hash + " ~* &* +@all\n" + strings.Join(saved, "\n") + "\n"
		require.NoError(t, os.WriteFile(acl+".tmp", []byte(text), 0o644))
		return os.Rename(acl+".tmp", acl)
	}

	code, out, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir)
	require.Zero(t, code, "first serve: exit %d stderr %q", code, errb)
	require.Len(t, h.launches, 1)
	first := h.launches[0]
	assert.Equal(t, acl, strings.Join(only(t, config(t, first.Config), "aclfile"), " "), "the config names the ACL file under --dir")
	assert.Contains(t, out, " aclfile="+acl+" users=0 ", "START names the ACL file and the users it held: %q", out)

	code, out2, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir, "--users", "coordinator,bench")
	require.Zero(t, code, "restart: exit %d stderr %q", code, errb)
	require.Len(t, h.launches, 2)
	assert.Equal(t, string(first.Config), string(h.launches[1].Config), "the restart hands redis-server the same config, the same ACL file")
	assert.Contains(t, out2, " aclfile="+acl+" users=2 ", "START counts the users the file kept: %q", out2)
	fi, err := os.Stat(acl)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "serve puts the ACL file back to 0600 after the store's save")
	raw, err := os.ReadFile(acl)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	assert.Equal(t, append([]string{"user default on sanitize-payload " + hash + " ~* &* +@all"}, saved...), lines,
		"the restart keeps every saved user's line as the store wrote it, and the default user on the password")
	before := string(raw)

	for _, args := range [][]string{
		{"serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir, "--users", "coordinator,bench,ns-friend"},
		{"serve", "--dry-run", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir, "--users", "coordinator,bench,ns-friend"},
	} {
		code, out, errb := h.run(args...)
		assert.Equal(t, 2, code, "%v: a store whose ACL file lacks a user --users names is refused: stdout %q stderr %q", args, out, errb)
		assert.Contains(t, errb, "missing the users ns-friend", "%v: the refusal names the missing user", args)
		assert.Contains(t, errb, "nova-redis acl apply", "%v: the refusal names the remedy", args)
		assert.Len(t, h.launches, 2, "%v: nothing launched", args)
		raw, err := os.ReadFile(acl)
		require.NoError(t, err)
		assert.Equal(t, before, string(raw), "%v: the refused run left the ACL file as it was", args)
	}

	for _, o := range []string{out, out2, errb} {
		assert.False(t, secrets.Leaks(o, secret), "serve printed the secret: %q", o)
	}
	assert.False(t, secrets.Leaks(before, secret), "the ACL file holds the password; it holds only its hash")
	for _, spec := range h.launches {
		for _, e := range spec.Env {
			assert.False(t, secrets.Leaks(e, secret), "the child environment carries the secret: %q", e)
		}
	}
}

// A path at the ACL file's place that is not a regular file is refused, never
// followed or replaced, and a blank --users name is refused before anything.
func TestServeRefusesAnACLFileItCannotKeep(t *testing.T) {
	t.Parallel()

	h := newServeHarness(t, "pw-from-nova-secrets")
	require.NoError(t, os.MkdirAll(filepath.Join(h.dir, "users.acl"), 0o700))
	code, _, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir)
	assert.Equal(t, 2, code, errb)
	assert.Contains(t, errb, "is not a regular file", errb)
	assert.Empty(t, h.launches)

	h = newServeHarness(t, "pw-from-nova-secrets")
	code, _, errb = h.run("serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir, "--users", "bench,,coordinator")
	assert.Equal(t, 2, code, errb)
	assert.Contains(t, errb, "--users", errb)
	assert.Empty(t, h.launches)
	_, err := os.Stat(h.dir)
	assert.ErrorIs(t, err, os.ErrNotExist, "a refused --users created the store directory")
}
