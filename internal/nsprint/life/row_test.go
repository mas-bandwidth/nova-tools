//go:build functional

package life_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// fixtureRows splits the #2674 live keyspace (every key the bash of record
// sprint-table-redis reads, captured from the fleet Redis) into what the row
// verbs must write (friend:<f> and bench:<host>, as bin/friend-row and
// bin/bench-row wrote them) and everything else, which is seeded as is.
func fixtureRows() (rest [][]string, friends, benches map[string]map[string]string, benchOrder []string) {
	friends, benches = map[string]map[string]string{}, map[string]map[string]string{}
	for _, cmd := range table.Fixture2674() {
		if cmd[0] != "HSET" {
			rest = append(rest, cmd)
			continue
		}
		f := map[string]string{}
		for i := 2; i+1 < len(cmd); i += 2 {
			f[cmd[i]] = cmd[i+1]
		}
		name, isFriend := strings.CutPrefix(cmd[1], "friend:")
		if isFriend && !strings.Contains(name, ":") {
			friends[name] = f
			continue
		}
		name, isBench := strings.CutPrefix(cmd[1], "bench:")
		if isBench && f["host"] == name {
			benches[name] = f
			benchOrder = append(benchOrder, name)
			// The dealer's own fields stay: another writer owns them.
			if f["dealer_queue"] != "" {
				rest = append(rest, []string{"HSET", cmd[1], "dealer_queue", f["dealer_queue"], "dealer_at", f["dealer_at"]})
			}
			continue
		}
		rest = append(rest, cmd)
	}
	return rest, friends, benches, benchOrder
}

// members is n distinct members for an index set or a card view.
func members(prefix string, n int) []any {
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s-%d", prefix, i))
	}
	return out
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("fixture count %q: %v", s, err)
	}
	return n
}

// TestControl3440RowVerbsRenderTheBashTable is the DONE-WHEN of #3440. The
// friend:<f> and bench:<host> rows are written ONLY by `friend row`
// (life.FriendRow, ns_friend_row) and `bench beat` (life.BenchBeat with a row
// stamp, ns_bench_beat), each one Function call, from the sources the bash
// loops read: the friend-queue index sets sprint:<S>:idx:<f>:*, the declared
// slots, the seat's own beat, and the bench's load and ncpu (its counts are the
// card views bench:<b>:cards:<w>, #3692). Ten passes; after each, the live
// table renders byte-equal to what sprint-table-redis printed on the captured
// keyspace (Golden2674), 10/10.
func TestControl3440RowVerbsRenderTheBashTable(t *testing.T) {
	t.Parallel()

	st, client, _ := controlRedis(t)
	ctx := context.Background()
	cfg, now := table.Fixture2674Config(), table.Fixture2674Now()
	rest, friends, benches, benchOrder := fixtureRows()
	if len(friends) != 4 || len(benches) == 0 {
		t.Fatalf("fixture: %d friend rows, %d bench rows", len(friends), len(benches))
	}
	for _, cmd := range rest {
		args := make([]any, len(cmd))
		for i, a := range cmd {
			args[i] = a
		}
		if err := client.Do(ctx, args...).Err(); err != nil {
			t.Fatalf("seed %v: %v", cmd, err)
		}
	}
	ix := "sprint:" + cfg.Sprint + ":idx:"
	for name, f := range friends {
		for set, field := range map[string]string{"open": "ready", "working": "working", "closed": "done"} {
			if n := atoi(t, f[field]); n > 0 {
				client.SAdd(ctx, ix+name+":"+set, members(name+"-"+set, n)...)
			}
		}
		client.HSet(ctx, "friend:"+name+":desired", "slots", f["slots"], "machine", "studio")
		if f["up"] == "1" {
			// up is the beat's at under a minute old (#4233), on the
			// server's clock (ns_friend_row reads TIME)
			client.HSet(ctx, "friend:"+name+":beat", "harness", "claude", "host", "studio", "session", name,
				"at", strconv.FormatInt(time.Now().UnixMilli(), 10))
		}
	}
	for _, name := range benchOrder {
		f := benches[name]
		ready := f["queue"]
		if at, err := time.Parse("2006-01-02T15:04:05Z", f["dealer_at"]); err == nil && f["dealer_queue"] != "" {
			if age := now.Unix() - at.Unix(); age >= 0 && age <= 30 {
				ready = f["dealer_queue"]
			}
		}
		for cell, v := range map[string]string{"ready": ready, "working": f["working"], "done": f["done"], "ok": f["ok"], "fail": f["fail"]} {
			var z []redis.Z
			for i, m := range members(name+"-"+cell, atoi(t, v)) {
				z = append(z, redis.Z{Score: float64(i), Member: m})
			}
			if len(z) > 0 {
				client.ZAdd(ctx, "bench:"+name+":cards:"+cell, z...)
			}
		}
	}

	for pass := 1; pass <= 10; pass++ {
		for name, f := range friends {
			at, err := time.Parse("2006-01-02T15:04:05Z", f["at"])
			if err != nil {
				t.Fatal(err)
			}
			if _, err := life.FriendRow(ctx, st, life.FriendRowRequest{Friend: name, Sprint: cfg.Sprint, At: at}); err != nil {
				t.Fatalf("pass %d: %v", pass, err)
			}
		}
		for _, name := range benchOrder {
			f := benches[name]
			at, err := time.Parse("2006-01-02T15:04:05Z", f["at"])
			if err != nil {
				t.Fatal(err)
			}
			res, err := life.BenchBeat(ctx, st, life.BenchRequest{
				Bench: name, Host: name, Load1: f["load1"], Session: "s-" + name, Actor: "bench",
				TTL: time.Minute, RowAt: at, NCPU: atoi(t, f["ncpu"]),
			})
			if err != nil || !res.Accepted {
				t.Fatalf("pass %d bench %s: %+v %v", pass, name, res, err)
			}
		}
		snap, err := table.ReadLive(ctx, client, cfg)
		if err != nil {
			t.Fatal(err)
		}
		// the progress line is the one count (#4411): no stream here, so
		// 0/0 with no eta; every other byte is the bash's (table.MaskXY)
		got, want := snap.RenderLive(now), table.Golden2674()
		if table.MaskXY(got) != table.MaskXY(want) || !strings.HasPrefix(got, "SPRINT TABLE\n\n0/0 done 0%, left 0, eta -\n\n") {
			t.Fatalf("pass %d: the table from the row verbs differs from sprint-table-redis\ngot:\n%s\nwant:\n%s", pass, got, want)
		}
	}
	// The rows themselves carry every field the bash loops wrote.
	for name, f := range friends {
		got := client.HGetAll(ctx, "friend:"+name).Val()
		for k, v := range f {
			if got[k] != v {
				t.Errorf("friend:%s %s = %q, bin/friend-row wrote %q", name, k, got[k], v)
			}
		}
		if last := client.Get(ctx, "friend:"+name+":last").Val(); last != f["at"] {
			t.Errorf("friend:%s:last = %q, want %q", name, last, f["at"])
		}
	}
	for name, f := range benches {
		got := client.HGetAll(ctx, "bench:"+name).Val()
		for _, k := range []string{"host", "load1", "ncpu", "at", "dealer_queue", "dealer_at"} {
			if got[k] != f[k] {
				t.Errorf("bench:%s %s = %q, want %q", name, k, got[k], f[k])
			}
		}
		if ttl := client.PTTL(ctx, "bench:"+name).Val(); ttl != -1 {
			t.Errorf("bench:%s has a TTL %s; the row never expires (an old at prints stale)", name, ttl)
		}
	}
}

