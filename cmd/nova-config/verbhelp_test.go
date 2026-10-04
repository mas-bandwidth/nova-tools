package main

import (
	"fmt"
	"io"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestHelpSaysWhichVariableHoldsThePassword pins the help's sentence about
// the Postgres password (ONBOARDING point 2: a flag's description says what
// it wants): NOVA_PG_PASSWORD_ENV holds the name of the variable that holds
// the password, never the password itself, the rule config.ResolveDSN applies
// (docs/nova-config/README.md, "Connecting"). The sentence it replaces, "the
// variable NOVA_PG_PASSWORD_ENV names", read two ways: that the variable
// holds the password, or that it holds that name.
func TestHelpSaysWhichVariableHoldsThePassword(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "banner", args: []string{"help"}},
		{name: "pg flag description", args: []string{"migrate", "-h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errs := newHarness().run(t, tc.args...)
			require.Equal(t, 0, code, "%v: exit %d, stderr %q", tc.args, code, errs)
			assert.Contains(t, out, "NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password",
				"%v: the help says what NOVA_PG_PASSWORD_ENV holds: a variable name, not the password", tc.args)
			assert.Contains(t, out, "NOVA_PG_PASSWORD when it is unset",
				"%v: the help names the variable read when NOVA_PG_PASSWORD_ENV is unset", tc.args)
			assert.Contains(t, out, "never the password itself",
				"%v: the help says NOVA_PG_PASSWORD_ENV never holds the password itself", tc.args)
			assert.NotContains(t, out, "the password comes from the variable NOVA_PG_PASSWORD_ENV",
				"%v: the old sentence reads as NOVA_PG_PASSWORD_ENV holding the password", tc.args)
			assert.NotContains(t, out, "it is read from the variable NOVA_PG_PASSWORD_ENV",
				"%v: the old sentence reads as NOVA_PG_PASSWORD_ENV holding the password", tc.args)
		})
	}
}
