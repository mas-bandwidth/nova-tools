package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
// LINT OK line so a reader knows how much of the card was actually checked.
const cardLintChecks = 12

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
	max := maxFlag(f.fs)
	if !f.parse(args, stderr) {
		return 2
	}
	f.wantMax(*max)
	f.want(*card, "card", "the path to the card file whose shape is checked before any spend")
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
		fmt.Fprintf(stdout, "LINT DRIFT card=%s %s: %d: %s\n",
			oneline.Field(name), oneline.Field(fd.check), fd.line,
			oneline.Escape(oneline.Cap(fd.excerpt, oneline.TailBytes)))
	}
	if more {
		fmt.Fprintf(stdout, "LINT MORE card=%s findings=%d remedy=nova-swarm lint --card %s --max 0\n",
			oneline.Field(name), len(findings), oneline.Field(*card))
	}
	return 2
}
