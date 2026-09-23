package life_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// fakeWakeHost is the seam wakehealth.go takes: the units a supervisor has
// loaded, the unit files on disk, how many loads of a unit fail before one
// works, and each clone's commits behind. Every mutating call is recorded, so
// a control can say "one bootstrap happened", not just "the row looks right".
type fakeWakeHost struct {
	loaded    map[string]bool
	files     map[string]bool
	loadFails map[string]int // -1: every load fails
	behind    map[string]int
	calls     []string
}

func newFakeWakeHost() *fakeWakeHost {
	return &fakeWakeHost{loaded: map[string]bool{}, files: map[string]bool{},
		loadFails: map[string]int{}, behind: map[string]int{}}
}

func (f *fakeWakeHost) UnitLoaded(_ context.Context, unit string) (bool, error) {
	return f.loaded[unit], nil
}

func (f *fakeWakeHost) UnitFileExists(_ context.Context, file string) (bool, error) {
	return f.files[file], nil
}

func (f *fakeWakeHost) LoadUnit(_ context.Context, unit, file string) error {
	f.calls = append(f.calls, "load "+unit+" "+file)
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

func (f *fakeWakeHost) Behind(_ context.Context, dir string) (int, error) {
	return f.behind[dir], nil
}

func (f *fakeWakeHost) Pull(_ context.Context, dir string) error {
	f.calls = append(f.calls, "pull "+dir)
	f.behind[dir] = 0
	return nil
}

func beatsLive(live bool) life.BeatReader {
	return func(context.Context, string) (bool, error) { return live, nil }
}

const walterUnit = "com.nova.loop.wake-serve-walter"

func walterDecl() life.WakeDecl {
	return life.WakeDecl{
		Friend:   "walter",
		Mode:     life.WakeUnit,
		Unit:     walterUnit,
		UnitFile: "/fixture/LaunchAgents/" + walterUnit + ".plist",
		Bus:      "/fixture/wake/walter/bus",
	}
}

// TestWakeHealthRepairsUnloadedUnit is the #3048 control: the fixture friend's
// wake unit file exists but the supervisor does not have it (booted out, as
// com.nova.loop.wake-serve-johnny sat for six hours on 2026-09-23). One tick
// finds wake:unit-missing, bootstraps the unit exactly once, and the row says
// `wake: repaired <unit>` with the finding beside it.
func TestWakeHealthRepairsUnloadedUnit(t *testing.T) {
	host := newFakeWakeHost()
	d := walterDecl()
	host.files[d.UnitFile] = true

	got := life.WakeTick(context.Background(), host, beatsLive(true), []life.WakeDecl{d})
	if len(got) != 1 {
		t.Fatalf("one friend in, %d health rows out", len(got))
	}
	h := got[0]
	row := h.Row()
	if !strings.Contains(row, "wake:unit-missing") {
		t.Errorf("row %q does not carry the finding wake:unit-missing", row)
	}
	if !strings.Contains(row, "wake: repaired "+walterUnit) {
		t.Errorf("row %q does not say wake: repaired %s", row, walterUnit)
	}
	if h.State != life.WakeRepaired {
		t.Errorf("state %q, want %q", h.State, life.WakeRepaired)
	}
	want := []string{"load " + walterUnit + " " + d.UnitFile}
	if strings.Join(host.calls, "|") != strings.Join(want, "|") {
		t.Errorf("repair calls %q, want exactly %q", host.calls, want)
	}
	if len(h.Repairs) != 1 || h.Repairs[0].Action != "bootstrap" || h.Repairs[0].Target != walterUnit || h.Repairs[0].Err != "" {
		t.Errorf("recorded repairs %+v, want one clean bootstrap of %s", h.Repairs, walterUnit)
	}
	if !host.loaded[walterUnit] {
		t.Errorf("after the tick the unit is still not loaded")
	}
}

// TestWakeHealthLoadedUnitIsOK is the positive half: a loaded unit, a current
// clone and a live beat print `wake: ok` and make no repair call at all.
func TestWakeHealthLoadedUnitIsOK(t *testing.T) {
	host := newFakeWakeHost()
	d := walterDecl()
	host.files[d.UnitFile] = true
	host.loaded[walterUnit] = true

	h := life.CheckWake(context.Background(), host, beatsLive(true), d)
	if h.State != life.WakeOK || len(h.Findings) != 0 || len(host.calls) != 0 {
		t.Fatalf("healthy friend: state %q findings %v calls %v; want ok, none, none", h.State, h.Findings, host.calls)
	}
	if row := h.Row(); !strings.HasPrefix(row, "wake: ok "+walterUnit) || !strings.Contains(row, "behind=0") || !strings.Contains(row, "beat=live") {
		t.Errorf("healthy row %q", row)
	}
}

// TestWakeHealthPullsBehindBus: the wake server's bus clone is 3 commits
// behind; one tick pulls it and the row prints behind=0 with the finding.
func TestWakeHealthPullsBehindBus(t *testing.T) {
	host := newFakeWakeHost()
	d := walterDecl()
	host.files[d.UnitFile] = true
	host.loaded[walterUnit] = true
	host.behind[d.Bus] = 3

	h := life.CheckWake(context.Background(), host, beatsLive(true), d)
	if h.BehindBefore != 3 || h.Behind != 0 {
		t.Fatalf("behind before %d after %d, want 3 then 0", h.BehindBefore, h.Behind)
	}
	if got := strings.Join(host.calls, "|"); got != "pull "+d.Bus {
		t.Errorf("calls %q, want one pull of %s", got, d.Bus)
	}
	row := h.Row()
	if !strings.Contains(row, "wake:bus-behind") || !strings.Contains(row, "behind=0") || h.State != life.WakeRepaired {
		t.Errorf("row %q state %q; want wake:bus-behind, behind=0, repaired", row, h.State)
	}
}

// TestWakeHealthDownAfterTwoFailedLoads: a unit that fails to load twice is
// tried exactly twice in the tick and the row prints `wake: down` in red.
func TestWakeHealthDownAfterTwoFailedLoads(t *testing.T) {
	host := newFakeWakeHost()
	d := walterDecl()
	host.files[d.UnitFile] = true
	host.loadFails[walterUnit] = -1

	h := life.CheckWake(context.Background(), host, beatsLive(false), d)
	if h.State != life.WakeDown {
		t.Fatalf("state %q, want down", h.State)
	}
	if n := len(host.calls); n != 2 {
		t.Errorf("%d load attempts (%v), want exactly 2", n, host.calls)
	}
	row := h.Row()
	if !strings.HasPrefix(row, "\x1b[31mwake: down\x1b[0m") {
		t.Errorf("row %q does not start with a red `wake: down`", row)
	}
	if !strings.Contains(row, "wake:unit-missing") || !strings.Contains(row, "beat=stale") {
		t.Errorf("down row %q lacks its finding or the stale beat", row)
	}

	// One failure then success is a repair, not a down: the second attempt is the retry.
	host = newFakeWakeHost()
	host.files[d.UnitFile] = true
	host.loadFails[walterUnit] = 1
	if h := life.CheckWake(context.Background(), host, beatsLive(true), d); h.State != life.WakeRepaired || len(host.calls) != 2 {
		t.Errorf("fail-once unit: state %q after %d attempts, want repaired after 2", h.State, len(host.calls))
	}
}

// TestWakeHealthNoUnitFileIsDown: with no unit file there is nothing to
// bootstrap; the tick says so and does not pretend to repair.
func TestWakeHealthNoUnitFileIsDown(t *testing.T) {
	host := newFakeWakeHost()
	h := life.CheckWake(context.Background(), host, beatsLive(true), walterDecl())
	if h.State != life.WakeDown || len(host.calls) != 0 || !strings.Contains(h.Row(), "wake:no-unit-file") {
		t.Fatalf("no unit file: state %q calls %v row %q", h.State, host.calls, h.Row())
	}
}

// TestWakeHealthHumanAndUndeclared is the #3048 addendum: `wake: human` with
// the owner's notify channel is a declared path; without a channel, or with
// neither mode, the friend is down in red.
func TestWakeHealthHumanAndUndeclared(t *testing.T) {
	host := newFakeWakeHost()
	ctx := context.Background()
	h := life.CheckWake(ctx, host, beatsLive(false), life.WakeDecl{Friend: "stella", Mode: life.WakeHuman, Notify: "bus To: Glenn"})
	if h.State != life.WakeHumanState || !strings.HasPrefix(h.Row(), "wake: human notify=bus To: Glenn") {
		t.Errorf("human with notify: state %q row %q", h.State, h.Row())
	}
	for _, d := range []life.WakeDecl{
		{Friend: "stella", Mode: life.WakeHuman},
		{Friend: "nobody"},
	} {
		h := life.CheckWake(ctx, host, beatsLive(false), d)
		if h.State != life.WakeDown || !strings.HasPrefix(h.Row(), "\x1b[31mwake: down\x1b[0m") || !strings.Contains(h.Row(), "wake:undeclared") {
			t.Errorf("%+v: state %q row %q, want red down with wake:undeclared", d, h.State, h.Row())
		}
	}
	if len(host.calls) != 0 {
		t.Errorf("human/undeclared friends must not touch the host: %v", host.calls)
	}
}

// TestWakeHealthRecordsRepairInRedis: the repaired row lands on the friend's
// wakehealth hash and the bootstrap is one cap:log wake-repair receipt, so the
// table prints it from Redis and the repair is on the record.
func TestWakeHealthRecordsRepairInRedis(t *testing.T) {
	st, client, _ := controlRedis(t)
	ctx := context.Background()
	host := newFakeWakeHost()
	d := walterDecl()
	host.files[d.UnitFile] = true

	h := life.CheckWake(ctx, host, beatsLive(true), d)
	if err := life.RecordWake(ctx, st, h, "rowan", "tick-1"); err != nil {
		t.Fatal(err)
	}
	row, err := client.HGet(ctx, life.WakeHealthKey("walter"), "row").Result()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row, "wake:unit-missing") || !strings.Contains(row, "wake: repaired "+walterUnit) {
		t.Errorf("stored row %q", row)
	}
	if state := client.HGet(ctx, life.WakeHealthKey("walter"), "state").Val(); state != life.WakeRepaired {
		t.Errorf("stored state %q", state)
	}
	entries, err := client.XRange(ctx, "cap:log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var receipts []string
	for _, e := range entries {
		if e.Values["kind"] == "wake-repair" {
			receipts = append(receipts, fmt.Sprint(e.Values["subject"], " ", e.Values["reason"]))
		}
	}
	if len(receipts) != 1 || receipts[0] != "walter bootstrap "+walterUnit {
		t.Errorf("cap:log wake-repair receipts %q, want exactly [walter bootstrap %s]", receipts, walterUnit)
	}
}

// TestExecWakeHostBehindAndPullOnRealGit runs the production Behind/Pull on a
// real local clone three commits behind its (local path) upstream.
func TestExecWakeHostBehindAndPullOnRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	dir := t.TempDir()
	up, clone := filepath.Join(dir, "up"), filepath.Join(dir, "clone")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", up)
	git("-C", up, "commit", "-q", "--allow-empty", "-m", "0")
	git("clone", "-q", up, clone)
	for i := 1; i <= 3; i++ {
		git("-C", up, "commit", "-q", "--allow-empty", "-m", fmt.Sprint(i))
	}

	host := life.ExecWakeHost{}
	ctx := context.Background()
	n, err := host.Behind(ctx, clone)
	if err != nil || n != 3 {
		t.Fatalf("behind = %d, %v; want 3", n, err)
	}
	if err := host.Pull(ctx, clone); err != nil {
		t.Fatal(err)
	}
	if n, err := host.Behind(ctx, clone); err != nil || n != 0 {
		t.Fatalf("after pull behind = %d, %v; want 0", n, err)
	}
}
