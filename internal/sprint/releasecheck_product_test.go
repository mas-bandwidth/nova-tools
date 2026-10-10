package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReleaseCheckForNovaToolsScopesEveryCheckToItsStreamsAndHead(t *testing.T) {
	t.Parallel()
	product, err := FindReleaseProduct("nova-tools")
	require.NoError(t, err)
	require.Equal(t, "tools-v1-2-0-*", product.Streams)
	require.Equal(t, "sprint/mechanical-2026-10-02", product.ReleaseHead)
	lines, err := ReleaseStreamLines([]Line{{Stream: "tools-v1-2-0-a"}, {Stream: "sprint-v1-a"}}, product.Streams)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	require.Equal(t, "tools-v1-2-0-a", lines[0].Stream)

	green, err := RunReleaseChecksForProduct(relFacts(t0, lines...), nil, product)
	require.NoError(t, err)
	require.True(t, green.Ready)
	require.Equal(t, product.Name, green.Product)
	require.Equal(t, product.ReleaseHead, green.Head)
	require.Len(t, green.Results, len(ReleaseChecks))

	late := Line{Kind: LineMove, At: t0, Table: Fleet, Card: "tools-1.w1", Stream: "tools-v1-2-0-a", To: FriendRow("amy") + ":working"}
	red, err := RunReleaseChecksForProduct(relFacts(t0.Add(3*time.Hour), late), nil, product)
	require.NoError(t, err)
	require.False(t, red.Ready)
	require.Contains(t, red.Results[0].Line(), "tools-1.w1")
}
