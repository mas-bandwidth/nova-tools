package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A card is the one artefact whose defects are paid for in tokens before a test runs: a
// clone step that enters the wrong directory, a `../scratch` the wall refuses, a missing red
// test, no deadline. `lint --card` reads the card mechanically -- no model, no probe, one
// file -- and names each defect by check, line and excerpt before any spend. The checks are
// the shape the card deaths taught (docs/WORKER-CARDS.md practices 17, 18, 23, 25).

// cardMaxBytes is the ceiling a card may not reach: a card past it is not read in one
// window, and the lint says so before any spend.
const cardMaxBytes = 12000

// cardLintChecks is how many independent shapes lintCard looks for. It is printed on the
// LINT OK line so a reader knows how much of the card was actually checked, and it is the
// size of cardLintRemedies below: a check with no remedy is a red test, never a judgement.
//
// It counts the twelve shape rules of docs/WORKER-CARDS.md:23-36 and the four typed-header
// tokens SPEC-TOOLWORK.md §5 rule 1 adds -- `kind-declared`, `paths-declared`, `test-named`
// and `paused` -- whose rules live in internal/swarm/lintheader.go, beside a note on the
// gate parser they have to agree with (internal/pulse/cardheader.go, #1721 at f927bccc).
const cardLintChecks = 16

// EVERY DRIFT NAMES ITS REMEDY, AND THE BINARY CAN PRINT THE WHOLE TABLE (issue #1464).
//
// A card written by hand on 2026-09-18 came back with five drifts, three of them pointing at
// line 1 -- which is the contract line practice 1 says line 1 must be -- and the writer could
// not tell what any of them wanted:
//
//	LINT DRIFT result-first: 1: fixed: row 5 writer_bound_count on go
//	LINT DRIFT scratch-absolute: 24: write; everything you clone, scratch and report goes under that absolute path.
//
// `nova-swarm help` says of every listing that it carries "one MORE line naming the remedy".
// This one named the rule, quoted the line and stopped.
//
// AND THE RULE TOKENS WERE WRITTEN DOWN NOWHERE THE BENCH COULD READ. docs/WORKER-CARDS.md
// carries the practices in prose and names none of these tokens, and a bench's clone of this
// repository is months behind the binary installed on it -- `grep -rn result-first` over the
// clone on vision found nothing at all. So the remedies live HERE, in the tool, beside the
// checks they belong to: `nova-swarm lint --rules` prints every one of them, which a bench
// with a stale clone can still run, and the doc names the tokens beside their practices.
//
// ONE TABLE, TWO READERS. The DRIFT line and the `--rules` listing are the same map, so a
// remedy cannot drift from the rule it explains, and a check added without one is caught by
// the count above before it ships.
var cardLintRemedies = map[string]string{
	"result-first":     "line 1 IS the contract: " + swarm.CardContractWanted + ". A title, a heading or a `#` comment on line 1 is this drift, however right the words are (WORKER-CARDS.md practice 1; the two forms and why there are two are in internal/swarm/lintcontract.go)",
	"clone-step":       "STEP 1 enters the repository from the working directory: the whole step, its line and the lines under it, holds a `git clone -q <url> repo && cd repo`, or a `cd ` into a checkout that may already be there. The wording of the STEP line itself is yours; the command is the rule (practices 17, 25)",
	"steps-numbered":   "each step is its own line beginning `STEP <n>.`, numbered 1, 2, 3 with no gap and no repeat; a card with no STEP lines at all is this drift (practice 17)",
	"red-test":         "name the reproducing test by its own name -- `TestSomething` -- or, for a card that only reads, say `probe` or `read` in so many words (practice 23)",
	"test-command":     "write the gate verbatim, exactly as the card is to run it (`go test ./internal/x/ -run TestY -count=1`), or say in words that there are no tests (practice 5)",
	"deadline":         "give the card its own bound: a `deadline` line, or `finish within <n> minutes` (practice 17)",
	"files-named":      "name the file or the package the work lives in, so the change has a home to start from (practice 3)",
	"scratch-absolute": "the LINE quoted is the one to fix: spell scratch against a named root -- `<job>/scratch`, `$PWD/scratch`, an absolute path -- and never as the bare word. An absolute path on another line does not answer for this one (practice 25)",
	"no-parent-path":   "the wall refuses every path above the job: put the worktree, the scratch and the notes under the working directory instead of reaching through `../` (practice 25)",
	"no-sandbox":       "a card runs INSIDE the wall and never invokes it; drop the `nova-sandbox` line (practice 2)",
	"result-last":      "the LAST step writes RESULT.md, and RESULT.md's own line 1 is the contract line from line 1 of this card (practices 1, 25)",
	"size":             "cut the card under the ceiling so a model reads it in one window: point at a file instead of pasting it, and drop quoted source",
}

