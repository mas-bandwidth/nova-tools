package main

// redis_flag_test.go pins the family's one name for the store address:
// --redis is the flag, --addr is its old spelling kept for one release, and a
// run that spells the old name still works and says so on a NOTE
// (docs/STANDARD.md section 2, "One shape across the set").

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRedisIsTheStoreAddress: a good line spelled --redis runs with no NOTE.
func TestRedisIsTheStoreAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code, out, errs := h.runBare("spill", "--dry-run", "--redis", "127.0.0.1:6379", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SPILL OK")
	assert.Contains(t, out, "store=127.0.0.1:6379")
	assert.NotContains(t, out, "NOTE")
	assert.Empty(t, errs)
}

// TestAddrIsTheOldSpellingAndPrintsANote: the old spelling still works for one
// release, and the success carries the one NOTE that says so.
func TestAddrIsTheOldSpellingAndPrintsANote(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code, out, errs := h.runBare("spill", "--dry-run", "--addr", "127.0.0.1:6379", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SPILL OK")
	assert.Contains(t, out, "NOTE --addr is --redis", out)
}

// TestABadAddressNamesTheFlagSpelled: the refusal quotes the flag the reader
// typed, so a --redis user is not sent to the old name.
func TestABadAddressNamesTheFlagSpelled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		flag string
		want string
	}{
		{"--redis", `SPILL REFUSED: --redis "nohost" is not <host:port>`},
		{"--addr", `SPILL REFUSED: --addr "nohost" is not <host:port>`},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			t.Parallel()
			code, out, errs := coldRun(t, "spill", tc.flag, "nohost", "--ttl", "0s", "--owner", "a", "--name", "b", "--value", "c")
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.Contains(t, errs, tc.want, errs)
		})
	}
}

// TestHelpNamesBothSpellings: the verb's help names --redis and says --addr is
// its old spelling.
func TestHelpNamesBothSpellings(t *testing.T) {
	t.Parallel()
	code, out, errs := coldRun(t, "spill", "-h")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "--redis")
	assert.Contains(t, out, "--addr")
	assert.Contains(t, out, "old spelling of --redis")
}

// TestAddrNoteFollowsTheVerbsOwnLine: a run that spells the old --addr gets
// its NOTE after the verb's own outcome line, never before it, for the verbs
// that print their own lines (fn, acl) as for the skeleton's. The status word
// leads the line the reader acts on, and the renderer prints notes last
// (docs/STANDARD.md section 2, "The status word leads every line").
func TestAddrNoteFollowsTheVerbsOwnLine(t *testing.T) {
	t.Parallel()
	const addr = "127.0.0.1:6399"
	sha := want(t)
	note := "\nNOTE --addr is --redis"

	h := newFnHarness(t)
	code, out, errs := h.run("fn", "check", "--addr", addr)
	require.Equal(t, 1, code, errs)
	assert.True(t, strings.HasPrefix(out, "MISSING nova_sprint sha="+sha+" "), out)
	assert.True(t, strings.HasSuffix(strings.TrimRight(out, "\n"), note), out)

	f := &fakeACL{}
	code, out, errs = aclRun(t, f, "apply", "--dry-run", "--addr", "127.0.0.1:6379", "--user", "admin", "--password-env", "ADMIN_PW")
	require.Equal(t, 0, code, errs)
	assert.True(t, strings.HasPrefix(out, "ACL WOULD-SET "), out)
	assert.True(t, strings.HasSuffix(strings.TrimRight(out, "\n"), note), out)
}
