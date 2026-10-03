package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
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
			[]string{`"bogus" is no verb and no file`, "the verbs are exec, names, check, gate, keygen, place, placed, seal, seat add, seat inject, version", "run: nova-secrets help"}, nil},
		{"a verb with a space is quoted plain", []string{"no such"}, 2, []string{`"no such" is no verb and no file`}, []string{`\x20`}},
		{"unknown flag lists the verb's flags", []string{"names", "--stoer", "x"}, 2,
			[]string{"unknown flag --stoer", "the flags of names are --as, --json, --max, --store", "run: nova-secrets names -h"}, []string{"flag provided but not defined"}},
		{"a bad value says what the flag wants", []string{"names", "--max", "lots"}, 2,
			[]string{"invalid value for --max", "it wants a whole number"}, []string{"lots", "parse error"}},
		{"a stray argument is named", []string{"check", "extra"}, 2, []string{`takes no positional arguments, got "extra"`, "run: nova-secrets help"}, nil},
		{"exec with nothing names the flags and the command at once", []string{"exec"}, 125,
			[]string{"--store <dir>", "--as <name>", "--key <path>", "--sops <path>", "--only <names|all>", "the command after '--'", "example: nova-secrets exec"},
			[]string{"delimiter", "rowan"}},
		{"exec's command before -- is told where it goes", []string{"exec", "--only", "all", "gh", "api"}, 125,
			[]string{`unexpected argument "gh" before '--'`, "the command goes after '--'"}, nil},
		{"the seat group names its subverbs", []string{"seat"}, 2, []string{"add", "inject", "run: nova-secrets seat -h"}, nil},
		{"version refuses with the status word", []string{"version", "x"}, 2, []string{"VERSION REFUSED: takes no positional arguments"}, nil},
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

// TestNamesJSONIsTheSameValueAndCarriesNoValue: names --json is the text form's value as
// one JSON object (names, counts, the MORE cut), a refusal included, and neither form
// carries a value, sealed or clear.
func TestNamesJSONIsTheSameValueAndCarriesNoValue(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	store := filepath.Join(t.TempDir(), "secrets")
	require.NoError(t, os.MkdirAll(store, 0o755))
	initGitStore(t, store)
	const clearValue, sealedValue = "qzxjwkvbnmplqzxj", "pwqzkxjvmbnlrtzk"
	for name, body := range map[string]string{
		".sops.yaml": "creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: age1x\n",
		"ada.yaml":   "A_TOKEN: ENC[AES256_GCM,data:" + sealedValue + ",type:str]\nB_USER: " + clearValue + "\nC_TOKEN: ENC[AES256_GCM,data:y,type:str]\nsops:\n    age: []\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(store, name), []byte(body), 0o644))
	}
	stdout, stderr, code := runNovaSecrets(bin, "names", "--store", store, "--as", "ada", "--max", "2", "--json")
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.JSONEq(t, `{"result":{"verb":"names","status":"ok","exit":0},
		"facts":{"as":"ada","keys":3,"shown":2,"sealed":2,"clear":1},
		"items":[{"kind":"key","fields":{"key":"A_TOKEN","clear":false}},{"kind":"key","fields":{"key":"B_USER","clear":true}}],
		"more":[{"kind":"key","shown":2,"total":3,"remedy":"--max <n> raises the ceiling, --max 0 lists all"}]}`, stdout)
	text, _, code := runNovaSecrets(bin, "names", "--store", store, "--as", "ada", "--max", "2")
	require.Equal(t, 0, code)
	assert.Contains(t, text, "SECRETS NAMES OK as=ada keys=3 shown=2 sealed=2 clear=1")
	for _, out := range []string{stdout, text} {
		for _, v := range []string{clearValue, sealedValue} {
			assert.False(t, secrets.Leaks(out, secrets.NewSecret(v)), "names printed a value: %s", out)
		}
	}

	stdout, _, code = runNovaSecrets(bin, "names", "--store", store, "--as", "bo", "--json")
	assert.Equal(t, 2, code)
	assert.Contains(t, stdout, `"status":"refused"`)
	assert.Contains(t, stdout, "its seats are ada")
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
