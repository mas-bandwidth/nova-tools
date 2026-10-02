package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
