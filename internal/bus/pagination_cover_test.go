package bus

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPaginationCoverCursorMismatchErrorNamesBothCursors pins the cursor-mismatch refusal
// as a type with one sentence: the token's cursor and the persisted cursor, in the shape
// the reply spec fixes, both from the method and from the page turn that constructs it.
func TestPaginationCoverCursorMismatchErrorNamesBothCursors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		token   string
		persist string
	}{
		{name: "two cursors are named in one sentence", token: "aaaa", persist: "bbbb"},
		{name: "the same cursor twice is still the sentence", token: "cccc", persist: "cccc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "--after names cursor "+tc.token+" and this reader's cursor is "+tc.persist, (&BodyCursorMismatchError{Token: tc.token, Persisted: tc.persist}).Error())
		})
	}

	items := []BodyItem{
		{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("a")},
		{Commit: paginationTwo, Path: "from-bo/b.md", Entry: OpenEntry{Path: "from-bo/b.md"}, Body: []byte("b")},
	}
	first, err := BodyPageFor(items, paginationRequest("", paginationBase, 1, 10))
	require.NoError(t, err, "the first page: %v", err)
	_, err = BodyPageFor(items, paginationRequest(first.Next, paginationTwo, 1, 10))
	require.Error(t, err, "a continuation over a moved cursor was accepted")
	var moved *BodyCursorMismatchError
	assert.ErrorAs(t, err, &moved, "the refusal is the type, not prose to match: %v", err)
	assert.Equal(t, paginationBase, moved.Token, "the refusal does not carry the token's cursor: %+v", moved)
	assert.Equal(t, paginationTwo, moved.Persisted, "the refusal does not carry the persisted cursor: %+v", moved)
	assert.Equal(t, "--after names cursor "+paginationBase+" and this reader's cursor is "+paginationTwo, err.Error(), "the refusal is not the one sentence: %v", err)
}

// TestPaginationCoverBodyContinuationDecodesASnapshotAndRefusesMalformedTokens pins the
// command-facing decoder: a page's own token opens into the snapshot identity that wrote
// it and the persisted cursor it carried, and a malformed token is refused, never guessed.
func TestPaginationCoverBodyContinuationDecodesASnapshotAndRefusesMalformedTokens(t *testing.T) {
	t.Parallel()

	items := []BodyItem{
		{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("a")},
		{Commit: paginationTwo, Path: "from-bo/b.md", Entry: OpenEntry{Path: "from-bo/b.md"}, Body: []byte("b")},
	}
	first, err := BodyPageFor(items, paginationRequest("", paginationBase, 1, 10))
	require.NoError(t, err, "the first page: %v", err)
	require.NotEmpty(t, first.Next, "the first page made no continuation")
	snapshot, expected, err := BodyContinuation(first.Next)
	require.NoError(t, err, "the page's own continuation: %v", err)
	assert.Equal(t, BodySnapshot{Base: paginationBase, Head: paginationTwo, Reader: "Ada", Selector: "inbox-new"}, snapshot, "the continuation does not open into the snapshot that wrote it")
	assert.Equal(t, paginationBase, expected, "the continuation lost the persisted cursor")

	other, err := encodeBodyToken(bodyToken{Version: 99})
	require.NoError(t, err, "the wrong-version token: %v", err)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "an empty token is refused", in: "", want: "continuation is malformed or over 8KiB"},
		{name: "a token that is not base64 is refused", in: "not a continuation", want: "continuation is malformed or over 8KiB"},
		{name: "a token of another version is refused", in: other, want: "continuation version 99 is not supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := BodyContinuation(tc.in)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// TestPaginationCoverSnapshotSourceUnwrapReachesTheInnerError pins the snapshot source
// wrapper: errors.Is and errors.As cross it, a direct Unwrap hands back the inner error,
// and the wrapper adds no prose of its own.
func TestPaginationCoverSnapshotSourceUnwrapReachesTheInnerError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		inner error
	}{
		{name: "a plain refusal survives the wrap", inner: errors.New("the blob is refused")},
		{name: "a sentinel refusal survives the wrap", inner: ErrBodyTokenNoItem},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped := snapshotSource(tc.inner)
			assert.ErrorIs(t, wrapped, tc.inner, "errors.Is does not reach the inner error")
			assert.Equal(t, tc.inner, errors.Unwrap(wrapped), "Unwrap does not hand back the inner error")
			var source *snapshotSourceError
			assert.ErrorAs(t, wrapped, &source, "errors.As does not cross the wrapper")
			assert.Equal(t, tc.inner, source.Unwrap(), "the wrapper's Unwrap lost the inner error")
			assert.Equal(t, tc.inner.Error(), wrapped.Error(), "the wrapper added prose of its own")
		})
	}
}
