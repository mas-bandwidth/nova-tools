//go:build functional

package main

// #3048 rev 3: the friend's bus tie is a checked and repaired unit. These
// controls run `friend declare`, `friend wake-health` and the `bench beat`
// repair tick against a throwaway redis-server with the nova_sprint library
// and a fake WakeHost; git runs only on local fixture repos.

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// fakeClone is one clone the fake FetchFF serves: behind counts against the
// remote's tip, never a local @{u}.
type fakeClone struct {
	behind   int
	tip      string
	fetchErr bool // the bounded fetch fails
	ffFails  bool // the fast-forward fails
}

// fakeWake is the WakeHost seam; every call is recorded.
type fakeWake struct {
	mu        sync.Mutex
	loaded    map[string]bool
	files     map[string]bool
	loadFails map[string]int // -1: every load fails
	clones    map[string]*fakeClone
	calls     []string
	refuseAll bool // fail on any call (the read-only control)
}

func newFakeWake() *fakeWake {
	return &fakeWake{loaded: map[string]bool{}, files: map[string]bool{},
		loadFails: map[string]int{}, clones: map[string]*fakeClone{}}
}

func (f *fakeWake) note(c string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if f.refuseAll {
		return errors.New("the read-only path reached the host: " + c)
	}
	return nil
}

func (f *fakeWake) UnitLoaded(_ context.Context, unit string) (bool, error) {
	if err := f.note("loaded? " + unit); err != nil {
		return false, err
	}
	return f.loaded[unit], nil
}

func (f *fakeWake) UnitFileExists(_ context.Context, file string) (bool, error) {
	if err := f.note("file? " + file); err != nil {
		return false, err
	}
	return f.files[file], nil
}

func (f *fakeWake) LoadUnit(_ context.Context, unit, _ string) error {
	if err := f.note("load " + unit); err != nil {
		return err
	}
	switch n := f.loadFails[unit]; {
	case n < 0:
		return errors.New("Bootstrap failed: 5: Input/output error")
	case n > 0:
		f.loadFails[unit] = n - 1
		return errors.New("Bootstrap failed: 5: Input/output error")
	}
	f.loaded[unit] = true
	return nil
}

func (f *fakeWake) FetchFF(_ context.Context, dir, remote, branch string, bound time.Duration) (life.FetchFF, error) {
	if err := f.note("fetch " + dir + " " + remote + " " + branch); err != nil {
		return life.FetchFF{}, err
	}
	c := f.clones[dir]
	if c == nil {
		return life.FetchFF{}, fmt.Errorf("%w: no fixture clone %s", life.ErrFetchFailed, dir)
	}
	if c.fetchErr {
		return life.FetchFF{}, fmt.Errorf("%w: git fetch timed out after %s", life.ErrFetchFailed, bound)
	}
	r := life.FetchFF{Fetched: c.tip, Before: c.behind}
	if c.behind == 0 {
		return r, nil
	}
	if c.ffFails {
		r.After = c.behind
		return r, errors.New("merge --ff-only: Not possible to fast-forward")
	}
	_ = f.note("ff " + dir)
	c.behind = 0
	return r, nil
}

// callsFor is every recorded call naming friend f.
func (f *fakeWake) callsFor(friend string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.Contains(c, "/"+friend+"/") || strings.HasSuffix(c, "-"+friend) || strings.Contains(c, "-"+friend+" ") {
			out = append(out, c)
		}
	}
	return out
}

func useFakeWake(t *testing.T, f *fakeWake) {
	t.Helper()
	prev := newWakeHost
	newWakeHost = func() life.WakeHost { return f }
	t.Cleanup(func() { newWakeHost = prev })
}

func tipOf(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// fleetRepo is a committed fixture fleet repo: group_vars/all.yml in a local
// git checkout. commit writes the file and commits it, returning the rev.
type fleetRepo struct {
	t    *testing.T
	dir  string
	file string
}

func newFleetRepo(t *testing.T) *fleetRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "fleet")
	r := &fleetRepo{t: t, dir: dir, file: filepath.Join(dir, "group_vars", "all.yml")}
	r.git("init", "-q", dir)
	return r
}

