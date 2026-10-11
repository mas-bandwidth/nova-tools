// Package hooktest is the test rig of the hook record: a hook.Store over a
// miniredis reached through net.Pipe, so no TCP connection is made (the
// cleanup fails a test whose fake took one), on a clock that moves only when
// a test moves it. Every tool that gates on the hook tests its gate on it.
package hooktest

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/redis/go-redis/v9"
)

// Rig is one fake store and the hook.Store over it.
type Rig struct {
	MR     *miniredis.Miniredis
	Client *redis.Client // the one client, for a tool that opens the bus store itself
	Store  hook.Store
	now    time.Time
}

// New is a rig whose clock reads start, closed at the test's end.
func New(t testing.TB) *Rig {
	t.Helper()
	mr := miniredis.RunT(t)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	mr.SetTime(now)
	var piped atomic.Int64
	c := redis.NewClient(&redis.Options{
		Addr:            "127.0.0.1:0", // never dialled: Dialer is the transport
		DisableIdentity: true,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			near, far := net.Pipe()
			piped.Add(1)
			mr.Server().ServeConn(far)
			return near, nil
		},
	})
	t.Cleanup(func() {
		_ = c.Close() // ignored: a test fixture's cleanup; the test's assertions are the report
		if got := int64(mr.TotalConnectionCount()); got != piped.Load() {
			t.Errorf("hooktest: the fake took %d connections and only %d were pipes: a TCP connection was made", got, piped.Load())
		}
	})
	return &Rig{MR: mr, Client: c, Store: hook.Store{C: c}, now: now}
}

// Pass moves the fake's clock by d: the TTLs and TIME together.
func (r *Rig) Pass(d time.Duration) {
	r.MR.FastForward(d)
	r.now = r.now.Add(d)
	r.MR.SetTime(r.now)
}

// Hook hooks name in as role with every push of the role live and proven,
// as its holders would after their first beat.
func (r *Rig) Hook(t testing.TB, name string, role hook.Role) {
	t.Helper()
	h := &hook.Holder{Store: r.Store, Name: name, Role: role, Pushes: hook.PushesOf(role), By: "hooktest"}
	ctx := context.Background()
	if err := h.Start(ctx); err != nil {
		t.Fatalf("hooktest: hook %s as %s: %v", name, role, err)
	}
	proven := map[hook.Push]bool{}
	for _, p := range h.Pushes {
		proven[p] = true
	}
	if err := h.Beat(ctx, proven); err != nil {
		t.Fatalf("hooktest: prove %s as %s: %v", name, role, err)
	}
}

// Unhook unhooks name.
func (r *Rig) Unhook(t testing.TB, name string) {
	t.Helper()
	if _, err := r.Store.Unhook(context.Background(), name); err != nil {
		t.Fatalf("hooktest: unhook %s: %v", name, err)
	}
}

// State is name's status now.
func (r *Rig) State(t testing.TB, name string) hook.Status {
	t.Helper()
	st, _, err := r.Store.Read(context.Background(), name)
	if err != nil {
		t.Fatalf("hooktest: read %s: %v", name, err)
	}
	return st[0]
}
