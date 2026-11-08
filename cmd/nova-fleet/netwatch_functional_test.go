//go:build functional

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// roles/netwatch/files/nova-netwatch, the self-heal ladder's tick (netwatch.yml; model tla/NetWatch.tla). Each case
// drives ticks against a faked gateway ping, a faked rx_packets counter and a faked tailscale, and checks the branch
// the model names: misses to the threshold, then restart tailscaled, renegotiate, link bounce, driver reload and
// reboot with the backoff between them; recovery resets the ladder; a router outage (no ping, packets still
// arriving) is no miss; a lost tailnet with a working LAN is a miss; a protected job or a hold.d file defers the
// reboot and, past the limit, raises the alert; the reboot budget is per window.
//
// Hermetic: the roots (/run, /var/lib, /etc, /sys/class/net) are under the test's directory, the clock is
// NETWATCH_NOW, and ping, ip, ethtool, modprobe, systemctl, logger, pgrep, sleep, timeout and tailscale are stubs
// on PATH that record their calls. Nothing touches a real interface, unit, tailnet or clock.
//
// Every tick appends its record before, its inputs and its record after to a trace (NETWATCH_TRACE).
// TestFunctionalNetwatch renders the ticks of all cases as tla/NetWatchLog.tla and requires the committed file to
// be the same; NetWatchTrace.tla checks every one of them is a Step of the model. NETWATCH_UPDATE_LOG=1 rewrites
// the file.

type nw struct {
	t     *testing.T
	dir   string
	env   []string
	trace string
}

const nwHealthyStatus = `{"BackendState":"Running","Self":{"Online":true},"Peer":{"k2":{"Online":true,"TailscaleIPs":["100.64.0.2","fd7a::2"]},"k3":{"Online":false,"TailscaleIPs":["100.64.0.3"]}}}`

var nwStubs = map[string]string{
	"ethtool":   `echo "ethtool $*" >> "$CALLS"`,
	"modprobe":  `echo "modprobe $*" >> "$CALLS"`,
	"systemctl": `echo "systemctl $*" >> "$CALLS"`,
	"ip": `if [ "$1 $2 $3" = "-4 route show" ]; then echo "default via 192.168.1.1 dev eno1 proto dhcp"; exit 0; fi
echo "ip $*" >> "$CALLS"`,
	"ping": `[ -e "$PINGOK" ]`,
	"logger": `pri=""; while [ $# -gt 0 ]; do case "$1" in -t) shift 2 ;; -p) pri=$2; shift 2 ;; *) break ;; esac; done
echo "$pri $*" >> "$LOGF"`,
	"pgrep":   `[ "$1" = -f ] && shift; [ "$1" = -- ] && shift; grep -Eq -- "$1" "$PROCS"`,
	"sleep":   `exit 0`,
	"timeout": `shift; exec "$@"`,
	"tailscale": `echo "tailscale $*" >> "$TSCALLS"
case "$1" in
status) [ -e "$TS/hang" ] && exit 124; [ -e "$TS/status" ] || exit 1; cat "$TS/status" ;;
ping) for a; do last=$a; done; grep -qx -- "$last" "$TS/answer" ;;
*) exit 2 ;;
esac`,
}

