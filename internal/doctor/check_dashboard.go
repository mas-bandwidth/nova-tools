package doctor

import (
	"context"
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"
	"time"
)

// dashboardTimeout bounds the dial of the dashboard's loopback port.
const dashboardTimeout = 3 * time.Second

// dashboardListen is the page's address when the loop's argv names no --listen
// (nova-sprint's DashboardListen).
const dashboardListen = "127.0.0.1:7390"

// loopMark is the line fleet/loops.yml writes into every unit it renders from a loop record.
const loopMark = "written by fleet/loops.yml from the loop record"

var (
	dashboardArgvRe   = regexp.MustCompile(`nova-sprint(?:</string>\s*<string>|\s+)dashboard\b`)
	dashboardListenRe = regexp.MustCompile(`--listen(?:</string>\s*<string>|\s+)([^<\s"]+)`)
)

func init() {
	Default.Register(Check{Name: "dashboard", Dependency: "the sprint dashboard's loop record", Fleet: true, Run: checkDashboard})
}

// checkDashboard finds the unit fleet/loops.yml rendered from the loop record that runs
// `nova-sprint dashboard` (launchd's com.nova.loop.<name>.plist or systemd's
// nova-loop-<name>.service, in the user's place or the system's) and dials the first
// loopback address of its --listen. ok when the unit is there and the port answers; a
// warn when no unit runs the dashboard on this machine, or a hand unit serves it in
// place of a loop record; a fail when the unit is there and the port does not answer
// (docs/SETUP.md, "The sprint dashboard").
func checkDashboard(ctx context.Context, env Env) Result {
	home := env.Getenv("HOME")
	unit, body, hand := dashboardUnit(env, home)
	fix := "nova-config loop add sprint-dashboard --machine <m> --argv '[\"env\",\"NOVA_SPRINT_SERVER=127.0.0.1:6390\",\"nova-sprint\",\"dashboard\"]' --keepalive true --as <name>, then nova-config apply and the fleet/loops.yml play"
	if unit == "" {
		evidence := "no loop record's unit runs nova-sprint dashboard on this machine"
		if hand != "" {
			evidence = fmt.Sprintf("%s is a hand unit serving the dashboard; no loop record's unit runs nova-sprint dashboard", hand)
		}
		return Result{Status: Warn, Evidence: evidence, Fix: fix}
	}
	addr := dashboardLoopback(body)
	if addr == "" {
		return Result{Status: OK, Evidence: fmt.Sprintf("%s runs nova-sprint dashboard and serves no page on loopback", unit)}
	}
	dctx, cancel := context.WithTimeout(ctx, dashboardTimeout)
	defer cancel()
	if err := env.Dial(dctx, "tcp", addr); err != nil {
		name := loopName(unit)
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("%s runs nova-sprint dashboard but %s does not answer: %v", unit, addr, err),
			Fix:      fmt.Sprintf("nova-config loop show %s, and read its log (~/Library/Logs/nova-loop-%s.log on darwin, journalctl --user -u nova-loop-%s on linux)", name, name, name)}
	}
	evidence := fmt.Sprintf("%s runs nova-sprint dashboard and %s answers", unit, addr)
	if hand != "" {
		return Result{Status: Warn, Evidence: evidence + "; " + hand + " is a hand unit beside it",
			Fix: "unload and remove " + hand + ": the dashboard runs as its loop record alone"}
	}
	return Result{Status: OK, Evidence: evidence}
}

// dashboardUnit is the loop record's unit that runs the dashboard and its text, and a
// unit another hand wrote that runs a dashboard (a name with "dashboard" in it and no
// loop mark), each "" when there is none.
func dashboardUnit(env Env, home string) (unit, body, hand string) {
	places := []string{"/Library/LaunchDaemons", "/etc/systemd/system"}
	if home != "" {
		places = append([]string{path.Join(home, "Library/LaunchAgents"), path.Join(home, ".config/systemd/user")}, places...)
	}
	for _, dir := range places {
		entries, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".plist") && !strings.HasSuffix(name, ".service") {
				continue
			}
			p := path.Join(dir, name)
			raw, err := env.ReadFile(p)
			if err != nil {
				continue
			}
			text := string(raw)
			switch {
			case strings.Contains(text, loopMark) && dashboardArgvRe.MatchString(text):
				if unit == "" {
					unit, body = p, text
				}
			case !strings.Contains(text, loopMark) && strings.Contains(strings.ToLower(name), "dashboard"):
				if hand == "" {
					hand = p
				}
			}
		}
	}
	return unit, body, hand
}

// dashboardLoopback is the first loopback address:port of the unit's --listen (its
// default when it names none), "" when it serves no page on loopback.
func dashboardLoopback(unit string) string {
	m := dashboardListenRe.FindStringSubmatch(unit)
	if m == nil {
		return dashboardListen
	}
	for _, addr := range strings.Split(m[1], ",") {
		host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
		if err != nil {
			continue
		}
		if ip := net.ParseIP(host); host == "localhost" || ip != nil && ip.IsLoopback() {
			return strings.TrimSpace(addr)
		}
	}
	return ""
}

// loopName is the loop record's name in its unit's file name: com.nova.loop.<name>.plist
// or nova-loop-<name>.service.
func loopName(unit string) string {
	name := path.Base(unit)
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".plist"), ".service")
	return strings.TrimPrefix(strings.TrimPrefix(name, "com.nova.loop."), "nova-loop-")
}