func (r *fleetRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+filepath.Dir(r.dir))
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *fleetRepo) write(body string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(r.file), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(r.file, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *fleetRepo) commit(body string) string {
	r.t.Helper()
	r.write(body)
	r.git("-C", r.dir, "add", "-A")
	r.git("-C", r.dir, "commit", "-q", "-m", "fleet")
	return r.git("-C", r.dir, "rev-parse", "HEAD")
}

// unitEntry is one `wake: unit` friend on host, with a keeper when keeper.
func unitEntry(f, host string, keeper bool) string {
	s := fmt.Sprintf("  %s:\n    machine: studio\n    slots: 4\n    wake: unit\n    host: %s\n"+
		"    unit: com.nova.loop.wake-serve-%s\n    unit_file: /fixture/%s/wake.plist\n"+
		"    wake_bus: /fixture/%s/bus\n    wake_bus_remote: file:///fixture/remotes/%s-bus.git\n"+
		"    wake_bus_branch: main\n    wake_on_note: \"To: %s\"\n", f, host, f, f, f, f, f)
	if keeper {
		s += fmt.Sprintf("    keeper_unit: com.nova.loop.bus-keeper-%s\n    keeper_file: /fixture/%s/keeper.plist\n"+
			"    keeper_bus: /fixture/%s/keeper\n    keeper_remote: file:///fixture/remotes/%s-keeper.git\n"+
			"    keeper_branch: main\n", f, f, f, f)
	}
	return s
}

func wakeUnit(f string) string   { return "com.nova.loop.wake-serve-" + f }
func keeperUnit(f string) string { return "com.nova.loop.bus-keeper-" + f }
func busDir(f string) string     { return "/fixture/" + f + "/bus" }
func keeperDir(f string) string  { return "/fixture/" + f + "/keeper" }

func wakeFixture(t *testing.T) (string, *redis.Client, *store.Store) {
	t.Helper()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return addr, c, store.New(c)
}

func friendVerb(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(append([]string{"friend"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// named is the friend list of the WAKE summary line.
func named(t *testing.T, out string) []string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "WAKE ") {
			f := strings.Fields(l)
			if len(f) < 4 {
				return nil
			}
			n := strings.Split(f[3], ",")
			sort.Strings(n)
			return n
		}
	}
	t.Fatalf("no WAKE line in %q", out)
	return nil
}

func preflightNamed(t *testing.T, c *redis.Client) (preflight.FleetLine, []string) {
	t.Helper()
	w, err := gatherWake(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	l := preflight.CheckFriendWake(preflight.FleetInput{Loaded: preflight.Loaded{Wake: true}, Wake: w})
	var n []string
	for _, x := range w.Named {
		n = append(n, x.Friend)
	}
	sort.Strings(n)
	return l, n
}

// capCount counts cap:log entries of kind for subject.
func capCount(t *testing.T, c *redis.Client, kind, subject string) int {
	t.Helper()
	es, err := c.XRange(context.Background(), "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range es {
		if e.Values["kind"] == kind && e.Values["subject"] == subject {
			n++
		}
	}
	return n
}

// TestAcceptance3048 is the DONE-WHEN: eleven friends, one per case, declared
// from a committed fixture fleet repo onto a fake WakeHost, then two bench
// beat repair ticks on the fixture host.
func TestAcceptance3048(t *testing.T) {
	const host = "fixture"
	addr, c, st := wakeFixture(t)
	ctx := context.Background()
	fake := newFakeWake()
	useFakeWake(t, fake)

	units := []string{"unit_booted_out", "bus_behind_3", "upstream_stale_3", "fetch_fails",
		"unit_fails_twice", "beat_expired", "boot_silent"}
	keepers := []string{"keeper_behind_2", "keeper_stuck_2"}
	var body strings.Builder
	body.WriteString("fleet_name: fixture\nfriends:\n")
	for _, f := range units {
		body.WriteString(unitEntry(f, host, false))
	}
	for _, f := range keepers {
		body.WriteString(unitEntry(f, host, true))
	}
	body.WriteString("  human_notify: { wake: human, notify: glenn-sms }\n")
	repo := newFleetRepo(t)
	rev := repo.commit(body.String())

	// The fake host: every unit file on disk, every clone current, unless the case says otherwise.
	for _, f := range append(units, keepers...) {
		fake.files["/fixture/"+f+"/wake.plist"] = true
		fake.loaded[wakeUnit(f)] = true
		fake.clones[busDir(f)] = &fakeClone{tip: tipOf(f + "-bus")}
	}
	for _, f := range keepers {
		fake.files["/fixture/"+f+"/keeper.plist"] = true
		fake.loaded[keeperUnit(f)] = true
		fake.clones[keeperDir(f)] = &fakeClone{tip: tipOf(f + "-keeper"), behind: 2}
	}
	fake.loaded[wakeUnit("unit_booted_out")] = false
	fake.clones[busDir("bus_behind_3")].behind = 3
	fake.clones[busDir("upstream_stale_3")].behind = 3 // the remote is 3 ahead; the local @{u} says 0
	fake.clones[busDir("fetch_fails")].fetchErr = true
	fake.loaded[wakeUnit("unit_fails_twice")] = false
	fake.loadFails[wakeUnit("unit_fails_twice")] = -1
	fake.clones[keeperDir("keeper_stuck_2")].ffFails = true
	fake.loaded[wakeUnit("boot_silent")] = false

	for _, f := range []string{"unit_booted_out", "bus_behind_3", "upstream_stale_3", "fetch_fails",
		"unit_fails_twice", "keeper_behind_2", "keeper_stuck_2"} {
		if err := c.HSet(ctx, "friend:"+f+":beat", "harness", "claude", "host", host, "at", "1").Err(); err != nil {
			t.Fatal(err)
		}
	}
	// hello_only is registered (the friends set hello and capacity keep) and never declared.
	if err := c.SAdd(ctx, "friends", "hello_only").Err(); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file)
	if code != 0 || out != fmt.Sprintf("declared 10 unit=9 human=1 removed=0 rev=%s\n", rev[:8]) {
		t.Fatalf("declare: %d %q %q", code, out, errOut)
	}
	code, out, _ = friendVerb("wake-health", "--redis", addr, "--all")
	if code != 1 || len(named(t, out)) != 10 || strings.Contains(strings.Join(named(t, out), ","), "human_notify") {
		t.Fatalf("before any tick wake-health --all must exit 1 naming every friend but the human one: %d %q", code, out)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("declare and the read-only wake-health touched the host: %v", fake.calls)
	}

	type snap struct {
		health map[string]string
		repair int
		down   int
		calls  []string
	}
	all := append(append(append([]string{}, units...), keepers...), "human_notify", "hello_only")
	take := func() map[string]snap {
		m := map[string]snap{}
		for _, f := range all {
			m[f] = snap{c.HGetAll(ctx, life.WakeHealthKey(f)).Val(), capCount(t, c, "wake-repair", f),
				capCount(t, c, "wake-down", f), fake.callsFor(f)}
		}
		return m
	}

	var errBuf bytes.Buffer
	benchWakeRepair(ctx, st, host, "bench-session", &errBuf)
	if errBuf.Len() != 0 {
		t.Fatalf("tick 1: %s", errBuf.String())
	}
	t1 := take()
	code, out1, _ := friendVerb("wake-health", "--redis", addr, "--all")
	gate1 := []string{"beat_expired", "fetch_fails", "hello_only", "keeper_stuck_2", "unit_fails_twice"}
	if code != 1 || strings.Join(named(t, out1), ",") != strings.Join(gate1, ",") {
		t.Errorf("after tick 1 wake-health --all: exit %d named %v, want 1 naming %v\n%s", code, named(t, out1), gate1, plain(out1))
	}
	if l, n := preflightNamed(t, c); !l.Red || strings.Join(n, ",") != strings.Join(gate1, ",") {
		t.Errorf("after tick 1 preflight 7.18: %s names %v, want RED naming %v", l, n, gate1)
	}

	benchWakeRepair(ctx, st, host, "bench-session", &errBuf)
	if errBuf.Len() != 0 {
		t.Fatalf("tick 2: %s", errBuf.String())
	}
	t2 := take()
	code, out2, _ := friendVerb("wake-health", "--redis", addr, "--all")
	gate2 := []string{"beat_expired", "boot_silent", "fetch_fails", "hello_only", "keeper_stuck_2", "unit_fails_twice"}
	if code != 1 || strings.Join(named(t, out2), ",") != strings.Join(gate2, ",") {
		t.Errorf("after tick 2 wake-health --all: exit %d named %v, want 1 naming %v\n%s", code, named(t, out2), gate2, plain(out2))
	}
	if l, n := preflightNamed(t, c); !l.Red || strings.Join(n, ",") != strings.Join(gate2, ",") {
		t.Errorf("after tick 2 preflight 7.18: %s names %v, want RED naming %v", l, n, gate2)
	}
	// Down stays quiet: tick 2 writes no second wake-down for a friend already down.
	for _, f := range gate1 {
		if f != "hello_only" && (t1[f].down != 1 || t2[f].down != 1) {
			t.Errorf("%s wake-down after tick 1 = %d, after tick 2 = %d; want 1 and 1", f, t1[f].down, t2[f].down)
		}
	}

	row := func(s snap) string { return plain(s.health["row"]) }
	has := func(t *testing.T, s snap, parts ...string) {
		t.Helper()
		for _, p := range parts {
			if !strings.Contains(row(s), p) {
				t.Errorf("row %q lacks %q", row(s), p)
			}
		}
	}
	t.Run("unit_booted_out", func(t *testing.T) {
		s := t1["unit_booted_out"]
		has(t, s, "wake: repaired "+wakeUnit("unit_booted_out"))
		if s.repair != 1 || s.health["state"] != life.WakeRepaired {
			t.Errorf("wake-repair receipts %d state %q; want exactly 1, repaired", s.repair, s.health["state"])
		}
	})
	t.Run("bus_behind_3", func(t *testing.T) {
		s := t1["bus_behind_3"]
		has(t, s, "wake: repaired "+busDir("bus_behind_3"), "behind=3->0")
		if s.health["behind"] != "0" || s.health["fetched_bus"] != tipOf("bus_behind_3-bus") {
			t.Errorf("behind %q fetched_bus %q; want 0 and the fetched id", s.health["behind"], s.health["fetched_bus"])
		}
	})
	t.Run("upstream_stale_3", func(t *testing.T) {
		s, tip := t1["upstream_stale_3"], tipOf("upstream_stale_3-bus")
		has(t, s, "wake: repaired "+busDir("upstream_stale_3"), "behind=3->0 fetched="+tip[:8])
		if s.health["fetched_bus"] != tip || s.health["behind"] != "0" || s.repair != 1 {
			t.Errorf("fetched_bus %q behind %q repairs %d; want the remote tip, 0, 1", s.health["fetched_bus"], s.health["behind"], s.repair)
		}
	})
	t.Run("fetch_fails", func(t *testing.T) {
		s := t1["fetch_fails"]
		has(t, s, "wake: down "+wakeUnit("fetch_fails")+" wake:fetch-failed behind=?")
		if s.health["behind"] != "?" || s.down != 1 {
			t.Errorf("behind %q wake-down %d; want ? and 1", s.health["behind"], s.down)
		}
		for _, call := range s.calls {
			if strings.HasPrefix(call, "ff ") {
				t.Errorf("a failed fetch fast-forwarded: %v", s.calls)
			}
		}
	})
	t.Run("unit_fails_twice", func(t *testing.T) {
		s := t1["unit_fails_twice"]
		has(t, s, "wake: down "+wakeUnit("unit_fails_twice"))
		if !strings.HasPrefix(s.health["row"], "\x1b[31mwake: down") || s.down != 1 || s.health["attempts"] != "2" {
			t.Errorf("row %q wake-down %d attempts %q; want red, 1, 2", s.health["row"], s.down, s.health["attempts"])
		}
	})
	t.Run("human_notify", func(t *testing.T) {
		if !strings.Contains(plain(out1), "human_notify wake: human notify=glenn-sms\n") {
			t.Errorf("wake-health lacks `wake: human notify=glenn-sms`:\n%s", plain(out1))
		}
		if len(t2["human_notify"].calls) != 0 {
			t.Errorf("a human friend touched the host: %v", t2["human_notify"].calls)
		}
	})
	t.Run("hello_only", func(t *testing.T) {
		if !strings.Contains(plain(out1), "hello_only wake: undeclared\n") {
			t.Errorf("wake-health lacks `hello_only wake: undeclared`:\n%s", plain(out1))
		}
	})
	t.Run("beat_expired", func(t *testing.T) {
		s := t1["beat_expired"]
		has(t, s, "wake: down "+wakeUnit("beat_expired")+" behind=0 beat=stale")
		for _, call := range s.calls {
			if strings.HasPrefix(call, "load ") || strings.HasPrefix(call, "fetch ") {
				t.Errorf("a stale beat on a loaded unit got a repair call: %v", s.calls)
			}
		}
		es, _ := c.XRange(ctx, "cap:log", "-", "+").Result()
		var detail []string
		for _, e := range es {
			if e.Values["kind"] == "wake-down" && e.Values["subject"] == "beat_expired" {
				detail = append(detail, fmt.Sprint(e.Values["reason"]))
			}
		}
		if len(detail) != 1 || detail[0] != "beat older than its TTL" {
			t.Errorf("wake-down receipts %q; want one with detail `beat older than its TTL`", detail)
		}
	})
	t.Run("keeper_behind_2", func(t *testing.T) {
		s := t1["keeper_behind_2"]
		has(t, s, "wake: repaired "+keeperDir("keeper_behind_2"), "wake:bus-behind", "behind=2->0")
		if s.repair != 1 || s.health["fetched_keeper"] != tipOf("keeper_behind_2-keeper") {
			t.Errorf("wake-repair %d fetched_keeper %q; want 1 and the keeper tip", s.repair, s.health["fetched_keeper"])
		}
	})
	t.Run("keeper_stuck_2", func(t *testing.T) {
		s := t1["keeper_stuck_2"]
		has(t, s, "wake: down "+wakeUnit("keeper_stuck_2")+" wake:bus-behind behind=2")
		if s.down != 1 {
			t.Errorf("wake-down %d, want 1", s.down)
		}
	})
	t.Run("boot_silent", func(t *testing.T) {
		s1, s2 := t1["boot_silent"], t2["boot_silent"]
		has(t, s1, "wake: repaired "+wakeUnit("boot_silent"))
		has(t, s2, "wake: down "+wakeUnit("boot_silent")+" behind=0 beat=stale")
		if s1.down != 0 || s2.down != 1 {
			t.Errorf("wake-down after tick 1 = %d, after tick 2 = %d; want 0 then 1", s1.down, s2.down)
		}
	})
}

// declareRefuses commits body, runs declare, and wants exit 2 naming want
// with zero writes.
func declareRefuses(t *testing.T, body, want string) {
	t.Helper()
	addr, c, _ := wakeFixture(t)
	repo := newFleetRepo(t)
	repo.commit("friends:\n" + body)
	code, out, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file)
	if code != 2 || !strings.Contains(errOut, want) {
		t.Fatalf("declare: exit %d %q %q; want 2 naming %q", code, out, errOut, want)
	}
	if n := c.DBSize(context.Background()).Val(); n != 0 {
		t.Fatalf("a refused declare wrote %d keys", n)
	}
}

func TestDeclareRefusesUnitWithoutBus(t *testing.T) {
	t.Parallel()

	declareRefuses(t, "  walter: { wake: unit, host: h, wake_bus_remote: r, wake_bus_branch: main, wake_on_note: x }\n", "walter wake_bus")
}

func TestDeclareRefusesUnitWithoutRemote(t *testing.T) {
	t.Parallel()

	declareRefuses(t, "  walter: { wake: unit, host: h, wake_bus: /b, wake_bus_branch: main, wake_on_note: x }\n", "walter wake_bus_remote")
}

func TestDeclareRefusesHumanWithoutNotify(t *testing.T) {
	t.Parallel()

	declareRefuses(t, "  stella: { wake: human }\n", "stella notify")
}

func TestDeclareRefusesKeeperWithoutBus(t *testing.T) {
	t.Parallel()

	declareRefuses(t, "  walter: { wake: unit, host: h, wake_bus: /b, wake_bus_remote: r, wake_bus_branch: main, wake_on_note: x, keeper_unit: k, keeper_remote: r, keeper_branch: main }\n", "walter keeper_bus")
}

func TestDeclareRefusesDirtyFile(t *testing.T) {
	t.Parallel()

	addr, c, _ := wakeFixture(t)
	repo := newFleetRepo(t)
	repo.commit("friends:\n  stella: { wake: human, notify: glenn }\n")
	repo.write("friends:\n  stella: { wake: human, notify: someone-else }\n")
	code, _, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file)
	if code != 2 || !strings.Contains(errOut, "dirty") {
		t.Fatalf("dirty file: exit %d %q; want 2 dirty", code, errOut)
	}
	if n := c.DBSize(context.Background()).Val(); n != 0 {
		t.Fatalf("a dirty declare wrote %d keys", n)
	}
}

