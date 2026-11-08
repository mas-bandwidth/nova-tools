//go:build functional

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// roles/netwatch/files/nova-netwatch, the self-heal ladder's tick (netwatch.yml; model tla/NetWatch.tla). Each case
// drives ticks against a faked gateway ping, faked rx_packets counters, a faked tailscale and a faked coordination
// server, and checks the branch the model names: misses to the threshold, then restart tailscaled; a LAN miss then
// renegotiates, bounces the link, reloads the driver and reboots; a tailnet miss with the LAN working never touches
// the card: local, it reboots after the restart, remote, it raises the alert and does nothing; recovery resets the
// ladder; a router outage (no ping, packets still arriving) is no miss; a protected job or a hold.d file defers the
// reboot and, past the limit, raises the alert; the reboot budget halts the watch until a healthy tick or a person.
//
// Hermetic: the roots (/run, /var/lib, /etc, /sys/class/net) are under the test's directory, the clock is
// NETWATCH_NOW, and ping, ip, ethtool, modprobe, systemctl, logger, pgrep, sleep, timeout, curl and tailscale are
// stubs on PATH that record their calls. Nothing touches a real interface, unit, tailnet, network or clock.
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

// nwStatus is `tailscale status --json` in the shape tailscale 1.104 prints (every top-level, Self and Peer
// field, read from a bench), with neutral values: the backend state, whether the own node is online, and the
// peers as address -> online.
func nwStatus(backend string, selfOnline bool, peers map[string]bool) string {
	node := func(ip string, online bool) map[string]any {
		return map[string]any{
			"ID": "n" + ip, "PublicKey": "nodekey:" + ip, "HostName": "bench-" + ip, "DNSName": "bench-" + ip + ".example.ts.net.",
			"OS": "linux", "UserID": 1, "TailscaleIPs": []string{ip, "fd7a::" + ip}, "AllowedIPs": []string{ip + "/32"},
			"Tags": []string{"tag:bench"}, "Addrs": nil, "CurAddr": "", "Relay": "nyc", "PeerRelay": "", "RxBytes": 0,
			"TxBytes": 0, "Created": "2026-01-01T00:00:00Z", "LastWrite": "0001-01-01T00:00:00Z",
			"LastSeen": "0001-01-01T00:00:00Z", "LastHandshake": "0001-01-01T00:00:00Z", "Online": online,
			"ExitNode": false, "ExitNodeOption": false, "Active": false, "PeerAPIURL": []string{}, "TaildropTarget": 0,
			"NoFileSharingReason": "", "InNetworkMap": true, "InMagicSock": true, "InEngine": true, "NodeID": 1,
		}
	}
	self := node("100.64.0.1", selfOnline)
	self["Capabilities"], self["CapMap"] = []string{}, map[string]any{}
	ps := map[string]any{}
	for ip, on := range peers {
		ps["nodekey:"+ip] = node(ip, on)
	}
	b, _ := json.Marshal(map[string]any{
		"Version": "1.104.5", "TUN": true, "BackendState": backend, "HaveNodeKey": true, "AuthURL": "",
		"TailscaleIPs": []string{"100.64.0.1", "fd7a::1"}, "Self": self, "Health": []string{},
		"MagicDNSSuffix": "example.ts.net", "CurrentTailnet": map[string]any{"Name": "example.com", "MagicDNSSuffix": "example.ts.net", "MagicDNSEnabled": true},
		"CertDomains": []string{}, "Peer": ps, "User": map[string]any{}, "ClientVersion": nil, "ExtraRecords": nil,
	})
	return string(b)
}

var nwHealthyStatus = nwStatus("Running", true, map[string]bool{"100.64.0.2": true, "100.64.0.3": false})

