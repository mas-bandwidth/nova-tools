package preflight

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// t0 is the fixture's Redis server time. Every age in these controls is
// measured from it, never from the test machine's clock.
var t0 = time.Unix(1_790_000_000, 0)

func fixture(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.SetTime(t0)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return mr, c
}

func ago(d time.Duration) float64 { return float64(t0.Add(-d).Unix()) }

func find(t *testing.T, lines []Line, n string) Line {
	t.Helper()
	for _, l := range lines {
		if l.N == n {
			return l
		}
	}
	t.Fatalf("no line %s in %v", n, lines)
	return Line{}
}

func TestLineAndExitCode(t *testing.T) {
	t.Parallel()

	green := Line{N: "7.3", Name: "leases-vs-beats", Why: "8 leased, 8 living"}
	red := Line{N: "7.2", Name: "state-files", Red: true, Why: "policy names BEAT"}
	if got := green.String(); got != "GREEN 7.3 leases-vs-beats: 8 leased, 8 living" {
		t.Fatalf("green line %q", got)
	}
	if got := red.String(); !strings.HasPrefix(got, "RED 7.2 state-files: ") {
		t.Fatalf("red line %q", got)
	}
	if ExitCode([]Line{green}) != 0 {
		t.Fatal("all green must exit 0")
	}
	if ExitCode([]Line{green, red}) != 1 {
		t.Fatal("any RED must exit 1")
	}
}

