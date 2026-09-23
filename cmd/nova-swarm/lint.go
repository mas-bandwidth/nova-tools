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

// cardMaxBytes is the ADVISORY ceiling a card is written within: past it a model stops
// reading the card in one window, so the lint says so before any spend -- and nothing is
// refused and nothing is cut, so a card over it still ships (issues #1494, #1527).
//
// IT WAS READ BOTH WAYS ON THE SAME DAY. On 2026-09-19 two managers trimmed cards to reach
// it and two others shipped over it on purpose, because the line said `at or over the
// 12000-byte ceiling` and the verb exited 2, which is the exit code a caller refuses on.
// The ceiling is a reading budget, not an input limit: a 12422-byte card was measured
// through the harness untruncated (#1494). So a card over it draws a `LINT NOTE`, never a
// `LINT DRIFT`; the note never changes the verdict; and the size rides on every lint,
// clean or not, with `advisory=true` said in the bytes so nobody has to ask again.
const cardMaxBytes = 12000

// cardLintAdvisory is every check whose finding is advice rather than a defect. An advisory
// finding is printed on a `LINT NOTE` line and is not in the verdict: a card whose only
// findings are advisory is a clean card and exits 0.
var cardLintAdvisory = map[string]bool{"size": true}

// cardLintChecks is how many independent shapes lintCard looks for. It is printed on the
// LINT OK line so a reader knows how much of the card was actually checked, and it is the
// size of cardLintRemedies below: a check with no remedy is a red test, never a judgement.
//
// It counts the twelve shape rules of docs/WORKER-CARDS.md:23-36, the four typed-header
// tokens SPEC-TOOLWORK.md §5 rule 1 adds -- `kind-declared`, `paths-declared`, `test-named`
// and `paused` -- whose rules live in internal/swarm/lintheader.go, beside a note on the
// gate parser they have to agree with (internal/pulse/cardheader.go, #1721 at f927bccc),
// and `depends-on` (#2636), which fires only under `--typed`, and the four base checks of
// internal/swarm/lintbase.go -- `paths-at-base`, `no-push-steps`, `leg-in-fleet` and
// `deadline-p95` (#2636) -- which fire only under `--base-check`.
const cardLintChecks = 21

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
	"test-command":     "write the gate verbatim, exactly as the card is to run it -- the accepted set is `make`/`gmake <target>` (the most common polyglot gate and the one `ci-fast.yml` runs), `go test`, `go vet`, `pytest`, `cargo test`, `npm test`, `dotnet test`, `ctest`, `mvn test`, `gradle test`, `bash <script>` or a bare `./<script>`, or say in words that there are no tests (practice 5; #1994)",
	"deadline":         "give the card its own bound: a `deadline` line, or `finish within <n> minutes` (practice 17)",
	"files-named":      "name the file or the package the work lives in, so the change has a home to start from (practice 3)",
	"scratch-absolute": "the LINE quoted is the one to fix: spell scratch against a named root -- `<job>/scratch`, `$PWD/scratch`, an absolute path -- and never as the bare word. An absolute path on another line does not answer for this one (practice 25)",
	"no-parent-path":   swarm.CardParentPathWanted,
	"no-sandbox":       "a card runs INSIDE the wall and never invokes it; drop the `nova-sandbox` line (practice 2)",
	"result-last":      "the LAST step writes RESULT.md, and RESULT.md's own line 1 is the contract line from line 1 of this card (practices 1, 25)",
	"size":             "ADVICE, not a limit: a card over the ceiling is not refused, not truncated and still ships, so nothing here has to be cut. The ceiling is the budget that keeps a model reading the card in one window -- to come under it, point at a file instead of pasting it, and drop quoted source",
	"depends-on":       swarm.CardDependsRemedy,
}

