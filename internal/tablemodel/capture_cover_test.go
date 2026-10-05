package tablemodel

import (
	"context"
	"maps"
	"testing"

	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCaptureCoverFcallNameFormatsTheVerbAsNsTableVerb tests fcallName.
func TestCaptureCoverFcallNameFormatsTheVerbAsNsTableVerb(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		verb string
		want string
	}{
		{"create verb", "create", "ns_table_create"},
		{"drop verb", "drop", "ns_table_drop"},
		{"cell_add verb", "cell_add", "ns_table_cell_add"},
		{"cell_remove verb", "cell_remove", "ns_table_cell_remove"},
		{"cell_move verb", "cell_move", "ns_table_cell_move"},
		{"row_add verb", "row_add", "ns_table_row_add"},
		{"row_del verb", "row_del", "ns_table_row_del"},
		{"bind verb", "bind", "ns_table_bind"},
		{"clear verb", "clear", "ns_table_clear"},
		{"advance verb", "advance", "ns_table_advance"},
		{"read_epoch verb", "read_epoch", "ns_table_read_epoch"},
		{"empty verb", "", "ns_table_"},
		{"verb with underscore", "custom_verb", "ns_table_custom_verb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := fcallName(tc.verb)
			tassert.Equal(t, tc.want, got, "fcallName(%q) = %q, want %q", tc.verb, got, tc.want)
		})
	}
}

// TestCaptureCoverPrefixFormatsTableAndEpoch tests prefix.
func TestCaptureCoverPrefixFormatsTableAndEpoch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		table string
		epoch int
		want  string
	}{
		{"t1 epoch 1", "t1", 1, "table:t1:1"},
		{"t2 epoch 2", "t2", 2, "table:t2:2"},
		{"table with underscore epoch 10", "my_table", 10, "table:my_table:10"},
		{"epoch zero", "t1", 0, "table:t1:0"},
		{"negative epoch", "t1", -1, "table:t1:-1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := prefix(tc.table, tc.epoch)
			tassert.Equal(t, tc.want, got, "prefix(%q, %d) = %q, want %q", tc.table, tc.epoch, got, tc.want)
		})
	}
}

// TestCaptureCoverOptionsBuildsTheOptionMap tests options.
func TestCaptureCoverOptionsBuildsTheOptionMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		epoch int
		actor string
		want  callOptions
	}{
		{"epoch 1 actor w1", 1, "w1", callOptions{"epoch": "1", "actor": "w1", "fence": "fixture-fence", "idem": "fixture-attempt"}},
		{"epoch 2 actor w2", 2, "w2", callOptions{"epoch": "2", "actor": "w2", "fence": "fixture-fence", "idem": "fixture-attempt"}},
		{"epoch 0 actor seed", 0, "seed", callOptions{"epoch": "0", "actor": "seed", "fence": "fixture-fence", "idem": "fixture-attempt"}},
		{"negative epoch", -1, "test", callOptions{"epoch": "-1", "actor": "test", "fence": "fixture-fence", "idem": "fixture-attempt"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := options(tc.epoch, tc.actor)
			require.Equal(t, tc.want["epoch"], got["epoch"])
			require.Equal(t, tc.want["actor"], got["actor"])
			require.Equal(t, tc.want["fence"], got["fence"])
			require.Equal(t, tc.want["idem"], got["idem"])
			require.Len(t, got, 4)
		})
	}
}

// TestCaptureCoverCallRefusesWhenContextIsDone tests call with cancelled context.
func TestCaptureCoverCallRefusesWhenContextIsDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Store{ctx: ctx}
	args := []string{"t1", "r1", "c1", "1", "m1"}
	opts := options(1, "w1")
	err := guard(func() { call(r, "cell_add", args, opts) })
	tassert.Error(t, err, "call should refuse on cancelled context")
	tassert.ErrorContains(t, err, "ran out of time", "error should mention ran out of time")
}

