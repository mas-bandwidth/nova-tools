package main

import (
	"strings"
	"testing"
)

// TestWSVerbsRefuseHelpAndUsage: every usage fault is one stderr line through
// refuse (exit 2) naming the door, and nothing on stdout; --help is the usage
// line and flags on stdout, exit 2 (#3254).
func TestWSVerbsRefuseHelpAndUsage(t *testing.T) {
	for _, args := range [][]string{
		{"ws"}, {"ws", "nope"}, {"scope"}, {"stream"}, {"stream", "nope"},
		{"ws", "counts", "--redis", "127.0.0.1:1", "extra"},
		{"ws", "checkpoint", "--redis", "127.0.0.1:1"},
		{"scope", "keep", "--redis", "127.0.0.1:1"},
		{"scope", "park", "--redis", "127.0.0.1:1"},
		{"stream", "rename", "--redis", "127.0.0.1:1", "--stream", "only-one"},
		{"stream", "order", "--redis", "127.0.0.1:1"},
	} {
		t.Setenv("NOVA_SPRINT_REDIS", "")
		code, stdout, stderr := runSprint(args...)
		if code != 2 || stdout != "" || strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, "; usage: nova-sprint "+args[0]) {
			t.Errorf("%v: exit %d stdout %q stderr %q; want one refusal line, exit 2", args, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{
		{"ws", "counts", "--help"}, {"ws", "checkpoint", "--help"},
		{"scope", "keep", "--help"}, {"scope", "park", "--help"}, {"scope", "unpark", "--help"}, {"scope", "ls", "--help"},
		{"stream", "ls", "--help"}, {"stream", "order", "--help"}, {"stream", "rename", "--help"},
	} {
		code, stdout, stderr := runSprint(args...)
		if code != 2 || stderr != "" || !strings.HasPrefix(stdout, "usage: nova-sprint "+args[0]+" "+args[1]+" ") || !strings.Contains(stdout, "  --redis <string>") {
			t.Errorf("%v: exit %d stdout %q stderr %q; want the usage and flags on stdout, exit 2", args, code, stdout, stderr)
		}
	}
	code, stdout, _ := runSprint("help")
	for _, v := range []string{"  ws  ", "  scope  ", "  stream  "} {
		if code != 0 || !strings.Contains(stdout, v) {
			t.Errorf("help lacks %q", v)
		}
	}
}
