package main

import (
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every named verb answers -h and --help with its own help on stdout at exit 0,
// and none of them probes, wraps, writes a worktree or a ruleset
// (the CLI style's rule (b), #4505). Only -h is in this rule: the bare wrap and its
// 125 are not verbs and are not touched.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, sandboxRun, []testverbhelp.Case{
		{Verb: "check"},
		{Verb: "run", Flags: []string{"--write", "{dir}/w"}},
		{Verb: "reap"},
		{Verb: "worktree", Flags: []string{"--repo", "{dir}/repo"}},
		{Verb: "egress"},
		{Verb: "egress plan", Flags: []string{"--out", "{dir}/egress.nft"}},
		{Verb: "egress apply"},
		{Verb: "egress check"},
		{Verb: "egress drop"},
		{Verb: "policy", Flags: []string{"--write", "{dir}/w"}},
		{Verb: "probe", Flags: []string{"--write", "{dir}/w"}},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, sandboxRun, "nova-sandbox", "worktree", "egress plan", "probe", "version")
}

// help names verbs only: `help <anything else>` is the banner, never the bare
// wrap of a command called <anything else>.
func TestHelpNeverReachesTheBareWrap(t *testing.T) {
	t.Parallel()
	var out, errb strings.Builder
	if code := sandboxRun([]string{"help", "sh", "-c", "exit 7"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "nova-sandbox:") || errb.Len() != 0 {
		t.Fatalf("help sh: exit %d stdout %.60q stderr %q", code, out.String(), errb.String())
	}
}

func sandboxRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr, nil)
}