// THE TYPED HEADER'S FOUR TOKENS JOIN THE SAME TABLE (SPEC-TOOLWORK.md §5 rule 1, #1651).
// They are defined in internal/swarm beside the header rules themselves, because that is
// where the grammar the gate parses is restated; they are merged here so `--rules` prints
// one listing and cardLintChecks counts one set. A token defined in both places is a
// collision this init refuses to paper over.
func init() {
	for name, remedy := range swarm.CardHeaderRemedies {
		if _, clash := cardLintRemedies[name]; clash {
			panic("nova-swarm lint: two remedies for the rule " + name)
		}
		cardLintRemedies[name] = remedy
	}
}

// cardLintRuleNames is every rule token in one order, so the listing is byte-stable.
func cardLintRuleNames() []string {
	names := make([]string, 0, len(cardLintRemedies))
	for name := range cardLintRemedies {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// cardLintRemedy is what one rule wants, in one line. A rule with no entry is a defect this
// package's own tests refuse, and the fallback says so rather than printing an empty field.
func cardLintRemedy(check string) string {
	if r, ok := cardLintRemedies[check]; ok {
		return r
	}
	return "this rule carries no remedy line, which is itself a defect in nova-swarm; run `nova-swarm lint --rules` for the rules that do"
}

// cardFinding is one mechanical defect: the check's name, the 1-based line it sits on, and
// the line's own text, escaped and capped before it reaches an event line.
type cardFinding struct {
	check   string
	line    int
	excerpt string
}

var (
	cardStepRE    = regexp.MustCompile(`^STEP[ \t]+([0-9]+)[.)]?`)
	cardCloneRE   = regexp.MustCompile(`(?i)(clone|(^|[ \t&|(])cd[ \t])`)
	cardTestRE    = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]*`)
	cardReadRE    = regexp.MustCompile(`(?i)\b(probe|read)\b`)
	cardCommandRE = regexp.MustCompile(`(?i)(go[ \t]+test|go[ \t]+vet|pytest|cargo[ \t]+test|npm[ \t]+test|no[ \t]+tests)`)
	cardDeadRE    = regexp.MustCompile(`(?i)(deadline|finish within)`)
	cardFileRE    = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_.-]*\.(go|py|rs|js|ts|md|lisp|sh|json|toml|txt)\b|\./[A-Za-z0-9_./-]+`)
	cardScratchRE = regexp.MustCompile(`(/[A-Za-z0-9_./<>$-]*scratch\b)|(\$\{?[A-Za-z_]+\}?/scratch\b)|(<[^>]+>/scratch\b)`)
)

// cardStep is one `STEP <n>` line and the line number it sits on.
type cardStep struct {
	line int
	num  int
	text string
}

func cardSteps(lines []string) []cardStep {
	var steps []cardStep
	for i, l := range lines {
		if m := cardStepRE.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			steps = append(steps, cardStep{line: i + 1, num: n, text: l})
		}
	}
	return steps
}

