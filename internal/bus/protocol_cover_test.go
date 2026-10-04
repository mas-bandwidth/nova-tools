package bus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProtocolCoverHasTokenPrefix pins the one rule the stream split rests on: a
// line is named by a prefix only when the prefix ends on a token boundary, so
// "INBOX WALK" covers `INBOX WALK ...` but not `INBOX WALKER`. The carriage
// return a tool on Windows appends is trimmed first, so a trailing '\r' does
// not defeat the boundary.
func TestProtocolCoverHasTokenPrefix(t *testing.T) {
	t.Parallel()
	prefixes := []string{"INBOX WALK"}
	for _, tc := range []struct {
		name string
		line string
		want bool
	}{
		{name: "exact prefix is a token prefix", line: "INBOX WALK", want: true},
		{name: "prefix followed by a space is a token prefix", line: "INBOX WALK commits=1/1", want: true},
		{name: "a longer token sharing the prefix is refused", line: "INBOX WALKER", want: false},
		{name: "a trailing carriage return is trimmed before matching", line: "INBOX WALK\r", want: true},
		{name: "a line that shares no prefix is refused", line: "BUS OK", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, hasTokenPrefix(tc.line, prefixes))
		})
	}
}

// TestProtocolCoverIsProgress pins IsProgress against ProgressPrefixes: a line
// that is progress is dropped by every consumer, and a protocol line is not.
func TestProtocolCoverIsProgress(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		want bool
	}{
		{name: "an INBOX WALK with a payload is progress", line: "INBOX WALK commits=1/1 notes=0 elapsed=3ms", want: true},
		{name: "INBOX WALK alone is progress", line: "INBOX WALK", want: true},
		{name: "a protocol line is refused as progress", line: "INBOX OK ad=1", want: false},
		{name: "a word beginning with a progress token is refused", line: "INBOX WALKER", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsProgress(tc.line))
		})
	}
}

// TestProtocolCoverIsProtocol pins IsProtocol against both halves of the
// allow-list: a documented stdout prefix is protocol, a WAIT opener with fields
// is protocol, and a progress line -- or any token-boundary violation -- is not.
func TestProtocolCoverIsProtocol(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		want bool
	}{
		{name: "a one-word stdout prefix is protocol", line: "BUS INDEX sha=abc", want: true},
		{name: "a two-word stdout prefix is protocol", line: "INBOX OK ad=1", want: true},
		{name: "a WAIT opener carrying fields is protocol", line: "WAIT as=from-ada timeout=30s interval=5s cursor=abc", want: true},
		{name: "a progress line is refused as protocol", line: "INBOX WALK commits=1/1", want: false},
		{name: "a word beginning with a protocol token is refused", line: "INBOX WALKER", want: false},
		{name: "WAIT without its field is refused", line: "WAIT", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsProtocol(tc.line))
		})
	}
}
