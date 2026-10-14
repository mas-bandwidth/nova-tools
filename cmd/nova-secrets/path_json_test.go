package main

// path_json_test.go holds the contract that every verb of nova-secrets answers
// --json (docs/SPEC-SECRETS.md "Output grammar", one line per event and the same
// value's other rendering) and that the banner's runnable block assigns every
// shell variable it uses. A refusal asked for as --json is one JSON object on
// stdout with the verb's own reason and exit, never an unknown-flag refusal; and
// no --json object carries a value, because the lines it renders never do.

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// secretsVerbs are the verbs of nova-secrets whose --json this test holds
// (help and version aside, as the standard's "every verb accepts --json").
var secretsVerbs = []string{
	"exec", "names", "check", "gate", "keygen", "place", "placed", "seal", "seat add", "seat inject",
}

// jsonResult is the result envelope of the one output value (tool.Out).
type jsonResult struct {
	Result struct {
		Verb   string   `json:"verb"`
		Status string   `json:"status"`
		Exit   int      `json:"exit"`
		Remedy string   `json:"remedy"`
		Why    []string `json:"why"`
	} `json:"result"`
	Facts map[string]any `json:"facts"`
}

func TestEveryVerbTakesJSONAndTheExampleSetsEveryVariable(t *testing.T) {
	t.Parallel()

	// (a) Every verb parses --json with its other flags missing and answers with
	// its own refusal as one JSON object, not the flag parser's unknown-flag
	// refusal. run is the whole tool on its streams, so this drives it in process.
	for _, verb := range secretsVerbs {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			args := append(strings.Fields(verb), "--json")
			var stdout, stderr strings.Builder
			code := run(args, strings.NewReader(""), &stdout, &stderr)
			require.NotZero(t, code, "a refusal exits non-zero: %q", stdout.String())
			require.Empty(t, stderr.String(), "--json says everything on stdout")
			require.Equal(t, 1, strings.Count(stdout.String(), "\n"), "one JSON object: %q", stdout.String())
			var r jsonResult
			require.NoError(t, json.Unmarshal([]byte(stdout.String()), &r), "the object is JSON: %q", stdout.String())
			assert.Equal(t, verb, r.Result.Verb)
			assert.Equal(t, "refused", r.Result.Status)
			assert.Equal(t, code, r.Result.Exit)
			assert.NotEmpty(t, r.Result.Why, "the refusal carries its reason")
			assert.NotContains(t, strings.ToLower(stdout.String()), "unknown flag",
				"--json is a flag of %s, not an unknown flag", verb)
		})
	}

	// (b) Every shell variable the banner's runnable block references is assigned
	// earlier in the same block: sops and age-keygen once by absolute path (the
	// law: PATH is the attacker's guess), and the new seat's public key from the
	// key file its own keygen line writes.
	require.NoError(t, exampleAssignsEveryVariable(t), "the banner references a variable it does not set")
	require.NoError(t, exampleUsesTheAssignedBinaries(t), "a later line hardcodes a path or looks a binary up on PATH")

	// (c) A verb's --json over a planted store with a fake sops at an absolute
	// path never prints the planted value: a value never appears in JSON any more
	// than in the lines.
	f := newPlaceFixture(t)
	var stdout, stderr strings.Builder
	args := append(f.placeArgs("mini", "DEEPSEEK_API_KEY", f.remotePath), "--json")
	code := run(args, strings.NewReader(""), &stdout, &stderr)
	require.Zero(t, code, "place --json: stderr: %s", stderr.String())
	require.NotContains(t, stdout.String(), f.value, "the planted value reached the JSON: %s", stdout.String())
	require.NotContains(t, stderr.String(), f.value, "the planted value reached a stream: %s", stderr.String())
	var r jsonResult
	require.NoError(t, json.Unmarshal([]byte(stdout.String()), &r), "one JSON object: %s", stdout.String())
	assert.Equal(t, "place", r.Result.Verb)

	// (d) exec --json on a launch that succeeds forks and waits, then prints one
	// JSON object with the child's exit and none of the child's output
	// (docs/SPEC-SECRETS.md "Output grammar"). A subprocess, so an execve
	// regression replaces that process and this assertion sees the child's
	// output instead of the object.
	t.Run("exec launch", func(t *testing.T) {
		t.Parallel()
		assertExecJSONWaits(t)
	})
}

