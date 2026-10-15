package friend

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorNoticesRetryOnceAndAllowANewEpisode(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"before commit", "lost reply", "no coordinator"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			f := bustest.NewFake(t0, "ada", "bob")
			to := "ada"
			var records []string
			n := &Notices{Bus: &bus.Bus{Store: f}, To: func(context.Context) (string, error) { return to, nil }, Record: func(s string) { records = append(records, s) }}
			switch failure {
			case "before commit":
				f.Fail = errors.New("store unavailable")
			case "lost reply":
				f.Lose = errors.New("reply lost")
			case "no coordinator":
				to = ""
			}
			ctx := context.Background()
			m := bus.Message{From: "bob", Subject: "environment fault", Body: "the lanes are held"}
			require.True(t, n.Send(ctx, m))
			assert.Equal(t, 1, n.Unsent())
			require.NotEmpty(t, records, "the delivery failure is visible")
			f.Fail, to = nil, "ada"
			n.Flush(ctx)
			n.Flush(ctx)
			assert.Zero(t, n.Unsent())
			assert.Equal(t, 1, f.Len(bus.StreamOf("ada")), "one message even when the first write committed")
			require.True(t, n.Send(ctx, m), "a fault after recovery is a new episode")
			assert.Equal(t, 2, f.Len(bus.StreamOf("ada")))
		})
	}
}

func TestCoordinatorNoticeCapacityBackpressuresWithoutLosingEpisodes(t *testing.T) {
	t.Parallel()
	for _, cancelAdmission := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAdmission), func(t *testing.T) {
			t.Parallel()
			f := bustest.NewFake(t0, "ada", "bob")
			var ready atomic.Bool
			waiting, resume := make(chan struct{}), make(chan struct{})
			var records []string
			n := &Notices{Bus: &bus.Bus{Store: f}, To: func(context.Context) (string, error) {
				if ready.Load() {
					return "ada", nil
				}
				return "", nil
			}, Record: func(s string) { records = append(records, s) }}
			n.Wait = func(ctx context.Context) bool {
				close(waiting)
				select {
				case <-ctx.Done():
					return false
				case <-resume:
					return true
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			m := bus.Message{From: "bob", Subject: "environment fault", Body: "the lanes are held"}
			for range NoticeLimit {
				require.True(t, n.Send(ctx, m))
			}
			done := make(chan bool, 1)
			go func() { done <- n.Send(ctx, m) }()
			<-waiting
			assert.Equal(t, NoticeLimit+1, n.Unsent(), "the waiting caller is visible and inspection does not deadlock")
			if cancelAdmission {
				cancel()
			} else {
				ready.Store(true)
				close(resume)
			}
			assert.Equal(t, !cancelAdmission, <-done)
			if cancelAdmission {
				assert.Contains(t, records[len(records)-1], "cancelled before admission; unsent: environment fault")
				assert.Equal(t, NoticeLimit, n.Unsent())
				ready.Store(true)
				n.Flush(t.Context())
				assert.Equal(t, NoticeLimit, f.Len(bus.StreamOf("ada")))
			} else {
				assert.Equal(t, NoticeLimit+1, f.Len(bus.StreamOf("ada")), "every accepted and blocked episode arrives exactly once")
			}
			assert.Zero(t, n.Unsent())
		})
	}
}

// The bus retains token records longer than their retry lifetime. An old
// unknown receipt is retained for inspection without blocking a new episode
// or risking a duplicate after the bus eventually expires its token record.
func TestAnExpiredNoticeReceiptNeverBlocksOtherEpisodes(t *testing.T) {
	t.Parallel()
	f := bustest.NewFake(t0, "ada", "bob")
	now := t0
	var records []string
	n := &Notices{Bus: &bus.Bus{Store: f}, To: func(context.Context) (string, error) { return "ada", nil }, Now: func() time.Time { return now }, Record: func(s string) { records = append(records, s) }}
	f.Lose = errors.New("reply lost")
	ctx := context.Background()
	require.True(t, n.Send(ctx, bus.Message{From: "bob", Subject: "old fault", Body: "old"}))
	require.Equal(t, 1, n.Unsent())
	now = now.Add(2 * bus.DefaultTokenCleanup)
	f.Advance(2 * bus.DefaultTokenCleanup)
	require.True(t, n.Send(ctx, bus.Message{From: "bob", Subject: "new fault", Body: "new"}))
	assert.Equal(t, 1, n.Unsent(), "only the old unknown receipt remains")
	assert.Equal(t, 2, f.Len(bus.StreamOf("ada")), "old notice not duplicated after token cleanup")
	assert.Contains(t, records[len(records)-1], "past the token retry lifetime")
}
