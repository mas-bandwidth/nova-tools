package swarm

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// THE BASE CHECKS: FIVE RULES A CODING CARD IS HELD TO BEFORE IT IS DEALT (#2636, #3083).
//
// The first four are each one class of the 2026-09-22 sprint's failed cards (rowan-new
// reports/failed-cards-2026-09-22.md), and each needs evidence the card text alone
// does not hold, so they run only under `nova-swarm lint --base-check`:
//
//	paths-at-base  every PATHS entry resolves at the card's base-sha, or is a new
//	               `_test` file, or (on a repair card) at its PR-HEAD. Class 9: card-nx-f19 named internal/decide/entry.go,
//	               which does not exist at its base, and ABSTAINed out-of-scope.
//	no-push-steps  no STEP runs `git push` or `gh`. Class 8: 39 nx-r and holdfix
//	               repair cards asked the wall, which holds no credential by design,
//	               to push and to post the REPAIR comment. The harvest pushes.
//	leg-in-fleet   every LEG/LEGS entry is a leg the fleet's leg table carries. The
//	               seven nx-r cards cut `LEG: lisp` were held: no bench carries
//	               `lisp`, the fleet's leg is `sbcl`.
//	deadline-p95   DEADLINE is at or above the measured p95 wall of the card's KIND.
//	               Class 11: 27 cards died at 1526 s against a 1500 s DEADLINE.
//	donewhen-test-name  DONE-WHEN names a test runner and a literal test that is
//	               absent at base-sha, so it can be red there (#3083, Jev's insertion
//	               2). 321 of 543 post-tune ledger rows read `donewhen missing`: the
//	               card never named a control a test could fail. "applied cleanly" and
//	               "make preflight" are English outcomes, not controls.
//
// NO EVIDENCE IS NOT NEGATIVE EVIDENCE. A check whose evidence was not handed over
// (no repository, a base-sha the repository does not hold, no leg table, no p95
// row for the kind) does not pass: it draws its finding with MISSING in the
// excerpt, naming what to hand over. `--base-check` is the ask for these checks,
// and a check that could not run is not a check that passed.

// BaseCheck is the evidence the five checks read. A nil table or an empty Repo
// means that evidence was not handed over.
type BaseCheck struct {
	Repo string    // a git repository holding the card's base-sha
	Legs FleetLegs // the fleet's leg table
	P95  KindP95   // p95 wall seconds of DONE cards, by KIND; `*` answers for any kind
}

// FleetLegs is the set of legs some bench of the fleet carries, lower case.
type FleetLegs map[string]bool

// KindP95 is the measured p95 wall, in seconds, of each kind's DONE cards.
type KindP95 map[string]int

// CardBaseRemedies is what each base token wants, in the table shape of
// CardHeaderRemedies, so `nova-swarm lint --rules` prints them beside the rest.
var CardBaseRemedies = map[string]string{
	"paths-at-base":      "every PATHS entry names a file, directory or glob that exists at the card's base-sha (or is a new `_test` file); cut the card from the tree at that sha, not from the issue's words, and hand the lint a repository holding the sha with `--repo <dir>` (failed-cards-2026-09-22 class 9, nx-f19)",
	"no-push-steps":      "a card ends at a local commit: no STEP runs `git push` or `gh`, because the wall holds no credential and the harvest pushes and comments; say `no gh, no push` in RULES, never as a STEP command (failed-cards-2026-09-22 class 8, holdfix and nx-r repair cards)",
	"leg-in-fleet":       "LEG: (or each LEGS: entry) is a leg the fleet's leg table carries -- `sbcl`, not `lisp` -- and the table is handed over with `--legs <file>` (failed-cards-2026-09-22 LEG row, #2728)",
	"donewhen-test-name": "DONE-WHEN: names the runner and a literal test that does not exist at the card's base-sha -- `go test ./<pkg> -run <TestName>`, `pytest <file>::<test_name>` (or `-k <test_name>`), `cargo test <name>` -- so the test can be red on base and green at head; an English outcome (\"applied cleanly\", \"make preflight\") or a runner with no test named is not a control, and the repository holding base-sha is handed over with `--repo <dir>` (#3083, the-control-is-the-sentence)",
	"deadline-p95":       "DEADLINE: is at or above the measured p95 wall of the card's KIND, in seconds (or `finish within <n> minutes`), and the p95 table is handed over with `--p95 <file>`; a kind with no row needs a `*` row or a measurement first (failed-cards-2026-09-22 class 11)",
}

