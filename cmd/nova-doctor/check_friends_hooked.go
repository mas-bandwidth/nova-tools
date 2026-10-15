// This file is nova-doctor's friends-hooked check: every friend on the
// nova-config roster has a hook record that is up and was hooked in by the
// installed nova-hook, a supervised hook service that is loaded, running and
// runs the installed build, and the heartbeat server's own stamp names the
// installed build too (docs/SPEC-DOCTOR.md, "friends-hooked").
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

func init() {
	doctor.Default.Register(doctor.Check{Name: "friends-hooked", Dependency: "the friend roster, every friend's hook record and hook service, and the heartbeat server's build", Run: checkFriendsHooked})
}

// rosterJSON is the part of `nova-config friend list --json` this check
// reads: one item per friend row. Items is a pointer so a document that
// carries no items at all is told apart from an empty roster.
type rosterJSON struct {
	Items *[]struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	} `json:"items"`
}

// friendsJSON is the part of `nova-hook friends --json` this check reads: the
// server facts and one item per name (pkg/tool's one output value). Items is
// a pointer for the same reason as rosterJSON's.
type friendsJSON struct {
	Facts map[string]any `json:"facts"`
	Items *[]struct {
		Fields map[string]any `json:"fields"`
	} `json:"items"`
}

// hookRow is one name's row of the hook record.
type hookRow struct {
	role, state, version string
}

// unreadable is the check's answer when an input it needs cannot be read: a
// fail, never a pass, since nothing below it can be judged.
func unreadable(evidence, fix string) doctor.Result {
	return doctor.Result{Status: doctor.Fail, Evidence: evidence, Fix: fix}
}

// checkFriendsHooked fails when the roster or the hook record cannot be read,
// when the roster is empty, when a rostered friend has no hook record, is not
// up, or was hooked by another build, when a rostered friend's supervised hook
// service is missing, not running or runs another build, or when the running
// heartbeat server passed another build (docs/SPEC-DOCTOR.md,
// "friends-hooked"). Every rostered friend is checked whatever role its record
// shows, and the role is named in the evidence.
func checkFriendsHooked(ctx context.Context, env doctor.Env) doctor.Result {
	roster, res, ok := readRoster(ctx, env)
	if !ok {
		return res
	}
	if len(roster) == 0 {
		return doctor.Result{Status: doctor.Fail, Evidence: "the nova-config friend roster is empty: no friend to check",
			Fix: "nova-config friend add <name>"}
	}
	rows, served, res, ok := readHookRecord(ctx, env)
	if !ok {
		return res
	}
	out, err := env.Exec(ctx, "nova-hook", "version")
	if err != nil {
		return unreadable("nova-hook version did not answer: "+err.Error(), "nova-update apply --file <manifest> nova-hook")
	}
	installed, ok := buildinfo.Parse(out)
	if !ok {
		return unreadable("nova-hook version printed no version line: "+strings.TrimSpace(out), "nova-update apply --file <manifest> nova-hook")
	}
	goos := doctorOS(env)
	var bad, who, good []string
	for _, name := range roster {
		row, hooked := rows[name]
		var faults []string
		switch {
		case !hooked:
			faults = append(faults, fmt.Sprintf("friend %s has no hook record", name))
		case row.state != "up":
			faults = append(faults, fmt.Sprintf("friend %s (%s) is %s", name, roleName(row.role), row.state))
		case row.version != installed.Version:
			faults = append(faults, fmt.Sprintf("friend %s (%s) is hooked by %s, installed nova-hook is %s", name, roleName(row.role), buildName(row.version), installed.Version))
		}
		faults = append(faults, serviceFaults(ctx, env, goos, name, installed.Version)...)
		if len(faults) > 0 {
			bad = append(bad, faults...)
			who = append(who, name)
			continue
		}
		good = append(good, fmt.Sprintf("%s (%s)", name, roleName(row.role)))
	}
	if served.alive && served.version != installed.Version {
		bad = append(bad, fmt.Sprintf("the running hook serve is %s, installed nova-hook is %s", buildName(served.version), installed.Version))
		who = append(who, "hook-serve")
	}
	if len(bad) > 0 {
		return doctor.Result{Status: doctor.Fail, Evidence: strings.Join(bad, "; "),
			Fix: strings.Join(who, ", ") + ": nova-update apply --file <manifest> nova-hook --version " + installed.Version + ", then nova-hook hook --as <name>"}
	}
	return doctor.Result{Status: doctor.OK, Evidence: fmt.Sprintf("every rostered friend is up on nova-hook %s with its hook service running it: %s",
		installed.Version, strings.Join(good, ", "))}
}

