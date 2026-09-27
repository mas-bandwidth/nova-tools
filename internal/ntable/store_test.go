//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
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

func demo() ntable.Table {
	cols, err := ntable.ParseColumns("job:text:none:job,ready,working,done,who:members:union")
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
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatalf("second identical create: %v", err)
	}
	other := demo()
	other.Columns = other.Columns[:2]
	if err := ntable.Create(ctx, c, other, now); !errors.Is(err, ntable.ErrExists) {
		t.Fatalf("create with another definition: %v, want ErrExists", err)
	}
	if names, err := ntable.List(ctx, c); err != nil || strings.Join(names, ",") != "demo" {
		t.Fatalf("List = %v %v", names, err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "test", ntable.RowSpec{Label: "test suite"}); err != nil {
		t.Fatal(err)
	}
	for _, add := range []struct {
		row, col, member string
		score            float64
	}{
		{"build", "ready", "b1", 1}, {"build", "ready", "b2", 2}, {"build", "working", "b3", 3},
		{"test", "done", "t1", 1}, {"build", "who", "ann", 1}, {"test", "who", "bo", 1}, {"test", "who", "ann", 2},
	} {
		if _, err := ntable.CellAdd(ctx, c, "demo", add.row, add.col, add.member, add.score); err != nil {
			t.Fatalf("cell add %+v: %v", add, err)
		}
	}
	if n, err := ntable.CellRemove(ctx, c, "demo", "build", "ready", "b2"); err != nil || n != 1 {
		t.Fatalf("cell remove: n=%d err=%v", n, err)
	}
	tb, err := ntable.Read(ctx, c, "demo")
	if err != nil {
		t.Fatal(err)
	}
	want := "job        | ready | working | done | who\n" +
		"-----------+-------+---------+------+-------\n" +
		"build      |     1 |       1 |    0 | ann\n" +
		"test suite |     0 |       0 |    1 | bo,ann\n" +
		"-----------+-------+---------+------+-------\n" +
		"           |     1 |       1 |    1 | ann,bo\n"
	if got := ntable.Render(tb, ntable.RenderOpts{}); got != want {
		t.Fatalf("rendered:\n%s\nwant:\n%s", got, want)
	}
	ms, err := ntable.CellMembers(ctx, c, "demo", "test", "who")
	if err != nil || len(ms) != 2 || ms[0].Member != "bo" || ms[1].Member != "ann" || ms[1].Score != 2 {
		t.Fatalf("CellMembers = %+v %v", ms, err)
	}
	// a cell of a row or column the table lacks, and a text column, refuse
	for _, bad := range [][3]string{{"nope", "ready", "x"}, {"build", "nope", "x"}, {"build", "job", "x"}} {
		if _, err := ntable.CellAdd(ctx, c, "demo", bad[0], bad[1], bad[2], 1); err == nil {
			t.Errorf("cell add %v accepted", bad)
		}
	}
	if _, err := ntable.Read(ctx, c, "nope"); !errors.Is(err, ntable.ErrNoTable) {
		t.Fatalf("read of no table: %v, want ErrNoTable", err)
	}
}

// TestRowOrderIsStableAcrossReAdds: rows render in the order added, a
// re-added row keeps its place and its cells, and a deleted row's owned
// cells go with it.
func TestRowOrderIsStableAcrossReAdds(t *testing.T) {
	t.Parallel()

	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"c", "a", "b"} {
		if _, err := ntable.RowAdd(ctx, c, "demo", key, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "a", "ready", "m", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "a", ntable.RowSpec{Label: "A"}); err != nil {
		t.Fatal(err)
	}
	tb, err := ntable.Read(ctx, c, "demo")
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, r := range tb.Rows {
		keys = append(keys, r.Key)
	}
	if strings.Join(keys, ",") != "c,a,b" || tb.Rows[1].Label != "A" || tb.Rows[1].Cells[1].Count != 1 {
		t.Fatalf("rows after re-add: %v label=%q ready=%d", keys, tb.Rows[1].Label, tb.Rows[1].Cells[1].Count)
	}
	if ok, err := ntable.RowDel(ctx, c, "demo", "a"); err != nil || !ok {
		t.Fatalf("RowDel: %v %v", ok, err)
	}
	if ok, err := ntable.RowDel(ctx, c, "demo", "a"); err != nil || ok {
		t.Fatalf("second RowDel: %v %v, want false", ok, err)
	}
	if n, err := c.Exists(ctx, ntable.CellKey("demo", "a", "ready"), ntable.RowKey("demo", "a")).Result(); err != nil || n != 0 {
		t.Fatalf("a deleted row left keys: %d %v", n, err)
	}
	tb, err = ntable.Read(ctx, c, "demo")
	if err != nil || len(tb.Rows) != 2 || tb.Rows[0].Key != "c" || tb.Rows[1].Key != "b" {
		t.Fatalf("rows after delete: %+v %v", tb.Rows, err)
	}
	if n, err := ntable.Drop(ctx, c, "demo"); err != nil || n != 2 {
		t.Fatalf("Drop: %d %v", n, err)
	}
	if keys, err := c.Keys(ctx, "*").Result(); err != nil || len(keys) != 0 {
		t.Fatalf("Drop left keys %v (%v)", keys, err)
	}
}

