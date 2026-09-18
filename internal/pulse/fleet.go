package pulse

// The fleet verb: the machines named in one tab-separated file, acted on over ssh. This file
// is `fleet reboot` -- boot a bench, then wait for its runners to register again -- and the
// fleet power verbs `fleet suspend`, which sleeps the idle benches so a solar fleet does not
// burn the afternoon, and `fleet wake`, which wakes them by magic packet (SPEC-PULSE ## Fleet,
// issue #880 items 14 and 17).
//
// The fleet rule (docs/SPEC-PULSE.md, "Fleet"): every fleet verb prints one FLEET <name>
// line per bench, runs the benches in parallel under --timeout, exits 0/2/3 (0 ok, 2
// drift-or-refused, 3 unreachable), takes ssh from --ssh so a test puts a fake on PATH and
// no test makes a network call, and refuses `studio` for any admin act. The magic packet is
// built here, in Go, and sent through an injected sender: a test replaces it and no test
// opens a socket. The waiter here is a clock and a sleep handed in, so a test reaches a wall
// of minutes without waiting for one.

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// fleetPowerDefaultTimeout bounds every ssh child when no --timeout is given.
const fleetPowerDefaultTimeout = 120 * time.Second

// fleetPowerDefaultWait is the whole time a waking bench gets to answer: three minutes.
const fleetPowerDefaultWait = 3 * time.Minute

// fleetPowerPollEvery is how often the wake waiter asks a bench whether it is up.
// Fifteen seconds: long enough that the poll is not the load, short enough that a boot
// under a minute is seen within a minute.
const fleetPowerPollEvery = 15 * time.Second

// fleetPollEvery is how often the waiter asks a rebooting bench whether its runners are
// back. Fifteen seconds: long enough that the poll is not the load, short enough that a
// boot under a minute is seen within a minute.
const fleetPollEvery = 15 * time.Second

// fleetDefaultWait is the whole time a bench gets to come back: five minutes.
const fleetDefaultWait = 5 * time.Minute

// fleetDefaultTimeout is the bound on every ssh child when the caller names none.
const fleetDefaultTimeout = 120 * time.Second

// FleetBench is one line of the benches file: name, ssh target, home, mac (mac is "-" when
// the bench never sleeps). The file is shared with `fleet survey`.
type FleetBench struct {
	Name string
	SSH  string
	Home string
	MAC  string
}

// ReadFleetBenches reads the benches file: one bench per line,
// `name<TAB>ssh target<TAB>home<TAB>mac`. Blank lines and `#` comments are skipped, and a
// line that is not four fields is a refusal naming the file and the line -- a partially-read
// fleet is worse than none.
func ReadFleetBenches(path string) (map[string]FleetBench, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]FleetBench{}
	for i, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Split(t, "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("%s line %d wants name<TAB>ssh target<TAB>home<TAB>mac, got %d fields", path, i+1, len(f))
		}
		b := FleetBench{
			Name: strings.TrimSpace(f[0]), SSH: strings.TrimSpace(f[1]),
			Home: strings.TrimSpace(f[2]), MAC: strings.TrimSpace(f[3]),
		}
		if b.Name == "" {
			return nil, fmt.Errorf("%s line %d has an empty bench name", path, i+1)
		}
		out[b.Name] = b
	}
	return out, nil
}

