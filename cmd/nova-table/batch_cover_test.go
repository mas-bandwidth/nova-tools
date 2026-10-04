package main

import (
	"encoding/json"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sptr is the pointer a field side needs: a present value is *string, absence is
// nil, so a test's literal carries the address of its own string.
//
//go:fix inline
func sptr(s string) *string { return new(s) }

// batchJSON renders the applied batch as one object: revisions, scores and the
// epoch are decimal strings, an absent place or score is null, fields are
// [before, after] pairs. The main path moves a member's place and score and drops
// a field whose two sides are equal; the refusal leaves a guard unplaced and
// unscored, so its place and score are null and its fields are {}.
func TestBatchCoverBatchJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		table string
		r     ntable.Receipt
		delta ntable.BatchDelta
		trips int64
		want  string
	}{
		{
			name:  "main path: a member moves place and score, an equal field drops",
			table: "demo",
			r:     ntable.Receipt{ID: "1-0", Epoch: 3, Before: 2, After: 3, Outcome: "applied"},
			delta: ntable.BatchDelta{
				OperationID: "op-1", SelectedCount: 1, ChangedCount: 1,
				Members: []ntable.BatchMemberDelta{{
					ID: "b1", BeforePlace: "", AfterPlace: "build.ready",
					BeforeScoreText: new("0"), AfterScoreText: new("5"),
					BeforeRev: "1", AfterRev: "2",
					Fields: map[string]ntable.FieldChange{
						"role": {Before: new("x"), After: new("y")},
						"kept": {Before: new("k"), After: new("k")},
					},
				}},
			},
			trips: 1,
			want:  `{"table":"demo","operation_id":"op-1","epoch":"3","table_revision":{"before":"2","after":"3"},"outcome":"applied","selected":1,"guards":0,"changed":1,"event":"1-0","replay":false,"trips":1,"members":[{"id":"b1","place":{"before":null,"after":"build.ready"},"score":{"before":"0","after":"5"},"member_revision":{"before":"1","after":"2"},"fields":{"role":["x","y"]}}]}`,
		},
		{
			name:  "refusal: a guard left in place is null score and empty fields",
			table: "demo",
			r:     ntable.Receipt{ID: "2-0", Epoch: 0, Before: 0, After: 0, Outcome: "applied", Replay: true},
			delta: ntable.BatchDelta{
				OperationID: "op-2", SelectedCount: 1, GuardCount: 1,
				Members: []ntable.BatchMemberDelta{{
					ID: "g1", BeforePlace: "build.ready", AfterPlace: "build.ready",
					BeforeRev: "4", AfterRev: "4",
				}},
			},
			trips: 2,
			want:  `{"table":"demo","operation_id":"op-2","epoch":"0","table_revision":{"before":"0","after":"0"},"outcome":"applied","selected":1,"guards":1,"changed":0,"event":"2-0","replay":true,"trips":2,"members":[{"id":"g1","place":{"before":"build.ready","after":"build.ready"},"score":{"before":null,"after":null},"member_revision":{"before":"4","after":"4"},"fields":{}}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(batchJSON(tc.table, tc.r, tc.delta, tc.trips))
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

// placeOrDash prints a place, or - for none.
func TestBatchCoverPlaceOrDash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, want string
	}{
		{"main path: a place is its own words", "build.ready", "build.ready"},
		{"refusal: no place prints a dash", "", "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, placeOrDash(tc.in))
		})
	}
}

// scoreOrDash prints the score exactly as the store holds it, or - for none.
func TestBatchCoverScoreOrDash(t *testing.T) {
	t.Parallel()
	five := "5"
	for _, tc := range []struct {
		name string
		in   *string
		want string
	}{
		{"main path: a score is its own text", &five, "5"},
		{"main path: a zero score is a score, not a dash", new("0"), "0"},
		{"refusal: no score prints a dash", nil, "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, scoreOrDash(tc.in))
		})
	}
}

// fieldChanges is changedFields as one JSON object on one line: a set field is a
// [before, after] pair, a set-only side pairs null with its value, and a member
// that changed nothing renders the empty object.
func TestBatchCoverFieldChanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		m    ntable.BatchMemberDelta
		want string
	}{
		{
			name: "main path: a changed field is a pair and an equal one drops",
			m: ntable.BatchMemberDelta{Fields: map[string]ntable.FieldChange{
				"role": {Before: new("x"), After: new("y")},
				"kept": {Before: new("k"), After: new("k")},
			}},
			want: `{"role":["x","y"]}`,
		},
		{
			name: "main path: a set-only field pairs null with its value",
			m:    ntable.BatchMemberDelta{Fields: map[string]ntable.FieldChange{"k": {After: new("v")}}},
			want: `{"k":[null,"v"]}`,
		},
		{
			name: "refusal: nothing changed is the empty object",
			m:    ntable.BatchMemberDelta{Fields: map[string]ntable.FieldChange{"kept": {Before: new("k"), After: new("k")}}},
			want: `{}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, fieldChanges(tc.m))
		})
	}
}
