package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemberCoverCellListSet: the repeatable --cell flag appends every value
// it is given and never errors, and its text joins the places with commas.
func TestMemberCoverCellListSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		values []string
		want   []string
		joined string
	}{
		{
			name:   "one value appends",
			values: []string{"build:ready"},
			want:   []string{"build:ready"},
			joined: "build:ready",
		},
		{
			name:   "repeatable accumulates in order",
			values: []string{"build:ready", "review:ship"},
			want:   []string{"build:ready", "review:ship"},
			joined: "build:ready,review:ship",
		},
		{
			name:   "empty value kept as given",
			values: []string{""},
			want:   []string{""},
			joined: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var cells cellList
			for _, v := range tc.values {
				require.NoError(t, cells.Set(v), "Set never refuses a flag value")
			}
			assert.Equal(t, tc.want, []string(cells), "places kept in flag order")
			assert.Equal(t, tc.joined, cells.String(), "the list's text joins with commas")
		})
	}
}

// TestMemberCoverSplitCell: a place row:column splits at the last colon, so a
// row may itself hold colons; a missing, leading or trailing colon refuses.
func TestMemberCoverSplitCell(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		wantRow string
		wantCol string
		wantOK  bool
	}{
		{name: "row and column", in: "build:ready", wantRow: "build", wantCol: "ready", wantOK: true},
		{name: "row with colons keeps the row whole", in: "a:b:c", wantRow: "a:b", wantCol: "c", wantOK: true},
		{name: "no colon refuses", in: "build", wantOK: false},
		{name: "leading colon refuses", in: ":ready", wantOK: false},
		{name: "trailing colon refuses", in: "build:", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row, col, ok := splitCell(tc.in)
			assert.Equal(t, tc.wantOK, ok, "ok")
			assert.Equal(t, tc.wantRow, row, "row")
			assert.Equal(t, tc.wantCol, col, "col")
		})
	}
}

// TestMemberCoverPrintJSON: a reading renders as one line of JSON and exits 0;
// a value JSON cannot hold refuses with the verb named, exit 2, and prints
// nothing on stdout.
func TestMemberCoverPrintJSON(t *testing.T) {
	t.Parallel()

	t.Run("one line of json exits 0", func(t *testing.T) {
		t.Parallel()

		var stdout, stderr bytes.Buffer
		code := printJSON(&stdout, &stderr, "member read", map[string]any{"table": "demo"})
		assert.Equal(t, 0, code)
		assert.Empty(t, stderr.String())
		assert.Equal(t, 1, strings.Count(stdout.String(), "\n"), "one line")
		var got map[string]any
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
		assert.Equal(t, map[string]any{"table": "demo"}, got)
	})

	t.Run("unrenderable value refuses with exit 2", func(t *testing.T) {
		t.Parallel()

		var stdout, stderr bytes.Buffer
		code := printJSON(&stdout, &stderr, "member read", map[string]any{"bad": make(chan int)})
		assert.Equal(t, 2, code)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), "MEMBER-READ REFUSED")
		assert.Contains(t, stderr.String(), "cannot render the reading as JSON")
	})
}

// TestMemberCoverReadJSON: a reading renders as the map member read --json
// prints: a placed member carries its place and score, an unplaced one nil,
// nil fields and a nil missing list render as empty containers, epochs and
// revisions are strings, trips pass through, and the whole value marshals.
func TestMemberCoverReadJSON(t *testing.T) {
	t.Parallel()

	res := ntable.ReadSetResult{
		Table: "demo", Epoch: 3, Revision: 9,
		Members: []ntable.ReadSetMember{
			{ID: "m1", Revision: 12, Placed: true, Row: "build", Col: "ready", ScoreText: "0.50", Fields: map[string]string{"status": "ok"}},
			{ID: "m2", Revision: 7},
		},
		Missing: []string{"m3"},
	}
	got := readJSON(res, 4)

	assert.Equal(t, "demo", got["table"])
	assert.Equal(t, "3", got["epoch"])
	assert.Equal(t, "9", got["table_revision"])
	assert.Equal(t, int64(4), got["trips"])
	assert.Equal(t, []string{"m3"}, got["missing"])

	members, ok := got["members"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, members, 2)
	assert.Equal(t, map[string]any{
		"id": "m1", "place": "build:ready", "score": "0.50",
		"member_revision": "12", "fields": map[string]string{"status": "ok"},
	}, members[0], "a placed member carries place, score and fields")
	assert.Equal(t, map[string]any{
		"id": "m2", "place": nil, "score": nil,
		"member_revision": "7", "fields": map[string]string{},
	}, members[1], "an unplaced member carries nil place and empty fields")

	t.Run("nil slices render empty and the whole value marshals", func(t *testing.T) {
		t.Parallel()

		empty := readJSON(ntable.ReadSetResult{Table: "demo"}, 1)
		assert.Equal(t, []map[string]any{}, empty["members"])
		assert.Equal(t, []string{}, empty["missing"])
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 0, printJSON(&stdout, &stderr, "member read", empty))
		var round map[string]any
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &round))
		assert.Equal(t, float64(1), round["trips"])
	})
}