// THE TYPED HEADER'S FOUR TOKENS JOIN THE SAME TABLE (SPEC-TOOLWORK.md §5 rule 1, #1651).
// They are defined in internal/swarm beside the header rules themselves, because that is
// where the grammar the gate parses is restated; they are merged here so `--rules` prints
// one listing and cardLintChecks counts one set. A token defined in both places is a
// collision this init refuses to paper over.
func init() {
	for _, table := range []map[string]string{swarm.CardHeaderRemedies, swarm.CardBaseRemedies} {
		for name, remedy := range table {
			if _, clash := cardLintRemedies[name]; clash {
				panic("nova-swarm lint: two remedies for the rule " + name)
			}
			cardLintRemedies[name] = remedy
		}
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
	cardStepRE  = regexp.MustCompile(`^STEP[ \t]+([0-9]+)[.)]?`)
	cardCloneRE = regexp.MustCompile(`(?i)(clone|(^|[ \t&|(])cd[ \t])`)
	cardTestRE  = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]*`)
	cardReadRE  = regexp.MustCompile(`(?i)\b(probe|read)\b`)
	// THE TEST-COMMAND CHECK ACCEPTS MORE THAN ONE VOCABULARY (issue #1994).
	//
	// The four-line whitelist (`go test`/`go vet`/`pytest`/`cargo test`/`npm test`) was
	// written when every gate in ci-fast.yml was `go test ./...`. Then ci-fast.yml moved
	// to `make <leg>` (the Makefile is the one entry per AGENTS.md rule 6), and 155 of 176
	// polyglot cards in the `mas-bandwidth/schema` lane tripped on a verbatim `make`
	// gate that the repository would actually run. The accepted set widens to the verbs a
	// card writer might honestly name -- the four it already took, the rest of the common
	// runners, the script-and-runner shapes, and the words `no tests` -- so a card that
	// names the gate `ci-fast.yml` runs is not refused by the linter.
	//
	// The leading context is start-of-string or a non-word character; the trailing context
	// is end-of-string or a non-word character. Word characters here are `A-Za-z0-9_./-`,
	// the set a path component can hold. `npmtest` does not match `npm test`, and
	// `cargo run` does not match `cargo test`.
	cardCommandRE = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_./-])(?:go[ \t]+(?:test|vet)|pytest|cargo[ \t]+test|npm[ \t]+test|dotnet[ \t]+test|ctest|mvn[ \t]+test|gradle[ \t]+test|(?:g)?make[ \t]+[A-Za-z0-9_./-]+|bash[ \t]+\S+|\./[A-Za-z0-9_][A-Za-z0-9_./-]*|no[ \t]+tests)(?:[^A-Za-z0-9_./-]|$)`)
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

	// 9. no path above the job: the wall refuses one, so the card may not walk there. The
	// rule is what the card WALKS, not every `../` in its text; a `../` the card quotes --
	// a fenced block, a backtick span, a markdown link target, a `go test` ellipsis -- is
	// not a path the worker takes. The four shapes, measured on the 2026-09-19 shift's own
	// cards, and what is given up by exempting them, are in internal/swarm/lintparent.go.
	for _, n := range swarm.CardParentPaths(lines) {
		add("no-parent-path", n, lines[n-1])
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

	// 12. the card is under the advisory ceiling. Over it is said and never refused.
	if len(raw) >= cardMaxBytes {
		add("size", 1, fmt.Sprintf("card is %d bytes, over the %d-byte advisory ceiling; it is not refused and not truncated", len(raw), cardMaxBytes))
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

// THE FLEET'S SCRIPTS ARE LINTED TOO (issue #2012).
//
// Two self-inflicted outages in one night, same class. A launcher used `mapfile`;
// launchers execute on the coordinator -- a Mac whose /bin/bash is 3.2 -- so every
// Linux launch failed ~10 minutes on `mapfile: command not found` while the darwin
// benches kept working. Earlier, a zsh one-liner passed `$C` unquoted expecting word
// splitting, and four loops printed usage and died; the lanes also measured zsh eating
// `:r` in refspecs (`$NEW:refs/...`) and `set -- $var` not splitting.
//
// `lint --fleet <script>` is the rule the issue asks for, in the tool the fleet already
// runs: it reads the ONE launcher script it is handed -- no model, no probe, one file,
// before any launch -- and fails it on the three shapes that night cost. It runs no
// shell and asks no bench which bash it has; it holds every fleet script to the one the
// coordinator pins, /bin/bash 3.2, because that is the least the fleet has.

// fleetLintChecks is how many independent shapes lintFleetScript looks for. It rides the
// LINT OK line the way cardLintChecks does, so a reader knows how much of the script
// was actually checked and a check added without a remedy is a red test.
const fleetLintChecks = 3

// fleetLintRemedies is the fleet table, the same contract as cardLintRemedies: every
// drift names what its rule wants, and the table lives in the binary because a bench's
// clone of this repository is months behind the binary installed on it. It is a
// SEPARATE table rather than a fourth section of cardLintRemedies because `--rules`
// lists the card checks and its count is cardLintChecks -- one table, one count,
// two verbs that never share a file.
var fleetLintRemedies = map[string]string{
	"bash-shebang":       "line 1 is the interpreter line and it names bash, the one shell every fleet script is linted for: `#!/bin/bash` is the spelling the coordinator pins, `#!/usr/bin/env bash` names the same shell through PATH. `#!/bin/zsh` runs zsh, which neither word-splits an unquoted variable nor forgives `$NEW:refs/...` (its `:r` modifier eats it), and a script with no interpreter line is run by whatever shell typed its name -- on the coordinator that is zsh (nova-tools #2012)",
	"bash4-builtin":      "`mapfile` and `readarray` are one bash-4 builtin under two names and macOS's /bin/bash is 3.2 and has neither: every Linux launch died ~10 minutes on `mapfile: command not found` while the darwin benches kept working. Read lines the 3.2 way -- `while IFS= read -r line; do ...; done < \"$file\"` (nova-tools #2012)",
	"unquoted-expansion": "put the expansion in double quotes -- `\"$C\"` -- or, when it concatenates, quote the variable and let the literal follow: `\"$NEW\":refs/heads/x`. zsh does not word-split an unquoted variable (four loops printed usage and died on `for c in $C`, and `set -- $var` does not split) and its `:r` modifier eats `$NEW:refs/...`; when the split is the point, ask for it in a spelling every shell keeps: `read -r -a xs <<< \"$C\"` and then `\"${xs[@]}\"` (nova-tools #2012)",
}

// fleetLintRemedy is what one fleet rule wants, in one line -- the same contract as
// cardLintRemedy, over this verb's own table.
func fleetLintRemedy(check string) string {
	if r, ok := fleetLintRemedies[check]; ok {
		return r
	}
	return "this fleet rule carries no remedy line, which is itself a defect in nova-swarm; run `nova-swarm lint --fleet <script>` for the rules that do"
}

// fleetBash4RE is the bash-4-only command set this lint fails: `mapfile` and `readarray`
// are one builtin under its two names, new in bash 4.0, and macOS's /bin/bash is 3.2.
// The word must stand alone -- `remapfile` is not the builtin, `./mapfile` is a file,
// `--mapfile` is a flag and `x=mapfile` is a string -- and it is matched only in text
// OUTSIDE quotes, where fleetLineScan has already cut comments: a `mapfile` in prose or
// in a quoted string is a word, not a command.
var fleetBash4RE = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.=-])(?:mapfile|readarray)(?:[^A-Za-z0-9_-]|$)`)

// fleetShebangNamesBash reports whether a script's first line is the interpreter line
// and names bash: `#!/bin/bash` is the spelling the coordinator pins and
// `#!/usr/bin/env bash` names the same shell through PATH. `#!/bin/zsh`, `#!/bin/sh`
// and a first line with no `#!` at all are the shapes the second outage came from: a
// launcher run by name on a Mac gets whatever shell the line names, and with none it
// gets the shell that typed it, which there is zsh.
func fleetShebangNamesBash(first string) bool {
	rest, ok := strings.CutPrefix(first, "#!")
	if !ok {
		return false
	}
	for _, f := range strings.Fields(rest) {
		if f == "bash" || strings.HasSuffix(f, "/bash") {
			return true
		}
	}
	return false
}