func TestDeclareRemovesDroppedFriend(t *testing.T) {
	t.Parallel()

	addr, c, _ := wakeFixture(t)
	ctx := context.Background()
	repo := newFleetRepo(t)
	repo.commit("friends:\n" + unitEntry("a", "h", false) + unitEntry("b", "h", false) + unitEntry("c", "h", false))
	if code, out, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file); code != 0 {
		t.Fatalf("declare r1: %d %q %q", code, out, errOut)
	}
	r2 := repo.commit("friends:\n" + unitEntry("a", "h", false) + unitEntry("b", "h", false))
	code, out, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file)
	if code != 0 || out != "declared 2 unit=2 human=0 removed=1 rev="+r2[:8]+"\n" {
		t.Fatalf("declare r2: %d %q %q", code, out, errOut)
	}
	if got := c.SMembers(ctx, "friends:declared").Val(); len(got) != 2 || c.SIsMember(ctx, "friends:declared", "c").Val() {
		t.Fatalf("friends:declared = %v, want a and b", got)
	}
	if c.Exists(ctx, life.WakePathKey("c")).Val() != 0 {
		t.Fatal("the dropped friend's wakepath is still there")
	}
	es, _ := c.XRange(ctx, "cap:log", "-", "+").Result()
	var undeclared []string
	for _, e := range es {
		if e.Values["kind"] == "friend-undeclared" {
			undeclared = append(undeclared, fmt.Sprint(e.Values["subject"], " ", e.Values["reason"]))
		}
	}
	if len(undeclared) != 1 || undeclared[0] != "c rev="+r2 {
		t.Fatalf("friend-undeclared receipts %q, want [c rev=%s]", undeclared, r2)
	}
}

