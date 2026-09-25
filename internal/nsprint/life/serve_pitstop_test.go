package life_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// lineBuf keeps what serve printed.
type lineBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lineBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lineBuf) count(prefix string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, l := range strings.Split(w.b.String(), "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func (w *lineBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// TestServePitstopDispatchesNothing: while s:<S>:pitstop holds the sprint,
// serve still beats (the seat stays up) but takes and starts no child, and
// prints `PITSTOP idle` once on entering, not every tick; `pitstop clear`
// takes and dispatches on the next pass with one `PITSTOP resume` line.
func TestServePitstopDispatchesNothing(t *testing.T) {
	t.Parallel()
	st, client := seedSeat(t, 1)
	ctx := context.Background()
	pushWork(t, st, "w1")
	if _, err := pitstop.Set(ctx, client, "s1", "glenn", "rest tonight", false, ""); err != nil {
		t.Fatal(err)
	}
	out := &lineBuf{}
	cfg := serveConfig(t, "sess-pit", 1, "done")
	cfg.Out = out
	s, err := life.NewServer(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background(), "test end") })
	for tick := 1; tick <= 3; tick++ {
		res, err := s.Pass(ctx)
		if err != nil {
			t.Fatalf("held tick %d: %v", tick, err)
		}
		if res.Taken != 0 || res.Live != 0 {
			t.Fatalf("held tick %d: %+v, want nothing taken or started", tick, res)
		}
	}
	if client.Exists(ctx, "friend:emma:beat").Val() != 1 {
		t.Fatalf("held: the seat's beat is gone; the pit stop must not stop beats")
	}
	if n := out.count("PITSTOP idle"); n != 1 {
		t.Fatalf("held: %d PITSTOP idle lines over 3 ticks, want exactly 1:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "sprint=s1 scope=all") || !strings.Contains(out.String(), `why="rest tonight"`) {
		t.Fatalf("held: the idle line does not name sprint, scope and why:\n%s", out.String())
	}

	if _, err := pitstop.Clear(ctx, client, "s1", "glenn", ""); err != nil {
		t.Fatal(err)
	}
	res, err := s.Pass(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Taken != 1 {
		t.Fatalf("cleared: %+v, want the task taken", res)
	}
	if _, err := s.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if n := out.count("PITSTOP resume"); n != 1 {
		t.Fatalf("cleared: %d PITSTOP resume lines, want exactly 1:\n%s", n, out.String())
	}
	if n := out.count("PITSTOP idle"); n != 1 {
		t.Fatalf("cleared: %d PITSTOP idle lines, want still 1:\n%s", n, out.String())
	}
}
