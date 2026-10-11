package ntable

import (
	"context"
	"sort"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The read verbs of read.go run against a store. These tests stand in the
// store's answers through the go-redis command types the package's own seams
// hand back, so every path runs with no store, socket, subprocess or clock.

// coverPipe is the pipeliner QueueCells and Reader.Queue write through: the
// seeds are the store's answers, keyed by the cell key the read queues. An
// unseeded ZSCORE answers redis.Nil, as the store answers for an absent
// member; an unseeded count or members read answers empty, as the store
// answers an absent key.
type coverPipe struct {
	redis.Pipeliner
	zcard  map[string]*redis.IntCmd
	zscore map[string]*redis.FloatCmd
	zrange map[string]*redis.ZSliceCmd
	fn     string
	key    string
	args   []any
	read   *redis.Cmd
}

func (p *coverPipe) ZCard(_ context.Context, key string) *redis.IntCmd {
	if cmd, ok := p.zcard[key]; ok {
		return cmd
	}
	return redis.NewIntCmd(context.Background())
}

func (p *coverPipe) ZScore(_ context.Context, key, _ string) *redis.FloatCmd {
	if cmd, ok := p.zscore[key]; ok {
		return cmd
	}
	cmd := redis.NewFloatCmd(context.Background())
	cmd.SetErr(redis.Nil)
	return cmd
}

func (p *coverPipe) ZRangeWithScores(_ context.Context, key string, _, _ int64) *redis.ZSliceCmd {
	if cmd, ok := p.zrange[key]; ok {
		return cmd
	}
	return redis.NewZSliceCmd(context.Background())
}

func (p *coverPipe) FCallRO(_ context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	p.fn, p.key, p.args = fn, keys[0], args
	return p.read
}

// coverStore is the cmdable the read verbs of read.go call through: it
// records the one read-only function call and answers with the command the
// test set.
type coverStore struct {
	redis.Cmdable
	fn   string
	key  string
	args []any
	read *redis.Cmd
}

func (s *coverStore) FCallRO(_ context.Context, fn string, keys []string, args ...any) *redis.Cmd {
	s.fn, s.key, s.args = fn, keys[0], args
	return s.read
}

func coverCount(n int64) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	cmd.SetVal(n)
	return cmd
}

func coverFailedInt(err error) *redis.IntCmd {
	cmd := redis.NewIntCmd(context.Background())
	cmd.SetErr(err)
	return cmd
}

func coverZS(zs ...redis.Z) *redis.ZSliceCmd {
	cmd := redis.NewZSliceCmd(context.Background())
	cmd.SetVal(zs)
	return cmd
}

func coverFailedZS(err error) *redis.ZSliceCmd {
	cmd := redis.NewZSliceCmd(context.Background())
	cmd.SetErr(err)
	return cmd
}

func coverRead(reply []any, err error) *redis.Cmd {
	cmd := redis.NewCmd(context.Background())
	if err != nil {
		cmd.SetErr(err)
	} else {
		cmd.SetVal(reply)
	}
	return cmd
}

func coverScore(score float64) *redis.FloatCmd {
	cmd := redis.NewFloatCmd(context.Background())
	cmd.SetVal(score)
	return cmd
}

var (
	// coverDef is the definition hash of a two-column table: todo (count,
	// sum) and names (members, union).
	coverDef = []any{
		"order", "todo,names",
		"footer", "",
		"col:todo", "count:sum:0:",
		"col:names", "members:union:0:",
	}
	// coverCells are a count cell and a members cell, both read.
	coverCells = []any{
		[]any{"OK", "2", []any{"a", "1", "b", "2.5"}},
		[]any{"OK", "2", []any{"a", "1", "b", "2.5"}},
	}
	// coverUnreadCells put the count cell's set on the wrong type.
	coverUnreadCells = []any{
		[]any{"UNREAD", "why", "table:demo:cell:r1:todo", "string"},
		[]any{"OK", "2", []any{"a", "1", "b", "2.5"}},
	}
)

func coverRow(cells []any) []any { return []any{"r1", []any{"label", "Row 1"}, cells} }