// registryDump is every registry value byte for byte.
func registryDump(t *testing.T, c *redis.Client) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder
	decl := c.HGetAll(ctx, "friends:decl").Val()
	names := c.SMembers(ctx, "friends:declared").Val()
	sort.Strings(names)
	fmt.Fprintf(&b, "decl=%v declared=%v\n", decl, names)
	for _, n := range append(names, "a", "b", "c", "d") {
		fmt.Fprintf(&b, "%s=%v\n", n, c.HGetAll(ctx, life.WakePathKey(n)).Val())
	}
	return b.String()
}

// TestDeclareStaleCheckoutRefused is the two-declarer control: checkout B at
// r2 (a child of r1) declares after the registry holds r1; checkout A, still
// at r1, then declares and is refused stale with zero writes and no false
// receipt.
func TestDeclareStaleCheckoutRefused(t *testing.T) {
	t.Parallel()

	addr, c, _ := wakeFixture(t)
	ctx := context.Background()
	b := newFleetRepo(t)
	r1 := b.commit("friends:\n" + unitEntry("a", "h", false) + unitEntry("b", "h", false) + unitEntry("c", "h", false))
	aDir := filepath.Join(t.TempDir(), "a")
	b.git("clone", "-q", b.dir, aDir)
	aFile := filepath.Join(aDir, "group_vars", "all.yml")
	if code, out, errOut := friendVerb("declare", "--redis", addr, "--from", aFile); code != 0 || !strings.Contains(out, "rev="+r1[:8]) {
		t.Fatalf("A declares r1: %d %q %q", code, out, errOut)
	}
	r2 := b.commit("friends:\n" + unitEntry("a", "h", false) + unitEntry("b", "h", false) + unitEntry("d", "h", false))
	code, out, errOut := friendVerb("declare", "--redis", addr, "--from", b.file)
	if code != 0 || out != "declared 3 unit=3 human=0 removed=1 rev="+r2[:8]+"\n" {
		t.Fatalf("B declares r2: %d %q %q", code, out, errOut)
	}
	if capCount(t, c, "friend-undeclared", "c") != 1 || capCount(t, c, "friend-declared", "d") != 1 {
		t.Fatal("B's declare must write one friend-undeclared c and one friend-declared d")
	}
	before, logBefore := registryDump(t, c), c.XLen(ctx, "cap:log").Val()
	code, out, errOut = friendVerb("declare", "--redis", addr, "--from", aFile)
	if code != 3 || !strings.Contains(errOut, "stale: registry at "+r2[:8]+", this checkout at "+r1[:8]) {
		t.Fatalf("A at r1 after B: exit %d %q %q; want 3 stale", code, out, errOut)
	}
	if after := registryDump(t, c); after != before {
		t.Fatalf("a stale declare changed the registry:\nbefore %s\nafter  %s", before, after)
	}
	if n := c.XLen(ctx, "cap:log").Val(); n != logBefore {
		t.Fatalf("a stale declare added %d cap:log entries", n-logBefore)
	}
}

