//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The note column against the package's throwaway Postgres and Redis: the
// migration adds it to routes and machines with every old row at '', a note
// round-trips and is in the history row, the refusal holds in the store, and
// apply leaves the note in the view the sprint reads.

// TestMigrationFifteenAddsTheNoteEmptyToEveryOldRow: 0015 adds config.routes.note
// and config.machines.note, NOT NULL DEFAULT ”, so a row made before it reads
// back with an empty note, and the file run again keeps a note written since.
func TestMigrationFifteenAddsTheNoteEmptyToEveryOldRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := OpenPG(ctx, server.Database(t))
	require.NoError(t, err)
	defer st.Close()
	all, err := Migrations()
	require.NoError(t, err)
	var fifteen Migration
	for _, m := range all {
		if m.Version == 15 {
			fifteen = m
			break
		}
		require.NoError(t, st.applyOne(ctx, m), "migration %s", m.Name)
	}
	require.Equal(t, "0015_row_notes.sql", fifteen.Name)
	for _, q := range []string{
		`INSERT INTO config.machines (name, "user", seat, slots, runners) VALUES ('m1', 'u', 's', 8, 0)`,
		`INSERT INTO config.routes (name, tier, provider, model, deadline, enabled) VALUES ('r1', 'flash', 'p', 'm', 60, false)`,
	} {
		_, err = st.db.ExecContext(ctx, q)
		require.NoError(t, err, q)
	}
	require.NoError(t, st.applyOne(ctx, fifteen))
	for _, m := range all[15:] { // the row is read with the kind as it is now
		require.NoError(t, st.applyOne(ctx, m), "migration %s", m.Name)
	}
	m1, found, err := st.Get(ctx, KindMachine, "m1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "", m1.Fields["note"], "the backfill is nothing")
	r1, _, err := st.Get(ctx, KindRoute, "r1")
	require.NoError(t, err)
	assert.Equal(t, "", r1.Fields["note"], "an old disabled route reads back with no note; the seed script writes its reason")
	for _, table := range []string{"machines", "routes"} {
		var nullable, def string
		require.NoError(t, st.db.QueryRowContext(ctx, `SELECT is_nullable, column_default FROM information_schema.columns WHERE table_schema = 'config' AND table_name = $1 AND column_name = 'note'`, table).Scan(&nullable, &def))
		assert.Equal(t, "NO", nullable, table)
		assert.Equal(t, "''::text", def, table)
	}
	_, _, err = st.Update(ctx, KindMachine, "m1", map[string]string{"note": "held"}, "t")
	require.NoError(t, err)
	_, err = st.db.ExecContext(ctx, fifteen.SQL)
	require.NoError(t, err, "the file run again")
	m1, _, _ = st.Get(ctx, KindMachine, "m1")
	assert.Equal(t, "held", m1.Fields["note"], "the file run again overwrote a note written since")
}

func TestPostgresNoteRoundTripsIsInTheHistoryAndTheRuleHolds(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := migrated(t)
	route := newRoute(t, "flash-a", map[string]string{"tier": "flash", "provider": "p", "model": "m", "deadline": "600"})
	_, err := st.Insert(ctx, KindRoute, route, "rowan")
	require.NoError(t, err)
	machine, _ := Lookup(KindMachine)
	m, err := machine.NewRow("superman", map[string]string{"user": "u", "seat": "s", "slots": "8", "width": "8", "note": "a first note"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, m, "rowan")
	require.NoError(t, err)
	got, _, err := st.Get(ctx, KindMachine, "superman")
	require.NoError(t, err)
	assert.Equal(t, "a first note", got.Fields["note"])

	// the reason rule is the store's, not only the CLI's
	_, _, err = st.Update(ctx, KindRoute, "flash-a", map[string]string{"enabled": "false"}, "rowan")
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "say why: --note '<the measured reason>'")
	_, _, err = st.Update(ctx, KindRoute, "flash-a", map[string]string{"enabled": "false", "note": "4 of 52 ok"}, "rowan")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindRoute, "flash-a", map[string]string{"note": ""}, "stella")
	require.ErrorIs(t, err, ErrInvalid)
	_, _, err = st.Update(ctx, KindRoute, "flash-a", map[string]string{"enabled": "true", "note": ""}, "stella")
	require.NoError(t, err)

	hist, err := st.History(ctx, KindRoute, "flash-a")
	require.NoError(t, err)
	require.Len(t, hist, 3, "an add and two sets; the two refused sets left none")
	assert.Equal(t, "", hist[0].After["note"])
	assert.Equal(t, "", hist[1].Before["note"])
	assert.Equal(t, "4 of 52 ok", hist[1].After["note"])
	assert.Equal(t, "rowan", hist[1].Actor)
	assert.Equal(t, "4 of 52 ok", hist[2].Before["note"])
	assert.Equal(t, "", hist[2].After["note"])
	assert.Equal(t, "stella", hist[2].Actor)
}

func TestApplyWritesTheNoteIntoTheRouteAndMachineViews(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := migrated(t)
	route := newRoute(t, "flash-off", map[string]string{"tier": "flash", "provider": "p", "model": "m", "deadline": "600", "enabled": "false", "note": "4 of 52 ok"})
	_, err := st.Insert(ctx, KindRoute, route, "t")
	require.NoError(t, err)
	machine, _ := Lookup(KindMachine)
	m, err := machine.NewRow("superman", map[string]string{"user": "u", "seat": "s", "slots": "8", "width": "0", "note": "held 1:46 PM"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, m, "t")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindFleet, KindFleet, map[string]string{"redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova"}, "t")
	require.NoError(t, err)
	applyKinds(t, st, ap, "t")

	assert.Equal(t, "4 of 52 ok", c.HGet(ctx, RouteKey("flash-off"), "note").Val(), "the applied route carries its reason")
	assert.Equal(t, "held 1:46 PM", c.HGet(ctx, MachineKey("superman"), "note").Val())

	// a note changed is one set on the next apply, and the view follows
	_, _, err = st.Update(ctx, KindRoute, "flash-off", map[string]string{"note": "4 of 52 ok; 48 ended with no result"}, "t")
	require.NoError(t, err)
	var ops []string
	_, err = Apply(ctx, st, ap, KindRoute, "t", false, func(op Op) { ops = append(ops, OpLine("APPLY", KindRoute, op)) })
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY SET kind=route name=flash-off changed=note"}, ops)
	assert.Equal(t, "4 of 52 ok; 48 ended with no result", c.HGet(ctx, RouteKey("flash-off"), "note").Val())
}