// MagicPacket is the Wake-on-LAN packet for one mac: six 0xFF bytes then the six-byte
// hardware address repeated sixteen times, 102 bytes and nothing else.
func MagicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return nil, fmt.Errorf("%q is not a mac address: %w", mac, err)
	}
	if len(hw) != 6 {
		return nil, fmt.Errorf("%q is not a six-byte mac address", mac)
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

// fleetWOLAddress is where the magic packet goes: the all-ones IPv4 broadcast on the
// Wake-on-LAN port, so every bench on the segment may answer.
const fleetWOLAddress = "255.255.255.255:9"

// SendWakeOnLAN is the real transport: one UDP datagram to the broadcast address. It is a
// variable so a test replaces it and never opens a socket.
var SendWakeOnLAN = func(packet []byte, addr string) error {
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(packet)
	return err
}

// fleetPowerResult is one bench's one line and how it went.
type fleetPowerResult struct {
	line        string
	refused     bool
	unreachable bool
}

// fleetPowerRefusal reads one requested name against the file and, when one was named,
// against the machines registry. `studio` is refused for any admin act; a name the file does
// not carry is refused rather than guessed; and a machine the registry does not call a bench
// is refused with the lock's reason and remedy on the line (Glenn 2026-09-18: runner hosts
// are CI-only). A nil registry leaves the first two refusals as the whole guard.
func fleetPowerRefusal(name string, benches map[string]FleetBench, reg *fleet.Registry) (fleetPowerResult, bool) {
	if strings.EqualFold(strings.TrimSpace(name), "studio") {
		return fleetPowerResult{line: "FLEET REFUSED bench=" + oneline.Field(name), refused: true}, true
	}
	if _, ok := benches[name]; !ok {
		return fleetPowerResult{line: "FLEET REFUSED bench=" + oneline.Field(name), refused: true}, true
	}
	if line, refused := fleetRoleRefusal(name, reg); refused {
		return fleetPowerResult{line: line, refused: true}, true
	}
	return fleetPowerResult{}, false
}

// fleetPowerPrint writes one FLEET line per result under the --max ceiling and returns the
// fleet rule's exit code over every result, printed or not.
func fleetPowerPrint(w io.Writer, max int, results []fleetPowerResult, remedy string) int {
	list := bounded.Capped(w, max, "FLEET", "bench", remedy)
	exit := 0
	for _, r := range results {
		list.Line(r.line)
		switch {
		case r.unreachable:
			exit = 3
		case r.refused && exit == 0:
			exit = 2
		}
	}
	list.More()
	return exit
}

// fleetSSH runs one remote script on the bench with the ssh program from --ssh, through
// `ssh <target> bash -s`, bounded by --timeout. The target is the benches file's ssh
// column; the script is the remote command, on the child's stdin.
func fleetSSH(ctx context.Context, program, target, script string) (string, error) {
	if program == "" {
		program = "ssh"
	}
	cmd := exec.CommandContext(ctx, program, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	raw, err := cmd.CombinedOutput()
	return string(raw), err
}

// fleetPowerTimeout is the per-child bound: the input's, or the default.
func fleetPowerTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return fleetPowerDefaultTimeout
	}
	return d
}

// fleetMarker returns the text after `TOKEN<TAB>` on the first line that carries it, and
// whether one was found.
func fleetMarker(out, token string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		before, rest, ok := strings.Cut(strings.TrimRight(line, "\r"), token+"\t")
		if ok && before == "" {
			return rest, true
		}
	}
	return "", false
}

// fleetQuote single-quotes a path for the remote shell.
func fleetQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- fleet suspend -------------------------------------------------------------------

// FleetSuspendInput is everything `fleet suspend` needs, apart from flag parsing, so a test
// can drive it against a benches file and a fake ssh on PATH.
type FleetSuspendInput struct {
	Benches  string
	Machines string // the machines registry; empty leaves the older refusals as the guard
	Names    []string
	SSH      string // the ssh program; empty is "ssh"
	Force    bool   // suspend even a busy bench
	IfIdle   bool   // skip a busy bench instead of refusing it
	Timeout  time.Duration
	Max      int // at most this many FLEET lines; 0 is all
	Stdout   io.Writer
	Stderr   io.Writer
}

