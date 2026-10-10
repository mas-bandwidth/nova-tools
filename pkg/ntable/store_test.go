//go:build functional

package ntable_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tripLog counts the round trips a client makes and the commands it sends.
type tripLog struct {
	mu    sync.Mutex
	trips int
	names []string
}

func (l *tripLog) DialHook(next redis.DialHook) redis.DialHook { return next }

func (l *tripLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		l.mu.Lock()
		l.trips++
		l.names = append(l.names, strings.ToUpper(cmd.Name()))
		l.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (l *tripLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		l.mu.Lock()
		l.trips++
		for _, c := range cmds {
			l.names = append(l.names, strings.ToUpper(c.Name()))
		}
		l.mu.Unlock()
		return next(ctx, cmds)
	}
}

func (l *tripLog) reset() (int, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, names := l.trips, l.names
	l.trips, l.names = 0, nil
	return n, names
}

func store(t *testing.T) (*redis.Client, *tripLog) {
	t.Helper()
	_, c := live(t)
	log := &tripLog{}
	c.AddHook(log)
	return c, log
}

var now = time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)

// tableRig is a table on a live store under setup: each step is one plain
// command, and a step that fails fails the test. It is the setup the contract
// tests repeat before the call they are about: create, rows, members in cells.
type tableRig struct {
	t    *testing.T
	c    *redis.Client
	name string
}

// newTable creates tb at now.
func newTable(t *testing.T, c *redis.Client, tb ntable.Table) *tableRig {
	t.Helper()
	require.NoError(t, ntable.Create(context.Background(), c, tb, now))
	return &tableRig{t: t, c: c, name: tb.Name}
}

// rows adds each row, one RowAdd per row, in order.
func (r *tableRig) rows(keys ...string) *tableRig {
	r.t.Helper()
	for _, k := range keys {
		_, err := ntable.RowAdd(context.Background(), r.c, r.name, k, ntable.RowSpec{})
		require.NoError(r.t, err, "row %s", k)
	}
	return r
}

// cell puts member in the cell (row, col) at score.
func (r *tableRig) cell(row, col, member string, score float64) *tableRig {
	r.t.Helper()
	_, err := ntable.CellAdd(context.Background(), r.c, r.name, row, col, member, score)
	require.NoError(r.t, err, "cell %s:%s member %s", row, col, member)
	return r
}

func demo() ntable.Table {
	cols, err := ntable.ParseColumns("ready,working,done,who:members:union")
	if err != nil {
		panic(err)
	}
	return ntable.Table{Name: "demo", Columns: cols}
}

