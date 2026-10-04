package main

import (
	"bytes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestHelpSaysWhichVariableHoldsThePassword pins the help's sentence about the
// store password (ONBOARDING point 2: a flag's description says what it
// wants): NOVA_REDIS_PASSWORD_ENV holds the name of the variable the password
// is read from, never the password itself, the rule login.check applies (the
// spill and recall section of docs/SPEC-REDIS.md). The sentence it replaces,
// "(default NOVA_REDIS_PASSWORD_ENV, else NOVA_REDIS_PASSWORD)", read two
// ways: that NOVA_REDIS_PASSWORD_ENV holds the password, or that it holds that
// name. The --password-env flag description says the same.
func TestHelpSaysWhichVariableHoldsThePassword(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "banner", args: []string{"-h"}, want: []string{
			"The password is read from the variable NOVA_REDIS_PASSWORD_ENV names",
			"else NOVA_REDIS_PASSWORD",
		}},
		{name: "password-env flag description", args: []string{"spill", "-h"}, want: []string{
			"the NAME of the variable that holds the password, never the password itself",
			"the variable $NOVA_REDIS_PASSWORD_ENV names, else NOVA_REDIS_PASSWORD",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			code := redisRun(tc.args, &stdout, io.Discard)
			require.Equal(t, 0, code, "%v: want exit 0", tc.args)
			out := stdout.String()
			for _, w := range tc.want {
				assert.Contains(t, out, w, "%v: the help does not say which variable holds the password and which one holds its name; a reader may export the password into NOVA_REDIS_PASSWORD_ENV", tc.args)
			}
			assert.NotContains(t, out, "default NOVA_REDIS_PASSWORD_ENV", "%v: the old sentence reads as NOVA_REDIS_PASSWORD_ENV holding the password", tc.args)
		})
	}
}
