package fn

import (
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestFromListFindsOnlyOurLibrary: FromList reads the nova_sprint entry out of
// a FUNCTION LIST reply and nothing else.
func TestFromListFindsOnlyOurLibrary(t *testing.T) {
	t.Parallel()
	if code, found := FromList(nil); found || code != "" {
		require.Failf(t, "assertion failed", "FromList(nil) = %q %v; want none", code, found)
	}
	libs := []redis.Library{{Name: "other", Code: "x"}, {Name: Library, Code: "ours"}}
	if code, found := FromList(libs); !found || code != "ours" {
		require.Failf(t, "assertion failed", "FromList = %q %v; want ours", code, found)
	}
	if _, found := FromList(libs[:1]); found {
		require.False(t, found, "FromList found nova_sprint in a reply that holds only another library")
	}
}
