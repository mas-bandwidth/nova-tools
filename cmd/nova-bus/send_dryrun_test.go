package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendDryRunHandlesCall: card bus-send-dry-run-handles-call-dryrun.
// send --dry-run validates and prints a simulated envelope with
// dry_run=true and writes nothing; a store that does not answer under
// --dry-run keeps its own refusal instead of the Call.DryRun mask.
func TestSendDryRunHandlesCall(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()

	res := cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--dry-run", "--json")
	require.Equal(t, 0, res.Code, "%s%s", res.Stdout, res.Stderr)
	var got struct {
		Facts map[string]any `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got), res.Stdout)
	assert.Equal(t, true, got.Facts["dry_run"])
	assert.Equal(t, 0, r.store.Len(bus.LogKey), "a dry send writes nothing")

	down := newRig("ada", "bob")
	down.openErr = errors.New("dial failed: connection refused")
	refused := down.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--dry-run")
	require.Equal(t, 2, refused.Code, "%s%s", refused.Stdout, refused.Stderr)
	assert.Contains(t, refused.Stderr+refused.Stdout, "dial failed")
	assert.NotContains(t, refused.Stderr+refused.Stdout, "never read it")
}
