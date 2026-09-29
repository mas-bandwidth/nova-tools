package tlc

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

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

// LoadCases reads tla/CASES.tsv under root and checks it against the tree: it
// must name every MC*.cfg exactly once and every module must exist.
func LoadCases(root string) ([]Case, error) {
	dir := filepath.Join(root, "tla")
	f, err := os.Open(filepath.Join(dir, CasesFile))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %v", filepath.Join(dir, CasesFile), err)
	}
	defer f.Close()
	cases, err := ParseCases(f)
	if err != nil {
		return nil, err
	}
	cfgs, err := filepath.Glob(filepath.Join(dir, "MC*.cfg"))
	if err != nil {
		return nil, err
	}
	var actual, named []string
	for _, p := range cfgs {
		actual = append(actual, filepath.Base(p))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		named = append(named, c.Config)
		if seen[c.Config] {
			return nil, fmt.Errorf("%s must name every MC*.cfg exactly once: %s is named twice", CasesFile, c.Config)
		}
		seen[c.Config] = true
	}
	sort.Strings(actual)
	sort.Strings(named)
	if strings.Join(actual, "\n") != strings.Join(named, "\n") {
		return nil, fmt.Errorf("%s must name every MC*.cfg exactly once", CasesFile)
	}
	for _, c := range cases {
		if info, err := os.Stat(filepath.Join(dir, c.Module)); err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("missing instance module: %s", c.Module)
		}
	}
	return cases, nil
}

// RequiredGroups returns the groups that hold a required case, sorted: the
// matrix a CI run derives.
func RequiredGroups(cases []Case) []string {
	set := map[string]bool{}
	for _, c := range cases {
		if c.Gate == "required" {
			set[c.Group] = true
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// Select returns the cases one run covers: a group, or shard number shard of
// shards (every shards'th case from shard), never both.
func Select(cases []Case, group string, shards, shard int) ([]Case, error) {
	if shards < 1 || shard < 0 || shard >= shards {
		return nil, errors.New("use a positive shard count and a zero-based shard below it")
	}
	if shards > len(cases) {
		return nil, errors.New("shard count exceeds the number of declared cases")
	}
	if group != "" {
		if shards != 1 || shard != 0 {
			return nil, errors.New("--group and shard selection cannot be combined")
		}
		var chosen []Case
		for _, c := range cases {
			if c.Group == group {
				chosen = append(chosen, c)
			}
		}
		if len(chosen) == 0 {
			return nil, fmt.Errorf("unknown group: %s", group)
		}
		return chosen, nil
	}
	var chosen []Case
	for i := shard; i < len(cases); i += shards {
		chosen = append(chosen, cases[i])
	}
	return chosen, nil
}
