package main

import (
	"fmt"
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// opens neither Postgres nor Redis (the CLI style's rule (b), #4505). The store
// seams are the harness's; a verb that opened one says so on stderr, which
// the check holds empty.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	conn := []string{"--redis", "{addr}"}
	var cases []testverbhelp.Case
	for _, v := range []string{"version", "kinds", "migrate", "status", "apply", "inventory"} {
		cases = append(cases, testverbhelp.Case{Verb: v})
	}
	for _, v := range []string{"machine", "machine list", "machine self", "machine width", "machine show", "machine history", "machine remove", "machine add", "machine set", "friend add", "fleet set", "fleet show", "sprint set"} {
		cases = append(cases, testverbhelp.Case{Verb: v})
	}
	cases = append(cases, testverbhelp.Case{Verb: "machine list", Flags: conn})
	testverbhelp.Check(t, configRun, cases)
	testverbhelp.HelpVerb(t, configRun, "nova-config", "status", "machine add", "fleet set", "version")
}

func configRun(args []string, stdout, stderr io.Writer) int {
	h := newHarness()
	code := run(args, stdout, stderr, h.deps())
	if h.opens != 0 || h.redis.opens != 0 {
		fmt.Fprintf(stderr, "opened postgres %d and redis %d time(s)\n", h.opens, h.redis.opens)
	}
	return code
}

// `help <verb>` is that verb's help whatever follows the verb, for every verb the
// tool's help names and for every verb a kind answers to: `machine add`, `fleet set`.
func TestHelpForAVerbIsHelpWhateverFollowsIt(t *testing.T) {
	t.Parallel()
	var kindVerbs []string
	for _, k := range config.Kinds {
		verbs := []string{"add", "set", "remove", "list", "show", "history"}
		switch {
		case k.Singleton:
			verbs = []string{"set", "show", "history"}
		case k.Name == config.KindMachine:
			verbs = append(verbs, "width", "self")
		}
		for _, v := range verbs {
			kindVerbs = append(kindVerbs, k.Name+" "+v)
		}
	}
	testverbhelp.HelpWhateverFollows(t, configRun, banner(), toolName, kindVerbs...)
}
