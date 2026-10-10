package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC-NOVA-TABLE's batch contract is discoverable without a store, and the
// manifest printed in help passes the same validator as a real invocation.
func TestBatchHelpManifestPassesPreSendValidation(t *testing.T) {
	t.Parallel()
	code, out, errout := runTable("help", "batch")
	require.Equal(t, 0, code)
	require.Empty(t, errout)
	start := strings.Index(out, "\n{\n")
	require.NotEqual(t, -1, start)
	end := strings.Index(out[start:], "\nRead the current")
	require.NotEqual(t, -1, end)
	manifest, err := ntable.ValidateBatchManifestRaw([]byte(out[start : start+end]))
	require.NoError(t, err)
	assert.Equal(t, "create-b1", manifest.OperationID)
	require.Len(t, manifest.Members, 1)
	assert.True(t, manifest.Members[0].Expect.Absent)
	assert.Contains(t, out, "Retry the same manifest with the same operation_id")
	assert.Contains(t, out, "Optional keys: actor, props, prop_expect, prop_absent")
}

// ONBOARDING point 2: missing batch input leads straight to its grammar.
func TestBatchMissingManifestNamesLeafHelp(t *testing.T) {
	t.Parallel()
	code, out, errout := runTable("batch")
	assert.Equal(t, 2, code)
	assert.Empty(t, out)
	assert.Contains(t, errout, "(<manifest-file> | - | '<json>')")
	assert.Contains(t, errout, "; run: nova-table help batch")
}

// --idem remains an accepted flag; help distinguishes receipt metadata from
// the batch contract's operation_id replay guarantee.
func TestIdemHelpDisclaimsDeduplication(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help"}, {"create", "-h"}, {"row", "set", "-h"}, {"cell", "add", "-h"}} {
		code, out, errout := runTable(args...)
		require.Equal(t, 0, code)
		require.Empty(t, errout)
		assert.Contains(t, out, "--idem")
		assert.Contains(t, out, "does not deduplicate")
	}
}

// TestARefusalAskedForAsJSONIsJSON: a verb that takes --json answers its
// refusal as the one JSON object every nova tool's result is, on stdout, with
// the exit the refusal carries; a verb asked nothing of the kind keeps its
// line on stderr.
func TestARefusalAskedForAsJSONIsJSON(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		exit int
		why  string
		run  string
	}{
		{"batch, no manifest", []string{"batch", "--json"}, 2, "wants one manifest", "nova-table help batch"},
		{"batch, unknown flag", []string{"batch", "--json", "--bogus", "m.json"}, 2, "unknown flag --bogus", "nova-table help batch"},
		{"batch, a manifest that breaks a rule", []string{"batch", "--json", `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"2","operation_id":"x","members":[]}`}, 1, "", "nova-table batch -h"},
		{"member read, no ids", []string{"member", "read", "demo", "--json"}, 2, "wants a table and member IDs", "nova-table help member read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, out, errout := runTable(tc.args...)
			assert.EqualValues(t, tc.exit, code)
			assert.Empty(t, errout)
			var got struct {
				Result struct {
					Verb, Status, Remedy string
					Exit                 int
					Why                  []string
				}
			}
			require.NoError(t, json.Unmarshal([]byte(out), &got), "%q", out)
			assert.Equal(t, "refused", got.Result.Status)
			assert.Equal(t, tc.exit, got.Result.Exit)
			assert.Equal(t, tc.run, got.Result.Remedy)
			require.Len(t, got.Result.Why, 1)
			assert.Contains(t, got.Result.Why[0], tc.why)
			assert.Equal(t, 1, strings.Count(out, "\n"), "one object, one line: %q", out)
		})
	}
	code, out, errout := runTable("batch")
	assert.EqualValues(t, 2, code)
	assert.Empty(t, out)
	assert.True(t, strings.HasPrefix(errout, "BATCH REFUSED: wants one manifest"), "%q", errout)
}
