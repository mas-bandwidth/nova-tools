//go:build !windows

package table_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

var (
	updateGolden2674 = flag.Bool("update-golden-2674", false, "write the bash of record's output on each #2674 case to its golden file")
	captureLive2674  = flag.String("capture-live-2674", "", "host:port of the fleet Redis: dump every key the bash reads to testdata/sprint-table-2674-live.snap (user bench, password in NOVA_REDIS_BENCH_PASSWORD)")
)

// TestCapture2674LiveSnapshot writes testdata/sprint-table-2674-live.snap from
// the fleet Redis: every key the bash of record reads (bench:* by SCAN, the
// whole friend:<name> hash as friend-row wrote it, friend:<name>:down, the xy
// and landed keys, q:blocked), all values from ONE pipelined read. On demand
// only (-capture-live-2674 host:port); then run TestControl2674SprintLayout
// with -update-golden-2674 on the Studio to take the goldens from the bash.
func TestCapture2674LiveSnapshot(t *testing.T) {
	t.Parallel()

	if *captureLive2674 == "" {
		t.Skip("on demand: -capture-live-2674 host:port")
	}
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: *captureLive2674, Username: "bench", Password: os.Getenv("NOVA_REDIS_BENCH_PASSWORD")})
	defer client.Close()
	var keys []string
	for cursor := uint64(0); ; {
		page, next, err := client.Scan(ctx, cursor, "bench:*", 1000).Result()
		if err != nil {
			t.Fatal(err)
		}
		keys, cursor = append(keys, page...), next
		if cursor == 0 {
			break
		}
	}
	sort.Strings(keys)
	cfg := table.Fixture2674Config()
	pipe := client.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	hashes := make([]*redis.MapStringStringCmd, len(keys))
	strs := make([]*redis.StringCmd, len(keys))
	for i, k := range keys {
		types[i], hashes[i], strs[i] = pipe.Type(ctx, k), pipe.HGetAll(ctx, k), pipe.Get(ctx, k)
	}
	friends := make([]*redis.MapStringStringCmd, len(cfg.Friends))
	downs := make([]*redis.StringCmd, len(cfg.Friends))
	for i, f := range cfg.Friends {
		friends[i], downs[i] = pipe.HGetAll(ctx, "friend:"+f), pipe.Get(ctx, "friend:"+f+":down")
	}
	xyKey, landedKey := "sprint:"+cfg.Sprint+":xy", "sprint:"+cfg.Sprint+":landed"
	xy, landed := pipe.Get(ctx, xyKey), pipe.Get(ctx, landedKey)
	blocked := pipe.ZRangeWithScores(ctx, "q:blocked", 0, -1)
	_, _ = pipe.Exec(ctx)
	captured := time.Now().UTC()

	var lines []string
	add := func(args ...string) {
		for _, a := range args {
			if strings.ContainsAny(a, "\t\r\n") {
				t.Fatalf("value %q has a tab or newline; the snapshot format cannot carry it", a)
			}
		}
		lines = append(lines, strings.Join(args, "\t"))
	}
	hset := func(key string, h map[string]string) {
		fields := make([]string, 0, len(h))
		for f := range h {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		args := []string{"HSET", key}
		for _, f := range fields {
			args = append(args, f, h[f])
		}
		add(args...)
	}
	for i, k := range keys {
		switch types[i].Val() {
		case "hash":
			hset(k, hashes[i].Val())
		case "string":
			add("SET", k, strs[i].Val())
		case "none":
		default:
			t.Fatalf("%s is a %s; the snapshot format carries hashes and strings", k, types[i].Val())
		}
	}
	for i, f := range cfg.Friends {
		if h := friends[i].Val(); len(h) > 0 {
			hset("friend:"+f, h)
		}
		if v, err := downs[i].Result(); err == nil {
			add("SET", "friend:"+f+":down", v)
		}
	}
	for _, kv := range []struct {
		key string
		cmd *redis.StringCmd
	}{{xyKey, xy}, {landedKey, landed}} {
		if v, err := kv.cmd.Result(); err == nil {
			add("SET", kv.key, v)
		}
	}
	note := "q:blocked members as read"
	zs, err := blocked.Result()
	if err != nil {
		n, cerr := client.ZCard(ctx, "q:blocked").Result()
		if cerr != nil {
			t.Fatalf("q:blocked: %v / %v", err, cerr)
		}
		note = fmt.Sprintf("q:blocked members synthesized: the bench ACL refused ZRANGE (%v); the count %d is live", err, n)
		zs = nil
		for i := int64(0); i < n; i++ {
			zs = append(zs, redis.Z{Score: 0, Member: fmt.Sprintf("blocked-%03d", i)})
		}
	}
	if len(zs) > 0 {
		args := []string{"ZADD", "q:blocked"}
		for _, z := range zs {
			args = append(args, fmt.Sprintf("%.0f", z.Score), fmt.Sprint(z.Member))
		}
		add(args...)
	}
	header := []string{
		"# #2674 control fixture: every key rowan-tools bin/sprint-table-redis (the bash of record, pinned in",
		"# testdata/sprint-table-redis.bash) reads, dumped from the fleet Redis by TestCapture2674LiveSnapshot",
		"# in one pipelined read as the bench ACL user. One Redis command per line, tab-separated.",
		fmt.Sprintf("# captured=%s keys=%d; %s", captured.Truncate(time.Second).Format("2006-01-02T15:04:05Z"), len(lines), note),
	}
	path := filepath.Join("testdata", "sprint-table-2674-live.snap")
	if err := os.WriteFile(path, []byte(strings.Join(append(header, lines...), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s: %d keys at %s", path, len(lines), captured.Format(time.RFC3339))
}
