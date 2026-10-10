// Package doctor is nova-doctor's frame: a registry of checks, each one a name, the
// dependency it covers, a Run over an Env, and a result of ok, warn or fail with
// evidence and, when not ok, the one fix line. The contract is docs/SPEC-DOCTOR.md.
//
// A check lives in its own file, check_<dependency>.go, and registers itself from an
// init, so a new dependency adds one file and edits nothing here. Every outside fact a
// check reads (a process, a file, a network address, the clock, the environment) comes
// through Env, so a test fakes all of them and no test starts a service or opens a
// socket.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// Status is one check's verdict.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Result is what one check found. Fix is required when Status is not OK: a nova verb
// when one exists, never a hand script.
type Result struct {
	Check      string `json:"check"`
	Dependency string `json:"dependency"`
	Status     Status `json:"status"`
	Evidence   string `json:"evidence"`
	Fix        string `json:"fix,omitempty"`
}

// Check is one dependency's check. Fleet marks a check only a fleet needs: --local
// skips it and says so.
type Check struct {
	Name       string
	Dependency string
	Fleet      bool
	Run        func(ctx context.Context, env Env) Result
}

// Env is everything a check may reach outside itself. Paths are the real ones in the
// real Env and a tree under t.TempDir() in a fake.
type Env interface {
	Getenv(key string) string
	Now() time.Time
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]fs.DirEntry, error)
	Exec(ctx context.Context, name string, args ...string) (string, error)
	Dial(ctx context.Context, network, addr string) error
}

// Registry is the set of checks. Default is the one the checks register into.
type Registry struct {
	mu     sync.Mutex
	checks map[string]Check
}

// Default is the registry the check_<dependency>.go files register into from an init.
var Default = NewRegistry()

// NewRegistry is an empty registry.
func NewRegistry() *Registry { return &Registry{checks: map[string]Check{}} }

// Register adds a check; an unnamed check, one with no Run, or a name taken is a
// programming error and panics at init, never at a stranger's run.
func (r *Registry) Register(c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.Name == "" || c.Dependency == "" || c.Run == nil {
		panic("doctor: a check needs a Name, a Dependency and a Run")
	}
	if _, dup := r.checks[c.Name]; dup {
		panic("doctor: check " + c.Name + " is registered twice")
	}
	r.checks[c.Name] = c
}