// coverSnapshot is the store's read reply of the two-column table, one row,
// with the table's properties at the read epoch when props is set.
func coverSnapshot(cells []any, props []any) []any {
	reply := []any{"TABLE", coverDef, []any{coverRow(cells)}}
	if props != nil {
		reply = append(reply, props)
	}
	return reply
}

func coverPositions[V any](m map[[2]int]V) [][2]int {
	out := make([][2]int, 0, len(m))
	for at := range m {
		out = append(out, at)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// TestReadCoverQueueCellsResult pins QueueCells and CellsCmd.Result: one
// count command per count cell (a row's excluded member rides the same
// pipeline and is left out of the count), one members command per members,
// first or last cell, nothing for a text column or a row short of cells, and
// Result filling every cell from the answers, leaving a cell whose read did
// not come back unread, never a false 0.
func TestReadCoverQueueCellsResult(t *testing.T) {
	t.Parallel()
	tbl := &Table{
		Name: "demo",
		Columns: []Column{
			{Name: "todo", Projection: Count, Fold: Sum},
			{Name: "names", Projection: Members, Fold: Union},
			{Name: "note", Projection: Text},
		},
		Rows: []Row{
			{Key: "r1", Cells: []Cell{{Key: CellKey("demo", "r1", "todo")}, {Key: CellKey("demo", "r1", "names")}, {}}},
			{Key: "r2", Exclude: "stop", Cells: []Cell{{Key: CellKey("demo", "r2", "todo")}, {Key: CellKey("demo", "r2", "names")}, {}}},
			{Key: "r3", Cells: []Cell{{Key: CellKey("demo", "r3", "todo")}}},
			{Key: "r4", Exclude: "gone", Cells: []Cell{{Key: CellKey("demo", "r4", "todo")}, {Key: CellKey("demo", "r4", "names")}, {}}},
		},
	}
	pipe := &coverPipe{
		zcard: map[string]*redis.IntCmd{
			CellKey("demo", "r1", "todo"): coverCount(3),
			CellKey("demo", "r2", "todo"): coverCount(4),
			CellKey("demo", "r4", "todo"): coverCount(2),
		},
		zscore: map[string]*redis.FloatCmd{
			CellKey("demo", "r2", "todo"): coverScore(1.5),
		},
		zrange: map[string]*redis.ZSliceCmd{
			CellKey("demo", "r1", "names"): coverZS(redis.Z{Score: 1, Member: "a"}, redis.Z{Score: 2.5, Member: "b"}),
			CellKey("demo", "r2", "names"): coverZS(redis.Z{Score: 1, Member: "a"}, redis.Z{Score: 3, Member: "stop"}),
			CellKey("demo", "r4", "names"): coverZS(redis.Z{Score: 1, Member: "a"}),
		},
	}
	q := QueueCells(context.Background(), pipe, tbl)
	assert.Equal(t, [][2]int{{0, 0}, {1, 0}, {2, 0}, {3, 0}}, coverPositions(q.counts), "one count per count cell, none for text columns or cells a short row does not have")
	assert.Equal(t, [][2]int{{0, 1}, {1, 1}, {3, 1}}, coverPositions(q.lists), "one members read per members cell")
	q.Result()
	assert.Equal(t, int64(3), tbl.Rows[0].Cells[0].Count)
	assert.False(t, tbl.Rows[0].Cells[0].Unread)
	assert.Equal(t, int64(3), tbl.Rows[1].Cells[0].Count, "the excluded member's presence comes off the count")
	assert.Equal(t, int64(2), tbl.Rows[3].Cells[0].Count, "an excluded member that is absent leaves the count alone")
	assert.Equal(t, []Member{{"a", 1}, {"b", 2.5}}, tbl.Rows[0].Cells[1].Members)
	assert.Equal(t, int64(2), tbl.Rows[0].Cells[1].Count)
	assert.Equal(t, []Member{{"a", 1}}, tbl.Rows[1].Cells[1].Members, "the excluded member is dropped from the members read")
	assert.Equal(t, int64(1), tbl.Rows[1].Cells[1].Count)
	assert.Equal(t, Cell{}, tbl.Rows[0].Cells[2], "a text cell holds no set and stays alone")

	t.Run("refusal: a count that did not come back leaves its cell unread", func(t *testing.T) {
		t.Parallel()
		tbl := &Table{Name: "demo", Columns: []Column{{Name: "todo", Projection: Count}},
			Rows: []Row{{Key: "r1", Cells: []Cell{{Key: CellKey("demo", "r1", "todo")}}}}}
		pipe := &coverPipe{zcard: map[string]*redis.IntCmd{CellKey("demo", "r1", "todo"): coverFailedInt(assert.AnError)}}
		QueueCells(context.Background(), pipe, tbl).Result()
		assert.True(t, tbl.Rows[0].Cells[0].Unread)
		assert.Zero(t, tbl.Rows[0].Cells[0].Count)
	})
	t.Run("refusal: a members read that did not come back leaves its cell unread", func(t *testing.T) {
		t.Parallel()
		tbl := &Table{Name: "demo", Columns: []Column{{Name: "names", Projection: Members}},
			Rows: []Row{{Key: "r1", Cells: []Cell{{Key: CellKey("demo", "r1", "names")}}}}}
		pipe := &coverPipe{zrange: map[string]*redis.ZSliceCmd{CellKey("demo", "r1", "names"): coverFailedZS(assert.AnError)}}
		QueueCells(context.Background(), pipe, tbl).Result()
		assert.True(t, tbl.Rows[0].Cells[0].Unread)
		assert.Nil(t, tbl.Rows[0].Cells[0].Members)
		assert.Zero(t, tbl.Rows[0].Cells[0].Count)
	})
}

// TestReadCoverReaderQueueResult pins NewReader, Reader.Queue and
// ReadCmd.Result: the read queues one read-only function call at the table's
// definition naming the read, its Result decodes the snapshot with changed
// always false, a refusal answering the store's refusal, and a store that
// did not answer a transport error naming the table.
func TestReadCoverReaderQueueResult(t *testing.T) {
	t.Parallel()
	t.Run("main path: one queued read decodes the snapshot it answers", func(t *testing.T) {
		t.Parallel()
		pipe := &coverPipe{read: coverRead(coverSnapshot(coverCells, nil), nil)}
		r := NewReader("demo")
		assert.Equal(t, "demo", r.Name)
		rc := r.Queue(context.Background(), pipe)
		assert.Equal(t, FnRead, pipe.fn)
		assert.Equal(t, DefKey("demo"), pipe.key)
		assert.Equal(t, []any{"demo", "read"}, pipe.args)
		got, changed, err := rc.Result()
		require.NoError(t, err)
		assert.False(t, changed, "Result's changed is always false")
		assert.Equal(t, "demo", got.Name)
		require.Len(t, got.Columns, 2)
		assert.Equal(t, "todo", got.Columns[0].Name)
		assert.Equal(t, Count, got.Columns[0].Projection)
		require.Len(t, got.Rows, 1)
		assert.Equal(t, "Row 1", got.Rows[0].Label)
		assert.Equal(t, int64(2), got.Rows[0].Cells[0].Count)
	})
	t.Run("refusal: a refused read answers the store's refusal", func(t *testing.T) {
		t.Parallel()
		pipe := &coverPipe{read: coverRead([]any{"REFUSED", "NOTABLE", "demo"}, nil)}
		_, _, err := NewReader("demo").Queue(context.Background(), pipe).Result()
		assert.ErrorIs(t, err, ErrNoTable)
		assert.ErrorContains(t, err, `table "demo": no such table`)
		assert.ErrorContains(t, err, "run: nova-table create")
	})
	t.Run("refusal: a store that did not answer names the table", func(t *testing.T) {
		t.Parallel()
		pipe := &coverPipe{read: coverRead(nil, assert.AnError)}
		_, _, err := NewReader("demo").Queue(context.Background(), pipe).Result()
		assert.ErrorContains(t, err, `read table "demo"`)
	})
}

// TestReadCoverRead pins Read: one read-only function call of the table's
// definition naming the read, the snapshot decoded, the store's refusal
// answered, and a store that did not answer a transport error with the next
// command.
func TestReadCoverRead(t *testing.T) {
	t.Parallel()
	t.Run("main path: one call reads the snapshot", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead(coverSnapshot(coverCells, nil), nil)}
		got, err := Read(context.Background(), s, "demo")
		require.NoError(t, err)
		assert.Equal(t, "demo", got.Name)
		assert.Equal(t, FnRead, s.fn)
		assert.Equal(t, DefKey("demo"), s.key)
		assert.Equal(t, []any{"demo", "read"}, s.args)
		assert.Equal(t, int64(2), got.Rows[0].Cells[1].Count)
		assert.Equal(t, []Member{{"a", 1}, {"b", 2.5}}, got.Rows[0].Cells[1].Members)
	})
	t.Run("refusal: a refused read answers the store's refusal", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead([]any{"REFUSED", "NOTABLE", "demo"}, nil)}
		_, err := Read(context.Background(), s, "demo")
		assert.ErrorIs(t, err, ErrNoTable)
	})
	t.Run("refusal: a store that did not answer carries the remedy", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead(nil, assert.AnError)}
		_, err := Read(context.Background(), s, "demo")
		assert.ErrorContains(t, err, `table "demo": ns_table_read`)
		assert.ErrorContains(t, err, "run: nova-table show 'demo'")
	})
}