// LintCardBase returns the base-check findings for one card, in the order the
// tokens are listed above.
func LintCardBase(raw []byte, bc BaseCheck) []CardHeaderFinding {
	var out []CardHeaderFinding
	add := func(check string, line int, excerpt string) {
		if line < 1 {
			line = 1
		}
		out = append(out, CardHeaderFinding{Check: check, Line: line, Excerpt: excerpt})
	}
	h, _ := cardHeaderBlock(raw)

	// 1. paths-at-base.
	if paths := h["PATHS"]; paths.found && paths.value != "none" && paths.value != "" {
		base, baseLine := baseSHAOf(h)
		if base == "" {
			base, baseLine = contractSha(firstLine(raw)), 1
		}
		switch {
		case bc.Repo == "":
			add("paths-at-base", paths.line, "MISSING: no repository handed over (--repo <dir>), so PATHS was not resolved at base-sha")
		case base == "":
			add("paths-at-base", paths.line, "MISSING: the card names no base-sha (no `base-sha:` line, no `sha=` on the contract line), so PATHS was not resolved")
		default:
			full, err := baseGit(bc.Repo, "rev-parse", "--verify", "--quiet", base+"^{commit}")
			if err != nil || full == "" {
				add("paths-at-base", baseLine, fmt.Sprintf("MISSING: base-sha %s is not a commit in %s; fetch it, then lint again", base, bc.Repo))
				break
			}
			miss, err := pathsMissingAt(bc.Repo, full, splitPathsValue(paths.value))
			if err != nil {
				add("paths-at-base", paths.line, fmt.Sprintf("MISSING: could not list the tree at %s: %v", short12(full), err))
				break
			}
			// A REPAIR CARD'S PATHS ARE THE PR'S OWN FILES. A card that carries
			// `PR-HEAD:` works on the PR's branch, and a file the PR adds is absent at
			// base by construction; such an entry resolves at PR-HEAD instead. A
			// PR-HEAD the repository does not hold is MISSING, never a pass.
			if len(miss) > 0 && h["PR-HEAD"].found && h["PR-HEAD"].value != "" {
				prh := h["PR-HEAD"]
				prFull, perr := baseGit(bc.Repo, "rev-parse", "--verify", "--quiet", prh.value+"^{commit}")
				if perr != nil || prFull == "" {
					add("paths-at-base", prh.line, fmt.Sprintf("MISSING: PR-HEAD %s is not a commit in %s, and PATHS %s is not at base-sha %s; fetch the PR head, then lint again", prh.value, bc.Repo, quoteDepends(miss), short12(full)))
					break
				}
				if miss, err = pathsMissingAt(bc.Repo, prFull, miss); err != nil {
					add("paths-at-base", paths.line, fmt.Sprintf("MISSING: could not list the tree at %s: %v", short12(prFull), err))
					break
				}
				if len(miss) > 0 {
					add("paths-at-base", paths.line, fmt.Sprintf("PATHS %s does not exist at base-sha %s or at PR-HEAD %s", quoteDepends(miss), short12(full), short12(prFull)))
				}
				break
			}
			if len(miss) > 0 {
				add("paths-at-base", paths.line, fmt.Sprintf("PATHS %s does not exist at base-sha %s", quoteDepends(miss), short12(full)))
			}
		}
	}

	// 2. no-push-steps.
	for _, f := range pushStepLines(raw) {
		add("no-push-steps", f.line, f.text)
	}

	// 3. leg-in-fleet.
	legLine, legs := 0, []string(nil)
	if f := h["LEG"]; f.found {
		legLine, legs = f.line, append(legs, splitLegs(f.value)...)
	}
	if f := h["LEGS"]; f.found {
		if legLine == 0 {
			legLine = f.line
		}
		legs = append(legs, splitLegs(f.value)...)
	}
	switch {
	case legLine == 0:
		add("leg-in-fleet", 1, "no LEG: line under the contract line; a coding card names the leg a bench must carry")
	case bc.Legs == nil:
		add("leg-in-fleet", legLine, "MISSING: no fleet leg table handed over (--legs <file>), so LEG was not checked")
	case len(legs) == 0:
		add("leg-in-fleet", legLine, "LEG: names no leg")
	default:
		var unknown []string
		for _, l := range legs {
			if !bc.Legs[strings.ToLower(l)] {
				unknown = append(unknown, l)
			}
		}
		if len(unknown) > 0 {
			add("leg-in-fleet", legLine, fmt.Sprintf("LEG %s is not in the fleet leg table (%s); no bench carries it", quoteDepends(unknown), strings.Join(sortedLegs(bc.Legs), ", ")))
		}
	}

	// 4. deadline-p95.
	kind, dl := h["KIND"], h["DEADLINE"]
	switch {
	case !dl.found:
		add("deadline-p95", 1, "no DEADLINE: line under the contract line")
	case !kind.found || kind.value == "":
		add("deadline-p95", dl.line, "no KIND: line, so the DEADLINE has no p95 to be held to")
	case bc.P95 == nil:
		add("deadline-p95", dl.line, "MISSING: no p95 table handed over (--p95 <file>), so DEADLINE was not checked")
	default:
		secs, ok := deadlineSeconds(dl.value)
		if !ok {
			add("deadline-p95", dl.line, fmt.Sprintf("DEADLINE: %q is not a number of seconds or `finish within <n> minutes`", dl.value))
			break
		}
		p95, have := bc.P95[kind.value]
		from := kind.value
		if !have {
			p95, have = bc.P95["*"]
			from = "*"
		}
		if !have {
			add("deadline-p95", dl.line, fmt.Sprintf("MISSING: the p95 table has no row for KIND %q and no `*` row, so DEADLINE %d s was not checked", kind.value, secs))
			break
		}
		if secs < p95 {
			add("deadline-p95", dl.line, fmt.Sprintf("DEADLINE %d s is below the p95 wall %d s of KIND %q (row %s)", secs, p95, kind.value, from))
		}
	}

	// 5. donewhen-test-name.
	dwLine, dw, dwFound := doneWhenValue(raw, h)
	switch {
	case !dwFound:
		add("donewhen-test-name", 1, "no DONE-WHEN: line; a card names the test that is red at base-sha and green when the work is done")
	default:
		tests, why := doneWhenTests(dw)
		if len(tests) == 0 {
			add("donewhen-test-name", dwLine, fmt.Sprintf("DONE-WHEN %q %s; an English outcome is not a control", oneLineCap(dw, 120), why))
			break
		}
		base, baseLine := baseSHAOf(h)
		if base == "" {
			base, baseLine = contractSha(firstLine(raw)), 1
		}
		switch {
		case bc.Repo == "":
			add("donewhen-test-name", dwLine, "MISSING: no repository handed over (--repo <dir>), so the DONE-WHEN test was not looked up at base-sha")
		case base == "":
			add("donewhen-test-name", dwLine, "MISSING: the card names no base-sha (no `base-sha:` line, no `sha=` on the contract line), so the DONE-WHEN test was not looked up")
		default:
			full, err := baseGit(bc.Repo, "rev-parse", "--verify", "--quiet", base+"^{commit}")
			if err != nil || full == "" {
				add("donewhen-test-name", baseLine, fmt.Sprintf("MISSING: base-sha %s is not a commit in %s; fetch it, then lint again", base, bc.Repo))
				break
			}
			var present []string
			for _, tn := range tests {
				at, err := testDefinedAt(bc.Repo, full, tn)
				if err != nil {
					add("donewhen-test-name", dwLine, fmt.Sprintf("MISSING: could not search the tree at %s for %s: %v", short12(full), tn.name, err))
					return out
				}
				if at {
					present = append(present, tn.name)
				}
			}
			if len(present) == len(tests) {
				add("donewhen-test-name", dwLine, fmt.Sprintf("DONE-WHEN test %s exists at base-sha %s, so it cannot be red there; name the new test the card adds", quoteDepends(present), short12(full)))
			}
		}
	}
	return out
}