// readRoster is the friend names `nova-config friend list --json` carries,
// sorted; ok false with the failing result when it cannot be read.
func readRoster(ctx context.Context, env doctor.Env) ([]string, doctor.Result, bool) {
	const fix = "nova-config friend list --json"
	raw, err := env.Exec(ctx, "nova-config", "friend", "list", "--json")
	if err != nil {
		return nil, unreadable("nova-config friend list did not answer: "+err.Error(), fix), false
	}
	var got rosterJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &got); err != nil {
		return nil, unreadable("nova-config friend list printed no JSON: "+err.Error(), fix), false
	}
	if got.Items == nil {
		return nil, unreadable("nova-config friend list printed no items", fix), false
	}
	var names []string
	for _, it := range *got.Items {
		if it.Kind != "" && it.Kind != "friend" {
			continue
		}
		name, _ := it.Fields["name"].(string)
		if name == "" {
			return nil, unreadable("nova-config friend list printed a friend with no name", fix), false
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, doctor.Result{}, true
}

// serve is the heartbeat server's facts as the hook record shows them.
type serve struct {
	alive   bool
	version string
}

// readHookRecord is every name's row of `nova-hook friends --json` and the
// server's facts; ok false with the failing result when it cannot be read.
func readHookRecord(ctx context.Context, env doctor.Env) (map[string]hookRow, serve, doctor.Result, bool) {
	const fix = "nova-hook friends --json"
	raw, err := env.Exec(ctx, "nova-hook", "friends", "--json")
	if err != nil {
		return nil, serve{}, unreadable("nova-hook friends did not answer: "+err.Error(), fix), false
	}
	var got friendsJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &got); err != nil {
		return nil, serve{}, unreadable("nova-hook friends printed no JSON: "+err.Error(), fix), false
	}
	if got.Items == nil {
		return nil, serve{}, unreadable("nova-hook friends printed no items", fix), false
	}
	rows := map[string]hookRow{}
	for _, it := range *got.Items {
		name, _ := it.Fields["name"].(string)
		if name == "" {
			return nil, serve{}, unreadable("nova-hook friends printed a name row with no name", fix), false
		}
		role, _ := it.Fields["role"].(string)
		state, _ := it.Fields["state"].(string)
		build, _ := it.Fields["version"].(string)
		rows[name] = hookRow{role: role, state: state, version: build}
	}
	var sv serve
	sv.alive, _ = got.Facts["serve_alive"].(bool)
	sv.version, _ = got.Facts["serve_version"].(string)
	return rows, sv, doctor.Result{}, true
}

// doctorOS is the platform whose service manager the check asks: the real
// host, or the one a test injects through Env (an optional GOOS method), so
// both the launchd and the systemd paths run on either bench.
func doctorOS(env doctor.Env) string {
	if h, ok := env.(interface{ GOOS() string }); ok {
		if g := h.GOOS(); g != "" {
			return g
		}
	}
	return runtime.GOOS
}

// hookServiceName is the supervised nova-hook client service for a friend, as
// nova-hook installs it (cmd/nova-hook/service.go): a launchd label on macOS,
// a systemd --user unit elsewhere.
func hookServiceName(goos, name string) string {
	if goos == "darwin" {
		return "nova-hook.client." + name
	}
	return "nova-hook-" + name + ".service"
}

