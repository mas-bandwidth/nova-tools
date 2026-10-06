// Package doctor is the frame of nova-doctor: a registry of checks, an Env the
// checks reach the machine through, and the run that prints one DOCTOR line per
// check and exits by the worst result (docs/SPEC-DOCTOR.md).
//
// A check lives in its own file, check_<dependency>.go, and registers itself
// from an init, so a new dependency adds a file and edits nothing here.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// Status is how one check ended.
type Status string

// The three ends of a check, in order of weight.
const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Result is what a check found: its status, the evidence behind it and, when
// the status is not ok, the one line that fixes it (a nova verb when one
// exists, never a hand script).
type Result struct {
	Status   Status `json:"status"`
	Evidence string `json:"evidence"`
	Fix      string `json:"fix,omitempty"`
}

// Env is everything a check may touch outside itself: processes, files, the
// network and the clock. A test fakes all of them.
type Env interface {
	Getenv(key string) string
	// ReadDir names the executable regular files of one directory.
	ReadDir(path string) ([]string, error)
	// Exec runs a program to its end and returns its standard output.
	Exec(ctx context.Context, name string, args ...string) (string, error)
	ReadFile(path string) ([]byte, error)
	// Dial opens and closes one connection.
	Dial(ctx context.Context, network, addr string) error
	Now() time.Time
}

// Check is one dependency's test: its name, the dependency it covers and a Run
// over Env. Fleet marks a check only a fleet needs, which --local skips.
type Check struct {
	Name   string
	Covers string
	Fleet  bool
	Run    func(ctx context.Context, env Env) Result
}

// Registry is the set of checks one run draws from.
type Registry struct{ checks []Check }

// Default is the registry the check files register into.
var Default = &Registry{}

// Register adds a check to the Default registry; a check file calls it from init.
func Register(c Check) { Default.Register(c) }

// Register adds a check. A check without a name, a dependency or a Run, and a
// second check of one name, are programming errors and panic at registration.
func (r *Registry) Register(c Check) {
	if c.Name == "" || c.Covers == "" || c.Run == nil {
		panic(fmt.Sprintf("doctor: check %q needs a name, the dependency it covers and a Run", c.Name))
	}
	if _, dup := r.Lookup(c.Name); dup {
		panic("doctor: check " + c.Name + " is registered twice")
	}
	r.checks = append(r.checks, c)
	slices.SortFunc(r.checks, func(a, b Check) int { return strings.Compare(a.Name, b.Name) })
}