// doneWhenValue is the card's DONE-WHEN: the header line when the header carries one,
// else the first `DONE-WHEN:` line anywhere in the card (a bullet or bold key allowed),
// which is where a card cut from an issue body carries it.
func doneWhenValue(raw []byte, h map[string]headerField) (int, string, bool) {
	if f := h["DONE-WHEN"]; f.found {
		return f.line, f.value, true
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if m := doneWhenBodyRE.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			return i + 1, strings.TrimSpace(m[1]), true
		}
	}
	return 0, "", false
}

var doneWhenBodyRE = regexp.MustCompile(`^[ \t]*(?:[-*][ \t]+)?\**DONE-WHEN:?\**:?[ \t]*(.*)$`)

// doneTest is one literal test a DONE-WHEN names, and the runner that names it.
type doneTest struct {
	runner string // go, pytest, cargo
	name   string
	// scope is the command's own targets, as written: go packages (`./internal/decide`,
	// `./x/...`, `<module>/x`), pytest files or directories (`tests/test_x.py`, `tests`).
	// The lookup searches only there, so a same-named test in another package or file
	// does not count as present. Empty is the whole repository (no target named).
	scope []string
}

var (
	goTestRunRE  = regexp.MustCompile(`\bgo[ \t]+test\b[^` + "`" + `]*?[ \t]-(?:test\.)?run(?:=|[ \t]+)("[^"]*"|'[^']*'|[^ \t` + "`" + `]+)`)
	goTestRE     = regexp.MustCompile(`\bgo[ \t]+test\b`)
	goTestSegRE  = regexp.MustCompile(`\bgo[ \t]+test\b[^` + "`" + `;&\n]*`)
	pytestSegRE  = regexp.MustCompile(`\bpytest\b[^` + "`" + `;&\n]*`)
	pytestNodeRE = regexp.MustCompile(`\bpytest\b[^` + "`" + `;&\n]*?[ \t]["']?([^ \t"'` + "`" + `;&:]+)::([A-Za-z_][A-Za-z0-9_]*)`)
	pytestKRE    = regexp.MustCompile(`\bpytest\b[^` + "`" + `]*?[ \t]-k(?:=|[ \t]+)["']?([A-Za-z_][A-Za-z0-9_]*)["']?(?:[ \t` + "`" + `]|$)`)
	cargoTestRE  = regexp.MustCompile(`\bcargo[ \t]+test\b((?:[ \t]+-{1,2}[A-Za-z-]+(?:[ \t]+[^-\s` + "`" + `]\S*)?)*)[ \t]+([A-Za-z_][A-Za-z0-9_:]*)`)
	pyTestNameRE = regexp.MustCompile(`^test[A-Za-z0-9_]*$`)
)

