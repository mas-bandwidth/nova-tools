package ntable

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// coverReceipt is the wire receipt every write-based fake reply ends with.
var coverReceipt = []any{"RECEIPT", "op-1", "0", "0", "1", "applied"}

// coverCmdable is a redis.Cmdable that answers only the function calls the
// store seam makes, with no socket, no process and no live store. The embedded
// interface carries every other method; the ones the store calls are answered
// from replies keyed by function name.
type coverCmdable struct {
	redis.Cmdable
	replies map[string][]any
	errs    map[string]error
	calls   []string
}

func newCoverCmdable() *coverCmdable {
	return &coverCmdable{replies: map[string][]any{}, errs: map[string]error{}}
}

func (c *coverCmdable) answer(fn string, reply []any) *coverCmdable {
	c.replies[fn] = reply
	return c
}

func (c *coverCmdable) fail(fn string, err error) *coverCmdable {
	c.errs[fn] = err
	return c
}

func (c *coverCmdable) cmd(ctx context.Context, fn string) *redis.Cmd {
	c.calls = append(c.calls, fn)
	cmd := redis.NewCmd(ctx, fn)
	if err, ok := c.errs[fn]; ok {
		cmd.SetErr(err)
	} else if reply, ok := c.replies[fn]; ok {
		cmd.SetVal(reply)
	} else {
		cmd.SetVal([]any{"OK", int64(2), coverReceipt})
	}
	return cmd
}

func (c *coverCmdable) FCall(ctx context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	return c.cmd(ctx, fn)
}

func (c *coverCmdable) FCallRO(ctx context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	return c.cmd(ctx, fn)
}

func (c *coverCmdable) Pipeline() redis.Pipeliner { return &coverStorePipe{store: c} }

func (c *coverCmdable) TxPipeline() redis.Pipeliner { return &coverStorePipe{store: c} }

// coverStorePipe answers a queued FCall from its store immediately; Exec is a
// no-op because every command already holds its reply.
type coverStorePipe struct {
	redis.Pipeliner
	store *coverCmdable
}

func (p *coverStorePipe) FCall(ctx context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	return p.store.cmd(ctx, fn)
}

func (p *coverStorePipe) Exec(context.Context) ([]redis.Cmder, error) { return nil, nil }

// coverPairs flattens a hash into the alternating key/value slice the wire uses.
func coverPairs(m map[string]string) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(m)*2)
	for _, k := range keys {
		out = append(out, k, m[k])
	}
	return out
}

func coverColumns(t *testing.T, spec string) []Column {
	t.Helper()
	cols, err := ParseColumns(spec)
	require.NoError(t, err)
	return cols
}

func TestStoreCoverRefusalAndProse(t *testing.T) {
	t.Parallel()

	cause := errors.New("member revision")
	r := &Refusal{Code: "MEMBERREVISION", Location: `table "demo"`, Sentence: "expected 1, observed 2",
		Next: "nova-table member read 'demo' 'm'", Guarded: true, cause: cause}
	require.Equal(t, `table "demo": expected 1, observed 2; code=MEMBERREVISION; changed=no; run: nova-table member read 'demo' 'm'`, r.Error())
	require.ErrorIs(t, r, cause)
	require.True(t, IsRefusal(r))
	require.False(t, IsRefusal(cause))
	require.False(t, IsRefusal(nil))

	unguarded := &Refusal{Code: "NOTMEMBER", Location: `table "demo"`, Sentence: "not a member", Next: "nova-table show 'demo'"}
	require.NotContains(t, unguarded.Error(), "changed=no")

	p := say(ErrLimit, "limit %s: bound %d", "rows", 3)
	require.Equal(t, "limit rows: bound 3", p.Error())
	require.ErrorIs(t, p, ErrLimit)
	require.False(t, IsRefusal(p))

	require.Equal(t, "demo.row.col is bound to key, owned elsewhere; run: owner-tool",
		(&BoundError{Table: "demo", Row: "row", Col: "col", Key: "key", Owner: "owner-tool"}).Error())
	require.Equal(t, "demo.row.col is bound to key, owned elsewhere",
		(&BoundError{Table: "demo", Row: "row", Col: "col", Key: "key"}).Error())
}

