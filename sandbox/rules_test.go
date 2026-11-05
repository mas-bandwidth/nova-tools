package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testWall() *wall {
	return &wall{reads: []string{"/r/one"}, writes: []string{"/w/job", "/w/data"}}
}

func find(rs *ruleset, path string) (rule, bool) {
	for _, r := range rs.rules {
		if r.path == path {
			return r, true
		}
	}
	return rule{}, false
}

func TestRulesetGrants(t *testing.T) {
	t.Parallel()
	rs := buildRuleset(testWall(), 6, "")
	assert.Equal(t, handledFS(6), rs.fs)

	r, ok := find(rs, "/usr")
	require.True(t, ok)
	assert.EqualValues(t, readMask, r.mask)
	assert.False(t, r.must, "a system root is skipped if absent")

	r, _ = find(rs, "/r/one")
	assert.EqualValues(t, readMask, r.mask)
	assert.True(t, r.must, "a caller path that cannot be added is a refusal")

	r, _ = find(rs, "/w/data")
	assert.Equal(t, rs.fs, r.mask, "a write gets every handled right, removal included")
	assert.True(t, r.must)

	r, _ = find(rs, "/dev/null")
	assert.Equal(t, fileMask(6), r.mask)
	assert.Zero(t, r.mask&fsReadDir, "a file rule carries no directory-only right")

	r, ok = find(rs, "/dev/shm")
	require.True(t, ok)
	assert.Equal(t, rs.fs, r.mask, "/dev/shm is writable")
	assert.False(t, r.must, "but skipped if absent")
}

func TestRulesetOutsideTheSpecIsNotGranted(t *testing.T) {
	t.Parallel()
	rs := buildRuleset(testWall(), 6, "")
	for _, d := range []string{"/home", "/root", "/tmp", "/var", "/mnt", "/w"} {
		_, ok := find(rs, d)
		assert.False(t, ok, d)
	}
	assert.Len(t, rs.rules, len(linuxReadRoots)+len(linuxWriteFiles)+len(linuxWriteDirs)+3)
}

func TestRulesetResolverDirectory(t *testing.T) {
	t.Parallel()
	r, ok := find(buildRuleset(testWall(), 6, "/mnt/wsl"), "/mnt/wsl")
	require.True(t, ok)
	assert.EqualValues(t, readMask, r.mask)
}

func TestRulesetByABI(t *testing.T) {
	t.Parallel()
	assert.Zero(t, handledFS(1)&(fsRefer|fsTruncate|fsIoctlDev))
	assert.NotZero(t, handledFS(2)&fsRefer)
	assert.Zero(t, handledFS(2)&fsTruncate)
	assert.NotZero(t, handledFS(3)&fsTruncate)
	assert.NotZero(t, handledFS(5)&fsIoctlDev)

	w := testWall()
	rs := buildRuleset(w, 6, "")
	assert.Zero(t, rs.net, "without --net-deny the network is not handled")
	assert.EqualValues(t, scopeAbstractUnix|scopeSignal, rs.scope)
	w.netDeny = true
	rs = buildRuleset(w, 6, "")
	assert.EqualValues(t, netBindTCP|netConnectTCP, rs.net, "handling with no rule is how the denial works")
	rs = buildRuleset(w, 3, "")
	assert.Zero(t, rs.net, "below abi 4 there is no net right; execWall refuses before this")
	assert.Zero(t, rs.scope)
}

func TestRulesetNetAllowPorts(t *testing.T) {
	t.Parallel()
	w := testWall()
	w.netDeny, w.netPorts = true, []uint16{11434}
	assert.Equal(t, []uint16{11434}, buildRuleset(w, 6, "").connectPorts)
	assert.Empty(t, buildRuleset(w, 3, "").connectPorts)
}