func newNW(t *testing.T) *nw {
	t.Helper()
	d := t.TempDir()
	n := &nw{t: t, dir: d, trace: filepath.Join(d, "trace")}
	for _, p := range []string{"stubs", "sys/eno1/statistics", "sys/eno1/device", "drivers/atlantic", "run", "lib", "etc", "ts"} {
		n.mkdir(p)
	}
	if err := os.Symlink(filepath.Join(d, "drivers/atlantic"), filepath.Join(d, "sys/eno1/device/driver")); err != nil {
		t.Fatal(err)
	}
	n.write("sys/eno1/statistics/rx_packets", "1000\n")
	for _, f := range []string{"procs", "calls", "log", "tscalls"} {
		n.write(f, "")
	}
	for name, body := range nwStubs {
		if err := os.WriteFile(filepath.Join(d, "stubs", name), []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	n.write("etc/busy-patterns", "\\.cache/nova-gate\nrefs/nova-gate/\n")
	n.write("ts/status", nwHealthyStatus)
	n.write("ts/answer", "100.64.0.2\n")
	n.env = append(os.Environ(),
		"PATH="+filepath.Join(d, "stubs")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLS="+filepath.Join(d, "calls"), "LOGF="+filepath.Join(d, "log"), "PROCS="+filepath.Join(d, "procs"),
		"PINGOK="+filepath.Join(d, "ping_ok"), "TS="+filepath.Join(d, "ts"), "TSCALLS="+filepath.Join(d, "tscalls"),
		"NETWATCH_RUN="+filepath.Join(d, "run"), "NETWATCH_LIB="+filepath.Join(d, "lib"),
		"NETWATCH_ETC="+filepath.Join(d, "etc"), "NETWATCH_SYS="+filepath.Join(d, "sys"),
		"NETWATCH_TRACE="+n.trace, "NETWATCH_NOW=100000", "NETWATCH_THRESHOLD=2", "NETWATCH_BACKOFF=1",
		"NETWATCH_BUSY_BACKOFF=1", "NETWATCH_BUSY_WAIT_LIMIT=2", "NETWATCH_MAX_REBOOTS=2", "NETWATCH_WINDOW=21600",
		"NETWATCH_DRY=0", "NETWATCH_TAILNET_IF=eno1", "NETWATCH_TAILNET_PEERS=", "NETWATCH_TAILNET_TRIES=3")
	return n
}

func (n *nw) path(p string) string { return filepath.Join(n.dir, p) }

func (n *nw) mkdir(p string) {
	if err := os.MkdirAll(n.path(p), 0o755); err != nil {
		n.t.Fatal(err)
	}
}

func (n *nw) write(p, s string) {
	if err := os.WriteFile(n.path(p), []byte(s), 0o644); err != nil {
		n.t.Fatal(err)
	}
}

func (n *nw) read(p string) string {
	b, err := os.ReadFile(n.path(p))
	if err != nil && !os.IsNotExist(err) {
		n.t.Fatal(err)
	}
	return string(b)
}

func (n *nw) exists(p string) bool { _, err := os.Stat(n.path(p)); return err == nil }

func (n *nw) remove(p string) {
	if err := os.Remove(n.path(p)); err != nil && !os.IsNotExist(err) {
		n.t.Fatal(err)
	}
}

func (n *nw) setenv(kv string) { n.env = append(n.env, kv) }

func (n *nw) tick() {
	n.t.Helper()
	cmd := exec.Command("bash", "roles/netwatch/files/nova-netwatch", "eno1")
	cmd.Env = n.env
	if out, err := cmd.CombinedOutput(); err != nil {
		n.t.Fatalf("tick: %v: %s", err, out)
	}
}

func (n *nw) ticks(k int) {
	n.t.Helper()
	for range k {
		n.tick()
	}
}

func (n *nw) state() string { return strings.TrimSpace(n.read("run/eno1")) }

func (n *nw) calls() []string { return nwLines(n.read("calls")) }

func (n *nw) logHas(s string) bool { return strings.Contains(n.read("log"), s) }

func nwLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// climbToReboot runs the misses to the threshold (2) and rungs 1 to 4 with their backoff (1, 2, 3, 4 ticks).
func (n *nw) climbToReboot() {
	n.t.Helper()
	n.ticks(16)
	if c := n.calls(); len(c) != 6 || c[0] != "systemctl restart tailscaled" {
		n.t.Fatalf("after the climb the calls are %q, want the 4 rungs' 6 calls", c)
	}
}

func (n *nw) last() string {
	c := n.calls()
	if len(c) == 0 {
		return ""
	}
	return c[len(c)-1]
}

var nwCases = []struct {
	name string
	run  func(t *testing.T, n *nw)
}{
	{"a healthy tick changes nothing and acts on nothing", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		n.ticks(3)
		if n.state() != "0 0 0 0 1000" || len(n.calls()) != 0 || n.read("log") != "" {
			t.Fatalf("state %q calls %q log %q", n.state(), n.calls(), n.read("log"))
		}
	}},
	{"a failed ping while packets still arrive is no miss (a router outage walks nobody up the ladder)", func(t *testing.T, n *nw) {
		n.tick()
		for _, rx := range []int{1010, 1020, 1030, 1040, 1050, 1060, 1070, 1080} {
			n.write("sys/eno1/statistics/rx_packets", fmt.Sprintf("%d\n", rx))
			n.tick()
		}
		if n.state() != "0 0 0 0 1080" || len(n.calls()) != 0 {
			t.Fatalf("state %q calls %q", n.state(), n.calls())
		}
	}},
	{"the ladder in order, with the backoff between rungs, ends in one reboot", func(t *testing.T, n *nw) {
		n.ticks(2)
		if !n.logHas("warning NETWATCH eno1 MISS threshold reached lan=no gw=192.168.1.1 rx=1000 tailnet=yes") || len(n.calls()) != 0 {
			t.Fatalf("threshold: log %q calls %q", n.read("log"), n.calls())
		}
		want := []string{"systemctl restart tailscaled"}
		step := func(ticks int, add ...string) {
			t.Helper()
			n.ticks(ticks)
			want = append(want, add...)
			if got := n.calls(); strings.Join(got, ";") != strings.Join(want, ";") {
				t.Fatalf("calls %q, want %q", got, want)
			}
		}
		step(1)                                                 // rung 1, wait 1
		step(1)                                                 // the backoff
		step(1, "ethtool -r eno1")                              // rung 2, wait 2
		step(2)                                                 // the backoff
		step(1, "ip link set eno1 down", "ip link set eno1 up") // rung 3, wait 3
		step(3)                                                 // the backoff
		step(1, "modprobe -r atlantic", "modprobe atlantic")    // rung 4, wait 4: the interface's driver
		step(4)                                                 // the backoff
		step(1, "systemctl reboot")                             // rung 5
		if !n.logHas("crit NETWATCH eno1 RUNG 5 reboot (1/2 in window)") || strings.TrimSpace(n.read("lib/reboots")) != "100000" {
			t.Fatalf("log %q reboots %q", n.read("log"), n.read("lib/reboots"))
		}
		if n.state() != "0 0 0 0 1000" || n.exists("lib/ALERT") {
			t.Fatalf("after the reboot: state %q alert %v", n.state(), n.exists("lib/ALERT"))
		}
	}},
	{"recovery mid-ladder resets it and says so", func(t *testing.T, n *nw) {
		n.ticks(3)
		n.write("ping_ok", "")
		n.tick()
		if !n.logHas("notice NETWATCH eno1 RECOVERED misses=2 rung=1") || n.state() != "0 0 0 0 1000" {
			t.Fatalf("log %q state %q", n.read("log"), n.state())
		}
		n.remove("ping_ok")
		n.ticks(2)
		if len(n.calls()) != 1 {
			t.Fatalf("a new outage starts from zero misses: calls %q", n.calls())
		}
	}},
	{"a counter that drops (driver reload resets it) is not taken for traffic", func(t *testing.T, n *nw) {
		n.ticks(3)
		n.write("sys/eno1/statistics/rx_packets", "5\n")
		n.tick()
		if n.state() != "2 1 0 0 5" {
			t.Fatalf("state %q", n.state())
		}
	}},
	{"a protected job defers the reboot, then the alert, then the reboot once it ends", func(t *testing.T, n *nw) {
		n.write("procs", `sh -c set -e gate_root="$HOME"/.cache/nova-gate gate_dir=$(mktemp -d "$gate_root"/run.XXXXXX)`+"\n")
		n.climbToReboot()
		n.tick()
		if !n.logHas("warning NETWATCH eno1 DEFER reboot for a protected job (1/2)") || n.last() == "systemctl reboot" {
			t.Fatalf("deferral 1: log %q calls %q", n.read("log"), n.calls())
		}
		n.ticks(2)
		if !n.logHas("crit NETWATCH eno1 ALERT reboot deferred 2 times for a protected job") || !n.exists("lib/ALERT") || n.last() == "systemctl reboot" {
			t.Fatalf("deferral 2: log %q calls %q", n.read("log"), n.calls())
		}
		n.ticks(2)
		if c := strings.Count(n.read("log"), "ALERT"); c != 1 {
			t.Fatalf("the alert is raised once, got %d", c)
		}
		n.write("procs", "")
		n.ticks(2)
		if n.last() != "systemctl reboot" {
			t.Fatalf("the job ended: calls %q", n.calls())
		}
	}},
	{"a process matching a busy pattern counts as a protected job", func(t *testing.T, n *nw) {
		n.write("procs", "git -C /srv/mirrors/tools fetch --quiet --no-tags origin refs/nova-gate/v15-abcdef123456\n")
		n.climbToReboot()
		n.tick()
		if !n.logHas("DEFER reboot") || n.last() == "systemctl reboot" {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
	}},
	{"an unrelated process does not defer the reboot", func(t *testing.T, n *nw) {
		n.write("procs", "go test ./... -count=1\n")
		n.climbToReboot()
		n.tick()
		if n.last() != "systemctl reboot" {
			t.Fatalf("calls %q", n.calls())
		}
	}},
	{"a hold.d file is the manual hold", func(t *testing.T, n *nw) {
		n.mkdir("run/hold.d")
		n.write("run/hold.d/operator", "")
		n.climbToReboot()
		n.tick()
		if !n.logHas("DEFER reboot") || n.last() == "systemctl reboot" {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
		n.remove("run/hold.d/operator")
		n.ticks(2)
		if n.last() != "systemctl reboot" {
			t.Fatalf("released: calls %q", n.calls())
		}
	}},
	{"the reboot budget is per window: spent, the alert and no reboot; older reboots do not count", func(t *testing.T, n *nw) {
		n.write("lib/reboots", "99000\n99500\n")
		n.climbToReboot()
		n.tick()
		if !n.logHas("crit NETWATCH eno1 ALERT reboot budget spent (2 in 21600s)") || n.last() == "systemctl reboot" {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
		n.write("lib/reboots", "1000\n2000\n")
		n.write("calls", "")
		n.remove("run/eno1")
		n.climbToReboot()
		n.tick()
		if n.last() != "systemctl reboot" {
			t.Fatalf("calls %q", n.calls())
		}
	}},
	{"a healthy tick clears the alert", func(t *testing.T, n *nw) {
		n.write("lib/reboots", "99000\n99500\n")
		n.climbToReboot()
		n.tick()
		if !n.exists("lib/ALERT") {
			t.Fatal("no alert")
		}
		n.write("ping_ok", "")
		n.tick()
		if n.exists("lib/ALERT") || !n.logHas("RECOVERED") {
			t.Fatalf("alert %v log %q", n.exists("lib/ALERT"), n.read("log"))
		}
	}},
	{"dry mode logs every action and takes none", func(t *testing.T, n *nw) {
		n.setenv("NETWATCH_DRY=1")
		n.ticks(17)
		dry := strings.Count(n.read("log"), " DRY ")
		if len(n.calls()) != 0 || dry != 7 {
			t.Fatalf("calls %q, %d DRY lines in %q", n.calls(), dry, n.read("log"))
		}
		for _, s := range []string{"DRY systemctl restart tailscaled", "DRY ethtool -r eno1", "DRY modprobe -r atlantic", "DRY systemctl reboot"} {
			if !n.logHas(s) {
				t.Errorf("no %q in %q", s, n.read("log"))
			}
		}
	}},
	// ---- the tailnet ----
	{"a tailnet whose peers do not answer, with the LAN working, is a miss and restarts tailscaled", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		n.write("ts/answer", "")
		n.ticks(2)
		if !n.logHas("MISS threshold reached lan=yes gw=192.168.1.1 rx=1000 tailnet=no (no peer answered)") || len(n.calls()) != 0 {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
		n.tick()
		if !n.logHas("warning NETWATCH eno1 RUNG 1 restart tailscaled (no peer answered)") || strings.Join(n.calls(), ";") != "systemctl restart tailscaled" {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
		n.write("ts/answer", "100.64.0.2\n")
		n.tick()
		if !n.logHas("RECOVERED misses=2 rung=1") || n.state() != "0 0 0 0 1000" {
			t.Fatalf("log %q state %q", n.read("log"), n.state())
		}
	}},
	{"a tailnet loss nothing else clears climbs to the reboot", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		n.write("ts/answer", "")
		n.climbToReboot()
		n.tick()
		if n.last() != "systemctl reboot" {
			t.Fatalf("calls %q", n.calls())
		}
	}},
	{"tailscale not Running, its node offline, or its status hanging is a miss", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		for _, c := range []struct{ status, why string }{
			{`{"BackendState":"NeedsLogin","Self":{"Online":false}}`, "(backend=NeedsLogin)"},
			{`{"BackendState":"Running","Self":{"Online":false},"Peer":{}}`, "(self offline)"},
			{`not json`, "(backend=unparsable)"},
			{"", "(status failed)"},
			{"hang", "(status failed)"},
		} {
			n.remove("ts/status")
			n.remove("ts/hang")
			n.remove("run/eno1")
			n.write("log", "")
			switch c.status {
			case "":
			case "hang":
				n.write("ts/hang", "")
			default:
				n.write("ts/status", c.status)
			}
			n.ticks(2)
			if !n.logHas("lan=yes gw=192.168.1.1 rx=1000 tailnet=no " + c.why) {
				t.Errorf("status %q: log %q", c.status, n.read("log"))
			}
		}
	}},
	{"tailscale stopped by hand, or a tailnet with no peer online, is no miss", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		for _, s := range []string{`{"BackendState":"Stopped"}`, `{"BackendState":"Running","Self":{"Online":true},"Peer":{"k3":{"Online":false,"TailscaleIPs":["100.64.0.3"]}}}`} {
			n.write("ts/status", s)
			n.ticks(3)
			if n.state() != "0 0 0 0 1000" || len(n.calls()) != 0 {
				t.Fatalf("status %s: state %q calls %q", s, n.state(), n.calls())
			}
		}
	}},
	{"the peers named are pinged in place of the status's, at most the tries", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		n.write("ts/answer", "")
		n.setenv("NETWATCH_TAILNET_PEERS=peer-a peer-b peer-c peer-d")
		n.tick()
		pings := []string{}
		for _, l := range nwLines(n.read("tscalls")) {
			if strings.HasPrefix(l, "tailscale ping ") {
				pings = append(pings, l[strings.LastIndex(l, " ")+1:])
			}
		}
		if strings.Join(pings, " ") != "peer-a peer-b peer-c" || n.state() != "1 0 0 0 1000" {
			t.Fatalf("pings %q state %q", pings, n.state())
		}
		n.write("ts/answer", "peer-c\n")
		n.tick()
		if n.state() != "0 0 0 0 1000" {
			t.Fatalf("peer-c answers: state %q", n.state())
		}
	}},
	{"another interface's instance asks nothing of tailscale and restarts nothing at rung 1", func(t *testing.T, n *nw) {
		n.setenv("NETWATCH_TAILNET_IF=eth9")
		n.write("ts/answer", "")
		n.ticks(3)
		if n.read("tscalls") != "" || len(n.calls()) != 0 || !n.logHas("RUNG 1 restart tailscaled: not the tailnet's interface, nothing to do") {
			t.Fatalf("tscalls %q calls %q log %q", n.read("tscalls"), n.calls(), n.read("log"))
		}
		n.write("ping_ok", "")
		n.tick()
		if n.state() != "0 0 0 0 1000" {
			t.Fatalf("the LAN is back: state %q", n.state())
		}
	}},
}

func TestFunctionalNetwatch(t *testing.T) {
	t.Parallel()
	for _, b := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s is not installed on this machine", b)
		}
	}
	traces := make([]string, len(nwCases))
	t.Run("cases", func(t *testing.T) {
		for i, c := range nwCases {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				n := newNW(t)
				c.run(t, n)
				traces[i] = n.read("trace")
			})
		}
	})
	if t.Failed() {
		return
	}
	got := nwLog(traces)
	const file = "tla/NetWatchLog.tla"
	if os.Getenv("NETWATCH_UPDATE_LOG") == "1" {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("the ticks the script took are not the ones %s holds: the script and the model have drifted. "+
			"Change both together, then rewrite the log and run TLC on it; run: NETWATCH_UPDATE_LOG=1 go test -tags functional -run TestFunctionalNetwatch ./cmd/nova-fleet && tests/tla/check.sh <jar> cmd/nova-fleet/tla", file)
	}
}

