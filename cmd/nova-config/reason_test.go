package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The --reason flag on every write (nova-tools#5101): add, set and remove
// record it in the history row, the history verb prints it, the dry run names
// it, and a reason with a line break is refused before the store opens.

// reasonHarness is a harness whose store the verbs may open, with the actor
// named on each command.
func reasonHarness() *harness {
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	return h
}

func TestTheReasonFlagIsRecordedOnEveryWriteAndShownByHistory(t *testing.T) {
	t.Parallel()

	h := reasonHarness()
	code, out, errs := h.run(t, "machine", "add", "box", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--as", "rowan", "--reason", "the only 64-core box left")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG ADD kind=machine name=box rev=1\n", "the reason leaves the result line alone")
	code, out, errs = h.run(t, "machine", "set", "box", "--width", "6", "--as", "rowan", "--reason", "resting for the measured load")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG SET kind=machine name=box rev=2 changed=width\n")
	code, out, _ = h.run(t, "machine", "history", "box")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `reason=the\x20only\x2064-core\x20box\x20left`, "the add's reason")
	assert.Contains(t, out, `reason=resting\x20for\x20the\x20measured\x20load`, "the set's reason")
	code, out, errs = h.run(t, "machine", "remove", "box", "--actor", "rowan", "--reason", "returned to the pool")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "CONFIG REMOVE kind=machine name=box rev=3\n", withoutDisposition(t, out))
	code, out, _ = h.run(t, "machine", "history", "box")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `reason=returned\x20to\x20the\x20pool`, "the remove's reason")
	assert.Equal(t, 3, strings.Count(out, "HISTORY id="), "the add, set and remove: %s", out)
}

func TestTheDryRunNamesTheReasonItWouldRecord(t *testing.T) {
	t.Parallel()

	h := reasonHarness()
	code, out, errs := h.run(t, "machine", "add", "box", "--user", "u", "--seat", "s", "--slots", "8", "--as", "rowan", "--reason", "measured, not guessed", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG DRY-RUN op=add kind=machine name=box actor=rowan wrote=nothing reason=measured,\\x20not\\x20guessed", "the dry run names the reason: %q", out)

	code, out, errs = h.run(t, "machine", "add", "box", "--user", "u", "--seat", "s", "--slots", "8", "--as", "rowan", "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.NotContains(t, out, "reason=", "a dry run with no reason prints none: %q", out)
}

func TestAReasonWithALineBreakIsRefusedBeforeTheStoreOpens(t *testing.T) {
	t.Parallel()

	h := reasonHarness()
	for _, args := range [][]string{
		{"machine", "add", "box", "--user", "u", "--seat", "s", "--slots", "8", "--as", "rowan", "--reason", "two\nlines"},
		{"machine", "set", "box", "--width", "4", "--as", "rowan", "--reason", "two\nlines"},
	} {
		code, out, errs := h.run(t, args...)
		assert.Equal(t, 2, code, "%v: %q %q", args, out, errs)
		assert.Equal(t, "", out, "%v", args)
		assert.Contains(t, errs, "--reason: want one line", "%v: %q", args, errs)
	}
	assert.Zero(t, h.opens, "a refused reason opened the store")
}
