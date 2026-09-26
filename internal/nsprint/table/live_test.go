package table_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func liveStore(t *testing.T, cmds [][]string) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	seedCommands(t, client, cmds)
	return client
}

var stamp2674 = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z`)

// TestControl2674FixtureMargins: the bash reads its own clock (date) a few
// seconds after the test shifts the snapshot to "now", so every age in the
// fixture must sit at least 5 s from the threshold it is tested against, or
// a slow bash tick would flip a cell and the control would measure load.
func TestControl2674FixtureMargins(t *testing.T) {
	t.Parallel()

	now := table.Fixture2674Now()
	cmds := append(table.Fixture2674(), table.Fixture2674Degraded()...)
	checked := 0
	check := func(what, stamp string, threshold int64) {
		at, err := time.Parse("2006-01-02T15:04:05Z", stamp)
		if err != nil {
			return
		}
		age := now.Unix() - at.Unix()
		if d := age - threshold; d > -5 && d < 5 {
			t.Errorf("%s: age %ds is within 5 s of the %ds threshold; recapture the snapshot", what, age, threshold)
		}
		checked++
	}
	for _, cmd := range cmds {
		if cmd[0] == "SET" && strings.HasSuffix(cmd[1], ":landed") {
			for _, m := range stamp2674.FindAllString(cmd[2], -1) {
				check(cmd[1], m, 180)
			}
		}
		if cmd[0] != "HSET" {
			continue
		}
		f := map[string]string{}
		for i := 2; i+1 < len(cmd); i += 2 {
			f[cmd[i]] = cmd[i+1]
		}
		switch {
		case strings.HasPrefix(cmd[1], "friend:") && !strings.Contains(cmd[1][len("friend:"):], ":"):
			check(cmd[1]+" at", f["at"], 10)
		case strings.HasPrefix(cmd[1], "bench:") && f["host"] == strings.TrimPrefix(cmd[1], "bench:"):
			check(cmd[1]+" at", f["at"], 60)
			if f["dealer_queue"] != "" {
				check(cmd[1]+" dealer_at", f["dealer_at"], 30)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no stamps checked: the snapshot has no friend or bench rows")
	}
}

// TestControl2674FailedTickNeverBlank: a failed read with no good read yet
// prints the bash's never-read shape; after a good read it keeps that read's
// friend rows and says how stale they are.
func TestControl2674FailedTickNeverBlank(t *testing.T) {
	t.Parallel()

	cfg := table.Fixture2674Config()
	now := table.Fixture2674Now()
	got := table.FailedLive(cfg, nil).RenderLive(now)
	for _, want := range []string{"rowan      |     - |       - |     - | ?         \n", "stale: never read (Redis did not answer since start)\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("never-read tick lacks %q:\n%s", want, got)
		}
	}
	client := liveStore(t, table.Fixture2674())
	good, err := table.ReadLive(context.Background(), client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	good.LastGood = now.Add(-7e9)
	got = table.FailedLive(cfg, good).RenderLive(now)
	stella := good.Friends[len(good.Friends)-1]
	row := "stella     | " + pad(stella.Ready, 5) + " | " + pad(stella.Working, 7) + " | " + pad(stella.Done, 5) + " | "
	for _, want := range []string{row, "stale: 7s (Redis did not answer; rows are the last good read)\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("failed tick lacks %q:\n%s", want, got)
		}
	}
}

func pad(v string, n int) string {
	if v == "" {
		v = "-"
	}
	for len(v) < n {
		v = " " + v
	}
	return v
}

// argLog is a go-redis hook that records every command's full args (name
// first), so a test can assert on the exact keys a read touches.
type argLog struct {
	mu   sync.Mutex
	cmds [][]string
}

func (l *argLog) DialHook(next redis.DialHook) redis.DialHook { return next }

func (l *argLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		l.record(cmd)
		return next(ctx, cmd)
	}
}

func (l *argLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, c := range cmds {
			l.record(c)
		}
		return next(ctx, cmds)
	}
}

func (l *argLog) record(cmd redis.Cmder) {
	args := cmd.Args()
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = fmt.Sprint(a)
	}
	l.mu.Lock()
	l.cmds = append(l.cmds, out)
	l.mu.Unlock()
}

func (l *argLog) commands() [][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]string(nil), l.cmds...)
}

func (l *argLog) commandsNamed(name string) [][]string {
	var out [][]string
	for _, c := range l.commands() {
		if len(c) > 0 && strings.EqualFold(c[0], name) {
			out = append(out, c)
		}
	}
	return out
}

// TestLiveNoBlockedLandedLines (DONE-WHEN of #3424): the live layout dropped
// the blocked: and landed: lines at 7:05 PM. The live golden has no such line,
// and ReadLive no longer sends ZCARD q:blocked nor reads the landed key; the
// xy MGET is gone too (#4411: the progress line is the one count, and the
// tick is still the SCAN walk plus one pipeline).
func TestLiveNoBlockedLandedLines(t *testing.T) {
	t.Parallel()
	golden := table.Golden2674()
	for _, line := range strings.Split(golden, "\n") {
		if strings.HasPrefix(line, "blocked:") || strings.HasPrefix(line, "landed:") {
			t.Fatalf("the live golden still has a %q line:\n%s", line, golden)
		}
	}
	cfg, now := table.Fixture2674Config(), table.Fixture2674Now()
	client := liveStore(t, withCardViews(table.Fixture2674(), now))
	log := &argLog{}
	client.AddHook(log)
	snap, err := table.ReadLive(context.Background(), client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out := snap.RenderLive(now); strings.Contains(out, "blocked:") || strings.Contains(out, "landed:") {
		t.Fatalf("RenderLive still prints a blocked:/landed: line:\n%s", out)
	}
	for _, cmd := range log.commands() {
		if len(cmd) > 1 && strings.EqualFold(cmd[0], "ZCARD") && cmd[1] == "q:blocked" {
			t.Fatalf("ReadLive still sends ZCARD q:blocked: %v", cmd)
		}
	}
	if mgets := log.commandsNamed("MGET"); len(mgets) != 0 {
		t.Fatalf("MGET %v: ReadLive still reads sprint-xy's key (the progress line is the one count)", mgets)
	}
	for _, cmd := range log.commands() {
		for _, a := range cmd[1:] {
			if strings.HasSuffix(a, ":xy") || strings.HasSuffix(a, ":landed") {
				t.Fatalf("ReadLive still reads a retired key: %v", cmd)
			}
		}
	}
}

// TestTableShowsStaleBenchRowNotAbsent (#3372): with the default config a
// host row whose own at is older than 2 beat intervals prints "stale" with no
// numbers and no share of the totals; it never vanishes, however old. A row
// within 2 intervals prints its numbers.
func TestTableShowsStaleBenchRowNotAbsent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 24, 20, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(-d).Format("2006-01-02T15:04:05Z") }
	row := func(host, stamp string) []string {
		return []string{"HSET", "bench:" + host, "host", host, "queue", "2", "working", "3", "done", "10", "ok", "9", "fail", "1", "load1", "4.00", "at", stamp}
	}
	// The counts are the card views (#3692); withCardViews seeds them from
	// the hash's queue, working, done, ok and fail.
	client := liveStore(t, withCardViews([][]string{
		row("fresh", at(1*time.Second)),
		row("edge", at(2*time.Second)),
		row("slow", at(3*time.Second)),
		row("dead", at(10*time.Minute)),
	}, now))
	cfg := table.LiveConfig{Friends: []string{"rowan"}}
	snap, err := table.ReadLive(context.Background(), client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.RenderLive(now)
	for _, want := range []string{
		"fresh      |     2 |       3 |    10 |     9 |     1 |  90% |   4.00\n",
		"edge       |     2 |       3 |    10 |     9 |     1 |  90% |   4.00\n",
		"slow       | stale |       ? |     ? |     ? |     ? |    ? |      ?\n",
		"dead       | stale |       ? |     ? |     ? |     ? |    ? |      ?\n",
		"total      |     4 |       6 |    20 |    18 |     2 |  90% |\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("table lacks %q:\n%s", want, got)
		}
	}
}

// TestLiveTitlePitstop (DONE-WHEN of #3423, the key moved by #3887): the
// live layout's title reads s:<S>:pitstop, the pitstop verb's hash: SPRINT
// TABLE *** PIT STOP *** <why> since <at> while it is set, SPRINT TABLE when
// it is unset; a key of another type there still reads as a stop. The HGETALL
// rides the one pipeline, so the tick is still the SCAN walk plus one
// pipeline (the xy MGET is gone, #4411); only line 1 changes, and a failed
// tick keeps the last good title.
func TestLiveTitlePitstop(t *testing.T) {
	t.Parallel()

	cfg, now := table.Fixture2674Config(), table.Fixture2674Now()
	golden := table.Golden2674()
	rest := table.MaskXY(golden[strings.Index(golden, "\n"):])
	pitKey := "s:" + cfg.Sprint + ":pitstop"
	for _, c := range []struct {
		name  string
		extra [][]string
		title string
	}{
		{"unset", nil, "SPRINT TABLE"},
		{"set", [][]string{{"HSET", pitKey, "by", "rowan", "why", "Glenn: rest\ntonight", "at", "1790000000000", "scope", "all"}},
			"SPRINT TABLE *** PIT STOP *** Glenn: rest tonight since 2026-09-21T14:13:20Z"},
		{"lifted", [][]string{{"HSET", pitKey, "by", "rowan", "why", "fixes", "at", "1790000000000", "scope", "all", "lifted:nova-work", "1"}},
			`SPRINT TABLE *** PIT STOP *** fixes since 2026-09-21T14:13:20Z lifted="nova-work"`},
		{"wrongtype", [][]string{{"SET", pitKey, "Glenn: rest tonight"}},
			"SPRINT TABLE *** PIT STOP *** (" + pitKey + " is not a hash; nova-sprint pitstop clear repairs it)"},
		{"other-sprint", [][]string{{"HSET", "s:other:pitstop", "by", "x"}}, "SPRINT TABLE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			client := liveStore(t, withCardViews(append(table.Fixture2674(), c.extra...), now))
			log := &cmdLog{}
			client.AddHook(log)
			snap, err := table.ReadLive(context.Background(), client, cfg)
			if err != nil {
				t.Fatal(err)
			}
			names, trips := log.reset()
			if got, want := snap.RenderLive(now), c.title+rest; table.MaskXY(got) != want {
				t.Fatalf("title with %s:\ngot:\n%s\nwant:\n%s", c.name, got, want)
			}
			if trips != 2 {
				t.Fatalf("round trips = %d, want 2 (SCAN with the one count's memberships, then one pipeline): %v", trips, names)
			}
			for _, n := range names {
				if n == "MGET" {
					t.Fatalf("an MGET in %v: the xy key is retired (#4411)", names)
				}
			}
			failed := table.FailedLive(cfg, snap).RenderLive(now)
			if first := failed[:strings.Index(failed, "\n")]; first != c.title {
				t.Fatalf("failed tick title = %q, want the last good %q", first, c.title)
			}
		})
	}
}

// TestLiveProgressLineIsTheOneCount (#4411): the live layout's progress line
// is the one count (ws.Counts' Header), the streams' sentinels never
// counted, read in the tick's two round trips (the SCAN carries ws:order,
// the pipeline every cell with the sentinel's ZSCORE); a failed tick keeps
// the last good line.
func TestLiveProgressLineIsTheOneCount(t *testing.T) {
	t.Parallel()
	cfg, now := table.Fixture2674Config(), table.Fixture2674Now()
	cmds := append(withCardViews(table.Fixture2674(), now), [][]string{
		{"ZADD", "ws:order", "1", "alpha", "2", "beta"},
		{"ZADD", "ws:alpha:waiting", "1", "alpha:sentinel", "2", "a1"},
		{"ZADD", "ws:alpha:landed", "3", "a2"},
		{"ZADD", "ws:beta:waiting", "1", "beta:sentinel"},
		{"ZADD", "ws:beta:review", "2", "b1"},
		{"ZADD", "ws:beta:parked", "3", "b2"},
	}...)
	client := liveStore(t, cmds)
	log := &cmdLog{}
	client.AddHook(log)
	snap, err := table.ReadLive(context.Background(), client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	names, trips := log.reset()
	if trips != 2 {
		t.Fatalf("round trips = %d, want 2: %v", trips, names)
	}
	want, err := ws.Counts(context.Background(), client, cfg.Sprint)
	if err != nil {
		t.Fatal(err)
	}
	// 3 cards (a1, a2, b1; the sentinels and the parked b2 aside), 1 landed
	if !strings.HasPrefix(want.Header(), "1/3 done 33%, left 2, eta ") {
		t.Fatalf("the one count %q; want 1/3 done 33%%, left 2", want.Header())
	}
	got := snap.RenderLive(now)
	if line := strings.Split(got, "\n")[2]; line != want.Header() {
		t.Fatalf("progress line %q; want the one count %q", line, want.Header())
	}
	if failed := table.FailedLive(cfg, snap).RenderLive(now); strings.Split(failed, "\n")[2] != want.Header() {
		t.Fatalf("a failed tick lost the last good line:\n%s", failed)
	}
}