// lintCard returns every mechanical defect in one card's text. It reads nothing but the
// bytes it was handed, in the order the checks are named in cardLintChecks.
func lintCard(raw []byte) []cardFinding {
	text := string(raw)
	lines := strings.Split(text, "\n")
	var out []cardFinding
	add := func(check string, line int, excerpt string) {
		out = append(out, cardFinding{check: check, line: line, excerpt: excerpt})
	}
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	}

	// 1. line 1 is the contract, in either of the two forms the tools write today. The
	// two are swarm.CardContractPrefixes, and the reason there are two -- `cut` writes
	// one, WORKER-CARDS practice 1 the other -- is written out there, with the class test
	// beside it that holds this lint, `cut` and `gather` to one line.
	if !swarm.IsCardContractLine(first) {
		add("result-first", 1, first)
	}

	steps := cardSteps(lines)

	// 2. STEP 1 clones or enters the repo with cd, from the working directory first.
	stepOne := -1
	for _, s := range steps {
		if s.num == 1 {
			stepOne = s.line
			break
		}
	}
	switch {
	case stepOne < 0 && len(steps) > 0:
		add("clone-step", steps[0].line, steps[0].text)
	case stepOne < 0:
		add("clone-step", 1, first)
	case !cardCloneRE.MatchString(cardStepBody(lines, steps, stepIndex(steps, 1))):
		// THE STEP IS ITS LINE AND ITS BODY. Matching the `STEP 1.` line alone made this
		// a check on wording: a step that reads `STEP 1. Get the tree.` and then clones
		// and cds on the line under it drew a drift for the verb it chose, while the
		// command the rule is actually about was right there. The rule is what the step
		// DOES, so the whole step is read, down to the next STEP line.
		add("clone-step", stepOne, steps[stepIndex(steps, 1)].text)
	}

	// 3. the STEP lines are numbered 1, 2, 3, ... in order.
	if len(steps) == 0 {
		add("steps-numbered", 1, "(no STEP lines)")
	} else {
		want := 1
		for _, s := range steps {
			if s.num != want {
				add("steps-numbered", s.line, s.text)
				want = s.num + 1
				continue
			}
			want++
		}
	}

	// 4. a named red test, or the word probe/read for a card that only reads.
	if !cardTestRE.MatchString(text) && !cardReadRE.MatchString(text) &&
		!strings.Contains(strings.ToLower(text), "red test") {
		add("red-test", 1, "no reproducing test named")
	}

	// 5. a named test command, or the statement that there are no tests.
	if !cardCommandRE.MatchString(text) {
		add("test-command", 1, "no test command named")
	}

	// 6. a deadline, or the words `finish within`.
	if !cardDeadRE.MatchString(text) {
		add("deadline", 1, "no deadline")
	}

	// 7. a file or a package is named, so the work has a home.
	if !cardFileRE.MatchString(text) {
		add("files-named", 1, "no file or package named")
	}

	// 8. scratch is named absolutely, never as a bare relative path.
	if strings.Contains(text, "scratch") && !cardScratchRE.MatchString(text) {
		for i, l := range lines {
			if strings.Contains(l, "scratch") {
				add("scratch-absolute", i+1, l)
				break
			}
		}
	}

	// 9. no `../` path anywhere: the wall refuses a path above the job.
	for i, l := range lines {
		if strings.Contains(l, "../") {
			add("no-parent-path", i+1, l)
		}
	}

	// 10. no nova-sandbox invocation: the card runs inside the wall, never probes it.
	for i, l := range lines {
		if strings.Contains(l, "nova-sandbox") {
			add("no-sandbox", i+1, l)
		}
	}

	// 11. the final step writes RESULT.md with the RESULT line first.
	if len(steps) == 0 {
		add("result-last", 1, first)
	} else {
		last := steps[len(steps)-1]
		found := false
		for i := last.line - 1; i < len(lines); i++ {
			if strings.Contains(lines[i], "RESULT.md") {
				found = true
				break
			}
		}
		if !found {
			add("result-last", last.line, last.text)
		}
	}

	// 12. the card is under the ceiling.
	if len(raw) >= cardMaxBytes {
		add("size", 1, fmt.Sprintf("card is %d bytes, at or over the %d-byte ceiling", len(raw), cardMaxBytes))
	}

	return out
}

// cardStepBody is one step's whole text: its own `STEP <n>.` line and every line under
// it up to the next STEP line, or to the end of the card. A step's command usually sits
// on the line below the sentence that introduces it, and a check on what a step DOES has
// to read there.
func cardStepBody(lines []string, steps []cardStep, i int) string {
	if i < 0 || i >= len(steps) {
		return ""
	}
	from := steps[i].line - 1 // the STEP line itself, 0-based
	to := len(lines)
	if i+1 < len(steps) {
		to = steps[i+1].line - 1
	}
	if from < 0 || from > len(lines) || to < from {
		return ""
	}
	return strings.Join(lines[from:to], "\n")
}

// stepIndex is the index of the first step whose number is n, or 0.
func stepIndex(steps []cardStep, n int) int {
	for i, s := range steps {
		if s.num == n {
			return i
		}
	}
	return 0
}

// matchingTemplate returns the name of the shipped template whose verbatim text is exactly
// raw, or "" when raw is not one of the templates this tool prints. It is what lets
// `nova-swarm template --name <t>` piped into `lint --card` be answered by name.
func matchingTemplate(raw []byte) string {
	for _, name := range swarm.TemplateNames() {
		if body, err := swarm.Template(name); err == nil && string(raw) == body {
			return name
		}
	}
	return ""
}