// doneWhenTests is every literal test the DONE-WHEN names, or nil and why not. A go
// `-run` pattern is split on `|`, stripped of `^`/`$` anchors and of a `/subtest`,
// and each piece must then be a literal `Test...` name: a regex that is no name
// (`TestDecide.*`) names no test a lookup can find.
func doneWhenTests(v string) ([]doneTest, string) {
	var out []doneTest
	for _, seg := range goTestSegRE.FindAllString(v, -1) {
		scope := goTestTargets(seg)
		for _, m := range goTestRunRE.FindAllStringSubmatch(seg, -1) {
			pat := strings.Trim(m[1], `"'`)
			for _, p := range strings.Split(pat, "|") {
				p = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(p), "^"), "$")
				if i := strings.Index(p, "/"); i >= 0 {
					p = p[:i]
				}
				if !goTestNameRE.MatchString(p) {
					return nil, fmt.Sprintf("runs `-run %s`, a pattern that is no literal test name", pat)
				}
				out = append(out, doneTest{runner: "go", name: p, scope: scope})
			}
		}
	}
	for _, seg := range pytestSegRE.FindAllString(v, -1) {
		for _, m := range pytestNodeRE.FindAllStringSubmatch(seg, -1) {
			if pyTestNameRE.MatchString(m[2]) {
				out = append(out, doneTest{runner: "pytest", name: m[2], scope: []string{m[1]}})
			}
		}
		for _, m := range pytestKRE.FindAllStringSubmatch(seg, -1) {
			if pyTestNameRE.MatchString(m[1]) {
				out = append(out, doneTest{runner: "pytest", name: m[1], scope: pytestTargets(seg)})
			}
		}
	}
	for _, m := range cargoTestRE.FindAllStringSubmatch(v, -1) {
		name := m[2]
		if i := strings.LastIndex(name, "::"); i >= 0 {
			name = name[i+2:]
		}
		if name != "" {
			out = append(out, doneTest{runner: "cargo", name: name})
		}
	}
	if len(out) > 0 {
		return out, ""
	}
	if goTestRE.MatchString(v) {
		return nil, "runs `go test` with no `-run <TestName>`, so no test is named that could be red"
	}
	return nil, "names no test runner and no test"
}

