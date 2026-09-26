//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/redis/go-redis/v9"
)

func pitOneLine(t *testing.T, s string) {
	t.Helper()
	if strings.Count(s, "\n") != 1 {
		t.Fatalf("want exactly one line, got %q", s)
	}
}

// TestPitstopVerbEveryBranch is the verb over the real functions (a throwaway
// redis-server: miniredis has no FCALL): set, status, set refused, set
// --force, clear, clear refused, unknown sprint, and --by defaulting to the
// seat. Each prints one line with exit 0, or one REFUSED line naming the
// remedy with exit 1.
func TestPitstopVerbEveryBranch(t *testing.T) {
	const S = "control-00003371"
	ctx := context.Background()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}

	code, _, errOut := runPit(t, "rowan", "set", "--redis", addr, "--sprint", S, "--why", "x")
	if code != 1 || !strings.Contains(errOut, "REFUSED pitstop set: sprint="+S+" is unknown") {
		t.Fatalf("unknown sprint: %d %q", code, errOut)
	}
	pitOneLine(t, errOut)
	c.HSet(ctx, "s:"+S, "status", "open")

	code, out, _ := runPit(t, "rowan", "status", "--redis", addr, "--sprint", S)
	if code != 0 || out != "PITSTOP sprint="+S+" none\n" {
		t.Fatalf("status none: %d %q", code, out)
	}

	code, out, errOut = runPit(t, "rowan", "set", "--redis", addr, "--sprint", S, "--why", "Glenn: rest tonight")
	if code != 0 || !strings.HasPrefix(out, "PITSTOP SET sprint="+S+" by=rowan at=") || !strings.HasSuffix(out, ` why="Glenn: rest tonight"`+"\n") {
		t.Fatalf("set: %d %q %q", code, out, errOut)
	}
	pitOneLine(t, out)

	code, out, _ = runPit(t, "", "status", "--redis", addr, "--sprint", S)
	if code != 0 || !strings.HasPrefix(out, "PITSTOP sprint="+S+" set by=rowan at=") || !strings.Contains(out, `why="Glenn: rest tonight"`) {
		t.Fatalf("status set: %d %q", code, out)
	}
	pitOneLine(t, out)

	code, _, errOut = runPit(t, "stella", "set", "--redis", addr, "--sprint", S, "--why", "again")
	if code != 1 || !strings.Contains(errOut, "already stopped by=rowan") || !strings.Contains(errOut, "remedy: nova-sprint pitstop clear --sprint "+S+", or set --force") {
		t.Fatalf("set over a stop: %d %q", code, errOut)
	}
	pitOneLine(t, errOut)

	code, out, _ = runPit(t, "rowan", "set", "--redis", addr, "--sprint", S, "--why", "again", "--force")
	if code != 0 || !strings.Contains(out, "by=stella") || !strings.Contains(out, `replaced_by=rowan replaced_why="Glenn: rest tonight"`) {
		t.Fatalf("set --force: %d %q", code, out)
	}
	if got := c.HGet(ctx, "s:"+S+":pitstop", "by").Val(); got != "stella" {
		t.Fatalf("--by did not win over the seat: by=%q", got)
	}

	code, out, _ = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S)
	if code != 0 || !strings.HasPrefix(out, "PITSTOP CLEAR sprint="+S+" by=rowan at=") || !strings.Contains(out, `was_by=stella`) || !strings.HasSuffix(out, `was_why="again"`+"\n") {
		t.Fatalf("clear: %d %q", code, out)
	}
	pitOneLine(t, out)

	code, _, errOut = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S)
	if code != 1 || !strings.Contains(errOut, "REFUSED pitstop clear: sprint="+S+" has no pit stop; remedy:") {
		t.Fatalf("clear with none: %d %q", code, errOut)
	}
	pitOneLine(t, errOut)

	if n := c.XLen(ctx, "s:"+S+":log").Val(); n != 3 {
		t.Fatalf("s:%s:log has %d receipts, want 3 (set, forced set, clear)", S, n)
	}
}

