package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
)

// A friend daemon is the thing this machine loads and runs; the checks below
// enumerate them from the agents this machine loads, never from ~/.nova/friends
// rows (docs/SPEC-DOCTOR.md, "the friend checks").
type friendDaemon struct {
	Name  string // the friend's name
	Label string // the launchd label or systemd unit name
	Path  string // the plist or unit file
	Argv  []string
}

// hostOS is the operating system whose agents the checks enumerate: the real
// host, or the platform a test injects through Env (an optional GOOS method), so
// both the launchd and the systemd paths run on either bench.
func hostOS(env Env) string {
	if h, ok := env.(interface{ GOOS() string }); ok {
		if g := h.GOOS(); g != "" {
			return g
		}
	}
	return runtime.GOOS
}

// friendDaemonDir is where the friend agents live: ~/Library/LaunchAgents on
// darwin, ~/.config/systemd/user on linux.
func friendDaemonDir(env Env) (dir, prefix, suffix string) {
	home := env.Getenv("HOME")
	if hostOS(env) == "linux" {
		return filepath.Join(home, ".config", "systemd", "user"), "nova-friend-", ".service"
	}
	return filepath.Join(home, "Library", "LaunchAgents"), "com.nova.friend-", ".plist"
}

// friendDaemons enumerates the friend daemons this machine loads, reading each
// daemon's argv from its launchd plist (friend.PlistArgs) or systemd unit.
func friendDaemons(env Env) []friendDaemon {
	dir, prefix, suffix := friendDaemonDir(env)
	entries, err := env.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []friendDaemon
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		label := strings.TrimSuffix(name, suffix)
		b, err := env.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var argv []string
		if hostOS(env) == "linux" {
			argv = unitExecStart(string(b))
		} else {
			argv = friend.PlistArgs(string(b))
		}
		out = append(out, friendDaemon{
			Name:  strings.TrimPrefix(label, prefix),
			Label: label,
			Path:  filepath.Join(dir, name),
			Argv:  argv,
		})
	}
	return out
}

// unitExecStart is the arguments of the first ExecStart line of a systemd unit.
func unitExecStart(unit string) []string {
	for _, line := range strings.Split(unit, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			return strings.Fields(rest)
		}
	}
	return nil
}

// argValue is the value of a --flag in argv, as `--flag v` or `--flag=v`.
func argValue(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
	}
	return ""
}

// jsonValue decodes s and finds key anywhere in it (server objects nest).
func jsonValue(s, key string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return nil
	}
	return jsonFind(v, key)
}

func jsonFind(v any, key string) any {
	switch t := v.(type) {
	case map[string]any:
		if x, ok := t[key]; ok {
			return x
		}
		for _, x := range t {
			if y := jsonFind(x, key); y != nil {
				return y
			}
		}
	case []any:
		for _, x := range t {
			if y := jsonFind(x, key); y != nil {
				return y
			}
		}
	}
	return nil
}

func jsonString(s, key string) string {
	v, _ := jsonValue(s, key).(string)
	return v
}

func jsonInt(s, key string) int {
	switch v := jsonValue(s, key).(type) {
	case float64:
		return int(v)
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
			return 0
		}
		return n
	}
	return 0
}

func jsonStrings(s, key string) []string {
	switch v := jsonValue(s, key).(type) {
	case []any:
		var out []string
		for _, x := range v {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
		return out
	case string:
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

// firstLine is s's first non-empty line, one-lined, for evidence.
func firstLine(s string) string { return oneLine(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]) }

func friendDaemonsOrOK(env Env) ([]friendDaemon, *Result) {
	daemons := friendDaemons(env)
	if len(daemons) == 0 {
		return nil, &Result{Status: OK, Evidence: "no friend daemons here"}
	}
	return daemons, nil
}

func init() {
	Default.Register(Check{Name: "friend-seat", Dependency: "each friend daemon's --coordinator against the sprint seat", Run: checkFriendSeat})
}

// checkFriendSeat holds each friend daemon's --coordinator to the seat holder
// the sprint server reports: a daemon pinned to a previous seat can never be
// handed work (docs/SPEC-DOCTOR.md, "friend-seat").
func checkFriendSeat(ctx context.Context, env Env) Result {
	daemons, ok := friendDaemonsOrOK(env)
	if ok != nil {
		return *ok
	}
	out, err := env.Exec(ctx, "nova-sprint", "where", "--json")
	if err != nil {
		return Result{Status: Warn, Evidence: "nova-sprint where did not answer: " + firstLine(err.Error()),
			Fix: "nova-sprint where --json (the daemon reads the seat from the server)"}
	}
	seat := jsonString(out, "coordinator")
	var bad []string
	for _, d := range daemons {
		c := argValue(d.Argv, "--coordinator")
		if c != "" && seat != "" && c != seat {
			bad = append(bad, fmt.Sprintf("daemon %s: plist says %s, seat is %s", d.Name, c, seat))
		}
	}
	if len(bad) > 0 {
		return Result{Status: Fail, Evidence: strings.Join(bad, "; "),
			Fix: "nova-friend install --as <friend> --coordinator " + seat + " (or drop --coordinator so the daemon reads the seat from the server)"}
	}
	return Result{Status: OK, Evidence: fmt.Sprintf("%d friend daemons on seat %s", len(daemons), seat)}
}