// Control 18 (#2756 section 8): a sprint whose policy names a state file is
// preflight red, and so is a retired file touched inside the window.
func TestControl18StateFileIsRed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	cleanPolicy := filepath.Join(dir, "policy.conf")
	if err := os.WriteFile(cleanPolicy, []byte("backpressure_missing = open\ndebt_cap = 40\nreaders = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirtyPolicy := filepath.Join(dir, "dirty.conf")
	if err := os.WriteFile(dirtyPolicy, []byte("backpressure_missing = open\nbeat = /Users/x/rowan-working/sprint/BEAT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	clock := func() time.Time { return now }

	t.Run("clean policy is green", func(t *testing.T) {
		_, c := fixture(t)
		c.HSet(ctx, "s:control-00000018:policy", "backpressure_missing", "open", "debt_cap", "40", "readers", "1")
		l := checkStateFiles(ctx, c, Options{Sprint: "control-00000018", PolicyFile: cleanPolicy, Now: clock})
		if l.Red || l.N != "7.2" {
			t.Fatalf("clean policy: %s", l)
		}
	})
	t.Run("policy hash naming a backpressure file is red", func(t *testing.T) {
		_, c := fixture(t)
		c.HSet(ctx, "s:control-00000018:policy", "backpressure_missing", "open", "debt_file", "/tmp/sprint/backpressure.on")
		l := checkStateFiles(ctx, c, Options{Sprint: "control-00000018", PolicyFile: cleanPolicy, Now: clock})
		if !l.Red || !strings.Contains(l.Why, "backpressure.on") {
			t.Fatalf("policy hash names a state file: %s", l)
		}
		if ExitCode([]Line{l}) != 1 {
			t.Fatal("a RED state-file line must exit 1")
		}
	})
	t.Run("policy file naming BEAT is red", func(t *testing.T) {
		_, c := fixture(t)
		c.HSet(ctx, "s:control-00000018:policy", "backpressure_missing", "open")
		l := checkStateFiles(ctx, c, Options{Sprint: "control-00000018", PolicyFile: dirtyPolicy, Now: clock})
		if !l.Red || !strings.Contains(l.Why, "BEAT") {
			t.Fatalf("policy file names BEAT: %s", l)
		}
	})
	t.Run("unit env and launcher config naming a TSV or lock are red", func(t *testing.T) {
		_, c := fixture(t)
		env := filepath.Join(dir, "unit.env")
		if err := os.WriteFile(env, []byte("CARDS=/home/u/nova-bench/cards.tsv\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		launcher := filepath.Join(dir, "launcher.conf")
		if err := os.WriteFile(launcher, []byte("lock /tmp/card-dealer.lock\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		l := checkStateFiles(ctx, c, Options{Sprint: "control-00000018", UnitEnv: []string{env}, LauncherConfig: launcher, Now: clock})
		if !l.Red || !strings.Contains(l.Why, "cards.tsv") || !strings.Contains(l.Why, "card-dealer.lock") {
			t.Fatalf("unit env and launcher: %s", l)
		}
	})
	t.Run("a queued card whose results name a state file is red", func(t *testing.T) {
		_, c := fixture(t)
		c.SAdd(ctx, "s:control-00000018:idx:card:queued", "a1")
		c.HSet(ctx, "s:control-00000018:card:a1", "results", "/home/u/results/queue.tsv")
		l := checkStateFiles(ctx, c, Options{Sprint: "control-00000018", Now: clock})
		if !l.Red || !strings.Contains(l.Why, "card a1") {
			t.Fatalf("card results: %s", l)
		}
	})
	t.Run("a retired file touched inside ten minutes is red, older is green", func(t *testing.T) {
		_, c := fixture(t)
		retired := filepath.Join(dir, "friend-slots.tsv")
		if err := os.WriteFile(retired, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(retired, now.Add(-5*time.Minute), now.Add(-5*time.Minute)); err != nil {
			t.Fatal(err)
		}
		l := checkStateFiles(ctx, c, Options{Sprint: "control-00000018", Retired: []string{retired}, Now: clock})
		if !l.Red || !strings.Contains(l.Why, "friend-slots.tsv") {
			t.Fatalf("retired file touched 5 min ago: %s", l)
		}
		if err := os.Chtimes(retired, now.Add(-11*time.Minute), now.Add(-11*time.Minute)); err != nil {
			t.Fatal(err)
		}
		l = checkStateFiles(ctx, c, Options{Sprint: "control-00000018", Retired: []string{retired}, Now: clock})
		if l.Red {
			t.Fatalf("retired file untouched 11 min: %s", l)
		}
	})
	t.Run("an unreadable named file is red, never skipped", func(t *testing.T) {
		_, c := fixture(t)
		l := checkStateFiles(ctx, c, Options{Sprint: "control-00000018", PolicyFile: filepath.Join(dir, "missing.conf"), Now: clock})
		if !l.Red || !strings.Contains(l.Why, "missing.conf") {
			t.Fatalf("missing policy file: %s", l)
		}
	})
}

// Stella's hold 6 on #3005: a relative state filename (bare `BEAT`) was
// returned as a word before the BEAT, backpressure and control names were
// checked. Every shape a state file can be named in, and the words that must
// stay green.
func TestIsStateFileShapes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		tok  string
		want bool
	}{
		{"BEAT", true},
		{"BEAT_rowan", true},
		{"./BEAT", true},
		{"sprint/BEAT", true},
		{"/Users/x/rowan-working/sprint/BEAT", true},
		{"BEAT.rowan", true},
		{"backpressure", true},
		{"backpressure.on", true},
		{"/tmp/sprint/backpressure.on", true},
		{"control", true},
		{"control.conf", true},
		{"etc/control", true},
		{"cards.tsv", true},
		{"/tmp/card-dealer.lock", true},
		{"dealer.pid", true},
		{"backpressure_missing", false},
		{"beat", false},
		{"open", false},
		{"debt_cap", false},
		{"control-00000018", false},
		{"/home/u/results", false},
	} {
		if got := isStateFile(tc.tok); got != tc.want {
			t.Errorf("isStateFile(%q) = %v, want %v", tc.tok, got, tc.want)
		}
	}
}

// A policy file naming a relative BEAT is RED end to end, not only the
// absolute path.
func TestStateFileRelativeBEATIsRed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	policy := filepath.Join(t.TempDir(), "policy.conf")
	if err := os.WriteFile(policy, []byte("backpressure_missing = open\nbeat = BEAT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, c := fixture(t)
	c.HSet(ctx, "s:control-00000018:policy", "backpressure_missing", "open")
	l := checkStateFiles(ctx, c, Options{Sprint: "control-00000018", PolicyFile: policy})
	if !l.Red || !strings.Contains(l.Why, "names BEAT") {
		t.Fatalf("policy file names a relative BEAT: %s", l)
	}
}

// Control 1's preflight half (#2756 section 8) and check 7.3 (Johnny 10):
// eight claims, one child beats.
func TestPreflightLeasesNotEqualBeats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seed := func(c *redis.Client, consumer string, startAge, beatAge time.Duration) {
		kind, name, _ := strings.Cut(consumer, ":")
		c.SAdd(ctx, kind+"s", name)
		for i := 1; i <= 7; i++ {
			c.ZAdd(ctx, consumer+":starting", redis.Z{Score: ago(startAge), Member: fmt.Sprintf("control-00000001/t%d/1", i)})
		}
		c.ZAdd(ctx, consumer+":living", redis.Z{Score: ago(beatAge), Member: "control-00000001/t8/1"})
	}
	t.Run("at 10 s eight leases with one beat is a healthy deal", func(t *testing.T) {
		_, c := fixture(t)
		seed(c, "friend:ctl-a", 10*time.Second, 5*time.Second)
		l := checkLeases(ctx, c)
		if l.Red || l.N != "7.3" {
			t.Fatalf("at 10 s: %s", l)
		}
	})
	t.Run("at 70 s seven unreleased reservations are red", func(t *testing.T) {
		_, c := fixture(t)
		seed(c, "friend:ctl-a", 70*time.Second, 5*time.Second)
		l := checkLeases(ctx, c)
		if !l.Red || !strings.Contains(l.Why, "friend ctl-a") || !strings.Contains(l.Why, "7 starting past 60s") {
			t.Fatalf("at 70 s: %s", l)
		}
		if ExitCode([]Line{l}) != 1 {
			t.Fatal("a RED lease line must exit 1")
		}
	})
	t.Run("after release starting 0 living 1 is green", func(t *testing.T) {
		mr, c := fixture(t)
		seed(c, "friend:ctl-a", 70*time.Second, 5*time.Second)
		mr.Del("friend:ctl-a:starting")
		l := checkLeases(ctx, c)
		if l.Red {
			t.Fatalf("after release: %s", l)
		}
	})
	t.Run("a living lease whose beat is older than 120 s is red", func(t *testing.T) {
		_, c := fixture(t)
		c.SAdd(ctx, "benches", "ctl-b")
		c.ZAdd(ctx, "bench:ctl-b:living", redis.Z{Score: ago(130 * time.Second), Member: "control-00000001/c1/1"})
		c.ZAdd(ctx, "bench:ctl-b:living", redis.Z{Score: ago(30 * time.Second), Member: "control-00000001/c2/1"})
		l := checkLeases(ctx, c)
		if !l.Red || !strings.Contains(l.Why, "bench ctl-b") || !strings.Contains(l.Why, "1 living beat past 120s") {
			t.Fatalf("stale bench beat: %s", l)
		}
	})
	t.Run("millisecond scores read the same as seconds", func(t *testing.T) {
		_, c := fixture(t)
		c.SAdd(ctx, "friends", "ctl-a")
		c.ZAdd(ctx, "friend:ctl-a:starting", redis.Z{Score: ago(70*time.Second) * 1000, Member: "control-00000001/t1/1"})
		if l := checkLeases(ctx, c); !l.Red {
			t.Fatalf("ms score: %s", l)
		}
	})
}

func TestPreflightReconcilerLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	at := func(d time.Duration) string { return fmt.Sprint(int64(ago(d))) }
	_, c := fixture(t)
	if l := checkReconciler(ctx, c); !l.Red || !strings.Contains(l.Why, "lease:reconciler missing") {
		t.Fatalf("no lease: %s", l)
	}
	c.HSet(ctx, "lease:reconciler", "instance", "r1", "token", "1.ab", "host", "space", "at", at(2*time.Second))
	c.HSet(ctx, "proc:reconciler", "pass_at", at(25*time.Second))
	if l := checkReconciler(ctx, c); !l.Red || !strings.Contains(l.Why, "last pass 25s ago") {
		t.Fatalf("old pass: %s", l)
	}
	c.HSet(ctx, "proc:reconciler", "pass_at", at(3*time.Second))
	if l := checkReconciler(ctx, c); l.Red || l.N != "7.7" {
		t.Fatalf("healthy reconciler: %s", l)
	}
	c.HSet(ctx, "lease:reconciler", "at", at(9*time.Second))
	if l := checkReconciler(ctx, c); !l.Red || !strings.Contains(l.Why, "stale") {
		t.Fatalf("stale lease: %s", l)
	}
}

func TestPreflightStateWithoutReceipt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, c := fixture(t)
	s := "control-00000010"
	c.SAdd(ctx, "s:"+s+":idx:task:closed", "t1", "t2")
	c.SAdd(ctx, "s:"+s+":idx:card:queued", "c1")
	c.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + s + ":log", Values: []any{"kind", "task", "id", "t1", "from", "open", "to", "working"}})
	c.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + s + ":log", Values: []any{"kind", "task", "id", "t1", "from", "working", "to", "closed"}})
	c.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + s + ":log", Values: []any{"kind", "task", "id", "t2", "from", "open", "to", "working"}})
	c.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + s + ":log", Values: []any{"kind", "card", "id", "c1", "from", "", "to", "queued"}})
	l := checkReceipts(ctx, c, s)
	if !l.Red || l.N != "7.10" || !strings.Contains(l.Why, "task t2 closed, receipt working") || strings.Contains(l.Why, "t1") {
		t.Fatalf("t2 without its receipt: %s", l)
	}
	c.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + s + ":log", Values: []any{"kind", "task", "id", "t2", "from", "working", "to", "closed"}})
	if l := checkReceipts(ctx, c, s); l.Red {
		t.Fatalf("every state has its receipt: %s", l)
	}
	c.SAdd(ctx, "s:"+s+":idx:card:dealt", "c9")
	if l := checkReceipts(ctx, c, s); !l.Red || !strings.Contains(l.Why, "card c9 dealt, receipt none") {
		t.Fatalf("no receipt at all: %s", l)
	}
}

