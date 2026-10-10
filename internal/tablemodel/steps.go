package tablemodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// Verdicts of a step. A met expectation is named for what was expected.
const (
	VerdictPass                    = "pass"
	VerdictExpectedCounterexample  = "expected-counterexample"
	VerdictExpectedScopeControl    = "expected-scope-control"
	VerdictExpectedMutationFailure = "expected-mutation-failure"
	VerdictFail                    = "fail"
	VerdictTimeout                 = "timeout"
)

// Step is one configuration of a suite and the result it must reach.
type Step struct {
	Name     string // configuration base name: MCTableMachine
	Module   string // the module file: MCTableMachine.tla
	Workers  int    // TLC workers
	Kind     string // pass, invariant or action
	Property string // the violated name for invariant and action
	Met      string // the verdict a met expectation is called
}

var tableWitnesses = []struct{ name, invariant string }{
	{"OnePlace", "OnePlacePerTable"},
	{"Bind", "BindPreservesOwned"},
	{"BindRetained", "BindPreservesOwned"},
	{"BoundAlias", "CellWritesPreserveBoundSets"},
	{"DropAlias", "DropPreservesBound"},
}

// TableModes are the modes of the table model check.
var TableModes = []string{"strict", "contracts", "witnesses", "controls", "all"}

// TableSteps plans the table model check for a mode.
//
//   - contracts: the model with its fixed point, expected to pass.
//   - witnesses: the five findings, each expected to violate its invariant.
//   - controls: the cross-table configuration, a scope control that is expected
//     to violate OneTablePerMember (the model is per table by design).
//   - all: the three above.
//   - strict: the five findings expected to PASS. This is the desired contract,
//     and it fails on the first finding the table code still has.
func TableSteps(mode string) ([]Step, error) {
	valid := false
	for _, m := range TableModes {
		valid = valid || m == mode
	}
	if !valid {
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
	const module = "MCTableMachine.tla"
	var steps []Step
	if mode == "contracts" || mode == "all" {
		steps = append(steps,
			Step{Name: "MCTableMachine", Module: module, Workers: 2, Kind: "pass", Property: "-", Met: VerdictPass},
			Step{Name: "MCTableFixedPoint", Module: module, Workers: 2, Kind: "pass", Property: "-", Met: VerdictPass})
	}
	if mode == "strict" || mode == "witnesses" || mode == "all" {
		for _, w := range tableWitnesses {
			s := Step{Name: "MCTable" + w.name, Module: module, Workers: 1, Kind: "pass", Property: "-", Met: VerdictPass}
			if mode != "strict" {
				s.Kind, s.Property, s.Met = "invariant", w.invariant, VerdictExpectedCounterexample
			}
			steps = append(steps, s)
		}
	}
	if mode == "controls" || mode == "all" {
		steps = append(steps, Step{Name: "MCTableCrossTable", Module: module, Workers: 1, Kind: "invariant", Property: "OneTablePerMember", Met: VerdictExpectedScopeControl})
	}
	return steps, nil
}

// MemberSuites are the suites of the member protocol check.
var MemberSuites = []string{"all", "member", "epoch", "small"}

var memberCases = []struct{ name, module, kind, property string }{
	{"MCMemberTable", "MCMemberTable", "pass", "-"},
	{"MCMemberFixedPoint", "MCMemberTable", "pass", "-"},
	{"MCMemberBrokenMove", "MCMemberTable", "invariant", "RecordSetLink"},
	{"MCMemberBrokenAlias", "MCMemberTable", "invariant", "NoOwnedAlias"},
	{"MCEpochMemberTable", "MCEpochMemberTable", "pass", "-"},
	{"MCEpochMemberFixedPoint", "MCEpochMemberTable", "pass", "-"},
	{"MCEpochMemberBroken", "MCEpochMemberTable", "invariant", "NoEpochLeak"},
	{"MCEpochMemberBrokenStale", "MCEpochMemberTable", "action", "StaleWritesRefuse"},
}

// MemberSteps plans the member protocol check. The positive instances run
// with the given workers; every mutation control runs with one. Suite member
// and epoch select one module's cases; small drops the two large instances.
func MemberSteps(suite string, workers int) ([]Step, error) {
	valid := false
	for _, s := range MemberSuites {
		valid = valid || s == suite
	}
	if !valid {
		return nil, fmt.Errorf("unknown suite %q", suite)
	}
	if workers < 1 {
		return nil, fmt.Errorf("workers must be positive")
	}
	var steps []Step
	for _, c := range memberCases {
		if suite == "member" && c.module != "MCMemberTable" || suite == "epoch" && c.module != "MCEpochMemberTable" {
			continue
		}
		if suite == "small" && (c.name == "MCMemberTable" || c.name == "MCEpochMemberTable") {
			continue
		}
		s := Step{Name: c.name, Module: c.module + ".tla", Workers: workers, Kind: c.kind, Property: c.property, Met: VerdictPass}
		if c.kind != "pass" {
			s.Workers, s.Met = 1, VerdictExpectedMutationFailure
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// StepResult is what one step did.
type StepResult struct {
	Step    Step
	Verdict string
	OK      bool
	Code    int
	Seconds time.Duration
	Outcome tlc.Outcome
	Log     string
}

// StepOptions is a run of steps under one budget.
type StepOptions struct {
	Jar    string        // the TLC jar
	Java   string        // the java program
	Models string        // the directory of the modules and configurations
	Dir    string        // output directory: logs and the private copy of the models
	Budget time.Duration // the whole run's limit
	Exec   tlc.Executor  // tlc.Execute when nil
	OnStep func(StepResult)
}

// RunSteps runs the steps in order and stops at the first that is not the
// result it declares. The models are copied to Dir/work first and TLC runs
// there, so the checkout gains no error-trace files. The last result is the
// failing one.
func RunSteps(steps []Step, o StepOptions) ([]StepResult, error) {
	exec := o.Exec
	if exec == nil {
		exec = tlc.Execute
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create %s: %v", dir, err)
	}
	work := filepath.Join(dir, "work")
	if err := tlc.CopyModels(o.Models, work); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), o.Budget)
	defer cancel()
	var results []StepResult
	for _, s := range steps {
		start := time.Now()
		log := filepath.Join(dir, s.Name+".log")
		scratch, err := os.MkdirTemp(dir, "tlc-")
		if err != nil {
			return results, err
		}
		code := exec(ctx, tlc.Run{
			Java: o.Java, Jar: o.Jar, Dir: work,
			JVM:     []string{"-XX:+UseParallelGC", "-Xmx2g"},
			TmpDir:  scratch,
			Workers: s.Workers,
			MetaDir: filepath.Join(scratch, "states"),
			Config:  s.Name + ".cfg",
			Module:  s.Module,
		}, log)
		// ignored: a cleanup of this step's own scratch directory; the log read below is the report
		_ = safepath.RemoveUnder(dir, scratch)
		raw, err := os.ReadFile(log)
		if err != nil {
			return results, fmt.Errorf("cannot read %s: %v", log, err)
		}
		r := StepResult{Step: s, Code: code, Seconds: time.Since(start), Outcome: tlc.Parse(string(raw)), Log: log}
		switch {
		case ctx.Err() != nil && code == tlc.ExitTimeout:
			r.Verdict = VerdictTimeout
		case tlc.Accepts(tlc.Case{Expected: s.Kind, Property: s.Property}, code, string(raw), ""):
			r.Verdict, r.OK = s.Met, true
		default:
			r.Verdict = VerdictFail
		}
		results = append(results, r)
		if o.OnStep != nil {
			o.OnStep(r)
		}
		if !r.OK {
			break
		}
	}
	return results, nil
}
