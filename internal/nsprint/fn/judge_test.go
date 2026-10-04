package fn

import (
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