// FleetSuspend sleeps each named bench: a bench holding a running card (a lease or a job
// directory with a live pid under ~/rowan-swarm-root or ~/stella-swarm-root) or a busy
// runner (a Runner.Worker process) is BUSY and refused (exit 2) unless --force, and is
// skipped under --if-idle. A bench that cannot be reached is exit 3; every bench prints one
// FLEET line.
func FleetSuspend(in FleetSuspendInput) int {
	if strings.TrimSpace(in.Benches) == "" {
		return refusal(in.Stderr, "FLEET", fmt.Errorf("missing --benches; refusing to guess (supply the fleet benches file)"))
	}
	benches, err := ReadFleetBenches(in.Benches)
	if err != nil {
		return refusal(in.Stderr, "FLEET", fmt.Errorf("%s (a benches file is name<TAB>ssh target<TAB>home<TAB>mac)", oneline.Err(err)))
	}
	if len(in.Names) == 0 {
		return refusal(in.Stderr, "FLEET", fmt.Errorf("missing --bench; refusing to guess (name the bench or benches to suspend)"))
	}
	reg, code := fleetRegistry(in.Machines, in.Stderr)
	if code != 0 {
		return code
	}

	results := make([]fleetPowerResult, len(in.Names))
	var wg sync.WaitGroup
	for i, name := range in.Names {
		if r, refused := fleetPowerRefusal(name, benches, reg); refused {
			results[i] = r
			continue
		}
		b := benches[name]
		wg.Add(1)
		go func(i int, b FleetBench) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(),
				fleetPowerTimeout(in.Timeout))
			defer cancel()
			results[i] = in.suspendOne(ctx, b)
		}(i, b)
	}
	wg.Wait()

	return fleetPowerPrint(in.Stdout, in.Max, results,
		"run: nova-pulse fleet suspend --benches <file> --bench <names> --max 0")
}

// suspendOne is one bench: the busy check and the sleep in one remote script.
func (in FleetSuspendInput) suspendOne(ctx context.Context, b FleetBench) fleetPowerResult {
	out, err := fleetSSH(ctx, in.SSH, b.SSH, fleetSuspendScript(b.Home, in.Force, in.IfIdle))
	if err != nil {
		return fleetPowerResult{
			line:        fmt.Sprintf("FLEET %s UNREACHABLE %s", oneline.Field(b.Name), oneline.Escape(fleetReason(out, err))),
			unreachable: true,
		}
	}
	if what, ok := fleetMarker(out, "FLEETBUSY"); ok {
		return fleetPowerResult{
			line:    fmt.Sprintf("FLEET %s BUSY %s", oneline.Field(b.Name), oneline.Escape(what)),
			refused: true,
		}
	}
	if what, ok := fleetMarker(out, "FLEETSKIP"); ok {
		return fleetPowerResult{
			line: fmt.Sprintf("FLEET %s SKIP %s", oneline.Field(b.Name), oneline.Escape(what)),
		}
	}
	if _, ok := fleetMarker(out, "FLEETFAIL"); ok {
		reason, _ := fleetMarker(out, "FLEETFAIL")
		return fleetPowerResult{
			line:        fmt.Sprintf("FLEET %s SUSPEND FAILED %s", oneline.Field(b.Name), oneline.Escape(reason)),
			unreachable: true,
		}
	}
	if _, ok := fleetMarker(out, "FLEETSUSPENDED"); ok {
		return fleetPowerResult{line: fmt.Sprintf("FLEET %s SUSPENDED", oneline.Field(b.Name))}
	}
	return fleetPowerResult{
		line:        fmt.Sprintf("FLEET %s UNREACHABLE no answer", oneline.Field(b.Name)),
		unreachable: true,
	}
}

