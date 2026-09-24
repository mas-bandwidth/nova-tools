package main

// The Mac-bench power verbs, `nova-pulse wake` and `nova-pulse sleep` (card 9344, #1142).
// They replace the retired coordination scripts fleet-wake.sh and fleet-sleep.sh, which are
// no longer in the tree; they woke a sleeping
// iMac Pro by magic packet from the LAN bench and let it sleep again on idle. The Macs draw
// 100 W each and the fleet runs on solar, so an idle bench sleeping is the point.
//
// The shape is not copied. Every input is validated before any ssh; every remote step goes
// through one runner interface that is `ssh <target> bash -s` with the script on the
// child's stdin, so a test drives a fake that records the script and answers canned output,
// and no test opens a socket or reaches a machine. The GitHub runner list is a second
// interface; the real one is one bounded `gh api .../actions/runners` call. The magic
// packet and the activity assertion are the reference's behaviour, not its shell: the
// packet is built here in Go, the assertions are argv, not a string interpolated into a
// remote shell. A timeout kills the child's whole process group (power_proc_unix.go).

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// powerRepo is the repo whose self-hosted runners are the awake test. The reference script
// hard-codes it and so does this verb.
const powerRepo = "mas-bandwidth/nova-tools"

// powerAssertScript is the user-activity assertion: a magic packet only DARK-wakes a Mac
// (ssh answers, runners stay offline), and an assertion from inside turns it into a full
// wake. It holds the bench awake while the runners reconnect. Exact argv, no shell string
// built from input.
const powerAssertScript = "sudo -n pmset -a sleep 0 >/dev/null 2>&1; /usr/bin/caffeinate -u -t 5"

// powerSSHWait is the whole time a woken bench gets to answer ssh: ninety seconds, measured
// three times on batman (3 to 6 minutes to runners, seconds to ssh).
const powerSSHWait = 90 * time.Second

// powerSSHPollEvery is how often the ssh waiter knocks. Three seconds: the reference's
// cadence, short enough that the first answer is seen at once.
const powerSSHPollEvery = 3 * time.Second

// powerRunnerPollEvery is how often the runner waiter asks GitHub. Fifteen seconds: long
// enough that the poll is not the load, short enough that a three-to-six-minute reconnect
// is seen inside the default window.
const powerRunnerPollEvery = 15 * time.Second

// powerDefaultTimeout is the whole time the runners get to come online when --timeout is not
// given: eight minutes, more than the measured worst case.
const powerDefaultTimeout = 8 * time.Minute

// powerDefaultIdle is how long a Mac waits before sleeping when --idle is not given.
const powerDefaultIdle = 30 * time.Minute

// powerChildTimeout bounds one ssh child when the caller does not.
const powerChildTimeout = 30 * time.Second

// powerRunnerListTimeout bounds the gh runner-list child.
const powerRunnerListTimeout = 30 * time.Second

// powerName is a legal bench or lan-bench name: a plain host alias, never anything a shell
// could read as syntax.
var powerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// powerBench is one line of the registry: name, mac, lan-bench.
type powerBench struct {
	Name string
	MAC  string
	LAN  string
}

// powerRunner is the one remote-step interface: run a script on a target and return its
// combined output. The real one is `ssh <target> bash -s` with script on stdin.
type powerRunner interface {
	Run(ctx context.Context, target, script string) (string, error)
}

// powerRunners is the GitHub side: the self-hosted runners of one repo.
type powerRunners interface {
	Runners(repo string) ([]powerRunnerInfo, error)
}

// powerRunnerInfo is one self-hosted runner as the runners API reports it.
type powerRunnerInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"` // online, offline
	Busy   bool   `json:"busy"`
}

// The hooks a test replaces. Nothing here is a global side effect: a test wires fakes in
// and restores them.
var (
	powerNewRunner  = func() powerRunner { return powerSSHRunner{Program: "ssh"} }
	powerNewRunners = func() powerRunners { return ghPowerRunners{Timeout: powerRunnerListTimeout} }
	powerNow        = func() time.Time { return time.Now().UTC() }
	powerSleepFn    = time.Sleep
)

// powerMultiFlag collects a repeatable --bench, splitting commas as the fleet verbs do.
type powerMultiFlag []string

func (m *powerMultiFlag) String() string { return strings.Join(*m, ",") }

func (m *powerMultiFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if t := strings.TrimSpace(part); t != "" {
			*m = append(*m, t)
		}
	}
	return nil
}