// lintFleetScript returns every mechanical defect in one fleet script's text, in line
// order: a first line that does not name bash, a bash-4-only builtin macOS's 3.2 has
// not got, and an unquoted parameter expansion relying on a word split zsh never makes.
// It reads nothing but the bytes it was handed.
func lintFleetScript(raw []byte) []cardFinding {
	lines := strings.Split(string(raw), "\n")
	var out []cardFinding
	add := func(check string, line int, excerpt string) {
		out = append(out, cardFinding{check: check, line: line, excerpt: excerpt})
	}

	// 1. the fleet runs under /bin/bash. The interpreter line is the one thing a
	// launcher run by name controls, and on the coordinator it is the whole of the
	// zsh outage.
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	}
	if !fleetShebangNamesBash(first) {
		add("bash-shebang", 1, first)
	}

	// 2. and 3. read the script line by line, skipping what is not code: a heredoc's
	// body is data, a comment is prose, and neither can run a builtin or lean on a
	// split. The shebang line reads as a comment to the scanner, which it is.
	heredoc := ""
	for i, l := range lines {
		if heredoc != "" {
			if strings.TrimLeft(l, "\t") == heredoc {
				heredoc = ""
			}
			continue
		}
		bare, unquoted, start := fleetLineScan(l)
		if start != "" {
			heredoc = start
		}
		if fleetBash4RE.MatchString(bare) {
			add("bash4-builtin", i+1, l)
		}
		if unquoted {
			add("unquoted-expansion", i+1, l)
		}
	}
	return out
}