// fleetReason is the last non-empty line of a failed child, or its error.
func fleetReason(out string, err error) string {
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

// fleetSuspendScript is the remote script, run through `ssh <target> bash -s`. It sets
// HOME to the bench's home column, looks for a lease or a job directory with a live pid
// under the two swarm roots, and for a Runner.Worker process, then either reports the busy
// finding, skips it (--if-idle) or runs `sudo systemctl suspend`. It prints exactly one
// tab-separated marker line; the Go side is the only place a FLEET line is formatted.
func fleetSuspendScript(home string, force, ifIdle bool) string {
	forceVar, idleVar := "no", "no"
	if force {
		forceVar = "yes"
	}
	if ifIdle {
		idleVar = "yes"
	}
	return strings.Join([]string{
		"HOME=" + fleetQuote(home),
		"export HOME",
		"force=" + forceVar,
		"ifidle=" + idleVar,
		`busy=""`,
		`for root in "$HOME/rowan-swarm-root" "$HOME/stella-swarm-root"; do`,
		`  [ -d "$root" ] || continue`,
		`  for f in "$root"/*/jobs/*/pid "$root"/*/slots/*.json "$root"/slots/*/lease; do`,
		`    [ -f "$f" ] || continue`,
		`    pid=$(sed -n 's/.*"pid"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$f" | head -n 1)`,
		`    [ -n "$pid" ] || pid=$(sed -n 's/^pid=\([0-9][0-9]*\).*/\1/p' "$f" | head -n 1)`,
		`    [ -n "$pid" ] || continue`,
		`    if kill -0 "$pid" 2>/dev/null; then busy="job:$f pid=$pid"; break; fi`,
		`  done`,
		`  [ -n "$busy" ] && break`,
		`done`,
		`if [ -z "$busy" ] && command -v pgrep >/dev/null 2>&1 && pgrep -x Runner.Worker >/dev/null 2>&1; then`,
		`  busy="runner:Runner.Worker"`,
		`fi`,
		`if [ -n "$busy" ]; then`,
		`  if [ "$force" = "yes" ]; then :`,
		`  elif [ "$ifidle" = "yes" ]; then printf 'FLEETSKIP\t%s\n' "$busy"; exit 0`,
		`  else printf 'FLEETBUSY\t%s\n' "$busy"; exit 0`,
		`  fi`,
		`fi`,
		`if sudo systemctl suspend; then`,
		// The marker carries a field. fleetMarker reads `TOKEN<TAB>rest`, so a BARE
		// `FLEETSUSPENDED` line matched nothing and every successful suspend printed
		// `FLEET <name> UNREACHABLE no answer` and exited 3. Found 2026-09-18 by the first
		// test to drive the success path (`fleet sleep`); only the busy path was covered.
		`  printf 'FLEETSUSPENDED\tsystemctl suspend\n'`,
		`else`,
		`  printf 'FLEETFAIL\tsudo systemctl suspend failed\n'`,
		`fi`,
	}, "\n")
}

// --- fleet wake ----------------------------------------------------------------------

// FleetWakeInput is everything `fleet wake` needs, apart from flag parsing, so a test can
// drive it against a benches file, a fake ssh on PATH and a sender that captures the
// packet instead of opening a socket.
type FleetWakeInput struct {
	Benches  string
	Machines string // the machines registry; empty leaves the older refusals as the guard
	Names    []string
	SSH      string // the ssh program; empty is "ssh"
	Wait     time.Duration
	Timeout  time.Duration
	Max      int // at most this many FLEET lines; 0 is all
	Now      func() time.Time
	Sleep    func(time.Duration)
	Send     func(packet []byte, addr string) error
	Stdout   io.Writer
	Stderr   io.Writer
}

// FleetWake sends each named bench one Wake-on-LAN magic packet to its mac, then polls ssh
// until the bench answers, printing `FLEET <name> AWAKE wall=<s>` or
// `FLEET <name> WAKE TIMEOUT` (exit 3). A bench with no mac is refused (exit 2); every bench
// prints one FLEET line.
func FleetWake(in FleetWakeInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Sleep == nil {
		in.Sleep = time.Sleep
	}
	if in.Send == nil {
		in.Send = SendWakeOnLAN
	}
	if strings.TrimSpace(in.Benches) == "" {
		return refusal(in.Stderr, "FLEET", fmt.Errorf("missing --benches; refusing to guess (supply the fleet benches file)"))
	}
	benches, err := ReadFleetBenches(in.Benches)
	if err != nil {
		return refusal(in.Stderr, "FLEET", fmt.Errorf("%s (a benches file is name<TAB>ssh target<TAB>home<TAB>mac)", oneline.Err(err)))
	}
	if len(in.Names) == 0 {
		return refusal(in.Stderr, "FLEET", fmt.Errorf("missing --bench; refusing to guess (name the bench or benches to wake)"))
	}
	wait := in.Wait
	if wait <= 0 {
		wait = fleetPowerDefaultWait
	}
	reg, code := fleetRegistry(in.Machines, in.Stderr)
	if code != 0 {
		return code
	}

	results := make([]fleetPowerResult, len(in.Names))
	var wg sync.WaitGroup
	for i, name := range in.Names {
		if r, refused := fleetPowerRefusal(name, benches, reg); refused {
			results[i] = r
			continue
		}
		b := benches[name]
		if strings.TrimSpace(b.MAC) == "" || b.MAC == "-" {
			results[i] = fleetPowerResult{
				line:    "FLEET REFUSED bench=" + oneline.Field(name) + " no-mac",
				refused: true,
			}
			continue
		}
		packet, perr := MagicPacket(b.MAC)
		if perr != nil {
			results[i] = fleetPowerResult{
				line:    fmt.Sprintf("FLEET REFUSED bench=%s mac=%s", oneline.Field(name), oneline.Field(b.MAC)),
				refused: true,
			}
			continue
		}
		wg.Add(1)
		go func(i int, b FleetBench, packet []byte) {
			defer wg.Done()
			results[i] = in.wakeOne(b, packet, wait)
		}(i, b, packet)
	}
	wg.Wait()

	return fleetPowerPrint(in.Stdout, in.Max, results,
		"run: nova-pulse fleet wake --benches <file> --bench <names> --max 0")
}

// wakeOne is one bench: one magic packet, then a poll every fifteen seconds until the bench
// answers or the whole wait is gone.
func (in FleetWakeInput) wakeOne(b FleetBench, packet []byte, wait time.Duration) fleetPowerResult {
	start := in.Now()
	if err := in.Send(packet, fleetWOLAddress); err != nil {
		return fleetPowerResult{
			line:        fmt.Sprintf("FLEET %s WAKE ERROR %s", oneline.Field(b.Name), oneline.Escape(err.Error())),
			unreachable: true,
		}
	}
	deadline := start.Add(wait)
	for {
		if !in.Now().Before(deadline) {
			return fleetPowerResult{
				line:        fmt.Sprintf("FLEET %s WAKE TIMEOUT after %s", oneline.Field(b.Name), fleetWaitLabel(wait)),
				unreachable: true,
			}
		}
		in.Sleep(fleetPowerPollEvery)
		ctx, cancel := context.WithTimeout(context.Background(), fleetPowerTimeout(in.Timeout))
		out, err := fleetSSH(ctx, in.SSH, b.SSH, fleetWakePollScript)
		cancel()
		if err != nil {
			continue
		}
		if strings.Contains(out, "AWAKE") {
			wall := int64(in.Now().Sub(start).Seconds())
			return fleetPowerResult{
				line: fmt.Sprintf("FLEET %s AWAKE wall=%d", oneline.Field(b.Name), wall),
			}
		}
	}
}

// fleetWakePollScript is the remote poll: any answer from a booted bench carries AWAKE.
const fleetWakePollScript = "echo AWAKE"

// --- fleet reboot --------------------------------------------------------------------

// FleetRebootInput is everything `fleet reboot` needs: the file, the names, the ssh path,
// the wait and child bound, and the clock and sleep a test replaces.
type FleetRebootInput struct {
	Benches  string
	Machines string // the machines registry; empty leaves the older refusals as the guard
	Names    []string
	SSH      string // the ssh program; empty is "ssh"
	Wait     time.Duration
	Timeout  time.Duration
	Max      int // at most this many FLEET lines; 0 is all
	Now      func() time.Time
	Sleep    func(time.Duration)
	Stdout   io.Writer
	Stderr   io.Writer
}

// fleetRebootResult is one bench's one line and how it went.
type fleetRebootResult struct {
	line        string
	refused     bool
	unreachable bool
}

// FleetReboot boots each named bench, waits for its runners, and prints one FLEET line per
// bench. A bench not in the file, and `studio` by name, are refused (exit 2); a bench that
// never answers is unreachable (exit 3). Refusals are read before any ssh starts.
func FleetReboot(in FleetRebootInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Sleep == nil {
		in.Sleep = time.Sleep
	}
	wait := in.Wait
	if wait <= 0 {
		wait = fleetDefaultWait
	}
	benches, err := ReadFleetBenches(in.Benches)
	if err != nil {
		fmt.Fprintf(in.Stderr, "nova-pulse fleet reboot: %s; refusing to guess\n", oneline.Err(err))
		return 2
	}

	reg, code := fleetRegistry(in.Machines, in.Stderr)
	if code != 0 {
		return code
	}

	results := make([]fleetRebootResult, len(in.Names))
	var wg sync.WaitGroup
	for i, name := range in.Names {
		results[i] = fleetRebootRefusal(name, benches, reg)
		if results[i].refused {
			continue
		}
		b := benches[name]
		wg.Add(1)
		go func(i int, b FleetBench) {
			defer wg.Done()
			results[i] = in.rebootOne(b, wait)
		}(i, b)
	}
	wg.Wait()

	exit := 0
	printed := 0
	for _, r := range results {
		if in.Max > 0 && printed >= in.Max {
			break
		}
		fmt.Fprintln(in.Stdout, r.line)
		printed++
		switch {
		case r.unreachable:
			exit = 3
		case r.refused:
			if exit == 0 {
				exit = 2
			}
		}
	}
	return exit
}

// fleetRebootRefusal reads one requested name against the file. `studio` is refused for any
// admin act; a name the file does not carry is refused rather than guessed.
func fleetRebootRefusal(name string, benches map[string]FleetBench, reg *fleet.Registry) fleetRebootResult {
	if strings.EqualFold(strings.TrimSpace(name), "studio") {
		return fleetRebootResult{line: "FLEET REFUSED bench=" + oneline.Field(name), refused: true}
	}
	if _, ok := benches[name]; !ok {
		return fleetRebootResult{line: "FLEET REFUSED bench=" + oneline.Field(name), refused: true}
	}
	if line, refused := fleetRoleRefusal(name, reg); refused {
		return fleetRebootResult{line: line, refused: true}
	}
	return fleetRebootResult{}
}

// rebootOne is one bench: the reboot command, then a poll every fifteen seconds until the
// runners answer or the whole wait is gone.
func (in FleetRebootInput) rebootOne(b FleetBench, wait time.Duration) fleetRebootResult {
	start := in.Now()
	deadline := start.Add(wait)
	_, _ = in.ssh(b.SSH, "sudo systemctl reboot 2>/dev/null || sudo reboot 2>/dev/null")
	for {
		if !in.Now().Before(deadline) {
			return fleetRebootResult{
				line:        fmt.Sprintf("FLEET %s REBOOT TIMEOUT after %s", oneline.Field(b.Name), fleetWaitLabel(wait)),
				unreachable: true,
			}
		}
		in.Sleep(fleetPollEvery)
		out, err := in.ssh(b.SSH, fleetPollScript)
		if err != nil {
			continue
		}
		if n, ok := fleetReady(out); ok {
			wall := int64(in.Now().Sub(start).Seconds())
			return fleetRebootResult{
				line: fmt.Sprintf("FLEET %s REBOOTED wall=%d runners=%d", oneline.Field(b.Name), wall, n),
			}
		}
	}
}

// ssh runs one remote script on the bench with the ssh program from --ssh, bounded by
// --timeout. The target is the benches file's ssh column; the script is the remote command.
func (in FleetRebootInput) ssh(target, script string) (string, error) {
	program := in.SSH
	if program == "" {
		program = "ssh"
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = fleetDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, script)
	raw, err := cmd.CombinedOutput()
	return string(raw), err
}

// fleetReady reads the poll script's answer: `READY <n>` when the runners are listening,
// anything else is not yet.
func fleetReady(out string) (int, bool) {
	fields := strings.Fields(out)
	for i, f := range fields {
		if f != "READY" || i+1 >= len(fields) {
			continue
		}
		if n, err := strconv.Atoi(fields[i+1]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// fleetPollScript is the remote poll: nova-runner-1 must be active (system or user scope),
// and then the active runner services are counted.
const fleetPollScript = `if systemctl is-active --quiet nova-runner-1.service 2>/dev/null || systemctl --user is-active --quiet nova-runner-1.service 2>/dev/null; then
  n=0
  for u in $(systemctl list-units --type=service --all --no-legend 'nova-runner-*.service' 2>/dev/null | awk '{print $1}'); do
    if systemctl is-active --quiet "$u" 2>/dev/null || systemctl --user is-active --quiet "$u" 2>/dev/null; then n=$((n+1)); fi
  done
  [ "$n" -lt 1 ] && n=1
  echo "READY $n"
else
  echo "WAIT"
fi`

// fleetWaitLabel prints the wait the way a person said it: whole minutes as `5m`, else the
// duration's own spelling.
func fleetWaitLabel(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.String()
}

// --- fleet secrets -------------------------------------------------------------------

// FleetSecretsInput is everything the fleet secrets verb needs, apart from flag
// parsing, so a test can drive it against a benches file and a fake ssh on PATH.
type FleetSecretsInput struct {
	Benches string
	SSH     string
	Timeout time.Duration
	Max     int
	Stdout  io.Writer
	Stderr  io.Writer
}

// fleetBench is one row of the benches file: name<TAB>ssh-target<TAB>home<TAB>mac.
type fleetBench struct {
	Name   string
	Target string
	Home   string
	Mac    string
}

// readFleetBenches reads the shared fleet benches file. A line that is blank or starts
// with `#` is skipped; every other line needs at least name, ssh-target and home.
func readFleetBenches(path string) ([]fleetBench, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []fleetBench
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			return nil, fmt.Errorf("line %d wants name, ssh-target and home, tab separated, got %d field(s)", i+1, len(f))
		}
		b := fleetBench{Name: f[0], Target: f[1], Home: f[2]}
		if len(f) > 3 {
			b.Mac = f[3]
		}
		if b.Name == "" || b.Target == "" || b.Home == "" {
			return nil, fmt.Errorf("line %d has an empty name, ssh-target or home", i+1)
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no benches in %s", path)
	}
	return out, nil
}

// fleetSeat is one bench's answer, after the remote seat script has run.
type fleetSeat struct {
	status string // OK, REFUSED, NO-SEAT, UNREACHABLE
	seat   string
	head   string
	names  string
	reason string
}

// fleetSeatScript is the remote script, run through `ssh <target> bash -s`. It sets
// HOME to the bench's home column so `~` is the bench's own home, pulls the store,
// counts the seat keys, and runs the check for the one seat it found. It prints exactly
// one tab-separated FLEETSEAT line; the Go side is the only place a line is formatted.
func fleetSeatScript(home string) string {
	return strings.Join([]string{
		"HOME=" + fleetQuote(home),
		"export HOME",
		`store="$HOME/nova-bench/secrets"`,
		`git -C "$store" pull --ff-only >/dev/null 2>&1`,
		`n=0`,
		`keys=""`,
		`for k in "$HOME"/.config/nova-secrets/*.key; do`,
		`  [ -e "$k" ] || continue`,
		`  n=$((n+1))`,
		`  keys="$k"`,
		`done`,
		`if [ "$n" -ne 1 ]; then`,
		`  printf 'FLEETSEAT\tNO-SEAT\t-\t-\t%s\tfound %s keys\n' "$n" "$n"`,
		`  exit 0`,
		`fi`,
		`seat=$(basename "$keys")`,
		`seat=${seat%.key}`,
		`if out=$(nova-secrets check --store "$store" --as "$seat" --key "$keys" --sops "$HOME/.local/bin/sops" 2>&1); then`,
		`  head=$(printf '%s\n' "$out" | sed -n 's/.*head=\([0-9A-Za-z]*\).*/\1/p' | head -n 1)`,
		`  names=$(printf '%s\n' "$out" | sed -n 's/.*keys=\([0-9]*\).*/\1/p' | head -n 1)`,
		`  if [ -z "$names" ]; then`,
		`    nout=$(nova-secrets names --store "$store" --as "$seat" 2>/dev/null || true)`,
		`    names=$(printf '%s\n' "$nout" | sed -n 's/.*keys=\([0-9]*\).*/\1/p' | head -n 1)`,
		`  fi`,
		`  [ -n "$names" ] || names=1`,
		`  printf 'FLEETSEAT\tOK\t%s\t%s\t%s\t\n' "$seat" "$head" "$names"`,
		`else`,
		`  reason=$(printf '%s' "$out" | tr '\n\r\t' '   ')`,
		`  printf 'FLEETSEAT\tREFUSED\t%s\t-\t-\t%s\n' "$seat" "$reason"`,
		`fi`,
	}, "\n")
}

// fleetSeatParse reads the one FLEETSEAT line the remote script prints.
func fleetSeatParse(out string) (fleetSeat, bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 6)
		if len(f) != 6 || f[0] != "FLEETSEAT" {
			continue
		}
		s := fleetSeat{status: f[1], seat: f[2], head: f[3], names: f[4], reason: f[5]}
		return s, true
	}
	return fleetSeat{}, false
}

// checkFleetSeat runs one bench's seat script under the per-bench timeout.
func checkFleetSeat(ctx context.Context, ssh string, b fleetBench) fleetSeat {
	cmd := exec.CommandContext(ctx, ssh, b.Target, "bash", "-s")
	cmd.Stdin = strings.NewReader(fleetSeatScript(b.Home))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fleetSeat{status: "UNREACHABLE", reason: "timeout"}
	}
	if err != nil {
		reason := lastLine(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return fleetSeat{status: "UNREACHABLE", reason: reason}
	}
	if s, ok := fleetSeatParse(stdout.String()); ok {
		return s
	}
	return fleetSeat{status: "UNREACHABLE", reason: "no answer"}
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

// FleetSecrets runs the seat check on every bench in parallel and prints one FLEET
// line per bench. It exits 0 when every seat checked OK, 2 when any refused or found no
// seat, and 3 when any bench was unreachable.
func FleetSecrets(in FleetSecretsInput) int {
	if in.SSH == "" {
		in.SSH = "ssh"
	}
	if in.Timeout <= 0 {
		in.Timeout = 120 * time.Second
	}
	if strings.TrimSpace(in.Benches) == "" {
		return refusal(in.Stderr, "FLEET", fmt.Errorf("missing --benches; refusing to guess (supply the fleet benches file)"))
	}
	benches, err := readFleetBenches(in.Benches)
	if err != nil {
		return refusal(in.Stderr, "FLEET", fmt.Errorf("%s (a benches file is name, ssh-target, home, mac per line)", oneline.Err(err)))
	}

	results := make([]fleetSeat, len(benches))
	var wg sync.WaitGroup
	for i, b := range benches {
		wg.Add(1)
		go func(i int, b fleetBench) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), in.Timeout)
			defer cancel()
			results[i] = checkFleetSeat(ctx, in.SSH, b)
		}(i, b)
	}
	wg.Wait()

	list := bounded.Capped(in.Stdout, in.Max, "FLEET", "bench",
		"run: nova-pulse fleet secrets --benches <file> --max 0")
	exit := 0
	for i, b := range benches {
		r := results[i]
		switch r.status {
		case "OK":
			list.Line(fmt.Sprintf("FLEET %s SEAT %s check=OK head=%s names=%s",
				oneline.Field(b.Name), oneline.Field(r.seat), oneline.Field(fleetShortHead(r.head)), oneline.Field(r.names)))
		case "NO-SEAT":
			n := r.names
			if n == "" {
				n = "0"
			}
			list.Line(fmt.Sprintf("FLEET %s NO-SEAT (found %s keys)", oneline.Field(b.Name), oneline.Field(n)))
			if exit < 2 {
				exit = 2
			}
		case "REFUSED":
			list.Line(fmt.Sprintf("FLEET %s SEAT %s check=REFUSED %s",
				oneline.Field(b.Name), oneline.Field(r.seat), oneline.Escape(r.reason)))
			if exit < 2 {
				exit = 2
			}
		default:
			list.Line(fmt.Sprintf("FLEET %s UNREACHABLE %s",
				oneline.Field(b.Name), oneline.Escape(r.reason)))
			exit = 3
		}
	}
	list.More()
	return exit
}

// fleetShortHead is the store head as the line prints it: the first eight hex.
func fleetShortHead(head string) string {
	if len(head) > 8 {
		return head[:8]
	}
	return head
}
