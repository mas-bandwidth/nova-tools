package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSprintLaneCoverValidLaneWho(t *testing.T) {
	t.Parallel()

	tests := []struct {
		who string
		want bool
	}{
		{"w1", true},
		{"lander/base", true},
		{"lander/v1-4", true},
		{"", false},
		{"a.b", false},
		{"/x", false},
		{"x/", false},
		{"a/b/c", false},
		{"verylongverylongverylongverylongverylongverylongverylongverylongverylongverylongverylong", false},
	}

	for _, tt := range tests {
		t.Run(tt.who, func(t *testing.T) {
			t.Parallel()
			got := ValidLaneWho(tt.who)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSprintLaneCoverLaneWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prop string
		want int
		ok   bool
	}{
		{"3", 3, true},
		{"1", 1, true},
		{"0", LaneWidthDefault, true},
		{"-2", LaneWidthDefault, true},
		{"x", LaneWidthDefault, true},
		{"", LaneWidthDefault, true},
	}

	for _, tt := range tests {
		t.Run(tt.prop, func(t *testing.T) {
			t.Parallel()
			got, ok := LaneWidth(tt.prop, nil)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.ok, ok)
		})
	}
}

func TestSprintLaneCoverExpire(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	t.Run("releases holder not seen for more than LaneHoldFor", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Holding: map[string]Holder{
				"holder1": {Since: now.Add(-LaneHoldFor - time.Minute)},
			},
			Granted: map[string]Grant{
				"holder1": {Since: now.Add(-LaneHoldFor - time.Minute)},
			},
		}
		l.Expire(now)
		assert.Empty(t, l.Holding)
		assert.Empty(t, l.Granted)
	})

	t.Run("granting the queue head in its place", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Holding: map[string]Holder{
				"holder1": {Since: now.Add(-LaneHoldFor - time.Minute)},
			},
			Waiting: map[string]Machine{
				"waiter1": {},
			},
		}
		l.Expire(now)
		assert.Empty(t, l.Holding)
		assert.Equal(t, 1, len(l.Waiting))
	})

	t.Run("releases unclaimed grant older than LaneWaitFor", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Granted: map[string]Grant{
				"holder1": {Since: now.Add(-LaneWaitFor - time.Minute)},
			},
		}
		l.Expire(now)
		assert.Empty(t, l.Granted)
	})

	t.Run("drops waiter not seen for LaneWaitFor", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Waiting: map[string]Machine{
				"waiter1": {},
			},
		}
		l.Expire(now)
		assert.Empty(t, l.Waiting)
	})

	t.Run("leaves input unchanged", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Holding: map[string]Holder{
				"holder1": {Since: now},
			},
		}
		l.Expire(now)
		assert.Equal(t, 1, len(l.Holding))
	})
}

func TestSprintLaneCoverRows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	t.Run("sorted by machine", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Holding: map[string]Holder{
				"z": {Since: now},
				"a": {Since: now},
			},
		}
		rows := l.Rows()
		assert.Equal(t, 2, len(rows))
		assert.Equal(t, "a", rows[0].Who)
		assert.Equal(t, "z", rows[1].Who)
	})

	t.Run("Held empty slice where none", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Waiting: map[string]Machine{
				"waiter1": {},
			},
		}
		rows := l.Rows()
		assert.Equal(t, 1, len(rows))
		assert.NotEmpty(t, rows[0].Held)
	})

	t.Run("Waiting empty slice where none", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Holding: map[string]Holder{
				"holder1": {Since: now},
			},
		}
		rows := l.Rows()
		assert.Equal(t, 1, len(rows))
		assert.NotEmpty(t, rows[0].Waiting)
	})

	t.Run("Since the first holder's Since", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Holding: map[string]Holder{
				"holder1": {Since: now},
			},
		}
		rows := l.Rows()
		assert.Equal(t, now, rows[0].Held[0].Since)
	})

	t.Run("zero Since and waiter listed when width 0 grants nothing", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Waiting: map[string]Machine{
				"waiter1": {},
			},
		}
		rows := l.Rows()
		assert.Equal(t, 1, len(rows))
	})

	t.Run("Kind and Width carried", func(t *testing.T) {
		t.Parallel()
		l := &Lanes{
			Holding: map[string]Holder{
				"holder1": {Since: now},
			},
		}
		rows := l.Rows()
		require.Equal(t, 1, len(rows))
		assert.NotEmpty(t, rows[0].Held)
	})
}
