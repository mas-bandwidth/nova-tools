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
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// THE TYPED CARD HEADER, CHECKED BEFORE ANY SPEND (SPEC-TOOLWORK.md §5 rule 1, #1651).
//
// `cut` writes five lines under the contract line and inside its hash:
//
//	KIND: <kind>
//	PATHS: <glob>[, <glob>...]
//	TEST: [-tags <tags>] <package> <TestName>   (or `TEST: none <why>` where the kind allows it)
//	LEGS: <leg>[,<leg>...]
//	SOURCE: <owner>/<repo>#<n> | <file:line at the pinned head>
//
// Rule 1 ends: *"`lint --card` gains the tokens `kind-declared`, `paths-declared`,
// `paused` (the coordinator paused this kind; the remedy is the `trust --set trial`
// command) and `test-named`"*. That is what this file is.
//
// The brief's header grammar adds two lines beside those five
// (docs/SPEC-CARD-CONTRACT.md §2, the header grammar):
//
//	START: <files or packages to read first>
//	STOP: <the condition that ends the task>
//
// A brief is a card that names a tier, or one the caller declares typed; a missing
// either line draws the token `start-named` or `stop-named`. `cmd/nova-swarm/lint.go`
// refuses that finding for a tier flash brief and prints it as a note for tier pro, or
// no tier, so a flash child with no stopping condition cannot read past its budget.
//
// THREE READERS, ONE GRAMMAR. `cut` renders the lines, `pulse/cardheader.go`
// reads them at the gate (T03, PR #1721 at f927bccc), and the lint checks them on the
// bench before a token is spent. A lint that accepted a line the gate refuses would
// send a card out to die at `accept`; a lint that refused a line the gate reads would
// stop a card that was fine. So the rules below are the parser's rules, restated with
// the parser cited beside each one:
//
//   - the block starts at line 2 and ends at the first non-empty line that is not
//     `KEY: value`, blank lines skipped, unknown keys read past (cardheader.go:63-76);
//   - the key is one word and a colon at column 0 and nowhere else:
//     `^[A-Za-z][A-Za-z0-9-]*:`, any case (#2605, and see THE BLOCK IS EVERY KEY LINE
//     below for why the case-sensitive `^[A-Z][A-Z-]*:` this used to be was a defect);
//   - `PATHS: none` declares no paths; otherwise the value is comma-separated
//     (cardheader.go:82-88);
//   - TEST is read by cardhdr.ParseTest, the one TEST grammar the copy wrapper's gate
//     runs (nova-tools#4313): `none <why>` is a declaration where the kind allows it (a
//     bare `none` is refused: the reader must see why); otherwise `[-tags <tags>]
//     <package> <TestName>`, the name matching `^Test[A-Za-z0-9_]*$` (cardheader.go:89-108);
//   - KIND, PATHS and TEST are the three a gated card must carry (cardheader.go:138-150).
//
// THE PARSER IS CITED; THE VALIDATOR IS CALLED. The retired pulse package's
// `cardheader.go` at f927bccc never landed on `dev` -- T03/#1721 is open -- so the
// block's SHAPE above is the parser's rules written out, cited line by line. But `hygiene.ValidatePaths` (T02) HAS
// landed, and `validGlobs` below is now a call to it rather than a second copy of the
// PATHS: rule. The copy it replaces had drifted in both directions inside a day (#1853,
// a friend's item-4 dogfood), which is the whole argument for calling a validator instead of
// restating one. When `cardheader.go` lands, the shape rules above should go the same
// way, with the class test that the two agree.
//
// KIND: IS THE NAME SET, NOT A SECOND TABLE (#1853). `hygiene.KindDeclared` reads
// internal/hygiene/kinds.txt, which is the names `cut` and `nova-check hygiene`
// already refuse. The gate table (steps, control, reject tokens) was previously in
// pulse/kinds.go at f927bccc (SPEC-TOOLWORK.md §5 rule 3). The instruction kind is a
// column of the one list (docs/SPEC-ISA.md): the KIND: line already names the work,
// and the column names the instruction. A second name list here would be the same
// mistake `validGlobs` just undid.

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
//
// The two brief-grammar tokens are not in this map: CardHeaderChecks is the four of §5
// rule 1, and gap_test pins that list. Their remedies are StartNamedRemedy and
// StopNamedRemedy below, merged into `cmd/nova-swarm/lint.go`'s cardLintRemedies.
var CardHeaderRemedies = map[string]string{
	"kind-declared":  "the card carries `KIND: <kind>` as the first typed line under the contract line, and the kind is one the pool's kinds list names; the cutter writes it from the pool row and a model never does; a declared kind whose row names no instruction kind is this finding too: name the instruction kind on that row of the one kinds list",
	"paths-declared": "the card carries `PATHS: <glob>[, <glob>...]`, repository-relative, every glob holding at least one literal segment and none of them climbing with `..`; a card that changes nothing says `PATHS: none`",
	"test-named":     "the card carries `TEST: [-tags <tags>] <package> <TestName>` -- the package repository-relative and the name a Go test name -- or `TEST: none <why>` where the kind declares no gate",
	"paused":         "the coordinator paused this kind, so `cut` cuts no card of it and a card launched before the pause is `ACCEPT ABSTAIN reason=paused` at harvest; the remedy is not a rerun but `nova-pulse trust --set trial --queue <dir> --kind <kind> --who <name> --reason <text>`",
}