// Control 27's preflight half (#2756 2.4, 7.15).
func TestPreflightMachineCeiling(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, c := fixture(t)
	c.SAdd(ctx, "friends", "ctl-a", "ctl-b")
	c.SAdd(ctx, "benches", "ctl-bench")
	c.HSet(ctx, "machine:ctl-studio:ceiling", "slots", "64", "cores", "24", "mem_gb", "192")
	c.HSet(ctx, "friend:ctl-a:desired", "slots", "32", "machine", "ctl-studio")
	c.HSet(ctx, "friend:ctl-b:desired", "slots", "32", "machine", "ctl-studio")
	c.HSet(ctx, "friend:ctl-a:beat", "host", "ctl-studio")
	c.HSet(ctx, "bench:ctl-bench:desired", "slots", "8", "machine", "ctl-hulk")
	c.HSet(ctx, "machine:ctl-hulk:ceiling", "slots", "16")
	if l := checkCeiling(ctx, c); l.Red || l.N != "7.15" {
		t.Fatalf("32+32 under 64: %s", l)
	}
	c.Set(ctx, "friend:ctl-a:slots", "32", 0)
	if l := checkCeiling(ctx, c); !l.Red || !strings.Contains(l.Why, "friend:ctl-a:slots") {
		t.Fatalf("interim slots key beside desired: %s", l)
	}
	c.Del(ctx, "friend:ctl-a:slots")
	c.HSet(ctx, "friend:ctl-b:desired", "slots", "33")
	if l := checkCeiling(ctx, c); !l.Red || !strings.Contains(l.Why, "ctl-studio 65/64") {
		t.Fatalf("65 over 64: %s", l)
	}
	c.HSet(ctx, "friend:ctl-b:desired", "slots", "32")
	c.HSet(ctx, "bench:ctl-bench:beat", "host", "ctl-other")
	if l := checkCeiling(ctx, c); !l.Red || !strings.Contains(l.Why, "bench ctl-bench beats on ctl-other, desired ctl-hulk") {
		t.Fatalf("beat host differs: %s", l)
	}
	c.Del(ctx, "bench:ctl-bench:beat", "machine:ctl-hulk:ceiling")
	if l := checkCeiling(ctx, c); !l.Red || !strings.Contains(l.Why, "ctl-hulk has no ceiling") {
		t.Fatalf("missing ceiling: %s", l)
	}
}