// readPowerRegistry reads and validates the registry whole: one `name,mac,lan-bench` per
// line, blank lines and `#` comments skipped. A partially-read registry is worse than none,
// so any bad row refuses the file and names the line.
func readPowerRegistry(path string) (map[string]powerBench, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]powerBench{}
	for i, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Split(t, ",")
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s line %d wants name,mac,lan-bench (three comma-separated fields), got %d", path, i+1, len(fields))
		}
		name := strings.TrimSpace(fields[0])
		mac := strings.TrimSpace(fields[1])
		lan := strings.TrimSpace(fields[2])
		if name == "" {
			return nil, fmt.Errorf("%s line %d has an empty bench name", path, i+1)
		}
		if !powerName.MatchString(name) {
			return nil, fmt.Errorf("%s line %d: bench name %q is not a plain host alias", path, i+1, name)
		}
		if _, err := powerMagicPacket(mac); err != nil {
			return nil, fmt.Errorf("%s line %d: %s", path, i+1, err)
		}
		if lan == "" {
			return nil, fmt.Errorf("%s line %d: bench %s has no lan-bench", path, i+1, name)
		}
		if !powerName.MatchString(lan) {
			return nil, fmt.Errorf("%s line %d: lan-bench %q is not a plain host alias", path, i+1, lan)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%s line %d: bench %s appears twice", path, i+1, name)
		}
		out[name] = powerBench{Name: name, MAC: mac, LAN: lan}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no benches; refusing to guess", path)
	}
	return out, nil
}

// powerMagicPacket is the Wake-on-LAN packet for a mac: six 0xFF bytes then the six-byte
// hardware address repeated sixteen times, 102 bytes and nothing else.
func powerMagicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return nil, fmt.Errorf("mac %q is not an address: %w", mac, err)
	}
	if len(hw) != 6 {
		return nil, fmt.Errorf("mac %q is not a six-byte address", mac)
	}
	packet := make([]byte, 6+16*len(hw))
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}
	for i := 0; i < 16; i++ {
		copy(packet[6+i*len(hw):], hw)
	}
	return packet, nil
}

// powerWOLScript is the remote script that broadcasts the packet, run through the one
// runner interface on the lan-bench. The hex is built by Go from a parsed six-byte mac, so
// it is [0-9a-f] and there is no input to interpolate; the packet goes to the all-ones
// broadcast on the Wake-on-LAN port, three times.
func powerWOLScript(packet []byte) string {
	return strings.Join([]string{
		"set -eu",
		"python3 - <<'PY'",
		"import socket",
		"pkt = bytes.fromhex('" + hex.EncodeToString(packet) + "')",
		"s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)",
		"s.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)",
		"for _ in range(3):",
		"    s.sendto(pkt, ('255.255.255.255', 9))",
		"PY",
	}, "\n")
}

// powerSleepScript is the remote script that sets idle sleep to whole minutes. The minutes
// are an int, never input text.
func powerSleepScript(minutes int) string {
	return fmt.Sprintf("sudo -n pmset -a sleep %d displaysleep 1 womp 1", minutes)
}

// powerSSHRunner is the one real runner: `ssh <target> bash -s`, script on stdin, bounded
// by the context, and on timeout the child's whole process group is killed.
type powerSSHRunner struct {
	Program string
}

