//go:build !windows

package table_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// The bash of record, pinned. testdata/sprint-table-redis.bash is the file
// the Studio's table loop ran on 2026-09-23 (rowan-tools
// ~/rowan-working/bin/sprint-table-redis): rowan-tools main 14ee4c8 (#262:
// friend:<name> is ONE hash, HMGET at up ready working done; no
// friend:<name>:queue/:width/:done/:last keys and no friend-beat stale line,
// #3299) plus the 3:30 PM ET edit that drops the age from the status cell
// ("up|down|stale"). testdata/redis-pipe.bash is the pipelined reader it
// sources. A change to either is a change of contract and must change the pin.
const (
	bashOfRecordSHA256 = "8e172b6f1d9e9a04a4383762423c76ccc058cd6798781e6519dcf23707c9c8ba"
	redisPipeSHA256    = "cd5317acdead07a3a8464b1a78e3e06d3a68a74a4e3c3f3d81480cd55f7b1d56"
)

var (
	updateGolden2674 = flag.Bool("update-golden-2674", false, "write the bash of record's output on each #2674 case to its golden file")
	captureLive2674  = flag.String("capture-live-2674", "", "host:port of the fleet Redis: dump every key the bash reads to testdata/sprint-table-2674-live.snap (user bench, password in NOVA_REDIS_BENCH_PASSWORD)")
)

func testWait2674() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// findTool: PATH first, then the Homebrew prefixes a CI runner's PATH may lack.
func findTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()
	return port
}

// shift2674 moves every UTC stamp in every value by d, so each age the bash
// computes from its own clock is the age the snapshot had when captured.
func shift2674(cmds [][]string, d time.Duration) [][]string {
	out := make([][]string, len(cmds))
	for i, cmd := range cmds {
		c := append([]string(nil), cmd...)
		for j := 2; j < len(c); j++ {
			c[j] = stamp2674.ReplaceAllStringFunc(c[j], func(s string) string {
				at, err := time.Parse("2006-01-02T15:04:05Z", s)
				if err != nil {
					return s
				}
				return at.Add(d).UTC().Format("2006-01-02T15:04:05Z")
			})
		}
		out[i] = c
	}
	return out
}

