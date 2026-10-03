package swarm

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// The typed card header, checked before any spend so a card dies at the lint
// rather than at the gate.
//
// `cut` writes five lines under the contract line and inside its hash:
//
//	KIND: <kind>
//	PATHS: <glob>[, <glob>...]
//	TEST: [-tags <tags>] <package> <TestName>   (or `TEST: none <why>` where the kind allows it)
//	LEGS: <leg>[,<leg>...]
//	SOURCE: <owner>/<repo>#<n> | <file:line at the pinned head>
//
// `lint --card` checks the tokens `kind-declared`, `paths-declared`,
// `paused`, and `test-named`. That is what this file is.
//
// Three readers, one grammar. `cut` renders the lines, `internal/pulse/cardheader.go`
// reads them at the gate, and the lint checks them on the bench before a token is spent.
// A lint that accepted a line the gate refuses would send a card out to die at `accept`;
// a lint that refused a line the gate reads would stop a card that was fine. So the
// rules below are the parser's rules, restated with the parser cited beside each one:
//
//   - the block starts at line 2 and ends at the first non-empty line that is not
//     `KEY: value`, blank lines skipped, unknown keys read past (cardheader.go:63-76);
//   - the key is one word and a colon at column 0 and nowhere else:
//     `^[A-Za-z][A-Za-z0-9-]*:`.
//   - `PATHS: none` declares no paths; otherwise the value is comma-separated
//     (cardheader.go:82-88);
//   - TEST is read by cardhdr.ParseTest, the one TEST grammar the gate runs:
//     `none <why>` is a declaration where the kind allows it (a bare `none` is
//     refused: the reader must see why); otherwise `[-tags <tags>] <package>
//     <TestName>`, the name matching `^Test[A-Za-z0-9_]*$`;
//   - KIND, PATHS and TEST are the three a gated card must carry.
//
// The validator is called, not restated. `validGlobs` below calls
// `hygiene.ValidatePaths` rather than restating the PATHS: rule.
//
// KIND is the name set, not a second table. `hygiene.KindDeclared` reads
// internal/hygiene/kinds.txt, which is the names `cut` and `nova-check hygiene`
// already refuse. The gate's TABLE is a second structure that `TEST: none` on
// a gated kind needs.

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
// a guess per drift, so `nova-swarm lint --rules` prints these beside them.
var CardHeaderRemedies = map[string]string{
	"kind-declared":  "the card carries `KIND: <kind>` as the first typed line under the contract line, and the kind is one the pool's kinds list names; the cutter writes it from the pool row and a model never does",
	"paths-declared": "the card carries `PATHS: <glob>[, <glob>...]`, repository-relative, every glob holding at least one literal segment and none of them climbing with `..`; a card that changes nothing says `PATHS: none`",
	"test-named":     "the card carries `TEST: [-tags <tags>] <package> <TestName>` -- the package repository-relative and the name a Go test name -- or `TEST: none <why>` where the kind declares no gate",
	"paused":         "the coordinator paused this kind, so `cut` cuts no card of it and a card launched before the pause is `ACCEPT ABSTAIN reason=paused` at harvest; the remedy is not a rerun but `nova-pulse trust --set trial --queue <dir> --kind <kind> --who <name> --reason <text>`",
}

// CardHeaderChecks is every token this file draws, in one byte-stable order.
func CardHeaderChecks() []string {
	return slices.Sorted(maps.Keys(CardHeaderRemedies))
}

// TrustState is the coordinator's per-kind state, keyed by kind: `trial`, `trusted` or
// `paused` (one of the trust states the coordinator tracks).
type TrustState map[string]string

