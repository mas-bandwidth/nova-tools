package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// config is the repository's checked-in config.
type config struct {
	dir      string // relative paths are read against it; "" is the working directory
	path     string
	suites   map[string]bool
	skips    map[[2]string]bool // suite, test
	tables   map[string]*table
	order    []string // table names in the order the config gives them
	baseline string
}

// table is one required coverage table: every row x column pair is covered by
// a test, or held as a gap with a reason.
type table struct {
	source map[string][2]string // axis (rows, columns, pairs) -> kind (file, run), argument
	gaps   map[[2]string]bool
}

var axes = []string{"rows", "columns", "pairs"}

// at is a path as the process opens it.
func (c *config) at(p string) string {
	if c.dir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

func readConfig(dir, path string) (*config, error) {
	c := &config{dir: dir, path: path, suites: map[string]bool{}, skips: map[[2]string]bool{}, tables: map[string]*table{}}
	data, err := os.ReadFile(c.at(path))
	if err != nil {
		return nil, refuse("ls -l "+path, "reading the config: %v", err)
	}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		f := strings.Split(line, "\t")
		at := fmt.Sprintf("sed -n %dp %s", n+1, path)
		bad := func(want string) error {
			return refuse(at, "%s:%d: %q wants %s, separated by tabs", path, n+1, f[0], want)
		}
		for _, field := range f {
			if field == "" || field != strings.TrimSpace(field) {
				return nil, refuse(at, "%s:%d: a field is empty or starts or ends with a blank: %q", path, n+1, field)
			}
		}
		switch f[0] {
		case "suite":
			if len(f) != 2 {
				return nil, bad("NAME")
			}
			c.suites[f[1]] = true
		case "skip":
			if len(f) != 4 {
				return nil, bad("SUITE, TEST and REASON")
			}
			c.skips[[2]string{f[1], f[2]}] = true
		case "cover":
			if len(f) != 5 || !slices.Contains(axes, f[2]) || (f[3] != "file" && f[3] != "run") {
				return nil, bad("TABLE, rows|columns|pairs, file|run and its PATH or COMMAND")
			}
			c.table(f[1]).source[f[2]] = [2]string{f[3], f[4]}
		case "gap":
			if len(f) != 5 {
				return nil, bad("TABLE, ROW, COLUMN and REASON")
			}
			c.table(f[1]).gaps[[2]string{f[2], f[3]}] = true
		case "baseline":
			if len(f) != 2 || c.baseline != "" {
				return nil, bad("DIR, once")
			}
			c.baseline = f[1]
		default:
			return nil, refuse(at, "%s:%d: %q is no entry; the entries are suite, skip, cover, gap and baseline", path, n+1, f[0])
		}
	}
	var missing []string
	if len(c.suites) == 0 {
		missing = append(missing, "suite line (a config that registers nothing checks nothing)")
	}
	if c.baseline == "" {
		missing = append(missing, "baseline line")
	}
	if len(missing) > 0 {
		return nil, refuse("nova-testguard help check", "%s has no %s", path, strings.Join(missing, " and no "))
	}
	for _, name := range c.order {
		for _, axis := range axes {
			if _, ok := c.tables[name].source[axis]; !ok {
				return nil, refuse("grep -n "+name+" "+path, "%s: table %s has no %s source; a table names rows, columns and pairs", path, name, axis)
			}
		}
	}
	return c, nil
}

func (c *config) table(name string) *table {
	t := c.tables[name]
	if t == nil {
		t = &table{source: map[string][2]string{}, gaps: map[[2]string]bool{}}
		c.tables[name] = t
		c.order = append(c.order, name)
	}
	return t
}

// report is the outcome of one check.
type report struct {
	findings []string
	notes    []string
	rules    map[string]int
	stats    string
}

func (r *report) add(rule, id, what, fix string) {
	r.findings = append(r.findings, fmt.Sprintf("RED %s %s: %s; fix: %s", rule, id, what, fix))
	r.rules[rule]++
}

