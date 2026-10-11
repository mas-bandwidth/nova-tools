package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSONRefusalIsOneObject pins the --json refusal contract on every verb
// shape: a verb asked for --json that refuses answers with one JSON object on
// stdout, status refused, the run's own exit, and nothing of the refusal on
// stderr (docs/STANDARD.md, "One output structure, two renderings"; the
// pkg/testkit Contract probe).
func TestJSONRefusalIsOneObject(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.dir = t.TempDir()
	cases := []struct {
		name string
		args []string
	}{
		{name: "machine add names every missing field", args: []string{"machine", "add", "m2", "--file", "try.json"}},
		{name: "machine show names no row", args: []string{"machine", "show", "m1"}},
		{name: "machine self names no row", args: []string{"machine", "self", "--check"}},
		{name: "apply refuses with no fleet endpoints", args: []string{"apply", "--file", "try.json"}},
		{name: "an unknown flag is refused", args: []string{"machine", "list", "--no-such-flag-breadcrumb"}},
		{name: "a bare command is refused", args: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plainCode, _, plainErrs := h.run(t, tc.args...)
			require.NotEqual(t, 0, plainCode, "the plain run refuses: %s", plainErrs)

			code, out, errs := h.run(t, append(append([]string{}, tc.args...), "--json")...)
			assert.Equal(t, plainCode, code, "the JSON run exits as the plain one; stdout=%q stderr=%q", out, errs)

			var obj struct {
				Result struct {
					Status string `json:"status"`
					Exit   int    `json:"exit"`
				} `json:"result"`
			}
			dec := json.NewDecoder(strings.NewReader(out))
			require.NoError(t, dec.Decode(&obj), "--json stdout is one object: %q", out)
			assert.Equal(t, io.EOF, dec.Decode(&struct{}{}), "--json stdout holds exactly one object: %q", out)
			assert.Equal(t, "refused", obj.Result.Status, "--json status: %q", out)
			assert.Equal(t, plainCode, obj.Result.Exit, "--json exit: %q", out)
			assert.NotContains(t, errs, "REFUSED", "the refusal is the object, not a stderr line: %q", errs)
		})
	}
}