// TestWakeHealthReadOnlyTouchesNoHost: without --repair the verb reads Redis
// only; the fake WakeHost fails on any call.
func TestWakeHealthReadOnlyTouchesNoHost(t *testing.T) {
	addr, _, _ := wakeFixture(t)
	fake := newFakeWake()
	fake.refuseAll = true
	useFakeWake(t, fake)
	repo := newFleetRepo(t)
	repo.commit("friends:\n" + unitEntry("walter", "fixture", true))
	if code, _, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file); code != 0 {
		t.Fatal(errOut)
	}
	for _, args := range [][]string{{"--all"}, {"--as", "walter"}} {
		code, out, _ := friendVerb(append([]string{"wake-health", "--redis", addr}, args...)...)
		if code != 1 || !strings.Contains(plain(out), "walter wake: ?") {
			t.Errorf("wake-health %v: %d %q; want exit 1 with `wake: ?`", args, code, out)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("the read-only path called the host: %v", fake.calls)
	}
}

// TestRepairRefusesOtherHost: a friend declared on another host is refused
// by --repair --as, and nothing on this host is touched.
func TestRepairRefusesOtherHost(t *testing.T) {
	addr, c, _ := wakeFixture(t)
	fake := newFakeWake()
	useFakeWake(t, fake)
	repo := newFleetRepo(t)
	repo.commit("friends:\n" + unitEntry("walter", "elsewhere", false))
	if code, _, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file); code != 0 {
		t.Fatal(errOut)
	}
	code, out, errOut := friendVerb("wake-health", "--redis", addr, "--as", "walter", "--repair", "--host", "fixture")
	if code != 1 || !strings.Contains(errOut, "walter is declared on elsewhere, not fixture") {
		t.Fatalf("repair of another host's friend: %d %q %q; want 1 refused", code, out, errOut)
	}
	if len(fake.calls) != 0 || c.Exists(context.Background(), life.WakeHealthKey("walter")).Val() != 0 {
		t.Fatalf("a refused repair touched the host %v or wrote a wake cell", fake.calls)
	}
	// --all on this host skips it: nothing is declared here.
	code, out, _ = friendVerb("wake-health", "--redis", addr, "--all", "--repair", "--host", "fixture")
	if !strings.Contains(out, "REPAIRED host=fixture checked=0 held=0") || len(fake.calls) != 0 {
		t.Fatalf("--all --repair on a host with no friends: %d %q calls %v", code, out, fake.calls)
	}
}

