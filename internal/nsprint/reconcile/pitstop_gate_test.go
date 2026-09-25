package reconcile_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// The pit stop gates every duty (Glenn 2026-09-25 5:50 PM ET: "pit stop
// should enable/disable all automatic activity related to the sprint
// table"). s:<S>:pitstop was set at 5:05 PM and only the dealer honoured it;
// the land duty kept rebuilding and force-pushing every stream branch.

const (
	pgSprint = "ctl-pitstop"
	pgStream = "nova-sprint"
	pgRepo   = "mas-bandwidth/ctl-pitstop"
)

// lockedBuf is a writer the land worker's goroutine and the test share.
type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lockedBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// linesWith is the lines of s that start with prefix.
func linesWith(s, prefix string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// countDuty stands in for every duty the fixture does not build for real.
type countDuty struct{ n atomic.Int32 }

func (d *countDuty) Run(context.Context, *reconcile.Lease) (reconcile.Counts, error) {
	d.n.Add(1)
	return reconcile.Counts{}, nil
}

// TestPitstopIdlesEveryDuty: with s:<S>:pitstop set scope=all, one pass over
// the production shape (the friend deal, the land duty with its git and
// GitHub seams faked, waiting-resolve, and a stand-in for every other duty)
// moves nothing: the ready task stays ready, the merging member and the open
// landing waiting on CI are not touched (no forge client made, no CI request,
// no base tip read, no push), no other duty runs, and the pass prints exactly
// one `PITSTOP idle sprint=<S> scope=all duties=<n> skipped` line naming why.
// A second held pass prints nothing new. `pitstop clear` and one more pass
// deal the ready task and start the land worker again.
func TestPitstopIdlesEveryDuty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })

	// The sprint, one live friend with a slot, one ready task.
	head := strings.Repeat("a", 40)
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", pgSprint)
	pipe.HSet(ctx, "s:"+pgSprint, "status", "open")
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: pgSprint})
	pipe.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: pgStream})
	pipe.SAdd(ctx, "ws:names", pgStream)
	pipe.SAdd(ctx, "friends", "emma")
	pipe.Set(ctx, "friend:emma:slots", 1, 0)
	pipe.HSet(ctx, "friend:emma", "at", time.Now().UTC().Format(time.RFC3339))
	pipe.HSet(ctx, "task:t-ready", "stream", pgStream, "state", "ready", "created_at", cardEpoch+1, "kind", "build")
	pipe.ZAdd(ctx, "ws:"+pgStream+":ready", redis.Z{Score: float64(cardEpoch + 1), Member: "t-ready"})
	// One merging member, read at head, landable.
	pipe.ZAdd(ctx, "ws:"+pgStream+":merging", redis.Z{Score: float64(cardEpoch + 2), Member: "t-merge"})
	pipe.ZAdd(ctx, "friend:emma:cards:merging", redis.Z{Score: float64(cardEpoch + 2), Member: "t-merge"})
	pipe.HSet(ctx, "task:t-merge", "stream", pgStream, "state", "merging", "where", "merging", "where_ok", "-",
		"pr", pgRepo+"#7", "created_at", fmt.Sprint(cardEpoch+2), "owner", "emma", "friend", "emma")
	pipe.HSet(ctx, "cfg:land", "remote:"+pgRepo, "file:///nonexistent/ctl-pitstop.git", "min_score", "8")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Record(ctx, c, pgRepo, 7, stream.RecordFields{Head: head, Base: "dev", Stream: pgStream, Task: "t-merge"}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.AddLine(ctx, c, pgRepo, 7, fmt.Sprintf("SCORE who=stella head=%s score=10/10 gates=ci:ok,base:ok,scope:ok", head)); err != nil {
		t.Fatal(err)
	}
	// One open landing whose stream head waits on CI.
	slug, err := stream.Slug(pgStream)
	if err != nil {
		t.Fatal(err)
	}
	must(t, c.HSet(ctx, stream.LandKey(pgRepo, slug), "slug", slug, "streams", pgStream, "state", "open",
		"pr", "901", "head", strings.Repeat("b", 40), "base", "dev", "branch", "stream/"+slug+"-1").Err())
	must(t, c.SAdd(ctx, stream.IndexKey(pgRepo), slug).Err())

	// The seams: every host touch the land duty could make, counted.
	var forge, ci, tip, push atomic.Int32
	out := &lockedBuf{}
	land := &reconcile.LandDuty{
		Client: c, Repos: []string{pgRepo}, Workroot: t.TempDir(), Host: "fixture", Out: out,
		GitHub: func() (*stream.GitHub, error) {
			forge.Add(1)
			return nil, errors.New("fixture: no forge")
		},
		Request: func(context.Context, string, string, int, string) (string, error) {
			ci.Add(1)
			return "", errors.New("fixture: no CI")
		},
		BaseTip: func(context.Context, string, string) (string, error) {
			tip.Add(1)
			return "", errors.New("fixture: no remote")
		},
		Push: func(context.Context, reconcile.RebaseTask) (string, error) {
			push.Add(1)
			return "", errors.New("fixture: no push")
		},
	}
	deal := &reconcile.FriendDeal{Client: c, Out: out}
	resolve := &reconcile.WaitingResolve{Client: c, Out: out}
	other := &countDuty{}
	names := []string{"deal", "land", "waiting-resolve", "other"}
	loop := &reconcile.Loop{
		Lease:        l,
		Duties:       []reconcile.Duty{deal.Run, land.Run, resolve.Run, other.Run},
		Names:        names,
		Out:          out,
		StreamScoped: map[string]bool{"deal": true, "land": true, "waiting-resolve": true},
	}

	if _, err := pitstop.Set(ctx, c, pgSprint, "glenn", "pit stop at 5:05 PM", false, ""); err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := loop.Pass(ctx); err != nil {
			t.Fatalf("held pass %d: %v", pass, err)
		}
	}
	land.Wait()
	if got := c.ZRange(ctx, "ws:"+pgStream+":ready", 0, -1).Val(); len(got) != 1 || got[0] != "t-ready" {
		t.Errorf("held: ready = %v, want [t-ready]: a held pass dealt", got)
	}
	if n := c.ZCard(ctx, "friend:emma:cards:working").Val(); n != 0 {
		t.Errorf("held: emma working = %d, want 0", n)
	}
	if st := c.HGet(ctx, "task:t-merge", "state").Val(); st != "merging" {
		t.Errorf("held: t-merge state = %q, want merging", st)
	}
	if f, q, b, p := forge.Load(), ci.Load(), tip.Load(), push.Load(); f+q+b+p != 0 {
		t.Errorf("held: forge=%d ci=%d base-tip=%d push=%d, want no host touch at all", f, q, b, p)
	}
	if n := other.n.Load(); n != 0 {
		t.Errorf("held: the other duty ran %d times, want 0", n)
	}
	idle := linesWith(out.String(), "PITSTOP idle")
	want := fmt.Sprintf("PITSTOP idle sprint=%s scope=all duties=%d skipped", pgSprint, len(names))
	if len(idle) != 1 || !strings.HasPrefix(idle[0], want) || !strings.Contains(idle[0], `why="pit stop at 5:05 PM"`) {
		t.Fatalf("held: PITSTOP idle lines %q, want exactly one %q naming why; out:\n%s", idle, want, out.String())
	}

	if _, err := pitstop.Clear(ctx, c, pgSprint, "glenn", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := loop.Pass(ctx); err != nil {
		t.Fatalf("cleared pass: %v", err)
	}
	land.Wait()
	if got := c.ZRange(ctx, "friend:emma:cards:working", 0, -1).Val(); len(got) != 1 || got[0] != "t-ready" {
		t.Errorf("cleared: emma working = %v, want [t-ready]", got)
	}
	if n := other.n.Load(); n != 1 {
		t.Errorf("cleared: the other duty ran %d times, want 1", n)
	}
	if forge.Load() == 0 {
		t.Errorf("cleared: the land worker did not run (no forge client asked for)")
	}
	if got := linesWith(out.String(), "PITSTOP resume"); len(got) != 1 {
		t.Errorf("cleared: PITSTOP resume lines %q, want exactly one", got)
	}
	if got := linesWith(out.String(), "PITSTOP idle"); len(got) != 1 {
		t.Errorf("cleared: PITSTOP idle lines %q, want still one", got)
	}
}

