package main

import (
	"github.com/stretchr/testify/assert"
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them dials the instance, launches redis-server or creates the store
// directory (the CLI style's rule (b), #4505). The deps are the real ones: a verb
// that ran past -h would dial {addr} or make {dir}/store.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, redisRun, []testverbhelp.Case{
		{Verb: "serve", Flags: []string{"--dir", "{dir}/store", "--port", "6399", "--bind", "127.0.0.1"}},
		{Verb: "spill", Flags: []string{"--addr", "{addr}", "--owner", "o", "--name", "n", "--ttl", "1m", "--value", "v"}},
		{Verb: "recall", Flags: []string{"--addr", "{addr}", "--owner", "o", "--name", "n"}},
		{Verb: "fn"},
		{Verb: "fn load", Flags: []string{"--addr", "{addr}"}},
		{Verb: "fn check", Flags: []string{"--addr", "{addr}"}},
		{Verb: "acl"},
		{Verb: "acl render"},
		{Verb: "acl check", Flags: []string{"--addr", "{addr}"}},
		{Verb: "acl apply", Flags: []string{"--addr", "{addr}"}},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, redisRun, "nova-redis", "serve", "spill", "recall", "fn", "fn load", "fn check", "acl", "acl render", "acl check", "acl apply", "version")
}

func redisRun(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, realDeps())
}

// nova-redis's definition meets the standard its banner and help cannot hold
// by construction: every verb's effect, and a how text of five short lines.
func TestRedisToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, redisTool(deps{}).Problems())
}
