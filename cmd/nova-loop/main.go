// nova-loop runs one loop's command as the only copy of that loop on the machine: it takes
// the loop's lock, <run-dir>/<name>.lock, holding its own pid, refuses a second copy at exit
// 3 while the holder lives, takes the lock of a holder that is dead, counts the start in a
// node_exporter textfile when --metrics names a directory, then execs the command, which
// keeps the pid the lock holds. It replaces the bash nova-loop wrapper one coordinator
// installed from its own tool repository (docs/COORDINATOR-TOOLS.md). The dispatch, the
// banner, the help, the version verb and the refusals are internal/tool's.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

func main() { os.Exit(loopTool(hostWorld()).Main()) }

// exitHeld is a start refused because a live copy of the loop holds its lock.
const exitHeld = 3

// defaultRunDir is where the locks live when --run-dir names none: the fleet's layout.
const defaultRunDir = "~/nova-bench/run"

// loopNameRe is a loop's name: what fleet/loops.yml names a loop record, and a file name.
var loopNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// world is what a run touches beyond its flags, the seams a test fakes: this process's
// pid, whether another pid lives, the clock, the home directory, the lock's guard, and
// the exec that replaces this process with the command.
type world struct {
	pid   int
	alive func(pid int) bool
	now   func() time.Time
	home  string
	guard func(path string) (release func(), err error)
	exec  func(argv []string) error
	// refuse is why this machine runs no loop, "" when it does.
	refuse string
}

func loopTool(w world) *tool.Tool {
	// the words after -- are the command; the flag set is the verb's, read in its check
	var argv []string
	return &tool.Tool{
		Name: "nova-loop",
		What: "runs one loop's command as the only copy of that loop on this machine",
		How: "run takes the lock <run-dir>/<name>.lock, writing its own pid in it: a live pid there\n" +
			"refuses the start at exit 3, a dead one's lock is taken. --metrics counts the start for\n" +
			"node_exporter's textfile collector. Then it execs the command, which keeps the pid the lock\n" +
			"holds, so the lock is the command's while it runs. A loop's unit runs it before the command.",
		Default: "run",
		Stamp:   version,
		ExitTable: "0 the command ran (its own exit is the loop's), 1 the command could not be started, 2 could not run: a missing " +
			"flag, a bad name, no command, 3 a live copy of the loop holds its lock",
		Verbs: []tool.Verb{{
			Name:    "run",
			Usage:   "run --name <loop> [--run-dir <dir>] [--metrics <dir>] -- <command> [args...]",
			Example: "", // it execs a loop's command: the example block has no line that only looks
			Effect:  tool.LocalWrite + "; then runs the command, whose own effect it has",
			Detail: "lines: RUN OK loop=<name> pid=<pid> lock=<file> [starts=<n>] before the exec; RUN HELD loop=<name> pid=<holder>\n" +
				"lock=<file> at exit 3; RUN FAILED loop=<name>: <why> at exit 1.\n",
			Flags: func(f *tool.Flags) {
				f.Prints()
				f.Required("name", "the loop's `name`, its loop record's name ([a-z0-9._-])")
				f.String("run-dir", defaultRunDir, "the `dir` the lock <name>.lock is kept in")
				f.String("metrics", "", "a node_exporter textfile `dir`: each start is counted in nova_loop_<name>.prom; none: no metrics")
				f.Check(func(c *tool.Call) {
					argv = f.Args()
					if n := c.Str("name"); n != "" && !loopNameRe.MatchString(n) {
						c.Problem(fmt.Sprintf("--name %q is not a loop's name: lower case letters, digits, dot, dash and underscore", n))
					}
				})
			},
			Run: func(c *tool.Call) *tool.Out { return w.run(c, argv) },
		}},
	}
}