func (r *report) byRule() string {
	var parts []string
	for rule, n := range r.rules {
		parts = append(parts, fmt.Sprintf("%s %d", rule, n))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// id is how a finding names a test: SUITE<TAB>TEST, as a skip line holds it.
func id(suite, test string) string { return suite + "\t" + test }

// guard reads the config and the results and runs every check. With write
// set, a run whose only findings are tests the baseline does not list yet
// adds those tests to the baseline; any other finding writes nothing.
func guard(dir, configPath string, args []string, write bool, sh shellFunc) (*report, error) {
	kinds, paths := make([]string, len(args)), make([]string, len(args))
	for i, a := range args {
		f, path, err := format(a)
		if err != nil {
			return nil, err
		}
		kinds[i], paths[i] = f, path
	}
	c, err := readConfig(dir, configPath)
	if err != nil {
		return nil, err
	}
	res := results{}
	for i, f := range kinds {
		path := paths[i]
		data, err := os.ReadFile(c.at(path))
		if err != nil {
			return nil, refuse("ls -l "+path, "reading a result file: %v", err)
		}
		if err := formats[f](res, path, data); err != nil {
			return nil, err
		}
	}
	tests, skips := 0, 0
	for _, s := range res {
		tests += s.tests()
		skips += len(s.skipped)
	}
	if tests == 0 {
		return nil, refuse("ls -l "+strings.Join(args, " "), "the result files hold 0 tests that ran; a run of nothing is never a pass")
	}
	rep := &report{rules: map[string]int{}}

	// Failures and incomplete runs.
	for _, name := range sortedKeys(res) {
		s := res[name]
		for _, t := range sortedKeys(s.failed) {
			rep.add("failed", id(name, t), "failed, or started and never ended", "make it pass")
		}
		if s.countFailed > 0 {
			rep.add("failed", name, fmt.Sprintf("reports %d failed", s.countFailed), "make them pass")
		}
		for _, p := range s.problems {
			rep.add("incomplete", name, p, "make the run complete")
		}
	}

	// 1. Skips: the ledger is the exact set of skips.
	skipped := map[[2]string]bool{}
	for name, s := range res {
		for t := range s.skipped {
			skipped[[2]string{name, t}] = true
		}
	}
	for _, k := range sortedPairs(skipped) {
		if !c.skips[k] {
			rep.add("skip", id(k[0], k[1]), "skipped, and the ledger does not hold it", "make it run, or add the line skip<TAB>SUITE<TAB>TEST<TAB>REASON to "+configPath)
		}
	}
	for _, k := range sortedPairs(c.skips) {
		if !skipped[k] {
			rep.add("stale-skip", id(k[0], k[1]), "ledgered as a skip, and it did not skip here (it ran, or it is gone)", "delete its skip line from "+configPath)
		}
	}

	// 2. Suites: every registered suite ran a test, and every suite that ran a test is registered.
	for _, name := range sortedKeys(c.suites) {
		s := res[name]
		switch {
		case s == nil:
			rep.add("missing-suite", name, "registered, and absent from every result file", "run the suite and pass its results, or delete its suite line from "+configPath)
		case s.tests() == 0:
			rep.add("empty-suite", name, "registered, and ran 0 tests", "make its tests run (a build tag, a filter or a missing file can leave none)")
		}
	}
	for _, name := range sortedKeys(res) {
		if !c.suites[name] && res[name].tests() > 0 {
			rep.add("unregistered-suite", name, fmt.Sprintf("ran %d tests and is not registered, so its disappearance would go unseen", res[name].tests()), "add a suite line for it to "+configPath)
		}
	}

	// 3. Coverage tables.
	pairs := 0
	for _, name := range c.order {
		n, err := c.cover(name, rep, sh)
		if err != nil {
			return nil, err
		}
		pairs += n
	}

	// 4. The baseline.
	unlisted, err := c.checkBaseline(res, rep)
	if err != nil {
		return nil, err
	}
	if write {
		if len(rep.findings) > rep.rules["unlisted"] {
			rep.notes = append(rep.notes, "nothing written: --write-baseline adds tests only to a run whose only findings are unlisted tests")
		} else {
			for _, k := range unlisted {
				p := c.at(c.entry(k[0], k[1]))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return nil, refuse("ls -ld "+c.baseline, "writing the baseline: %v", err)
				}
				if err := os.WriteFile(p, nil, 0o644); err != nil {
					return nil, refuse("ls -ld "+filepath.Dir(p), "writing the baseline: %v", err)
				}
			}
			rep.findings, rep.rules = nil, map[string]int{}
			rep.notes = append(rep.notes, fmt.Sprintf("wrote %d baseline entries under %s; commit them", len(unlisted), c.baseline))
		}
	}
	rep.stats = fmt.Sprintf("read %d result files: %d suites, %d tests run, %d skipped; %d coverage tables, %d pairs",
		len(args), len(res), tests, skips, len(c.order), pairs)
	return rep, nil
}

// lines reads one axis of a table: a file, or a command's stdout. Blank lines
// and '#' comments are left out.
func (c *config) lines(name, axis string, sh shellFunc) ([]string, error) {
	src := c.tables[name].source[axis]
	var data []byte
	var err error
	if src[0] == "file" {
		if data, err = os.ReadFile(c.at(src[1])); err != nil {
			return nil, refuse("ls -l "+src[1], "table %s %s: %v", name, axis, err)
		}
	} else if data, err = sh(c.at("."), src[1]); err != nil {
		return nil, refuse(src[1], "table %s %s: the command %q failed: %v", name, axis, src[1], err)
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out, nil
}

// cover checks one table and returns its number of required pairs.
func (c *config) cover(name string, rep *report, sh shellFunc) (int, error) {
	got := map[string][]string{}
	for _, axis := range axes {
		l, err := c.lines(name, axis, sh)
		if err != nil {
			return 0, err
		}
		got[axis] = l
	}
	rows, cols := set(got["rows"]), set(got["columns"])
	for _, axis := range []string{"rows", "columns"} {
		if len(got[axis]) == 0 {
			rep.add("uncovered", name, "its "+axis+" list is empty, so the table vouches for nothing", "fix the "+axis+" source of table "+name)
		}
	}
	covered := map[[2]string]bool{}
	for _, l := range got["pairs"] {
		f := strings.Split(l, "\t")
		if len(f) != 2 {
			return 0, refuse(c.tables[name].source["pairs"][1], "table %s pairs: %q is not ROW<TAB>COLUMN", name, l)
		}
		if !rows[f[0]] || !cols[f[1]] {
			rep.add("uncovered", name+" "+f[0]+" x "+f[1], "a covered pair names a row or column the table does not list", "add it to the rows or columns source of table "+name)
			continue
		}
		covered[[2]string{f[0], f[1]}] = true
	}
	t := c.tables[name]
	for _, r := range sortedKeys(rows) {
		for _, col := range sortedKeys(cols) {
			p := [2]string{r, col}
			if !covered[p] && !t.gaps[p] {
				rep.add("uncovered", name+" "+r+" x "+col, "no test covers this pair, and no gap holds it", "cover it, or add a gap line with the reason to "+c.path)
			}
		}
	}
	for _, p := range sortedPairs(t.gaps) {
		gid := name + " " + p[0] + " x " + p[1]
		switch {
		case !rows[p[0]] || !cols[p[1]]:
			rep.add("stale-gap", gid, "a gap names a row or column the table does not list", "delete its gap line from "+c.path)
		case covered[p]:
			rep.add("stale-gap", gid, "a gap holds a pair that is covered", "delete its gap line from "+c.path)
		}
	}
	return len(rows) * len(cols), nil
}

// escape makes a suite or test name one path segment: URL path escaping, with
// a leading dot escaped too, so "." and ".." stay names.
func escape(name string) string {
	e := url.PathEscape(name)
	if strings.HasPrefix(e, ".") {
		e = "%2E" + e[1:]
	}
	return e
}

func unescape(seg string) string {
	s, err := url.PathUnescape(seg)
	if err != nil {
		return seg
	}
	return s
}

// entry is a test's file in the baseline: DIR/SUITE/TEST, each escaped.
func (c *config) entry(suite, test string) string {
	return filepath.Join(c.baseline, escape(suite), escape(test))
}

// checkBaseline holds the run to the baseline: one empty file per test that
// runs, under a directory per suite. A listed test that did not run vanished;
// a test that ran with no entry is unlisted. A suite read from a "ran N,
// failed M" line names no tests and has no entries. It returns the unlisted
// tests.
func (c *config) checkBaseline(res results, rep *report) ([][2]string, error) {
	dirs, err := os.ReadDir(c.at(c.baseline))
	if err != nil {
		return nil, refuse("ls -ld "+c.baseline, "the baseline %s cannot be read: %v", c.baseline, err)
	}
	listed := map[[2]string]bool{}
	for _, d := range dirs {
		p := filepath.Join(c.baseline, d.Name())
		suite := unescape(d.Name())
		if !d.IsDir() || !c.suites[suite] || escape(suite) != d.Name() {
			rep.add("baseline", p, "is not the directory of a registered suite", "delete it, or register its suite in "+c.path)
			continue
		}
		files, err := os.ReadDir(c.at(p))
		if err != nil {
			return nil, refuse("ls -ld "+p, "the baseline %s cannot be read: %v", p, err)
		}
		for _, f := range files {
			test := unescape(f.Name())
			if f.IsDir() || escape(test) != f.Name() {
				rep.add("baseline", filepath.Join(p, f.Name()), "is not a test entry (a file named for an escaped test name)", "delete it")
				continue
			}
			listed[[2]string{suite, test}] = true
		}
	}
	for _, k := range sortedPairs(listed) {
		s := res[k[0]]
		if s == nil || s.tests() == 0 {
			continue // missing-suite or empty-suite names it once
		}
		if !s.ran[k[1]] {
			rep.add("vanished", id(k[0], k[1]), "ran on the baseline and did not run here", "make it run, or delete "+c.entry(k[0], k[1])+" in this change")
		}
	}
	var unlisted [][2]string
	for _, name := range sortedKeys(res) {
		if !c.suites[name] {
			continue
		}
		for _, t := range sortedKeys(res[name].ran) {
			if k := [2]string{name, t}; !listed[k] {
				unlisted = append(unlisted, k)
				rep.add("unlisted", id(name, t), "ran, and the baseline does not list it", "add the empty file "+c.entry(name, t)+" in this change")
			}
		}
	}
	return unlisted, nil
}

func set(list []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range list {
		m[s] = true
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedPairs(m map[[2]string]bool) [][2]string {
	keys := make([][2]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}
