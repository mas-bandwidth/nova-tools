package main

// The Mac-bench power tests (card 9344, #1142): `nova-pulse wake` and `nova-pulse sleep`.
// The two verbs replace scripts/coordination/fleet-wake.sh and fleet-sleep.sh, whose shape
// (a case table, a python heredoc over ssh, fixed sleeps and swallowed errors) is not
// copied. Everything a test needs is an interface: the ssh runner records every script it
// was handed and answers canned output, and the GitHub runner list is a fake. No test
// opens a socket, reaches a machine, or calls gh.

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// powerCall is one recorded remote step.
type powerCall struct {
	Target string
	Script string
}

// fakePowerRunner is the one runner interface's fake: it records what was sent and fails
// or answers per target.
type fakePowerRunner struct {
	mu     sync.Mutex
	calls  []powerCall
	errors map[string]error
}

func (f *fakePowerRunner) Run(ctx context.Context, target, script string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, powerCall{Target: target, Script: script})
	return "", f.errors[target]
}

func (f *fakePowerRunner) to(target string) []powerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []powerCall
	for _, c := range f.calls {
		if c.Target == target {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakePowerRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakePowerRunners is the GitHub runner list's fake. onlineAfter is the Runners call
// number at which the canned list flips to online, so a bench that is dark on the first
// look and up after the packet is one fixture.
type fakePowerRunners struct {
	mu          sync.Mutex
	list        []powerRunnerInfo
	err         error
	onlineAfter int
	calls       int
}

func (f *fakePowerRunners) Runners(repo string) ([]powerRunnerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.onlineAfter > 0 && f.calls >= f.onlineAfter {
		out := make([]powerRunnerInfo, len(f.list))
		for i := range f.list {
			out[i] = f.list[i]
			out[i].Status = "online"
		}
		return out, nil
	}
	return f.list, nil
}

// powerTestClock is the injected clock: Sleep advances it, so a test crosses a wall of
// minutes without waiting.
type powerTestClock struct {
	mu sync.Mutex
	t  time.Time
}

func newPowerTestClock() *powerTestClock {
	return &powerTestClock{t: time.Unix(1_700_000_000, 0).UTC()}
}

func (c *powerTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *powerTestClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// withPowerFakes wires the fakes into the package hooks for one test.
func withPowerFakes(t *testing.T, runner *fakePowerRunner, lister *fakePowerRunners, clock *powerTestClock) {
	t.Helper()
	origRunner, origLister, origNow, origSleep := powerNewRunner, powerNewRunners, powerNow, powerSleepFn
	powerNewRunner = func() powerRunner { return runner }
	powerNewRunners = func() powerRunners { return lister }
	powerNow = clock.Now
	powerSleepFn = clock.Sleep
	t.Cleanup(func() {
		powerNewRunner, powerNewRunners, powerNow, powerSleepFn = origRunner, origLister, origNow, origSleep
	})
}

func writePowerRegistry(t *testing.T, lines string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "macs.csv")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runPower(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String()
}

// power-wake-registry-refused-before-ssh: a registry whose mac does not parse is refused
// with exit 2 and the runner is never asked to run anything.
func TestPowerWakeRegistryRefusedBeforeSSH(t *testing.T) {
	reg := writePowerRegistry(t, "batman,not-a-mac,hulk\n")
	runner := &fakePowerRunner{}
	withPowerFakes(t, runner, &fakePowerRunners{}, newPowerTestClock())
	code, _, errs := runPower(t, "wake", "--bench", "batman", "--registry", reg)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "mac") {
		t.Fatalf("stderr=%q, want the bad mac named", errs)
	}
	if runner.count() != 0 {
		t.Fatalf("the runner was called %d times before the registry was validated", runner.count())
	}
}

// power-wake-unknown-bench-refused-before-ssh: a valid registry but a bench it does not
// name is refused, and nothing is sent.
func TestPowerWakeUnknownBenchRefusedBeforeSSH(t *testing.T) {
	reg := writePowerRegistry(t, "batman,d0:81:7a:d8:3a:ec,hulk\n")
	runner := &fakePowerRunner{}
	withPowerFakes(t, runner, &fakePowerRunners{}, newPowerTestClock())
	code, _, errs := runPower(t, "wake", "--bench", "robin", "--registry", reg)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "robin") {
		t.Fatalf("stderr=%q, want the unknown bench named", errs)
	}
	if runner.count() != 0 {
		t.Fatalf("the runner was called %d times for an unknown bench", runner.count())
	}
}

// power-wake-sends-packet-assercts-and-waits: the order is the contract. The magic packet
// goes from the lan-bench (hulk) first, the bench answers ssh, the user-activity assertion
// runs on the bench, and only then does the runner poll decide the bench is awake.
func TestPowerWakeSendsPacketAssertsAndWaitsForRunners(t *testing.T) {
	reg := writePowerRegistry(t, "batman,d0:81:7a:d8:3a:ec,hulk\n")
	runner := &fakePowerRunner{}
	lister := &fakePowerRunners{
		list:        []powerRunnerInfo{{Name: "batman-1", Status: "offline"}},
		onlineAfter: 2,
	}
	clock := newPowerTestClock()
	withPowerFakes(t, runner, lister, clock)

	code, out, errs := runPower(t, "wake", "--bench", "batman", "--registry", reg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%q stderr=%q", code, out, errs)
	}
	if out != "WAKE batman up after 0s runners=1\n" {
		t.Fatalf("stdout = %q", out)
	}

	// The magic packet: six 0xFF then the mac sixteen times, sent from hulk, to UDP port 9,
	// three times.
	if runner.count() < 3 {
		t.Fatalf("only %d remote steps, want packet, ssh poll and assertion at least", runner.count())
	}
	if first := runner.calls[0]; first.Target != "hulk" {
		t.Fatalf("first remote step target = %q, want the lan-bench hulk", first.Target)
	}
	packet, _ := powerMagicPacket("d0:81:7a:d8:3a:ec")
	hexed := hex.EncodeToString(packet)
	if !strings.Contains(runner.calls[0].Script, hexed) {
		t.Fatalf("the packet script does not carry the magic packet %s:\n%s", hexed, runner.calls[0].Script)
	}
	if !strings.Contains(runner.calls[0].Script, "255.255.255.255") || !strings.Contains(runner.calls[0].Script, "range(3)") {
		t.Fatalf("the packet script does not broadcast to port 9 three times:\n%s", runner.calls[0].Script)
	}
	if !strings.Contains(runner.calls[0].Script, "9") {
		t.Fatalf("the packet script does not name port 9:\n%s", runner.calls[0].Script)
	}
	foundAssert := false
	for _, c := range runner.to("batman") {
		if c.Script == powerAssertScript {
			foundAssert = true
		}
	}
	if !foundAssert {
		t.Fatalf("the user-activity assertion was never sent to batman: %+v", runner.to("batman"))
	}
}

// power-wake-fails-with-the-stage-that-did: no ssh answer inside ninety seconds is a WAKE
// FAIL naming the ssh stage, not a silent pass.
func TestPowerWakeFailsWhenSSHNeverAnswers(t *testing.T) {
	reg := writePowerRegistry(t, "batman,d0:81:7a:d8:3a:ec,hulk\n")
	runner := &fakePowerRunner{errors: map[string]error{"batman": context.DeadlineExceeded}}
	withPowerFakes(t, runner, &fakePowerRunners{list: []powerRunnerInfo{{Name: "batman-1", Status: "offline"}}}, newPowerTestClock())

	code, out, _ := runPower(t, "wake", "--bench", "batman", "--registry", reg)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%q", code, out)
	}
	if !strings.HasPrefix(out, "WAKE FAIL batman ssh ") {
		t.Fatalf("stdout = %q, want a WAKE FAIL at the ssh stage", out)
	}
}

