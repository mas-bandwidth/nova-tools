package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func linuxPolicy() *policy {
	return &policy{reads: []string{"/r/one"}, readsNoExec: []string{"/n/cache"}, writes: []string{"/w/job", "/w/data"},
		cwd: "/w/job", tmp: "/w/job/.nova-sandbox-tmp", home: "/w/data"}
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
	rs, err := buildRuleset(linuxPolicy(), 6, "")
	require.NoError(t, err)
	assert.Equal(t, handledFS(6), rs.fs)

	r, ok := find(rs, "/usr")
	require.True(t, ok)
	assert.EqualValues(t, readMask, r.mask)
	assert.False(t, r.must, "a root is skipped if absent")

	r, _ = find(rs, "/r/one")
	assert.EqualValues(t, readMask, r.mask)
	assert.True(t, r.must, "a caller path that cannot be added is a refusal")

	r, _ = find(rs, "/w/job")
	assert.Equal(t, rs.fs, r.mask, "a write root gets every handled right, removal included")
	assert.True(t, r.must)

	r, _ = find(rs, "/n/cache")
	assert.EqualValues(t, fsReadFile|fsReadDir, r.mask)
	assert.Zero(t, r.mask&fsExecute)

	r, _ = find(rs, "/dev/null")
	assert.True(t, r.file)
	assert.Zero(t, r.mask&fsReadDir, "a file rule carries no directory-only right")
}

func TestRulesetOutsideTheSetIsNotGranted(t *testing.T) {
	t.Parallel()
	rs, err := buildRuleset(linuxPolicy(), 6, "")
	require.NoError(t, err)
	for _, home := range []string{"/home", "/root", "/tmp", "/var", "/mnt"} {
		_, ok := find(rs, home)
		assert.False(t, ok, home)
	}
}

func TestRulesetTmpAndCwdOutsideWritesAreWritable(t *testing.T) {
	t.Parallel()
	p := linuxPolicy()
	p.tmp, p.cwd = "/t/tmp", "/c"
	rs, err := buildRuleset(p, 6, "")
	require.NoError(t, err)
	for _, d := range []string{"/t/tmp", "/c"} {
		r, ok := find(rs, d)
		require.True(t, ok, d)
		assert.Equal(t, rs.fs, r.mask)
	}
}

func TestRulesetResolverDirectory(t *testing.T) {
	t.Parallel()
	rs, err := buildRuleset(linuxPolicy(), 6, "/mnt/wsl")
	require.NoError(t, err)
	r, ok := find(rs, "/mnt/wsl")
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

	p := linuxPolicy()
	rs, _ := buildRuleset(p, 6, "")
	assert.Zero(t, rs.net, "without --net-deny the network is not handled")
	assert.NotZero(t, rs.scope)
	p.netDeny = true
	rs, _ = buildRuleset(p, 6, "")
	assert.EqualValues(t, netBindTCP|netConnectTCP, rs.net, "handling with no rule is how the denial works")
	rs, _ = buildRuleset(p, 3, "")
	assert.Zero(t, rs.net, "below abi 4 there is no net right; execWall refuses before this")
	assert.Zero(t, rs.scope)
}

func TestRulesetNetAllowPorts(t *testing.T) {
	t.Parallel()
	p := linuxPolicy()
	p.netAllow = []string{"localhost:11434"}
	rs, _ := buildRuleset(p, 6, "")
	assert.Empty(t, rs.connectPorts, "an open network has nothing to open")
	p.netDeny = true
	rs, _ = buildRuleset(p, 6, "")
	assert.Equal(t, []uint16{11434}, rs.connectPorts)
}

func TestNoExecUnderAnExecuteRootIsRefused(t *testing.T) {
	t.Parallel()
	for _, under := range []string{"/r/one/cache", "/w/job/cache", "/usr/lib/cache"} {
		p := linuxPolicy()
		p.readsNoExec = []string{under}
		_, err := buildRuleset(p, 6, "")
		assert.Error(t, err, under)
	}
}
