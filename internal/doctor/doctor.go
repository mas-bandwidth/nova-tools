// Package doctor is the frame of nova-doctor: a registry of checks, an Env
// the checks reach the machine through, and the run that prints one DOCTOR line
// per check and exits by the worst result (docs/SPEC-DOCTOR.md).
//
// A check lives in its own file, check_<dependency>.go, and registers itself
// from an init, so a new dependency adds a file and edits nothing here.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// Status is how one check ended.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Result is what a check found: its status, the evidence that supports it, and,
// when the status is not ok, the one line that fixes it (a nova verb when one
// exists, never a hand script).
type Result struct {
	Status   Status `json:"status"`
	Evidence string `json:"evidence"`
	Fix      string `json:"fix,omitempty"`
}

// Env is everything a check may touch outside itself: processes, files, the
// network and the clock. A test fakes all four.
type Env interface {
	// LookPath is the path of a program on PATH.
	LookPath(name string) (string, error)
	// Exec runs a program to its end and returns its standard output.
	Exec(ctx context.Context, name string, args ...string) (string, error)
	// ReadFile reads one file.
	ReadFile(path string) ([]byte, error)
	// Dial opens and closes one connection.
	Dial(ctx context.Context, network, addr string) error
	// Now is the clock.
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
type Registry struct{ checks map[string]Check }

// Default is the registry the check files register into.
var Default = &Registry{}

// Register adds a check to the Default registry; a check file calls it from init.
func Register(c Check) { Default.Register(c) }

// Register adds a check. A nameless check, one without a Run, and a second check
// of the same name are programming errors and panic at registration.
func (r *Registry) Register(c Check) {
	if c.Name == "" || c.Covers == "" || c.Run == nil {
		panic("doctor: a check needs a name, the dependency it covers and a Run")
	}
	if r.checks == nil {
		r.checks = map[string]Check{}
	}
	if _, dup := r.checks[c.Name]; dup {
		panic("doctor: check " + c.Name + " is registered twice")
	}
	r.checks[c.Name] = c
}

// Checks is every registered check, by name.
func (r *Registry) Checks() []Check {
	out := make([]Check, 0, len(r.checks))
	for _, c := range r.checks {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names is the registered names, sorted.
func (r *Registry) Names() []string {
	var n []string
	for _, c := range r.Checks() {
		n = append(n, c.Name)
	}
	return n
}

// Options are the run's flags.
type Options struct {
	Only   []string // --check: just these, by name; empty runs every check
	Strict bool     // --strict: a warn exits 1
	Local  bool     // --local: skip the checks only a fleet needs, saying so
}

// Line is one check's row of the report.
type Line struct {
	Check   string `json:"check"`
	Covers  string `json:"covers"`
	Skipped bool   `json:"skipped,omitempty"`
	Result
}

// Report is the whole run.
type Report struct {
	Lines  []Line `json:"checks"`
	Exit   int    `json:"exit"`
	Strict bool   `json:"strict"`
}

// Run runs the selected checks in name order and folds their results into the
// exit code (SPEC-DOCTOR: 0, 1 on a warn only with --strict, 2 on a fail). An
// unknown --check name is an error naming every check there is.
func (r *Registry) Run(ctx context.Context, env Env, o Options) (Report, error) {
	rep := Report{Strict: o.Strict}
	have := r.Names()
	var unknown []string
	for _, n := range o.Only {
		if !slices.Contains(have, n) {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		return rep, fmt.Errorf("no check named %s; the checks are %s", strings.Join(unknown, ", "), strings.Join(have, ", "))
	}
	for _, c := range r.Checks() {
		if len(o.Only) > 0 && !slices.Contains(o.Only, c.Name) {
			continue
		}
		l := Line{Check: c.Name, Covers: c.Covers}
		switch {
		case c.Fleet && o.Local:
			l.Skipped = true
			l.Status, l.Evidence = OK, "skipped: only a fleet needs this check and --local was given"
		default:
			l.Result = c.Run(ctx, env)
			if l.Status != OK && l.Status != Warn {
				l.Status = Fail
			}
		}
		if !l.Skipped && l.Status != OK && l.Fix == "" {
			l.Fix = "run nova-doctor --check " + c.Name + " --json for the full evidence"
		}
		rep.Lines = append(rep.Lines, l)
		switch {
		case l.Skipped || l.Status == OK:
		case l.Status == Fail:
			rep.Exit = 2
		case l.Status == Warn && o.Strict && rep.Exit == 0:
			rep.Exit = 1
		}
	}
	return rep, nil
}

// Text writes one line per check: DOCTOR <check> ok|warn|fail <evidence> [fix: <line>].
// A check --local skipped prints as DOCTOR <check> skip <why>.
func (rep Report) Text(w io.Writer) {
	for _, l := range rep.Lines {
		word := string(l.Status)
		if l.Skipped {
			word = "skip"
		}
		line := fmt.Sprintf("DOCTOR %s %s %s", l.Check, word, oneLine(l.Evidence))
		if l.Fix != "" {
			line += " fix: " + oneLine(l.Fix)
		}
		fmt.Fprintln(w, line)
	}
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Tool is nova-doctor on internal/tool: one verb, run, which is the default so
// `nova-doctor --local` is the whole command line.
func Tool(stamp string, env Env, reg *Registry) *tool.Tool {
	return &tool.Tool{
		Name:    "nova-doctor",
		What:    "says what is missing from this machine's nova setup and the one line that fixes each",
		Stamp:   stamp,
		Default: "run",
		How: `runs every registered check, one per dependency, and prints one line each:
DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]
the exit is the worst result: 0, 1 on a warn only with --strict, 2 on a fail.
--local skips the checks only a fleet needs and prints a skip line for each.`,
		ExitTable: "0 every check ok (or warn, without --strict), 1 a warn with --strict, 2 a check failed or the command line was refused",
		Verbs: []tool.Verb{{
			Name:    "run",
			Usage:   "run [--check <name>]... [--strict] [--local] [--json]",
			Example: "--local",
			Effect:  tool.Inspection,
			Flags: func(f *tool.Flags) {
				f.Prints()
				f.Var(new(names), "check", "run only this check; repeatable; without it, every check runs")
				f.Bool("strict", false, "exit 1 when a check warns")
				f.Bool("local", false, "skip the checks only a fleet needs, saying so")
				f.Bool("json", false, "print the report as JSON")
			},
			Run: func(c *tool.Call) *tool.Out {
				rep, err := reg.Run(c.Ctx, env, Options{Only: []string(*c.Get("check").(*names)), Strict: c.Bool("strict"), Local: c.Bool("local")})
				if err != nil {
					return tool.Refuse(err.Error())
				}
				if c.Bool("json") {
					if err := writeJSON(c.Stdout, rep); err != nil {
						return tool.Fail("could not write the report: " + err.Error())
					}
				} else {
					rep.Text(c.Stdout)
				}
				return tool.Exit(rep.Exit)
			},
		}},
	}
}

// OSEnv is the Env of the machine the process runs on.
type OSEnv struct{}

func (OSEnv) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (OSEnv) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (OSEnv) Now() time.Time                       { return time.Now() }
func (OSEnv) Exec(ctx context.Context, name string, args ...string) (string, error) {
	cmd, cancel := subproc.CommandFor(ctx, 15*time.Second, name, args...)
	defer cancel()
	out, err := cmd.Output()
	return string(out), err
}
func (OSEnv) Dial(ctx context.Context, network, addr string) error {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
	if err == nil {
		conn.Close()
	}
	return err
}

// names is a repeatable string flag (--check a --check b).
type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Get() any           { return n }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

func writeJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}
