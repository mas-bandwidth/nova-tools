package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads a checkout, runs a compiler or composes a remote command
// (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	testverbhelp.Check(t, releaseRun, []testverbhelp.Case{
		{Verb: "cut"},
		{Verb: "build"},
		{Verb: "install"},
		{Verb: "adopt"},
		{Verb: "pull"},
		{Verb: "cycle"},
	})
}

func releaseRun(args []string, stdout, stderr io.Writer) int {
	return release.Main("nova-release", args, "", stdout, stderr)
}

// A verb's -h lists its flags with what each wants, the exit codes, and no
// person: the three things a stranger reads before they type (moved with the
// verb from nova-update's help, ledger U5).
func TestEveryVerbHelpListsItsFlagsAndExitCodes(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"cut", "build", "install", "adopt", "pull", "cycle"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var h, errs bytes.Buffer
			code := releaseRun([]string{verb, "-h"}, &h, &errs)
			require.Equal(t, 0, code, "%s -h: %s", verb, errs.String())
			assert.Empty(t, errs.String())
			assert.Contains(t, h.String(), "flags:\n")
			assert.Contains(t, h.String(), "  --version <string>  ")
			assert.Contains(t, h.String(), "exit codes: 0 ")
			assert.NotContains(t, h.String(), "Johnny")
		})
	}
}

// A bare run and an unknown verb refuse in the one grammar, naming the release
// verbs and the door (STANDARD §2); the refusals moved with the verbs from
// nova-update's table.
func TestRefusalsNameTheVerbsAndTheDoor(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"bare", nil, "RELEASE REFUSED: a release verb is required; the release verbs are cut, build, install, adopt, pull, cycle; run: nova-release help"},
		{"unknown verb", []string{"bogus"}, `RELEASE REFUSED: unknown release verb "bogus"; the release verbs are cut, build, install, adopt, pull, cycle; run: nova-release help`},
		{"missing flags", []string{"build"}, "BUILD REFUSED: missing --version, --out, --source; refusing to guess (supply each named flag); run: nova-release build -h"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var out, errs bytes.Buffer
			code := releaseRun(c.args, &out, &errs)
			assert.Equal(t, 2, code)
			assert.Empty(t, out.String())
			assert.Equal(t, 1, strings.Count(errs.String(), "\n"), "one line: %q", errs.String())
			assert.Equal(t, c.want+"\n", errs.String())
		})
	}
}