// testDefinedAt says whether the tree at sha defines the test, by its runner's
// definition shape: `func Name(` in a `_test.go` file, `def name(` in a Python file,
// `fn name(` in a Rust file. `git grep` exits 1 on no match, which is an answer
// (absent), not an error.
func testDefinedAt(repo, sha string, tn doneTest) (bool, error) {
	var pat string
	var specs []string
	switch tn.runner {
	case "go":
		pat, specs = `^func[ \t]+`+tn.name+`[ \t]*\(`, goTestPathspecs(repo, sha, tn.scope)
	case "pytest":
		pat, specs = `^[ \t]*(async[ \t]+)?def[ \t]+`+tn.name+`[ \t]*\(`, pytestPathspecs(repo, sha, tn.scope)
	default:
		pat, specs = `fn[ \t]+`+tn.name+`[ \t]*[(<]`, []string{"*.rs"}
	}
	args := append([]string{"-C", repo, "grep", "-q", "-E", "-e", pat, sha, "--"}, specs...)
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 && strings.TrimSpace(stderr.String()) == "" {
		return false, nil
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return false, fmt.Errorf("%v: %s", err, msg)
	}
	return false, err
}

// goTestTargets is the package arguments of one `go test` command: `.`, `./x`,
// `./x/...`, `../x`, or an import path (`github.com/org/repo/x`). A flag value
// (`-count 1`, `-timeout 60s`) is no package and is left out.
func goTestTargets(seg string) []string {
	var out []string
	for _, f := range strings.Fields(seg) {
		f = strings.Trim(f, `"'`)
		switch {
		case f == ".", f == "...", strings.HasPrefix(f, "./"), strings.HasPrefix(f, "../"):
			out = append(out, f)
		case !strings.HasPrefix(f, "-") && strings.Contains(f, "/") && strings.Contains(strings.SplitN(f, "/", 2)[0], "."):
			out = append(out, f)
		}
	}
	return out
}

// pytestValueOptions is the pytest options (core, pytest-cov, -xdist, -timeout,
// -html, -randomly, -rerunfailures, -repeat, hypothesis, -asyncio, -django) whose
// required value is the next word when no `=` joins it (options whose value is
// optional, like --cov, are left out): `--rootdir tests` names a directory
// to root at, not a directory of tests to run.
var pytestValueOptions = map[string]bool{}

