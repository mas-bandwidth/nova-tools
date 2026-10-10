package friend

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFriendLimitCoverRefuse tests Refuse: first refusal sets Limited with until and reason,
// Down is called once; same kind and reason again calls Down no second time;
// different reason calls it again; Beat then refuses with "not beating".
func TestFriendLimitCoverRefuse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	until := now.Add(time.Hour)
	downs := 0
	l := &Limits{
		Now:  func() time.Time { return now },
		Down: func(time.Time, string) { downs++ },
	}

	// First refusal
	l.Refuse(KindLimit, "reason1", until)
	assert.Equal(t, 1, downs, "Down called once on first refusal")
	_, reason, limited := l.Limited()
	assert.Equal(t, "reason1", reason)
	assert.True(t, limited)

	// Same kind and reason again - Down should not be called again
	l.Refuse(KindLimit, "reason1", until)
	assert.Equal(t, 1, downs, "Down not called again for same kind and reason")

	// Different reason - Down should be called again
	l.Refuse(KindLimit, "reason2", until)
	assert.Equal(t, 2, downs, "Down called again for different reason")

	// Beat should refuse with "not beating"
	err := l.Beat(func(context.Context) error { return nil })(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not beating")
}

// TestFriendLimitCoverResetOf tests resetOf on various reset lines.
func TestFriendLimitCoverResetOf(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		line string
		want time.Time
		ok   bool
	}{
		{"epoch", "limit reached|1728130800", time.Unix(1728130800, 0).In(now.Location()), true},
		{"after 3pm before 3pm", "resets at 3pm", time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC), true},
		{"after 12am", "resets at 12am", time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), true},
		{"in 2 hours", "try again in 2 hours", now.Add(2 * time.Hour), true},
		{"in 30 minutes", "resets in 30 minutes", now.Add(30 * time.Minute), true},
		{"invalid 13pm", "resets at 13pm", time.Time{}, false},
		{"invalid 3:75pm", "resets at 3:75pm", time.Time{}, false},
		{"no reset", "something else", time.Time{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := resetOf(tc.line, now)
			assert.Equal(t, tc.ok, ok, tc.line)
			if tc.ok {
				assert.True(t, tc.want.Equal(got), "%s: got %s, want %s", tc.line, got.Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
		})
	}
}

// TestFriendLimitCoverLimitAlikeText tests LimitAlikeText.
func TestFriendLimitCoverLimitAlikeText(t *testing.T) {
	t.Parallel()
	friend := "alice"
	line := "connection timeout"
	subject, body := LimitAlikeText(friend, line)
	assert.Contains(t, subject, friend)
	assert.Contains(t, body, line)
	assert.Contains(t, body, "nova-sprint friend up "+friend)
}

// fakeGatedHarness records calls for testing Gate.
type fakeGatedHarness struct {
	openSessions    []string
	deliverToCalls  []string
	deliverCalls    []string
	openSessionResp string
	deliverToResp   LaneTurn
	deliverResp     int
}

func (f *fakeGatedHarness) Deliver(ctx context.Context, text string) (int, error) {
	f.deliverCalls = append(f.deliverCalls, text)
	return f.deliverResp, nil
}

func (f *fakeGatedHarness) OpenSession(ctx context.Context, seed string) (string, error) {
	f.openSessions = append(f.openSessions, seed)
	return f.openSessionResp, nil
}

func (f *fakeGatedHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	f.deliverToCalls = append(f.deliverToCalls, text)
	return f.deliverToResp, nil
}

// TestFriendLimitCoverGate tests Gate over a fake LaneHarness.
func TestFriendLimitCoverGate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	limitedUntil := now.Add(time.Hour)
	l := &Limits{
		Now:  func() time.Time { return now },
		Down: func(time.Time, string) {},
	}
	l.limited = true
	l.until = limitedUntil

	harness := &fakeGatedHarness{
		openSessionResp: "session1",
		deliverToResp:   LaneTurn{Exit: 0},
		deliverResp:     0,
	}
	g := l.Gate(harness).(LaneHarness)

	// OpenSession and DeliverTo should reach the harness
	resp, err := g.OpenSession(context.Background(), "seed")
	require.NoError(t, err)
	assert.Equal(t, "session1", resp)
	assert.Len(t, harness.openSessions, 1)

	turn, err := g.DeliverTo(context.Background(), "session1", "text")
	require.NoError(t, err)
	assert.Equal(t, 0, turn.Exit)
	assert.Len(t, harness.deliverToCalls, 1)
}

// TestFriendLimitCoverBeatOrDown tests BeatOrDown.
func TestFriendLimitCoverBeatOrDown(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	until := now.Add(time.Hour)

	tests := []struct {
		name        string
		down        func(context.Context, time.Time, string) error
		limited     bool
		wantErr     bool
		errContains string
	}{
		{"down func that fails",
			func(context.Context, time.Time, string) error { return context.DeadlineExceeded },
			true, true, "beating down until",
		},
		{"nil down (Beat) when not limited",
			nil,
			false, false, "",
		},
		{"nil down (Beat) when limited",
			nil,
			true, true, "not beating",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := &Limits{
				Now:  func() time.Time { return now },
				Down: func(time.Time, string) {},
			}
			l.limited = tc.limited
			l.until = until

			fn := l.BeatOrDown(func(context.Context) error { return nil }, tc.down)
			err := fn(context.Background())
			if tc.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestFriendLimitCoverNonce tests nonce.
func TestFriendLimitCoverNonce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

	t.Run("nonce nil (crypto/rand)", func(t *testing.T) {
		t.Parallel()
		l := &Limits{
			Now: func() time.Time { return now },
		}
		nonce := l.nonce()
		assert.Len(t, nonce, 6)
		re := regexp.MustCompile(`^[a-z0-9]$`)
		for _, c := range nonce {
			assert.True(t, re.MatchString(string(c)), "character %c not in a-z0-9", c)
		}
	})

	t.Run("nonce set", func(t *testing.T) {
		t.Parallel()
		want := "custom123"
		l := &Limits{
			Now:   func() time.Time { return now },
			Nonce: func() string { return want },
		}
		nonce := l.nonce()
		assert.Equal(t, want, nonce)
	})
}
