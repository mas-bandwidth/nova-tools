package table_test

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestPitstopTitleReadsTheVerbKey (DONE-WHEN of #3887): the live title reads
// the one key the pitstop verb writes, s:<S>:pitstop (a hash), on a throwaway
// redis-server with the nova_sprint library loaded. After a set by rowan the
// title says PIT STOP with the reason and the time; after a clear it does
// not. A string left at s:<S>:pitstop (the 09-23 shape) is replaced by set
// and lifted by clear, never refused WRONGTYPE, and the title shows it while
// it stands. live.go names no other pit stop key.
func TestPitstopTitleReadsTheVerbKey(t *testing.T) {
	const S = "control-00003887"
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "s:"+S, "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	cfg := table.LiveConfig{Sprint: S}
	now := time.Now()
	title := func() string {
		t.Helper()
		snap, err := table.ReadLive(ctx, c, cfg)
		if err != nil {
			t.Fatal(err)
		}
		out := snap.RenderLive(now)
		return out[:strings.Index(out, "\n")]
	}
	if got := title(); got != "SPRINT TABLE" {
		t.Fatalf("no stop: title %q", got)
	}

	// The 09-23 string at the verb's key: the table shows the stop (the
	// dealer's EXISTS honours it too), set replaces it without --force.
	c.Set(ctx, pitstop.Key(S), "Glenn: rest tonight", 0)
	if got := title(); !strings.HasPrefix(got, "SPRINT TABLE *** PIT STOP ***") {
		t.Fatalf("wrong-typed stop: title %q", got)
	}
	r, err := pitstop.Set(ctx, c, S, "rowan", "fixes day", false, "")
	if err != nil {
		t.Fatalf("set over a string: %v", err)
	}
	if r.Outcome != pitstop.Done || r.Prior.By != "wrongtype:string" || r.Prior.Why != "Glenn: rest tonight" {
		t.Fatalf("set over a string: %+v", r)
	}
	if typ := c.Type(ctx, pitstop.Key(S)).Val(); typ != "hash" {
		t.Fatalf("after set the key is %s, want hash", typ)
	}
	at := time.UnixMilli(r.At).UTC().Format("2006-01-02T15:04:05Z")
	if got, want := title(), "SPRINT TABLE *** PIT STOP *** fixes day since "+at; got != want {
		t.Fatalf("after set:\n got %q\nwant %q", got, want)
	}

	// A scoped stop names its streams in the title.
	if _, err := pitstop.Set(ctx, c, S, "rowan", "fixes day", true, "", "nova-work"); err != nil {
		t.Fatal(err)
	}
	if got := title(); !strings.HasSuffix(got, ` streams="nova-work"`) || !strings.Contains(got, "PIT STOP *** fixes day since ") {
		t.Fatalf("scoped stop: title %q", got)
	}

	r, err = pitstop.Clear(ctx, c, S, "rowan", "")
	if err != nil || r.Outcome != pitstop.Done {
		t.Fatalf("clear: %+v %v", r, err)
	}
	if got := title(); got != "SPRINT TABLE" {
		t.Fatalf("after clear: title %q", got)
	}

	// clear lifts a string at the key (it is ours) instead of WRONGTYPE,
	// with or without streams named.
	for _, streams := range [][]string{nil, {"nova-work"}} {
		c.Set(ctx, pitstop.Key(S), "stale", 0)
		r, err = pitstop.Clear(ctx, c, S, "rowan", "", streams...)
		if err != nil || r.Outcome != pitstop.Done || r.Prior.By != "wrongtype:string" || r.Prior.Why != "stale" {
			t.Fatalf("clear %v over a string: %+v %v", streams, r, err)
		}
		if n := c.Exists(ctx, pitstop.Key(S)).Val(); n != 0 {
			t.Fatalf("clear %v over a string left the key", streams)
		}
		if got := title(); got != "SPRINT TABLE" {
			t.Fatalf("after clear %v over a string: title %q", streams, got)
		}
	}
	// Each repair leaves its receipt on s:<S>:log naming the wrong type.
	log, err := c.XRange(ctx, pitstop.LogKey(S), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	repaired := 0
	for _, m := range log {
		if ev, _ := m.Values["evidence"].(string); strings.Contains(ev, "repaired wrongtype=string") {
			repaired++
		}
	}
	if repaired != 3 {
		t.Fatalf("repair receipts = %d, want 3: %v", repaired, log)
	}

	src, err := os.ReadFile("live.go")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`sprint:.*:pitstop`).FindAll(src, -1); len(m) > 0 {
		t.Fatalf("live.go still names a second pit stop key: %q", m)
	}
}
