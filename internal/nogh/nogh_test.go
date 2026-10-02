package nogh

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPathFirst(t *testing.T) {
	t.Parallel()

	sep := string(os.PathListSeparator)
	got := PathFirst([]string{"A=1", "PATH=/x", "PATH="}, "/s")
	want := []string{"A=1", "PATH=/s" + sep + "/x", "PATH=/s"}
	require.Equal(t, strings.Join(want, "|"), strings.Join(got, "|"), "PathFirst = %q, want %q", got, want)
	got = PathFirst([]string{"A=1"}, "/s")
	require.Equal(t, "A=1|PATH=/s", strings.Join(got, "|"), "no PATH: %q", got)
	got = PathFirst([]string{"PATH=/x"}, "")
	require.Equal(t, "PATH=/x", got[0], "empty dir changed env: %q", got)
}
