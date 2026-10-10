package redisconn

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// storeRefusal is a store's typed reply (redis.Error): a refusal the store
// wrote, which Classify reads before any transport wrapper.
type storeRefusal string

func (storeRefusal) RedisError()     {}
func (e storeRefusal) Error() string { return string(e) }

// TestWrappedCancellationKeepsCallerRemedy: a cancelled context keeps its
// class and its remedy through the transport wrappers net and go-redis put
// around it. Classify reads the cancellation after this package's own
// failures and the store's typed replies, so a wrapped cancellation is Other
// and explain names the cancellation remedy whether it failed opening the
// connection or running a later command. A deadline, a refused dial and a
// timed-out read stay Unreachable; a login refusal and a package failure keep
// the class they were made with.
func TestWrappedCancellationKeepsCallerRemedy(t *testing.T) {
	t.Parallel()
	const (
		cancelRemedy = "cancelled before the store answered"
		dialRemedy   = "start the store or correct the address"
		userRemedy   = "name the user"
	)
	cancelledDial := &net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	timeout := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	_, ours := Resolve(Options{}, nil)

	for _, c := range []struct {
		name       string
		err        error
		want       Class
		wantRemedy string
	}{
		{"a bare cancellation", context.Canceled, Other, cancelRemedy},
		{"a cancellation wrapped once", fmt.Errorf("table list: %w", context.Canceled), Other, cancelRemedy},
		{"a cancellation wrapped twice", fmt.Errorf("verb: %w", fmt.Errorf("table list: %w", context.Canceled)), Other, cancelRemedy},
		{"a cancellation inside a dial wrapper", cancelledDial, Other, cancelRemedy},
		{"a cancellation inside a dial wrapper, wrapped in text", fmt.Errorf("open: %w", cancelledDial), Other, cancelRemedy},

		{"a deadline stays unreachable", context.DeadlineExceeded, Unreachable, dialRemedy},
		{"a refused dial stays unreachable", refused, Unreachable, dialRemedy},
		{"a timed-out read stays unreachable", timeout, Unreachable, dialRemedy},

		{"a login refusal inside a transport wrapper", &net.OpError{Op: "read", Net: "tcp", Err: storeRefusal("NOAUTH Authentication required.")}, AuthRefused, userRemedy},
		{"this package's failure keeps its class", &net.OpError{Op: "dial", Net: "tcp", Err: ours}, Unreachable, dialRemedy},
	} {
		assert.Equal(t, c.want, Classify(c.err), "%s: Classify(%v)", c.name, c.err)
		for _, opening := range []bool{true, false} {
			f := explain(login{Options: Options{Addr: "store.test:6379"}}, hider(""), c.err, opening)
			assert.Equal(t, c.want, f.class, "%s (opening=%t): class", c.name, opening)
			assert.ErrorIs(t, f, c.err, "%s (opening=%t): the cause is preserved", c.name, opening)
			assert.Contains(t, f.Error(), c.wantRemedy, "%s (opening=%t): the remedy names the caller's next step", c.name, opening)
		}
	}
}