func init() {
	for _, o := range []string{
		"-k", "-m", "-p", "-c", "-o", "-r", "-W", "-n", "--rootdir", "--confcutdir",
		"--basetemp", "--config-file", "--inifile", "--ignore", "--ignore-glob", "--deselect",
		"--override-ini", "--pythonwarnings", "--maxfail", "--tb", "--durations",
		"--durations-min", "--capture", "--import-mode", "--junitxml", "--junit-xml",
		"--junit-prefix", "--log-level", "--log-file", "--log-file-level", "--log-format",
		"--log-date-format", "--log-cli-level", "--log-cli-format", "--log-cli-date-format",
		"--show-capture", "--doctest-glob", "--color", "--code-highlight", "--pdbcls",
		"--last-failed-no-failures", "--lfnf", "--cov-report", "--cov-config",
		"--cov-fail-under", "--numprocesses", "--maxprocesses", "--dist", "--timeout",
		"--timeout-method", "--html", "--randomly-seed", "--reruns", "--reruns-delay",
		"--count", "--hypothesis-profile", "--hypothesis-seed", "--asyncio-mode", "--ds",
	} {
		pytestValueOptions[o] = true
	}
}

// pytestTakesValue says whether a pytest option word is followed by its value: a
// short value flag (`-k expr`), or a long option from pytestValueOptions written
// without `=` (`--rootdir tests`). `--opt=value` and attached short values
// (`-ktest_x`) carry their value in the same word.
func pytestTakesValue(f string) bool {
	if strings.Contains(f, "=") {
		return false
	}
	if !strings.HasPrefix(f, "--") && len(f) > 2 {
		return false
	}
	return pytestValueOptions[f]
}

// pytestTargets is the file and directory arguments of one pytest command: every
// word that is no flag, no flag's value (`-k x`, `--rootdir tests`) and no node id.
func pytestTargets(seg string) []string {
	var out []string
	fs := strings.Fields(seg)
	for i := 1; i < len(fs); i++ {
		f := strings.Trim(fs[i], `"'`)
		switch {
		case strings.HasPrefix(f, "-"):
			if pytestTakesValue(f) {
				i++ // an option whose value is the next word, never a target
			}
		case strings.Contains(f, "::"):
		case f != "":
			out = append(out, f)
		}
	}
	return out
}

// goTestPathspecs is the `*_test.go` pathspecs of the named packages at sha: a
// package is its own directory, `/...` adds every directory under it, and an import
// path is read against the module line of go.mod at sha. No package that resolves
// in this repository means the whole repository (the command's cwd is unknown).
func goTestPathspecs(repo, sha string, scope []string) []string {
	module := ""
	if len(scope) > 0 {
		if gm, err := baseGit(repo, "show", sha+":go.mod"); err == nil {
			for _, l := range strings.Split(gm, "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(l), "module "); ok {
					module = strings.Trim(strings.TrimSpace(v), `"`)
					break
				}
			}
		}
	}
	var specs []string
	for _, t := range scope {
		recursive := t == "..." || strings.HasSuffix(t, "/...")
		t = strings.TrimSuffix(strings.TrimSuffix(t, "..."), "/")
		switch {
		case strings.HasPrefix(t, "../"):
			continue
		case t == "." || t == "":
			t = ""
		case strings.HasPrefix(t, "./"):
			t = strings.TrimPrefix(t, "./")
		case module != "" && t == module:
			t = ""
		case module != "" && strings.HasPrefix(t, module+"/"):
			t = strings.TrimPrefix(t, module+"/")
		default:
			continue
		}
		specs = append(specs, globSpec(t, recursive, "*_test.go"))
	}
	if len(specs) == 0 {
		return []string{"*_test.go"}
	}
	return specs
}