func TestStoreCoverCommandsAndRemedies(t *testing.T) {
	t.Parallel()

	got := removeMembersCommand("demo", "row", "col", []any{"a", "-b", "c'd"})
	require.Contains(t, got, "nova-table cell remove")
	require.Contains(t, got, "-- ")
	require.Contains(t, got, `'c'\''d'`)
	require.NotContains(t, removeMembersCommand("demo", "row", "col", []any{"a"}), "-- ")

	require.Equal(t, "; run: nova-table help", runUnlessNamed(errors.New("plain"), "nova-table help"))
	require.Empty(t, runUnlessNamed(errors.New("x; run: already"), "remedy"))

	require.Contains(t, memberReadCommand("demo", "m", "--at-epoch", "1"), "nova-table member read --at-epoch 1 'demo' 'm'")
	require.NotContains(t, memberReadCommand("demo", "m"), "-- ")

	require.Equal(t, "nova-table drop 'demo' --definition --epoch 3", dropDefinitionCommand("demo", []any{"3"}))
	require.Equal(t, "nova-table drop 'demo' --definition", dropDefinitionCommand("demo", []any{"0"}))
	require.Equal(t, "nova-table drop 'demo' --definition", dropDefinitionCommand("demo", nil))

	require.Equal(t, "a 1 true", words([]any{"a", 1, true}))

	require.Equal(t, "nova-table help view", (operation{view: true}).remedy())
	require.Equal(t, "nova-table view show 'demo'", (operation{view: true, table: "demo"}).remedy())
	require.Equal(t, "nova-table show 'demo'", (operation{table: "demo"}).remedy())
}

func TestStoreCoverReplyCountAndCall(t *testing.T) {
	t.Parallel()

	n, err := replyCount([]any{"OK", "7"})
	require.NoError(t, err)
	require.Equal(t, int64(7), n)
	_, err = replyCount([]any{"OK"})
	require.ErrorContains(t, err, "no count")

	c := newCoverCmdable().answer(FnMembers, []any{"OK", int64(2), []any{"m", "1"}})
	o := operation{table: "demo", row: "r", col: "c"}
	reply, err := o.call(context.Background(), c, FnMembers, true, "r", "c")
	require.NoError(t, err)
	require.Len(t, reply, 3)

	cf := newCoverCmdable().fail(FnMembers, errors.New("dial unix: connection refused"))
	_, err = o.call(context.Background(), cf, FnMembers, true)
	require.ErrorContains(t, err, "connection refused")
	require.Contains(t, err.Error(), "run: ")

	cr := newCoverCmdable().answer(FnMembers, []any{"REFUSED", "NOTABLE"})
	_, err = o.call(context.Background(), cr, FnMembers, true)
	require.ErrorIs(t, err, ErrNoTable)
	require.True(t, IsRefusal(err))
}

func TestStoreCoverWrite(t *testing.T) {
	t.Parallel()

	o := operation{table: "demo"}
	c := newCoverCmdable()
	reply, err := o.write(context.Background(), c, FnSet, nil, "body")
	require.NoError(t, err)
	require.Equal(t, []any{"OK", int64(2)}, reply)

	_, err = o.write(context.Background(), c, FnSet, []WriteOptions{{}, {}})
	require.ErrorContains(t, err, "one write-options value is allowed")

	cm := newCoverCmdable().answer(FnSet, []any{"OK"})
	_, err = o.write(context.Background(), cm, FnSet, nil)
	require.ErrorContains(t, err, "missing committed receipt")

	cb := newCoverCmdable().answer(FnSet, []any{"OK", int64(1), []any{"NOPE"}})
	_, err = o.write(context.Background(), cb, FnSet, nil)
	require.ErrorContains(t, err, "malformed committed receipt")

	ce := newCoverCmdable().answer(FnSet, []any{"OK", int64(1), []any{"RECEIPT", "id", "x", "0", "1", "applied"}})
	_, err = o.write(context.Background(), ce, FnSet, nil)
	require.Error(t, err)

	var r Receipt
	cd := newCoverCmdable().answer(FnSet, []any{"OK", int64(1),
		[]any{"RECEIPT", "id", "2", "1", "3", "applied", `{"operation_id":"op","changed_count":1}`}})
	_, err = o.write(context.Background(), cd, FnSet, []WriteOptions{{Receipt: &r}})
	require.NoError(t, err)
	require.Equal(t, "id", r.ID)
	require.Equal(t, uint64(2), r.Epoch)
	require.Equal(t, uint64(1), r.Before)
	require.Equal(t, uint64(3), r.After)
	require.Equal(t, "applied", r.Outcome)
	require.NotNil(t, r.BatchDelta)
	require.Equal(t, "op", r.BatchDelta.OperationID)
}

