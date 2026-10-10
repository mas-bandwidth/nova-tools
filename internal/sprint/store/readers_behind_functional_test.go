//go:build functional

package store

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// The readers are behind on a real store (readers_behind_test.go): the flash route as
// nova-config's apply writes it, the reader added to the readers table, and the same drive,
// its judgments read from the Redis.
func TestRedisTheTickRaisesReadersBehindWhenReadsWaitTheWindow(t *testing.T) {
	t.Parallel()
	h, c := liveHarness(t)
	require.NoError(t, c.SAdd(h.ctx, config.RoutesKey, "flash-a").Err())
	require.NoError(t, c.HSet(h.ctx, config.RouteKey("flash-a"), "name", "flash-a", "tier", "flash", "provider", "prov-flash-a", "model", "model-flash-a",
		"tokens", "1000", "deadline", "900", "enabled", "true").Err())
	require.NoError(t, h.st.B.RowsAdd(h.ctx, h.st.Names.Table(sprint.Readers), []string{"reader-m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	readersBehind(h, func(typ string) []sprint.Open { return liveOpen(h, typ) }, func(typ string) int { return liveWritten(h, typ) })
}
