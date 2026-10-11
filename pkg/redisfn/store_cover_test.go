package redisfn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreCoverUnsentError covers unsent.Error at store.go:568, which no
// existing test reaches. The failed function (store.go:520) handles *unsent
// specially, reading early.err directly (store.go:529), so unsent.Error is
// never called on the error path. The main path is the wrapped error's
// message; the refusal is the scenario unsent stands for: the caller's context
// had ended before the call was made, so nothing was sent.
func TestStoreCoverUnsentError(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"context canceled", context.Canceled, "context canceled"},
		{"deadline exceeded", context.DeadlineExceeded, "context deadline exceeded"},
		{"arbitrary cause", errors.New("connection lost"), "connection lost"},
	} {
		// Main path: unsent.Error returns the wrapped error's exact text.
		u := &unsent{err: c.err}
		if got := u.Error(); got != c.want {
			assert.Equal(t, c.want, got, "%s: unsent.Error() = %q, want %q", c.name, got, c.want)
		}
		// The wrapper preserves errors.Is for its cause.
		if !errors.Is(u, c.err) {
			assert.ErrorIs(t, u, c.err, "%s: errors.Is does not see the wrapped cause", c.name)
		}
	}
}

// TestStoreCoverWithinUnsent covers within returning *unsent when the caller's
// context has already ended: nothing is sent, and the returned error's Error
// method (store.go:568) names the cause. This is the refusal.
func TestStoreCoverWithinUnsent(t *testing.T) {
	t.Parallel()
	ended, stop := context.WithCancel(context.Background())
	stop()
	called := false
	_, err := within[string](ended, time.Second, func(ctx context.Context) (string, error) {
		called = true
		return "", errors.New("should not be called")
	})
	require.Error(t, err)
	require.False(t, called, "within invoked the store after the context had ended")
	var u *unsent
	require.ErrorAs(t, err, &u, "the error should be an *unsent")
	// Calling Error on the returned value dispatches to unsent.Error (store.go:568).
	if got := err.Error(); got != context.Canceled.Error() {
		assert.Equal(t, context.Canceled.Error(), got, "the error reads %q, want the context's text", got)
	}
	if !errors.Is(err, context.Canceled) {
		assert.ErrorIs(t, err, context.Canceled, "errors.Is does not see context.Canceled")
	}
}
