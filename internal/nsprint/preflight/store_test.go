package preflight

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// storeOrder is the store lines in section order (#2947 rev 3).
const storeOrder = "7.1 7.2 7.3 7.7 7.9 7.10 7.11 7.13 7.15 7.26 7.27"

// t0 is the fixture's Redis server time. Every age in these controls is
// measured from it, never from the test machine's clock.
var t0 = time.Unix(1_790_000_000, 0)

// fixture is a miniredis at t0 holding sprint ctl with the preflight.conf
// windows in its policy. miniredis serves neither INFO nor FUNCTION, so 7.1
// is proven against redis-server here and in cmd/nova-sprint's matrix.
func fixture(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.SetTime(t0)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	policy := []any{"backpressure_missing", "open", "debt_cap", "40", "share", "rowan:1"}
	for k, v := range Defaults() {
		policy = append(policy, k, v)
	}
	c.HSet(context.Background(), "s:ctl:policy", policy...)
	return mr, c
}

func snap(t *testing.T, c *redis.Client) *snapshot {
	t.Helper()
	s := readSnapshot(context.Background(), c, "ctl")
	if s.down != nil {
		t.Fatal(s.down)
	}
	return s
}

func ago(d time.Duration) float64 { return float64(t0.Add(-d).Unix()) }

func TestLineAndExitCode(t *testing.T) {
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

// The embedded preflight.conf holds the four windows at rev 3's values, and
// 7.9 requires each of them plus the three brakes.
func TestDefaultsAreTheFourWindows(t *testing.T) {
	d := Defaults()
	want := map[string]string{"start_window_s": "60", "beat_stale_s": "120", "reconciler_pass_s": "20", "interim_window_s": "600"}
	if fmt.Sprint(d) != fmt.Sprint(want) {
		t.Fatalf("preflight.conf = %v, want %v", d, want)
	}
	for k := range want {
		found := false
		for _, f := range PolicyFields {
			found = found || f == k
		}
		if !found {
			t.Fatalf("PolicyFields lacks window %s", k)
		}
	}
}

// Stella's hold 6 on #3005: a relative state filename (bare `BEAT`) was
// returned as a word before the BEAT, backpressure and control names were
// checked. Every shape a state file can be named in, and the words that must
// stay green.
func TestIsStateFileShapes(t *testing.T) {
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
		{"nova-sprint", false},
	} {
		if got := isStateFile(tc.tok); got != tc.want {
			t.Errorf("isStateFile(%q) = %v, want %v", tc.tok, got, tc.want)
		}
	}
}

// A card's own description of its work (PATHS, DONE-WHEN) may name any file
// the work edits; only its other fields are configuration.
func TestStateFileCardWorkFieldsAreNotConfiguration(t *testing.T) {
	ctx := context.Background()
	_, c := fixture(t)
	c.SAdd(ctx, "s:ctl:idx:card:queued", "c1")
	c.HSet(ctx, "s:ctl:card:c1", "label", "c1", "paths", "fleet/loops.tsv", "done_when", "cards.tsv parses")
	if l := snap(t, c).checkStateFiles(StateSources, InterimKeys); l.Red {
		t.Fatalf("a card editing a TSV is not a state file: %s", l)
	}
	c.HSet(ctx, "s:ctl:card:c1", "results", "/home/u/results/queue.tsv")
	if l := snap(t, c).checkStateFiles(StateSources, InterimKeys); !l.Red || !strings.Contains(l.Why, "s:ctl:card:c1 results names") {
		t.Fatalf("a card field naming a TSV: %s", l)
	}
}

