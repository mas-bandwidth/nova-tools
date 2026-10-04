package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestHelpSaysWhichVariableHoldsThePassword pins the rule that
// NOVA_REDIS_PASSWORD_ENV holds the NAME of the variable the password is read
// from, not the password itself: a reader must not export the password into it
// (see PasswordEnvEnv in main.go, and SPEC-REDIS.md the password is read from
// the variable --password-env names). The --password-env flag description says
// the same.
func TestHelpSaysWhichVariableHoldsThePassword(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	code := redisRun([]string{"-h"}, &stdout, io.Discard)
	require.Equal(t, 0, code, "nova-redis -h: want exit 0")
	out := stdout.String()
	// The password is read from the variable NOVA_REDIS_PASSWORD_ENV names
	// (an indirection: that variable holds a name, not a password); when it is
	// unset the password is read from NOVA_REDIS_PASSWORD.
	assert.Contains(t, out, "NOVA_REDIS_PASSWORD_ENV names", "help does not say NOVA_REDIS_PASSWORD_ENV names the variable that holds the password; a reader may export the password into it")
}