// power-wake-fails-when-no-runner-comes-online: ssh answers and the assertion runs, but
// the bench's runners never appear in GitHub inside --timeout.
func TestPowerWakeFailsWhenNoRunnerComesOnline(t *testing.T) {
	reg := writePowerRegistry(t, "batman,d0:81:7a:d8:3a:ec,hulk\n")
	runner := &fakePowerRunner{}
	lister := &fakePowerRunners{list: []powerRunnerInfo{{Name: "batman-1", Status: "offline"}}}
	withPowerFakes(t, runner, lister, newPowerTestClock())

	code, out, _ := runPower(t, "wake", "--bench", "batman", "--registry", reg)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%q", code, out)
	}
	if !strings.HasPrefix(out, "WAKE FAIL batman runners ") {
		t.Fatalf("stdout = %q, want a WAKE FAIL at the runners stage", out)
	}
}

// power-wake-positional-bench: the bench names may also be positional, per the result line
// `nova-pulse wake <bench>...`.
func TestPowerWakeAcceptsPositionalBench(t *testing.T) {
	reg := writePowerRegistry(t, "batman,d0:81:7a:d8:3a:ec,hulk\n")
	runner := &fakePowerRunner{}
	lister := &fakePowerRunners{list: []powerRunnerInfo{{Name: "batman-1", Status: "online"}}}
	withPowerFakes(t, runner, lister, newPowerTestClock())
	code, out, errs := runPower(t, "wake", "--registry", reg, "batman")
	if code != 0 || !strings.HasPrefix(out, "WAKE batman up after 0s runners=1") {
		t.Fatalf("exit=%d out=%q errs=%q", code, out, errs)
	}
}

