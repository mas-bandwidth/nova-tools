package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
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
	for _, v := range []string{"machine", "machine list", "machine self", "machine width", "machine show", "machine history", "machine remove", "machine add", "machine set", "friend add", "fleet set", "fleet show", "sprint set", "loop run"} {
		cases = append(cases, testverbhelp.Case{Verb: v})
	}
	cases = append(cases, testverbhelp.Case{Verb: "machine list", Flags: conn})
	testverbhelp.Check(t, configRun, cases)
	testverbhelp.HelpVerb(t, configRun, "nova-config", "status", "machine add", "fleet set", "version")

	// The banner's costliest lines (a cold read): the password sentence
	// cannot be read backwards, the sprint row's decide_* bars carry their
	// unit, the first run's export is alone on its own indented line so a
	// paste exports no trailing comma, every line is at most 100 columns,
	// and the example: lines run as printed.
	h := newHarness()
	h.dir = t.TempDir()
	var out, errb bytes.Buffer
	require.Zero(t, run([]string{"help"}, &out, &errb, h.deps()), "help: %s", errb.String())
	banner := out.String()
	flat := strings.Join(strings.Fields(banner), " ")
	assert.Contains(t, flat, "NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password", "the password sentence still reads backwards")
	assert.Contains(t, flat, "each a probability in [0,1]", "the sprint row's decide_* bars still name no unit")
	assert.Contains(t, flat, "nova-config sprint set -h says what each bar decides", "the sprint row does not point at sprint set -h for what each bar decides")
	assert.Contains(t, banner, "\n  export NOVA_PG_DSN=postgres://user@host:5432/db\n", "the first run's export is not alone on its own indented line")
	for i, line := range strings.Split(banner, "\n") {
		assert.LessOrEqual(t, len(line), 100, "help line %d is %d columns: %q", i+1, len(line), line)
	}
	// A help sentence that describes a behaviour is checked against the
	// behaviour by running it: the password is the one the variable
	// NOVA_PG_PASSWORD_ENV names, never the variable itself.
	env := func(k string) string {
		return map[string]string{"NOVA_PG_DSN": dsn, "NOVA_PG_PASSWORD_ENV": "NOVA_SECRET_PG", "NOVA_SECRET_PG": "pw"}[k]
	}
	got, err := pgDSN("", env)
	require.NoError(t, err)
	assert.Equal(t, "postgres://nova_config:pw@127.0.0.1:5432/nova", got, "the sentence's named variable is the one the password is read from")
	// sprint set -h says what each decide_* bar decides, and that it is a
	// probability.
	var sprintHelp bytes.Buffer
	require.Zero(t, run([]string{"sprint", "set", "-h"}, &sprintHelp, &errb, h.deps()), "sprint set -h: %s", errb.String())
	for _, bar := range []string{"--decide_bounce", "--decide_review", "--decide_score_bar", "--decide_attempt_no_result", "--decide_attempt_nothing_to_do", "--decide_grade", "--decide_gate_flaky", "--decide_gate_preexisting", "--decide_judgment_bar", "--decide_brief_bar"} {
		assert.Contains(t, sprintHelp.String(), bar, "sprint set -h does not name %s", bar)
	}
	assert.Contains(t, sprintHelp.String(), "a probability", "sprint set -h does not say the decide_* bars are probabilities")
	// The example: block runs as printed, in order.
	examples, err := onboarding.ExampleLines(banner, "nova-config")
	require.NoError(t, err)
	for _, ex := range examples {
		var eout, eerr bytes.Buffer
		args := strings.Fields(ex)[1:]
		code := run(args, &eout, &eerr, h.deps())
		assert.Zero(t, code, "example %q exits %d: %s", ex, code, eerr.String())
	}
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