// TestCreateRowAddCellAddReadRender is the sitting: a table created, two
// rows added, members put in cells, the table read back and rendered, all
// through plain commands. A second identical create is a no-op and a
// different one is refused.
func TestCreateRowAddCellAddReadRender(t *testing.T) {
	t.Parallel()

	c, _ := store(t)
	ctx := context.Background()
	newTable(t, c, demo())
	require.NoError(t, ntable.Create(ctx, c, demo(), now), "second identical create")
	other := demo()
	other.Columns = other.Columns[:2]
	require.ErrorIs(t, ntable.Create(ctx, c, other, now), ntable.ErrExists, "create with another definition")
	names, err := ntable.List(ctx, c)
	require.NoError(t, err)
	require.Equal(t, "demo", strings.Join(names, ","), "List = %v", names)
	_, err = ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{Binds: map[string]string{"who": "people:build"}})
	require.NoError(t, err)
	_, err = ntable.RowAdd(ctx, c, "demo", "test", ntable.RowSpec{Label: "test suite", Binds: map[string]string{"who": "people:test"}})
	require.NoError(t, err)
	for _, add := range []struct {
		row, col, member string
		score            float64
	}{
		{"build", "ready", "b1", 1}, {"build", "ready", "b2", 2}, {"build", "working", "b3", 3},
		{"test", "done", "t1", 1}, {"build", "who", "ann", 1}, {"test", "who", "bo", 1}, {"test", "who", "ann", 2},
	} {
		var err error
		if add.col == "who" {
			// A bound projection may repeat an external member across rows; owned placements may not.
			err = c.ZAdd(ctx, "people:"+add.row, redis.Z{Score: add.score, Member: add.member}).Err()
		} else {
			_, err = ntable.CellAdd(ctx, c, "demo", add.row, add.col, add.member, add.score)
		}
		require.NoError(t, err, "cell add %+v: %v", add, err)
	}
	n, err := ntable.CellRemove(ctx, c, "demo", "build", "ready", "b2")
	require.NoError(t, err, "cell remove")
	require.Equal(t, int64(1), n, "cell remove")
	tb, err := ntable.Read(ctx, c, "demo")
	require.NoError(t, err)
	want := "row        | ready | working | done | who\n" +
		"-----------+-------+---------+------+-------\n" +
		"build      |     1 |       1 |    0 | ann\n" +
		"test suite |     0 |       0 |    1 | bo,ann\n" +
		"-----------+-------+---------+------+-------\n" +
		"           |     1 |       1 |    1 | ann,bo\n"
	got := ntable.Render(tb, ntable.RenderOpts{})
	require.Equal(t, want, got, "rendered:\n%s\nwant:\n%s", got, want)
	ms, err := ntable.CellMembers(ctx, c, "demo", "test", "who")
	require.NoError(t, err)
	require.Equal(t, []ntable.Member{{Member: "bo", Score: 1}, {Member: "ann", Score: 2}}, ms, "CellMembers")
	// a cell of a row or column the table lacks refuses (a text column's: TestReview4456RefusalsAreAtomic, the CLI sitting)
	for _, bad := range [][3]string{{"nope", "ready", "x"}, {"build", "nope", "x"}} {
		_, err := ntable.CellAdd(ctx, c, "demo", bad[0], bad[1], bad[2], 1)
		assert.Error(t, err, "cell add %v accepted", bad)
	}
	_, err = ntable.Read(ctx, c, "nope")
	require.ErrorIs(t, err, ntable.ErrNoTable, "read of no table: %v, want ErrNoTable", err)
}

// TestRowOrderIsStableAcrossReAdds: rows render in the order added, a
// re-added row keeps its place and its cells, and a deleted row's owned
// cells go with it.
func TestRowOrderIsStableAcrossReAdds(t *testing.T) {
	t.Parallel()

	c, _ := store(t)
	ctx := context.Background()
	newTable(t, c, demo()).rows("c", "a", "b").cell("a", "ready", "m", 1)
	_, err := ntable.RowAdd(ctx, c, "demo", "a", ntable.RowSpec{Label: "A"})
	require.NoError(t, err)
	tb, err := ntable.Read(ctx, c, "demo")
	require.NoError(t, err)
	keys := []string{}
	for _, r := range tb.Rows {
		keys = append(keys, r.Key)
	}
	require.Equal(t, []string{"c", "a", "b"}, keys, "rows after re-add")
	require.Equal(t, "A", tb.Rows[1].Label, "rows after re-add")
	require.Equal(t, int64(1), tb.Rows[1].Cells[0].Count, "rows after re-add: ready")
	ok, err := ntable.RowDel(ctx, c, "demo", "a")
	require.NoError(t, err)
	require.True(t, ok, "RowDel")
	ok, err = ntable.RowDel(ctx, c, "demo", "a")
	require.NoError(t, err)
	require.False(t, ok, "second RowDel")
	left, err := c.Exists(ctx, ntable.CellKey("demo", "a", "ready"), ntable.RowKey("demo", "a")).Result()
	require.NoError(t, err)
	require.Zero(t, left, "a deleted row left keys")
	tb, err = ntable.Read(ctx, c, "demo")
	require.NoError(t, err)
	require.Len(t, tb.Rows, 2, "rows after delete: %+v", tb.Rows)
	require.Equal(t, "c", tb.Rows[0].Key, "rows after delete")
	require.Equal(t, "b", tb.Rows[1].Key, "rows after delete")
	dropped, err := ntable.Drop(ctx, c, "demo")
	require.NoError(t, err)
	require.Equal(t, 2, dropped, "Drop")
	for _, pattern := range []string{"table:demo:row:*", "table:demo:cell:*"} {
		keys, err := c.Keys(ctx, pattern).Result()
		require.NoError(t, err)
		require.Empty(t, keys, "Drop left owned keys")
	}
	_, err = ntable.Read(ctx, c, "demo")
	require.ErrorIs(t, err, ntable.ErrNoTable, "dropped presence")
	require.True(t, c.HExists(ctx, ntable.DefKey("demo"), "order").Val(), "drop lost definition or member identity")
	require.True(t, c.HExists(ctx, ntable.MemberKey("m"), "epoch").Val(), "drop lost definition or member identity")
}