// TestCaptureCoverCallOKAssertsExpectedReply tests callOK main path and refusal.
func TestCaptureCoverCallOKAssertsExpectedReply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		reply  []any
		want   string
		what   string
		refuse bool
	}{
		{"OK reply matches", []any{"OK"}, "OK", "test create", false},
		{"ROW reply matches", []any{"ROW"}, "ROW", "test row_add", false},
		{"REFUSED reply matches", []any{"REFUSED", "BOUND"}, "REFUSED", "test refused", false},
		{"mismatch refuses", []any{"OK"}, "ROW", "test mismatch", true},
		{"empty reply refuses", []any{}, "OK", "test empty", true},
		{"nil reply refuses", nil, "OK", "test nil", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := guard(func() { callOK(tc.reply, tc.want, tc.what) })
			if tc.refuse {
				tassert.Error(t, err, "callOK should have failed for %s", tc.name)
				tassert.ErrorContains(t, err, tc.what, "error should mention %s", tc.what)
			} else {
				tassert.NoError(t, err, "callOK failed unexpectedly for %s: %v", tc.name, err)
			}
		})
	}
}

// TestCaptureCoverScoresOfConvertsPairs tests scoresOf.
func TestCaptureCoverScoresOfConvertsPairs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  any
		want   []pair
		refuse bool
	}{
		{"valid flat list", []any{"k1", "v1", "k2", "v2"}, []pair{{"k1", "v1"}, {"k2", "v2"}}, false},
		{"empty list", []any{}, []pair{}, false},
		{"single pair", []any{"key", "value"}, []pair{{"key", "value"}}, false},
		{"odd elements refuses", []any{"k1", "v1", "k2"}, nil, true},
		{"nil input refuses", nil, nil, true},
		{"non-list input refuses", "not a list", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := guard(func() {
				got := scoresOf(tc.input)
				if !tc.refuse {
					tassert.Equal(t, tc.want, got, "scoresOf(%v) = %v", tc.input, got)
				}
			})
			if tc.refuse {
				tassert.Error(t, err, "scoresOf should refuse for %s", tc.name)
			} else {
				tassert.NoError(t, err, "scoresOf failed unexpectedly for %s: %v", tc.name, err)
			}
		})
	}
}

// TestCaptureCoverMkCellBuildsCell tests mkCell.
func TestCaptureCoverMkCellBuildsCell(t *testing.T) {
	t.Parallel()
	c := mkCell("t1", 2, "r1", "c3")
	tassert.Equal(t, "t1", c.P.Table)
	tassert.Equal(t, 2, c.P.Epoch)
	tassert.Equal(t, "r1", c.Row)
	tassert.Equal(t, "c3", c.Col)

	c2 := mkCell("t2", 1, "r2", "c1")
	tassert.Equal(t, "t2", c2.P.Table)
	tassert.Equal(t, 1, c2.P.Epoch)
	tassert.Equal(t, "r2", c2.Row)
	tassert.Equal(t, "c1", c2.Col)
}

// TestCaptureCoverAtoiParsesInteger tests atoi.
func TestCaptureCoverAtoiParsesInteger(t *testing.T) {
	t.Parallel()
	tassert.Equal(t, 42, atoi("42"))
	tassert.Equal(t, 0, atoi("0"))
	tassert.Equal(t, -5, atoi("-5"))

	err := guard(func() { atoi("not-a-number") })
	tassert.Error(t, err, "atoi should refuse non-integer")
	tassert.ErrorContains(t, err, "not an integer", "error should mention not an integer")
}

// TestCaptureCoverEndpointFormatsCell tests endpoint.
func TestCaptureCoverEndpointFormatsCell(t *testing.T) {
	t.Parallel()
	c := Cell{P: Phys{"t1", 1}, Row: "r1", Col: "c2"}
	tassert.Equal(t, "r1:c2", endpoint(c))

	tassert.Equal(t, "", endpoint(NoPlace))
	// External is a Cell with Row="external", Col="external", so endpoint returns "external:external"
	tassert.Equal(t, "external:external", endpoint(External))
}

// TestCaptureCoverCloneEventCopiesEvent tests Event.clone.
func TestCaptureCoverCloneEventCopiesEvent(t *testing.T) {
	t.Parallel()
	e := Event{"verb": "cell_add", "args": `["t1", "r1", "c1", "1", "m1"]`, "epoch": "1"}
	c := e.clone()

	require.Equal(t, e, c, "clone should have same content")

	c["verb"] = "cell_remove"
	tassert.NotEqual(t, e["verb"], c["verb"], "modifying clone should not affect original")
}