// TestRepairLockOneRepairer: while another session holds a friend's repair
// lock, a second repairer skips the friend with no host call.
func TestRepairLockOneRepairer(t *testing.T) {
	addr, c, _ := wakeFixture(t)
	ctx := context.Background()
	fake := newFakeWake()
	useFakeWake(t, fake)
	repo := newFleetRepo(t)
	repo.commit("friends:\n" + unitEntry("walter", "fixture", false))
	if code, _, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file); code != 0 {
		t.Fatal(errOut)
	}
	if err := c.Set(ctx, "friend:walter:wakerepair", "other-session", 30*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	_, out, _ := friendVerb("wake-health", "--redis", addr, "--all", "--repair", "--host", "fixture", "--session", "me")
	if !strings.Contains(out, "walter wake: repair held by other-session") || !strings.Contains(out, "checked=0 held=1") {
		t.Fatalf("second repairer: %q", out)
	}
	if len(fake.calls) != 0 || c.Get(ctx, "friend:walter:wakerepair").Val() != "other-session" {
		t.Fatalf("the second repairer called the host %v or took the lock", fake.calls)
	}
	// Once the holder is gone the repairer runs and releases its own lock.
	c.Del(ctx, "friend:walter:wakerepair")
	fake.loaded[wakeUnit("walter")] = true
	fake.clones[busDir("walter")] = &fakeClone{tip: tipOf("w")}
	_, out, _ = friendVerb("wake-health", "--redis", addr, "--all", "--repair", "--host", "fixture", "--session", "me")
	if !strings.Contains(out, "checked=1 held=0") || c.Exists(ctx, "friend:walter:wakerepair").Val() != 0 {
		t.Fatalf("free lock: %q, lock left %d", out, c.Exists(ctx, "friend:walter:wakerepair").Val())
	}
}