// TestReaderTakesOnePipelineInTheSteadyState also pins the cold and changed
// shape cases: each returns the current bound cells in one round trip.
func TestReaderTakesOnePipelineInTheSteadyState(t *testing.T) {
	t.Parallel()

	c, log := store(t)
	ctx := context.Background()
	newTable(t, c, demo()).rows("a", "b")
	r := ntable.NewReader("demo")
	log.reset()
	_, err := r.Read(ctx, c)
	require.NoError(t, err)
	// The cold read includes its shape and values in the same snapshot.
	trips, _ := log.reset()
	require.Equal(t, 1, trips, "cold read took %d round trips, want 1", trips)
	_, err = r.Read(ctx, c)
	require.NoError(t, err)
	trips, names := log.reset()
	require.Equal(t, 1, trips, "steady read took %d round trips, want 1: %v", trips, names)
	for _, n := range names {
		require.NotEqual(t, "KEYS", n, "the read sent %s", n)
		require.NotEqual(t, "SCAN", n, "the read sent %s", n)
	}
	_, err = ntable.RowAdd(ctx, c, "demo", "c", ntable.RowSpec{Binds: map[string]string{"ready": "elsewhere:ready"}, Owner: "other-tool put"})
	require.NoError(t, err)
	require.NoError(t, c.ZAdd(ctx, "elsewhere:ready", redis.Z{Score: 1, Member: "e1"}).Err())
	log.reset()
	tb, err := r.Read(ctx, c)
	require.NoError(t, err)
	// The changed shape and its bound values still arrive in one read.
	trips, _ = log.reset()
	require.Equal(t, 1, trips, "read after a bound row add took %d round trips, want 1", trips)
	require.Len(t, tb.Rows, 3, "bound row read")
	require.True(t, tb.Rows[2].Cells[0].Bound, "bound row read: %+v", tb.Rows[2])
	require.Equal(t, int64(1), tb.Rows[2].Cells[0].Count, "bound row read: %+v", tb.Rows[2])
	require.Equal(t, "other-tool put", tb.Rows[2].Owner, "bound row read: %+v", tb.Rows[2])
	// a write to the bound cell is refused, naming the owner
	_, err = ntable.CellAdd(ctx, c, "demo", "c", "ready", "x", 1)
	var bound *ntable.BoundError
	require.ErrorAs(t, err, &bound, "cell add on a bound cell")
	require.Equal(t, "elsewhere:ready", bound.Key, "cell add on a bound cell: %v", err)
	require.Equal(t, "other-tool put", bound.Owner, "cell add on a bound cell: %v", err)
	require.ErrorContains(t, err, "demo.c.ready is bound to elsewhere:ready, owned elsewhere; run: other-tool put", "bound refusal reads")
	_, err = ntable.CellRemove(ctx, c, "demo", "c", "ready", "e1")
	require.ErrorAs(t, err, &bound, "cell remove on a bound cell")
	n, err := c.ZCard(ctx, "elsewhere:ready").Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the bound set was written")
	// the bound set is read freely
	ms, err := ntable.CellMembers(ctx, c, "demo", "c", "ready")
	require.NoError(t, err)
	require.Len(t, ms, 1, "CellMembers of a bound cell = %+v", ms)
	require.Equal(t, "e1", ms[0].Member, "CellMembers of a bound cell")
	// a drop leaves the bound set where it is
	_, err = ntable.Drop(ctx, c, "demo")
	require.NoError(t, err)
	n, err = c.ZCard(ctx, "elsewhere:ready").Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "drop touched the bound set")
}

