package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/release"

	"github.com/stretchr/testify/assert"
)

// Every refusal nova-release prints is one line in the one grammar,
// `<TOKEN> REFUSED: <reason>; run: <a command that runs>` (STANDARD §2, §3.1;
// ledger U12, X3): an unknown verb is named and the verbs listed, a
// misspelled flag is named with the verb's flags, every missing flag at once.
func TestEveryReleaseRefusalEndsInACommandToRun(t *testing.T) {
	t.Parallel()
	verbs := "the verbs are cut, build, install, adopt, pull, cycle, version"
	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"no verb", nil, []string{"RELEASE REFUSED: a verb is required; " + verbs + "; run: nova-release help"}},
		{"unknown verb", []string{"bogus"}, []string{`RELEASE REFUSED: unknown verb "bogus"; ` + verbs + "; run: nova-release help"}},
		{"flag misspelled", []string{"cut", "--rpeo", "x"}, []string{"CUT REFUSED:", "unknown flag --rpeo", "; run: nova-release cut -h"}},
		{"missing flags", []string{"build"}, []string{"missing --version, --out, --source", "; run: nova-release build -h"}},
		{"version with an argument", []string{"version", "x"}, []string{"VERSION REFUSED:", "takes no positional arguments", "; run: nova-release version -h"}},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var out, errs bytes.Buffer
			code := release.Main("nova-release", c.args, "", &out, &errs)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.Equal(t, 1, strings.Count(errs.String(), "\n"), "one line: %q", errs.String())
			assert.NotContains(t, errs.String(), "(run ")
			for _, w := range c.want {
				assert.Contains(t, errs.String(), w)
			}
		})
	}
}

// Each verb's -h holds its own usage line, its flags with what each wants, and
// the exit codes, and no person: the pipeline's own help, which
// `nova-update help release` used to carry.
func TestEveryVerbsHelpListsItsFlags(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"cut", "build", "install", "adopt", "pull", "cycle"} {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var out, errs bytes.Buffer
			code := release.Main("nova-release", []string{verb, "-h"}, "", &out, &errs)
			assert.Equal(t, 0, code)
			assert.Empty(t, errs)
			assert.Contains(t, out.String(), "flags:\n")
			assert.Contains(t, out.String(), "  --version <string>  ")
			assert.Contains(t, out.String(), "exit codes: 0 ")
			// ONE usage line: the person asked about one verb.
			assert.Equal(t, 1, strings.Count(out.String(), "nova-release "+verb+" "), "%s --help printed more than its own line:\n%s", verb, out.String())
			assert.NotContains(t, out.String(), "Johnny")
		})
	}
}