// StartNamedRemedy and StopNamedRemedy are what the two brief-grammar tokens want, in one
// line, the same contract as CardHeaderRemedies. They are separate from that map because
// CardHeaderChecks is the four tokens of §5 rule 1; `cmd/nova-swarm/lint.go` merges these
// into its cardLintRemedies, so a start-named or stop-named line names its remedy.
const (
	StartNamedRemedy = "the brief carries `START: <files or packages to read first>`, the files or packages the child reads before it spends; it names where the work begins, so the reading is bounded before a token is spent"
	StopNamedRemedy  = "the brief carries `STOP: <the condition that ends the task>`, the condition that ends the task; it bounds a flash child's reading, which otherwise runs past its budget"
)

// CardHeaderChecks is every token this file draws, in one byte-stable order.
func CardHeaderChecks() []string {
	return slices.Sorted(maps.Keys(CardHeaderRemedies))
}

// TrustState is the coordinator's per-kind state, keyed by kind: `trial`, `trusted` or
// `paused` (SPEC-TOOLWORK.md eligibility rule 1's TRUST listing).
type TrustState map[string]string

// headerKeyRE is what makes a line a `KEY: value` line: one word, starting with a
// letter, then letters, digits and hyphens, then a colon, at column 0. ANY CASE (#2605).
//
// THE BLOCK IS EVERY KEY LINE, NOT EVERY UPPER-CASE KEY LINE. This was
// `^([A-Z][A-Z-]*):`, and the two lines every card the darwin launchers stage MUST carry
// are lower case: `~/<person>-working/<person>-tools/bin/launchers/*-native-darwin.sh:30-31`
// read `base-repo:` and `base-sha:` out of the card's first 40 lines with a
// case-sensitive `sed`, and refuse to launch without both. So on every one of the 59
// cards of the 2026-09-22 sprint set those two lines ENDED the header block, and
// `PATHS:`, `FILES:`, `TEST:`, `RUN:`, `SYMBOL:`, `RED-WHEN:`, `DONE-WHEN:`,
// `NO-SUBAGENTS:` and `SOURCE:` -- every key under them -- were outside the header the
// gate reads. `bin/sprint-stage:37` refuses the whole stage on one such card, so the set
// either did not stage or staged with a header the gate could not read.
//
// WIDENED, NOT ALLOWLISTED. An allowlist of those two names would have fixed today's two
// cards and broken on the next launcher key; the rule the card writer can hold in one
// sentence is "the header is the leading run of `word:` lines". The cost is a prose line
// that happens to be one word and a colon at column 0 (`Note: ...`) no longer ending the
// block -- which the negative control in the table test pins, alongside the prose line
// that does.
//
// THE KEY NAMES STAY UPPER CASE. Widening what CONTINUES the block is not the same as
// widening what a typed key IS: docs/SPEC-TOOLWORK.md:700-713 (§5 rule 1) and
// WORKER-CARDS.md:38-51 (now in the nova-work-old repository) write `KIND:`, `PATHS:`, `TEST:`, `LEGS:` and `SOURCE:` in
// upper case and say nothing anywhere about case, so `paths:` is not `PATHS:` here and
// the card that writes it still draws `paths-declared`. cardTypedKeys is the exact
// names; the day a spec line rules case-insensitive keys, this is the one place to say
// so.
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
// it stops (cardheader.go:63-76), and returns what it had to ignore.
//
// THE BLOCK'S END STAYS THE GATE'S; WHAT IT SWALLOWED DOES NOT (#1854, a friend's item-4
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