// TestBindMakesTheStoreTheCallersTable: Bind creates the definition, writes
// every row in the caller's order with its bindings and exclude, removes
// rows the caller no longer names, and a read renders the bound sets, the
// excluded member left out of every count and members list.
func TestBindMakesTheStoreTheCallersTable(t *testing.T) {
	t.Parallel()

	c, _ := store(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("waiting,landed")
	require.NoError(t, err)
	tb := ntable.Table{Name: "streams", Columns: cols, FooterLabel: "total"}
	bind := func(streams ...string) ntable.Table {
		out := ntable.Table{Name: tb.Name, Columns: tb.Columns, FooterLabel: tb.FooterLabel}
		for _, s := range streams {
			r := ntable.NewRow(out, s)
			r.Exclude, r.Owner = s+":sentinel", "other-tool move"
			r.Cells[0] = ntable.Cell{Key: "ws:" + s + ":waiting", Bound: true}
			r.Cells[1] = ntable.Cell{Key: "ws:" + s + ":landed", Bound: true}
			out.Rows = append(out.Rows, r)
		}
		return out
	}
	for _, z := range []struct {
		key, member string
	}{{"ws:a:waiting", "a1"}, {"ws:a:waiting", "a:sentinel"}, {"ws:b:landed", "b1"}, {"ws:b:landed", "b2"}} {
		require.NoError(t, c.ZAdd(ctx, z.key, redis.Z{Score: 1, Member: z.member}).Err())
	}
	require.NoError(t, ntable.Bind(ctx, c, bind("a", "b", "gone"), now))
	require.NoError(t, ntable.Bind(ctx, c, bind("b", "a"), now))
	got, err := ntable.Read(ctx, c, "streams")
	require.NoError(t, err)
	want := "row   | waiting | landed\n" +
		"------+---------+-------\n" +
		"b     |       0 |      2\n" +
		"a     |       1 |      0\n" +
		"------+---------+-------\n" +
		"total |       1 |      2\n"
	rendered := ntable.Render(got, ntable.RenderOpts{})
	require.Equal(t, want, rendered, "bound render:\n%s\nwant:\n%s", rendered, want)
	n, err := c.Exists(ctx, ntable.RowKey("streams", "gone")).Result()
	require.NoError(t, err)
	require.Zero(t, n, "the row Bind no longer names is still there")
	ms, err := ntable.CellMembers(ctx, c, "streams", "a", "waiting")
	require.NoError(t, err)
	require.Len(t, ms, 1, "members with the exclude left out = %+v", ms)
	require.Equal(t, "a1", ms[0].Member, "members with the exclude left out")
	require.True(t, ntable.SameShape(got, bind("b", "a")), "SameShape does not tell the bound order")
	require.False(t, ntable.SameShape(got, bind("a", "b")), "SameShape does not tell the bound order")
}

// TestQueueCellsFillsAnInMemoryTable: a caller holding the shape reads
// every cell in one pipeline of its own, and a set that did not come back
// is Unread, never a false 0.
func TestQueueCellsFillsAnInMemoryTable(t *testing.T) {
	t.Parallel()

	c, log := store(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("n,who:members:union")
	require.NoError(t, err)
	tb := ntable.Table{Name: "mem", Columns: cols}
	r := ntable.NewRow(tb, "x")
	r.Cells[0] = ntable.Cell{Key: "set:n", Bound: true}
	r.Cells[1] = ntable.Cell{Key: "set:who", Bound: true}
	tb.Rows = []ntable.Row{r}
	require.NoError(t, c.ZAdd(ctx, "set:n", redis.Z{Score: 1, Member: "one"}, redis.Z{Score: 2, Member: "two"}).Err())
	require.NoError(t, c.Set(ctx, "set:who", "not a zset", 0).Err())
	log.reset()
	pipe := c.Pipeline()
	q := ntable.QueueCells(ctx, pipe, &tb)
	_, _ = pipe.Exec(ctx) // WRONGTYPE on one command is that cell's error, not the pipeline's
	q.Result()
	trips, _ := log.reset()
	require.Equal(t, 1, trips, "QueueCells took %d round trips, want 1", trips)
	require.Equal(t, int64(2), tb.Rows[0].Cells[0].Count, "count cell = %+v", tb.Rows[0].Cells[0])
	require.False(t, tb.Rows[0].Cells[0].Unread, "count cell = %+v", tb.Rows[0].Cells[0])
	require.True(t, tb.Rows[0].Cells[1].Unread, "a set of the wrong type read as %+v, want Unread", tb.Rows[0].Cells[1])
	got := ntable.Render(tb, ntable.RenderOpts{})
	require.Contains(t, got, "x   | 2 | ?\n", "unread cell render")
	require.Contains(t, got, "    | 2 | ?\n", "unread cell render")
}
