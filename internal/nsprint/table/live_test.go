package table_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
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

// case2674 is one keyspace of the #2674 control. Every golden is the bash of
// record's own output on that keyspace (live_bash_test.go runs it and, with
// -update-golden-2674, wrote the file); Go must print the same bytes.
type case2674 struct {
	name    string
	extra   [][]string // applied after Fixture2674()
	noRedis bool       // Redis does not answer at all
	golden  func() string
	file    string
	// diverge names a bench row the bash prints with its numbers and Go
	// prints "stale" (the bash's IFS-collapse defect; #3372); such a case has
	// no golden.
	diverge string
}

func cases2674() []case2674 {
	return []case2674{
		{name: "live", golden: table.Golden2674, file: "sprint-table-2674.golden"},
		{name: "degraded", extra: table.Fixture2674Degraded(), golden: table.Golden2674Degraded, file: "sprint-table-2674-degraded.golden"},
		{name: "noredis", noRedis: true, golden: table.Golden2674NoRedis, file: "sprint-table-2674-noredis.golden"},
		{name: "dead-bench-no-dealer-fields", diverge: "zz-dead", extra: [][]string{
			{"HSET", "bench:zz-dead", "host", "zz-dead", "queue", "2", "working", "1", "done", "5", "ok", "5", "fail", "0", "load1", "0.20", "at", table.Fixture2674Now().Add(-300 * time.Second).Format("2006-01-02T15:04:05Z")},
		}},
	}
}

// TestControl2674SprintLayout (DONE-WHEN of #2674): on the keyspace captured
// from the live fleet Redis, and on the degraded and no-Redis variants, Go's
// RenderLive prints the bytes the bash of record printed on the same keyspace.
// The "bash" subtest re-runs the pinned bash itself against a real
// redis-server loaded with the same keys and requires bash == golden == Go.
func TestControl2674SprintLayout(t *testing.T) {
	cfg, now := table.Fixture2674Config(), table.Fixture2674Now()
	for _, c := range cases2674() {
		if c.golden == nil {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			snap := table.FailedLive(cfg, nil)
			if !c.noRedis {
				var err error
				client := liveStore(t, withCardViews(append(table.Fixture2674(), c.extra...), now))
				if snap, err = table.ReadLive(context.Background(), client, cfg); err != nil {
					t.Fatal(err)
				}
			}
			if got, want := snap.RenderLive(now), c.golden(); got != want {
				t.Fatalf("Go differs from the bash's %s\ngot:\n%s\nwant:\n%s", c.file, got, want)
			}
		})
	}
	t.Run("bash", bashParity2674)
}

var stamp2674 = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z`)

// TestControl2674FixtureMargins: the bash reads its own clock (date) a few
// seconds after the test shifts the snapshot to "now", so every age in the
// fixture must sit at least 5 s from the threshold it is tested against, or
// a slow bash tick would flip a cell and the control would measure load.
func TestControl2674FixtureMargins(t *testing.T) {
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

func TestFormatLandedAgesOut(t *testing.T) {
	now := time.Date(2026, 9, 23, 19, 26, 17, 0, time.UTC)
	for raw, want := range map[string]string{
		"": "landed: ?",
		"193/666 landed 28% (a 1/2) eta=~21h at=2026-09-23T19:25:17Z": "landed: 193/666 28% -> ~21h",
		"193/666 landed 28% (a 1/2) eta=~21h at=2026-09-23T19:20:00Z": "landed: ?",
		"193/666 landed 28% (a 1/2) at=2026-09-23T19:25:17Z":          "landed: 193/666 28% -> ?",
		"garbage": "landed: ?",
	} {
		if got := table.FormatLanded(raw, now); got != want {
			t.Errorf("FormatLanded(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestTableShowsStaleBenchRowNotAbsent (#3372): with the default config a
// host row whose own at is older than 2 beat intervals prints "stale" with no
// numbers and no share of the totals; it never vanishes, however old. A row
// within 2 intervals prints its numbers.
func TestTableShowsStaleBenchRowNotAbsent(t *testing.T) {
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
	cfg := table.LiveConfig{Friends: []string{"rowan"}, Sprint: "s1"}
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
// pipeline with its one xy/landed MGET; only line 1 changes, and a failed
// tick keeps the last good title.
func TestLiveTitlePitstop(t *testing.T) {
	cfg, now := table.Fixture2674Config(), table.Fixture2674Now()
	golden := table.Golden2674()
	rest := golden[strings.Index(golden, "\n"):]
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
			if got, want := snap.RenderLive(now), c.title+rest; got != want {
				t.Fatalf("title with %s:\ngot:\n%s\nwant:\n%s", c.name, got, want)
			}
			if trips != 2 {
				t.Fatalf("round trips = %d, want 2 (SCAN, then one pipeline): %v", trips, names)
			}
			mgets := 0
			for _, n := range names {
				if n == "MGET" {
					mgets++
				}
			}
			if mgets != 1 {
				t.Fatalf("MGET count = %d, want the one xy/landed MGET: %v", mgets, names)
			}
			failed := table.FailedLive(cfg, snap).RenderLive(now)
			if first := failed[:strings.Index(failed, "\n")]; first != c.title {
				t.Fatalf("failed tick title = %q, want the last good %q", first, c.title)
			}
		})
	}
}
