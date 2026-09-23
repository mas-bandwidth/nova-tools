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

// THE BASE CHECKS: FOUR RULES A CODING CARD IS HELD TO BEFORE IT IS DEALT (#2636).
//
// Each rule is one class of the 2026-09-22 sprint's failed cards (rowan-new
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
//
// NO EVIDENCE IS NOT NEGATIVE EVIDENCE. A check whose evidence was not handed over
// (no repository, a base-sha the repository does not hold, no leg table, no p95
// row for the kind) does not pass: it draws its finding with MISSING in the
// excerpt, naming what to hand over. `--base-check` is the ask for these checks,
// and a check that could not run is not a check that passed.

// BaseCheck is the evidence the four checks read. A nil table or an empty Repo
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
	"paths-at-base": "every PATHS entry names a file, directory or glob that exists at the card's base-sha (or is a new `_test` file); cut the card from the tree at that sha, not from the issue's words, and hand the lint a repository holding the sha with `--repo <dir>` (failed-cards-2026-09-22 class 9, nx-f19)",
	"no-push-steps": "a card ends at a local commit: no STEP runs `git push` or `gh`, because the wall holds no credential and the harvest pushes and comments; say `no gh, no push` in RULES, never as a STEP command (failed-cards-2026-09-22 class 8, holdfix and nx-r repair cards)",
	"leg-in-fleet":  "LEG: (or each LEGS: entry) is a leg the fleet's leg table carries -- `sbcl`, not `lisp` -- and the table is handed over with `--legs <file>` (failed-cards-2026-09-22 LEG row, #2728)",
	"deadline-p95":  "DEADLINE: is at or above the measured p95 wall of the card's KIND, in seconds (or `finish within <n> minutes`), and the p95 table is handed over with `--p95 <file>`; a kind with no row needs a `*` row or a measurement first (failed-cards-2026-09-22 class 11)",
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
		base, baseLine := h["base-sha"].value, h["base-sha"].line
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
	return out
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