// run is one start of the loop.
func (w world) run(c *tool.Call, argv []string) *tool.Out {
	switch {
	case w.refuse != "":
		return refused(c, w.refuse)
	case len(argv) == 0:
		return refused(c, "no command: name it after --, as nova-loop run --name <loop> -- <command> [args...]")
	}
	name := c.Str("name")
	dir := w.tilde(c.Str("run-dir"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return failed(c, name, "the run directory could not be made: "+oneline.Err(err))
	}
	lock := filepath.Join(dir, name+".lock")
	holder, err := w.take(lock)
	if err != nil {
		return failed(c, name, "the lock could not be taken: "+oneline.Err(err))
	}
	if holder != 0 {
		fmt.Fprintf(c.Stdout, "RUN HELD loop=%s pid=%d lock=%s\n", oneline.Field(name), holder, oneline.Field(lock))
		return tool.Exit(exitHeld)
	}
	line := fmt.Sprintf("RUN OK loop=%s pid=%d lock=%s", oneline.Field(name), w.pid, oneline.Field(lock))
	if m := c.Str("metrics"); m != "" {
		starts, err := w.count(w.tilde(m), name)
		if err != nil {
			// the start goes ahead: a metric is a report, never a gate on the loop
			fmt.Fprintf(c.Stdout, "RUN NOTE loop=%s: the start was not counted: %s\n", oneline.Field(name), oneline.Err(err))
		} else {
			line += " starts=" + strconv.Itoa(starts)
		}
	}
	fmt.Fprintln(c.Stdout, line)
	if err := w.exec(argv); err != nil {
		return failed(c, name, "the command could not be started: "+oneline.Err(err))
	}
	return tool.Exit(0) // a fake exec returns; the real one does not
}

// tilde is p with a leading ~/ read as the home directory.
func (w world) tilde(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(w.home, rest)
	}
	return filepath.Clean(p)
}

func refused(c *tool.Call, why string) *tool.Out {
	fmt.Fprintf(c.Stderr, "RUN REFUSED: %s; run: nova-loop run -h\n", oneline.Escape(why))
	return tool.Exit(2)
}

func failed(c *tool.Call, name, why string) *tool.Out {
	fmt.Fprintf(c.Stderr, "RUN FAILED loop=%s: %s\n", oneline.Field(name), oneline.Escape(why))
	return tool.Exit(1)
}

// take takes the lock at path for this process: 0 when it is ours, the holder's pid when
// a live process holds it. The check and the write happen under the lock's guard, so two
// starts that both find a dead holder cannot both take its lock.
func (w world) take(path string) (int, error) {
	release, err := w.guard(path + ".guard")
	if err != nil {
		return 0, err
	}
	defer release()
	if b, err := os.ReadFile(path); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 && pid != w.pid && w.alive(pid) {
			return pid, nil
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	// no lock, or a dead holder's, or an unreadable one: this start's
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(w.pid)+"\n"), 0o644); err != nil {
		return 0, err
	}
	return 0, os.Rename(tmp, path)
}

// count adds one start to the loop's textfile in dir and returns the count.
func (w world) count(dir, name string) (int, error) {
	file := filepath.Join(dir, "nova_loop_"+strings.NewReplacer("-", "_", ".", "_").Replace(name)+".prom")
	label := `{loop="` + name + `"}`
	starts := 0
	if b, err := os.ReadFile(file); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if rest, ok := strings.CutPrefix(l, "nova_loop_starts_total"+label+" "); ok {
				starts, _ = strconv.Atoi(strings.TrimSpace(rest)) // an unreadable count starts again at 0
			}
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	starts++
	text := "# HELP nova_loop_starts_total Times nova-loop started the loop's command.\n" +
		"# TYPE nova_loop_starts_total counter\n" +
		"nova_loop_starts_total" + label + " " + strconv.Itoa(starts) + "\n" +
		"# HELP nova_loop_last_start_seconds When nova-loop last started the loop's command, in Unix seconds.\n" +
		"# TYPE nova_loop_last_start_seconds gauge\n" +
		"nova_loop_last_start_seconds" + label + " " + strconv.FormatInt(w.now().Unix(), 10) + "\n"
	// the collector reads *.prom: the copy is written under another name and renamed in
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return 0, err
	}
	return starts, os.Rename(tmp, file)
}
