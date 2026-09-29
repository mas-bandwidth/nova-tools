package tlc

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// This file turns a row of tla/CASES.tsv into the Case a run is judged by: what
// each column means, what a default is, and what is refused. Expected, Property
// and Deadlock decide whether a run passes (Accepts in outcome.go, NoDeadlock in
// suite.go), so the file is one of ResultFiles: a change to how a row is read
// stales every record, though the row's own bytes are unchanged. The rest of
// the plan's handling (checking it against the tree, choosing the cases of a
// run) is in cases.go.

// CasesFile and RunsFile are the case plan and the run records, under tla/.
const (
	CasesFile = "CASES.tsv"
	RunsFile  = "RUNS.tsv"
)

// Case is one row of tla/CASES.tsv: one MC configuration, the module it
// instantiates and the result it must reach.
type Case struct {
	Config   string // MCFoo.cfg
	Module   string // MCFoo.tla
	Expected string // pass, invariant, action or temporal
	Property string // "-" for pass; the violated name (or names joined by "|") otherwise
	Deadlock string // check, or ignore-terminal (the models end in a terminal stutter by design)
	Group    string // the execution group; one CI job runs one group
	Gate     string // required, or bench
	Debt     string // "-", or why a bench case has no passing measurement
}

var casesHeader = []string{"config", "module", "expected", "property", "deadlock", "group", "gate", "debt"}

var (
	moduleRE = regexp.MustCompile(`^MC[A-Za-z0-9]+\.tla$`)
	groupRE  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// ParseCases reads and checks the case plan. It does not look at the
// filesystem: LoadCases adds the checks against the files.
func ParseCases(r io.Reader) ([]Case, error) {
	cr := csv.NewReader(r)
	cr.Comma = '\t'
	cr.LazyQuotes = true
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s is not tab-separated text: %v", CasesFile, err)
	}
	if len(rows) == 0 {
		return nil, errors.New(CasesFile + " is empty")
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	for _, want := range casesHeader {
		if _, ok := col[want]; !ok {
			return nil, fmt.Errorf("%s has no %q column", CasesFile, want)
		}
	}
	var cases []Case
	for n, row := range rows[1:] {
		if len(row) != len(rows[0]) {
			return nil, fmt.Errorf("%s row %d has %d fields, want %d", CasesFile, n+2, len(row), len(rows[0]))
		}
		get := func(name string) string { return row[col[name]] }
		c := Case{
			Config: get("config"), Module: get("module"), Expected: get("expected"),
			Property: get("property"), Deadlock: get("deadlock"), Group: get("group"),
			Gate: get("gate"), Debt: get("debt"),
		}
		if err := c.check(); err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func (c Case) check() error {
	switch {
	case !moduleRE.MatchString(c.Module):
		return fmt.Errorf("invalid instance module: %s", c.Module)
	case c.Expected != "pass" && c.Expected != "invariant" && c.Expected != "action" && c.Expected != "temporal":
		return fmt.Errorf("invalid expected outcome: %s", c.Config)
	case c.Deadlock != "check" && c.Deadlock != "ignore-terminal":
		return fmt.Errorf("invalid deadlock policy: %s", c.Config)
	case (c.Expected == "pass") != (c.Property == "-"):
		return fmt.Errorf("expected property required only for a counterexample: %s", c.Config)
	case c.Gate != "required" && c.Gate != "bench", !groupRE.MatchString(c.Group):
		return fmt.Errorf("invalid gate/group: %s", c.Config)
	case c.Gate == "required" && c.Debt != "-":
		return fmt.Errorf("required case cannot be waived as debt: %s", c.Config)
	}
	return nil
}