// Control 1's preflight half (#2756 section 8) and check 7.3 (Johnny 10):
// eight claims, one child beats, with the windows from the policy.
func TestPreflightLeasesNotEqualBeats(t *testing.T) {
	ctx := context.Background()
	seed := func(c *redis.Client, consumer string, startAge, beatAge time.Duration) {
		kind, name, _ := strings.Cut(consumer, ":")
		c.SAdd(ctx, kind+"s", name)
		for i := 1; i <= 7; i++ {
			c.ZAdd(ctx, consumer+":starting", redis.Z{Score: ago(startAge), Member: fmt.Sprintf("ctl/t%d/1", i)})
		}
		c.ZAdd(ctx, consumer+":living", redis.Z{Score: ago(beatAge), Member: "ctl/t8/1"})
	}
	t.Run("at 10 s eight leases with one beat is a healthy deal", func(t *testing.T) {
		_, c := fixture(t)
		seed(c, "friend:ctl-a", 10*time.Second, 5*time.Second)
		if l := snap(t, c).checkLeases(); l.Red || l.N != "7.3" {
			t.Fatalf("at 10 s: %s", l)
		}
	})
	t.Run("at 70 s seven unreleased reservations are red", func(t *testing.T) {
		_, c := fixture(t)
		seed(c, "friend:ctl-a", 70*time.Second, 5*time.Second)
		l := snap(t, c).checkLeases()
		if !l.Red || !strings.Contains(l.Why, "friend ctl-a 7 starting past 60s (leased 8, living beats 1)") {
			t.Fatalf("at 70 s: %s", l)
		}
	})
	t.Run("the start window is the policy's", func(t *testing.T) {
		_, c := fixture(t)
		c.HSet(ctx, "s:ctl:policy", "start_window_s", "90")
		seed(c, "friend:ctl-a", 70*time.Second, 5*time.Second)
		if l := snap(t, c).checkLeases(); l.Red {
			t.Fatalf("70 s under a 90 s window: %s", l)
		}
	})
	t.Run("a living lease whose beat is older than beat_stale_s is red", func(t *testing.T) {
		_, c := fixture(t)
		c.SAdd(ctx, "benches", "ctl-b")
		c.ZAdd(ctx, "bench:ctl-b:living", redis.Z{Score: ago(130 * time.Second), Member: "ctl/c1/1"})
		c.ZAdd(ctx, "bench:ctl-b:living", redis.Z{Score: ago(30 * time.Second), Member: "ctl/c2/1"})
		l := snap(t, c).checkLeases()
		if !l.Red || !strings.Contains(l.Why, "bench ctl-b 1 living beat past 120s") {
			t.Fatalf("stale bench beat: %s", l)
		}
	})
	t.Run("millisecond scores read the same as seconds", func(t *testing.T) {
		_, c := fixture(t)
		c.SAdd(ctx, "friends", "ctl-a")
		c.ZAdd(ctx, "friend:ctl-a:starting", redis.Z{Score: ago(70*time.Second) * 1000, Member: "ctl/t1/1"})
		if l := snap(t, c).checkLeases(); !l.Red {
			t.Fatalf("ms score: %s", l)
		}
	})
}

func TestPreflightReconcilerLease(t *testing.T) {
	ctx := context.Background()
	at := func(d time.Duration) string { return fmt.Sprint(int64(ago(d))) }
	_, c := fixture(t)
	if l := snap(t, c).checkReconciler(); !l.Red || !strings.Contains(l.Why, "lease:reconciler missing") {
		t.Fatalf("no lease: %s", l)
	}
	c.HSet(ctx, "lease:reconciler", "instance", "r1", "token", "1.ab", "at", at(2*time.Second))
	c.HSet(ctx, "proc:reconciler", "pass_at", at(25*time.Second))
	if l := snap(t, c).checkReconciler(); !l.Red || !strings.Contains(l.Why, "last pass 25s ago (limit 20s)") {
		t.Fatalf("old pass: %s", l)
	}
	c.HSet(ctx, "proc:reconciler", "pass_at", at(3*time.Second))
	if l := snap(t, c).checkReconciler(); l.Red || l.N != "7.7" {
		t.Fatalf("healthy reconciler: %s", l)
	}
	c.HSet(ctx, "lease:reconciler", "at", at(30*time.Second))
	if l := snap(t, c).checkReconciler(); !l.Red || !strings.Contains(l.Why, "stale") {
		t.Fatalf("stale lease: %s", l)
	}
}

// 7.10 reads the index keys fn.IndexStates names; an id in a state its latest
// receipt does not name is RED, and so is one with no receipt.
func TestPreflightStateWithoutReceipt(t *testing.T) {
	ctx := context.Background()
	_, c := fixture(t)
	c.SAdd(ctx, "s:ctl:idx:task:closed", "t1", "t2")
	c.SAdd(ctx, "s:ctl:idx:card:queued", "c1")
	for _, e := range [][]any{
		{"kind", "task", "id", "t1", "from", "open", "to", "working"},
		{"kind", "task", "id", "t1", "from", "working", "to", "closed"},
		{"kind", "task", "id", "t2", "from", "open", "to", "working"},
		{"kind", "card", "id", "c1", "from", "", "to", "queued"},
	} {
		c.XAdd(ctx, &redis.XAddArgs{Stream: "s:ctl:log", Values: e})
	}
	l := snap(t, c).checkReceipts()
	if !l.Red || l.N != "7.10" || !strings.Contains(l.Why, "task t2 closed, receipt working") || strings.Contains(l.Why, "t1") {
		t.Fatalf("t2 without its receipt: %s", l)
	}
	c.XAdd(ctx, &redis.XAddArgs{Stream: "s:ctl:log", Values: []any{"kind", "task", "id", "t2", "from", "working", "to", "closed"}})
	if l := snap(t, c).checkReceipts(); l.Red {
		t.Fatalf("every state has its receipt: %s", l)
	}
	c.SAdd(ctx, "s:ctl:idx:card:dealt", "c9")
	if l := snap(t, c).checkReceipts(); !l.Red || !strings.Contains(l.Why, "card c9 dealt, receipt none") {
		t.Fatalf("no receipt at all: %s", l)
	}
}