// sleep-refuses-a-busy-bench: a bench with any busy runner is refused and never told to
// sleep.
func TestPowerSleepRefusesBusyRunner(t *testing.T) {
	runner := &fakePowerRunner{}
	lister := &fakePowerRunners{list: []powerRunnerInfo{{Name: "batman-1", Status: "online", Busy: true}}}
	withPowerFakes(t, runner, lister, newPowerTestClock())

	code, out, _ := runPower(t, "sleep", "--bench", "batman")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%q", code, out)
	}
	if out != "SLEEP REFUSED batman busy=1\n" {
		t.Fatalf("stdout = %q", out)
	}
	if runner.count() != 0 {
		t.Fatalf("a busy bench was told to sleep: %+v", runner.calls)
	}
}

// sleep-sets-idle-sleep: an idle bench is set to sleep after --idle minutes and the verb
// says so.
func TestPowerSleepSetsIdleSleep(t *testing.T) {
	runner := &fakePowerRunner{}
	lister := &fakePowerRunners{list: []powerRunnerInfo{{Name: "batman-1", Status: "online"}}}
	withPowerFakes(t, runner, lister, newPowerTestClock())

	code, out, errs := runPower(t, "sleep", "--bench", "batman", "--idle", "30m")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errs)
	}
	if out != "SLEEP batman idle=30\n" {
		t.Fatalf("stdout = %q", out)
	}
	calls := runner.to("batman")
	if len(calls) != 1 {
		t.Fatalf("remote steps to batman = %d, want 1", len(calls))
	}
	if !strings.Contains(calls[0].Script, "pmset -a sleep 30") {
		t.Fatalf("the sleep script does not set thirty idle minutes:\n%s", calls[0].Script)
	}
}

// power-registry-validates-shape: the registry is name,mac,lan-bench per line, with the
// name and the lan-bench plain host aliases and the mac six bytes. Every way of breaking
// that is a refusal.
func TestPowerRegistryValidatesShape(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"two fields", "batman,d0:81:7a:d8:3a:ec\n", "three"},
		{"empty name", ",d0:81:7a:d8:3a:ec,hulk\n", "name"},
		{"bad name", "bat man,d0:81:7a:d8:3a:ec,hulk\n", "name"},
		{"bad mac", "batman,zz:zz,hulk\n", "mac"},
		{"short mac", "batman,01:02:03:04:05,hulk\n", "mac"},
		{"no lan", "batman,d0:81:7a:d8:3a:ec,\n", "lan-bench"},
		{"duplicate", "batman,d0:81:7a:d8:3a:ec,hulk\nbatman,d0:81:7a:da:72:ec,hulk\n", "twice"},
		{"empty", "# only a comment\n", "no benches"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := writePowerRegistry(t, tc.body)
			_, err := readPowerRegistry(reg)
			if err == nil {
				t.Fatalf("no error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %q", err.Error(), tc.want)
			}
		})
	}
}

// powerRegistryLines is the accepted shape, one good row, so the rejections above are not
// accidental.
func TestPowerRegistryAcceptsGoodRow(t *testing.T) {
	reg := writePowerRegistry(t, "batman,d0:81:7a:d8:3a:ec,hulk\nsuperman,d0:81:7a:da:72:ec,hulk\n")
	got, err := readPowerRegistry(reg)
	if err != nil {
		t.Fatal(err)
	}
	if got["batman"].LAN != "hulk" || got["superman"].MAC != "d0:81:7a:da:72:ec" {
		t.Fatalf("registry = %+v", got)
	}
}