func TestStoreCoverDefinitionPayloadAndPlace(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)
	fields, err := definitionPayload(Table{Name: "demo", Columns: coverColumns(t, "ready,working")}, now)
	require.NoError(t, err)
	require.Equal(t, "ready,working", fields["order"])
	require.Equal(t, "2026-09-27T03:00:00Z", fields["created_at"])

	_, err = definitionPayload(Table{Name: "bad name!", Columns: coverColumns(t, "ready")}, now)
	require.ErrorContains(t, err, "wants letters")

	_, err = definitionPayload(Table{Name: "demo", Columns: []Column{{Name: "a", Projection: "nope"}}}, now)
	require.ErrorContains(t, err, "run: nova-table help")

	for _, c := range []struct {
		name  string
		place Place
		ok    bool
	}{
		{"first", Place{Where: "first"}, true},
		{"last with ref", Place{Where: "last", Ref: "x"}, false},
		{"before without ref", Place{Where: "before"}, false},
		{"after", Place{Where: "after", Ref: "x"}, true},
		{"sideways", Place{Where: "sideways"}, false},
	} {
		err := c.place.valid()
		if c.ok {
			require.NoError(t, err, c.name)
		} else {
			require.Error(t, err, c.name)
		}
	}

	m := map[string]any{}
	(Place{Where: "before", Ref: "x"}).wire(m)
	require.Equal(t, "before", m["where"])
	require.Equal(t, "x", m["ref"])
	m = map[string]any{}
	(Place{Where: "last"}).wire(m)
	require.NotContains(t, m, "ref")
}