var nwStubs = map[string]string{
	"ethtool":   `echo "ethtool $*" >> "$CALLS"`,
	"modprobe":  `echo "modprobe $*" >> "$CALLS"`,
	"systemctl": `echo "systemctl $*" >> "$CALLS"`,
	"ip": `if [ "$1 $2 $3" = "-4 route show" ]; then echo "default via 192.168.1.1 dev $6 proto dhcp"; exit 0; fi
echo "ip $*" >> "$CALLS"`,
	"ping": `if=""; while [ $# -gt 0 ]; do [ "$1" = -I ] && if=$2; shift; done
[ -e "$PINGOK" ] || [ -e "$PINGOK.$if" ]`,
	"logger": `pri=""; while [ $# -gt 0 ]; do case "$1" in -t) shift 2 ;; -p) pri=$2; shift 2 ;; *) break ;; esac; done
echo "$pri $*" >> "$LOGF"`,
	"pgrep":   `[ "$1" = -f ] && shift; [ "$1" = -- ] && shift; grep -Eq -- "$1" "$PROCS"`,
	"sleep":   `exit 0`,
	"timeout": `shift; exec "$@"`,
	"curl":    `for a; do last=$a; done; echo "curl $last" >> "$TSCALLS"; [ -e "$TS/control_ok" ]`,
	"tailscale": `echo "tailscale $*" >> "$TSCALLS"
case "$1" in
status) [ -e "$TS/hang" ] && exit 124; [ -e "$TS/status" ] || exit 1; cat "$TS/status" ;;
ping) for a; do last=$a; done; grep -qx -- "$last" "$TS/answer" ;;
debug) echo '{"ControlURL": "https://control.example"}' ;;
*) exit 2 ;;
esac`,
}

