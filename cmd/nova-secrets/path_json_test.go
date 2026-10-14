package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryVerbTakesJSONAndTheExampleSetsEveryVariable pins the two halves this
// card still owes (docs/SPEC-SECRETS.md `--json`, and the banner example).
// --sops and --age-keygen stay absolute paths from a flag (SPEC-SECRETS,
// those flags): this test does not look for them on PATH.
func TestEveryVerbTakesJSONAndTheExampleSetsEveryVariable(t *testing.T) {
	t.Parallel()

	t.Run("every verb refuses in JSON", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			verb string
			args []string
			exit int
		}{
			{"exec", []string{"exec", "--json"}, 125},
			{"names", []string{"names", "--json"}, 2},
			{"check", []string{"check", "--json"}, 2},
			{"gate", []string{"gate", "--json"}, 2},
			{"keygen", []string{"keygen", "--json"}, 2},
			{"place", []string{"place", "--json"}, 2},
			{"placed", []string{"placed", "--json"}, 2},
			{"seal", []string{"seal", "--json"}, 2},
			{"seat add", []string{"seat", "add", "--json"}, 2},
			{"seat inject", []string{"seat", "inject", "--json"}, 2},
		} {
			c := c
			t.Run(c.verb, func(t *testing.T) {
				t.Parallel()
				var stdout, stderr strings.Builder
				code := run(c.args, strings.NewReader(""), &stdout, &stderr)
				assert.Equal(t, c.exit, code, "stderr: %s stdout: %s", stderr.String(), stdout.String())
				assert.Empty(t, stderr.String(), "a --json refusal is one object on stdout, nothing on stderr")
				assert.Equal(t, 1, strings.Count(stdout.String(), "\n"), "one JSON object: %q", stdout.String())
				var body struct {
					Result struct {
						Verb   string   `json:"verb"`
						Status string   `json:"status"`
						Exit   int      `json:"exit"`
						Why    []string `json:"why"`
					} `json:"result"`
				}
				require.NoError(t, json.Unmarshal([]byte(stdout.String()), &body), "stdout: %s", stdout.String())
				assert.Equal(t, c.verb, body.Result.Verb)
				assert.Equal(t, "refused", body.Result.Status)
				assert.Equal(t, c.exit, body.Result.Exit)
				why := strings.Join(body.Result.Why, " ")
				assert.Contains(t, why, "missing", "the verb's own refusal, not a flag the verb does not know: %s", why)
				assert.NotContains(t, stdout.String(), "unknown flag", " --json must be the verb's flag: %s", stdout.String())
			})
		}
	})

	t.Run("the example sets every variable", func(t *testing.T) {
		t.Parallel()
		_, block, found := strings.Cut(usage, "\nexample:\n")
		require.True(t, found, "the banner has no example block")
		uses := exampleVarUses(block)
		require.NotEmpty(t, uses, "the example names no variable; seat add's public key is unset")
		for _, u := range uses {
			assert.Truef(t, exampleAssignsBefore(block, u.name, u.at),
				"$%s is used in the example and no earlier line of the same example sets it", u.name)
		}
		for _, flag := range []string{"--sops", "--age-keygen"} {
			for _, val := range exampleFlagValues(block, flag) {
				assert.Truef(t, strings.HasPrefix(val, "/"),
					"%s must be an absolute path, got %q", flag, val)
			}
			assert.NotEmpty(t, exampleFlagValues(block, flag), "the example names no %s", flag)
		}
	})

	t.Run("one verb's JSON holds no planted secret", func(t *testing.T) {
		t.Parallel()
		h := newSittingHome(t)
		const planted = "planted-secret-value-9f3a"
		require.True(t, filepath.IsAbs(h.sops), "the fake sops is handed by absolute path, not looked up on PATH")
		writeFakeExe(t, h.sops, "#!/bin/sh\ncase \"$1\" in\n--version) echo 'sops 3.13.3'; exit 0 ;;\n"+
			"-d) printf 'GH_TOKEN: "+planted+"\\n'; exit 0 ;;\nesac\nexit 0\n")
		store := filepath.Join(h.home, "secrets")
		key := filepath.Join(h.home, ".config", "nova-secrets", "ada.key")
		var stdout, stderr strings.Builder
		code := run([]string{"check", "--json", "--store", store, "--as", "ada", "--key", key, "--sops", h.sops},
			strings.NewReader(""), &stdout, &stderr)
		out := stdout.String()
		assert.NotContains(t, out, planted, "check --json printed the planted secret: %s", out)
		assert.NotContains(t, out, "unknown flag")
		assert.Empty(t, stderr.String(), "stderr: %s", stderr.String())
		var body struct {
			Result struct {
				Status string `json:"status"`
				Verb   string `json:"verb"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &body), "stdout: %s code %d", out, code)
		assert.Equal(t, "check", body.Result.Verb)
		assert.NotEqual(t, "refused", body.Result.Status, "a planted store and an absolute sops should run, not refuse: %s", out)
		assert.NotContains(t, out, planted)
	})
}

type exampleVarUse struct {
	name string
	at   int
}

func exampleVarUses(block string) []exampleVarUse {
	re := regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	var out []exampleVarUse
	for _, m := range re.FindAllStringSubmatchIndex(block, -1) {
		name := ""
		switch {
		case m[2] >= 0:
			name = block[m[2]:m[3]]
		case m[4] >= 0:
			name = block[m[4]:m[5]]
		}
		if name != "" {
			out = append(out, exampleVarUse{name, m[0]})
		}
	}
	return out
}

func exampleAssignsBefore(block, name string, useAt int) bool {
	re := regexp.MustCompile(`(?:^|[^A-Za-z0-9_])` + regexp.QuoteMeta(name) + `=`)
	for _, m := range re.FindAllStringIndex(block, -1) {
		if m[0] < useAt {
			return true
		}
	}
	return false
}

func exampleFlagValues(block, flag string) []string {
	var vals []string
	for _, line := range strings.Split(block, "\n") {
		fields := strings.Fields(line)
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] == flag {
				vals = append(vals, fields[i+1])
			}
		}
	}
	return vals
}