// TestReadCoverReaderRead pins Reader.Read: the same one call as Read,
// through the reader, with the store's refusal answered.
func TestReadCoverReaderRead(t *testing.T) {
	t.Parallel()
	t.Run("main path: the reader reads the snapshot in one call", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead(coverSnapshot(coverCells, nil), nil)}
		got, err := NewReader("demo").Read(context.Background(), s)
		require.NoError(t, err)
		assert.Equal(t, "demo", got.Name)
		assert.Equal(t, []any{"demo", "read"}, s.args)
		assert.Equal(t, int64(2), got.Rows[0].Cells[0].Count)
	})
	t.Run("refusal: a refused read answers the store's refusal", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead([]any{"REFUSED", "NOTABLE", "demo"}, nil)}
		_, err := NewReader("demo").Read(context.Background(), s)
		assert.ErrorIs(t, err, ErrNoTable)
	})
}

// TestReadCoverShape pins Shape: the call names the shape, the snapshot
// decodes, and a refusal answers the store's refusal.
func TestReadCoverShape(t *testing.T) {
	t.Parallel()
	t.Run("main path: the shape is one call naming the shape", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead(coverSnapshot(coverCells, nil), nil)}
		got, err := Shape(context.Background(), s, "demo")
		require.NoError(t, err)
		assert.Equal(t, "demo", got.Name)
		assert.Equal(t, []any{"demo", "shape"}, s.args)
		assert.Equal(t, "todo", got.Columns[0].Name)
	})
	t.Run("refusal: a refused shape answers the store's refusal", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead([]any{"REFUSED", "NOTABLE", "demo"}, nil)}
		_, err := Shape(context.Background(), s, "demo")
		assert.ErrorIs(t, err, ErrNoTable)
	})
}

