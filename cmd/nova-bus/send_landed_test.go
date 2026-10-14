package main

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

func TestSendOKLineSaysLandedAndNotesAreCapped(t *testing.T) {
	t.Parallel()
	r := deafRig("ada", "bob")
	cli := r.cli()

	// send to an unheard name: landed=yes, notes capped <=160 and name the hook status cmd
	got := cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x").Exit(0)
	assert.Contains(t, got.Stdout, "SEND OK ")
	assert.Contains(t, got.Stdout, "landed=yes")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "send wrote")
	for _, line := range strings.Split(got.Stdout, "\n") {
		if strings.Contains(line, "SEND NOTE") {
			assert.LessOrEqual(t, len(line), 160, "note must be capped: %s", line)
			assert.Contains(t, line, "push=")
			assert.Contains(t, line, " for ")
			assert.Contains(t, line, "nova-hook status --as ")
		}
	}

	// --dry-run: landed=no, still notes but nothing written
	dry := cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "preview", "--dry-run").Exit(0)
	assert.Contains(t, dry.Stdout, "SEND OK ")
	assert.Contains(t, dry.Stdout, "landed=no")
	assert.Contains(t, dry.Stdout, "dry_run=true")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "dry wrote nothing more")
	for _, line := range strings.Split(dry.Stdout, "\n") {
		if strings.Contains(line, "SEND NOTE") {
			assert.LessOrEqual(t, len(line), 160, "dry note must be capped: %s", line)
			assert.Contains(t, line, "nova-hook status --as ")
		}
	}

	// --dry-run against unreachable store: exit 2, names the address, says nothing was written
	r2 := newRig("ada", "bob")
	r2.env[RedisEnv] = "store.test:6379"
	r2.openErr = io.ErrUnexpectedEOF
	cli2 := r2.cli()
	ref := cli2.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--dry-run").Exit(2)
	assert.Contains(t, ref.Stderr+ref.Stdout, "store.test:6379")
	assert.Contains(t, ref.Stderr+ref.Stdout, "nothing was written")
	require.NotContains(t, ref.Stdout, "Call.DryRun")
}
