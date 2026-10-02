//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipeCounter counts the round trips a client makes: one per command sent
// alone, one per pipeline.
type pipeCounter struct{ trips int }

func (p *pipeCounter) DialHook(next redis.DialHook) redis.DialHook { return next }
func (p *pipeCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { p.trips++; return next(ctx, cmd) }
}
func (p *pipeCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { p.trips++; return next(ctx, cmds) }
}

// Snapshot reads what apply wrote, in two round trips: the machines with
// the ceiling as their slots, the fleet row, each machine's beat, and the
// loop views only once rev:loop is stamped. The inventory built from it is
// the one the plays read.
func TestSnapshotIsTheAppliedStateInTwoRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	sprintRow, _, sprintErr := st.Get(ctx, KindSprint, KindSprint)
	require.NoError(t, sprintErr)
	applyKinds(t, st, ap, sprintRow.Fields["coordinator"])
	machines, err := st.List(ctx, KindMachine)
	require.NoError(t, err)
	beatOf := machines[0].Name
	require.NoError(t, c.HSet(ctx, BeatKey(beatOf), "os", "linux", "arch", "amd64", "ncpu", "8").Err())

	count := &pipeCounter{}
	c.AddHook(count)
	snap, err := ap.Snapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count.trips)
	assert.Len(t, snap.Machines, len(machines))
	for _, m := range machines {
		assert.Equal(t, m.Fields["slots"], snap.Machines[m.Name]["slots"], m.Name)
		assert.Equal(t, m.Fields["user"], snap.Machines[m.Name]["user"], m.Name)
	}
	assert.Equal(t, &Beat{OS: "linux", Arch: "amd64", Cores: "8"}, snap.Beats[beatOf])
	_, applied := snap.Revs[KindLoop]
	assert.Equal(t, applied, snap.Loops != nil, "loops are read exactly when rev:loop is stamped")
	assert.NotZero(t, snap.Revs[KindMachine])
	assert.Equal(t, "6380", snap.Fleet["redis_port"])
	assert.Equal(t, "postgres://nova_config@localhost:5432/nova", snap.Fleet["pg_dsn"])

	require.NoError(t, c.SAdd(ctx, LoopsKey, "member-a").Err())
	require.NoError(t, c.HSet(ctx, LoopKey("member-a"), map[string]any{
		"name": "member-a", "machine": beatOf, "argv": `["nova-swarm","member"]`, "seat": "s", "keys": "",
		"every": "0", "keepalive": "true", "enabled": "true", "log": "~/nova-bench/loops/member-a.log",
	}).Err())
	require.NoError(t, c.HSet(ctx, DeclKey, "rev:"+KindLoop, "1").Err())
	snap, err = ap.Snapshot(ctx)
	require.NoError(t, err)
	require.Contains(t, snap.Loops, "member-a")
	inv, err := BuildInventory(snap, "")
	require.NoError(t, err)
	assert.Equal(t, "linux", inv.Meta.Hostvars[beatOf]["nova_os"])
	assert.Len(t, inv.Meta.Hostvars[beatOf]["nova_loops"], 1)
}
