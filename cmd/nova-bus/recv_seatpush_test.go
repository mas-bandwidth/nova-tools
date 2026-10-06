package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func TestProveCoordinatorBusStampsOnlyTheCoordinator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	kv := map[string]string{}
	get := func(_ context.Context, key string) (string, bool, error) {
		v, ok := kv[key]
		return v, ok, nil
	}
	set := func(_ context.Context, key, val string) error {
		kv[key] = val
		return nil
	}
	now := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	names := sprint.Names{}
	require.NoError(t, proveCoordinatorBus(ctx, get, set, "ada", now))
	_, ok := kv[names.Key(store.SeatPushKey("ada"))]
	require.False(t, ok, "no coordinator: nothing stamped")

	kv[names.Key("coordinator")] = "ada"
	kv[names.Key(store.SeatPushKey("ada"))] = `{"name":"ada","harness":"opencode","proven":"2026-10-06T19:00:00Z","pong_of":"n1"}`
	require.NoError(t, proveCoordinatorBus(ctx, get, set, "bey", now))
	assert.NotContains(t, kv[names.Key(store.SeatPushKey("ada"))], "bus_at", "a name who is not the coordinator wrote")

	require.NoError(t, proveCoordinatorBus(ctx, get, set, "ada", now))
	rec, err := sprint.DecodeSeatPushes(kv[names.Key(store.SeatPushKey("ada"))])
	require.NoError(t, err)
	assert.True(t, rec.BusAt.Equal(now), "bus_at %s", rec.BusAt)
	assert.Equal(t, "opencode", rec.Harness, "the judgments record stays")
	assert.Equal(t, "n1", rec.PongOf)
	assert.Contains(t, kv[names.Key(store.KeySeatPushers)], "ada")
}