// Control 27's preflight half (#2756 2.4, 7.15).
func TestPreflightMachineCeiling(t *testing.T) {
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
	if l := snap(t, c).checkCeiling(); l.Red || l.N != "7.15" {
		t.Fatalf("32+32 under 64: %s", l)
	}
	c.Set(ctx, "friend:ctl-a:slots", "32", 0)
	if l := snap(t, c).checkCeiling(); !l.Red || !strings.Contains(l.Why, "friend:ctl-a:slots") {
		t.Fatalf("interim slots key beside desired: %s", l)
	}
	c.Del(ctx, "friend:ctl-a:slots")
	c.HSet(ctx, "friend:ctl-b:desired", "slots", "33")
	if l := snap(t, c).checkCeiling(); !l.Red || !strings.Contains(l.Why, "ctl-studio 65/64") {
		t.Fatalf("65 over 64: %s", l)
	}
	c.HSet(ctx, "friend:ctl-b:desired", "slots", "32")
	c.HSet(ctx, "bench:ctl-bench:beat", "host", "ctl-other")
	if l := snap(t, c).checkCeiling(); !l.Red || !strings.Contains(l.Why, "bench ctl-bench beats on ctl-other, desired ctl-hulk") {
		t.Fatalf("beat host differs: %s", l)
	}
	c.Del(ctx, "bench:ctl-bench:beat", "machine:ctl-hulk:ceiling")
	if l := snap(t, c).checkCeiling(); !l.Red || !strings.Contains(l.Why, "ctl-hulk has no ceiling") {
		t.Fatalf("missing ceiling: %s", l)
	}
}

// Stella's hold 6 on #3005: a desired machine with no slots field was summed
// as zero width, so the machine read green under its ceiling.
func TestPreflightMachineCeilingMissingSlotsIsRed(t *testing.T) {
	ctx := context.Background()
	_, c := fixture(t)
	c.SAdd(ctx, "friends", "ctl-a", "ctl-b")
	c.HSet(ctx, "machine:ctl-studio:ceiling", "slots", "32")
	c.HSet(ctx, "friend:ctl-a:desired", "slots", "32", "machine", "ctl-studio")
	c.HSet(ctx, "friend:ctl-b:desired", "machine", "ctl-studio")
	if l := snap(t, c).checkCeiling(); !l.Red || !strings.Contains(l.Why, "friend ctl-b has no desired slots on ctl-studio") {
		t.Fatalf("desired machine without slots: %s", l)
	}
	c.HSet(ctx, "friend:ctl-b:desired", "slots", "x")
	if l := snap(t, c).checkCeiling(); !l.Red || !strings.Contains(l.Why, `desired slots "x" is not a number`) {
		t.Fatalf("desired slots not a number: %s", l)
	}
}

func TestStoreChecksPrintEveryCheckAndUnreachableIsRed(t *testing.T) {
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	defer c.Close()
	lines := Run(ctx, c, Options{Sprint: "ctl"})
	var got []string
	for _, l := range lines {
		got = append(got, l.N)
		if !l.Red {
			t.Fatalf("unreachable must print every line RED: %s", l)
		}
	}
	if strings.Join(got, " ") != storeOrder {
		t.Fatalf("checks %v, want %s", got, storeOrder)
	}
	if !strings.Contains(lines[0].Why, "unreachable") {
		t.Fatalf("7.1 unreachable: %s", lines[0])
	}
}

// Check 7.1 needs INFO and FUNCTION, which miniredis does not serve; it runs
// against a throwaway redis-server as the store package's controls do.
func TestPreflightRedisAndLibrary(t *testing.T) {
	ctx := context.Background()
	for _, aof := range []string{"no", "yes"} {
		// The helper starts with --appendonly no; a later argument wins.
		addr := testutil.Start(t, "--appendonly", aof)
		c := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = c.Close() })
		source, err := fn.Source()
		if err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, "proc:acl-test", "result", "ok", "library_sha", fn.Sum(source), "at", fmt.Sprint(time.Now().Unix()))
		red := func(want string) {
			t.Helper()
			if l := readSnapshot(ctx, c, "ctl").checkRedis(); !l.Red || !strings.Contains(l.Why, want) {
				t.Fatalf("want RED %q: %s", want, l)
			}
		}
		if aof == "no" {
			if err := fn.Load(ctx, c); err != nil {
				t.Fatal(err)
			}
			red("AOF off")
			continue
		}
		red("nova_sprint not loaded")
		if err := c.FunctionLoadReplace(ctx, "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'OLD' end)\n").Err(); err != nil {
			t.Fatal(err)
		}
		red("not the binary's version")
		if err := fn.Load(ctx, c); err != nil {
			t.Fatal(err)
		}
		if l := readSnapshot(ctx, c, "ctl").checkRedis(); l.Red {
			t.Fatalf("aof on, library at version, acl receipt ok: %s", l)
		}
		c.HSet(ctx, "proc:acl-test", "library_sha", "0000000000000000")
		red("proc:acl-test library_sha")
	}
}

// fn.TTLAllow is path.Match patterns over the lease keys.
func TestTTLAllow(t *testing.T) {
	for key, want := range map[string]bool{
		"lease:reconciler": true, "lease:harvest:hulk": true, "lease:table:rowan": true, "q:blocked:lock": true,
		"bench:b1:beat": false, "lease:other": false, "friend:f1": false,
	} {
		if got := fn.TTLAllowed(key); got != want {
			t.Errorf("TTLAllowed(%q) = %v, want %v", key, got, want)
		}
	}
}

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
