package swarm

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
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
// THE PARSER IS CITED; THE VALIDATOR IS CALLED. `internal/pulse` still does not carry
// `cardheader.go` on `dev` -- T03/#1721 is open -- so the block's SHAPE above is the
// parser's rules written out, cited line by line. But `hygiene.ValidatePaths` (T02) HAS
// landed, and `validGlobs` below is now a call to it rather than a second copy of the
// PATHS: rule. The copy it replaces had drifted in both directions inside a day (#1853,
// Emma's item-4 dogfood), which is the whole argument for calling a validator instead of
// restating one. When `cardheader.go` lands, the shape rules above should go the same
// way, with the class test that the two agree in the lane that owns `internal/pulse`.
//
// WHAT IS STILL OWED, AND WHY IT IS NOT HERE. Two of Emma's findings ask the lint to
// check `KIND:` against the kinds table and to refuse `TEST: none` on a gated kind. The
// kinds table is `internal/pulse/kinds.go`, which SPEC-TOOLWORK.md §5 rule 3 makes the
// one source of truth and which is not on `dev` either. Writing a second table here to
// close them would be the same mistake `validGlobs` just undid, so they wait for T06a.

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
	headerKeyRE  = regexp.MustCompile(`^([A-Z][A-Z-]*):\s*(.*)$`)
	goTestNameRE = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
)

// headerField is one `KEY: value` line of the block, with the line it sits on. `again`
// is the line of a SECOND line with the same key, 0 when there is none.
type headerField struct {
	line  int
	value string
	found bool
	again int
}

