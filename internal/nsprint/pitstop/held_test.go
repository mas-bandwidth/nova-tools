package pitstop_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// TestHeldEveryShape: Held and HeldOpen over a throwaway redis-server with
// the library loaded: no stop, a scope=all stop, a scope=streams stop, the
// 09-23 string key and the legacy sprint:<S>:pitstop (both whole), and a
// closed sprint's stop, which holds nothing.
func TestHeldEveryShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := fnRedis(t)
	for _, s := range []string{"all", "streams", "str", "legacy", "closed", "none"} {
		c.SAdd(ctx, "sprints", s)
		c.HSet(ctx, "s:"+s, "status", "open")
	}
	if _, err := pitstop.Set(ctx, c, "all", "glenn", "rest", false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pitstop.Set(ctx, c, "streams", "glenn", "redis only", false, "", "redis: store + bus"); err != nil {
		t.Fatal(err)
	}
	if _, err := pitstop.Set(ctx, c, "closed", "glenn", "old", false, ""); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "s:closed", "status", "closed")
	c.Set(ctx, pitstop.Key("str"), "stopped 09-23", 0)
	c.Set(ctx, pitstop.LegacyKey("legacy"), "1", 0)

	if _, held, err := pitstop.Held(ctx, c, "none"); err != nil || held {
		t.Fatalf("none: held=%v err=%v", held, err)
	}
	if _, held, err := pitstop.Held(ctx, c, "closed"); err != nil || held {
		t.Fatalf("closed sprint: held=%v err=%v, want not held", held, err)
	}
	h, held, err := pitstop.Held(ctx, c, "all")
	if err != nil || !held || !h.Whole() || h.Scope() != "all" || h.Why() != `"rest"` {
		t.Fatalf("all: %+v held=%v err=%v", h, held, err)
	}
	if got := h.Words(); got != "sprint=all scope=all" {
		t.Fatalf("all words %q", got)
	}
	h, held, err = pitstop.Held(ctx, c, "streams")
	if err != nil || !held || h.Whole() || h.Scope() != `"redis: store + bus"` {
		t.Fatalf("streams: %+v held=%v err=%v", h, held, err)
	}
	for _, s := range []string{"str", "legacy"} {
		h, held, err = pitstop.Held(ctx, c, s)
		if err != nil || !held || !h.Whole() {
			t.Fatalf("%s: %+v held=%v err=%v, want a whole stop", s, h, held, err)
		}
	}

	hs, err := pitstop.HeldOpen(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range hs {
		names = append(names, h.Sprint)
	}
	if got, want := strings.Join(names, " "), "all legacy str streams"; got != want {
		t.Fatalf("HeldOpen = %q, want %q", got, want)
	}
	if w, ok := hs.Whole(); !ok || w.Sprint != "all" {
		t.Fatalf("Whole = %+v %v", w, ok)
	}
	if _, ok := (pitstop.Holds{hs[3]}).Stream("nova-sprint"); ok {
		t.Fatal("a streams stop holds a stream it does not name")
	}
	if _, ok := (pitstop.Holds{hs[3]}).Stream("redis: store + bus"); !ok {
		t.Fatal("a streams stop does not hold its stream")
	}
}
