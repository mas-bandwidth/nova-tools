//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The loop kind against the package's throwaway Postgres (pg_functional_test.go)
// and a throwaway Redis (redis_functional_test.go): the row round-trips
// exactly, every write is a history row, the rules hold in the store and in
// the schema, and apply leaves the view the plays read.

// loopPG is a migrated database holding the named machines.
func loopPG(t *testing.T, machines ...string) *PG {
	t.Helper()
	st := migrated(t)
	for _, m := range machines {
		_, err := st.Insert(context.Background(), KindMachine, Row{Name: m, Fields: map[string]string{"user": "u", "seat": "s-" + m, "slots": "4", "runners": "0"}}, "t")
		require.NoError(t, err)
	}
	return st
}

func newLoop(t *testing.T, name string, raw map[string]string) Row {
	t.Helper()
	k, _ := Lookup(KindLoop)
	row, err := k.NewRow(name, raw)
	require.NoError(t, err)
	return row
}

func TestPostgresLoopRoundTripsAndHoldsItsRules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := loopPG(t, "m1", "m2")
	member := newLoop(t, "member-m1", map[string]string{"machine": "m1", "argv": `["/bin/member","a b=c",""]`, "keepalive": "true", "seat": "s-m1", "keys": "B_KEY,A_KEY", "width": "2"})
	periodic := newLoop(t, "refresh", map[string]string{"machine": "m1", "argv": `["/bin/refresh"]`, "every": "60", "enabled": "false"})
	for _, row := range []Row{member, periodic} {
		_, err := st.Insert(ctx, KindLoop, row, "t")
		require.NoError(t, err)
	}

	cases := []struct {
		name string
		want Row
	}{
		{name: "kept alive, with secrets by name and an argument holding a space", want: member},
		{name: "periodic and disabled", want: periodic},
	}
	for _, tc := range cases {
		got, found, err := st.Get(ctx, KindLoop, tc.want.Name)
		require.NoError(t, err, tc.name)
		require.True(t, found, tc.name)
		assert.Equal(t, tc.want.Fields, got.Fields, "%s: the row reads back exactly, bools and argv in their canonical text", tc.name)
	}
	rows, err := st.List(ctx, KindLoop)
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	// A set the kind's Check refuses writes nothing; one it accepts is one
	// history row with the row before and after.
	_, _, err = st.Update(ctx, KindLoop, "refresh", map[string]string{"keepalive": "true"}, "t")
	require.ErrorIs(t, err, ErrInvalid)
	_, id, err := st.Update(ctx, KindLoop, "refresh", map[string]string{"machine": "m2", "enabled": "true"}, "t")
	require.NoError(t, err)
	hist, err := st.History(ctx, KindLoop, "refresh")
	require.NoError(t, err)
	require.Len(t, hist, 2, "an add and a set; the refused set left none")
	assert.Equal(t, id, hist[1].ID)
	assert.Equal(t, "false", hist[1].Before["enabled"])
	assert.Equal(t, "true", hist[1].After["enabled"])
	rev, err := st.Rev(ctx, KindLoop)
	require.NoError(t, err)
	assert.Equal(t, id, rev, "the kind's revision is its newest history id")

	// The machine a loop names is held, by the tool and by the key.
	_, err = st.Delete(ctx, KindMachine, "m1", "t")
	require.ErrorIs(t, err, ErrReferenced)
	assert.Contains(t, err.Error(), "machine m1 is the --machine of loop member-m1")
	_, err = st.Insert(ctx, KindLoop, newLoop(t, "stray", map[string]string{"machine": "m9", "argv": `["/bin/x"]`, "every": "5"}), "t")
	require.ErrorIs(t, err, ErrNoRef)

	// The schema is a second wall behind the descriptor: a row written
	// around the tool that breaks a rule is refused by Postgres itself.
	walls := []struct {
		name string
		sql  string
	}{
		{name: "every and keepalive both", sql: `INSERT INTO config.loops (name, machine, argv, every, keepalive) VALUES ('w1', 'm2', '["/bin/x"]', 5, true)`},
		{name: "neither every nor keepalive", sql: `INSERT INTO config.loops (name, machine, argv) VALUES ('w2', 'm2', '["/bin/x"]')`},
		{name: "keys with no seat", sql: `INSERT INTO config.loops (name, machine, argv, every, keys) VALUES ('w3', 'm2', '["/bin/x"]', 5, 'A_KEY')`},
		{name: "argv that is no array", sql: `INSERT INTO config.loops (name, machine, argv, every) VALUES ('w4', 'm2', '/bin/x', 5)`},
		{name: "a machine that is no row", sql: `INSERT INTO config.loops (name, machine, argv, every) VALUES ('w5', 'm9', '["/bin/x"]', 5)`},
	}
	for _, w := range walls {
		_, err := st.db.ExecContext(ctx, w.sql)
		assert.Error(t, err, w.name)
	}

	_, err = st.Delete(ctx, KindLoop, "member-m1", "t")
	require.NoError(t, err)
	_, err = st.Delete(ctx, KindMachine, "m1", "t")
	assert.NoError(t, err, "a machine no loop names any more is removed")
}

