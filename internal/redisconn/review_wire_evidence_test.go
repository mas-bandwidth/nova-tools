package redisconn

import (
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

// The same write error can describe zero bytes sent or a whole request sent.
// A counter repair must distinguish them without dropping the latter.
type reviewWriteFailure struct {
	net.Conn
	armed atomic.Bool
	send  bool
	bytes atomic.Int64
}

func (c *reviewWriteFailure) Write(p []byte) (int, error) {
	if !c.armed.Load() {
		return c.Conn.Write(p)
	}
	if !c.send {
		return 0, io.EOF
	}
	n, err := c.Conn.Write(p)
	c.bytes.Add(int64(n))
	if err != nil {
		return n, err
	}
	return n, io.EOF
}
func TestReviewWireEvidenceDistinguishesIdenticalErrors(t *testing.T) {
	t.Parallel()
	for _, sent := range []bool{false, true} {
		name := "before any byte"
		if sent {
			name = "after whole request"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := newFakeStore(t, accepting)
			var wire *reviewWriteFailure
			dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
				nc, err := store.dial(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				wire = &reviewWriteFailure{Conn: nc, send: sent}
				return wire, nil
			}
			c, err := open(context.Background(), Options{Addr: storeAddr}, nothing, dial)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			store.commands()
			before := c.Trips().Total()
			wire.armed.Store(true)
			err = c.Client().Set(WithTripLabel(context.Background(), "write"), "key", "value", 0).Err()
			if err == nil {
				t.Fatal("fault was not delivered")
			}
			want := int64(0)
			if sent {
				want = 1
			}
			if (wire.bytes.Load() > 0) != sent {
				t.Fatalf("sent=%v, byte count=%d", sent, wire.bytes.Load())
			}
			if got := c.Trips().Total() - before; got != want {
				t.Errorf("bytes=%d error=%v trips=%d want=%d", wire.bytes.Load(), err, got, want)
			}
			if got := c.Trips().Of("write"); got != want {
				t.Errorf("label=%d want=%d", got, want)
			}
		})
	}
}

func TestReviewMarshalFailureCostsNoWireTrip(t *testing.T) {
	t.Parallel()
	c, store := opened(t, accepting)
	before := c.Trips().Total()
	err := c.Client().Set(context.Background(), "key", struct{}{}, 0).Err()
	if err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("not a marshal failure: %v", err)
	}
	if sent := store.commands(); len(sent) != 0 {
		t.Fatalf("unexpected request: %v", sent)
	}
	if got := c.Trips().Total() - before; got != 0 {
		t.Fatalf("no request sent; trips=%d want=0", got)
	}
}

// CountTrips advertises support for an ordinary go-redis client too. This is
// deliberately not Open, which disables retries. Each actual attempt costs.
func TestReviewCounterSeesEveryRetryTransmission(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t, func(n int, cmd []string) string {
		if strings.EqualFold(cmd[0], "incr") {
			return hangUp
		}
		return accepting(n, cmd)
	})
	c := redis.NewClient(&redis.Options{Addr: storeAddr, Dialer: store.dial, MaxRetries: 2, MinRetryBackoff: -1, MaxRetryBackoff: -1, DisableIdentity: true,
		MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled, EndpointType: maintnotifications.EndpointTypeNone}})
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	store.commands()
	trips := CountTrips(c)
	err := c.Incr(WithTripLabel(context.Background(), "retry"), "key").Err()
	if err == nil {
		t.Fatal("reply was not lost")
	}
	sent := store.commands()
	attempts, setup := int64(0), int64(0)
	for _, line := range sent {
		if strings.Contains(line, ": incr ") {
			attempts++
		}
		if strings.Contains(line, ": hello ") {
			setup++
		}
	}
	if attempts != 3 || setup != 2 {
		t.Fatalf("fixture sent %v, want three attempts and two handshakes", sent)
	}
	if trips.N() != attempts || trips.Setup() != setup || trips.Total() != attempts+setup || trips.Of("retry") != attempts {
		t.Fatalf("store=%v counter N=%d setup=%d total=%d label=%d; want %d/%d/%d/%d", sent, trips.N(), trips.Setup(), trips.Total(), trips.Of("retry"), attempts, setup, attempts+setup, attempts)
	}
}
