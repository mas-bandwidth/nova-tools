package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEveryVerbTakesJSON holds the skeleton contract that every verb of this tool
// answers --json (docs/STANDARD.md §2, "Every verb accepts --json"): a refusal under
// --json is one JSON object on stdout with the verb's name and the refused status,
// and the verb's help lists --json, so no verb is left out of the shape.
func TestEveryVerbTakesJSON(t *testing.T) {
	t.Parallel()
	type result struct {
		Result struct {
			Verb   string   `json:"verb"`
			Status string   `json:"status"`
			Exit   int      `json:"exit"`
			Why    []string `json:"why"`
		} `json:"result"`
	}
	// refuse is the argv that names one refusal for each verb under --json. egress takes
	// a sub-verb first, so its refusal is a sub-verb's; the rest take the flag straight.
	refuse := map[string][]string{
		"check":    {"check", "--json", "--this-flag-does-not-exist"},
		"egress":   {"egress", "plan", "--json", "--this-flag-does-not-exist"},
		"policy":   {"policy", "--json", "--this-flag-does-not-exist"},
		"probe":    {"probe", "--json", "--this-flag-does-not-exist"},
		"reap":     {"reap", "--json", "--this-flag-does-not-exist"},
		"run":      {"run", "--json", "--this-flag-does-not-exist"},
		"version":  {"version", "--json", "--this-flag-does-not-exist"},
		"worktree": {"worktree", "--json", "--this-flag-does-not-exist"},
	}
	for _, verb := range verbNames {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			// one refusal under --json decodes as one object
			exit, out, errOut := runSandbox(t, refuse[verb]...)
			require.Equal(t, 2, exit, "stderr: %s", errOut)
			require.Empty(t, errOut, "--json says everything on stdout")
			var r result
			require.NoError(t, json.Unmarshal([]byte(out), &r), "one JSON object: %s", out)
			require.Equal(t, verb, r.Result.Verb)
			require.Equal(t, "refused", r.Result.Status)
			require.Equal(t, 2, r.Result.Exit)
			require.NotEmpty(t, r.Result.Why)

			// the verb's help lists --json, so every verb takes it
			hExit, help, _ := runSandbox(t, verb, "-h")
			require.Equal(t, 0, hExit, verb)
			require.Contains(t, help, "--json", "%s -h does not list --json", verb)
		})
	}
}

// runSandbox runs this binary's own entry point in process with the argv a caller types
// after `nova-sandbox` and a minimal environment, and answers its exit and streams.
func runSandbox(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, nil, &out, &errb, []string{"PATH=/usr/bin:/bin"})
	return code, out.String(), errb.String()
}