// CardHeaderValue is the value of one key of a card's typed header block, read
// as the lint and the gate read the block (the unbroken run of `KEY: value`
// lines under the contract line); ok is false when the block has no such line.
// nova-sprint add reads DEPENDS-ON: and PATHS: through it.
func CardHeaderValue(raw []byte, key string) (value string, ok bool) {
	block, _ := cardHeaderBlock(raw)
	f := block[key]
	return f.value, f.found
}

// CardPaths is the typed header's PATHS and NEW globs (docs/SPEC-SPRINT.md,
// "A card is a tree of steps"). Both name files the card may change; a key
// in the body or a step grants no scope. PATHS: none names no files.
func CardPaths(raw []byte) []string {
	block, _ := cardHeaderBlock(raw)
	return typedrec.CardPaths(func(key string) (string, bool) {
		f := block[key]
		return f.value, f.found
	})
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

// StartNamedCheck and StopNamedCheck are the tokens for the two reading lines of the
// brief's header grammar: `START: <files or packages to read first>` and
// `STOP: <the condition that ends the task>` (docs/SPEC-CARD-CONTRACT.md §2). A brief
// missing either draws the finding; `cmd/nova-swarm/lint.go` refuses it for a tier
// flash brief and prints it as a note for tier pro, or no tier. Both carry a remedy in
// StartNamedRemedy and StopNamedRemedy above, so the note names what to write.
const (
	StartNamedCheck = "start-named"
	StopNamedCheck  = "stop-named"
)

// validGlobs is the PATHS: rule, and it is `hygiene.ValidatePaths` ITSELF, not a
// restatement of it.
//
// A restated PATHS rule drifts from the validator: it lets a Windows drive letter
// through as repo-relative, it has no cap where the rule caps the globs, and it refuses
// `*.go` and `**/*.go` that the validator clears. Then the lint on the bench and the
// gate at `accept` give a card writer different answers, which is the one thing these
// checks exist to prevent.
func validGlobs(globs []string) (string, bool) {
	if err := hygiene.ValidatePaths(globs); err != nil {
		return err.Error(), false
	}
	return "", true
}

// instructionFinding is the refusal of a declared KIND whose row of the one
// kinds list names no instruction kind (docs/SPEC-ISA.md: the instruction kind
// is a column of internal/hygiene/kinds.txt). It is nil when the row names one.
func instructionFinding(work, instruction string, line int) *CardHeaderFinding {
	if instruction != "" {
		return nil
	}
	return &CardHeaderFinding{
		Check:   "kind-declared",
		Line:    line,
		Excerpt: fmt.Sprintf("KIND: %q is declared but its row of the one kinds list names no instruction kind; add the instruction kind to that row of internal/hygiene/kinds.txt", work),
	}
}

// ResolveInstructionKind is the instruction kind the card lint resolves for one
// card's bytes: its `KIND:` line read through the instruction-kind column of the
// one kinds list, internal/hygiene/kinds.txt (docs/SPEC-ISA.md). It returns the
// work kind too, so a caller can print both. ok is false when the card carries no
// KIND: line, the kind is not declared, or its row names no instruction kind;
// LintCardHeader refuses each of those as kind-declared.
func ResolveInstructionKind(raw []byte) (work, instruction string, ok bool) {
	h, _ := cardHeaderBlock(raw)
	kind := h["KIND"]
	if kind.value == "" {
		return "", "", false
	}
	inst, ok := hygiene.InstructionKind(kind.value)
	return kind.value, inst, ok
}

// LintCardHeader returns the typed-header findings for one card's bytes.
//
// trust may be nil: with no trust state there is no paused kind, and the lint says
// nothing rather than guessing. required makes the three missing-line tokens apply to a
// card that carries no typed header at all; without it a card with no header line is
// left to the twelve older rules, which is what every card written before §5 is.
func LintCardHeader(raw []byte, trust TrustState, required bool) []CardHeaderFinding {
	return lintCardHeader(raw, trust, required, hygiene.InstructionKind)
}

// lintCardHeader is LintCardHeader with the instruction-kind column as a seam:
// instructionOf is hygiene.InstructionKind in the tool, and a test drives a
// declared kind whose row names none (docs/SPEC-ISA.md).
func lintCardHeader(raw []byte, trust TrustState, required bool, instructionOf func(string) (string, bool)) []CardHeaderFinding {
	h, stranded := cardHeaderBlock(raw)
	typed := required
	for k := range cardTypedKeys {
		if h[k].found {
			typed = true
		}
	}
	// A CARD WITH A TYPED LINE THE GATE CANNOT REACH IS A TYPED CARD. Without
	// this the card is neither: no header line sits inside the block, so no typed check
	// runs, and the card reads clean all the way to `accept`.
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
		// AN UNKNOWN KIND IS NOT A KIND. A KIND: line that names a kind the
		// toolchain does not declare is a finding, so `KIND: completely-unknown-kind`
		// is refused here instead of dying at accept. The names are hygiene.Kinds(),
		// the same set `nova-check hygiene --kind` prints when it refuses.
		add("kind-declared", kind.line, fmt.Sprintf("KIND: %q is not a kind this toolchain declares; one of: %s", kind.value, strings.Join(hygiene.Kinds(), ", ")))
	case hygiene.KindDeclared(kind.value):
		// THE INSTRUCTION KIND IS A COLUMN OF THE ONE LIST (docs/SPEC-ISA.md).
		// A declared KIND whose row names none is refused here, the same token
		// the unknown kind uses, so lint --card prints one remedy.
		inst, _ := instructionOf(kind.value)
		if finding := instructionFinding(kind.value, inst, kind.line); finding != nil {
			add(finding.Check, finding.Line, finding.Excerpt)
		}
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
		// AN EMPTY ENTRY IS NOT A SKIPPABLE ONE. In `PATHS: , , ` each empty
		// entry is a finding, not a value skipped past: the line declares no glob and
		// it is not `none`, so skipping it would answer a reader with neither a glob
		// nor a refusal.
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
		// require a reproducing test, per the toolwork spec's typed-header rule.
		if kind.value != "" && hygiene.KindGated(kind.value) {
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

	// 5. START: and STOP: name where the child reads first and where it ends
	// (docs/SPEC-CARD-CONTRACT.md §2, the header grammar). A brief is a card that names
	// a tier, or one the caller declares typed (`required`), so its missing reading line
	// is a finding. The RESULT line carries the tier as the sprint writes it, read by
	// cardhdr.ReadModel, the one parser of it; `cmd/nova-swarm/lint.go` refuses the
	// finding for a tier flash brief, whose child with no stopping condition reads past
	// its budget, and prints it as a note for tier pro, or no tier.
	m, _ := cardhdr.ReadModel(string(raw))
	if required || m.Tier != "" {
		if f := h["START"]; !f.found {
			add(StartNamedCheck, 1, "no START: line under the contract line; the brief names the files or packages to read first")
		}
		if f := h["STOP"]; !f.found {
			add(StopNamedCheck, 1, "no STOP: line under the contract line; the brief names the condition that ends the task")
		}
	}
	return out
}

// ReadTrustFixture reads the coordinator's per-kind state from a file in the shape
// `nova-pulse trust` prints (the toolwork spec's eligibility rule):
//
//	TRUST kind=<kind> area=<area> state=<trial|trusted|paused> cards=<n>/<N> ...
//	TRUST OK kinds=<n> trial=<n> trusted=<n> paused=<n>
//
// THE VERB DOES NOT EXIST YET. `nova-pulse trust` is the other half of T06a and belongs
// to the retired pulse package; until it lands, the bench hands `lint --card` a fixture
// in exactly this shape with `--trust <file>`, and the day the verb ships its
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

// A REPORT'S FORM IS CHECKED BY THE MACHINE, NEVER BY A READER (docs/SPEC-SPRINT.md, the
// report's form). A card may carry a FORM: block after STOP and before PATHS: the report
// file's path, then one rule per line in a small fixed grammar:
//
//	line 1: <exact text>
//	heading: <level> <regex>
//	item: <field>, <field>, ...
//	each item: one <noun>
//	count: <min>..<max> items
//
// nova-sprint finish on a card with a FORM: block reads the named file and checks every
// rule. A miss refuses the finish, one FORM: line per miss, at most MaxFormMisses; the
// refusal spends no attempt and asks no read. A third miss on one attempt finishes it FAIL.
// The card lint refuses a FORM: it cannot parse, as it refuses PATHS. The grammar lives
// here, in the package the card runner already reads the header with, so a worker's binary
// can render a skeleton without reaching a sprint's store.

// FormKey is the header line that opens a FORM: block.
const FormKey = "FORM"

// MaxFormMisses is the most misses one refusal names: a report is repaired by the first
// ten lines, and a longer list is the same refusal said twice.
const MaxFormMisses = 10

// FormMissesToFail is the number of misses on one attempt that finishes it FAIL.
const FormMissesToFail = 3

// FormRule is one rule of a FORM: block, Kind one of the five the grammar names.
type FormRule struct {
	Kind  string // line1, heading, item, each, count
	Text  string // line1: the exact first line
	Level int    // heading: the number of # marks
	RE    string // heading: the regular expression
	re    *regexp.Regexp
	Field []string // item: the labelled fields, in order
	Noun  string   // each: the noun each item names exactly once
	Min   int      // count: the fewest items
	Max   int      // count: the most items
	Raw   string   // the rule as the brief wrote it
}

// Form is a card's FORM: block: the report file it names and the rules the report holds.
type Form struct {
	Path  string
	Rules []FormRule
}

// HasFormLine says a brief carries a FORM: line, whether or not the block parses: the card
// lint reads it to refuse a FORM: block it cannot parse, as it refuses a PATHS: line.
func HasFormLine(brief string) bool {
	for _, l := range strings.Split(brief, "\n") {
		if k, _, ok := cardhdr.KeyValue(strings.TrimRight(l, "\r")); ok && k == FormKey {
			return true
		}
	}
	return false
}

// HasForm says a brief carries a FORM: block that parses.
func HasForm(brief string) bool {
	_, err := ReadForm(brief)
	return err == nil
}

// formRulePrefixes are the words a rule line starts with; a line after FORM: that starts
// with one of them is a rule and must parse, and any other line ends the block.
var formRulePrefixes = []string{"line ", "heading", "item", "each item", "count"}

// ReadForm reads a brief's FORM: block. It returns an error when the block is not there,
// when its path is empty, when a rule line does not parse, or when the block carries no
// rule; the error names the brief line the defect sits on, as the card lint prints it.
func ReadForm(brief string) (Form, error) {
	lines := strings.Split(brief, "\n")
	at := -1
	var f Form
	for i, l := range lines {
		k, v, ok := cardhdr.KeyValue(strings.TrimRight(l, "\r"))
		if !ok || k != FormKey {
			continue
		}
		at = i
		f.Path = strings.TrimSpace(v)
		break
	}
	if at < 0 {
		return Form{}, fmt.Errorf("no FORM: line")
	}
	if f.Path == "" {
		return Form{}, fmt.Errorf("FORM: line %d names no report file", at+1)
	}
	for i := at + 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			break
		}
		if !formRuleLine(line) {
			break
		}
		r, err := parseFormRule(line)
		if err != nil {
			return Form{}, fmt.Errorf("FORM: line %d: %v", i+1, err)
		}
		r.Raw = strings.TrimSpace(line)
		f.Rules = append(f.Rules, r)
	}
	if len(f.Rules) == 0 {
		return Form{}, fmt.Errorf("FORM: line %d carries no rule", at+1)
	}
	for i := range f.Rules {
		if f.Rules[i].Kind == "heading" {
			re, err := regexp.Compile(f.Rules[i].RE)
			if err != nil {
				return Form{}, fmt.Errorf("FORM: heading: %q is no regular expression: %v", f.Rules[i].RE, err)
			}
			f.Rules[i].re = re
		}
	}
	return f, nil
}

