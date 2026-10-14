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

	"github.com/mas-bandwidth/nova-tools/pkg/redisacl"
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

	// fn load and fn check success under --json print one object each, over a
	// fake store (the fn verbs dial a store, so the fake stands where connect's
	// connection stands).
	h := newFnHarness(t)
	lExit, lOut, lErr := h.run("fn", "load", "--redis", "127.0.0.1:6399", "--json")
	require.Equal(t, 0, lExit, lErr)
	require.Empty(t, lErr, "--json says everything on stdout")
	var lr result
	require.NoError(t, json.Unmarshal([]byte(lOut), &lr), "one JSON object: %s", lOut)
	require.Equal(t, "fn load", lr.Result.Verb)
	require.Equal(t, "ok", lr.Result.Status)

	cExit, cOut, cErr := h.run("fn", "check", "--redis", "127.0.0.1:6399", "--json")
	require.Equal(t, 0, cExit, cErr)
	require.Empty(t, cErr, "--json says everything on stdout")
	var cr result
	require.NoError(t, json.Unmarshal([]byte(cOut), &cr), "one JSON object: %s", cOut)
	require.Equal(t, "fn check", cr.Result.Verb)
	require.Equal(t, "ok", cr.Result.Status)

	// acl apply and acl check success under --json print one object each, over
	// a fake store: apply sets the users, then check reads them back as OK.
	f := &fakeACL{live: map[string]redisacl.Live{}, cat: redisacl.Catalog{"read": {"get"}, "write": {"set"}}}
	apExit, apOut, apErr := aclRun(t, f, append(append(append([]string{"apply"}, login4...), sourced...), "--json")...)
	require.Equal(t, 0, apExit, apErr)
	require.Empty(t, apErr, "--json says everything on stdout")
	var apr result
	require.NoError(t, json.Unmarshal([]byte(apOut), &apr), "one JSON object: %s", apOut)
	require.Equal(t, "acl apply", apr.Result.Verb)
	require.Equal(t, "ok", apr.Result.Status)

	acExit, acOut, acErr := aclRun(t, f, append(append([]string{"check"}, login4...), "--json")...)
	require.Equal(t, 0, acExit, acErr)
	require.Empty(t, acErr, "--json says everything on stdout")
	var acr result
	require.NoError(t, json.Unmarshal([]byte(acOut), &acr), "one JSON object: %s", acOut)
	require.Equal(t, "acl check", acr.Result.Verb)
	require.Equal(t, "ok", acr.Result.Status)
}
