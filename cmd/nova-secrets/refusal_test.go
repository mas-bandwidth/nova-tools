package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// TestEveryRefusalNamesItsWholeFix pins the refusals a cold reader meets first: each is
// one line at its exit code in the tool's one grammar (`SECRETS <VERB> REFUSED: <why>`,
// or `SECRETS REFUSED:` before there is a verb), names every problem at once and
// carries what to run next, so the next call is the fixed one (ONBOARDING points 1
// and 2). The tool runs in process: run is the whole of it.
func TestEveryRefusalNamesItsWholeFix(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		args []string
		code int
		want []string
		not  []string
	}{
		{"no verb", nil, 2, []string{"SECRETS REFUSED: no arguments is not an invocation", "run: nova-secrets help"}, nil},
		{"unknown verb lists the verbs", []string{"bogus"}, 2,
			[]string{`SECRETS REFUSED: unknown verb "bogus"`, "the verbs are exec, names, check, gate, keygen, place, placed, seal, seat add, seat inject, version", "run: nova-secrets help"}, nil},
		{"a verb with a space is quoted plain", []string{"no such"}, 2, []string{`unknown verb "no such"`}, []string{`\x20`}},
		{"unknown flag lists the verb's flags", []string{"names", "--stoer", "x"}, 2,
			[]string{"SECRETS NAMES REFUSED: unknown flag --stoer", "the flags of names are --as, --json, --max, --store; did you mean --store?", "run: nova-secrets names -h"}, []string{"flag provided but not defined"}},
		{"a bad value says what the flag wants", []string{"names", "--max", "lots"}, 2,
			[]string{"SECRETS NAMES REFUSED: invalid value for --max: it wants n names to list"}, []string{"lots", "parse error"}},
		{"a stray argument is named", []string{"check", "extra"}, 2, []string{`SECRETS CHECK REFUSED: unexpected argument "extra"`, "check takes flags only", "run: nova-secrets check -h"}, nil},
		{"exec with nothing names the flags and the command at once", []string{"exec"}, 125,
			[]string{"SECRETS EXEC REFUSED: missing --store <dir>", "--as <name>", "--key <path>", "--sops <path>", "--only <names|all>; no command after '--'", "example: nova-secrets exec"},
			[]string{"delimiter", "rowan", "the command after"}},
		{"exec's command before -- is told where it goes", []string{"exec", "--only", "all", "gh", "api"}, 125,
			[]string{`SECRETS EXEC REFUSED: unexpected argument "gh" before '--'`, "the command goes after '--'"}, nil},
		{"exec's unknown flag", []string{"exec", "--bogus", "--", "true"}, 125, []string{"SECRETS EXEC REFUSED: unknown flag --bogus", "run: nova-secrets exec -h"}, []string{"FAIL"}},
		{"the seat group names its subverbs", []string{"seat"}, 2, []string{"SECRETS SEAT REFUSED: seat takes a subverb", "add", "inject", "run: nova-secrets seat add -h"}, nil},
		{"seal's unknown flag", []string{"seal", "--bogus"}, 2, []string{"SECRETS SEAL REFUSED: unknown flag --bogus", "run: nova-secrets seal -h"}, []string{"FAIL"}},
		{"seal's missing flags", []string{"seal"}, 2, []string{"SECRETS SEAL REFUSED: missing --store <dir>", "run: nova-secrets seal -h"}, []string{"FAIL"}},
		{"seat add's missing flags", []string{"seat", "add"}, 2, []string{"SECRETS SEAT ADD REFUSED: ", "--pub", "run: nova-secrets seat add -h"}, []string{"FAIL"}},
		{"seat inject's missing flags", []string{"seat", "inject"}, 2, []string{"SECRETS SEAT INJECT REFUSED: ", "--from", "run: nova-secrets seat inject -h"}, []string{"FAIL"}},
		{"gate's missing flags", []string{"gate"}, 2, []string{"SECRETS GATE REFUSED: missing --store <dir>, --base <git ref>, --head <git ref>", "run: nova-secrets gate -h"}, nil},
		{"keygen's missing flags", []string{"keygen"}, 2, []string{"SECRETS KEYGEN REFUSED: ", "--key", "run: nova-secrets keygen -h"}, nil},
		{"place's missing flags", []string{"place"}, 2, []string{"SECRETS PLACE REFUSED: ", "--machine", "run: nova-secrets place -h"}, nil},
		{"placed's missing machine", []string{"placed"}, 2, []string{"SECRETS PLACED REFUSED: ", "--machine"}, nil},
		{"version refuses with the status word", []string{"version", "x"}, 2, []string{"SECRETS VERSION REFUSED: version takes no flags"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var stdoutB, stderrB strings.Builder
			code := run(c.args, strings.NewReader(""), &stdoutB, &stderrB)
			stdout, stderr := stdoutB.String(), stderrB.String()
			assert.Equal(t, c.code, code, "stderr: %s", stderr)
			assert.Empty(t, stdout)
			assert.Equal(t, 1, strings.Count(stderr, "\n"), "one line: %q", stderr)
			assert.Regexp(t, `^SECRETS ([A-Z]+ )*REFUSED: `, stderr, "the one grammar")
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

// TestNamesJSONRefusesInJSON: names promises a refusal in JSON when --json is asked, and
// that holds for the refusals the flag parse makes as well as the store's: one JSON
// object on stdout, status refused at exit 2, nothing on stderr. names is the one verb
// that takes --json.
func TestNamesJSONRefusesInJSON(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		args []string
		why  string
	}{
		{"a bad --max value", []string{"names", "--max", "oops", "--json"}, "invalid value for --max: it wants n names"},
		{"--json before the bad value", []string{"names", "--json", "--max", "oops"}, "invalid value for --max: it wants n names"},
		{"an unknown flag after --json", []string{"names", "--json", "--bogus"}, "unknown flag --bogus"},
		{"a stray argument", []string{"names", "--json", "extra"}, `unexpected argument \"extra\"`},
		{"the store's own refusal", []string{"names", "--json"}, "missing --store <dir>, --as <name>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr strings.Builder
			code := run(c.args, strings.NewReader(""), &stdout, &stderr)
			assert.Equal(t, 2, code)
			assert.Empty(t, stderr.String())
			assert.Equal(t, 1, strings.Count(stdout.String(), "\n"), "one JSON object: %q", stdout.String())
			assert.Contains(t, stdout.String(), `{"result":{"verb":"names","status":"refused","exit":2`)
			assert.Contains(t, stdout.String(), c.why)
		})
	}
}