// serviceFaults is what is wrong with a friend's supervised hook service:
// none when it is loaded, running, and its binary's version line names the
// installed build. A service manager that cannot be read is a fault.
func serviceFaults(ctx context.Context, env doctor.Env, goos, name, installed string) []string {
	svc := hookServiceName(goos, name)
	var loaded, running bool
	var program string
	if goos == "darwin" {
		out, err := env.Exec(ctx, "launchctl", "list", svc)
		if err != nil {
			return []string{fmt.Sprintf("friend %s has no hook service %s loaded", name, svc)}
		}
		loaded, running, program = parseLaunchctlList(out)
	} else {
		out, err := env.Exec(ctx, "systemctl", "--user", "show", svc, "--property=LoadState,ActiveState,ExecStart")
		if err != nil {
			return []string{fmt.Sprintf("friend %s: systemctl could not read hook service %s: %s", name, svc, err.Error())}
		}
		loaded, running, program = parseSystemctlShow(out)
	}
	switch {
	case !loaded:
		return []string{fmt.Sprintf("friend %s has no hook service %s loaded", name, svc)}
	case !running:
		return []string{fmt.Sprintf("friend %s's hook service %s is not running", name, svc)}
	case program == "":
		return []string{fmt.Sprintf("friend %s's hook service %s names no program", name, svc)}
	}
	out, err := env.Exec(ctx, program, "version")
	if err != nil {
		return []string{fmt.Sprintf("friend %s's hook service binary %s did not answer version: %s", name, program, err.Error())}
	}
	got, ok := buildinfo.Parse(out)
	if !ok {
		return []string{fmt.Sprintf("friend %s's hook service binary %s printed no version line", name, program)}
	}
	if got.Version != installed {
		return []string{fmt.Sprintf("friend %s's hook service %s runs %s, installed nova-hook is %s", name, svc, got.Version, installed)}
	}
	return nil
}

// parseLaunchctlList reads `launchctl list <label>`: a job that printed is
// loaded, one with a "PID" is running, and the program is "Program" or the
// first of "ProgramArguments".
func parseLaunchctlList(out string) (loaded, running bool, program string) {
	loaded = strings.Contains(out, `"Label"`)
	inArgs := false
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, `"PID" = `):
			running = true
		case strings.HasPrefix(l, `"Program" = `):
			program = launchdString(strings.TrimPrefix(l, `"Program" = `))
		case strings.HasPrefix(l, `"ProgramArguments" = (`):
			inArgs = true
		case inArgs:
			if program == "" && l != ");" {
				program = launchdString(l)
			}
			inArgs = false
		}
	}
	return loaded, running, program
}

// launchdString is a quoted launchctl value without its quotes and the
// trailing semicolon.
func launchdString(v string) string {
	return strings.Trim(strings.TrimSuffix(strings.TrimSpace(v), ";"), `"`)
}

// parseSystemctlShow reads `systemctl --user show <unit>
// --property=LoadState,ActiveState,ExecStart`: loaded is LoadState=loaded,
// running is ActiveState=active, and the program is ExecStart's path=.
func parseSystemctlShow(out string) (loaded, running bool, program string) {
	for _, l := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
		switch k {
		case "LoadState":
			loaded = v == "loaded"
		case "ActiveState":
			running = v == "active"
		case "ExecStart":
			for _, f := range strings.Fields(v) {
				if p, ok := strings.CutPrefix(f, "path="); ok {
					program = p
					break
				}
			}
		}
	}
	return loaded, running, program
}

// roleName is a hook role as prose: "" is a record with no role.
func roleName(r string) string {
	if r == "" {
		return "no role"
	}
	return r
}

// buildName is a build identity as prose: "" is a build that recorded none.
func buildName(v string) string {
	if v == "" {
		return "an unrecorded build"
	}
	return v
}
