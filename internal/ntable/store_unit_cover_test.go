package ntable

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverArgCmdable records the arguments of each function call beside the
// coverCmdable reply keyed by function name, so a test can read the spec Set
// and Bind build. It embeds coverCmdable and adds nothing to the transport.
type coverArgCmdable struct {
	*coverCmdable
	args map[string][]any
}

func newCoverArgCmdable() *coverArgCmdable {
	return &coverArgCmdable{coverCmdable: newCoverCmdable(), args: map[string][]any{}}
}

func (c *coverArgCmdable) FCall(ctx context.Context, fn string, keys []string, a ...any) *redis.Cmd {
	c.args[fn] = a
	return c.coverCmdable.FCall(ctx, fn, keys, a...)
}

// TestNtableStoreCoverRefused pins operation.refused for one row per refusal
// kind: the error text, the sentinel errors.Is finds, the *Refusal Next and
// Guarded, and the two helpers errors.As names (*BoundError, *LimitError).
func TestNtableStoreCoverRefused(t *testing.T) {
	t.Parallel()

	plain := operation{table: "demo"}
	batch := operation{table: "demo", batch: true}
	readSet := operation{table: "demo", readSet: true}

	cases := []struct {
		name        string
		op          operation
		reply       []any
		wantNil     bool
		wantIs      error
		wantText    []string
		wantNext    string
		wantNextHas []string
		wantRefusal bool
		wantGuarded bool
		check       func(*testing.T, error)
	}{
		{
			name:     "empty reply",
			op:       plain,
			reply:    []any{},
			wantText: []string{"empty function reply", "run: nova-table show 'demo'"},
		},
		{
			name:    "not refused",
			op:      plain,
			reply:   []any{"OK", int64(1)},
			wantNil: true,
		},
		{
			name:     "refused alone is malformed",
			op:       plain,
			reply:    []any{"REFUSED"},
			wantText: []string{"malformed refusal"},
		},
		{
			name:        "stale",
			op:          plain,
			reply:       []any{"REFUSED", "STALE", "2", "3"},
			wantIs:      ErrStale,
			wantText:    []string{"requested epoch is stale", "requested 2, active 3"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
		{
			name:        "epoch ahead",
			op:          plain,
			reply:       []any{"REFUSED", "EPOCHAHEAD", "5", "3"},
			wantIs:      ErrEpochAhead,
			wantText:    []string{"ahead of the active epoch", "requested epoch 5, active epoch 3"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
		{
			name:        "member epoch guarded below",
			op:          batch,
			reply:       []any{"REFUSED", "MEMBEREPOCH", "m", "1", "3"},
			wantIs:      ErrMemberEpoch,
			wantText:    []string{"the member is of epoch 1, the active epoch is 3"},
			wantNext:    "nova-table member read --at-epoch 1 'demo' 'm'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "member epoch unguarded",
			op:          plain,
			reply:       []any{"REFUSED", "MEMBEREPOCH", "m", "1", "3"},
			wantIs:      ErrMemberEpoch,
			wantText:    []string{"member belongs to another epoch"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
		{
			name:        "member exists guarded",
			op:          batch,
			reply:       []any{"REFUSED", "MEMBEREXISTS", "m", "placed at r:c"},
			wantIs:      ErrMemberExists,
			wantText:    []string{"expected absent, observed placed at r:c"},
			wantNext:    "nova-table member read 'demo' 'm'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "member exists unguarded",
			op:          plain,
			reply:       []any{"REFUSED", "MEMBEREXISTS", "m", "placed at r:c"},
			wantIs:      ErrMemberExists,
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
		{
			name:        "drift set only",
			op:          batch,
			reply:       []any{"REFUSED", "DRIFT", "r", "c", "m", "set-only"},
			wantIs:      ErrDrift,
			wantText:    []string{"owned set at row \"r\" column \"c\" holds member \"m\""},
			wantNext:    "nova-table cell members 'demo' 'r' 'c'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "drift record only",
			op:          batch,
			reply:       []any{"REFUSED", "DRIFT", "r", "c", "m"},
			wantIs:      ErrDrift,
			wantText:    []string{"record places member \"m\" at row \"r\" column \"c\""},
			wantNext:    "nova-table cell members 'demo' 'r' 'c'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "drift bad place",
			op:          batch,
			reply:       []any{"REFUSED", "DRIFT", "m", "r:c"},
			wantIs:      ErrDrift,
			wantText:    []string{"place \"r:c\", which is not a usable owned cell"},
			wantNext:    "nova-table check 'demo'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "drift short",
			op:          batch,
			reply:       []any{"REFUSED", "DRIFT", "x"},
			wantIs:      ErrDrift,
			wantText:    []string{"member record and owned set disagree: [x]"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "notable guarded",
			op:          batch,
			reply:       []any{"REFUSED", "NOTABLE"},
			wantIs:      ErrNoTable,
			wantNext:    "nova-table list",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "notable unguarded",
			op:          plain,
			reply:       []any{"REFUSED", "NOTABLE"},
			wantIs:      ErrNoTable,
			wantNext:    "nova-table create 'demo' --columns <columns>",
			wantRefusal: true,
		},
		{
			name:        "orphan",
			op:          plain,
			reply:       []any{"REFUSED", "ORPHAN", "4"},
			wantIs:      ErrOrphan,
			wantText:    []string{"left behind by a dropped definition", "nova-table drop 'demo' --definition --epoch 4"},
			wantNext:    "nova-table drop 'demo' --definition --epoch 4",
			wantRefusal: true,
		},
		{
			name:        "residue",
			op:          plain,
			reply:       []any{"REFUSED", "RESIDUE", "2", "k1", "k2"},
			wantIs:      ErrResidue,
			wantText:    []string{"keys of an earlier table of this name are left: k1, k2"},
			wantNext:    "nova-table drop 'demo' --definition --epoch 2",
			wantRefusal: true,
		},
		{
			name:     "residue short is malformed",
			op:       plain,
			reply:    []any{"REFUSED", "RESIDUE", "2"},
			wantText: []string{"malformed residue refusal"},
		},
		{
			name:        "occupied with a flag member",
			op:          plain,
			reply:       []any{"REFUSED", "OCCUPIED", "r", "c", []any{"-m"}},
			wantIs:      ErrOccupied,
			wantText:    []string{"move each member to a retained owned cell or remove it first"},
			wantNextHas: []string{"nova-table cell remove", "-- "},
			wantRefusal: true,
		},
		{
			name:     "occupied not a member list",
			op:       plain,
			reply:    []any{"REFUSED", "OCCUPIED", "r", "c", "not-a-list"},
			wantText: []string{"malformed occupied members"},
		},
		{
			name:     "occupied wrong length",
			op:       plain,
			reply:    []any{"REFUSED", "OCCUPIED", "r", "c"},
			wantText: []string{"malformed occupied-cell refusal"},
		},
		{
			name:        "occupied cells",
			op:          plain,
			reply:       []any{"REFUSED", "OCCUPIEDCELLS", []any{[]any{"r", "c", []any{"-m"}}}},
			wantIs:      ErrOccupied,
			wantText:    []string{"move the members or remove them first"},
			wantNextHas: []string{"nova-table cell remove", "-- "},
			wantRefusal: true,
		},
		{
			name:     "occupied cells not a list",
			op:       plain,
			reply:    []any{"REFUSED", "OCCUPIEDCELLS", "x"},
			wantText: []string{"malformed occupied cells"},
		},
		{
			name:     "occupied cells wrong size",
			op:       plain,
			reply:    []any{"REFUSED", "OCCUPIEDCELLS", []any{[]any{"r", "c"}}},
			wantText: []string{"malformed occupied cell"},
		},
		{
			name:     "occupied cells no members",
			op:       plain,
			reply:    []any{"REFUSED", "OCCUPIEDCELLS", []any{[]any{"r", "c", []any{}}}},
			wantText: []string{"malformed occupied members"},
		},
		{
			name:        "view table",
			op:          plain,
			reply:       []any{"REFUSED", "VIEWTABLE", "other"},
			wantText:    []string{`referenced table "other" does not exist`},
			wantNext:    "nova-table create 'other' --columns <columns>",
			wantRefusal: true,
		},
		{
			name:     "view table wrong length",
			op:       plain,
			reply:    []any{"REFUSED", "VIEWTABLE"},
			wantText: []string{"malformed view-table refusal"},
		},
		{
			name:        "summary",
			op:          plain,
			reply:       []any{"REFUSED", "SUMMARY", "t", "c"},
			wantText:    []string{`summary wants a count column in table "t"; "c" is not one`},
			wantNext:    "nova-table show 't'",
			wantRefusal: true,
		},
		{
			name:     "summary wrong length",
			op:       plain,
			reply:    []any{"REFUSED", "SUMMARY", "t"},
			wantText: []string{"malformed summary refusal"},
		},
		{
			name:        "formula missing column",
			op:          plain,
			reply:       []any{"REFUSED", "FORMULA", "f", "nope", "missing"},
			wantText:    []string{`it reads column "nope", which the table does not have`},
			wantNext:    "nova-table col add 'demo' 'nope'",
			wantRefusal: true,
		},
		{
			name:        "formula wrong type",
			op:          plain,
			reply:       []any{"REFUSED", "FORMULA", "f", "note", "text"},
			wantText:    []string{`it reads column "note", a text column`},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
		{
			name:     "formula wrong length",
			op:       plain,
			reply:    []any{"REFUSED", "FORMULA", "f"},
			wantText: []string{"malformed formula refusal"},
		},
		{
			name:        "bound guarded",
			op:          batch,
			reply:       []any{"REFUSED", "BOUND", "r", "c", "key", "owner"},
			wantText:    []string{`demo.r.c is bound to key, owned by "owner"`},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
			wantGuarded: true,
			check: func(t *testing.T, err error) {
				var be *BoundError
				require.ErrorAs(t, err, &be)
				assert.Equal(t, "demo", be.Table)
				assert.Equal(t, "r", be.Row)
				assert.Equal(t, "c", be.Col)
				assert.Equal(t, "key", be.Key)
				assert.Equal(t, "owner", be.Owner)
			},
		},
		{
			name:     "bound unguarded",
			op:       plain,
			reply:    []any{"REFUSED", "BOUND", "r", "c", "key", "owner"},
			wantText: []string{"owned elsewhere", "run: owner"},
			check: func(t *testing.T, err error) {
				var be *BoundError
				require.ErrorAs(t, err, &be)
				assert.Equal(t, "owner", be.Owner)
				assert.False(t, IsRefusal(err))
			},
		},
		{
			name:        "field guard equals",
			op:          batch,
			reply:       []any{"REFUSED", "FIELDGUARD", "m", "f", "equals", "a", "b"},
			wantIs:      ErrFieldGuard,
			wantText:    []string{`member "m" field "f": expected equals "a", observed "b"`},
			wantNext:    "nova-table member read 'demo' 'm'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "field guard absent",
			op:          batch,
			reply:       []any{"REFUSED", "FIELDGUARD", "m", "f", "absent", "x"},
			wantIs:      ErrFieldGuard,
			wantText:    []string{`member "m" field "f": expected absent, observed "x"`},
			wantNext:    "nova-table member read 'demo' 'm'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "field guard one of",
			op:          batch,
			reply:       []any{"REFUSED", "FIELDGUARD", "m", "f", "one_of", "[a b]", "c"},
			wantIs:      ErrFieldGuard,
			wantText:    []string{`member "m" field "f": expected one_of [a b], observed "c"`},
			wantNext:    "nova-table member read 'demo' 'm'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "field guard other",
			op:          batch,
			reply:       []any{"REFUSED", "FIELDGUARD", "m", "f", "guard must be object"},
			wantIs:      ErrFieldGuard,
			wantText:    []string{"failed field guard: m f guard must be object"},
			wantNext:    "nova-table member read 'demo' 'm'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "limit long",
			op:          batch,
			reply:       []any{"REFUSED", "LIMIT", "rows per table", "100000", "100001", "m", "at least"},
			wantIs:      ErrLimit,
			wantText:    []string{"rows per table: bound 100000, observed at least 100001"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
			wantGuarded: true,
			check: func(t *testing.T, err error) {
				var le *LimitError
				require.ErrorAs(t, err, &le)
				assert.Equal(t, "rows per table", le.Name)
				assert.Equal(t, 100000, le.Bound)
				assert.Equal(t, 100001, le.Observed)
				assert.Equal(t, "m", le.Member)
				assert.True(t, le.AtLeast)
			},
		},
		{
			name:        "limit short",
			op:          plain,
			reply:       []any{"REFUSED", "LIMIT", "rows per table"},
			wantIs:      ErrLimit,
			wantText:    []string{"limit exceeded: rows per table"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
		{
			name:        "no row on a batch",
			op:          batch,
			reply:       []any{"REFUSED", "NOROW", "r", "m"},
			wantText:    []string{"no such row"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "no row on a read set",
			op:          readSet,
			reply:       []any{"REFUSED", "NOROW", "r"},
			wantText:    []string{"no such row"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
			wantGuarded: true,
			check: func(t *testing.T, err error) {
				var r *Refusal
				require.ErrorAs(t, err, &r)
				assert.Contains(t, r.Location, "read set")
			},
		},
		{
			name:        "no row on a plain write",
			op:          plain,
			reply:       []any{"REFUSED", "NOROW", "r"},
			wantText:    []string{"no such row"},
			wantNext:    "nova-table row add 'demo' 'r'",
			wantRefusal: true,
		},
		{
			name:        "no column carries row and column",
			op:          plain,
			reply:       []any{"REFUSED", "NOCOL", "r", "c"},
			wantText:    []string{"no such column"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
			check: func(t *testing.T, err error) {
				var r *Refusal
				require.ErrorAs(t, err, &r)
				assert.Contains(t, r.Location, `row "r"`)
				assert.Contains(t, r.Location, `column "c"`)
			},
		},
		{
			name:        "wrong type guarded",
			op:          batch,
			reply:       []any{"REFUSED", "WRONGTYPE", "key", "string", "hash", "m"},
			wantIs:      ErrWrongType,
			wantText:    []string{"wrong type: key key is string, expected hash"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
			wantGuarded: true,
		},
		{
			name:        "wrong type unguarded",
			op:          plain,
			reply:       []any{"REFUSED", "WRONGTYPE", "key", "string", "hash"},
			wantIs:      ErrWrongType,
			wantText:    []string{"WRONGTYPE: key key is string, expected hash"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
		{
			name:        "wrong type short",
			op:          plain,
			reply:       []any{"REFUSED", "WRONGTYPE", "key"},
			wantIs:      ErrWrongType,
			wantText:    []string{"WRONGTYPE: [key]"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
		{
			name:        "unknown code",
			op:          plain,
			reply:       []any{"REFUSED", "WHATEVER", "a", "b"},
			wantText:    []string{"WHATEVER [a b]"},
			wantNext:    "nova-table show 'demo'",
			wantRefusal: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.op.refused(tc.reply)
			if tc.wantNil {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			if tc.wantIs != nil {
				assert.ErrorIs(t, err, tc.wantIs)
			}
			for _, s := range tc.wantText {
				assert.Contains(t, err.Error(), s)
			}
			var r *Refusal
			if tc.wantRefusal {
				require.ErrorAs(t, err, &r)
				assert.Equal(t, tc.wantGuarded, r.Guarded)
				if tc.wantNext != "" {
					assert.Equal(t, tc.wantNext, r.Next)
				}
				for _, s := range tc.wantNextHas {
					assert.Contains(t, r.Next, s)
				}
			} else {
				assert.False(t, IsRefusal(err), "want a plain error, got %v", err)
			}
			if tc.check != nil {
				tc.check(t, err)
			}
		})
	}
}

// TestNtableStoreCoverParseScoreText pins the nil, numeric and refused score
// text parse.
func TestNtableStoreCoverParseScoreText(t *testing.T) {
	t.Parallel()

	v, err := parseScoreText(nil)
	require.NoError(t, err)
	assert.Nil(t, v)

	good := "1.5"
	v, err = parseScoreText(&good)
	require.NoError(t, err)
	require.NotNil(t, v)
	assert.Equal(t, 1.5, *v)

	bad := "x"
	_, err = parseScoreText(&bad)
	require.Error(t, err)
	assert.ErrorContains(t, err, `score "x" is not a number`)
}

// TestNtableStoreCoverSet pins the Set checks and the spec keys it writes for
// renders, reorders and sorts, through coverCmdable with no socket.
func TestNtableStoreCoverSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c := newCoverCmdable()
	_, err := Set(ctx, c, "demo", SetOpts{RowMove: &Reorder{Item: "ok", Place: Place{Where: "before", Ref: "bad\nkey"}}})
	require.ErrorContains(t, err, "wants a non-empty")
	assert.Empty(t, c.calls)

	c = newCoverCmdable()
	_, err = Set(ctx, c, "demo", SetOpts{ColAdd: &Column{Name: "extra", Projection: Count, Fold: Sum}, ColAt: &Place{Where: "sideways"}})
	require.ErrorContains(t, err, "a place is")
	assert.Empty(t, c.calls)

	rec := newCoverArgCmdable()
	hidden := []string{"h1", "h2"}
	visible := true
	_, err = Set(ctx, rec, "demo", SetOpts{Hide: []string{"x"}, Show: []string{"y"}, Hidden: &hidden, Visible: &visible})
	require.NoError(t, err)
	require.Equal(t, []string{FnSet}, rec.calls)
	body := rec.args[FnSet][1].(string)
	var spec map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &spec))
	assert.Equal(t, "h1,h2", spec["hidden"])
	assert.Equal(t, []any{"x"}, spec["hide"])
	assert.Equal(t, []any{"y"}, spec["show"])
	assert.Equal(t, true, spec["visible"])

	rec = newCoverArgCmdable()
	_, err = Set(ctx, rec, "demo", SetOpts{
		ColMove: &Reorder{Item: "c", Place: Place{Where: "last"}},
		RowSort: &Sort{By: "name"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{FnSet}, rec.calls)
	body = rec.args[FnSet][1].(string)
	spec = map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(body), &spec))
	assert.Contains(t, spec, "col_move")
	assert.Contains(t, spec, "row_sort")
}

// TestNtableStoreCoverBind pins the Bind bounds and row checks and that a bound
// cell is carried into the Binds of the request.
func TestNtableStoreCoverBind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cols := coverColumns(t, "ready,working")

	big := newCoverCmdable()
	err := Bind(ctx, big, Table{Name: "demo", Columns: cols, Rows: make([]Row, LimitRows+1)}, time.Now())
	require.ErrorContains(t, err, "changed=no")
	require.ErrorContains(t, err, "rows per table")
	assert.Empty(t, big.calls)

	short := newCoverCmdable()
	err = Bind(ctx, short, Table{Name: "demo", Columns: cols, Rows: []Row{{Key: "r", Cells: []Cell{{}}}}}, time.Now())
	require.ErrorContains(t, err, "1 cells for 2 columns")
	assert.Empty(t, short.calls)

	rec := newCoverArgCmdable()
	tb := Table{Name: "demo", Columns: cols, Rows: []Row{{Key: "r", Cells: []Cell{{Key: "k1", Bound: true}, {}}}}}
	require.NoError(t, Bind(ctx, rec, tb, time.Now()))
	require.Equal(t, []string{FnBind}, rec.calls)
	body := rec.args[FnBind][1].(string)
	var req struct {
		Rows []struct {
			Key   string            `json:"key"`
			Binds map[string]string `json:"binds"`
		} `json:"rows"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	require.Len(t, req.Rows, 1)
	assert.Equal(t, "r", req.Rows[0].Key)
	assert.Equal(t, map[string]string{"ready": "k1"}, req.Rows[0].Binds)
}

// TestNtableStoreCoverBatchMemberDelta pins that a score that is not a number
// is refused on the before or the after side.
func TestNtableStoreCoverBatchMemberDelta(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"before", `{"id":"m","before_score":"x"}`},
		{"after", `{"id":"m","after_score":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var d BatchMemberDelta
			err := json.Unmarshal([]byte(tc.raw), &d)
			require.Error(t, err)
			assert.ErrorContains(t, err, "is not a number")
		})
	}
}