func TestStoreCoverWriteWrappers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cols := coverColumns(t, "ready,working")
	tb := Table{Name: "demo", Columns: cols}
	c := newCoverCmdable()

	require.NoError(t, Create(ctx, c, tb, time.Now()))
	require.ErrorContains(t, Create(ctx, c, Table{Name: "bad name!", Columns: cols}, time.Now()), "wants letters")

	footer := "total"
	n, err := Set(ctx, c, "demo", SetOpts{Footer: &footer})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	_, err = Set(ctx, c, "demo", SetOpts{})
	require.ErrorContains(t, err, "wants a change")
	_, err = Set(ctx, c, "demo", SetOpts{Rename: "bad name!"})
	require.ErrorContains(t, err, "new name wants letters")
	_, err = Set(ctx, c, "demo", SetOpts{Columns: []Column{{Name: "a", Projection: "nope"}}})
	require.Error(t, err)
	_, err = Set(ctx, c, "demo", SetOpts{RowOrder: []string{"bad\nkey"}})
	require.ErrorContains(t, err, "wants a non-empty")
	_, err = Set(ctx, c, "demo", SetOpts{ColAdd: &Column{Name: "x", Projection: "nope"}})
	require.Error(t, err)
	_, err = Set(ctx, c, "demo", SetOpts{RowMove: &Reorder{Item: "a", Place: Place{Where: "sideways"}}})
	require.Error(t, err)
	n, err = Set(ctx, c, "demo", SetOpts{ColAdd: &Column{Name: "extra", Projection: Count, Fold: Sum}, ColAt: &Place{Where: "last"}})
	require.NoError(t, err)
	require.Equal(t, 2, n)

	n, err = RowSet(ctx, c, "demo", "build", map[string]string{"ready": "x"})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	_, err = RowSet(ctx, c, "demo", "build", nil)
	require.ErrorContains(t, err, "at least one text value")

	require.NoError(t, RowSetMany(ctx, c, "demo", nil))
	require.ErrorContains(t, RowSetMany(ctx, c, "demo", map[string]map[string]string{"a": {"x": "y"}}, WriteOptions{}, WriteOptions{}), "one write-options value")
	require.NoError(t, RowSetMany(ctx, c, "demo", map[string]map[string]string{"a": {"x": "y"}}))
	require.ErrorContains(t, RowSetMany(ctx, c, "demo", map[string]map[string]string{"a": {}}), "at least one text value")

	n, err = Drop(ctx, c, "demo")
	require.NoError(t, err)
	require.Equal(t, 2, n)
	n, err = DropDefinition(ctx, c, "demo")
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.NoError(t, MemberCreate(ctx, c, "demo", "m1"))

	n, err = RowsAdd(ctx, c, "demo", []string{"a", "b"})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	n, err = RowsAddWithSpec(ctx, c, "demo", []string{"a"}, RowSpec{Label: "L"})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	_, err = RowsAddWithSpec(ctx, c, "demo", []string{"bad\nkey"}, RowSpec{})
	require.ErrorContains(t, err, "wants a non-empty")

	n, err = RowsHide(ctx, c, "demo", true, []string{"a"})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	ok, err := RowDel(ctx, c, "demo", "a")
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, Bind(ctx, c, tb, time.Now()))
	require.ErrorContains(t, Bind(ctx, c, Table{Name: "demo", Columns: cols, Rows: []Row{{Key: "bad\nkey"}}}, time.Now()), "invalid or repeated row key")

	_, err = Clear(ctx, c, "demo")
	require.NoError(t, err)

	n64, err := CellAdd(ctx, c, "demo", "r", "c", "m", 1)
	require.NoError(t, err)
	require.Equal(t, int64(2), n64)
	n64, err = CellRemove(ctx, c, "demo", "r", "c", "m")
	require.NoError(t, err)
	require.Equal(t, int64(2), n64)
	n64, err = CellMove(ctx, c, "demo", "r", "from", "to", "m")
	require.NoError(t, err)
	require.Equal(t, int64(2), n64)
	n64, err = CellsAdd(ctx, c, "demo", "r", "c", 1, []string{"m"})
	require.NoError(t, err)
	require.Equal(t, int64(2), n64)
	n64, err = CellsRemove(ctx, c, "demo", "r", "c", []string{"m"})
	require.NoError(t, err)
	require.Equal(t, int64(2), n64)
	n64, err = CellsMove(ctx, c, "demo", "r", "from", "to", []string{"m"})
	require.NoError(t, err)
	require.Equal(t, int64(2), n64)
	_, err = CellsAdd(ctx, c, "demo", "r", "c", 1, nil)
	require.ErrorContains(t, err, "wants at least one member")

	require.NoError(t, ViewState(ctx, c, "v", "RUN"))
	require.ErrorContains(t, ViewState(ctx, c, "v", "bad\x00"), "at most")
	require.NoError(t, ViewSet(ctx, c, View{Name: "v", Tables: []string{"demo"}}))
	vd, err := ViewDelete(ctx, c, "v")
	require.NoError(t, err)
	require.Equal(t, int64(2), vd)
}

func TestStoreCoverRowAdd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := newCoverCmdable()
	_, err := RowAdd(ctx, c, "demo", "bad\nkey", RowSpec{})
	require.ErrorContains(t, err, "wants a non-empty UTF-8 key")

	cols := coverColumns(t, "ready,working")
	tb := Table{Name: "demo", Columns: cols}
	c.answer(FnRowAdd, []any{"OK", coverPairs(definitionFields(tb)),
		coverPairs(rowFields(tb, Row{Key: "build", Label: "L"})), coverReceipt})
	row, err := RowAdd(ctx, c, "demo", "build", RowSpec{Label: "L"})
	require.NoError(t, err)
	require.Equal(t, "build", row.Key)
	require.Equal(t, "L", row.Label)
	require.Len(t, row.Cells, 2)
}

func TestStoreCoverCellMembers(t *testing.T) {
	t.Parallel()

	c := newCoverCmdable().answer(FnMembers, []any{"OK", []any{"m1", "1.5", "m2", "2"}})
	ms, err := CellMembers(context.Background(), c, "demo", "r", "c")
	require.NoError(t, err)
	require.Equal(t, []Member{{Member: "m1", Score: 1.5}, {Member: "m2", Score: 2}}, ms)

	cm := newCoverCmdable().answer(FnMembers, []any{"OK"})
	_, err = CellMembers(context.Background(), cm, "demo", "r", "c")
	require.ErrorContains(t, err, "malformed members reply")
}

