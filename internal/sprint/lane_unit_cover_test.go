package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSprintLaneCoverValidLaneWho tests ValidLaneWho for valid and invalid names.
func TestSprintLaneCoverValidLaneWho(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		who  string
		want bool
	}{
		// Valid single IDs
		{name: "w1", who: "w1", want: true},
		{name: "lander/base", who: "lander/base", want: true},
		{name: "lander/v1-4", who: "lander/v1-4", want: true},
		// Invalid: empty
		{name: "empty", who: "", want: false},
		// Invalid: dots not allowed
		{name: "a.b", who: "a.b", want: false},
		// Invalid: starts with slash
		{name: "/x", who: "/x", want: false},
		// Invalid: ends with slash (empty second part)
		{name: "x/", who: "x/", want: false},
		// Invalid: three parts (only one slash allowed)
		{name: "a/b/c", who: "a/b/c", want: false},
		// Invalid: too long
		{name: "over 128 chars", who: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ValidLaneWho(tc.who))
		})
	}
}

// TestSprintLaneCoverLaneWidth tests LaneWidth parsing.
func TestSprintLaneCoverLaneWidth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		v    string
		set  bool
		want int
	}{
		// Valid numbers
		{name: "3", v: "3", set: true, want: 3},
		{name: "1", v: "1", set: true, want: 1},
		// Default cases
		{name: "0", v: "0", set: true, want: LaneWidthDefault},
		{name: "-2", v: "-2", set: true, want: LaneWidthDefault},
		{name: "x", v: "x", set: true, want: LaneWidthDefault},
		{name: "empty", v: "", set: true, want: LaneWidthDefault},
		{name: "unset", v: "3", set: false, want: LaneWidthDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, LaneWidth(tc.v, tc.set))
		})
	}
}