// formRuleLine says a line after FORM: is a rule the grammar names, so a line that is not
// ends the block and a rule the grammar does not hold is refused rather than read past.
func formRuleLine(line string) bool {
	t := strings.TrimSpace(line)
	for _, p := range formRulePrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// parseFormRule reads one rule line.
func parseFormRule(line string) (FormRule, error) {
	t := strings.TrimSpace(line)
	if rest, ok := strings.CutPrefix(t, "line 1:"); ok {
		text := strings.TrimSpace(rest)
		if text == "" {
			return FormRule{}, fmt.Errorf("line 1: names no text")
		}
		return FormRule{Kind: "line1", Text: text}, nil
	}
	if rest, ok := strings.CutPrefix(t, "heading:"); ok {
		words := strings.SplitN(strings.TrimSpace(rest), " ", 2)
		if len(words) != 2 || strings.TrimSpace(words[1]) == "" {
			return FormRule{}, fmt.Errorf("heading: wants `<level> <regex>`")
		}
		level, err := strconv.Atoi(words[0])
		if err != nil || level < 1 || level > 6 {
			return FormRule{}, fmt.Errorf("heading: %q is no heading level from 1 to 6", words[0])
		}
		if _, err := regexp.Compile(words[1]); err != nil {
			return FormRule{}, fmt.Errorf("heading: %q is no regular expression: %v", words[1], err)
		}
		return FormRule{Kind: "heading", Level: level, RE: words[1]}, nil
	}
	if rest, ok := strings.CutPrefix(t, "item:"); ok {
		var fields []string
		for _, f := range strings.Split(rest, ",") {
			f = strings.TrimSpace(f)
			if f == "" {
				return FormRule{}, fmt.Errorf("item: has an empty field between its commas")
			}
			if !formFieldRE.MatchString(f) {
				return FormRule{}, fmt.Errorf("item: %q is no field name", f)
			}
			fields = append(fields, f)
		}
		if len(fields) == 0 {
			return FormRule{}, fmt.Errorf("item: names no field")
		}
		return FormRule{Kind: "item", Field: fields}, nil
	}
	if rest, ok := strings.CutPrefix(t, "each item:"); ok {
		noun, ok := strings.CutPrefix(strings.TrimSpace(rest), "one ")
		noun = strings.TrimSpace(noun)
		if !ok || noun == "" {
			return FormRule{}, fmt.Errorf("each item: wants `one <noun>`")
		}
		return FormRule{Kind: "each", Noun: noun}, nil
	}
	if rest, ok := strings.CutPrefix(t, "count:"); ok {
		body := strings.TrimSpace(rest)
		body = strings.TrimSuffix(body, "items")
		body = strings.TrimSpace(body)
		lo, hi, ok := strings.Cut(body, "..")
		if !ok {
			return FormRule{}, fmt.Errorf("count: wants `<min>..<max> items`")
		}
		min, err1 := strconv.Atoi(strings.TrimSpace(lo))
		max, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil || min < 0 || max < min {
			return FormRule{}, fmt.Errorf("count: %q is no `<min>..<max>` with min <= max", body)
		}
		return FormRule{Kind: "count", Min: min, Max: max}, nil
	}
	return FormRule{}, fmt.Errorf("%q is no rule the grammar names", t)
}

// formFieldRE is what an item's labelled field may be: a Go-identifier word.
var formFieldRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// formItemRE reads a numbered item's first line, `3. ...` or `3) ...`.
var formItemRE = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)