// TestReadCoverReadAt pins ReadAt: the materialised epoch rides the call as
// its decimal text, the snapshot decodes, and a refusal answers the store's
// refusal.
func TestReadCoverReadAt(t *testing.T) {
	t.Parallel()
	t.Run("main path: the epoch rides the call as its decimal text", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead(coverSnapshot(coverCells, nil), nil)}
		got, err := ReadAt(context.Background(), s, "demo", 7)
		require.NoError(t, err)
		assert.Equal(t, "demo", got.Name)
		assert.Equal(t, []any{"demo", "read", "7"}, s.args)
		assert.Equal(t, int64(2), got.Rows[0].Cells[0].Count)
	})
	t.Run("refusal: a refused epoch read answers the store's refusal", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead([]any{"REFUSED", "NOTABLE", "demo"}, nil)}
		_, err := ReadAt(context.Background(), s, "demo", 7)
		assert.ErrorIs(t, err, ErrNoTable)
	})
}

// TestReadCoverFlatHash pins flatHash: alternating pairs become a map with
// values printed as words, an odd reply or a reply that is not a list is
// refused as malformed.
func TestReadCoverFlatHash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		in    any
		want  map[string]string
		scene string
	}{
		{"main path: pairs become a map, values printed as words", []any{"a", "1", "b", 2}, map[string]string{"a": "1", "b": "2"}, ""},
		{"refusal: an odd reply is a malformed hash", []any{"a", "1", "b"}, nil, "malformed hash"},
		{"refusal: a reply that is not a list is a malformed hash", "x", nil, "malformed hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, err := flatHash(tc.in)
			if tc.scene == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.want, h)
				return
			}
			assert.ErrorContains(t, err, tc.scene)
			assert.Nil(t, h)
		})
	}
}

