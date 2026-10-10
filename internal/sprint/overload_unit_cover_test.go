package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSprintOverloadCoverTimeoutKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, report, want string
	}{
		{"staging refused", "staging refused: stage-timeout", TimeoutStaging},
		{"bare stage-timeout", "stage-timeout", TimeoutStaging},
		{"deadline", "deadline: killed", TimeoutDeadline},
		{"usage source", "budget: unverifiable: the usage source stopped answering, x", TimeoutUsage},
		{"no colon", "deadline", ""},
		{"ordinary failure", "tests red", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, TimeoutKind(c.report))
		})
	}
}

func TestSprintOverloadCoverMemberTimeouts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-OverloadWindow)

	t.Run("nil Fleet", func(t *testing.T) {
		t.Parallel()
		s := &Snapshot{Now: now, Fleet: nil}
		assert.Nil(t, MemberTimeouts(s, "m1"))
	})

	t.Run("done failed card inside window", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{"m1"})
		fleet.Put(&Card{ID: "c1", Row: "m1", Col: DoneFailed, Fields: map[string]string{
			"report":   "deadline: killed",
			"finished": windowStart.Add(5 * time.Minute).Format(time.RFC3339),
		}})
		s := &Snapshot{Now: now, Fleet: fleet}
		ts := MemberTimeouts(s, "m1")
		require.Len(t, ts, 1)
		assert.Equal(t, "c1", ts[0].Card)
		assert.Equal(t, TimeoutDeadline, ts[0].Kind)
	})

	t.Run("finished more than OverloadWindow before Now", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{"m1"})
		fleet.Put(&Card{ID: "c2", Row: "m1", Col: DoneFailed, Fields: map[string]string{
			"report":   "deadline: killed",
			"finished": windowStart.Add(-1 * time.Minute).Format(time.RFC3339),
		}})
		s := &Snapshot{Now: now, Fleet: fleet}
		ts := MemberTimeouts(s, "m1")
		assert.Len(t, ts, 0)
	})

	t.Run("finished after Now", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{"m1"})
		fleet.Put(&Card{ID: "c3", Row: "m1", Col: DoneFailed, Fields: map[string]string{
			"report":   "deadline: killed",
			"finished": now.Add(1 * time.Minute).Format(time.RFC3339),
		}})
		s := &Snapshot{Now: now, Fleet: fleet}
		ts := MemberTimeouts(s, "m1")
		assert.Len(t, ts, 0)
	})

	t.Run("unparsable timestamp", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{"m1"})
		fleet.Put(&Card{ID: "c4", Row: "m1", Col: DoneFailed, Fields: map[string]string{
			"report":   "deadline: killed",
			"finished": "not-a-timestamp",
		}})
		s := &Snapshot{Now: now, Fleet: fleet}
		ts := MemberTimeouts(s, "m1")
		assert.Len(t, ts, 0)
	})

	t.Run("same card on another member's row", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{"m1", "m2"})
		fleet.Put(&Card{ID: "c5", Row: "m2", Col: DoneFailed, Fields: map[string]string{
			"report":   "deadline: killed",
			"finished": windowStart.Add(5 * time.Minute).Format(time.RFC3339),
		}})
		s := &Snapshot{Now: now, Fleet: fleet}
		ts := MemberTimeouts(s, "m1")
		assert.Len(t, ts, 0)
	})

	t.Run("two cards at one instant ordered by card id", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{"m1"})
		fleet.Put(&Card{ID: "c-b", Row: "m1", Col: DoneFailed, Fields: map[string]string{
			"report":   "deadline: killed",
			"finished": windowStart.Add(5 * time.Minute).Format(time.RFC3339),
		}})
		fleet.Put(&Card{ID: "c-a", Row: "m1", Col: DoneFailed, Fields: map[string]string{
			"report":   "deadline: killed",
			"finished": windowStart.Add(5 * time.Minute).Format(time.RFC3339),
		}})
		s := &Snapshot{Now: now, Fleet: fleet}
		ts := MemberTimeouts(s, "m1")
		require.Len(t, ts, 2)
		assert.Equal(t, "c-a", ts[0].Card)
		assert.Equal(t, "c-b", ts[1].Card)
	})

	t.Run("staging refusal via StagingTakes", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{"m1"})
		stakeTake := "r\tmodel\tm1\t" + windowStart.Add(5*time.Minute).Format(time.RFC3339) + "\t\tstage-timeout\t"
		fleet.Put(&Card{ID: "wc1", Row: "m1", Col: Working, Fields: map[string]string{
			"gen":            "1",
			"staging_take_1": stakeTake,
		}})
		s := &Snapshot{Now: now, Fleet: fleet}
		ts := MemberTimeouts(s, "m1")
		require.Len(t, ts, 1)
		assert.Equal(t, "wc1", ts[0].Card)
		assert.Equal(t, TimeoutStaging, ts[0].Kind)
	})
}