// bashParity2674 runs the pinned bash of record and Go on the same keyspace in
// a throwaway redis-server and requires identical bytes, and that the bytes
// are the committed golden. Skips only when a tool the bash needs is absent,
// or when date is not BSD date: the bash's beat-freshness and dealer checks
// use `date -j -f` and are skipped silently under GNU date (it runs on the
// Studio only), which would test the platform, not the port.
func bashParity2674(t *testing.T) {
	dir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	script, pipe := filepath.Join(dir, "sprint-table-redis.bash"), filepath.Join(dir, "redis-pipe.bash")
	if got := sha256File(t, script); got != bashOfRecordSHA256 {
		t.Fatalf("testdata/sprint-table-redis.bash sha256 %s, pinned %s: the bash of record changed; re-pin deliberately", got, bashOfRecordSHA256)
	}
	if got := sha256File(t, pipe); got != redisPipeSHA256 {
		t.Fatalf("testdata/redis-pipe.bash sha256 %s, pinned %s", got, redisPipeSHA256)
	}
	addr := testutil.Start(t)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	cli, bash, perl := findTool("redis-cli"), findTool("bash"), findTool("perl")
	if cli == "" || bash == "" || perl == "" {
		t.Skipf("the bash of record needs redis-cli, bash and perl (have %q %q %q)", cli, bash, perl)
	}
	if err := exec.Command("date", "-j", "-u", "-f", "%s", "0", "+%s").Run(); err != nil {
		t.Skip("date is not BSD date: the bash of record's freshness checks need `date -j -f` (Studio only)")
	}

	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	ctx := context.Background()
	// The fleet's bench ACL user: the bash authenticates as bench; no KEYS.
	const password = "control-2674"
	if err := admin.Do(ctx, "ACL", "SETUSER", "bench", "on", ">"+password, "~*", "&*", "+@all", "-keys").Err(); err != nil {
		t.Fatal(err)
	}
	bench := redis.NewClient(&redis.Options{Addr: addr, Username: "bench", Password: password})
	t.Cleanup(func() { _ = bench.Close() })

	for _, c := range cases2674() {
		t.Run(c.name, func(t *testing.T) {
			if err := admin.FlushAll(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			base := time.Now().Truncate(time.Second)
			if !c.noRedis {
				seedCommands(t, admin, shift2674(append(table.Fixture2674(), c.extra...), base.Sub(table.Fixture2674Now())))
			}
			home := t.TempDir()
			cfg := table.Fixture2674Config()
			cfg.XYFile = filepath.Join(home, "rowan-working", "tmp", "session-0919b", "SPRINT-XY.txt")
			bashPort, client := port, bench
			if c.noRedis {
				bashPort = freePort(t)
				client = redis.NewClient(&redis.Options{Addr: "127.0.0.1:" + bashPort, Username: "bench", Password: password, MaxRetries: -1})
				t.Cleanup(func() { _ = client.Close() })
			}
			got := runBash2674(t, bash, script, pipe, filepath.Dir(cli), home, bashPort, password)

			snap, err := table.ReadLive(ctx, client, cfg)
			if c.noRedis {
				if err == nil {
					t.Fatal("ReadLive answered on a closed port")
				}
				snap = table.FailedLive(cfg, nil)
			} else if err != nil {
				t.Fatal(err)
			}
			goOut := snap.RenderLive(base)

			if c.diverge != "" {
				// The one documented difference: IFS=$'\t' read collapses the
				// empty dealer_queue/dealer_at fields, `at` lands in dq, the
				// 60 s beat check is skipped and a dead bench prints forever with its
				// numbers; Go prints it "stale" (#3372).
				row := fmt.Sprintf("%-10s |", c.diverge)
				if !strings.Contains(got, row) {
					t.Fatalf("the bash no longer prints the dead bench %s (its defect is fixed?): drop this case\n%s", c.diverge, got)
				}
				if stale := fmt.Sprintf("%-10s | stale |", c.diverge); !strings.Contains(goOut, stale) {
					t.Fatalf("Go did not print the dead bench %s (beat 300 s old) as stale (#3372)\n%s", c.diverge, goOut)
				}
				return
			}
			if *updateGolden2674 {
				if err := os.WriteFile(filepath.Join(dir, c.file), []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if want := c.golden(); got != want {
				t.Fatalf("the bash of record no longer prints %s on this keyspace\nbash:\n%s\ngolden:\n%s", c.file, got, want)
			}
			if goOut != got {
				t.Fatalf("Go and the bash of record differ on the same keyspace\ngo:\n%s\nbash:\n%s", goOut, got)
			}
		})
	}
}

// runBash2674 runs ONE tick of the bash of record, exactly as the Studio's
// loop runs it after nova-secrets (STR_INNER=1, HOST_COUNTS=1), against
// 127.0.0.1:port, and returns the SPRINT-TABLE.txt it published.
func runBash2674(t *testing.T, bash, script, pipe, cliDir, home, port, password string) string {
	t.Helper()
	work := t.TempDir()
	out := filepath.Join(work, "SPRINT-TABLE.txt")
	cmd := exec.Command(bash, script, "3600")
	cmd.Env = []string{
		"PATH=" + cliDir + ":" + filepath.Dir(bash) + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + home, "TMPDIR=" + work, "LC_ALL=C",
		"STR_INNER=1", "STR_LIB=" + pipe,
		"NOVA_REDIS_HOST=127.0.0.1", "NOVA_REDIS_PORT=" + port, "NOVA_REDIS_BENCH_PASSWORD=" + password,
		"SPRINT_TABLE_OUT=" + out, "SPRINT_TABLE_RUN_DIR=" + filepath.Join(work, "run"), "HOST_COUNTS=1",
	}
	// Output to a file, not a pipe: Wait then never waits on a copy that a
	// straggler in the group still holds open.
	logFile, err := os.Create(filepath.Join(work, "bash.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); <-done }()
	logText := func() string { b, _ := os.ReadFile(logFile.Name()); return string(b) }
	for deadline := time.Now().Add(testWait2674()); ; time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(out); err == nil {
			return string(b)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("the bash exited before publishing: %v\n%s", err, logText())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the bash published nothing within %s\n%s", testWait2674(), logText())
		}
	}
}

// TestCapture2674LiveSnapshot writes testdata/sprint-table-2674-live.snap from
// the fleet Redis: every key the bash of record reads (bench:* by SCAN, the
// whole friend:<name> hash as friend-row wrote it, friend:<name>:down, the xy
// and landed keys, q:blocked), all values from ONE pipelined read. On demand
// only (-capture-live-2674 host:port); then run TestControl2674SprintLayout
// with -update-golden-2674 on the Studio to take the goldens from the bash.
func TestCapture2674LiveSnapshot(t *testing.T) {
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