// headerKeyRE is what makes a line a `KEY: value` line: one word, starting with a
// letter, then letters, digits and hyphens, then a colon, at column 0.
//
// The block is every key line, not every upper-case key line. The two lines every
// card must carry are lower case: the launcher scripts read `base-repo:` and
// `base-sha:` out of the card's first 40 lines with a case-sensitive `sed`.
// A key pattern that reads upper case only ends the header block at those two
// lower-case lines, and every subsequent typed key sits outside the block the
// gate reads; `PATHS:`, `FILES:`, `TEST:`, `RUN:`, `SYMBOL:`, `RED-WHEN:`,
// `DONE-WHEN:`, `NO-SUBAGENTS:` and `SOURCE:` -- every key under them -- sit
// outside the header the gate reads.
//
// Widened, not allowlisted. An allowlist of those two names fixes those two cards
// and breaks on the next launcher key; the rule is "the header is the leading
// run of `word:` lines". The cost is a prose line that happens to be one word
// and a colon at column 0 (`Note: ...`) no longer ending the block.
//
// The key names stay upper case. Widening what continues the block is not the same as
// widening what a typed key is: `cut` writes `KIND:`, `PATHS:`, `TEST:`, `LEGS:` and
// `SOURCE:` in upper case, so `paths:` is not `PATHS:` here. cardTypedKeys is the
// exact names; if a spec line rules case-insensitive keys, this is the one place
// to say so.
var (
	headerKeyRE  = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*):\s*(.*)$`)
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
// it stops, and returns what it had to ignore.
//
// The block's end stays the gate's; what it swallowed does not. The block ending at
// the first line that is not `KEY: value` is the parser's own rule. A lint that
// reads a header the gate will not read passes a card that dies at `accept`. So
// the keys below the block are collected and handed back to be named as findings
// where they sit. The gate will never read them, so the card writer doesn't
// discover it at the gate.
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

// ungatedKinds is the set of kinds that may carry TEST: none. It matches the third
// column of internal/hygiene/kinds.txt (SPEC-TOOLWORK.md §5 rule 2: read, probe, text,
// tone, report) until internal/pulse/kinds.go lands with the gate table.
var ungatedKinds = map[string]bool{"read": true, "probe": true, "text": true, "tone": true, "report": true}

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
		line = max(line, 1)
		out = append(out, CardHeaderFinding{Check: check, Line: line, Excerpt: excerpt})
	}
	// The stranded lines first, in line order, because they are why the rest of this
	// card's header reads the way it does.
	for _, k := range slices.Sorted(maps.Keys(stranded)) {
		add(cardKeyCheck[k], stranded[k], fmt.Sprintf("%s: on line %d is below the header block and the gate will never read it: the typed header is the unbroken run of `KEY: value` lines directly under the contract line", k, stranded[k]))
	}
	// A repeated key next: a card with two of one line has no one value for it.
	for _, k := range sortedHeaderKeys(h) {
		if f := h[k]; f.again > 0 {
			add(cardKeyCheck[k], f.again, fmt.Sprintf("%s: is declared twice, on lines %d and %d; a card with two of this line has no one value for it", k, f.line, f.again))
		}
	}

	// 1. KIND: is declared, not empty, and is a name the toolchain holds.
	kind := h["KIND"]
	switch {
	case !kind.found && stranded["KIND"] > 0:
		// Named already, where it sits: saying "no KIND: line" of a card whose KIND:
		// line is three lines down is the confusing half of a true finding.
	case !kind.found:
		add("kind-declared", 1, "no KIND: line under the contract line")
	case kind.value == "":
		add("kind-declared", kind.line, "KIND: with no kind after it")
	case !hygiene.KindDeclared(kind.value):
		// AN UNKNOWN KIND IS NOT A KIND (#1853). The line used to need only a
		// value, so `KIND: completely-unknown-kind` linted clean and died at
		// accept. The names are hygiene.Kinds(), the same set `nova-check hygiene
		// --kind` prints when it refuses.
		add("kind-declared", kind.line, fmt.Sprintf("KIND: %q is not a kind this toolchain declares; one of: %s", kind.value, strings.Join(hygiene.Kinds(), ", ")))
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

	// 3. TEST: is declared, and is what cardhdr.ParseTest reads: `[-tags <tags>]
	// <package> <TestName>` or `none <why>`.
	test := h["TEST"]
	tl, testWhy := cardhdr.ParseTest(test.value)
	switch {
	case !test.found && stranded["TEST"] > 0:
	case !test.found:
		add("test-named", 1, "no TEST: line under the contract line")
	case test.value == "":
		add("test-named", test.line, "TEST: with no package and no test name after it")
	case testWhy != "":
		add("test-named", test.line, testWhy)
	case tl.None:
		// TEST: none is only a declaration for ungated kinds; gated kinds strictly
		// require a reproducing test (SPEC-TOOLWORK.md §5 rule 1, rule 2).
		if kind.value != "" && !ungatedKinds[kind.value] {
			add("test-named", test.line, fmt.Sprintf("TEST: none is not allowed for kind %q; gated kinds require `TEST: <package> <TestName>`", kind.value))
		}
	default:
		pkg := strings.Trim(tl.Package, "/")
		if pkg == "" {
			pkg = "."
		}
		// The package runs the same path rule as PATHS:, from the same validator, so a
		// `TEST: C:/x TestA` cannot walk through a door PATHS: closed.
		if why, ok := validGlobs([]string{pkg}); !ok {
			add("test-named", test.line, "TEST: package: "+why)
		}
		if !goTestNameRE.MatchString(tl.Name) {
			add("test-named", test.line, fmt.Sprintf("TEST: %q is not a Go test name", tl.Name))
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

// sortedHeaderKeys is the typed keys of the block, in one byte-stable order so two runs of
// the lint over the same card print the same lines in the same order.
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