// TestSprintLaneCoverExpire tests Expire timeout handling.
func TestSprintLaneCoverExpire(t *testing.T) {
	t.Parallel()
	p0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	laneHoldFor := LaneHoldFor
	laneWaitFor := LaneWaitFor

	cases := []struct {
		name     string
		lanes    Lanes
		width    int
		now      time.Time
		want     Lanes
		released []string
	}{
		// Releasing holder not seen for more than LaneHoldFor
		{
			name: "holder released after LaneHoldFor",
			lanes: Lanes{
				"m1": {
					Holders: []LaneHold{
						{Who: "a", Since: p0, Seen: p0.Add(-laneHoldFor - time.Second)},
					},
				},
			},
			width:    1,
			now:      p0,
			want:     Lanes{},
			released: []string{"a"},
		},
		// Granting queue head when holder released
		{
			name: "grant queue head when holder released",
			lanes: Lanes{
				"m1": {
					Holders: []LaneHold{
						{Who: "a", Since: p0, Seen: p0.Add(-laneHoldFor - time.Second)},
					},
					Queue: []LaneWait{
						{Who: "b", Since: p0.Add(-time.Second), Seen: p0.Add(-time.Second)},
					},
				},
			},
			width: 1,
			now:   p0,
			want: Lanes{
				"m1": {
					Holders: []LaneHold{
						{Who: "b", Since: p0, Seen: p0.Add(-time.Second)},
					},
					Queue: []LaneWait{},
				},
			},
			released: []string{"a"},
		},
		// Releasing unclaimed grant older than LaneWaitFor
		{
			name: "unclaimed grant released after LaneWaitFor",
			lanes: Lanes{
				"m1": {
					Holders: []LaneHold{
						{Who: "a", Since: p0.Add(-laneWaitFor - time.Second), Seen: p0.Add(-laneWaitFor - time.Second), Claimed: false},
					},
				},
			},
			width:    1,
			now:      p0,
			want:     Lanes{},
			released: []string{"a"},
		},
		// Dropping waiter not seen for LaneWaitFor
		{
			name: "waiter dropped after LaneWaitFor",
			lanes: Lanes{
				"m1": {
					Queue: []LaneWait{
						{Who: "b", Since: p0.Add(-laneWaitFor - time.Second), Seen: p0.Add(-laneWaitFor - time.Second)},
					},
				},
			},
			width:    1,
			now:      p0,
			want:     Lanes{},
			released: []string{"b"},
		},
		// Dropping machine whose lane ends empty
		{
			name: "empty machine dropped",
			lanes: Lanes{
				"m1": {
					Holders: []LaneHold{
						{Who: "a", Since: p0, Seen: p0.Add(-laneHoldFor - time.Second)},
					},
				},
			},
			width:    1,
			now:      p0,
			want:     Lanes{},
			released: []string{"a"},
		},
		// Leaving input unchanged when no timeout triggers
		{
			name: "input unchanged",
			lanes: Lanes{
				"m1": {
					Holders: []LaneHold{
						{Who: "a", Since: p0, Seen: p0},
					},
					Queue: []LaneWait{
						{Who: "b", Since: p0, Seen: p0},
					},
				},
			},
			width: 1,
			now:   p0,
			want: Lanes{
				"m1": {
					Holders: []LaneHold{
						{Who: "a", Since: p0, Seen: p0},
					},
					Queue: []LaneWait{
						{Who: "b", Since: p0, Seen: p0},
					},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Make a deep copy to verify original is unchanged when expected
			orig := make(Lanes, len(tc.lanes))
			for k, v := range tc.lanes {
				orig[k] = v
			}

			out := tc.lanes.Expire(tc.width, tc.now)

			// Compare output
			assert.Equal(t, tc.want, out, "output lanes mismatch")

			// Check input unchanged when no expiration expected
			if len(tc.released) == 0 {
				assert.Equal(t, orig, tc.lanes, "input lanes should be unchanged")
			}
		})
	}
}

// TestSprintLaneCoverRows tests Rows output.
func TestSprintLaneCoverRows(t *testing.T) {
	t.Parallel()
	p0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		lanes Lanes
		kind  string
		width int
		now   time.Time
		want  []LaneRow
	}{
		// Sorted by machine
		{
			name: "sorted by machine",
			lanes: Lanes{
				"m2": {Holders: []LaneHold{{Who: "a", Since: p0, Seen: p0}}},
				"m1": {Holders: []LaneHold{{Who: "b", Since: p0, Seen: p0}}},
			},
			kind:  LaneGo,
			width: 1,
			now:   p0,
			want: []LaneRow{
				{Kind: LaneGo, Machine: "m1", Width: 1, Held: []string{"b"}, Waiting: []string{}, Since: p0},
				{Kind: LaneGo, Machine: "m2", Width: 1, Held: []string{"a"}, Waiting: []string{}, Since: p0},
			},
		},
		// Held and Waiting empty slices (not nil)
		{
			name:  "empty slices not nil",
			lanes: Lanes{},
			kind:  LaneGo,
			width: 1,
			now:   p0,
			want:  []LaneRow{},
		},
		// Since is the first holder's Since
		{
			name: "since first holder",
			lanes: Lanes{
				"m1": {
					Holders: []LaneHold{
						{Who: "a", Since: p0, Seen: p0},
						{Who: "b", Since: p0.Add(time.Second), Seen: p0.Add(time.Second)},
					},
				},
			},
			kind:  LaneGo,
			width: 2,
			now:   p0,
			want: []LaneRow{
				{Kind: LaneGo, Machine: "m1", Width: 2, Held: []string{"a", "b"}, Waiting: []string{}, Since: p0},
			},
		},
		// Width 0 grants nothing (waiter stays in queue)
		{
			name: "width zero grants nothing",
			lanes: Lanes{
				"m1": {
					Queue: []LaneWait{
						{Who: "a", Since: p0, Seen: p0},
					},
				},
			},
			kind:  LaneGo,
			width: 0,
			now:   p0,
			want: []LaneRow{
				{Kind: LaneGo, Machine: "m1", Width: 0, Held: []string{}, Waiting: []string{"a"}},
			},
		},
		// Kind and Width carried
		{
			name: "kind and width carried",
			lanes: Lanes{
				"m1": {Holders: []LaneHold{{Who: "a", Since: p0, Seen: p0}}},
			},
			kind:  "custom",
			width: 5,
			now:   p0,
			want: []LaneRow{
				{Kind: "custom", Machine: "m1", Width: 5, Held: []string{"a"}, Waiting: []string{}, Since: p0},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := tc.lanes.Rows(tc.kind, tc.width, tc.now)
			assert.Equal(t, tc.want, out, "rows mismatch")
		})
	}
}
