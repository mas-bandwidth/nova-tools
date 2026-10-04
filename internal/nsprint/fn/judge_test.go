package fn

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFromListFindsOnlyOurLibrary: FromList reads the nova_sprint entry out of
// a FUNCTION LIST reply and nothing else.
func TestFromListFindsOnlyOurLibrary(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	code, found := FromList(nil)
	require.False(t, found, "FromList(nil) = %q; want none", code)
	require.Empty(t, code)
	libs := r.list(r.other("x"), r.our("ours"))
	code, found = FromList(libs)
	require.True(t, found)
	require.Equal(t, "ours", code)
	_, found = FromList(libs[:1])
	require.False(t, found, "FromList found nova_sprint in a reply that holds only another library")
}

// TestPingReplyIsTheReplyOrTheError: PingReply answers the reply when the call
// worked and the error text when it did not.
func TestPingReplyIsTheReplyOrTheError(t *testing.T) {
	t.Parallel()
	require.Equal(t, "PONG", PingReply("PONG", nil), "PingReply(PONG)")
	require.Equal(t, "ERR Function not found", PingReply(nil, errors.New("ERR Function not found")), "PingReply(err)")
}