func (r powerSSHRunner) Run(ctx context.Context, target, script string) (string, error) {
	program := r.Program
	if program == "" {
		program = "ssh"
	}
	testguard.RefuseHosts(program, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, "bash", "-s")
	cmd := exec.CommandContext(ctx, program, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	powerSetProcessGroup(cmd)
	cmd.Cancel = func() error { return powerKillProcessGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ghPowerRunners is the real GitHub side: one `gh api repos/<repo>/actions/runners` call
// per ask, with `--paginate` and a jq filter that prints one runner object per line so
// every page folds the same way.
type ghPowerRunners struct {
	Timeout time.Duration
}

func (g ghPowerRunners) Runners(repo string) ([]powerRunnerInfo, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = powerRunnerListTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "api", "repos/"+repo+"/actions/runners",
		"--paginate", "--jq", ".runners[] | {name,status,busy}")
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api repos/%s/actions/runners: %w", repo, err)
	}
	var out []powerRunnerInfo
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r powerRunnerInfo
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("the runner row %q did not parse: %w", line, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// powerOnlineCount counts the bench's runners online in GitHub: a name that starts with the
// bench and status online. A bench's runners are named <bench>-<n>.
func powerOnlineCount(runners powerRunners, bench string) (int, error) {
	list, err := runners.Runners(powerRepo)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range list {
		if strings.HasPrefix(r.Name, bench) && r.Status == "online" {
			n++
		}
	}
	return n, nil
}

// powerBusyCount counts the bench's runners GitHub reports busy.
func powerBusyCount(runners powerRunners, bench string) (int, error) {
	list, err := runners.Runners(powerRepo)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range list {
		if strings.HasPrefix(r.Name, bench) && r.Busy {
			n++
		}
	}
	return n, nil
}

// powerWakeResult is one bench's one line and whether it failed.
type powerWakeResult struct {
	line   string
	failed bool
}

// powerWake sends each named bench's magic packet from its lan-bench, waits for ssh, runs
// the user-activity assertion and waits for the runners to come online. It prints exactly
// one WAKE or WAKE FAIL line per bench. Every input is validated before any ssh.
func powerWake(names []string, registryPath string, timeout time.Duration, stdout, stderr io.Writer) int {
	benches, err := readPowerRegistry(registryPath)
	if err != nil {
		fmt.Fprintf(stderr, "nova-pulse wake: --registry %s: %s; refusing to guess\n", oneline.Field(registryPath), oneline.Err(err))
		return 2
	}
	for _, name := range names {
		if _, ok := benches[name]; !ok {
			fmt.Fprintf(stderr, "nova-pulse wake: bench %s is not in %s; refusing to wake anything\n", oneline.Field(name), oneline.Field(registryPath))
			return 2
		}
	}
	if timeout <= 0 {
		timeout = powerDefaultTimeout
	}
	runner := powerNewRunner()
	runners := powerNewRunners()
	now, sleep := powerNow, powerSleepFn

	results := make([]powerWakeResult, len(names))
	for i, name := range names {
		results[i] = powerWakeOne(runner, runners, now, sleep, benches[name], timeout)
	}
	rc := 0
	for _, r := range results {
		if r.failed {
			rc = 1
		}
		fmt.Fprintln(stdout, r.line)
	}
	return rc
}

// powerWakeOne is one bench, in the order the reference proved: packet from the LAN bench,
// ssh, assertion, runners online.
func powerWakeOne(runner powerRunner, runners powerRunners, now func() time.Time, sleep func(time.Duration), b powerBench, timeout time.Duration) powerWakeResult {
	start := now()
	if n, err := powerOnlineCount(runners, b.Name); err == nil && n > 0 {
		return powerWakeResult{line: fmt.Sprintf("WAKE %s up after 0s runners=%d", oneline.Field(b.Name), n)}
	}
	packet, err := powerMagicPacket(b.MAC)
	if err != nil {
		return powerWakeResult{line: powerWakeFail(b.Name, "registry", err.Error()), failed: true}
	}
	ctx, cancel := context.WithTimeout(context.Background(), powerChildTimeout)
	out, err := runner.Run(ctx, b.LAN, powerWOLScript(packet))
	cancel()
	if err != nil {
		return powerWakeResult{line: powerWakeFail(b.Name, "packet", powerReason(out, err)), failed: true}
	}
	if !powerWaitSSH(runner, now, sleep, b.Name) {
		return powerWakeResult{line: powerWakeFail(b.Name, "ssh", "no ssh after 90s"), failed: true}
	}
	ctx, cancel = context.WithTimeout(context.Background(), powerChildTimeout)
	out, err = runner.Run(ctx, b.Name, powerAssertScript)
	cancel()
	if err != nil {
		return powerWakeResult{line: powerWakeFail(b.Name, "assert", powerReason(out, err)), failed: true}
	}
	deadline := now().Add(timeout)
	for {
		n, err := powerOnlineCount(runners, b.Name)
		if err == nil && n > 0 {
			wall := int64(now().Sub(start).Seconds())
			return powerWakeResult{line: fmt.Sprintf("WAKE %s up after %ds runners=%d", oneline.Field(b.Name), wall, n)}
		}
		if !now().Before(deadline) {
			return powerWakeResult{line: powerWakeFail(b.Name, "runners", fmt.Sprintf("no runner online after %s", timeout)), failed: true}
		}
		sleep(powerRunnerPollEvery)
	}
}

// powerWaitSSH knocks on the bench until ssh answers or ninety seconds are gone.
func powerWaitSSH(runner powerRunner, now func() time.Time, sleep func(time.Duration), name string) bool {
	deadline := now().Add(powerSSHWait)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), powerChildTimeout)
		_, err := runner.Run(ctx, name, "true")
		cancel()
		if err == nil {
			return true
		}
		if !now().Before(deadline) {
			return false
		}
		sleep(powerSSHPollEvery)
	}
}

