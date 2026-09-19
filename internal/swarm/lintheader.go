package swarm

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// THE TYPED CARD HEADER, CHECKED BEFORE ANY SPEND (SPEC-TOOLWORK.md §5 rule 1, #1651).
//
// `cut` writes five lines under the contract line and inside its hash:
//
//	KIND: <kind>
//	PATHS: <glob>[, <glob>...]
//	TEST: <package> <TestName>          (or `TEST: none` where the kind allows it)
//	LEGS: <leg>[,<leg>...]
//	SOURCE: <owner>/<repo>#<n> | <file:line at the pinned head>
//
// Rule 1 ends: *"`lint --card` gains the tokens `kind-declared`, `paths-declared`,
// `paused` (the coordinator paused this kind; the remedy is the `trust --set trial`
// command) and `test-named`"*. That is what this file is.
//
// THREE READERS, ONE GRAMMAR. `cut` renders the lines, `internal/pulse/cardheader.go`
// reads them at the gate (T03, PR #1721 at f927bccc), and the lint checks them on the
// bench before a token is spent. A lint that accepted a line the gate refuses would
// send a card out to die at `accept`; a lint that refused a line the gate reads would
// stop a card that was fine. So the rules below are the parser's rules, restated with
// the parser cited beside each one:
//
//   - the block starts at line 2 and ends at the first non-empty line that is not
//     `KEY: value`, blank lines skipped, unknown keys read past (cardheader.go:63-76);
//   - the key is `^[A-Z][A-Z-]*:` at column 0 and nowhere else (cardheader.go:45);
//   - `PATHS: none` declares no paths; otherwise the value is comma-separated
//     (cardheader.go:82-88);
//   - `TEST: none` is a declaration; otherwise the value is exactly two fields, the
//     second matching `^Test[A-Za-z0-9_]*$` (cardheader.go:89-108);
//   - KIND, PATHS and TEST are the three a gated card must carry (cardheader.go:138-150).
//
// THE PARSER IS CITED, NOT VENDORED. This package does not import it: `internal/pulse`
// does not carry `cardheader.go` on `dev` yet -- #1721 is open -- and neither does
// `internal/hygiene` carry `ValidatePaths` (T02, also open), which is the one validator
// the parser calls for PATHS: and for the TEST: package. Until both are on `dev` the
// path rules here are the spec's, written out; when they land, `validGlob` below should
// become a call to `hygiene.ValidatePaths` and this comment should go. A class test that
// the two agree belongs with them, in the lane that owns `internal/pulse`.

// CardHeaderFinding is one typed-header defect: the check's token, the 1-based line it
// sits on and the line's own text. It is the shape `cmd/nova-swarm/lint.go` prints on a
// LINT DRIFT line, remedy and all.
type CardHeaderFinding struct {
	Check   string
	Line    int
	Excerpt string
}

// CardHeaderRemedies is what each of the four tokens wants, in one line, in the same
// table shape the twelve older tokens use: a check without a remedy costs a card writer
// a guess per drift (#1464), and `nova-swarm lint --rules` prints these beside them.
var CardHeaderRemedies = map[string]string{
	"kind-declared":  "the card carries `KIND: <kind>` as the first typed line under the contract line, and the kind is one `nova-pulse accept --kinds` names; `cut` writes it from the pool row and a model never does (SPEC-TOOLWORK.md §5 rules 1, 3)",
	"paths-declared": "the card carries `PATHS: <glob>[, <glob>...]`, repository-relative, every glob holding at least one literal segment and none of them climbing with `..`; a card that changes nothing says `PATHS: none` (SPEC-TOOLWORK.md §5 rules 1, 2)",
	"test-named":     "the card carries `TEST: <package> <TestName>` -- two fields, the package repository-relative and the name a Go test name -- or `TEST: none` where the kind declares no gate (SPEC-TOOLWORK.md §5 rule 1)",
	"paused":         "the coordinator paused this kind, so `cut` cuts no card of it and a card launched before the pause is `ACCEPT ABSTAIN reason=paused` at harvest; the remedy is not a rerun but `nova-pulse trust --set trial --queue <dir> --kind <kind> --who <name> --reason <text>` (SPEC-TOOLWORK.md §5 rule 1, eligibility rule 3, §1's abstain list)",
}