// nwLog renders the distinct ticks of the traces, in the order the cases first took them, as a TLA+ module.
func nwLog(traces []string) string {
	b := func(s string) string {
		if s == "1" {
			return "TRUE"
		}
		return "FALSE"
	}
	rec := func(f []string) string {
		return fmt.Sprintf("R(%s, %s, %s, %s, %s, %s)", f[0], f[1], f[2], f[3], f[4], b(f[5]))
	}
	seen := map[string]bool{}
	var rows []string
	for _, tr := range traces {
		for _, l := range nwLines(tr) {
			f := strings.Fields(l)
			if len(f) != 14 {
				return fmt.Sprintf("malformed trace line %q", l)
			}
			row := fmt.Sprintf("T(%s, %s, %s, %s)", rec(f[0:6]), b(f[6]), b(f[7]), rec(f[8:14]))
			if !seen[row] {
				seen[row] = true
				rows = append(rows, row)
			}
		}
	}
	return `------------------------------ MODULE NetWatchLog ------------------------------
(* The ticks roles/netwatch/files/nova-netwatch took in netwatch_functional_test.go, distinct and in the order *)
(* the cases first took them: the record before (misses, rung, wait, busyWaits, reboots, alerted), whether both *)
(* signals were up, whether a protected job ran, and the record after. Written by the test                    *)
(* (NETWATCH_UPDATE_LOG=1); NetWatchTrace.tla checks each is a Step of NetWatch.tla.                           *)
R(m, r, wt, bw, rb, a) == [misses |-> m, rung |-> r, wait |-> wt, busyWaits |-> bw, reboots |-> rb, alerted |-> a]
T(pre, h, b, post) == [pre |-> pre, healthy |-> h, busy |-> b, post |-> post]
TickLog == <<
    ` + strings.Join(rows, ",\n    ") + `
>>
=============================================================================
`
}