// TestPitstopClearNarrowsScope (DONE-WHEN of nova-tools #3371, the scope
// half): a stop holds all streams or the named ones, and clear --scope
// narrows it by exactly the streams named, over the real functions on a
// throwaway redis-server. An all-scope stop narrowed by one stream lifts only
// that stream ("run one child on nova-work"); a named-scope stop loses its
// streams one by one and the last one going lifts the whole stop; a stream
// the stop does not hold refuses with nothing written.
func TestPitstopClearNarrowsScope(t *testing.T) {
	const S = "control-3371-scope"
	const redisStream = "redis: store + bus"
	ctx := context.Background()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "s:"+S, "status", "open")
	key := "s:" + S + ":pitstop"
	holds := func(stream string) bool {
		t.Helper()
		stop, err := pitstop.Read(ctx, c, S)
		if err != nil {
			t.Fatal(err)
		}
		return stop.InScope(stream)
	}
	logLen := func() int64 { return c.XLen(ctx, "s:"+S+":log").Val() }

	// All-scope stop, one stream lifted.
	code, out, errOut := runPit(t, "rowan", "set", "--redis", addr, "--sprint", S, "--why", "Glenn 8:00 PM: stop")
	if code != 0 || !strings.Contains(out, " scope=all why=") {
		t.Fatalf("set all: %d %q %q", code, out, errOut)
	}
	if !holds("nova-work") || !holds("swarm: cards") {
		t.Fatal("an all-scope stop does not hold every stream")
	}
	code, out, errOut = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S, "--stream", "nova-work")
	if code != 0 || !strings.HasPrefix(out, "PITSTOP NARROW sprint="+S+" by=rowan at=") || !strings.Contains(out, ` lifted="nova-work" was_by=rowan`) {
		t.Fatalf("narrow all: %d %q %q", code, out, errOut)
	}
	pitOneLine(t, out)
	if holds("nova-work") || !holds("swarm: cards") || c.Exists(ctx, key).Val() != 1 {
		t.Fatal("lifting nova-work did not leave every other stream stopped")
	}
	code, out, _ = runPit(t, "", "status", "--redis", addr, "--sprint", S)
	if code != 0 || !strings.HasSuffix(out, ` lifted="nova-work"`+"\n") {
		t.Fatalf("status after narrow: %d %q", code, out)
	}
	before := logLen()
	code, _, errOut = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S, "--stream", "nova-work")
	if code != 1 || !strings.Contains(errOut, `REFUSED pitstop clear: sprint=`+S+` stop does not hold stream="nova-work"`) || logLen() != before {
		t.Fatalf("clear of a lifted stream: %d %q (log %d -> %d)", code, errOut, before, logLen())
	}
	code, out, _ = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S)
	if code != 0 || !strings.HasPrefix(out, "PITSTOP CLEAR sprint="+S+" by=rowan at=") || strings.Contains(out, "lifted=") || c.Exists(ctx, key).Val() != 0 {
		t.Fatalf("whole clear: %d %q", code, out)
	}

	// Named-scope stop: only those streams, lifted one by one.
	code, out, errOut = runPit(t, "rowan", "set", "--redis", addr, "--sprint", S, "--why", "land first",
		"--stream", redisStream, "--stream", "nova-sprint")
	if code != 0 || !strings.Contains(out, ` scope="redis: store + bus","nova-sprint" why="land first"`) {
		t.Fatalf("set streams: %d %q %q", code, out, errOut)
	}
	if !holds(redisStream) || !holds("nova-sprint") || holds("nova-work") {
		t.Fatal("a named-scope stop holds the wrong streams")
	}
	code, out, _ = runPit(t, "", "status", "--redis", addr, "--sprint", S)
	if code != 0 || !strings.HasSuffix(out, ` streams="nova-sprint","redis: store + bus"`+"\n") {
		t.Fatalf("status streams: %d %q", code, out)
	}
	before = logLen()
	code, _, errOut = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S, "--stream", "nova-sprint", "--stream", "nova-work")
	if code != 1 || !strings.Contains(errOut, `does not hold stream="nova-work"`) || logLen() != before || !holds("nova-sprint") {
		t.Fatalf("a clear naming one unheld stream wrote: %d %q", code, errOut)
	}
	code, out, _ = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S, "--stream", "nova-sprint")
	if code != 0 || !strings.HasPrefix(out, "PITSTOP NARROW ") || holds("nova-sprint") || !holds(redisStream) {
		t.Fatalf("narrow streams: %d %q", code, out)
	}
	code, out, _ = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S, "--stream", redisStream)
	if code != 0 || !strings.HasPrefix(out, "PITSTOP CLEAR sprint="+S+" ") || !strings.Contains(out, ` lifted="redis: store + bus"`) || c.Exists(ctx, key).Val() != 0 {
		t.Fatalf("the last scoped stream did not lift the stop: %d %q", code, out)
	}
	if got := logLen(); got != 6 {
		t.Fatalf("receipts = %d, want 6 (one per write, none per refusal)", got)
	}

	// Usage: --scope all stands alone and only on set; status takes none.
	for _, args := range [][]string{
		{"clear", "--sprint", S, "--stream", "all"},
		{"set", "--sprint", S, "--stream", "all", "--stream", "nova-work"},
		{"status", "--sprint", S, "--stream", "nova-work"},
		{"set", "--sprint", S, "--stream", " "},
	} {
		if code, out, errOut := runPit(t, "rowan", append(args, "--redis", addr)...); code != 2 || out != "" || !strings.HasPrefix(errOut, "nova-sprint pitstop") {
			t.Fatalf("%v: want usage exit 2, got %d %q %q", args, code, out, errOut)
		}
	}
}