// Lookup is the check of that name.
func (r *Registry) Lookup(name string) (Check, bool) {
	for _, c := range r.checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// Names are the registered check names, sorted.
func (r *Registry) Names() []string {
	names := make([]string, len(r.checks))
	for i, c := range r.checks {
		names[i] = c.Name
	}
	return names
}

// checkTimeout bounds one check; a check that outlasts it fails with that said.
const checkTimeout = 30 * time.Second

// Finding is one check's result with its identity, as a run reports it.
type Finding struct {
	Name     string `json:"name"`
	Covers   string `json:"covers"`
	Status   Status `json:"status"`
	Evidence string `json:"evidence"`
	Fix      string `json:"fix,omitempty"`
}

// Report is one run: the findings, the fleet checks --local skipped, and the exit.
type Report struct {
	Checks  []Finding `json:"checks"`
	Skipped []string  `json:"skipped,omitempty"`
	Exit    int       `json:"exit"`
}

// Run runs the named checks (all when names is empty), skipping the fleet
// checks when local is set. A warn is exit 1 only under strict; a fail is 2.
// docs/SPEC-DOCTOR.md, "Exit".
func (r *Registry) Run(ctx context.Context, env Env, names []string, local, strict bool) Report {
	var rep Report
	for _, c := range r.checks {
		if len(names) > 0 && !slices.Contains(names, c.Name) {
			continue
		}
		if local && c.Fleet {
			rep.Skipped = append(rep.Skipped, c.Name)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		res := c.Run(cctx, env)
		if cctx.Err() != nil {
			res = Result{Status: Fail, Evidence: "the check did not finish: " + cctx.Err().Error(), Fix: "nova-doctor --check " + c.Name}
		}
		cancel()
		if res.Status == OK {
			res.Fix = ""
		}
		rep.Checks = append(rep.Checks, Finding{Name: c.Name, Covers: c.Covers, Status: res.Status,
			Evidence: strings.Join(strings.Fields(res.Evidence), " "), Fix: strings.Join(strings.Fields(res.Fix), " ")})
		switch {
		case res.Status == Fail:
			rep.Exit = 2
		case res.Status == Warn && strict && rep.Exit < 1:
			rep.Exit = 1
		}
	}
	return rep
}

func (rep Report) lines() string {
	var b strings.Builder
	for _, f := range rep.Checks {
		fmt.Fprintf(&b, "DOCTOR %s %s %s", f.Name, f.Status, f.Evidence)
		if f.Fix != "" {
			b.WriteString(" fix: " + f.Fix)
		}
		b.WriteByte('\n')
	}
	if n := len(rep.Skipped); n > 0 {
		noun := "checks"
		if n == 1 {
			noun = "check"
		}
		fmt.Fprintf(&b, "NOTE --local skipped %d fleet %s: %s\n", n, noun, strings.Join(rep.Skipped, ", "))
	}
	return b.String()
}

// names is the repeatable --check flag.
type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Set(s string) error { *n = append(*n, s); return nil }
func (n *names) Get() any           { return []string(*n) }

// Tool is nova-doctor over an Env and a registry.
func Tool(stamp string, env Env, reg *Registry) *tool.Tool {
	return &tool.Tool{
		Name: "nova-doctor",
		What: "says what is missing from this machine's nova setup, and the one line that fixes each",
		How: "every registered check runs against this machine (processes, files, network, clock)\n" +
			"and prints one DOCTOR line: its name, ok, warn or fail, the evidence, and for a\n" +
			"warn or fail the fix: a nova verb when one exists. It changes nothing.\n" +
			"first run: nova-doctor run --local checks everything a single machine needs.",
		ExitTable: "0 every check ok (or only warns), 1 a warn under --strict, 2 a check failed or the invocation was refused",
		Stamp:     stamp,
		Default:   "run",
		Verbs: []tool.Verb{{
			Name:    "run",
			Usage:   "[run] [--check <name>]... [--json] [--strict] [--local]",
			Example: "run --local",
			Effect:  tool.Inspection,
			Detail: "Each check line is: DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]\n" +
				"--check names a check to run (repeatable; none runs every one)\n" +
				"--strict makes a warn exit 1; --local skips the checks only a fleet needs, and says which\n" +
				"--json prints one object: checks (name, covers, status, evidence, fix), skipped, exit",
			Flags: func(f *tool.Flags) {
				f.Prints()
				var chosen names
				f.Var(&chosen, "check", "run only this check (repeatable); the names are: "+strings.Join(reg.Names(), ", "))
				f.Bool("json", false, "print the report as one JSON object instead of lines")
				f.Bool("strict", false, "exit 1 on a warn when nothing failed")
				f.Bool("local", false, "skip the checks only a fleet needs, and name them")
				f.Check(func(c *tool.Call) {
					for _, n := range c.Get("check").([]string) {
						if _, ok := reg.Lookup(n); !ok {
							c.Problem(fmt.Sprintf("--check %q names no check; the checks are: %s", n, strings.Join(reg.Names(), ", ")))
						}
					}
				})
			},
			Run: func(c *tool.Call) *tool.Out {
				rep := reg.Run(c.Ctx, env, c.Get("check").([]string), c.Bool("local"), c.Bool("strict"))
				if c.Bool("json") {
					b, _ := json.Marshal(rep)
					fmt.Fprintln(c.Stdout, string(b))
				} else {
					fmt.Fprint(c.Stdout, rep.lines())
				}
				return tool.Exit(rep.Exit)
			},
		}},
	}
}

var _ flag.Getter = (*names)(nil)

// OSEnv is the real machine.
type OSEnv struct{}

// Getenv reads the process environment.
func (OSEnv) Getenv(key string) string { return os.Getenv(key) }

// ReadDir names the executable regular files of a directory.
func (OSEnv) ReadDir(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && info.Mode()&fs.ModePerm&0o111 != 0 {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// Exec runs a program and returns its standard output.
func (OSEnv) Exec(ctx context.Context, name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := subproc.Context(ctx, name, args...)
	cmd.Stdout = &out
	err := cmd.Run()
	return out.String(), err
}

// ReadFile reads one file.
func (OSEnv) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// Dial opens and closes one connection.
func (OSEnv) Dial(ctx context.Context, network, addr string) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// Now is the wall clock.
func (OSEnv) Now() time.Time { return time.Now() }