// Names are the registered check names, sorted.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.checks))
	for n := range r.checks {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Options select which checks run.
type Options struct {
	Only  []string // check names; empty is every check
	Local bool     // skip the checks only a fleet needs
}

// Run runs the selected checks in name order and returns their results and the names
// --local skipped. A name that is no check is an error naming the ones there are.
func (r *Registry) Run(ctx context.Context, env Env, o Options) (results []Result, skipped []string, err error) {
	names := r.Names()
	for _, n := range o.Only {
		if !slices.Contains(names, n) {
			return nil, nil, fmt.Errorf("no check named %q; the checks are %s", n, strings.Join(names, ", "))
		}
	}
	for _, n := range names {
		if len(o.Only) > 0 && !slices.Contains(o.Only, n) {
			continue
		}
		c := r.checks[n]
		if o.Local && c.Fleet {
			skipped = append(skipped, n)
			continue
		}
		results = append(results, runOne(ctx, env, c))
	}
	return results, skipped, nil
}

// runOne runs one check and holds its result to the grammar: the check's name and
// dependency are the registry's, a status outside ok, warn and fail is a fail, a
// result that is not ok and has no fix line is a fail saying so, and a panic is a
// fail, never a crash that hides the other checks.
func runOne(ctx context.Context, env Env, c Check) (res Result) {
	defer func() {
		if p := recover(); p != nil {
			res = Result{Status: Fail, Evidence: fmt.Sprintf("the check panicked: %v", p),
				Fix: "report this to the nova-tools maintainers with the output of `nova-doctor run --check " + c.Name + " --json`"}
		}
		res.Check, res.Dependency = c.Name, c.Dependency
		res.Evidence = oneLine(res.Evidence)
		res.Fix = oneLine(res.Fix)
		switch {
		case res.Status != OK && res.Status != Warn && res.Status != Fail:
			res.Evidence = fmt.Sprintf("the check answered status %q: %s", res.Status, res.Evidence)
			res.Status = Fail
		}
		if res.Status != OK && res.Fix == "" {
			res.Evidence += " (the check gave no fix line)"
			res.Status = Fail
			res.Fix = "report this to the nova-tools maintainers: check " + c.Name + " gave no fix line"
		}
	}()
	return c.Run(ctx, env)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// ExitCode is the exit by the worst result: 2 for a fail, 1 for a warn under strict,
// else 0 (docs/SPEC-DOCTOR.md, "The exit").
func ExitCode(results []Result, strict bool) int {
	code := 0
	for _, r := range results {
		switch {
		case r.Status == Fail:
			return 2
		case r.Status == Warn && strict:
			code = 1
		}
	}
	return code
}

// Line is one result as printed: `DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]`.
func (r Result) Line() string {
	l := fmt.Sprintf("DOCTOR %s %s %s", r.Check, r.Status, r.Evidence)
	if r.Fix != "" {
		l += " fix: " + r.Fix
	}
	return l
}

// report is the --json value: the same results as the lines, and the exit.
type report struct {
	Exit    int      `json:"exit"`
	Results []Result `json:"results"`
	Skipped []string `json:"skipped,omitempty"`
}

// list is a repeatable string flag.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }
func (l *list) Get() any           { return []string(*l) }

// Tool is nova-doctor on the nova tool skeleton: one verb, run, which is also the
// default, so `nova-doctor --local` is a run. A bare `nova-doctor` is no run: it is
// refused naming the verbs and the door, as every tool's bare command is
// (docs/ONBOARDING.md point 1).
func Tool(reg *Registry, env Env, stamp string) *tool.Tool {
	return &tool.Tool{
		Name:    "nova-doctor",
		What:    "says what is missing for the nova tools to work, and the one line that fixes each",
		Stamp:   stamp,
		Default: "run",
		How: `each check covers one dependency: ok, warn or fail, with evidence and a fix line.
Every check runs; a fail does not stop the others. --local skips the checks only a fleet
needs and says which. Exit 0 is all ok (or warn), 1 a warn under --strict, 2 a fail.
first run: nova-doctor run; nothing is changed, no fix is run for you.`,
		NoJSON:    "run prints one `DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]` line per check; with --json it prints the same results as one object",
		ExitTable: "0 every check ok (a warn too, unless --strict), 1 a warn under --strict, 2 a fail, or usage",
		Verbs: []tool.Verb{{
			Name:    "run",
			Usage:   "[run] [--check <name>]... [--local] [--strict] [--json]",
			Example: "run --check harness\nrun --local\nrun --local --json",
			Effect:  tool.Inspection,
			Detail:  "checks: " + strings.Join(reg.Names(), ", ") + "\nexample: nova-doctor run --local",
			Flags: func(f *tool.Flags) {
				f.Prints()
				f.Var(new(list), "check", "run only this check (repeatable); the names are in `help run`")
				f.Bool("local", false, "skip the checks only a fleet needs, and say which")
				f.Bool("strict", false, "exit 1 on a warn")
				f.Bool("json", false, "print the results as one JSON object instead of lines")
				f.Check(func(c *tool.Call) {
					for _, n := range c.Get("check").([]string) {
						if !slices.Contains(reg.Names(), n) {
							c.Problem(fmt.Sprintf("no check named %q; the checks are %s", n, strings.Join(reg.Names(), ", ")))
						}
					}
				})
			},
			Run: func(c *tool.Call) *tool.Out {
				results, skipped, err := reg.Run(c.Ctx, env, Options{Only: c.Get("check").([]string), Local: c.Bool("local")})
				if err != nil {
					return tool.Refuse(err.Error())
				}
				code := ExitCode(results, c.Bool("strict"))
				if c.Bool("json") {
					b, _ := json.Marshal(report{Exit: code, Results: results, Skipped: skipped})
					fmt.Fprintf(c.Stdout, "%s\n", b)
					return tool.Exit(code)
				}
				for _, r := range results {
					fmt.Fprintln(c.Stdout, r.Line())
				}
				if len(skipped) > 0 {
					fmt.Fprintf(c.Stdout, "DOCTOR local skipped=%s (checks only a fleet needs; run without --local to include them)\n", strings.Join(skipped, ","))
				}
				return tool.Exit(code)
			},
		}},
	}
}

// Main runs nova-doctor over args. No arguments is refused at exit 2 with the
// verbs and `run: nova-doctor help` (pkg/tool's Run), never a run: a
// bare command is no invocation (docs/ONBOARDING.md point 1).
func Main(reg *Registry, env Env, stamp string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return Tool(reg, env, stamp).Run(args, stdin, stdout, stderr)
}

// OSEnv is the real Env: the process's environment, files, clock, children and network.
type OSEnv struct{}

func (OSEnv) Getenv(k string) string                  { return os.Getenv(k) }
func (OSEnv) Now() time.Time                          { return time.Now() }
func (OSEnv) ReadFile(p string) ([]byte, error)       { return os.ReadFile(p) }
func (OSEnv) ReadDir(p string) ([]fs.DirEntry, error) { return os.ReadDir(p) }
func (OSEnv) Exec(ctx context.Context, name string, args ...string) (string, error) {
	out, err := subproc.Context(ctx, name, args...).Output()
	return string(out), err
}
func (OSEnv) Dial(ctx context.Context, network, addr string) error {
	c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return err
	}
	return c.Close()
}