// TestPitstopStreamScopeHoldsOnlyItsStream: a scope=streams stop idles no
// pass; the stream-aware friend deal skips the held stream and deals the
// other, and every other duty runs.
func TestPitstopStreamScopeHoldsOnlyItsStream(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load fn: %v", err)
	}
	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-host"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = l.Release(ctx) })
	const held, free = "redis: store + bus", "nova-sprint"
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", pgSprint)
	pipe.HSet(ctx, "s:"+pgSprint, "status", "open")
	pipe.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: held}, redis.Z{Score: 2, Member: free})
	pipe.SAdd(ctx, "ws:names", held, free)
	pipe.SAdd(ctx, "friends", "emma")
	pipe.Set(ctx, "friend:emma:slots", 2, 0)
	pipe.HSet(ctx, "friend:emma", "at", time.Now().UTC().Format(time.RFC3339))
	for i, s := range []string{held, free} {
		id := fmt.Sprintf("t%d", i)
		pipe.HSet(ctx, "task:"+id, "stream", s, "state", "ready", "created_at", cardEpoch+int64(i), "kind", "build")
		pipe.ZAdd(ctx, "ws:"+s+":ready", redis.Z{Score: float64(cardEpoch + int64(i)), Member: id})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pitstop.Set(ctx, c, pgSprint, "glenn", "hold redis", false, "", held); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuf{}
	deal := &reconcile.FriendDeal{Client: c, Out: out}
	other := &countDuty{}
	loop := &reconcile.Loop{Lease: l, Duties: []reconcile.Duty{deal.Run, other.Run}, Names: []string{"deal", "other"},
		Out: out, StreamScoped: map[string]bool{"deal": true}}
	if _, err := loop.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.ZRange(ctx, "friend:emma:cards:working", 0, -1).Val(); len(got) != 1 || got[0] != "t1" {
		t.Errorf("working = %v, want [t1]: the held stream's t0 was dealt or the free one was not", got)
	}
	if other.n.Load() != 1 {
		t.Errorf("other duty ran %d times, want 1: a stream stop idled the pass", other.n.Load())
	}
	if got := linesWith(out.String(), "PITSTOP idle"); len(got) != 0 {
		t.Errorf("PITSTOP idle lines %q under a stream stop, want none", got)
	}
}