// Stella's hold 6 on #3005: a desired machine with no slots field was summed
// as zero width, so the machine read green under its ceiling.
func TestPreflightMachineCeilingMissingSlotsIsRed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, c := fixture(t)
	c.SAdd(ctx, "friends", "ctl-a", "ctl-b")
	c.HSet(ctx, "machine:ctl-studio:ceiling", "slots", "32")
	c.HSet(ctx, "friend:ctl-a:desired", "slots", "32", "machine", "ctl-studio")
	c.HSet(ctx, "friend:ctl-b:desired", "machine", "ctl-studio")
	l := checkCeiling(ctx, c)
	if !l.Red || !strings.Contains(l.Why, "friend ctl-b has no desired slots on ctl-studio") {
		t.Fatalf("desired machine without slots: %s", l)
	}
	c.HSet(ctx, "friend:ctl-b:desired", "slots", "")
	if l := checkCeiling(ctx, c); !l.Red || !strings.Contains(l.Why, "friend ctl-b has no desired slots") {
		t.Fatalf("desired slots empty: %s", l)
	}
	c.HSet(ctx, "friend:ctl-b:desired", "slots", "x")
	if l := checkCeiling(ctx, c); !l.Red || !strings.Contains(l.Why, `desired slots "x" is not a number`) {
		t.Fatalf("desired slots not a number: %s", l)
	}
}

