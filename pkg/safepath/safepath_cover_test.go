package safepath

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cover-refused-error: (*Refused).Error renders "path: reason", the one line a
// caller reads back; it sat at 0.0% in the unit tier's per-function coverage
// table, reached by no test in the package.
func TestSafepathCoverRefusedErrorRendersPathAndReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		refused *Refused
		want    string
	}{
		{
			name:    "path and reason",
			refused: &Refused{Path: "/srv.test/root/slot/jobs/card-1", Reason: "the path is a symlink"},
			want:    "/srv.test/root/slot/jobs/card-1: the path is a symlink",
		},
		{
			name:    "empty path",
			refused: &Refused{Reason: "the path is empty"},
			want:    ": the path is empty",
		},
		{
			name:    "empty reason",
			refused: &Refused{Path: "/srv.test/root/x"},
			want:    "/srv.test/root/x: ",
		},
		{
			name:    "both empty",
			refused: &Refused{},
			want:    ": ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.refused.Error())
		})
	}
}

// cover-refused-error-through-a-seam: a refusal the package really returns --
// ResolvedUnder refusing a path with a ".." element before anything resolves --
// is the *Refused the caller reads back, and its Error() renders the path and
// the reason. No filesystem is touched: the ".." check fires first.
func TestSafepathCoverRefusedErrorFromAResolvedUnderRefusal(t *testing.T) {
	t.Parallel()

	_, err := ResolvedUnder("/srv.test/root/x/../y", "/srv.test/root")
	require.Error(t, err, `a path with a ".." element is refused`)
	var refused *Refused
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "/srv.test/root/x/../y", refused.Path)
	assert.Equal(t, `the path contains ".."`, refused.Reason)
	assert.Equal(t, `/srv.test/root/x/../y: the path contains ".."`, refused.Error())
}
