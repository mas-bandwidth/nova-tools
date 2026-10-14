package main

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendOKLineSaysLandedAndNotesAreCapped pins the three reader findings of
// 2026-10-14 (v1.2.22): the OK line says whether the message is on the stream
// (landed=yes, or landed=no under --dry-run); each SEND NOTE is one line of at
// most 160 characters naming the name, its push state and the one command that
// fixes it (nova-hook status --as <name>); and a --dry-run that cannot reach
// the store is refused at exit 2 naming the address and saying nothing was
// written. The long explanation stays in `nova-bus send -h` (SPEC-BUS.md,
// bus-requires-inbox-push-proof; STANDARD.md, a verb that writes has a dry run).
func TestSendOKLineSaysLandedAndNotesAreCapped(t *testing.T) {
	t.Parallel()

	t.Run("a landed send says landed=yes and every note is one capped line", func(t *testing.T) {
		t.Parallel()
		r := deafRig("ada", "bob")
		got := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x").Exit(0)

		ok := okLine(t, got.Stdout)
		assert.Contains(t, ok, "landed=yes", "the OK line says the message is on the stream: %q", ok)
		assert.NotContains(t, ok, "landed=no")

		notes := noteLines(got.Stdout)
		require.NotEmpty(t, notes, "an unheard name is noted")
		for _, n := range notes {
			assert.LessOrEqual(t, len(n), 160, "NOTE over 160 characters: %q", n)
			assert.Contains(t, n, "push=", "the NOTE names the push state: %q", n)
			assert.Contains(t, n, "nova-hook status --as "+noteName(t, n),
				"the NOTE names the one command that fixes it: %q", n)
		}
	})

	t.Run("a dry send says landed=no", func(t *testing.T) {
		t.Parallel()
		r := deafRig("ada", "bob")
		got := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--dry-run").Exit(0)

		ok := okLine(t, got.Stdout)
		assert.Contains(t, ok, "landed=no", "a dry run wrote nothing, so it did not land: %q", ok)
		assert.NotContains(t, ok, "landed=yes")
	})

	t.Run("a dry send to a store nothing answers at is refused naming the address", func(t *testing.T) {
		t.Parallel()
		r := deafRig("ada", "bob")
		r.openErr = io.ErrUnexpectedEOF
		addr := r.env[RedisEnv]
		got := r.cli().Do(t, "send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "x", "--dry-run").Exit(2)

		assert.Contains(t, got.Stderr, "SEND REFUSED")
		assert.Contains(t, got.Stderr, addr, "the refusal names the store it could not reach")
		assert.Contains(t, got.Stderr, "nothing was written", "a dry run that could not reach the store wrote nothing")
	})
}

// okLine is the first SEND OK line of stdout.
func okLine(t *testing.T, stdout string) string {
	t.Helper()
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "SEND OK ") {
			return l
		}
	}
	require.Fail(t, "no SEND OK line", stdout)
	return ""
}

// noteLines is every SEND NOTE line of stdout.
func noteLines(stdout string) []string {
	var out []string
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "SEND NOTE ") {
			out = append(out, l)
		}
	}
	return out
}

// noteName is the name a SEND NOTE line names.
func noteName(t *testing.T, note string) string {
	t.Helper()
	_, rest, ok := strings.Cut(note, " for ")
	require.True(t, ok, "no name in NOTE: %q", note)
	name, _, _ := strings.Cut(rest, ":")
	return name
}
