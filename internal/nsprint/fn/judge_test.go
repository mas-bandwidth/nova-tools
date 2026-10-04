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
	if code, found := FromList(nil); found || code != "" {
		require.Failf(t, "assertion failed", "FromList(nil) = %q %v; want none", code, found)
	}
	libs := r.list(r.other("x"), r.our("ours"))
	if code, found := FromList(libs); !found || code != "ours" {
		require.Failf(t, "assertion failed", "FromList = %q %v; want ours", code, found)
	}
	if _, found := FromList(libs[:1]); found {
		require.False(t, found, "FromList found nova_sprint in a reply that holds only another library")
	}
}

func TestPingReplyIsTheReplyOrTheError(t *testing.T) {
	t.Parallel()
	if got := PingReply("PONG", nil); got != "PONG" {
		require.Equal(t, "PONG", got, "PingReply(PONG) = %q", got)
	}
	if got := PingReply(nil, errors.New("ERR Function not found")); got != "ERR Function not found" {
		require.Equal(t, "ERR Function not found", got, "PingReply(err) = %q", got)
	}
}
