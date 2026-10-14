package main

// json_every_verb_test.go holds the skeleton contract that every verb of
// nova-redis answers --json (docs/STANDARD.md §2, "One output structure, two
// renderings"): a refusal under --json is one JSON object on stdout with the
// verb's name and the refused status, and the verb's help lists --json, so no
// verb is left out of the shape.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEveryVerbTakesJSON: every verb of the tool takes --json, so a bad flag
// under --json is one object on stdout and the verb's own help says it takes
// the flag. It runs with no store: the refusal comes before the skeleton runs
// the verb, so a fake store is never dialled.
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
	for _, v := range redisTool(deps{}).Verbs {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			words := strings.Fields(v.Name)

			// one refusal under --json decodes as one object on stdout
			args := append(append([]string{}, words...), "--json", "--this-flag-does-not-exist")
			exit, out, errOut := coldRun(t, args...)
			require.Equal(t, 2, exit, "stderr: %s", errOut)
			require.Empty(t, errOut, "--json says everything on stdout")
			var r result
			require.NoError(t, json.Unmarshal([]byte(out), &r), "one JSON object: %s", out)
			require.Equal(t, v.Name, r.Result.Verb)
			require.Equal(t, "refused", r.Result.Status)
			require.Equal(t, 2, r.Result.Exit)
			require.NotEmpty(t, r.Result.Why)

			// the verb's help lists --json, so every verb takes it
			hExit, help, hErr := coldRun(t, append(append([]string{}, words...), "-h")...)
			require.Equal(t, 0, hExit, hErr)
			require.Contains(t, help, "--json", "%s -h does not list --json", v.Name)
		})
	}

	// a store-free success under --json prints exactly one object: acl render
	// opens no store, so it is the one success every run can reach. The object
	// carries the verb's name and the ok status (pkg/tool, one output
	// structure, two renderings).
	sExit, sOut, sErr := coldRun(t, "acl", "render", "--json")
	require.Equal(t, 0, sExit, sErr)
	require.Empty(t, sErr, "--json says everything on stdout")
	var ok result
	require.NoError(t, json.Unmarshal([]byte(sOut), &ok), "one JSON object: %s", sOut)
	require.Equal(t, "acl render", ok.Result.Verb)
	require.Equal(t, "ok", ok.Result.Status)
}
