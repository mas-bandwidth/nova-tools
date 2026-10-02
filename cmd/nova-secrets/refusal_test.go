package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEveryRefusalNamesItsWholeFix pins the refusals a cold reader meets first: each is
// one line at its exit code, names every problem at once and carries what to run next,
// so the next call is the fixed one (ONBOARDING points 1 and 2).
func TestEveryRefusalNamesItsWholeFix(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	for _, c := range []struct {
		name string
		args []string
		code int
		want []string
		not  []string
	}{
		{"unknown verb lists the verbs", []string{"bogus"}, 2,
			[]string{`unknown verb "bogus"`, "the verbs are exec, names, check, gate, keygen, place, placed, seal, seat add, seat inject, version", "run: nova-secrets help"}, nil},
		{"a verb with a space is quoted plain", []string{"no such"}, 2, []string{`unknown verb "no such"`}, []string{`\x20`}},
		{"unknown flag lists the verb's flags", []string{"names", "--stoer", "x"}, 2,
			[]string{"unknown flag --stoer", "names takes --as, --max, --store", "run: nova-secrets names -h"}, []string{"flag provided but not defined"}},
		{"a bad value says what the flag wants", []string{"names", "--max", "lots"}, 2,
			[]string{"--max wants <n>", "the value given is not one"}, []string{"lots", "parse error"}},
		{"a stray argument is named", []string{"check", "extra"}, 2, []string{`unexpected argument "extra"`, "check takes flags only", "run: nova-secrets check -h"}, nil},
		{"exec with nothing names the flags and the command at once", []string{"exec"}, 125,
			[]string{"--store <dir>", "--as <name>", "--key <path>", "--sops <path>", "--only <names|all>", "the command after '--'", "example: nova-secrets exec"},
			[]string{"delimiter", "rowan"}},
		{"exec's command before -- is told where it goes", []string{"exec", "--only", "all", "gh", "api"}, 125,
			[]string{`unexpected argument "gh" before '--'`, "the command goes after '--'"}, nil},
		{"the seat group names its subverbs", []string{"seat"}, 2, []string{"add", "inject", "run: nova-secrets seat add -h"}, nil},
		{"version refuses with the status word", []string{"version", "x"}, 2, []string{"SECRETS REFUSED: version takes no flags"}, nil},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runNovaSecrets(bin, c.args...)
			assert.Equal(t, c.code, code, "stderr: %s", stderr)
			assert.Empty(t, stdout)
			assert.Equal(t, 1, strings.Count(stderr, "\n"), "one line: %q", stderr)
			for _, w := range c.want {
				assert.Contains(t, stderr, w)
			}
			for _, n := range c.not {
				assert.NotContains(t, stderr, n)
			}
		})
	}
}

// TestEveryVerbHelpStatesItsEffectAndWhatEachFlagWants: every verb's -h says which kind
// of verb it is (STANDARD §2: inspection, local write, store write or delivery) and every
// flag carries a sentence of what it wants, never a two-word label.
func TestEveryVerbHelpStatesItsEffectAndWhatEachFlagWants(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	for _, verb := range []string{"exec", "names", "check", "gate", "keygen", "place", "placed", "seal", "seat add", "seat inject", "version"} {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runNovaSecrets(bin, append(strings.Fields(verb), "-h")...)
			assert.Equal(t, 0, code, "stderr: %s", stderr)
			assert.Contains(t, stdout, "\neffect: ")
			inFlags := false
			for _, line := range strings.Split(stdout, "\n") {
				switch {
				case line == "flags:":
					inFlags = true
				case inFlags && strings.HasPrefix(line, "  --"):
					_, text, _ := strings.Cut(strings.TrimSpace(line), "  ")
					assert.GreaterOrEqual(t, len(strings.Fields(text)), 5, "%s: the flag says too little of what it wants: %q", verb, line)
				case inFlags:
					inFlags = false
				}
			}
		})
	}
}
