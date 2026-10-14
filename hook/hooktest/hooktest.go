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
	// evident is the names the rig gave heartbeat evidence (Evidence): while
	// the server row is the rig's own, Pass keeps it and their answers fresh,
	// as a running server and answering sessions would.
	evident map[string]bool
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
		// as pkg/redisconn dials: a call's context bounds its reply's read
		ContextTimeoutEnabled: true,
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
	return &Rig{MR: mr, Client: c, Store: hook.Store{C: c}, now: now, evident: map[string]bool{}}
}

// Pass moves the fake's clock by d: the TTLs and TIME together.
func (r *Rig) Pass(d time.Duration) {
	r.MR.FastForward(d)
	r.now = r.now.Add(d)
	r.MR.SetTime(r.now)
	ctx := context.Background()
	if by, err := r.Client.HGet(ctx, hook.ServeKey, "by").Result(); err != nil || by != rigServer {
		return // no row, or a real server's: the test's own heartbeat
	}
	ms := r.now.UnixMilli()
	_ = r.Client.HSet(ctx, hook.ServeKey, "at", ms-hook.BeatEvery.Milliseconds()).Err() // ignored: a fake's write; a test that needs it reads the gate
	for n := range r.evident {
		_ = r.Client.HSet(ctx, hook.RecordKey(n), "answered_at", ms).Err() // ignored: as above
	}
}

// rigServer is the by of the server row the rig writes itself.
const rigServer = "hooktest"

// Hook hooks name in as role, as nova-hook hook does (the identity's word:
// after an unhook, or over another role), with every push of the role live
// and proven, as its holders would after their first beat.
func (r *Rig) Hook(t testing.TB, name string, role hook.Role) {
	t.Helper()
	h := &hook.Holder{Store: r.Store, Name: name, Role: role, Pushes: hook.PushesOf(role), By: "hooktest"}
	ctx := context.Background()
	if _, _, err := r.Store.HookAs(ctx, name, role, "hooktest"); err != nil {
		t.Fatalf("hooktest: hook %s as %s: %v", name, role, err)
	}
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
	r.Evidence(t, name)
}

// Evidence is what a heartbeat gives a name now: a server row (written only
// where none is), and the name's answer to its challenge, so the gate's evidence rule
// (hook.Served.Down) holds for a test that is not about the heartbeat.
func (r *Rig) Evidence(t testing.TB, name string) {
	t.Helper()
	ms := r.now.UnixMilli()
	ctx := context.Background()
	// a server row only where none is, an interval old, so a real pass in the
	// test is still taken (its once-key) and the row is not yet stale
	if n, err := r.Client.Exists(ctx, hook.ServeKey).Result(); err != nil || n == 0 {
		if err := r.Client.HSet(ctx, hook.ServeKey, "at", ms-hook.BeatEvery.Milliseconds(), "every", hook.BeatEvery.Milliseconds(), "by", rigServer).Err(); err != nil {
			t.Fatalf("hooktest: the server row: %v", err)
		}
	}
	if err := r.Client.HSet(ctx, hook.RecordKey(name), "answered_at", ms).Err(); err != nil {
		t.Fatalf("hooktest: %s's answer: %v", name, err)
	}
	r.evident[name] = true
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