func newNW(t *testing.T) *nw {
	t.Helper()
	d := t.TempDir()
	n := &nw{t: t, dir: d, trace: filepath.Join(d, "trace")}
	for _, p := range []string{"stubs", "drivers/atlantic", "run", "lib", "etc", "ts"} {
		n.mkdir(p)
	}
	n.addInterface("eno1")
	for _, f := range []string{"procs", "calls", "log", "tscalls"} {
		n.write(f, "")
	}
	for name, body := range nwStubs {
		if err := os.WriteFile(filepath.Join(d, "stubs", name), []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	n.write("etc/busy-patterns", "Runner\\.Worker\nnova-reserve( --dir [^ ]+)? run \n")
	n.write("ts/status", nwHealthyStatus)
	n.write("ts/answer", "100.64.0.2\n")
	n.write("ts/control_ok", "")
	n.env = append(os.Environ(),
		"PATH="+filepath.Join(d, "stubs")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLS="+filepath.Join(d, "calls"), "LOGF="+filepath.Join(d, "log"), "PROCS="+filepath.Join(d, "procs"),
		"PINGOK="+filepath.Join(d, "ping_ok"), "TS="+filepath.Join(d, "ts"), "TSCALLS="+filepath.Join(d, "tscalls"),
		"NETWATCH_RUN="+filepath.Join(d, "run"), "NETWATCH_LIB="+filepath.Join(d, "lib"),
		"NETWATCH_ETC="+filepath.Join(d, "etc"), "NETWATCH_SYS="+filepath.Join(d, "sys"),
		"NETWATCH_TRACE="+n.trace, "NETWATCH_NOW=100000", "NETWATCH_THRESHOLD=2", "NETWATCH_BACKOFF=1",
		"NETWATCH_BUSY_BACKOFF=1", "NETWATCH_BUSY_WAIT_LIMIT=2", "NETWATCH_MAX_REBOOTS=2", "NETWATCH_WINDOW=21600",
		"NETWATCH_DRY=0", "NETWATCH_TAILNET_IF=eno1", "NETWATCH_TAILNET_PEERS=", "NETWATCH_TAILNET_TRIES=3",
		"NETWATCH_CONTROL_URL=")
	return n
}

// addInterface gives the faked /sys/class/net an interface with 1000 packets received, driven by atlantic.
func (n *nw) addInterface(name string) {
	n.mkdir("sys/" + name + "/statistics")
	n.mkdir("sys/" + name + "/device")
	if err := os.Symlink(n.path("drivers/atlantic"), n.path("sys/"+name+"/device/driver")); err != nil {
		n.t.Fatal(err)
	}
	n.write("sys/"+name+"/statistics/rx_packets", "1000\n")
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

func (n *nw) tickOn(iface string) {
	n.t.Helper()
	cmd := exec.Command("bash", "roles/netwatch/files/nova-netwatch", iface)
	cmd.Env = n.env
	if out, err := cmd.CombinedOutput(); err != nil {
		n.t.Fatalf("tick on %s: %v: %s", iface, err, out)
	}
}

func (n *nw) tick() { n.t.Helper(); n.tickOn("eno1") }

func (n *nw) ticks(k int) {
	n.t.Helper()
	for range k {
		n.tick()
	}
}

func (n *nw) state() string { return strings.TrimSpace(n.read("run/eno1")) }

func (n *nw) calls() []string { return nwLines(n.read("calls")) }

func (n *nw) logHas(s string) bool { return strings.Contains(n.read("log"), s) }

func (n *nw) alertKind() string {
	f := strings.Fields(n.read("lib/ALERT.eno1"))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func nwLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// climbToReboot runs a LAN miss to the threshold (2) and rungs 1 to 4 with their backoff (1, 2, 3, 4 ticks).
func (n *nw) climbToReboot() {
	n.t.Helper()
	before := len(n.calls())
	n.ticks(16)
	if c := n.calls()[before:]; len(c) != 6 || c[0] != "systemctl restart tailscaled" {
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
	{"a LAN miss climbs the ladder in order, with the backoff between rungs, to one reboot", func(t *testing.T, n *nw) {
		n.ticks(2)
		if !n.logHas("warning NETWATCH eno1 MISS threshold reached lan=0 gw=192.168.1.1 rx=1000 tailnet=skip") || len(n.calls()) != 0 {
			t.Fatalf("threshold: log %q calls %q", n.read("log"), n.calls())
		}
		if n.read("tscalls") != "" {
			t.Fatalf("the tailnet is not read while the LAN is down: %q", n.read("tscalls"))
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
		if !n.logHas("crit NETWATCH eno1 RUNG 5 reboot (1/2 in window, 1/2 in this outage; lan=0 tailnet=skip)") || strings.TrimSpace(n.read("lib/reboots")) != "100000" {
			t.Fatalf("log %q reboots %q", n.read("log"), n.read("lib/reboots"))
		}
		if n.state() != "0 0 0 0 1000" || strings.TrimSpace(n.read("lib/eno1.state")) != "1 0" || n.exists("lib/ALERT.eno1") {
			t.Fatalf("after the reboot: state %q persistent %q alert %v", n.state(), n.read("lib/eno1.state"), n.exists("lib/ALERT.eno1"))
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
		n.write("procs", "/home/bench/actions-runner/repo-1/bin/Runner.Worker spawnclient 107 110\n")
		n.climbToReboot()
		n.tick()
		if !n.logHas("warning NETWATCH eno1 DEFER reboot for a protected job (1/2)") || n.last() == "systemctl reboot" {
			t.Fatalf("deferral 1: log %q calls %q", n.read("log"), n.calls())
		}
		n.ticks(2)
		if !n.logHas("crit NETWATCH eno1 ALERT reboot deferred 2 times for a protected job") || n.alertKind() != "busy" || n.last() == "systemctl reboot" {
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
	{"a held reservation counts as a protected job", func(t *testing.T, n *nw) {
		n.write("procs", "nice -n 15 nova-reserve --dir /home/bench/.local/share/nova-reserve run --wait 5s --why card-12 -- nova-sandbox -- opencode run\n")
		n.climbToReboot()
		n.tick()
		if !n.logHas("DEFER reboot") || n.last() == "systemctl reboot" {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
	}},
	{"an unrelated process does not defer the reboot", func(t *testing.T, n *nw) {
		n.write("procs", "/home/bench/actions-runner/repo-1/bin/Runner.Listener run\nnova-reserve help\n")
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
	{"a spent window budget halts the watch; removing the alert by hand resumes it", func(t *testing.T, n *nw) {
		n.write("lib/reboots", "99000\n99500\n")
		n.climbToReboot()
		n.tick()
		if !n.logHas("crit NETWATCH eno1 ALERT reboot budget spent (2 in 21600s, 0 in this outage, at most 2)") || n.alertKind() != "halt" || n.last() == "systemctl reboot" {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
		n.ticks(30)
		if len(n.calls()) != 6 {
			t.Fatalf("halted, the watch does nothing: calls %q", n.calls())
		}
		n.setenv("NETWATCH_NOW=200000") // the two reboots have left the window: still halted
		n.tick()
		if len(n.calls()) != 6 {
			t.Fatalf("halted after the window: calls %q", n.calls())
		}
		n.remove("lib/ALERT.eno1")
		n.tick()
		if !n.logHas("RESUMED: the alert was removed by hand") || n.last() != "systemctl reboot" {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
	}},
	{"a fault that never clears reboots at most the budget, and no more as the reboots age out of the window", func(t *testing.T, n *nw) {
		n.climbToReboot()
		n.tick()
		n.setenv("NETWATCH_NOW=200000") // the first reboot has left the window
		n.climbToReboot()
		n.tick()
		if c := strings.Count(n.read("calls"), "systemctl reboot"); c != 2 {
			t.Fatalf("%d reboots", c)
		}
		n.climbToReboot()
		n.tick()
		if !n.logHas("ALERT reboot budget spent (1 in 21600s, 2 in this outage, at most 2)") || n.alertKind() != "halt" {
			t.Fatalf("log %q", n.read("log"))
		}
		n.setenv("NETWATCH_NOW=500000") // days later: the window is empty, the outage is not over
		n.ticks(40)
		if c := strings.Count(n.read("calls"), "systemctl reboot"); c != 2 || len(n.calls()) != 3*6+2 {
			t.Fatalf("halted: %d reboots, calls %q", c, n.calls())
		}
		n.write("ping_ok", "")
		n.tick()
		if n.exists("lib/ALERT.eno1") || strings.TrimSpace(n.read("lib/eno1.state")) != "0 0" || !n.logHas("RECOVERED") {
			t.Fatalf("a healthy tick ends the halt: alert %v persistent %q", n.exists("lib/ALERT.eno1"), n.read("lib/eno1.state"))
		}
	}},
	{"a healthy tick clears the alert", func(t *testing.T, n *nw) {
		n.write("lib/reboots", "99000\n99500\n")
		n.climbToReboot()
		n.tick()
		if !n.exists("lib/ALERT.eno1") {
			t.Fatal("no alert")
		}
		n.write("ping_ok", "")
		n.tick()
		if n.exists("lib/ALERT.eno1") || !n.logHas("RECOVERED") {
			t.Fatalf("alert %v log %q", n.exists("lib/ALERT.eno1"), n.read("log"))
		}
	}},
	{"each interface keeps its own alert", func(t *testing.T, n *nw) {
		n.addInterface("eth9")
		n.write("ping_ok.eth9", "")
		n.write("lib/reboots", "99000\n99500\n")
		n.climbToReboot()
		n.tick()
		n.tickOn("eth9")
		n.tickOn("eth9")
		if n.alertKind() != "halt" || n.exists("lib/ALERT.eth9") || strings.TrimSpace(n.read("run/eth9")) != "0 0 0 0 1000" {
			t.Fatalf("eno1 alert %q, eth9 alert %v, eth9 state %q", n.alertKind(), n.exists("lib/ALERT.eth9"), n.read("run/eth9"))
		}
		n.remove("ping_ok.eth9")
		n.tickOn("eth9")
		n.tickOn("eth9")
		n.tickOn("eth9")
		if !strings.Contains(n.read("log"), "NETWATCH eth9 RUNG 1 restart tailscaled: not the tailnet's interface, nothing to do") || n.alertKind() != "halt" {
			t.Fatalf("log %q", n.read("log"))
		}
	}},
	{"a state file that is not numbers is reset with a journal line, never taken as the top of the ladder", func(t *testing.T, n *nw) {
		for _, bad := range []string{"", "2 4", "x y z w v", "2 4 0 0 1000 extra", "-1 4 0 0 1000"} {
			n.write("run/eno1", bad)
			n.write("lib/eno1.state", "x")
			n.write("log", "")
			n.write("sys/eno1/statistics/rx_packets", "1000\n")
			n.tick()
			if n.state() != "1 0 0 0 1000" || len(n.calls()) != 0 || strings.Count(n.read("log"), "warning NETWATCH eno1 STATE reset:") != 2 {
				t.Fatalf("state file %q: state %q calls %q log %q", bad, n.state(), n.calls(), n.read("log"))
			}
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
	{"a local tailnet miss restarts tailscaled, and recovers if that clears it", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		n.write("ts/answer", "")
		n.ticks(2)
		if !n.logHas("MISS threshold reached lan=1 gw=192.168.1.1 rx=1000 tailnet=local (no peer answered, https://control.example answers)") || len(n.calls()) != 0 {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
		n.tick()
		if !n.logHas("warning NETWATCH eno1 RUNG 1 restart tailscaled (no peer answered, https://control.example answers)") || strings.Join(n.calls(), ";") != "systemctl restart tailscaled" {
			t.Fatalf("log %q calls %q", n.read("log"), n.calls())
		}
		n.write("ts/answer", "100.64.0.2\n")
		n.tick()
		if !n.logHas("RECOVERED misses=2 rung=1") || n.state() != "0 0 0 0 1000" {
			t.Fatalf("log %q state %q", n.read("log"), n.state())
		}
	}},
	{"a local tailnet miss the restart does not clear reboots, and never touches the card", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		n.write("ts/answer", "")
		n.ticks(5) // the threshold, rung 1, its backoff, the reboot
		if got := strings.Join(n.calls(), ";"); got != "systemctl restart tailscaled;systemctl reboot" {
			t.Fatalf("calls %q", got)
		}
		if !n.logHas("RUNG 5 reboot (1/2 in window, 1/2 in this outage; lan=1 tailnet=local (no peer answered, https://control.example answers))") {
			t.Fatalf("log %q", n.read("log"))
		}
	}},
	{"a remote tailnet miss restarts tailscaled once, then only the alert, and never reboots", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		n.write("ts/answer", "")
		n.remove("ts/control_ok")
		n.ticks(40)
		if got := strings.Join(n.calls(), ";"); got != "systemctl restart tailscaled" || n.alertKind() != "remote" {
			t.Fatalf("calls %q alert %q", got, n.alertKind())
		}
		if strings.Count(n.read("log"), "ALERT") != 1 || !n.logHas("crit NETWATCH eno1 ALERT the tailnet is down and the fault is not this machine's (no peer answered, https://control.example does not answer); nothing to do here") {
			t.Fatalf("log %q", n.read("log"))
		}
		n.write("ts/control_ok", "") // the coordination server is back and the peers still do not answer: the fault is local
		n.tick()
		if n.last() != "systemctl reboot" {
			t.Fatalf("calls %q", n.calls())
		}
	}},
	{"the tailnet's classes: what is local and what is remote", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		for _, c := range []struct {
			status, answer string
			control        bool
			want           string
		}{
			{nwStatus("NeedsLogin", false, nil), "", true, "tailnet=remote (backend=NeedsLogin: it needs a person to log in)"},
			{nwStatus("NeedsMachineAuth", false, nil), "", true, "tailnet=remote (backend=NeedsMachineAuth: it needs a person to log in)"},
			{nwStatus("Running", false, map[string]bool{"100.64.0.2": true}), "100.64.0.2", true, "tailnet=remote (own node offline, peers answer)"},
			{nwStatus("Running", false, map[string]bool{"100.64.0.2": true}), "", true, "tailnet=local (no peer answered, https://control.example answers)"},
			{nwStatus("Running", true, map[string]bool{"100.64.0.2": true}), "", false, "tailnet=remote (no peer answered, https://control.example does not answer)"},
			{nwStatus("Starting", true, nil), "", true, "tailnet=local (backend=Starting, https://control.example answers)"},
			{nwStatus("Starting", true, nil), "", false, "tailnet=remote (backend=Starting, https://control.example does not answer)"},
			{"not json", "", true, "tailnet=local (backend=unparsable, https://control.example answers)"},
			{"", "", true, "tailnet=local (status failed, https://control.example answers)"},
			{"hang", "", true, "tailnet=local (status failed, https://control.example answers)"},
		} {
			n.remove("ts/status")
			n.remove("ts/hang")
			n.remove("run/eno1")
			n.write("log", "")
			n.write("ts/answer", c.answer)
			n.remove("ts/control_ok")
			if c.control {
				n.write("ts/control_ok", "")
			}
			switch c.status {
			case "":
			case "hang":
				n.write("ts/hang", "")
			default:
				n.write("ts/status", c.status)
			}
			n.ticks(2)
			if !n.logHas("lan=1 gw=192.168.1.1 rx=1000 " + c.want) {
				t.Errorf("want %q: log %q", c.want, n.read("log"))
			}
		}
	}},
	{"the control URL is the one tailscale names, or the one configured", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		n.write("ts/answer", "")
		n.tick()
		n.setenv("NETWATCH_CONTROL_URL=https://headscale.example")
		n.tick()
		var urls []string
		for _, l := range nwLines(n.read("tscalls")) {
			if strings.HasPrefix(l, "curl ") {
				urls = append(urls, strings.TrimPrefix(l, "curl "))
			}
		}
		if strings.Join(urls, " ") != "https://control.example https://headscale.example" {
			t.Fatalf("curl %q", urls)
		}
	}},
	{"tailscale stopped by hand, or a tailnet with no peer online, is no miss", func(t *testing.T, n *nw) {
		n.write("ping_ok", "")
		for _, s := range []string{nwStatus("Stopped", false, nil), nwStatus("Running", true, map[string]bool{"100.64.0.3": false})} {
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
		return fmt.Sprintf("R(%s, %s, %s, %s, %s, %s, %s, %s)", f[0], f[1], f[2], f[3], f[4], f[5], b(f[6]), b(f[7]))
	}
	seen := map[string]bool{}
	var rows []string
	for _, tr := range traces {
		for _, l := range nwLines(tr) {
			f := strings.Fields(l)
			if len(f) != 19 {
				return fmt.Sprintf("malformed trace line %q", l)
			}
			row := fmt.Sprintf("T(%s, %s, %q, %s, %s)", rec(f[0:8]), b(f[8]), f[9], b(f[10]), rec(f[11:19]))
			if !seen[row] {
				seen[row] = true
				rows = append(rows, row)
			}
		}
	}
	return `------------------------------ MODULE NetWatchLog ------------------------------
(* The ticks roles/netwatch/files/nova-netwatch took in netwatch_functional_test.go, distinct and in the order *)
(* the cases first took them: the record before (misses, rung, wait, busyWaits, reboots, outage, alerted,      *)
(* halted), the LAN signal, the tailnet signal, whether a protected job ran, and the record after. Written by  *)
(* the test (NETWATCH_UPDATE_LOG=1); NetWatchTrace.tla checks each is a Step of NetWatch.tla.                  *)
R(m, r, wt, bw, rb, o, a, h) == [misses |-> m, rung |-> r, wait |-> wt, busyWaits |-> bw, reboots |-> rb,
                                 outage |-> o, alerted |-> a, halted |-> h]
T(pre, lan, tail, b, post) == [pre |-> pre, lan |-> lan, tail |-> tail, busy |-> b, post |-> post]
TickLog == <<
    ` + strings.Join(rows, ",\n    ") + `
>>
=============================================================================
`
}