// powerWakeFail is the one failed-line shape: the bench, the stage and the reason.
func powerWakeFail(bench, stage, reason string) string {
	return fmt.Sprintf("WAKE FAIL %s %s %s", oneline.Field(bench), stage, oneline.Escape(reason))
}

// powerSleep refuses a bench whose runners are busy, else sets idle sleep. It prints one
// SLEEP, SLEEP REFUSED or SLEEP FAIL line per bench.
func powerSleep(names []string, idle time.Duration, stdout, stderr io.Writer) int {
	if idle <= 0 {
		idle = powerDefaultIdle
	}
	minutes := int(idle / time.Minute)
	if minutes < 1 {
		fmt.Fprintf(stderr, "nova-pulse sleep: --idle %s is less than one minute; refusing to guess\n", idle)
		return 2
	}
	runner := powerNewRunner()
	runners := powerNewRunners()
	rc := 0
	for _, name := range names {
		n, err := powerBusyCount(runners, name)
		if err != nil {
			fmt.Fprintf(stdout, "SLEEP REFUSED %s busy=unknown\n", oneline.Field(name))
			rc = 1
			continue
		}
		if n > 0 {
			fmt.Fprintf(stdout, "SLEEP REFUSED %s busy=%d\n", oneline.Field(name), n)
			rc = 1
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), powerChildTimeout)
		out, err := runner.Run(ctx, name, powerSleepScript(minutes))
		cancel()
		if err != nil {
			fmt.Fprintf(stdout, "SLEEP FAIL %s %s\n", oneline.Field(name), oneline.Escape(powerReason(out, err)))
			rc = 1
			continue
		}
		fmt.Fprintf(stdout, "SLEEP %s idle=%d\n", oneline.Field(name), minutes)
	}
	return rc
}

// powerReason is the last non-empty line of a failed child, or its error.
func powerReason(out string, err error) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	if err != nil {
		return err.Error()
	}
	return "no answer"
}

func cmdWake(args []string, stdout, stderr io.Writer) int {
	f := newFlags("wake")
	var benchFlags powerMultiFlag
	f.fs.Var(&benchFlags, "bench", "")
	registry := f.fs.String("registry", "", "")
	timeout := f.fs.String("timeout", "8m", "")
	if !f.parseAny(args, stderr) {
		return 2
	}
	names := append([]string{}, benchFlags...)
	names = append(names, f.fs.Args()...)
	f.want(*registry, "registry", "the bench registry: name,mac,lan-bench per line")
	if len(names) == 0 {
		f.add("--bench is required; name at least one bench to wake")
	}
	for _, n := range names {
		if !powerName.MatchString(n) {
			f.add(fmt.Sprintf("bench name %q is not a plain host alias", n))
		}
	}
	whole, err := time.ParseDuration(*timeout)
	if err != nil || whole <= 0 {
		f.add(fmt.Sprintf("--timeout wants a positive duration such as 8m or 90s, got %q", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	return powerWake(names, *registry, whole, stdout, stderr)
}

func cmdSleep(args []string, stdout, stderr io.Writer) int {
	f := newFlags("sleep")
	var benchFlags powerMultiFlag
	f.fs.Var(&benchFlags, "bench", "")
	idle := f.fs.String("idle", powerDefaultIdle.String(), "")
	if !f.parseAny(args, stderr) {
		return 2
	}
	names := append([]string{}, benchFlags...)
	names = append(names, f.fs.Args()...)
	if len(names) == 0 {
		f.add("--bench is required; name at least one bench to sleep")
	}
	for _, n := range names {
		if !powerName.MatchString(n) {
			f.add(fmt.Sprintf("bench name %q is not a plain host alias", n))
		}
	}
	whole, err := time.ParseDuration(*idle)
	if err != nil || whole <= 0 {
		f.add(fmt.Sprintf("--idle wants a positive duration such as 30m, got %q", *idle))
	}
	if f.refused(stderr) {
		return 2
	}
	return powerSleep(names, whole, stdout, stderr)
}