// TestWakeHealthStaleIsQuestionMark: a wake cell no tick wrote in the last
// 30 s prints `wake: ?`, never its carried-over row, and the gate names it.
func TestWakeHealthStaleIsQuestionMark(t *testing.T) {
	t.Parallel()

	addr, c, _ := wakeFixture(t)
	ctx := context.Background()
	repo := newFleetRepo(t)
	repo.commit("friends:\n" + unitEntry("walter", "fixture", false))
	if code, _, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file); code != 0 {
		t.Fatal(errOut)
	}
	old := time.Now().Add(-31 * time.Second).UnixMilli()
	c.HSet(ctx, life.WakeHealthKey("walter"), "state", "ok", "row", "wake: ok carried-over", "at", old)
	code, out, _ := friendVerb("wake-health", "--redis", addr, "--as", "walter")
	if code != 1 || plain(out) != "walter wake: ?\nWAKE friends=1 named=1 walter\n" {
		t.Fatalf("stale cell: %d %q", code, out)
	}
	c.HSet(ctx, life.WakeHealthKey("walter"), "at", time.Now().UnixMilli())
	if code, out, _ := friendVerb("wake-health", "--redis", addr, "--as", "walter"); code != 0 || !strings.Contains(out, "wake: ok carried-over") {
		t.Fatalf("fresh cell: %d %q", code, out)
	}
}