// FormMiss is one rule the report misses: the report line, the rule as the brief wrote
// it and what was found there.
type FormMiss struct {
	Line  int    `json:"line"`
	Rule  string `json:"rule"`
	Found string `json:"found"`
}

// String is the refusal line the finish prints: `FORM: <file>:<line>: <rule> (<found>)`.
func (m FormMiss) String(file string) string {
	return fmt.Sprintf("FORM: %s:%d: %s (%s)", file, m.Line, m.Rule, m.Found)
}

// FormOutcome is what a finish does with a report its card's FORM checks.
type FormOutcome int

const (
	FormPass   FormOutcome = iota // the report holds every rule
	FormRefuse                    // the report misses: refuse the finish, spend no attempt
	FormFail                      // the third miss on one attempt: finish it FAIL
)

// DecideForm is the finish's form decision: the report held against the form, with prior
// the form refusals this attempt already spent. A pass goes on; a miss refuses the finish,
// no attempt and no read spent, each miss one line at most MaxFormMisses; the third miss
// on the attempt is FormFail and the misses are the report.
func DecideForm(prior int, f Form, report string) (FormOutcome, []FormMiss) {
	misses := CheckForm(f, report)
	if len(misses) == 0 {
		return FormPass, nil
	}
	if prior+1 >= FormMissesToFail {
		return FormFail, misses
	}
	return FormRefuse, misses
}