// CardHeaderChecks is every token this file draws, in one byte-stable order.
func CardHeaderChecks() []string {
	out := make([]string, 0, len(CardHeaderRemedies))
	for name := range CardHeaderRemedies {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TrustState is the coordinator's per-kind state, keyed by kind: `trial`, `trusted` or
// `paused` (SPEC-TOOLWORK.md eligibility rule 1's TRUST listing).
type TrustState map[string]string

var (
	headerKeyRE   = regexp.MustCompile(`^([A-Z][A-Z-]*):\s*(.*)$`)
	goTestNameRE  = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
	globMetaChars = "*?["
)

// headerField is one `KEY: value` line of the block, with the line it sits on.
type headerField struct {
	line  int
	value string
	found bool
}

// cardHeaderBlock reads the typed header the way the gate's parser reads it, and stops
// where it stops (cardheader.go:63-76).
func cardHeaderBlock(raw []byte) map[string]headerField {
	out := map[string]headerField{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if n == 1 {
			continue // line 1 is the contract line
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := headerKeyRE.FindStringSubmatch(line)
		if m == nil {
			break // the prose begins here
		}
		key := m[1]
		if _, seen := out[key]; !seen {
			out[key] = headerField{line: n, value: strings.TrimSpace(m[2]), found: true}
		}
	}
	return out
}

// validGlob says whether one PATHS: glob is one the gate could use: repository-relative,
// no `..` segment, and at least one literal segment, so the glob names somewhere rather
// than everywhere. A bare `**` has no literal segment and is refused by that rule.
func validGlob(g string) (string, bool) {
	if g == "" {
		return "an empty glob names nothing", false
	}
	if strings.HasPrefix(g, "/") || strings.HasPrefix(g, `\`) {
		return fmt.Sprintf("%q is not repository-relative", g), false
	}
	segs := strings.Split(strings.ReplaceAll(g, `\`, "/"), "/")
	literal := false
	for _, s := range segs {
		if s == ".." {
			return fmt.Sprintf("%q climbs above the repository with ..", g), false
		}
		if s != "" && s != "." && !strings.ContainsAny(s, globMetaChars) {
			literal = true
		}
	}
	if !literal {
		return fmt.Sprintf("%q has no literal segment: it names every file, not a place", g), false
	}
	return "", true
}

// LintCardHeader returns the typed-header findings for one card's bytes.
//
// trust may be nil: with no trust state there is no paused kind, and the lint says
// nothing rather than guessing. required makes the three missing-line tokens apply to a
// card that carries no typed header at all; without it a card with no header line is
// left to the twelve older rules, which is what every card written before §5 is.
func LintCardHeader(raw []byte, trust TrustState, required bool) []CardHeaderFinding {
	h := cardHeaderBlock(raw)
	typed := required
	for _, k := range []string{"KIND", "PATHS", "TEST", "LEGS", "SOURCE"} {
		if h[k].found {
			typed = true
		}
	}
	if !typed {
		return nil
	}
	var out []CardHeaderFinding
	add := func(check string, line int, excerpt string) {
		if line < 1 {
			line = 1
		}
		out = append(out, CardHeaderFinding{Check: check, Line: line, Excerpt: excerpt})
	}

	// 1. KIND: is declared, and not declared empty.
	kind := h["KIND"]
	switch {
	case !kind.found:
		add("kind-declared", 1, "no KIND: line under the contract line")
	case kind.value == "":
		add("kind-declared", kind.line, "KIND: with no kind after it")
	}

	// 2. PATHS: is declared, and every glob is one the gate could use.
	paths := h["PATHS"]
	switch {
	case !paths.found:
		add("paths-declared", 1, "no PATHS: line under the contract line")
	case paths.value == "":
		add("paths-declared", paths.line, "PATHS: with no globs after it; a card that changes nothing says `PATHS: none`")
	case paths.value != "none":
		for _, g := range strings.Split(paths.value, ",") {
			g = strings.TrimSpace(g)
			if g == "" {
				continue
			}
			if why, ok := validGlob(g); !ok {
				add("paths-declared", paths.line, why)
			}
		}
	}

	// 3. TEST: is declared, and is `<package> <TestName>` or `none`.
	test := h["TEST"]
	switch {
	case !test.found:
		add("test-named", 1, "no TEST: line under the contract line")
	case test.value == "none":
		// a declaration: the kind declares no gate (cardheader.go:91-94)
	case test.value == "":
		add("test-named", test.line, "TEST: with no package and no test name after it")
	default:
		fields := strings.Fields(test.value)
		if len(fields) != 2 {
			add("test-named", test.line, fmt.Sprintf("TEST: wants `<package> <TestName>` or `none`, got %q", test.value))
			break
		}
		pkg := strings.Trim(fields[0], "/")
		if pkg == "" {
			pkg = "."
		}
		if why, ok := validGlob(pkg); !ok {
			add("test-named", test.line, "TEST: package: "+why)
		}
		if !goTestNameRE.MatchString(fields[1]) {
			add("test-named", test.line, fmt.Sprintf("TEST: %q is not a Go test name", fields[1]))
		}
	}

	// 4. the kind is not one the coordinator paused.
	if kind.value != "" && trust != nil {
		if trust[kind.value] == "paused" {
			add("paused", kind.line, fmt.Sprintf("kind=%s is paused: the coordinator stopped cutting it", kind.value))
		}
	}
	return out
}

// ReadTrustFixture reads the coordinator's per-kind state from a file in the shape
// `nova-pulse trust` prints (SPEC-TOOLWORK.md eligibility rule 1):
//
//	TRUST kind=<kind> area=<area> state=<trial|trusted|paused> cards=<n>/<N> ...
//	TRUST OK kinds=<n> trial=<n> trusted=<n> paused=<n>
//
// THE VERB DOES NOT EXIST YET. `nova-pulse trust` is the other half of T06a and belongs
// to the lane that owns `internal/pulse`; until it lands, the bench hands `lint --card`
// a fixture in exactly this shape with `--trust <file>`, and the day the verb ships its
// own stdout is the fixture. Lines that are not TRUST rows, and the TRUST OK summary,
// are read past, so a whole listing can be saved to a file and handed over unedited.
func ReadTrustFixture(path string) (TrustState, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("trust fixture %s: %v", path, err)
	}
	defer f.Close()
	out := TrustState{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		rest, ok := strings.CutPrefix(line, "TRUST ")
		if !ok || strings.HasPrefix(rest, "OK ") {
			continue
		}
		var kind, state string
		for _, fld := range strings.Fields(rest) {
			k, v, ok := strings.Cut(fld, "=")
			if !ok {
				continue
			}
			switch k {
			case "kind":
				kind = v
			case "state":
				state = v
			}
		}
		if kind != "" && state != "" {
			out[kind] = state
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("trust fixture %s: %v", path, err)
	}
	return out, nil
}
