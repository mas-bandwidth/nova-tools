package main

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// TestSendOKLineSaysLandedAndNotesAreCapped pins the reader's two complaints
// (docs/STANDARD.md, ONBOARDING point 4: one output structure; SPEC-BUS.md,
// bus-requires-inbox-push-proof): the OK line says whether the message landed,
// so a reader never takes a NOTE after it for a failed send, and each SEND NOTE
// is one bounded line naming the name, its push state and the one command that
// fixes it, never a several-hundred-character essay.
func TestSendOKLineSaysLandedAndNotesAreCapped(t *testing.T) {
	t.Parallel()

	// a real send to an unheard name: landed=yes on the OK line, and every NOTE
	// names the name, the push state and the runnable remedy, at most 160 bytes
	r := deafRig("ada", "bob")
	cli := r.cli()
	got := cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x").Exit(0)
	assert.Contains(t, got.Stdout, "SEND OK ")
	assert.Contains(t, got.Stdout, "landed=yes")
	assert.NotContains(t, got.Stdout, "landed=no")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "send wrote")
	notes := 0
	for _, line := range strings.Split(got.Stdout, "\n") {
		if !strings.Contains(line, "SEND NOTE") {
			continue
		}
		notes++
		assert.LessOrEqual(t, len(line), 160, "note must be capped: %s", line)
		assert.Contains(t, line, "push=none")
		assert.Contains(t, line, "nova-hook status --as ")
	}
	assert.Equal(t, 2, notes, "the sender and the recipient are each noted")

	// --dry-run: landed=no, the same bounded notes, and nothing written
	dry := cli.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "preview", "--dry-run").Exit(0)
	assert.Contains(t, dry.Stdout, "SEND OK ")
	assert.Contains(t, dry.Stdout, "landed=no")
	assert.NotContains(t, dry.Stdout, "landed=yes")
	assert.Contains(t, dry.Stdout, "dry_run=true")
	assert.Equal(t, 1, r.store.Len(bus.LogKey), "dry wrote nothing more")
	for _, line := range strings.Split(dry.Stdout, "\n") {
		if strings.Contains(line, "SEND NOTE") {
			assert.LessOrEqual(t, len(line), 160, "dry note must be capped: %s", line)
			assert.Contains(t, line, "nova-hook status --as ")
		}
	}

	// a name of the longest kind (bus.MaxName) still fits the cap, and the
	// command names it whole, so the note stays runnable rather than truncated
	long := strings.Repeat("a", bus.MaxName)
	rl := deafRig("ada", long)
	one := rl.cli().Do(t, "send", "--as", "ada", "--to", long, "--subject", "s", "--body", "x").Exit(0)
	assert.Contains(t, one.Stdout, "landed=yes")
	longNotes := 0
	for _, line := range strings.Split(one.Stdout, "\n") {
		if !strings.Contains(line, "SEND NOTE") {
			continue
		}
		longNotes++
		assert.LessOrEqual(t, len(line), 160, "long-name note must be capped: %s", line)
		assert.Contains(t, line, "nova-hook status --as ")
	}
	assert.GreaterOrEqual(t, longNotes, 1, "the long recipient is noted")
	assert.Contains(t, one.Stdout, "nova-hook status --as "+long, "the command names the whole name")

	// --dry-run against a store address nothing answers at: exit 2, the address
	// named, and the refusal says nothing was written
	r2 := newRig("ada", "bob")
	r2.env[RedisEnv] = "store.test:6379"
	r2.openErr = io.ErrUnexpectedEOF
	cli2 := r2.cli()
	ref := cli2.Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--dry-run").Exit(2)
	assert.Contains(t, ref.Stderr+ref.Stdout, "store.test:6379")
	assert.Contains(t, ref.Stderr+ref.Stdout, "nothing was written")
	require.NotContains(t, ref.Stdout, "Call.DryRun")
	assert.Equal(t, 0, r2.store.Len(bus.LogKey), "the refused dry run wrote nothing")
}