// TestPitstopVerbRepairsWrongType (#3887): a string at s:<S>:pitstop (the
// 09-23 shape) is ours: status prints it with the remedy, set replaces it
// without --force, clear lifts it; nothing answers WRONGTYPE.
func TestPitstopVerbRepairsWrongType(t *testing.T) {
	const S = "control-00003887"
	ctx := context.Background()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "s:"+S, "status", "open")
	key := "s:" + S + ":pitstop"

	c.Set(ctx, key, "Glenn: rest tonight", 0)
	code, out, errOut := runPit(t, "rowan", "status", "--redis", addr, "--sprint", S)
	if code != 0 || !strings.HasPrefix(out, "PITSTOP sprint="+S+` set by=wrongtype:string why="Glenn: rest tonight"; remedy: nova-sprint pitstop clear`) {
		t.Fatalf("status over a string: %d %q %q", code, out, errOut)
	}
	pitOneLine(t, out)

	code, out, errOut = runPit(t, "rowan", "set", "--redis", addr, "--sprint", S, "--why", "fixes day")
	if code != 0 || !strings.Contains(out, `replaced_by=wrongtype:string replaced_why="Glenn: rest tonight"`) {
		t.Fatalf("set over a string: %d %q %q", code, out, errOut)
	}
	if typ := c.Type(ctx, key).Val(); typ != "hash" {
		t.Fatalf("after set the key is %s, want hash", typ)
	}

	c.Del(ctx, key)
	c.Set(ctx, key, "stale", 0)
	code, out, errOut = runPit(t, "rowan", "clear", "--redis", addr, "--sprint", S)
	if code != 0 || !strings.HasPrefix(out, "PITSTOP CLEAR sprint="+S) || !strings.Contains(out, `was_by=wrongtype:string`) || !strings.HasSuffix(out, `was_why="stale"`+"\n") {
		t.Fatalf("clear over a string: %d %q %q", code, out, errOut)
	}
	if n := c.Exists(ctx, key).Val(); n != 0 {
		t.Fatal("clear over a string left the key")
	}
}