// fleetLineScan walks one line the way a shell reads it and returns the line's text
// OUTSIDE single and double quotes with any comment cut (bare), whether an unquoted
// parameter expansion sits in it, and the delimiter when the line starts a heredoc.
// Bare text is what a builtin check may read: a `mapfile` inside a quoted string is
// prose, a `mapfile` inside `$( )` is code, and the difference is the quotes.
func fleetLineScan(line string) (bare string, unquoted bool, heredoc string) {
	// The text is gathered with append rather than a strings.Builder's Write* methods
	// because the one-line audit refuses those by name anywhere in this package: the
	// tripwire cannot tell a builder from a stream and is right not to guess.
	var out []byte
	inSingle, inDouble := false, false
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '\\' && !inSingle && i+1 < len(line):
			// an escaped character is literal; no quote it spells opens anything
			out = append(out, line[i:i+2]...)
			i += 2
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			i++
		case c == '"' && !inSingle:
			inDouble = !inDouble
			i++
		case c == '#' && !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			// a `#` outside quotes at a word start is a comment: prose, not code
			return string(out), unquoted, heredoc
		case c == '$' && !inSingle:
			n, splits := fleetExpansionSpan(line, i)
			if n == 0 {
				out = append(out, c)
				i++
				continue
			}
			if !inDouble && splits {
				unquoted = true
			}
			out = append(out, line[i:i+n]...)
			i += n
		case c == '<' && !inSingle && !inDouble && strings.HasPrefix(line[i:], "<<") && heredoc == "":
			if d, n := fleetHeredocAt(line, i); n > 0 {
				heredoc = d
				i += n
				continue
			}
			out = append(out, c)
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return string(out), unquoted, heredoc
}

// fleetExpansionSpan reports how many bytes of line the parameter or command expansion
// at line[i] -- a `$` -- covers, 0 when it is not one, and whether the expansion is one
// whose word-splitting differs between bash and zsh. A parameter expansion does differ:
// zsh never word-splits an unquoted one, which is the outage. The single-character
// specials do not: zsh splits `$@` and `$*` like bash does, and `$# $? $$ $! $-` hold
// nothing splittable. Command and arithmetic substitution do not: zsh splits the output
// of an unquoted `$( )` exactly like bash, and `$(( ))` never splits.
func fleetExpansionSpan(line string, i int) (n int, splits bool) {
	if i+1 >= len(line) {
		return 0, false
	}
	switch c := line[i+1]; {
	case c == '{':
		depth := 0
		for j := i + 1; j < len(line); j++ {
			switch line[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return j - i + 1, true
				}
			}
		}
		return len(line) - i, true // unterminated: the whole tail is the expansion
	case c == '(':
		open := 1
		for j := i + 2; j < len(line); j++ {
			switch line[j] {
			case '(':
				open++
			case ')':
				open--
				if open == 0 {
					return j - i + 1, false
				}
			}
		}
		return len(line) - i, false // unterminated: taken whole, never flagged
	case c == '@' || c == '*' || c == '#' || c == '?' || c == '$' || c == '!' || c == '-':
		return 2, false
	case fleetNameByte(c):
		j := i + 1
		for j < len(line) && fleetNameByte(line[j]) {
			j++
		}
		return j - i, true
	default:
		return 0, false
	}
}