// TestCaptureCoverJsonListParsesJSONList tests jsonList.
func TestCaptureCoverJsonListParsesJSONList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		want   []any
		refuse bool
	}{
		{"valid array", `[1, "two", true]`, []any{float64(1), "two", true}, false},
		{"empty array", `[]`, []any{}, false},
		{"nested array", `[[1,2], [3,4]]`, []any{[]any{float64(1), float64(2)}, []any{float64(3), float64(4)}}, false},
		{"object not array refuses", `{"key": "value"}`, nil, true},
		{"string not array refuses", `"not an array"`, nil, true},
		{"invalid JSON refuses", `[1, 2,`, nil, true},
		{"number not array refuses", `42`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := guard(func() {
				got := jsonList(tc.input)
				if !tc.refuse {
					tassert.Equal(t, tc.want, got, "jsonList(%q) = %v", tc.input, got)
				}
			})
			if tc.refuse {
				tassert.Error(t, err, "jsonList should refuse for %s", tc.name)
				tassert.ErrorContains(t, err, "not a JSON list", "error should mention not a JSON list")
			} else {
				tassert.NoError(t, err, "jsonList failed unexpectedly for %s: %v", tc.name, err)
			}
		})
	}
}

// TestCaptureCoverJsonStringsParsesJSONStringArray tests jsonStrings.
func TestCaptureCoverJsonStringsParsesJSONStringArray(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		want   []string
		refuse bool
	}{
		{"valid string array", `["a", "b", "c"]`, []string{"a", "b", "c"}, false},
		{"empty array", `[]`, []string{}, false},
		{"single element", `["only"]`, []string{"only"}, false},
		{"non-string element refuses", `["a", 1, "c"]`, nil, true},
		{"object not array refuses", `{"key": "value"}`, nil, true},
		{"string not array refuses", `"not an array"`, nil, true},
		{"invalid JSON refuses", `["a", "b"`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := guard(func() {
				got := jsonStrings(tc.input)
				if !tc.refuse {
					tassert.Equal(t, tc.want, got, "jsonStrings(%q) = %v", tc.input, got)
				}
			})
			if tc.refuse {
				tassert.Error(t, err, "jsonStrings should refuse for %s", tc.name)
				tassert.ErrorContains(t, err, "not a JSON list of strings", "error should mention not a JSON list of strings")
			} else {
				tassert.NoError(t, err, "jsonStrings failed unexpectedly for %s: %v", tc.name, err)
			}
		})
	}
}

// TestCaptureCoverJsonStrings2ParsesJSONStringArray tests jsonStrings2.
func TestCaptureCoverJsonStrings2ParsesJSONStringArray(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		want   []string
		refuse bool
	}{
		{"valid string array", `["r1:c1", "r1:c2"]`, []string{"r1:c1", "r1:c2"}, false},
		{"empty array", `[]`, []string{}, false},
		{"single element", `["only"]`, []string{"only"}, false},
		{"non-string element refuses", `["a", 1]`, nil, true},
		{"object not array refuses", `{"key": "value"}`, nil, true},
		{"invalid JSON refuses", `["a", "b"`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := guard(func() {
				got := jsonStrings2(tc.input)
				if !tc.refuse {
					tassert.Equal(t, tc.want, got, "jsonStrings2(%q) = %v", tc.input, got)
				}
			})
			if tc.refuse {
				tassert.Error(t, err, "jsonStrings2 should refuse for %s", tc.name)
				tassert.ErrorContains(t, err, "not a list of strings", "error should mention not a list of strings")
			} else {
				tassert.NoError(t, err, "jsonStrings2 failed unexpectedly for %s: %v", tc.name, err)
			}
		})
	}
}

// TestCaptureCoverRevisionRefusesWhenContextIsDone tests revision with cancelled context.
func TestCaptureCoverRevisionRefusesWhenContextIsDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Store{ctx: ctx}
	err := guard(func() { revision(r, "t1") })
	tassert.Error(t, err, "revision should refuse on cancelled context")
	tassert.ErrorContains(t, err, "ran out of time", "error should mention ran out of time")
}

