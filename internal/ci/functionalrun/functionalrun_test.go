package functionalrun

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEveryRunIDMatchesTheReapersPattern(t *testing.T) {
	t.Parallel()
	id := newRunID(time.Now())
	require.True(t, runIDRE.MatchString(id), "run id does not match runIDRE")
	require.True(t, runIDRE.MatchString(id+"-mod"), "run id with -mod does not match runIDRE")
}