func cmdLint(args []string, stdout, stderr io.Writer) int {
	f := newFlags("lint")
	card := f.fs.String("card", "", "")
	rules := f.fs.Bool("rules", false, "")
	// THE TYPED HEADER IS CHECKED WHEN THE CARD HAS ONE, AND ON DEMAND WHEN IT DOES NOT.
	// A card cut under SPEC-TOOLWORK §5 carries five typed lines; every card written before
	// it carries none, and those are still linted by the twelve older rules. So the header
	// tokens fire on any card that declares one of the five lines, and `--typed` says that
	// this card is meant to have a header even though it has none.
	typed := f.fs.Bool("typed", false, "")
	// `--trust <file>` IS A FIXTURE UNTIL `nova-pulse trust` EXISTS. The per-kind state is
	// T06a's other half and lives in the lane that owns internal/pulse; the file this flag
	// reads is in the exact shape that verb's listing prints, so the day it ships, its own
	// stdout is what is handed here. With no --trust there is no state, and `paused` is not
	// checked rather than guessed at.
	trustPath := f.fs.String("trust", "", "")
	max := maxFlag(f.fs)
	if !f.parse(args, stderr) {
		return 2
	}
	// `--rules` IS THE DOCUMENT A BENCH CAN READ (issue #1464). A card writer on a bench has
	// the binary and a clone months behind it, so the rules are asked of the binary. It takes
	// no card, because the question is asked before there is one.
	if *rules {
		for _, name := range cardLintRuleNames() {
			fmt.Fprintf(stdout, "LINT RULE %s remedy=%s\n", oneline.Field(name), oneline.Escape(cardLintRemedies[name]))
		}
		return 0
	}
	f.wantMax(*max)
	f.want(*card, "card", "the path to the card file whose shape is checked before any spend; --rules prints every rule and what it wants instead")
	if f.refused(stderr) {
		return 2
	}
	raw, err := os.ReadFile(*card)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm lint: --card wants a readable file of the card text: %s\n", oneline.Err(err))
		return 2
	}
	name := filepath.Base(*card)
	// A template is printed verbatim and is not itself a card: `nova-swarm template --name
	// <t>` piped into `lint --card` used to report result-first drift on the template's first
	// line (issue #1471). The card templates pass, and a template that is not a card answers
	// by name rather than as a drift.
	if tmpl := matchingTemplate(raw); tmpl != "" {
		if swarm.IsCardTemplate(tmpl) {
			fmt.Fprintf(stdout, "LINT OK card=%s checks=%d bytes=%d cap=%d\n", oneline.Field(name), cardLintChecks, len(raw), cardMaxBytes)
			return 0
		}
		fmt.Fprintf(stdout, "LINT NOT-A-CARD card=%s template=%s remedy=%s\n",
			oneline.Field(name), oneline.Field(tmpl),
			oneline.Escape("not a card; lint --card wants a card, and the card templates are read-pr, probe-row, fix-card"))
		return 1
	}
	var trust swarm.TrustState
	if *trustPath != "" {
		t, err := swarm.ReadTrustFixture(*trustPath)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm lint: --trust wants a readable file of `TRUST kind=<kind> ... state=<trial|trusted|paused>` lines, in the shape `nova-pulse trust` prints: %s\n", oneline.Err(err))
			return 2
		}
		trust = t
	}
	findings := lintCard(raw)
	for _, hf := range swarm.LintCardHeader(raw, trust, *typed) {
		findings = append(findings, cardFinding{check: hf.Check, line: hf.Line, excerpt: hf.Excerpt})
	}
	// THE CEILING IS NEVER A SILENT BOUND. A card writer learned of the 12000-byte cap by
	// hitting it: a card at 11k looked exactly like a card at 2k. Every lint says how big
	// this card is and what the cap is, on the OK line and, below, on the drift path.
	if len(findings) == 0 {
		fmt.Fprintf(stdout, "LINT OK card=%s checks=%d bytes=%d cap=%d\n", oneline.Field(name), cardLintChecks, len(raw), cardMaxBytes)
		return 0
	}
	printed := findings
	more := false
	if *max > 0 && len(findings) > *max {
		printed, more = findings[:*max], true
	}
	for _, fd := range printed {
		// THE REMEDY RIDES ON THE SAME LINE (issue #1464). `nova-swarm help` promises one
		// more line naming the remedy of every listing; a rule token and a quoted line
		// without it cost a card writer a guess per drift.
		fmt.Fprintf(stdout, "LINT DRIFT card=%s %s: %d: %s remedy=%s\n",
			oneline.Field(name), oneline.Field(fd.check), fd.line,
			oneline.Escape(oneline.Cap(fd.excerpt, oneline.TailBytes)),
			oneline.Escape(cardLintRemedy(fd.check)))
	}
	if more {
		fmt.Fprintf(stdout, "LINT MORE card=%s findings=%d remedy=nova-swarm lint --card %s --max 0\n",
			oneline.Field(name), len(findings), oneline.Field(*card))
	}
	// A drifting card gets the size too: a writer cutting a card down to fix a drift is
	// exactly the writer who needs to know how close to the ceiling the card already is.
	fmt.Fprintf(stdout, "LINT SIZE card=%s bytes=%d cap=%d\n", oneline.Field(name), len(raw), cardMaxBytes)
	return 2
}