// CheckForm checks report against every rule of the form, in the form's order, and returns
// the misses at most MaxFormMisses.
func CheckForm(f Form, report string) []FormMiss {
	lines := formLines(report)
	items := formItems(lines)
	var out []FormMiss
	add := func(m FormMiss) {
		if len(out) < MaxFormMisses {
			out = append(out, m)
		}
	}
	for _, r := range f.Rules {
		switch r.Kind {
		case "line1":
			found := ""
			if len(lines) > 0 {
				found = lines[0]
			}
			if found != r.Text {
				add(FormMiss{Line: 1, Rule: r.Raw, Found: fmt.Sprintf("the first line is %q", found)})
			}
		case "heading":
			ok := false
			for _, l := range lines {
				level, text, is := formHeading(l)
				if is && level == r.Level && r.re.MatchString(text) {
					ok = true
					break
				}
			}
			if !ok {
				add(FormMiss{Line: 1, Rule: r.Raw, Found: fmt.Sprintf("no level-%d heading matches %q", r.Level, r.RE)})
			}
		case "item":
			for _, it := range items {
				for i, field := range r.Field {
					at := formFieldAt(it.Text, field)
					if at < 0 {
						add(FormMiss{Line: it.Line, Rule: r.Raw, Found: fmt.Sprintf("item %d has no %q field", it.N, field)})
						break
					}
					if i > 0 && at < formFieldAt(it.Text, r.Field[i-1]) {
						add(FormMiss{Line: it.Line, Rule: r.Raw, Found: fmt.Sprintf("item %d has %q before %q", it.N, field, r.Field[i-1])})
						break
					}
				}
			}
		case "each":
			for _, it := range items {
				if n := formWordCount(it.Text, r.Noun); n != 1 {
					add(FormMiss{Line: it.Line, Rule: r.Raw, Found: fmt.Sprintf("item %d names %d %s", it.N, n, r.Noun)})
				}
			}
		case "count":
			if len(items) < r.Min || len(items) > r.Max {
				add(FormMiss{Line: 1, Rule: r.Raw, Found: fmt.Sprintf("the report has %d items", len(items))})
			}
		}
	}
	return out
}