func TestStoreChecksPrintEveryCheckAndUnreachableIsRed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	defer c.Close()
	lines := StoreChecks(ctx, c, Options{Sprint: "control-00000001"})
	var got []string
	for _, l := range lines {
		got = append(got, l.N)
	}
	if strings.Join(got, " ") != "7.1 7.2 7.3 7.7 7.10 7.15" {
		t.Fatalf("checks %v", got)
	}
	if l := find(t, lines, "7.1"); !l.Red || !strings.Contains(l.Why, "unreachable") {
		t.Fatalf("unreachable: %s", l)
	}
	if ExitCode(lines) != 1 {
		t.Fatal("unreachable must exit 1")
	}
}

// Check 7.1 needs INFO and FUNCTION, which miniredis does not serve; it runs
// against a throwaway redis-server as the store package's controls do.
func TestPreflightRedisAndLibrary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, aof := range []string{"no", "yes"} {
		// The helper starts with --appendonly no; a later argument wins.
		addr := testutil.Start(t, "--appendonly", aof)
		c := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = c.Close() })
		if aof == "no" {
			if err := fn.Load(ctx, c); err != nil {
				t.Fatal(err)
			}
			if l := checkRedis(ctx, c); !l.Red || !strings.Contains(l.Why, "AOF off") {
				t.Fatalf("aof off: %s", l)
			}
			continue
		}
		if l := checkRedis(ctx, c); !l.Red || !strings.Contains(l.Why, "nova_sprint not loaded") {
			t.Fatalf("no library: %s", l)
		}
		if err := c.FunctionLoadReplace(ctx, "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'OLD' end)\n").Err(); err != nil {
			t.Fatal(err)
		}
		if l := checkRedis(ctx, c); !l.Red || !strings.Contains(l.Why, "not the binary's version") {
			t.Fatalf("old library: %s", l)
		}
		if err := fn.Load(ctx, c); err != nil {
			t.Fatal(err)
		}
		if l := checkRedis(ctx, c); l.Red {
			t.Fatalf("aof on, library at version: %s", l)
		}
	}
}
