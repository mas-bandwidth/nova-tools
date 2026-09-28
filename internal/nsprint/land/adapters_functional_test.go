//go:build functional

package land_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

type fixtureFiler struct {
	mu    sync.Mutex
	files int
	err   error
}

func (f *fixtureFiler) File(context.Context, string, string, string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files++
	if f.err != nil {
		return 0, f.err
	}
	return 42, nil
}

func (*fixtureFiler) Find(context.Context, string, string, time.Time) (int, bool, error) {
	return 0, false, nil
}

func TestFlakyObserveRedis(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	st := land.NewRedisStore(c, "c42r")
	filer := &fixtureFiler{}
	obs := land.Observation{Repo: "nova-tools", Key: "flaky:nova-tools:internal/deal.TestProbe3098", Lane: "lane-a", Title: "flaky", Body: "dedup=flaky:nova-tools:internal/deal.TestProbe3098\n"}
	r, filed, err := st.ObserveLive(ctx, obs, filer)
	if err != nil || !filed || r.Status != "FILED" || r.Issue != 42 {
		t.Fatalf("first=%+v filed=%t err=%v", r, filed, err)
	}
	obs.Lane = "lane-b"
	r, filed, err = st.ObserveLive(ctx, obs, filer)
	if err != nil || filed || r.Status != "SEEN" || r.LanesHit != 2 || r.LastLane != "lane-b" {
		t.Fatalf("second=%+v filed=%t err=%v", r, filed, err)
	}
	if filer.files != 1 {
		t.Fatalf("File calls=%d want 1", filer.files)
	}
	for _, key := range []string{"flaky:intent:nova-tools:internal/deal.TestProbe3098", "flaky:lock:nova-tools:internal/deal.TestProbe3098", "lock:flaky:nova-tools:internal/deal.TestProbe3098"} {
		if n := c.Exists(ctx, key).Val(); n != 0 {
			t.Fatalf("%s exists", key)
		}
	}

	rejected := obs
	rejected.Key = "flaky:nova-tools:internal/deal.TestRejected3098"
	bad := &fixtureFiler{err: &land.HTTPStatusError{Code: 422, Body: "rejected"}}
	if _, _, err := st.ObserveLive(ctx, rejected, bad); err == nil {
		t.Fatal("422 observe succeeded")
	}
	for _, key := range []string{rejected.Key, "flaky:intent:nova-tools:internal/deal.TestRejected3098", "flaky:lock:nova-tools:internal/deal.TestRejected3098"} {
		if c.Exists(ctx, key).Val() != 0 {
			t.Fatalf("422 left %s", key)
		}
	}

	concurrent := obs
	concurrent.Key = "flaky:nova-tools:internal/deal.TestConcurrent3098"
	one := &fixtureFiler{}
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() { defer wg.Done(); _, _, _ = st.ObserveLive(ctx, concurrent, one) }()
	}
	wg.Wait()
	one.mu.Lock()
	calls := one.files
	one.mu.Unlock()
	if calls != 1 {
		t.Fatalf("concurrent File calls=%d want 1", calls)
	}
}