// assignmentRe matches a shell assignment's name at the start of a word.
var assignmentRe = regexp.MustCompile(`(?:^|[\s;&|(])([A-Za-z_][A-Za-z0-9_]*)=`)

// variableRe matches a $NAME reference, not $( command substitution or $1.
var variableRe = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)

// exampleAssignsEveryVariable reads the banner's runnable block, from its
// `setup:` heading through the `example:` block, and returns an error naming a
// variable each referenced but not assigned earlier.
func exampleAssignsEveryVariable(t *testing.T) error {
	t.Helper()
	_, tail, found := strings.Cut(usage, "\nsetup:")
	require.True(t, found, "the banner has no `setup:` block")
	assigned := map[string]bool{}
	var missing []string
	for _, raw := range strings.Split(tail, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, m := range assignmentRe.FindAllStringSubmatch(line, -1) {
			assigned[m[1]] = true
		}
		for _, m := range variableRe.FindAllStringSubmatch(line, -1) {
			if !assigned[m[1]] {
				missing = append(missing, m[1]+" in "+line)
			}
		}
	}
	if len(missing) > 0 {
		return assert.AnError
	}
	return nil
}

// exampleUsesTheAssignedBinaries pins the banner's law for the two binaries:
// after the setup assignments, every --sops and --age-keygen uses $SOPS or
// $AGE_KEYGEN, and no line looks either up on PATH (docs/SPEC-SECRETS.md
// "--sops <path> is the sops binary").
func exampleUsesTheAssignedBinaries(t *testing.T) error {
	t.Helper()
	_, tail, found := strings.Cut(usage, "\nsetup:")
	require.True(t, found, "the banner has no `setup:` block")
	heading, _, _ := strings.Cut(tail, "\n")
	assert.NotContains(t, heading, "on PATH", "the setup heading must not send a reader to PATH: %s", heading)
	assert.NotContains(t, tail, "command -v", "the runnable block looks a binary up on PATH")
	assert.NotContains(t, tail, "--sops /opt/", "a later line hardcodes a sops path")
	for _, raw := range strings.Split(tail, "\n") {
		line := strings.TrimSpace(raw)
		if strings.Contains(line, "--age-keygen") {
			assert.Contains(t, line, "$AGE_KEYGEN", "age-keygen is not the assigned variable: %s", line)
		}
		if strings.Contains(line, "--sops") {
			assert.Contains(t, line, "$SOPS", "sops is not the assigned variable: %s", line)
		}
	}
	return nil
}

// assertExecJSONWaits runs exec --json against a planted store and a child that
// prints the planted value and exits 7. The object names the launch and that
// exit; the value is absent (docs/SPEC-SECRETS.md "Output grammar").
func assertExecJSONWaits(t *testing.T) {
	t.Helper()
	h := newSittingHome(t)
	child := filepath.Join(t.TempDir(), "show")
	writeFakeExe(t, child, "#!/bin/sh\nprintf '%s\\n' \"$GH_TOKEN\"\nexit 7\n")
	stdout, stderr, code := runNovaSecrets(h.bin,
		"exec", "--json",
		"--store", filepath.Join(h.home, "secrets"),
		"--as", "ada",
		"--key", filepath.Join(h.home, ".config", "nova-secrets", "ada.key"),
		"--sops", h.sops,
		"--only", "GH_TOKEN",
		"--", child,
	)
	require.Equal(t, 7, code, "exec --json exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	require.NotContains(t, stdout, "gh-value", "the child's output reached stdout: %s", stdout)
	require.NotContains(t, stderr, "gh-value", "the child's output reached stderr: %s", stderr)
	require.Equal(t, 1, strings.Count(stdout, "\n"), "one JSON object: %q", stdout)
	var r jsonResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &r), "the object is JSON: %s", stdout)
	assert.Equal(t, "exec", r.Result.Verb)
	assert.Equal(t, "ok", r.Result.Status)
	assert.Equal(t, 7, r.Result.Exit)
	assert.Equal(t, "1", r.Facts["only"], "the names passed are the launch's only count: %v", r.Facts)
}