// TestReadCoverFlatMembers pins flatMembers: alternating pairs become
// members in order with their scores parsed, an odd reply or a reply that is
// not a list is refused as malformed, and a score that is not a number is
// refused.
func TestReadCoverFlatMembers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		in    any
		want  []Member
		scene string
	}{
		{"main path: pairs become members with their scores", []any{"a", "1", "b", "2.5"}, []Member{{"a", 1}, {"b", 2.5}}, ""},
		{"refusal: an odd reply is a malformed members list", []any{"a", "1", "b"}, nil, "malformed members"},
		{"refusal: a reply that is not a list is a malformed members list", "x", nil, "malformed members"},
		{"refusal: a score that is not a number is refused", []any{"a", "x", "b", "2"}, nil, `parsing "x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ms, err := flatMembers(tc.in)
			if tc.scene == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.want, ms)
				return
			}
			assert.ErrorContains(t, err, tc.scene)
			assert.Nil(t, ms)
		})
	}
}

// TestReadCoverDecodeSnapshot pins decodeSnapshot: the definition, row and
// cells decode, a fourth value is the table's properties, an unread cell
// says why, and every malformed reply is named.
func TestReadCoverDecodeSnapshot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		reply []any
		scene func(t *testing.T, tbl Table, err error)
	}{
		{
			name:  "main path: a three-value snapshot decodes its table",
			reply: coverSnapshot(coverCells, nil),
			scene: func(t *testing.T, tbl Table, err error) {
				require.NoError(t, err)
				assert.Equal(t, "demo", tbl.Name)
				require.Len(t, tbl.Columns, 2)
				assert.Equal(t, Sum, tbl.Columns[0].Fold)
				assert.Equal(t, Union, tbl.Columns[1].Fold)
				require.Len(t, tbl.Rows, 1)
				assert.Equal(t, "r1", tbl.Rows[0].Key)
				assert.Equal(t, int64(2), tbl.Rows[0].Cells[0].Count)
				assert.Equal(t, []Member{{"a", 1}, {"b", 2.5}}, tbl.Rows[0].Cells[1].Members)
				assert.Nil(t, tbl.Props)
			},
		},
		{
			name:  "main path: a fourth value is the properties and an unread cell says why",
			reply: coverSnapshot(coverUnreadCells, []any{"index", "ws:one"}),
			scene: func(t *testing.T, tbl Table, err error) {
				require.NoError(t, err)
				assert.Equal(t, map[string]string{"index": "ws:one"}, tbl.Props)
				assert.True(t, tbl.Rows[0].Cells[0].Unread)
				assert.Equal(t, "key table:demo:cell:r1:todo is string, expected zset", tbl.Rows[0].Cells[0].UnreadWhy)
			},
		},
		{
			name:  "refusal: the store's refusal is the error",
			reply: []any{"REFUSED", "NOTABLE", "demo"},
			scene: func(t *testing.T, _ Table, err error) { assert.ErrorIs(t, err, ErrNoTable) },
		},
		{
			name:  "refusal: an empty definition is no table",
			reply: []any{"TABLE", []any{}, coverCells},
			scene: func(t *testing.T, _ Table, err error) { assert.ErrorIs(t, err, ErrNoTable) },
		},
		{
			name:  "refusal: a reply that opens otherwise than TABLE is a malformed snapshot",
			reply: []any{"ROWS"},
			scene: func(t *testing.T, _ Table, err error) {
				assert.ErrorContains(t, err, `table "demo": malformed snapshot`)
			},
		},
		{
			name:  "refusal: rows that are not a list are named",
			reply: []any{"TABLE", coverDef, "x"},
			scene: func(t *testing.T, _ Table, err error) { assert.ErrorContains(t, err, `table "demo": malformed rows`) },
		},
		{
			name:  "refusal: a row that is not three values is named",
			reply: []any{"TABLE", coverDef, []any{[]any{"r1", []any{}}}},
			scene: func(t *testing.T, _ Table, err error) { assert.ErrorContains(t, err, `table "demo": malformed row`) },
		},
		{
			name:  "refusal: cells of another width than the columns are named",
			reply: []any{"TABLE", coverDef, []any{coverRow([]any{coverCells[0]})}},
			scene: func(t *testing.T, _ Table, err error) { assert.ErrorContains(t, err, `row "r1": malformed cells`) },
		},
		{
			name:  "refusal: a cell that is not values is named",
			reply: []any{"TABLE", coverDef, []any{coverRow([]any{"x", coverCells[1]})}},
			scene: func(t *testing.T, _ Table, err error) { assert.ErrorContains(t, err, `row "r1": malformed cell`) },
		},
		{
			name:  "refusal: a cell without its count is named",
			reply: []any{"TABLE", coverDef, []any{coverRow([]any{[]any{"OK", "2"}, coverCells[1]})}},
			scene: func(t *testing.T, _ Table, err error) {
				assert.ErrorContains(t, err, `row "r1": malformed cell values`)
			},
		},
		{
			name:  "refusal: a count that is not a number is named",
			reply: []any{"TABLE", coverDef, []any{coverRow([]any{[]any{"OK", "x", []any{}}, coverCells[1]})}},
			scene: func(t *testing.T, _ Table, err error) { assert.ErrorContains(t, err, `parsing "x"`) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tbl, err := decodeSnapshot("demo", tc.reply)
			tc.scene(t, tbl, err)
		})
	}
}

// TestReadCoverSummariesList pins Summaries and List: the registry reads as
// one summary per table and List keeps only the names, a refusal answers the
// store's refusal, a store that did not answer carries the remedy, and a
// malformed entry is named.
func TestReadCoverSummariesList(t *testing.T) {
	t.Parallel()
	t.Run("main path: the registry reads as summaries and List keeps the names", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead([]any{"TABLES", []any{"demo", "3", "2"}, []any{"other", "1", "0"}}, nil)}
		sums, err := Summaries(context.Background(), s)
		require.NoError(t, err)
		assert.Equal(t, []Summary{{"demo", 3, 2}, {"other", 1, 0}}, sums)
		assert.Equal(t, FnList, s.fn)
		assert.Equal(t, Registry, s.key)
		names, err := List(context.Background(), s)
		require.NoError(t, err)
		assert.Equal(t, []string{"demo", "other"}, names)
	})
	t.Run("refusal: a refused registry is the store's refusal", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead([]any{"REFUSED", "NOTABLE", "demo"}, nil)}
		_, err := Summaries(context.Background(), s)
		assert.ErrorIs(t, err, ErrNoTable)
	})
	t.Run("refusal: a store that did not answer carries the remedy", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead(nil, assert.AnError)}
		_, err := Summaries(context.Background(), s)
		assert.ErrorContains(t, err, "list tables")
		assert.ErrorContains(t, err, "run: nova-table help")
	})
	t.Run("refusal: a malformed entry is named", func(t *testing.T) {
		t.Parallel()
		s := &coverStore{read: coverRead([]any{"TABLES", []any{"demo", "3"}}, nil)}
		_, err := Summaries(context.Background(), s)
		assert.ErrorContains(t, err, "malformed entry")
	})
}