func TestApplyWritesTheLoopViewThePlaysRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := loopPG(t, "m1")
	for _, row := range []Row{
		newLoop(t, "member-m1", map[string]string{"machine": "m1", "argv": `["/bin/member","--width","2"]`, "keepalive": "true", "seat": "s-m1", "keys": "A_KEY,B_KEY", "width": "2"}),
		newLoop(t, "refresh", map[string]string{"machine": "m1", "argv": `["/bin/refresh"]`, "every": "60"}),
	} {
		_, err := st.Insert(ctx, KindLoop, row, "t")
		require.NoError(t, err)
	}
	res := applyKinds(t, st, ap, "t")
	assert.Equal(t, 2, res[KindLoop].Add)
	rev, _ := st.Rev(ctx, KindLoop)

	assert.ElementsMatch(t, []string{"member-m1", "refresh"}, c.SMembers(ctx, LoopsKey).Val())
	got := c.HGetAll(ctx, LoopKey("member-m1")).Val()
	want := map[string]string{
		"name": "member-m1", "machine": "m1", "argv": `["/bin/member","--width","2"]`, "seat": "s-m1", "keys": "A_KEY,B_KEY",
		"every": "0", "keepalive": "true", "width": "2", "enabled": "true", "log": "~/nova-bench/loops/member-m1.log",
	}
	for f, v := range want {
		assert.Equal(t, v, got[f], "loop:member-m1 %s", f)
	}
	assert.NotEmpty(t, got["at"])
	assert.Equal(t, len(want)+2, len(got), "the fields, the log, rev and at, and nothing else: %v", got)
	assert.Equal(t, c.HGet(ctx, DeclKey, "rev:loop").Val(), got["rev"])

	// A second apply of the same rows writes nothing and keeps the stamp,
	// in two round trips: the set and the stamp, then the hashes.
	trips := redisconn.CountTrips(c)
	before := trips.N()
	res2, err := Apply(ctx, st, ap, KindLoop, "t", false, func(Op) {})
	require.NoError(t, err)
	assert.Equal(t, int64(2), trips.N()-before, "a steady loop apply reads in two trips and writes none")
	assert.Zero(t, res2.Add+res2.Set+res2.Remove)
	assert.Equal(t, rev, res2.RedisRev)

	// A field changed by hand in Redis is put back; a removed row's keys go.
	c.HSet(ctx, LoopKey("refresh"), "every", "1")
	_, err = st.Delete(ctx, KindLoop, "member-m1", "t")
	require.NoError(t, err)
	var ops []string
	_, err = Apply(ctx, st, ap, KindLoop, "t", false, func(op Op) { ops = append(ops, OpLine("APPLY", KindLoop, op)) })
	require.NoError(t, err)
	assert.Equal(t, []string{"APPLY SET kind=loop name=refresh changed=every", "APPLY REMOVE kind=loop name=member-m1"}, ops)
	assert.Equal(t, "60", c.HGet(ctx, LoopKey("refresh"), "every").Val())
	assert.Zero(t, c.Exists(ctx, LoopKey("member-m1")).Val())
	assert.False(t, c.SIsMember(ctx, LoopsKey, "member-m1").Val())
	views, applied, err := ap.Read(ctx, KindLoop)
	require.NoError(t, err)
	rev, _ = st.Rev(ctx, KindLoop)
	assert.Equal(t, rev, applied, "revision parity after apply")
	rows, _ := st.List(ctx, KindLoop)
	for _, row := range rows {
		assert.Equal(t, View(row.Fields), views[row.Name], "the view of %s is its row", row.Name)
	}
}
