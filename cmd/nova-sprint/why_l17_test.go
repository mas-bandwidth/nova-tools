package main

import (
	"strconv"
	"strings"
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

// TestL17b is control L17b (nova-tools#3139 rev 7 section 11, 7.7): every
// outcome of `why` (both forms) and `land status [<unit>]` exits with exactly
// its code: 0 printed, 1 no record, 2 refused or usage, 6 no Redis.
func TestL17b(t *testing.T) {
	mr := l17Redis(t)
	addr := mr.Addr()
	now := strconv.FormatInt(1790000000, 10)
	dead := "127.0.0.1:1"
	cases := []struct {
		name string
		args []string
		code int
		out  string // a substring of stdout+stderr
	}{
		{"why unit", []string{"why", l17Unit, "--redis", addr, "--sprint", l17Sprint, "--now", now}, 0, "ci OK@4139b79f"},
		{"why pr", []string{"why", "nova-tools#3200", "--redis", addr, "--sprint", l17Sprint, "--now", now}, 0, "unit " + l17Unit + " nova-tools#3200"},
		{"why unknown unit", []string{"why", "gh/mas-bandwidth/nova-tools/9999", "--redis", addr, "--sprint", l17Sprint}, 1, "MISSING s:l17:u:gh/mas-bandwidth/nova-tools/9999"},
		{"why unknown pr", []string{"why", "nova-tools#9999", "--redis", addr, "--sprint", l17Sprint}, 1, "MISSING s:l17:prunit:nova-tools:9999"},
		{"why no arg", []string{"why", "--redis", addr, "--sprint", l17Sprint}, 2, "REFUSED"},
		{"why two args", []string{"why", l17Unit, "nova-tools#3200", "--redis", addr, "--sprint", l17Sprint}, 2, "REFUSED"},
		{"why bare number", []string{"why", "3200", "--redis", addr, "--sprint", l17Sprint}, 2, "REFUSED"},
		{"why no sprint", []string{"why", l17Unit, "--redis", addr}, 2, "REFUSED"},
		{"why dead redis", []string{"why", l17Unit, "--redis", dead, "--sprint", l17Sprint}, 6, ""},
		{"status", []string{"land", "status", "--redis", addr, "--sprint", l17Sprint, "--now", now}, 0, "units 1: reading 1"},
		{"status unit", []string{"land", "status", l17Unit, "--redis", addr, "--sprint", l17Sprint, "--now", now}, 0, "state reading"},
		{"status unknown unit", []string{"land", "status", "gh/mas-bandwidth/nova-tools/9999", "--redis", addr, "--sprint", l17Sprint}, 1, "MISSING s:l17:u:gh/mas-bandwidth/nova-tools/9999"},
		{"status two units", []string{"land", "status", l17Unit, l17Unit, "--redis", addr, "--sprint", l17Sprint}, 2, "REFUSED"},
		{"status no sprint", []string{"land", "status", "--redis", addr}, 2, "REFUSED"},
		{"status dead redis", []string{"land", "status", "--redis", dead, "--sprint", l17Sprint}, 6, ""},
		{"status unit dead redis", []string{"land", "status", l17Unit, "--redis", dead, "--sprint", l17Sprint}, 6, ""},
	}
	for _, tc := range cases {
		code, stdout, stderr := runSprint(tc.args...)
		if code != tc.code {
			t.Errorf("%s: exit %d, want %d\nstdout %s\nstderr %s", tc.name, code, tc.code, stdout, stderr)
			continue
		}
		if tc.out != "" && !strings.Contains(stdout+stderr, tc.out) {
			t.Errorf("%s: want %q in\nstdout %s\nstderr %s", tc.name, tc.out, stdout, stderr)
		}
	}
}