// cardHeaderBlock reads the typed header the way the gate's parser reads it, stops where
// it stops (cardheader.go:63-76), and returns what it had to ignore.
//
// THE BLOCK'S END STAYS THE GATE'S; WHAT IT SWALLOWED DOES NOT (#1854, Emma's item-4
// dogfood). The block ending at the first line that is not `KEY: value` is the parser's
// own rule and moving it here would be worse than the defect: a lint that read a header
// the gate will not read passes a card that dies at `accept`. What was wrong is that a
// card with one sentence above its `KIND:` line got NO typed checks at all and was
// called clean. So the keys BELOW the block are collected and handed back, to be named
// as findings where they sit -- the gate will never read them, and now neither does the
// card writer have to find that out at the gate.
//
// A SECOND LINE WITH THE SAME KEY IS RECORDED, NOT DROPPED. Silently taking the first of
// two `KIND:` lines is the one answer a writer cannot act on.
func cardHeaderBlock(raw []byte) (block map[string]headerField, stranded map[string]int) {
	block, stranded = map[string]headerField{}, map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n, ended := 0, false
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if n == 1 {
			continue // line 1 is the contract line
		}
		m := headerKeyRE.FindStringSubmatch(line)
		if ended {
			// Past the block, only the five typed keys matter: any other `KEY:` line
			// is a table row or a heading and is nobody's business here.
			if m != nil && cardTypedKeys[m[1]] {
				if _, seen := stranded[m[1]]; !seen {
					stranded[m[1]] = n
				}
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m == nil {
			ended = true // the prose begins here, exactly as the gate has it
			continue
		}
		key := m[1]
		if f, seen := block[key]; seen {
			if f.again == 0 {
				f.again = n
				block[key] = f
			}
			continue
		}
		block[key] = headerField{line: n, value: strings.TrimSpace(m[2]), found: true}
	}
	return block, stranded
}

// cardTypedKeys is the five lines SPEC-TOOLWORK.md §5 rule 1 names, as a set.
var cardTypedKeys = map[string]bool{"KIND": true, "PATHS": true, "TEST": true, "LEGS": true, "SOURCE": true}

// cardKeyCheck is the token that answers for each typed key. LEGS: and SOURCE: have no
// token of their own, so a stranded or repeated one answers under `kind-declared`, which
// is the token for "the typed header is not the header the gate will read".
var cardKeyCheck = map[string]string{
	"KIND":   "kind-declared",
	"PATHS":  "paths-declared",
	"TEST":   "test-named",
	"LEGS":   "kind-declared",
	"SOURCE": "kind-declared",
}

// validGlobs is the PATHS: rule, and it is `hygiene.ValidatePaths` ITSELF, not a
// restatement of it (#1853, Emma's item-4 dogfood).
//
// This function used to write the rule out a second time, because T02's validator was
// not on `dev` when the checks were first written, and the comment above said in so many
// words that it should become a call the day T02 landed. T02 landed, this did not, and
// the copy drifted in BOTH directions within a day: it let a Windows drive letter
// (`C:/Windows/system32/evil.go`) through as repo-relative, it had no cap at all where
// the rule's cap is eight (SPEC-TOOLWORK.md:579-580), and it refused `*.go` and
// `**/*.go`, which the validator clears. A card writer got a different answer from the
// lint on the bench and from the gate at `accept`, which is the one thing these checks
// exist to prevent.
func validGlobs(globs []string) (string, bool) {
	if err := hygiene.ValidatePaths(globs); err != nil {
		return err.Error(), false
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
	h, stranded := cardHeaderBlock(raw)
	typed := required
	for k := range cardTypedKeys {
		if h[k].found {
			typed = true
		}
	}
	// A CARD WITH A TYPED LINE THE GATE CANNOT REACH IS A TYPED CARD (#1854). Without
	// this it was neither: no header line inside the block, so no typed checks ran, and
	// the card was called clean all the way to `accept`.
	if len(stranded) > 0 {
		typed = true
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
	// The stranded lines first, in line order, because they are why the rest of this
	// card's header reads the way it does.
	for _, k := range sortedKeys(stranded) {
		add(cardKeyCheck[k], stranded[k], fmt.Sprintf("%s: on line %d is below the header block and the gate will never read it: the typed header is the unbroken run of `KEY: value` lines directly under the contract line", k, stranded[k]))
	}
	// A repeated key next: a card with two of one line has no one value for it.
	for _, k := range sortedHeaderKeys(h) {
		if f := h[k]; f.again > 0 {
			add(cardKeyCheck[k], f.again, fmt.Sprintf("%s: is declared twice, on lines %d and %d; a card with two of this line has no one value for it", k, f.line, f.again))
		}
	}

	// 1. KIND: is declared, and not declared empty.
	kind := h["KIND"]
	switch {
	case !kind.found && stranded["KIND"] > 0:
		// Named already, where it sits: saying "no KIND: line" of a card whose KIND:
		// line is three lines down is the confusing half of a true finding.
	case !kind.found:
		add("kind-declared", 1, "no KIND: line under the contract line")
	case kind.value == "":
		add("kind-declared", kind.line, "KIND: with no kind after it")
	}

	// 2. PATHS: is declared, and every glob is one the gate could use.
	paths := h["PATHS"]
	switch {
	case !paths.found && stranded["PATHS"] > 0:
	case !paths.found:
		add("paths-declared", 1, "no PATHS: line under the contract line")
	case paths.value == "":
		add("paths-declared", paths.line, "PATHS: with no globs after it; a card that changes nothing says `PATHS: none`")
	case paths.value != "none":
		// AN EMPTY ENTRY IS NOT A SKIPPABLE ONE. `PATHS: , , ` used to have each empty
		// entry `continue`d past and the line called fine, which is the worst of the
		// three answers a reader could get: the line declares no glob and it is not
		// `none` (#1853, Emma's item-4 dogfood).
		var globs []string
		empty := false
		for _, g := range strings.Split(paths.value, ",") {
			g = strings.TrimSpace(g)
			if g == "" {
				empty = true
				continue
			}
			globs = append(globs, g)
		}
		switch {
		case len(globs) == 0:
			add("paths-declared", paths.line, fmt.Sprintf("PATHS: %q names no glob and is not `none`", paths.value))
		case empty:
			add("paths-declared", paths.line, fmt.Sprintf("PATHS: %q has an empty entry between its commas", paths.value))
		}
		if len(globs) > 0 {
			if why, ok := validGlobs(globs); !ok {
				add("paths-declared", paths.line, why)
			}
		}
	}

	// 3. TEST: is declared, and is `<package> <TestName>` or `none`.
	test := h["TEST"]
	switch {
	case !test.found && stranded["TEST"] > 0:
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
		// The package runs the same path rule as PATHS:, from the same validator, so a
		// `TEST: C:/x TestA` cannot walk through a door PATHS: closed.
		if why, ok := validGlobs([]string{pkg}); !ok {
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

// sortedKeys is the keys of a line-number map, in one byte-stable order so two runs of
// the lint over the same card print the same lines in the same order.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedHeaderKeys is the same, for the block itself.
func sortedHeaderKeys(m map[string]headerField) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if cardTypedKeys[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