// formLines is a report's lines, the trailing \r of a CRLF file dropped.
func formLines(report string) []string {
	lines := strings.Split(report, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}

// formItem is one numbered item: the number it carries, the line it opens on and its
// whole text (the following lines through the next item or heading).
type formItem struct {
	N    int
	Line int
	Text string
}

// formItems reads a report's numbered items, in order: a line `3. ...`, the lines under it
// through the next numbered line, heading or blank line joining its text.
func formItems(lines []string) []formItem {
	var out []formItem
	for i := 0; i < len(lines); i++ {
		m := formItemRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		it := formItem{N: n, Line: i + 1, Text: m[2]}
		for j := i + 1; j < len(lines); j++ {
			if formItemRE.MatchString(lines[j]) || strings.TrimSpace(lines[j]) == "" {
				break
			}
			if _, _, is := formHeading(lines[j]); is {
				break
			}
			it.Text += " " + strings.TrimSpace(lines[j])
			i = j
		}
		out = append(out, it)
	}
	return out
}

// formHeading reads a markdown heading: its # level and its text; is false for a line
// that is no heading.
func formHeading(line string) (level int, text string, is bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, "#") {
		return 0, "", false
	}
	for level < len(t) && t[level] == '#' {
		level++
	}
	if level > 6 {
		return 0, "", false
	}
	return level, strings.TrimSpace(t[level:]), true
}