// TestCaptureCoverReceiptRefusesWhenContextIsDone tests receipt with cancelled context.
func TestCaptureCoverReceiptRefusesWhenContextIsDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Store{ctx: ctx}
	opts := options(1, "w1")
	args := []string{"t1", "r1", "c1", "1", "m1"}
	reply := []any{"OK"}
	err := guard(func() { receipt(r, "cell_add", args, opts, reply, 0) })
	tassert.Error(t, err, "receipt should refuse on cancelled context")
	tassert.ErrorContains(t, err, "ran out of time", "error should mention ran out of time")
}

// TestCaptureCoverExecuteRefusesEveryArmWhenTheStoreIsDone enters each arm of
// execute — read_epoch, advance and a table verb — through the
// cancelled-context seam the package's own Store checks before any command:
// every arm reads the store first, so each refuses with the store's failure,
// returns nothing and leaves the writer's seen epoch untouched. The main
// paths (a recorded seen epoch, an incremented store epoch, a called verb
// with its receipt) run against the live store in the functional replay.
func TestCaptureCoverExecuteRefusesEveryArmWhenTheStoreIsDone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		action Action
		seen   map[string]int
	}{
		{"read_epoch arm", Action{Verb: "read_epoch", Actor: "w1"}, map[string]int{}},
		{"advance arm", Action{Verb: "advance", Actor: "w1"}, map[string]int{"w1": 1}},
		{"table verb arm", Action{Verb: "cell_add", Args: []string{"t1", "r1", "c1", "1", "m1"}, Actor: "w1"}, map[string]int{"w1": 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r := &Store{ctx: ctx}
			seen := maps.Clone(tc.seen)
			var refused *bool
			var event Event
			err := guard(func() { refused, event = execute(r, tc.action, seen, nil) })
			var f *Failure
			require.ErrorAs(t, err, &f, "error = %v, want a failed check, not a hang or a raw panic", err)
			require.ErrorContains(t, err, "ran out of time", "error = %v, want the store's done context named", err)
			require.Equal(t, tc.seen, seen, "seen = %v, want it untouched: the arm reads the store before it records", seen)
			require.Nil(t, refused, "a refused execute returned a refusal flag")
			require.Nil(t, event, "a refused execute returned an event")
		})
	}
}

// TestCaptureCoverContinuityRefusesGap tests continuity.
func TestCaptureCoverContinuityRefusesGap(t *testing.T) {
	t.Parallel()
	e := Event{"rev_before": "4", "rev_after": "5"}
	err := continuity(e, 4)
	tassert.NoError(t, err, "continuity should pass for matching revisions")

	for _, before := range []int{3, 5} {
		err := continuity(e, before)
		tassert.Error(t, err, "continuity should fail for store at %d", before)
		tassert.True(t, len(err.Error()) > 0 && err.Error()[:4] == "GAP:", "error should start with GAP:")
	}

	err = continuity(Event{"rev_before": "4", "rev_after": "6"}, 4)
	tassert.Error(t, err, "continuity should fail for skipped revision")
}

// TestCaptureCoverSeedRefusesWhenTheStoreIsDone enters seed through the
// cancelled-context seam: the epoch HSET is its first store command, so the
// whole seeding refuses with the store's failure instead of half-seeding.
// The seeded store itself (two tables, their rows, the member epochs and the
// external set) is checked by the functional capture against a live store.
func TestCaptureCoverSeedRefusesWhenTheStoreIsDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Store{ctx: ctx}
	err := guard(func() { seed(r) })
	var f *Failure
	require.ErrorAs(t, err, &f, "error = %v, want a failed check, not a hang or a raw panic", err)
	require.ErrorContains(t, err, "ran out of time", "error = %v, want the store's done context named", err)
}

// TestCaptureCoverSnapshotRefusesWhenTheStoreIsDone enters snapshot through
// the cancelled-context seam: the active epoch is its first store read, so
// snapshot refuses with the store's failure and hands back no state. The
// read-back of a seeded store into the model's vocabulary is checked by the
// functional capture against a live store.
func TestCaptureCoverSnapshotRefusesWhenTheStoreIsDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Store{ctx: ctx}
	var s State
	err := guard(func() { s = snapshot(r, map[string]int{"w1": 1}) })
	var f *Failure
	require.ErrorAs(t, err, &f, "error = %v, want a failed check, not a hang or a raw panic", err)
	require.ErrorContains(t, err, "ran out of time", "error = %v, want the store's done context named", err)
	require.Equal(t, State{}, s, "a refused snapshot returned a state")
}