// TestReaderTakesOnePipelineInTheSteadyState also pins the cold and changed
// shape cases: each returns the current bound cells in one round trip.
func TestReaderTakesOnePipelineInTheSteadyState(t *testing.T) {
	t.Parallel()

	c, log := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b"} {
		if _, err := ntable.RowAdd(ctx, c, "demo", key, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
	}
	r := ntable.NewReader("demo")
	log.reset()
	if _, err := r.Read(ctx, c); err != nil {
		t.Fatal(err)
	}
	// The cold read includes its shape and values in the same snapshot.
	if trips, _ := log.reset(); trips != 1 {
		t.Fatalf("cold read took %d round trips, want 1", trips)
	}
	if _, err := r.Read(ctx, c); err != nil {
		t.Fatal(err)
	}
	trips, names := log.reset()
	if trips != 1 {
		t.Fatalf("steady read took %d round trips, want 1: %v", trips, names)
	}
	for _, n := range names {
		if n == "KEYS" || n == "SCAN" {
			t.Fatalf("the read sent %s", n)
		}
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "c", ntable.RowSpec{Binds: map[string]string{"ready": "elsewhere:ready"}, Owner: "other-tool put"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ZAdd(ctx, "elsewhere:ready", redis.Z{Score: 1, Member: "e1"}).Err(); err != nil {
		t.Fatal(err)
	}
	log.reset()
	tb, err := r.Read(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	// The changed shape and its bound values still arrive in one read.
	if trips, _ := log.reset(); trips != 1 {
		t.Fatalf("read after a bound row add took %d round trips, want 1", trips)
	}
	if len(tb.Rows) != 3 || !tb.Rows[2].Cells[1].Bound || tb.Rows[2].Cells[1].Count != 1 || tb.Rows[2].Owner != "other-tool put" {
		t.Fatalf("bound row read: %+v", tb.Rows[2])
	}
	// a write to the bound cell is refused, naming the owner
	_, err = ntable.CellAdd(ctx, c, "demo", "c", "ready", "x", 1)
	var bound *ntable.BoundError
	if !errors.As(err, &bound) || bound.Key != "elsewhere:ready" || bound.Owner != "other-tool put" {
		t.Fatalf("cell add on a bound cell: %v", err)
	}
	if !strings.Contains(err.Error(), "demo.c.ready is bound to elsewhere:ready, owned elsewhere; run: other-tool put") {
		t.Fatalf("bound refusal reads %q", err)
	}
	if _, err := ntable.CellRemove(ctx, c, "demo", "c", "ready", "e1"); !errors.As(err, &bound) {
		t.Fatalf("cell remove on a bound cell: %v", err)
	}
	if n, err := c.ZCard(ctx, "elsewhere:ready").Result(); err != nil || n != 1 {
		t.Fatalf("the bound set was written: %d %v", n, err)
	}
	// the bound set is read freely
	if ms, err := ntable.CellMembers(ctx, c, "demo", "c", "ready"); err != nil || len(ms) != 1 || ms[0].Member != "e1" {
		t.Fatalf("CellMembers of a bound cell = %+v %v", ms, err)
	}
	// a drop leaves the bound set where it is
	if _, err := ntable.Drop(ctx, c, "demo"); err != nil {
		t.Fatal(err)
	}
	if n, err := c.ZCard(ctx, "elsewhere:ready").Result(); err != nil || n != 1 {
		t.Fatalf("drop touched the bound set: %d %v", n, err)
	}
}

// TestBindMakesTheStoreTheCallersTable: Bind creates the definition, writes
// every row in the caller's order with its bindings and exclude, removes
// rows the caller no longer names, and a read renders the bound sets, the
// excluded member left out of every count and members list.
func TestBindMakesTheStoreTheCallersTable(t *testing.T) {
	t.Parallel()

	c, _ := store(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("stream:text:none:stream,waiting,landed")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "streams", Columns: cols, FooterLabel: "total"}
	bind := func(streams ...string) ntable.Table {
		out := ntable.Table{Name: tb.Name, Columns: tb.Columns, FooterLabel: tb.FooterLabel}
		for _, s := range streams {
			r := ntable.NewRow(out, s)
			r.Exclude, r.Owner = s+":sentinel", "other-tool move"
			r.Cells[1] = ntable.Cell{Key: "ws:" + s + ":waiting", Bound: true}
			r.Cells[2] = ntable.Cell{Key: "ws:" + s + ":landed", Bound: true}
			out.Rows = append(out.Rows, r)
		}
		return out
	}
	for _, z := range []struct {
		key, member string
	}{{"ws:a:waiting", "a1"}, {"ws:a:waiting", "a:sentinel"}, {"ws:b:landed", "b1"}, {"ws:b:landed", "b2"}} {
		if err := c.ZAdd(ctx, z.key, redis.Z{Score: 1, Member: z.member}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := ntable.Bind(ctx, c, bind("a", "b", "gone"), now); err != nil {
		t.Fatal(err)
	}
	if err := ntable.Bind(ctx, c, bind("b", "a"), now); err != nil {
		t.Fatal(err)
	}
	got, err := ntable.Read(ctx, c, "streams")
	if err != nil {
		t.Fatal(err)
	}
	want := "stream | waiting | landed\n" +
		"-------+---------+-------\n" +
		"b      |       0 |      2\n" +
		"a      |       1 |      0\n" +
		"-------+---------+-------\n" +
		"total  |       1 |      2\n"
	if rendered := ntable.Render(got, ntable.RenderOpts{}); rendered != want {
		t.Fatalf("bound render:\n%s\nwant:\n%s", rendered, want)
	}
	if n, err := c.Exists(ctx, ntable.RowKey("streams", "gone")).Result(); err != nil || n != 0 {
		t.Fatalf("the row Bind no longer names is still there: %d %v", n, err)
	}
	if ms, err := ntable.CellMembers(ctx, c, "streams", "a", "waiting"); err != nil || len(ms) != 1 || ms[0].Member != "a1" {
		t.Fatalf("members with the exclude left out = %+v %v", ms, err)
	}
	if !ntable.SameShape(got, bind("b", "a")) || ntable.SameShape(got, bind("a", "b")) {
		t.Fatal("SameShape does not tell the bound order")
	}
}

// TestQueueCellsFillsAnInMemoryTable: a caller holding the shape reads
// every cell in one pipeline of its own, and a set that did not come back
// is Unread, never a false 0.
func TestQueueCellsFillsAnInMemoryTable(t *testing.T) {
	t.Parallel()

	c, log := store(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("row:text:none,n,who:members:union")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "mem", Columns: cols}
	r := ntable.NewRow(tb, "x")
	r.Cells[1] = ntable.Cell{Key: "set:n", Bound: true}
	r.Cells[2] = ntable.Cell{Key: "set:who", Bound: true}
	tb.Rows = []ntable.Row{r}
	if err := c.ZAdd(ctx, "set:n", redis.Z{Score: 1, Member: "one"}, redis.Z{Score: 2, Member: "two"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "set:who", "not a zset", 0).Err(); err != nil {
		t.Fatal(err)
	}
	log.reset()
	pipe := c.Pipeline()
	q := ntable.QueueCells(ctx, pipe, &tb)
	_, _ = pipe.Exec(ctx) // WRONGTYPE on one command is that cell's error, not the pipeline's
	q.Result()
	if trips, _ := log.reset(); trips != 1 {
		t.Fatalf("QueueCells took %d round trips, want 1", trips)
	}
	if tb.Rows[0].Cells[1].Count != 2 || tb.Rows[0].Cells[1].Unread {
		t.Fatalf("count cell = %+v", tb.Rows[0].Cells[1])
	}
	if !tb.Rows[0].Cells[2].Unread {
		t.Fatalf("a set of the wrong type read as %+v, want Unread", tb.Rows[0].Cells[2])
	}
	if got := ntable.Render(tb, ntable.RenderOpts{}); !strings.Contains(got, "x   | 2 | ?\n") || !strings.Contains(got, "    | 2 | ?\n") {
		t.Fatalf("unread cell render:\n%s", got)
	}
}