// formFieldAt is the byte offset of the labelled field `field:` in an item's text; -1 when
// the item does not name it.
func formFieldAt(text, field string) int {
	return strings.Index(strings.ToLower(text), strings.ToLower(field)+":")
}

// formWordCount is how many whole words equal noun in text, case-insensitively.
func formWordCount(text, noun string) int {
	n := 0
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-'
	}) {
		if w == strings.ToLower(noun) {
			n++
		}
	}
	return n
}

// FormRuleKinds is every rule kind the grammar names, in the order the brief writes them.
func FormRuleKinds() []string {
	return slices.Clone([]string{"line1", "heading", "item", "each", "count"})
}

// FormRefusalText is the refusal a form miss prints: one line per miss, at most
// MaxFormMisses, as the finish's `FORM: <file>:<line>: <rule> (<found>)` lines.
func FormRefusalText(path string, misses []FormMiss) string {
	var lines []string
	for _, m := range misses {
		lines = append(lines, m.String(path))
	}
	return strings.Join(lines, "\n")
}

// ReportSkeleton renders a fill-in skeleton of the report a form asks for: the first line,
// the heading, and one item per labelled field with the fields and the noun left empty, so
// a card can carry it under FILL IN and a flash model copies rather than composes.
func ReportSkeleton(f Form) string {
	first := "Verdict: LAND"
	level, heading := 0, ""
	n := 1
	var fields []string
	noun := ""
	for _, r := range f.Rules {
		switch r.Kind {
		case "line1":
			first = r.Text
		case "heading":
			level, heading = r.Level, strings.Trim(r.RE, "^$")
		case "item":
			fields = r.Field
		case "each":
			noun = r.Noun
		case "count":
			if r.Min > 0 {
				n = r.Min
			} else if r.Max > 0 {
				n = 1
			}
		}
	}
	var b strings.Builder
	b.WriteString(first + "\n")
	if heading != "" || level > 0 {
		b.WriteString("\n" + strings.Repeat("#", max(level, 1)) + " " + heading + "\n")
	}
	for i := 1; i <= n; i++ {
		b.WriteString("\n" + strconv.Itoa(i) + ". ")
		for _, field := range fields {
			b.WriteString(field + ": ")
		}
		if noun != "" {
			b.WriteString(noun + ": ")
		}
		b.WriteString("\n")
	}
	return b.String()
}