func TestSprintOverloadCoverOverloaded(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-OverloadWindow)
	member := "m1"

	t.Run("OverloadTimeouts-1 timeouts returns false", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{member})
		// Put OverloadTimeouts-1 cards
		for i := 0; i < OverloadTimeouts-1; i++ {
			id := "c" + string(rune('0'+i))
			fleet.Put(&Card{ID: id, Row: member, Col: DoneFailed, Fields: map[string]string{
				"report":   "deadline: killed",
				"finished": windowStart.Add(time.Duration(1+i) * time.Minute).Format(time.RFC3339),
			}})
		}
		s := &Snapshot{Now: now, Fleet: fleet}
		ol, ok := Overloaded(s, member)
		assert.False(t, ok)
		assert.Zero(t, ol.Member)
	})

	t.Run("OverloadTimeouts returns true", func(t *testing.T) {
		t.Parallel()
		fleet := NewTable("Fleet")
		fleet.SetRows([]string{member})
		fleet.Put(&Card{ID: CtlID(member), Row: member, Col: Ctl, Fields: map[string]string{FieldWidth: "8"}})
		// Put OverloadTimeouts cards
		for i := 0; i < OverloadTimeouts; i++ {
			id := "c" + string(rune('0'+i))
			fleet.Put(&Card{ID: id, Row: member, Col: DoneFailed, Fields: map[string]string{
				"report":   "deadline: killed",
				"finished": windowStart.Add(time.Duration(1+i) * time.Minute).Format(time.RFC3339),
			}})
		}
		s := &Snapshot{Now: now, Fleet: fleet}
		ol, ok := Overloaded(s, member)
		assert.True(t, ok)
		assert.Equal(t, member, ol.Member)
		assert.Len(t, ol.Timeouts, OverloadTimeouts)
		assert.Equal(t, 8, ol.Width)
	})
}

func TestSprintOverloadCoverHalfWidth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		width, want int
	}{
		{8, 4},
		{3, 1},
		{1, 1},
		{0, 1},
	}
	for _, c := range cases {
		t.Run(string(rune('0'+c.width)), func(t *testing.T) {
			t.Parallel()
			ol := Overload{Width: c.width}
			assert.Equal(t, c.want, ol.HalfWidth())
		})
	}
}

func TestSprintOverloadCoverWhat(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-OverloadWindow)
	member := "m1"
	width := 8
	ol := Overload{
		Member: member,
		Timeouts: []Timeout{
			{Card: "c1", Kind: TimeoutDeadline, At: windowStart.Add(5 * time.Minute)},
			{Card: "c2", Kind: TimeoutStaging, At: windowStart.Add(10 * time.Minute)},
		},
		Width: width,
	}
	got := ol.What()
	assert.Contains(t, got, member)
	assert.Contains(t, got, "2 cards")
	assert.Contains(t, got, "c1 (deadline)")
	assert.Contains(t, got, "c2 (stage-timeout)")
	assert.Contains(t, got, "nova-sprint fleet up m1 --width 4")
}

func TestSprintOverloadCoverDecisions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-OverloadWindow)
	member := "m1"
	ol := Overload{
		Member: member,
		Timeouts: []Timeout{
			{Card: "c1", Kind: TimeoutDeadline, At: windowStart.Add(5 * time.Minute)},
		},
		Width: 8,
	}
	want := []string{"fleet up m1 --width 4", "wait 15m"}
	assert.Equal(t, want, ol.Decisions())
}