// pytestPathspecs is the pathspecs of the named pytest targets at sha: a node id's
// or argument's `.py` file as written (absent at base is an answer: no test there),
// a directory as every `*.py` under it. No target means the whole repository.
func pytestPathspecs(repo, sha string, scope []string) []string {
	var specs []string
	for _, t := range scope {
		t = strings.TrimSuffix(strings.TrimPrefix(t, "./"), "/")
		if strings.HasSuffix(t, ".py") {
			specs = append(specs, ":(literal)"+t)
			continue
		}
		if typ, err := baseGit(repo, "cat-file", "-t", sha+":"+t); err == nil && strings.TrimSpace(typ) == "tree" {
			specs = append(specs, globSpec(t, true, "*.py"))
		}
	}
	if len(specs) == 0 {
		return []string{"*.py"}
	}
	return specs
}

// globSpec is a `:(glob)` pathspec for files named like base in dir, and in every
// directory under it when recursive; dir "" is the repository root.
func globSpec(dir string, recursive bool, base string) string {
	p := ":(glob)"
	if dir != "" {
		p += dir + "/"
	}
	if recursive {
		p += "**/"
	}
	return p + base
}

// oneLineCap is v with its length capped at n bytes, for an excerpt.
func oneLineCap(v string, n int) string {
	if len(v) > n {
		return v[:n] + "..."
	}
	return v
}

// contractSha is the `sha=<hex>` on the contract line, or "".
func contractSha(line string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, "sha="); ok && hexRE.MatchString(v) {
			return v
		}
	}
	return ""
}

var hexRE = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

