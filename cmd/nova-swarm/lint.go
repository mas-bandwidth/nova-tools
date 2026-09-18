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
const cardLintChecks = 12

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
	"result-first":     "line 1 IS the contract and begins `RESULT: ` -- `RESULT: <CARD-id> <one line of what done looks like>`. A title, a heading or a `#` comment on line 1 is this drift, however right the words are (WORKER-CARDS.md practice 1)",
	"clone-step":       "STEP 1 enters the repository from the working directory: `git clone -q <url> repo && cd repo`, or a `cd` into a checkout that may already be there (practices 17, 25)",
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

	// 1. line 1 is the contract and starts with `RESULT: `.
	if !strings.HasPrefix(first, "RESULT: ") {
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
	case !cardCloneRE.MatchString(steps[stepIndex(steps, 1)].text):
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

// stepIndex is the index of the first step whose number is n, or 0.
func stepIndex(steps []cardStep, n int) int {
	for i, s := range steps {
		if s.num == n {
			return i
		}
	}
	return 0
}

func cmdLint(args []string, stdout, stderr io.Writer) int {
	f := newFlags("lint")
	card := f.fs.String("card", "", "")
	rules := f.fs.Bool("rules", false, "")
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
	findings := lintCard(raw)
	if len(findings) == 0 {
		fmt.Fprintf(stdout, "LINT OK card=%s checks=%d\n", oneline.Field(name), cardLintChecks)
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
	return 2
}