// fleetHeredocAt reads the heredoc a `<<` at line[i] starts and returns its delimiter
// and its byte length. `<<-` strips the body's leading tabs, a quoted delimiter makes
// the body literal, and `<<<` -- a herestring, not a heredoc -- and no delimiter word
// are not one. The body a heredoc opens is data to the shell that reads it and is not
// code this lint may judge.
func fleetHeredocAt(line string, i int) (delim string, n int) {
	j := i + 2
	if j < len(line) && line[j] == '-' {
		j++
	}
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	quote := byte(0)
	if j < len(line) && (line[j] == '\'' || line[j] == '"') {
		quote = line[j]
		j++
	}
	start := j
	for j < len(line) && fleetNameByte(line[j]) {
		j++
	}
	if j == start {
		return "", 0
	}
	d := line[start:j]
	if quote != 0 {
		if j >= len(line) || line[j] != quote {
			return "", 0
		}
		j++
	}
	return d, j - i
}

// fleetNameByte is the character set a shell name is spelled with: the letters, the
// digits and the underscore.
func fleetNameByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func cmdLint(args []string, stdout, stderr io.Writer) int {
	f := newFlags("lint")
	card := f.fs.String("card", "", "")
	// `--fleet <file>` IS THE LAUNCHER'S OWN LINT (issue #2012). A card is checked for
	// what it costs a bench to run; a fleet script is checked for what it costs the
	// fleet to RUN AT ALL: the coordinator's /bin/bash is 3.2 and a script that leans
	// on bash 4 or on zsh's word-splitting differences takes every launch down with it.
	// The same verb, the same one-file contract, the same drift lines and remedies.
	fleet := f.fs.String("fleet", "", "")
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
	// `--lineup <file>` IS THE SPRINT LINEUP, AND ONLY FOR `--typed` (#2636). The lint has
	// no lineup of its own. The file is ORDER.tsv's shape — id in the first column, or the
	// column named id/card/card-id/label, a header that names depends-on skipped — or one
	// card id per line. With no file an id is not called unknown.
	lineupPath := f.fs.String("lineup", "", "")
	// `--base-check` IS THE ASK FOR THE FOUR BASE CHECKS OF A CODING CARD (#2636):
	// PATHS resolve at base-sha in `--repo` (default the working directory), no STEP
	// runs `git push` or `gh`, LEG is in the `--legs` fleet table, and DEADLINE is at
	// or above the kind's p95 in the `--p95` table. Evidence not handed over is not a
	// pass: its check draws a finding that says MISSING and names the flag.
	baseCheck := f.fs.Bool("base-check", false, "")
	repoDir := f.fs.String("repo", ".", "")
	legsPath := f.fs.String("legs", "", "")
	p95Path := f.fs.String("p95", "", "")
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
	// THE FLEET SCRIPT IS THE OTHER ONE-FILE INPUT, AND THE TWO NEVER SHARE A RUN
	// (issue #2012). A card is read by shape rules a card writer meets on a bench; a
	// launcher script is read by shell rules a fleet meets on the coordinator. The card
	// flags over a script are a guess about a different file, so they are refused, and
	// the verdict lines say `script=` rather than `card=` so a caller cannot mistake
	// one input's verdict for the other's.
	if *fleet != "" {
		if *card != "" {
			f.add("--card and --fleet want one input: --card is a worker card, --fleet is a launcher script; give one and lint the other in its own run")
		}
		if *typed || *trustPath != "" || *lineupPath != "" {
			f.add("--typed, --trust and --lineup are card checks; --fleet holds a launcher script, and a card flag over a script is a guess about a different file")
		}
		if f.refused(stderr) {
			return 2
		}
		raw, err := os.ReadFile(*fleet)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm lint: --fleet wants a readable launcher script of shell text to check: %s\n", oneline.Err(err))
			return 2
		}
		name := filepath.Base(*fleet)
		findings := lintFleetScript(raw)
		if len(findings) == 0 {
			fmt.Fprintf(stdout, "LINT OK script=%s checks=%d bytes=%d\n", oneline.Field(name), fleetLintChecks, len(raw))
			return 0
		}
		printed := findings
		more := false
		if *max > 0 && len(findings) > *max {
			printed, more = findings[:*max], true
		}
		for _, fd := range printed {
			// The remedy rides on the same line as the card lint's drifts (#1464): a
			// launcher writer holding this line reads what the rule wants without
			// grepping any clone.
			fmt.Fprintf(stdout, "LINT DRIFT script=%s %s: %d: %s remedy=%s\n",
				oneline.Field(name), oneline.Field(fd.check), fd.line,
				oneline.Escape(oneline.Cap(fd.excerpt, oneline.TailBytes)),
				oneline.Escape(fleetLintRemedy(fd.check)))
		}
		if more {
			fmt.Fprintf(stdout, "LINT MORE script=%s findings=%d remedy=nova-swarm lint --fleet %s --max 0\n",
				oneline.Field(name), len(findings), oneline.Field(*fleet))
		}
		return 2
	}
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
	var lineup swarm.Lineup
	if *lineupPath != "" {
		l, err := swarm.ReadLineup(*lineupPath)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm lint: --lineup wants a readable lineup file, one card id per line or a TSV whose id column is the card id (a header row that names depends-on is skipped): %s\n", oneline.Err(err))
			return 2
		}
		lineup = l
	}
	bc := swarm.BaseCheck{Repo: *repoDir}
	if *baseCheck && *legsPath != "" {
		l, err := swarm.ReadFleetLegs(*legsPath)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm lint: --legs wants a readable fleet leg table, one leg per line or a TSV whose first column is the leg: %s\n", oneline.Err(err))
			return 2
		}
		bc.Legs = l
	}
	if *baseCheck && *p95Path != "" {
		p, err := swarm.ReadKindP95(*p95Path)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm lint: --p95 wants a readable table of `<kind> <seconds>` rows, the p95 wall of each kind's DONE cards (`*` answers for any kind): %s\n", oneline.Err(err))
			return 2
		}
		bc.P95 = p
	}
	findings := lintCard(raw)
	for _, hf := range swarm.LintCardHeader(raw, trust, *typed) {
		findings = append(findings, cardFinding{check: hf.Check, line: hf.Line, excerpt: hf.Excerpt})
	}
	// DEPENDS-ON IS REQUIRED ONLY WHEN THE CARD WAS ASKED TO BE TYPED (#2636). A card
	// that already carries KIND: is still linted without this key, which is every card
	// cut before the key existed. `--typed` is the ask.
	if *typed {
		for _, hf := range swarm.LintCardDepends(raw, lineup) {
			findings = append(findings, cardFinding{check: hf.Check, line: hf.Line, excerpt: hf.Excerpt})
		}
	}
	if *baseCheck {
		for _, hf := range swarm.LintCardBase(raw, bc) {
			findings = append(findings, cardFinding{check: hf.Check, line: hf.Line, excerpt: hf.Excerpt})
		}
	}
	// ADVICE IS NOT A DEFECT, AND THE VERDICT SAYS WHICH (issues #1494, #1527). A drift is
	// a defect and exits 2, which a caller refuses on; a note is advice and changes no
	// verdict. The two are told apart here, once, so neither the writer nor the caller has
	// to read the check's name to know what happened to the card.
	var drifts, notes []cardFinding
	for _, fd := range findings {
		if cardLintAdvisory[fd.check] {
			notes = append(notes, fd)
			continue
		}
		drifts = append(drifts, fd)
	}
	note := func(fd cardFinding) {
		fmt.Fprintf(stdout, "LINT NOTE card=%s %s: %d: %s remedy=%s\n",
			oneline.Field(name), oneline.Field(fd.check), fd.line,
			oneline.Escape(oneline.Cap(fd.excerpt, oneline.TailBytes)),
			oneline.Escape(cardLintRemedy(fd.check)))
	}
	// THE CEILING IS NEVER A SILENT BOUND. A card writer learned of the 12000-byte cap by
	// hitting it: a card at 11k looked exactly like a card at 2k. Every lint says how big
	// this card is and what the cap is, on the OK line and, below, on the drift path.
	if len(drifts) == 0 {
		fmt.Fprintf(stdout, "LINT OK card=%s checks=%d bytes=%d cap=%d\n", oneline.Field(name), cardLintChecks, len(raw), cardMaxBytes)
		for _, fd := range notes {
			note(fd)
		}
		return 0
	}
	printed := drifts
	more := false
	if *max > 0 && len(drifts) > *max {
		printed, more = drifts[:*max], true
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
			oneline.Field(name), len(drifts), oneline.Field(*card))
	}
	for _, fd := range notes {
		note(fd)
	}
	// A drifting card gets the size too: a writer cutting a card down to fix a drift is
	// exactly the writer who needs to know how close to the ceiling the card already is --
	// and `advisory=true` is the answer to the question two managers asked on one day.
	fmt.Fprintf(stdout, "LINT SIZE card=%s bytes=%d cap=%d advisory=true\n", oneline.Field(name), len(raw), cardMaxBytes)
	return 2
}