func TestStoreCoverViews(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cg := newCoverCmdable().answer("ns_view_get",
		[]any{"OK", coverPairs(map[string]string{"title": "T", "summary": "S", "state": "RUN", "tables": "a,b"})})
	v, err := ViewGet(ctx, cg, "v")
	require.NoError(t, err)
	require.Equal(t, "T", v.Title)
	require.Equal(t, "S", v.Summary)
	require.Equal(t, "RUN", v.State)
	require.Equal(t, []string{"a", "b"}, v.Tables)

	cmal := newCoverCmdable().answer("ns_view_get", []any{"OK"})
	_, err = ViewGet(ctx, cmal, "v")
	require.ErrorContains(t, err, "malformed reply")

	cl := newCoverCmdable().answer("ns_view_list", []any{"OK", []any{"a", "b"}})
	names, err := ViewList(ctx, cl)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, names)

	cbad := newCoverCmdable().answer("ns_view_list", []any{"OK", []any{"bad name!"}})
	_, err = ViewList(ctx, cbad)
	require.ErrorContains(t, err, "malformed name")

	mis := newCoverCmdable().answer("ns_view_list", []any{"OK", "not a list"})
	_, err = ViewList(ctx, mis)
	require.ErrorContains(t, err, "malformed names")

	p := &coverStorePipe{store: newCoverCmdable()}
	cmd := QueueViewState(ctx, p, "v", "RUN")
	require.NotNil(t, cmd)
	require.NoError(t, ViewStateResult("v", cmd))

	bad := redis.NewCmd(ctx, "x")
	bad.SetErr(errors.New("boom"))
	require.ErrorContains(t, ViewStateResult("v", bad), "boom")

	ref := redis.NewCmd(ctx, "x")
	ref.SetVal([]any{"REFUSED", "NOVIEW"})
	err = ViewStateResult("v", ref)
	require.ErrorIs(t, err, ErrNoView)
	require.True(t, IsRefusal(err))
}

func TestStoreCoverApplyBatchesAndReceipt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	good := BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: "0",
		OperationID: "op-1", Members: []BatchMemberEntry{{ID: "m1", Expect: &MemberExpect{}, Set: map[string]string{"role": "b"}}}}

	c := newCoverCmdable()
	rcs, errs := ApplyBatches(ctx, c, []BatchManifest{{Table: "bad name!"}, good})
	require.Len(t, rcs, 2)
	require.Error(t, errs[0])
	require.ErrorContains(t, errs[1], "not sent")
	require.Empty(t, c.calls)

	cs := newCoverCmdable().answer(FnApply, []any{"OK", coverReceipt})
	rcs, errs = ApplyBatches(ctx, cs, []BatchManifest{good})
	require.NoError(t, errs[0])
	require.Equal(t, "op-1", rcs[0].ID)

	cr := newCoverCmdable().answer(FnApply, []any{"OK", coverReceipt, "REPLAY"})
	rcs, errs = ApplyBatches(ctx, cr, []BatchManifest{good})
	require.NoError(t, errs[0])
	require.True(t, rcs[0].Replay)

	rcs, errs = ApplyBatches(ctx, newCoverCmdable(), nil)
	require.Empty(t, rcs)
	require.Empty(t, errs)

	bad := redis.NewCmd(ctx, "x")
	bad.SetErr(errors.New("gone"))
	_, err := batchReceipt(good, bad)
	require.ErrorIs(t, err, ErrUnknownOutcome)
	require.Contains(t, err.Error(), "changed=unknown")

	ref := redis.NewCmd(ctx, "x")
	ref.SetVal([]any{"REFUSED", "NOTABLE"})
	_, err = batchReceipt(good, ref)
	require.ErrorIs(t, err, ErrNoTable)
	require.True(t, IsRefusal(err))

	mis := redis.NewCmd(ctx, "x")
	mis.SetVal([]any{"OK"})
	_, err = batchReceipt(good, mis)
	require.ErrorContains(t, err, "missing committed receipt")

	mal := redis.NewCmd(ctx, "x")
	mal.SetVal([]any{"OK", []any{"NOPE"}})
	_, err = batchReceipt(good, mal)
	require.ErrorContains(t, err, "malformed committed receipt")

	d := redis.NewCmd(ctx, "x")
	d.SetVal([]any{"OK", []any{"RECEIPT", "id", "0", "0", "1", "applied", "not-json"}})
	_, err = batchReceipt(good, d)
	require.ErrorContains(t, err, "unmarshal batch delta")
}

func TestStoreCoverReadSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, err := QueueReadSet(ctx, newCoverCmdable(), "bad name!", ReadSetScope{Members: []string{"m1"}})
	require.ErrorContains(t, err, "invalid name")

	cmd, err := QueueReadSetMembers(ctx, newCoverCmdable(), "demo", []string{"m1"})
	require.NoError(t, err)
	require.NotNil(t, cmd)

	require.Equal(t, "5", fastString(int64(5)))
	require.Equal(t, "b", fastString([]byte("b")))
	require.Equal(t, "s", fastString("s"))
	require.Equal(t, "true", fastString(true))

	u, err := fastUint(int64(7))
	require.NoError(t, err)
	require.Equal(t, uint64(7), u)
	u, err = fastUint("8")
	require.NoError(t, err)
	require.Equal(t, uint64(8), u)
	_, err = fastUint(true)
	require.Error(t, err)

	f, st, err := fastFloat("2.5")
	require.NoError(t, err)
	require.Equal(t, 2.5, f)
	require.Equal(t, "2.5", st)
	f, st, err = fastFloat(float64(3))
	require.NoError(t, err)
	require.Equal(t, 3.0, f)
	require.Equal(t, "3", st)
	f, st, err = fastFloat(int64(4))
	require.NoError(t, err)
	require.Equal(t, 4.0, f)
	require.Equal(t, "4", st)
	_, _, err = fastFloat(true)
	require.Error(t, err)

	fieldItem := []any{"m1", "1", "1", "row", "col", "2.5", []any{"k", "v"}}
	setReply := []any{"SET", "demo", "0", "1", []any{fieldItem}, []any{"gone"}}
	cs := newCoverCmdable().answer(FnReadSet, setReply)
	cmd2, err := QueueReadSet(ctx, cs, "demo", ReadSetScope{Members: []string{"m1"}}, 3)
	require.NoError(t, err)
	res, err := cmd2.Result()
	require.NoError(t, err)
	require.Equal(t, uint64(0), res.Epoch)
	require.Equal(t, uint64(1), res.Revision)
	m, ok := res.Member("m1")
	require.True(t, ok)
	require.Equal(t, "row", m.Row)
	require.Equal(t, "col", m.Col)
	require.Equal(t, 2.5, m.Score)
	require.Equal(t, "v", m.Fields["k"])
	_, ok = res.Member("nope")
	require.False(t, ok)
	require.True(t, res.IsMissing("gone"))
	require.False(t, res.IsMissing("m1"))

	_, err = ReadSet(ctx, cs, "demo", ReadSetScope{Selection: []CellSelection{{Row: "r", Col: "c"}}})
	require.NoError(t, err)
	_, err = ReadSetMembers(ctx, cs, "demo", []string{"m1"})
	require.NoError(t, err)

	ce := newCoverCmdable().fail(FnReadSet, errors.New("gone"))
	cmd3, err := QueueReadSet(ctx, ce, "demo", ReadSetScope{Members: []string{"m1"}})
	require.NoError(t, err)
	_, err = cmd3.Result()
	require.ErrorContains(t, err, "gone")

	cr := newCoverCmdable().answer(FnReadSet, []any{"REFUSED", "NOTABLE"})
	cmd4, err := QueueReadSet(ctx, cr, "demo", ReadSetScope{Members: []string{"m1"}})
	require.NoError(t, err)
	_, err = cmd4.Result()
	require.ErrorIs(t, err, ErrNoTable)
	require.True(t, IsRefusal(err))

	cm := newCoverCmdable().answer(FnReadSet, []any{"OK"})
	cmd5, err := QueueReadSet(ctx, cm, "demo", ReadSetScope{Members: []string{"m1"}})
	require.NoError(t, err)
	_, err = cmd5.Result()
	require.ErrorContains(t, err, "malformed read set reply")
}

// coverRedisError is a redis.Error (an answer from the store's protocol) that
// carries no connection.
type coverRedisError string

func (e coverRedisError) Error() string { return string(e) }
func (coverRedisError) RedisError()     {}

func TestStoreCoverValidationHelpers(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateRowKeys([]string{"a", "b"}))
	require.ErrorContains(t, validateRowKeys([]string{"bad\nkey"}), "wants a non-empty")
	require.True(t, isRedisReply(coverRedisError("boom")))
	require.False(t, isRedisReply(errors.New("boom")))
	require.False(t, isRedisReply(nil))
}
