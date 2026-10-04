package bus

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// (*ParseError).Error is how a note that will not parse speaks: ReadBus wraps the
// parser's refusal in a ParseError, and check and inbox print exactly what Error returns.
// Nothing in the unit tier reached it, so it stood at 0.0% in the per-function table.
// These tests pin the delegation directly and the one refusal shape that reaches it
// through a real bus read, with no store, no subprocess and no clock.

// TestBusCoverParseErrorMessage pins Error's main path: the message of the wrapped
// error comes back whole, for a plain error and for one carrying %w context.
func TestBusCoverParseErrorMessage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"a plain refusal comes back unchanged", errors.New("line 1: not a header line (a header is Key: value): blank"), "line 1: not a header line (a header is Key: value): blank"},
		{"a wrapped refusal comes back with its context", fmt.Errorf("this file was open and is no longer on the bus: %w", errors.New("no such file")), "this file was open and is no longer on the bus: no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &ParseError{Err: tc.err}
			assert.Equal(t, tc.want, p.Error(), `Error() = %q, want the wrapped message %q`, p.Error(), tc.want)
		})
	}
}

// TestBusCoverParseErrorFromUnreadableNote pins the refusal that reaches Error through
// the package's own seams: a lane file whose header is markdown bold will not parse, so
// ReadBus hands it back as a Note whose Parse names the repair, and a file that does
// parse carries no ParseError at all.
func TestBusCoverParseErrorFromUnreadableNote(t *testing.T) {
	t.Parallel()
	root := writeBus(t, map[string]string{
		"from-ada/2026-09-09T0041Z-fine.md": "From: Ada\nTo: Bo\nSubject: fine\n\nBody.\n",
		"from-bo/2026-09-09T0042Z-bold.md":  "**To**: Bo\nSubject: markdown bold\n\nBody.\n",
	})
	tab := loadBus(t, root)

	fine, ok := tab.NoteByPath("from-ada/2026-09-09T0041Z-fine.md")
	require.True(t, ok, "the readable note fell off the bus")
	assert.Nil(t, fine.Parse, "a note that parses must carry no ParseError")

	bold, ok := tab.NoteByPath("from-bo/2026-09-09T0042Z-bold.md")
	require.True(t, ok, "the unreadable file fell off the bus instead of becoming a ParseError note")
	require.NotNil(t, bold.Parse, "a lane file whose header will not parse must carry a ParseError")
	assert.Contains(t, bold.Parse.Error(), "not markdown bold",
		`Error() = %q, want the parser's own refusal naming the markdown-bold repair`, bold.Parse.Error())
}
