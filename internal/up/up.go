// Package up is nova-up: it takes one machine from nothing to a first sprint
// (docs/SPEC-UP.md). Setup is a list of steps, each in its own file, each
// registering itself from an init into the one registry (Register), so a step
// added later adds a file and edits no other. A run plans every step first,
// each saying what it found (ok, create, change, missing), and applies only
// when the plan holds no missing step: a dependency nova-up cannot provide
// stops the run before anything is written (SPEC-UP "Plan, then apply"). A
// step's plan reads, its apply writes, and a second run over an applied
// machine plans ok everywhere and changes nothing (SPEC-UP "Idempotence").
//
// Everything a step touches outside its root goes through Machine: the
// programs it runs (Exec), the clock, the randomness a password is drawn
// from, the operating system and the home directory, so a test runs every
// step on a fake machine rooted in a temporary directory.
package up

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// State is what a step's plan found.
type State string

const (
	OK      State = "ok"      // already as the step wants it; apply does nothing
	Create  State = "create"  // absent; apply makes it
	Change  State = "change"  // present and different; apply brings it to the step's form
	Missing State = "missing" // a dependency nova-up cannot provide; the run stops before any apply
)

// Finding is one step's plan: its state and a one-line detail. A missing
// finding's detail carries the install command that provides it.
type Finding struct {
	State  State
	Detail string
}

// Step is one part of a setup. Plan reads and never writes; Apply runs only
// after a plan of create or change and brings the machine to what Plan wants.
type Step struct {
	Name  string
	Order int // steps run in ascending order; a step's order is stated in docs/SPEC-UP.md
	Plan  func(e *Env) Finding
	Apply func(e *Env) error
}

var registry []Step

// Register adds a step; each step's file calls it from an init. Two steps of
// one name or one order are a bug of the build, refused at init.
func Register(s Step) {
	for _, r := range registry {
		if r.Name == s.Name || r.Order == s.Order {
			panic(fmt.Sprintf("up: step %s (order %d) collides with step %s (order %d)", s.Name, s.Order, r.Name, r.Order))
		}
	}
	registry = append(registry, s)
	slices.SortFunc(registry, func(a, b Step) int { return a.Order - b.Order })
}

// Steps is the registry in run order.
func Steps() []Step { return slices.Clone(registry) }

// Cmd is one program run: its name, arguments, working directory, the
// variables added to its environment, and its standard input. A secret
// travels in Stdin or in a variable, never in Args.
type Cmd struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin string
}

// String is the command as a line, for a refusal: name and arguments, never Stdin or Env.
func (c Cmd) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// Exec is how a step finds and runs programs.
type Exec interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, c Cmd) (stdout string, err error)
}

// Machine is the world a run sees outside its root.
type Machine struct {
	GOOS string // runtime.GOOS on a real machine
	Home string // the home directory; the default root and the service manager's directory are under it
	UID  int    // the login's uid, for launchd's gui domain
	Exec Exec
	Now  func() time.Time
	Rand io.Reader // passwords are drawn from it
}

// Env is one run: the machine, the root and what the steps share.
type Env struct {
	Machine
	Ctx  context.Context
	Root string
}

// Path is a path under the root.
func (e *Env) Path(elem ...string) string { return filepath.Join(append([]string{e.Root}, elem...)...) }

// Run runs c and wraps a failure with the command line and its output's last line.
func (e *Env) Run(c Cmd) (string, error) {
	out, err := e.Exec.Run(e.Ctx, c)
	if err != nil {
		return out, fmt.Errorf("%s: %w", c, err)
	}
	return out, nil
}

// Line is one step's printed plan: `UP <step> <state> <detail>`.
type Line struct {
	Step string
	Finding
}

func (l Line) String() string {
	return strings.TrimSpace(fmt.Sprintf("UP %s %s %s", l.Step, l.State, l.Detail))
}

// Result is one run: the plan's lines, how many steps were applied, and the
// error that stopped it. Missing lists the steps that stopped the run before
// any apply.
type Result struct {
	Lines   []Line
	Applied int
	Missing []string
	Err     error
}

// Changes is the number of steps the plan found not ok.
func (r Result) Changes() int {
	n := 0
	for _, l := range r.Lines {
		if l.State != OK {
			n++
		}
	}
	return n
}

// Up plans every registered step and, unless dryRun or a step is missing,
// applies each step that is not ok, in order (SPEC-UP "Plan, then apply").
// A step is planned after the steps before it are applied, so its plan reads
// the machine they left; a dry run plans each step on the machine as it is.
func Up(e *Env, dryRun bool) Result {
	var r Result
	steps := Steps()
	plans := make([]Finding, len(steps))
	for i, s := range steps {
		plans[i] = s.Plan(e)
		if plans[i].State == Missing {
			r.Missing = append(r.Missing, s.Name)
		}
	}
	if dryRun || len(r.Missing) > 0 {
		for i, s := range steps {
			r.Lines = append(r.Lines, Line{s.Name, plans[i]})
		}
		return r
	}
	for i, s := range steps {
		f := plans[i]
		if i > 0 && r.Applied > 0 {
			f = s.Plan(e) // what the steps applied before it left
		}
		r.Lines = append(r.Lines, Line{s.Name, f})
		if f.State == OK {
			continue
		}
		if f.State == Missing {
			r.Missing = append(r.Missing, s.Name)
			r.Err = fmt.Errorf("step %s is missing after the steps before it were applied: %s", s.Name, f.Detail)
			return r
		}
		if err := s.Apply(e); err != nil {
			r.Err = fmt.Errorf("step %s: %w", s.Name, err)
			return r
		}
		r.Applied++
	}
	return r
}
