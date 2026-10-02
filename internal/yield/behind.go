package yield

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// Behind puts this process, and so everything it starts after, in the class that
// runs only on what CI leaves: past Nice, which ranks threads inside one CPU group
// and no further. On Linux it is a transient systemd scope with CPUWeight=idle in
// the slice that holds this process's unit: the CI runners' units are siblings
// there, and a weight ranks only siblings (docs/FLEET.md, "CI over cards"). On
// darwin it is the background state (PRIO_DARWIN_BG): background QoS, throttled
// disk I/O and background-class sockets, inherited by every descendant.
//
// It never fails its caller: "" when done, else why not (no user manager, the
// manager refused, an OS with neither), which the caller says once and then runs
// at Nice alone. name is the launch's own word, used in the scope's name.
//
// It is for a card's own tree (nova-swarm native under a member), never for the
// loop that launched it: a loop in the idle class misses its beats when CI fills
// the machine.
func Behind(name string) string { return behind(name) }

// BehindNote is why Behind would not work for a process started from this one,
// read without changing anything: "" where it would. A member says it once at
// its start.
func BehindNote() string { return behindNote() }

// scopeDeps is what the Linux scope step touches, so its every path is tested on
// any OS: this process's id, a file reader (/proc and /sys) and a bounded command
// runner (busctl, systemctl).
type scopeDeps struct {
	pid  int
	read func(path string) ([]byte, error)
	run  func(name string, args ...string) ([]byte, error)
	wait func(time.Duration)
}

// scopeMoveWait is how long the scope step waits for systemd to move this process
// once the scope's job is queued: the move is that job's first act.
const scopeMoveWait = 3 * time.Second

// scopeVerbBudget bounds one busctl or systemctl call to the user manager.
const scopeVerbBudget = 10 * time.Second

// unifiedPath is the cgroup v2 path in a /proc/<pid>/cgroup text ("0::<path>"),
// "" when there is none (cgroup v1 alone, or no cgroup file).
func unifiedPath(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "0::"); ok {
			return p
		}
	}
	return ""
}

// scopePlan is where a scope for this process goes: the slice that holds its unit,
// when that unit is under a systemd user manager (user@<uid>.service). why is the
// reason there is no such place; slice is then "".
func scopePlan(cgroup string) (path, slice, why string) {
	path = unifiedPath(cgroup)
	if path == "" {
		return "", "", "no cgroup v2 path in /proc/self/cgroup"
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	manager := -1
	for i, p := range parts {
		if strings.HasPrefix(p, "user@") && strings.HasSuffix(p, ".service") {
			manager = i
		}
	}
	if manager < 0 {
		return path, "", "not under a systemd user manager (cgroup " + path + ")"
	}
	if len(parts) < manager+3 || !strings.HasSuffix(parts[len(parts)-2], ".slice") {
		return path, "", "this process's unit is in no slice of the user manager (cgroup " + path + ")"
	}
	return path, parts[len(parts)-2], ""
}

// scopeName is the transient scope's unit name for one launch: name with every
// character a unit name does not take made '_', and this process's id, so two
// live launches never share one.
func scopeName(name string, pid int) string {
	clean := []byte(name)
	for i, c := range clean {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':') {
			clean[i] = '_'
		}
	}
	if len(clean) > 64 {
		clean = clean[:64]
	}
	return "nova-card-" + string(clean) + "-" + strconv.Itoa(pid) + ".scope"
}

// behindScope is the Linux step: make a transient scope in this process's slice
// holding this process (StartTransientUnit through busctl), wait for systemd to
// move it there, set CPUWeight=idle on it (systemctl set-property, which spells
// idle itself), and read cpu.idle back. Every failure is a reason, never an error.
func behindScope(name string, d scopeDeps) string {
	cg, err := d.read("/proc/self/cgroup")
	if err != nil {
		return "read /proc/self/cgroup: " + err.Error()
	}
	_, slice, why := scopePlan(string(cg))
	if why != "" {
		return why
	}
	unit := scopeName(name, d.pid)
	out, err := d.run("busctl", "--user", "call", "org.freedesktop.systemd1", "/org/freedesktop/systemd1",
		"org.freedesktop.systemd1.Manager", "StartTransientUnit", "ssa(sv)a(sa(sv))", unit, "fail",
		"3", "PIDs", "au", "1", strconv.Itoa(d.pid), "Slice", "s", slice, "CollectMode", "s", "inactive-or-failed", "0")
	if err != nil {
		return "the user manager refused the scope " + unit + ": " + firstLine(out, err)
	}
	moved := ""
	for waited := time.Duration(0); ; waited += 20 * time.Millisecond {
		if cg, err := d.read("/proc/self/cgroup"); err == nil {
			if p := unifiedPath(string(cg)); strings.HasSuffix(p, "/"+unit) {
				moved = p
				break
			}
		}
		if waited >= scopeMoveWait {
			return "systemd did not move this process into " + unit + " within " + scopeMoveWait.String()
		}
		d.wait(20 * time.Millisecond)
	}
	if out, err := d.run("systemctl", "--user", "set-property", "--runtime", unit, "CPUWeight=idle"); err != nil {
		return "the user manager refused CPUWeight=idle on " + unit + ": " + firstLine(out, err)
	}
	idle, err := d.read("/sys/fs/cgroup" + moved + "/cpu.idle")
	if err != nil {
		return "read " + unit + "'s cpu.idle: " + err.Error()
	}
	if v := strings.TrimSpace(string(idle)); v != "1" {
		return unit + "'s cpu.idle is " + v + ", not 1"
	}
	return ""
}

// behindScopeNote is scopePlan's answer for this process, plus the user bus a
// scope is asked of: "" where behindScope can be tried.
func behindScopeNote(d scopeDeps) string {
	cg, err := d.read("/proc/self/cgroup")
	if err != nil {
		return "read /proc/self/cgroup: " + err.Error()
	}
	if _, _, why := scopePlan(string(cg)); why != "" {
		return why
	}
	if _, err := os.Stat(userBus()); err != nil {
		return "no user bus at " + userBus()
	}
	return ""
}

// userBus is the user manager's bus socket a busctl --user reaches.
func userBus() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir + "/bus"
	}
	return "/run/user/" + strconv.Itoa(os.Getuid()) + "/bus"
}

// realScopeDeps are the scope step's real reads and calls, each call bounded and
// pointed at the user bus even when XDG_RUNTIME_DIR was not handed down.
func realScopeDeps() scopeDeps {
	return scopeDeps{
		pid:  os.Getpid(),
		read: os.ReadFile,
		run: func(name string, args ...string) ([]byte, error) {
			cmd, cancel := subproc.CommandFor(context.Background(), scopeVerbBudget, name, args...)
			defer cancel()
			cmd.Env = os.Environ()
			if os.Getenv("XDG_RUNTIME_DIR") == "" {
				cmd.Env = append(cmd.Env, "XDG_RUNTIME_DIR=/run/user/"+strconv.Itoa(os.Getuid()))
			}
			return cmd.CombinedOutput()
		},
		wait: time.Sleep,
	}
}

// firstLine is a failed call's first line of output, else its error.
func firstLine(out []byte, err error) string {
	if line, _, _ := bytes.Cut(bytes.TrimSpace(out), []byte("\n")); len(line) > 0 {
		return string(line)
	}
	return fmt.Sprint(err)
}