// TestPreflight718RedOnUndeclaredAndMissing: with no friends:decl and a
// registered friend with no wakepath, 7.18 is RED naming both.
func TestPreflight718RedOnUndeclaredAndMissing(t *testing.T) {
	t.Parallel()

	_, c, _ := wakeFixture(t)
	c.SAdd(context.Background(), "friends", "walter")
	l, n := preflightNamed(t, c)
	if !l.Red || !strings.Contains(l.String(), "friends:decl absent (MISSING)") || !strings.Contains(l.String(), "walter undeclared") || strings.Join(n, ",") != "walter" {
		t.Fatalf("7.18 = %s named %v", l, n)
	}
	if l := preflight.CheckFriendWake(preflight.FleetInput{}); !l.Red || !strings.Contains(l.String(), "friend wake paths unread (MISSING)") {
		t.Fatalf("unread 7.18 = %s", l)
	}
}

// TestPreflight718RedOnBeatStale: a loaded unit whose beat is gone is down
// after the repair tick, and 7.18 names it.
func TestPreflight718RedOnBeatStale(t *testing.T) {
	addr, c, st := wakeFixture(t)
	fake := newFakeWake()
	useFakeWake(t, fake)
	fake.loaded[wakeUnit("walter")] = true
	fake.clones[busDir("walter")] = &fakeClone{tip: tipOf("w")}
	repo := newFleetRepo(t)
	repo.commit("friends:\n" + unitEntry("walter", "fixture", false))
	if code, _, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file); code != 0 {
		t.Fatal(errOut)
	}
	var errBuf bytes.Buffer
	benchWakeRepair(context.Background(), st, "fixture", "s", &errBuf)
	l, n := preflightNamed(t, c)
	if !l.Red || !strings.Contains(l.String(), "walter down") || strings.Join(n, ",") != "walter" {
		t.Fatalf("7.18 = %s named %v (%s)", l, n, errBuf.String())
	}
}

// TestDeclareCheckVerbReadsOnly: --check prints the diff and the stored rev
// and writes nothing.
func TestDeclareCheckVerbReadsOnly(t *testing.T) {
	t.Parallel()

	addr, c, _ := wakeFixture(t)
	repo := newFleetRepo(t)
	rev := repo.commit("friends:\n  stella: { wake: human, notify: glenn }\n")
	code, out, errOut := friendVerb("declare", "--redis", addr, "--from", repo.file, "--check")
	if code != 0 || out != "+ stella\ncheck registry=- file="+rev[:8]+" add=1 change=0 remove=0\n" {
		t.Fatalf("check: %d %q %q", code, out, errOut)
	}
	if n := c.DBSize(context.Background()).Val(); n != 0 {
		t.Fatalf("--check wrote %d keys", n)
	}
	if code, out, _ := friendVerb("declare", "--redis", addr, "--from", repo.file); code != 0 {
		t.Fatal(out)
	}
	if code, out, _ := friendVerb("declare", "--redis", addr, "--from", repo.file); code != 0 || out != "unchanged "+rev[:8]+"\n" {
		t.Fatalf("second declare: %d %q, want unchanged", code, out)
	}
}