func short12(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func splitPathsValue(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitLegs(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sortedLegs(l FleetLegs) []string {
	out := make([]string, 0, len(l))
	for k := range l {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// baseGit runs one git command in repo and returns its trimmed stdout.
func baseGit(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%v: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// pathsMissingAt lists the tree at sha once, and returns every entry that names
// nothing in it. An entry matches a file, or a directory (a prefix of a file), or,
// as a glob, any file hygiene.MatchGlob matches -- the matcher the gate uses. A
// NEW `_test` file is the one entry allowed to be absent: a card that writes the
// red test first creates it.
func pathsMissingAt(repo, sha string, entries []string) ([]string, error) {
	list, err := baseGit(repo, "ls-tree", "-r", "--name-only", "-z", sha)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(list, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	var miss []string
	for _, e := range entries {
		e = strings.TrimPrefix(strings.TrimSuffix(e, "/"), "./")
		if newTestFile(e) {
			continue
		}
		found := false
		for _, f := range files {
			if f == e || strings.HasPrefix(f, e+"/") || hygiene.MatchGlob(e, f) {
				found = true
				break
			}
		}
		if !found {
			miss = append(miss, e)
		}
	}
	return miss, nil
}

// newTestFile is a literal path whose base name is a test file: `x_test.go`,
// `x_test.py`, `test_x.py`. A glob is never one.
func newTestFile(p string) bool {
	if strings.ContainsAny(p, "*?[") {
		return false
	}
	b := path.Base(p)
	return strings.Contains(b, "_test.") || strings.HasPrefix(b, "test_")
}

// stepHeadRE is a STEP line: `STEP <n>` at column 0, or as a markdown heading.
var stepHeadRE = regexp.MustCompile(`^(#{1,6}[ \t]+)?STEP[ \t]+[0-9]+`)

// markdownHeadRE is any markdown heading; one that is not a STEP ends a step.
var markdownHeadRE = regexp.MustCompile(`^#{1,6}[ \t]`)

// gitPushRE is `git push`, with any `-c k=v` or `-C dir` options between.
var gitPushRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./-])git(?:[ \t]+-[cC][ \t]+\S+)*[ \t]+push\b`)

// ghRE is `gh <sub>` in command position: at the start of a line (after an
// optional `$ ` prompt), after `&&`, `||`, `;`, `|`, `(` or `$(`, or opening a
// backtick span. `no \`gh\“ is not a command, and neither is `ghost`.
var ghRE = regexp.MustCompile("(?:^[ \t]*(?:\\$[ \t]+)?|&&[ \t]*|\\|\\|[ \t]*|;[ \t]*|\\|[ \t]*|\\([ \t]*|`)gh[ \t]+[a-z]")

type stepLine struct {
	line int
	text string
}

// pushStepLines is every line inside a STEP that runs `git push` or `gh`. A step
// runs from its STEP line to the next STEP line, the next markdown heading, or a
// line that opens the `RULES` paragraph -- which is where a card says what it may
// NOT run, in words the check must not read as a command.
func pushStepLines(raw []byte) []stepLine {
	var out []stepLine
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n, in := 0, false
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case stepHeadRE.MatchString(line):
			in = true
		case markdownHeadRE.MatchString(line), strings.HasPrefix(line, "RULES"):
			in = false
		}
		if in && (gitPushRE.MatchString(line) || ghRE.MatchString(line)) {
			out = append(out, stepLine{line: n, text: line})
		}
	}
	return out
}

var (
	deadlineSecsRE = regexp.MustCompile(`^([0-9]+)[ \t]*(?:s|sec|secs|seconds)?\.?$`)
	deadlineMinRE  = regexp.MustCompile(`(?i)^(?:finish within[ \t]+)?([0-9]+)[ \t]*(?:m|min|mins|minutes?)\.?$`)
)

// deadlineSeconds reads `2700`, `2700s`, `45m` or `finish within 45 minutes`.
func deadlineSeconds(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if m := deadlineSecsRE.FindStringSubmatch(v); m != nil {
		n, err := strconv.Atoi(m[1])
		return n, err == nil
	}
	if m := deadlineMinRE.FindStringSubmatch(v); m != nil {
		n, err := strconv.Atoi(m[1])
		return n * 60, err == nil
	}
	return 0, false
}

// ReadFleetLegs reads the fleet's leg table: one leg per line, or a TSV whose first
// column is the leg. Blank lines, `#` comments and a header row whose first cell
// is `leg` are skipped. Legs are compared lower case.
func ReadFleetLegs(p string) (FleetLegs, error) {
	rows, err := tableRows(p)
	if err != nil {
		return nil, fmt.Errorf("fleet legs %s: %v", p, err)
	}
	out := FleetLegs{}
	for i, row := range rows {
		leg := strings.ToLower(row[0])
		if i == 0 && (leg == "leg" || leg == "legs") {
			continue
		}
		out[leg] = true
	}
	return out, nil
}

// ReadKindP95 reads `<kind> <seconds>` rows, tab or space separated; `1526s` is
// allowed. Blank lines and `#` comments are skipped, and so is a first row whose
// seconds are not a number (a header). Any later such row is an error: a table
// that silently dropped a kind would turn a measured bound into a MISSING one.
func ReadKindP95(p string) (KindP95, error) {
	rows, err := tableRows(p)
	if err != nil {
		return nil, fmt.Errorf("p95 %s: %v", p, err)
	}
	out := KindP95{}
	for i, row := range rows {
		if len(row) < 2 {
			return nil, fmt.Errorf("p95 %s: row %q wants `<kind> <seconds>`", p, strings.Join(row, " "))
		}
		secs, err := strconv.Atoi(strings.TrimSuffix(row[1], "s"))
		if err != nil || secs < 0 {
			if i == 0 {
				continue
			}
			return nil, fmt.Errorf("p95 %s: kind %s has seconds %q, not a number", p, row[0], row[1])
		}
		out[row[0]] = secs
	}
	return out, nil
}

// tableRows is the non-blank, non-comment lines of a file, split on tabs or spaces.
func tableRows(p string) ([][]string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rows = append(rows, strings.Fields(line))
	}
	return rows, nil
}

// baseSHAOf is the card's BASE-SHA: line (one case, nova-tools#4352 A), or
// its retired base-sha: spelling, which a card cut before that still carries.
func baseSHAOf(h map[string]headerField) (string, int) {
	bs := h["BASE-SHA"]
	if !bs.found {
		bs = h["base-sha"]
	}
	return bs.value, bs.line
}