// TestFriendRowReplacesAWrongTypeAndCountsDown: a friend:<f> left as a
// string is replaced by the hash, a friend with no beat is up=0, and a
// missing index set counts 0.
func TestFriendRowReplacesAWrongTypeAndCountsDown(t *testing.T) {
	t.Parallel()

	st, client, _ := controlRedis(t)
	ctx := context.Background()
	client.Set(ctx, "friend:walter", "legacy", 0)
	client.SAdd(ctx, "sprint:s1:idx:walter:open", "a", "b")
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	res, err := life.FriendRow(ctx, st, life.FriendRowRequest{Friend: "Walter", Sprint: "s1", At: at})
	if err != nil {
		t.Fatal(err)
	}
	if res.Up || res.Ready != 2 || res.Working != 0 || res.Done != 0 || res.Slots != "" || res.At != "2026-09-25T12:00:00Z" {
		t.Fatalf("result %+v", res)
	}
	got := client.HGetAll(ctx, "friend:walter").Val()
	want := map[string]string{"at": "2026-09-25T12:00:00Z", "up": "0", "ready": "2", "queue": "2", "working": "0", "waiting": "0", "width": "0", "done": "0", "slots": ""}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("friend:walter = %v, want %v", got, want)
	}
	if _, err := life.FriendRow(ctx, st, life.FriendRowRequest{Friend: "bad name", Sprint: "s1"}); err == nil {
		t.Fatal("a name that is not a friend slug was written")
	}
}

// TestBenchBeatWithoutRowStampWritesNoRow: a beat with no RowAt (callers
// before #3440) leaves bench:<b> alone.
func TestBenchBeatWithoutRowStampWritesNoRow(t *testing.T) {
	t.Parallel()

	st, client, _ := controlRedis(t)
	ctx := context.Background()
	if _, err := life.BenchBeat(ctx, st, life.BenchRequest{Bench: "b1", Session: "s", Actor: "bench"}); err != nil {
		t.Fatal(err)
	}
	if n := client.Exists(ctx, "bench:b1").Val(); n != 0 {
		t.Fatalf("bench:b1 written without a row stamp")
	}
}
