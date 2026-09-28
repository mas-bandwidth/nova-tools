package main

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
)

const (
	l17Sprint = "l17"
	l17Unit   = "gh/mas-bandwidth/nova-tools/3200"
	l17Head   = "4139b79f0a1b2c3d4e5f60718293a4b5c6d7e8f9"
	l17Tip    = "1111111111111111111111111111111111111111"
)

// l17Redis holds one unit in the shapes land.lua writes (2.2): the unit hash,
// the units index, the prunit pointer, the policy and a CI receipt at head.
// No s:<S>:pr:* key exists.
func l17Redis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	s := "s:" + l17Sprint + ":"
	mr.HSet(s+"u:"+l17Unit, "repo", "nova-tools", "base", "dev", "branch", "card-3200", "head", l17Head,
		"base_sha", l17Tip, "stack_parent", "none", "state", "reading", "author", "rowan", "pr", "3200", "seq", "7")
	mr.SAdd(s+"units", l17Unit)
	mr.Set(s+"prunit:nova-tools:3200", l17Unit)
	mr.HSet(civerdict.PolicyKey("nova-tools", "dev"), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1")
	gid := civerdict.GID("single", "dev", l17Tip, "req1", "pol1", "run1")
	mr.HSet(civerdict.Key("nova-tools", l17Head, gid), "verdict", "OK")
	mr.SAdd(civerdict.GIDsKey("nova-tools", l17Head), gid)
	return mr
}
