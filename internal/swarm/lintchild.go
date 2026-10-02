package swarm

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// THE CHILD RULES: EVERY RULE THE COORDINATOR GIVES A CHILD IS A RULE OF THE CARD LINT.
//
// A card is the whole of what a child is handed. The rules the coordinator gives every
// child -- stay in the job directory, never force-push, never kill what it did not start,
// report what was not done, and whatever else one project adds -- are carried by the card
// itself: a child that never read a rule breaks it. A card that would not stand alone in
// front of a stranger is not ready, so the rules are the lint's, and a card is refused
// before any spend when it does not carry them.
//
// THE RULE SET IS THE COORDINATOR'S, AND THE TOOL IS GENERAL. The required sentences are
// not one repository's: they come from a rules file the coordinator names (`nova-sprint add
// --rules <file>`, or the path `nova-sprint init --rules <file>` recorded; `nova-swarm lint
// --child-rules-file <file>`), one sentence per line (ParseChildRules). With no file the
// set is DefaultChildRules, the rules that hold for any project.
//
// THREE KINDS OF CHECK, ALL OVER THE CARD'S TEXT AND NOTHING ELSE.
//
//  1. PRESENCE. Each rule is one ChildRule: a name, the sentence the card quotes verbatim
//     (whitespace folded, so a wrapped line still matches), and where the rule came from. A
//     card without the sentence draws `rule-<name>`, whose remedy quotes the sentence, so
//     the writer never has to find the brief it came from.
//  2. SCAN. Where a violation can be read off the text, the text is scanned: a line that
//     runs `redis-server`, `kill`, `rm -rf` outside the job, `git push --force`, `git
//     rebase`, `git stash` or `gh pr merge` draws `step-<what>`; with a rule named
//     `no-go-clean` or `go-test-timeout` in the set, a line that runs `go clean`, or a `go
//     test` with no `-timeout` of a positive duration, draws one too (a Go project's rules,
//     scanned only where the set carries them). A clause (the text back to the last `;`,
//     `,`, `&&`, `||` or `. `) that says `never`, `not`, `no` or `without` before the
//     command is prose about the rule, and is not read as a command. The RULES paragraph,
//     which is where a card quotes what it forbids, runs from its `RULES` line to the first
//     blank line and is not scanned; every other line of the card is.
//
//  3. LIBRARIES CONSIDERED, where the rule set carries a rule named `libraries-considered`
//     (a rules file's switch, as `go-test-timeout` is for the Go scan). A card that builds code carries a `Libraries considered:` line
//     saying what the standard library and the adopted modules offered for the work and why
//     each was used or not (docs/STANDARD.md, section 7, library first). A card builds code
//     when a line outside the RULES paragraph runs `go build`, `go test`, `go run` or
//     `go generate`, or says write, add or implement beside a `.go` path; a card that only
//     reads a `.go` file or cites pkg.go.dev builds nothing. A card that builds code without
//     the line, or whose line is empty or still carries an angle-bracket placeholder, draws
//     `rule-libraries-considered`. The check is that the line says something, never whether
//     what it says is true: a reader names hand-rolled code that a library already does.
//
// WHAT THE TEXT CANNOT SHOW IS NOT CLAIMED. A card that says `Never kill a process you did
// not start` and then `kill $!` is read as the child's own process; whether the child
// kills what it did not start is a run's fact, not a card's. The lint checks the card.
//
// ONE ROW PER RULE. Adding a general rule is one row of DefaultChildRules; its remedy, its
// place in `nova-swarm lint --rules` and the template that quotes it (ChildRulesParagraph)
// follow from the row, and the class tests in lintchild_test.go fail until they do.

// ChildRule is one rule of the child brief: Name is the token (`rule-<Name>` is the lint
// check), Sentence is what the card quotes verbatim, Source is where the rule came from.
type ChildRule struct {
	Name     string
	Sentence string
	Source   string
}

// DefaultChildRules is the set that holds for any project, used when the coordinator names
// no rules file, in the order the RULES paragraph of the template prints them. A rule that
// holds for one project only (its build caches, its test flags, its commit trailer, its
// CI) belongs in that project's rules file.
var DefaultChildRules = []ChildRule{
	{"worktree", "Work only in the job directory this card names.", DefaultRulesSource},
	{"no-force-push", "Never force-push or rebase a shared branch.", DefaultRulesSource},
	{"no-kill", "Never kill a process you did not start.", DefaultRulesSource},
	{"no-server", "Never start a server on this machine.", DefaultRulesSource},
	{"no-rm-rf", "No `rm -rf` outside the job directory.", DefaultRulesSource},
	{"report-not-done", "Report what was not done.", DefaultRulesSource},
}

// LibrariesConsideredName is the name of the rule that switches the lint's libraries check
// on, in a rules file (fleet/child-rules.txt carries it): the check belongs to the project
// whose standard is library first, as the Go scans belong to a Go project's rules. Its
// sentence is what the child reads; the lint does not require the sentence verbatim, it
// requires the filled `Libraries considered:` line of a card that builds code.
const LibrariesConsideredName = "libraries-considered"

// LibrariesConsideredRule is the token of the line a card that builds code carries.
const LibrariesConsideredRule = "rule-" + LibrariesConsideredName

// EmptyCardCheck is the one finding an empty card draws (only blanks in it): a card is a
// child's whole brief, so an empty one is said once, never as every rule it does not quote.
const EmptyCardCheck = "empty"

// EmptyCardRemedy is what that token wants, in the remedies' table shape.
const EmptyCardRemedy = "the card is empty, and a card is a child's whole brief: `nova-swarm template --name card` prints one that passes; put it in the file and fill in its <...> lines"

// LibrariesConsideredRemedy is what that token wants, in the remedies' table shape.
const LibrariesConsideredRemedy = "a card that builds code carries one line `Libraries considered: <what the standard library and the adopted modules offered, and why each was used or not>` (docs/STANDARD.md section 7), filled: a line that is empty after the colon or still carries an angle-bracket placeholder does not count; search before any helper of more than about thirty lines is written, and name what was found; `nova-swarm template --name card` prints the line"

var (
	// childLibrariesLine is a `Libraries considered:` line; group 1 is what follows the colon.
	childLibrariesLine = regexp.MustCompile(`^[ \t]*(?:[-*][ \t]+)?Libraries considered:(.*)$`)
	// childPlaceholder is an angle-bracket placeholder the writer has not filled.
	childPlaceholder = regexp.MustCompile(`<[^<>]*>`)
	// childRunsGo is a line that runs the toolchain to build: go build, test, run or generate.
	childRunsGo = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./-])go[ \t]+(?:build|test|run|generate)\b`)
	// childWrites is a line asking for work: write, add or implement.
	childWrites = regexp.MustCompile(`(?i)\b(?:write|writes|writing|add|adds|adding|implement|implements|implementing)\b`)
	// childGoPath is a path to a Go source file: a name ending `.go` and then a delimiter or
	// the end of the line, so `pkg.go.dev` is no path.
	childGoPath = regexp.MustCompile(`[A-Za-z0-9_<>/.-]+\.go(?:[ \t)\x60"',:;]|\.(?:[ \t]|$)|$)`)
)

// childBuilds reports whether a line outside the RULES paragraph asks for code to be built:
// it runs go build, test, run or generate, or it says write, add or implement beside a .go
// path. A card that only reads a .go file, or cites pkg.go.dev, builds nothing.
func childBuilds(line string) bool {
	return childRunsGo.MatchString(line) || (childWrites.MatchString(line) && childGoPath.MatchString(line))
}

// childLibrariesFilled reports whether what follows the colon says something: not empty
// and not still an angle-bracket placeholder.
func childLibrariesFilled(rest string) bool {
	return strings.TrimSpace(rest) != "" && !childPlaceholder.MatchString(rest)
}

// DefaultRulesSource is where the default rules come from, as a rule's remedy names it.
const DefaultRulesSource = "the built-in default rules"

// childScan is one direct check: the check name, the command it looks for, and what to do
// instead. A scan with Needs set runs only where the rule set carries a rule of that name
// (a Go project's `go clean` and `go test` scans); every other scan runs over every card.
type childScan struct {
	Check  string
	Needs  string
	RE     *regexp.Regexp
	Remedy string
	// Allow, when set, says the command starting at byte `at` of the line is nevertheless
	// fine (a `go test` that carries its timeout, a `kill` of the child's own process).
	Allow func(line string, at int) bool
}

// childCmd wraps a command's pattern: a boundary (the start of the line or a character
// that cannot be part of a path or a name), then the command itself as group 1, so the
// match starts where the command does.
func childCmd(body string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_./-])(` + body + `)`)
}

// gitCmd is `git`, with any `-c k=v` or `-C dir` options between it and the verb.
const gitCmd = `git(?:[ \t]+-[cC][ \t]+\S+)*[ \t]+`

var childScans = []childScan{
	{Check: "step-redis-server",
		RE:     childCmd(`redis-server\b`),
		Remedy: "no line starts a redis-server: a server a child starts belongs to nobody who will stop it, and the machine's own servers belong to whoever runs them; a test that needs one runs where the card says, never by starting it here"},
	{Check: "step-go-clean", Needs: "no-go-clean",
		RE:     childCmd(`go[ \t]+clean\b`),
		Remedy: "no line runs `go clean`: a cache clean breaks every build that shares the cache; give the child a private GOCACHE (a path of its own) and let it be"},
	{Check: "step-kill",
		RE:     childCmd(`(?:kill|pkill|killall)(?:[ \t]|$)`),
		Remedy: "no line kills a process: a child stops only a process it started itself, and says so as `kill $!` or `kill %<n>`; `pkill` and `killall` name processes by pattern and reach another child's",
		Allow:  func(line string, at int) bool { return childKillOwn.MatchString(line[at:]) }},
	{Check: "step-rm-rf",
		RE:     childCmd(`rm[ \t]+(?:-[A-Za-z]+[ \t]+)*(?:-[A-Za-z]*[rR][A-Za-z]*|--recursive)\b`),
		Remedy: "a recursive `rm` names a path inside the job: a relative path without `..`, or one under `$PWD` or `<job>`; never `/`, `~`, `$HOME`, `.`, `*` or a path above the job, and never a variable the card cannot show the value of",
		Allow:  childRmInsideJob},
	{Check: "step-force-push",
		RE:     childCmd(gitCmd + `push\b[^\n;&|]*?(?:[ \t]--force[A-Za-z-]*|[ \t]-[A-Za-z]*f[A-Za-z]*|[ \t]\+\S)`),
		Remedy: "no line force-pushes (`--force`, `--force-with-lease`, `-f`, a `+` refspec): a card's push carries one commit to its own branch, and a rewrite of a shared branch is the coordinator's act alone"},
	{Check: "step-rebase",
		RE:     childCmd(gitCmd + `rebase\b`),
		Remedy: "no line rebases: merge the base forward with `git merge --no-edit`; a rebase rewrites the history another worktree shares"},
	{Check: "step-stash",
		RE:     childCmd(gitCmd + `stash\b`),
		Remedy: "no line stashes: the stash list is shared by every worktree of the repository, so a stash taken here is popped there; commit to the child's own branch instead"},
	{Check: "step-merge",
		RE:     childCmd(`gh[ \t]+pr[ \t]+merge\b`),
		Remedy: "no line merges a pull request: the child opens it against the base the card names and stops; the coordinator lands it"},
	{Check: "step-go-test-timeout", Needs: "go-test-timeout",
		RE:     childCmd(`go[ \t]+test\b`),
		Remedy: "every `go test` carries `-timeout 600s` on the same command, so a hung test ends at ten minutes and not at the card's deadline",
		Allow:  childHasTimeout},
}

var (
	// childNegation is a word that turns the text before a command into prose about the
	// rule: `Never start a redis-server` and `No rm -rf outside the job` are the rules,
	// not the violations.
	childNegation = regexp.MustCompile(`(?i)(?:^|[^-\w])(?:never|not|no|don't|cannot|can't|without|forbid\w*|refus\w*)(?:[^-\w]|$)`)
	// childClauseEnd ends a clause: a negation reaches the command only inside its clause.
	childClauseEnd = regexp.MustCompile(`[;,]|&&|\|\||\.(?:[ \t]|$)`)
	// childTimeout is `-timeout` with a positive duration: `600s`, `10m`, `=1h30m`; `0` and
	// `0s` mean no timeout and do not count.
	childTimeout = regexp.MustCompile(`(?:^|[ \t\x60"'])-{1,2}timeout[= \t]+"?[0-9.]*[1-9][0-9.hmsuµn]*`)
	// childKillOwn is `kill $!` or `kill %<n>`: a process the child started itself.
	childKillOwn = regexp.MustCompile(`^kill[ \t]+(?:-[A-Za-z0-9]+[ \t]+)*(?:"?\$!"?|%[0-9]+)(?:[ \t;&|)]|$)`)
	// childSeparator ends one command on a line.
	childSeparator = regexp.MustCompile(`&&|\|\||;|\||$`)
)

// childHasTimeout reports whether the `go test` command starting at byte at carries
// `-timeout` with a positive duration before the command ends (a `&&`, `||`, `;`, `|` or the end of the line).
func childHasTimeout(line string, at int) bool {
	rest := line[at:]
	end := childSeparator.FindStringIndex(rest)
	return childTimeout.MatchString(rest[:end[0]])
}

// childRmInsideJob reports whether every target of the recursive `rm` at byte at is inside
// the job: relative without `..`, or rooted at `$PWD` or a `<job>` placeholder.
func childRmInsideJob(line string, at int) bool {
	rest := line[at:]
	end := childSeparator.FindStringIndex(rest)
	targets := 0
	for i, f := range strings.Fields(rest[:end[0]]) {
		if i == 0 || strings.HasPrefix(f, "-") {
			continue // the `rm` itself, and its flags
		}
		f = strings.Trim(f, "\"'`")
		targets++
		if !childInsideJob(f) {
			return false
		}
	}
	return targets > 0
}

func childInsideJob(p string) bool {
	low := strings.ToLower(p)
	for _, root := range []string{"$pwd/", "${pwd}/", "<job", "<workspace", "<worktree", "<jobdir"} {
		if strings.HasPrefix(low, root) {
			return !strings.Contains(p, "..")
		}
	}
	switch {
	case p == "", p == ".", p == "./", p == "*", p == "./*":
		return false
	case strings.HasPrefix(p, "/"), strings.HasPrefix(p, "~"), strings.HasPrefix(p, "$"):
		return false
	case strings.Contains(p, ".."):
		return false
	}
	return true
}

// childRulesHeadRE opens the RULES paragraph, where a card quotes what it forbids.
var childRulesHeadRE = regexp.MustCompile(`^(?:#{1,6}[ \t]+)?RULES\b`)

// CardChildRemedies is what each child-rule token of the default set wants, in the table
// shape of CardHeaderRemedies, so `nova-swarm lint --rules` prints them beside the rest. It
// is built from DefaultChildRules and childScans: one row each, never a second list. A rule
// of a rules file is not in it; ChildRemedy answers for those.
var CardChildRemedies = map[string]string{}

func init() {
	seen := map[string]bool{}
	for _, r := range DefaultChildRules {
		if seen[r.Name] {
			panic("nova-swarm lint: two child rules named " + r.Name)
		}
		seen[r.Name] = true
		CardChildRemedies["rule-"+r.Name] = ruleRemedy(r)
	}
	for _, s := range childScans {
		CardChildRemedies[s.Check] = s.Remedy
	}
	CardChildRemedies[LibrariesConsideredRule] = LibrariesConsideredRemedy
	CardChildRemedies[EmptyCardCheck] = EmptyCardRemedy
}

// ruleRemedy is what a missing rule wants: its sentence, verbatim, and where it came from.
func ruleRemedy(r ChildRule) string {
	if r.Name == LibrariesConsideredName {
		return "the rule is " + r.Sentence + " (from " + r.Source + "); " + LibrariesConsideredRemedy
	}
	return "the card quotes this rule verbatim, in its RULES paragraph: " + r.Sentence + " (from " + r.Source + "); `nova-swarm template --name card` prints the paragraph of the default rules, and a rules file's own sentences are the ones the lint reads"
}

// ChildRemedy is what one child-rule check wants under the given rule set: the scan's remedy
// for a `step-` token, the rule's sentence for a `rule-` token of the set, and "" for a
// token that is neither.
func ChildRemedy(rules []ChildRule, check string) string {
	if r, ok := CardChildRemedies[check]; ok && strings.HasPrefix(check, "step-") {
		return r
	}
	for _, r := range rules {
		if "rule-"+r.Name == check {
			return ruleRemedy(r)
		}
	}
	switch check {
	case LibrariesConsideredRule:
		return LibrariesConsideredRemedy
	case EmptyCardCheck:
		return EmptyCardRemedy
	}
	return ""
}

// ChildRulesParagraph is the RULES paragraph of the default rules, one sentence per line:
// what `template --name card` prints and what a card quotes.
func ChildRulesParagraph() string { return RulesParagraph(DefaultChildRules) }

// RulesParagraph is the RULES paragraph of a rule set, one sentence per line.
func RulesParagraph(rules []ChildRule) string {
	var b strings.Builder
	b.WriteString("RULES.\n")
	for _, r := range rules {
		b.WriteString(r.Sentence)
		b.WriteString("\n")
	}
	return b.String()
}

// foldBlanks folds every run of blanks and newlines to one blank, so a rule wrapped over two lines
// is still the rule.
func foldBlanks(s string) string { return strings.Join(strings.Fields(s), " ") }

// childRuleName is a rule's name in a rules file: kebab case, the way a `rule-<name>` token
// is written.
var childRuleName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ParseChildRules reads a rules file: one required sentence per line. A blank line and a
// line starting with `#` are skipped. A line may open with `[name]`, the token its
// finding carries (`rule-<name>`); a line with none is named `line<n>` by its line number.
// Two rules of one name, two of one sentence, a name that is not kebab case and a file
// with no rule at all are refused, each problem named with its line, and every problem of
// the file is reported together, never the first alone. source names the file in each
// rule's remedy.
func ParseChildRules(text, source string) ([]ChildRule, error) {
	var rules []ChildRule
	var problems []string
	names, sentences := map[string]int{}, map[string]int{}
	n := 0
	for _, line := range strings.Split(text, "\n") {
		n++
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := "line" + strconv.Itoa(n)
		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				problems = append(problems, fmt.Sprintf("line %d opens a [name] and never closes it", n))
				continue
			}
			name = line[1:end]
			line = strings.TrimSpace(line[end+1:])
			if !childRuleName.MatchString(name) {
				problems = append(problems, fmt.Sprintf("line %d: the name [%s] is not kebab case (letters, digits and single -)", n, name))
				continue
			}
		}
		if line == "" {
			problems = append(problems, fmt.Sprintf("line %d: the rule [%s] has no sentence", n, name))
			continue
		}
		sentence := foldBlanks(line)
		if prev, dup := names[name]; dup {
			problems = append(problems, fmt.Sprintf("line %d: the name [%s] is already line %d's", n, name, prev))
			continue
		}
		if prev, dup := sentences[sentence]; dup {
			problems = append(problems, fmt.Sprintf("line %d: the sentence repeats line %d's", n, prev))
			continue
		}
		names[name], sentences[sentence] = n, n
		rules = append(rules, ChildRule{Name: name, Sentence: sentence, Source: source + ":" + strconv.Itoa(n)})
	}
	if len(rules) == 0 && len(problems) == 0 {
		problems = append(problems, "it holds no rule: one required sentence per line (a line of `#` words is a comment; `[name] sentence` names the token the lint prints)")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return rules, nil
}

// ReadChildRules reads and parses the rules file at path.
func ReadChildRules(path string) ([]ChildRule, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rules, err := ParseChildRules(string(raw), path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rules, nil
}

// LintCardChild returns the child-rule findings for one card under the default rules.
func LintCardChild(raw []byte) []CardHeaderFinding { return LintCardChildWith(raw, DefaultChildRules) }

// LintCardChildWith returns the child-rule findings for one card under a rule set: a
// `rule-<name>` for every required sentence the card does not quote, then a `step-<what>`
// for every line that runs a forbidden command, each with its line and text. Line 1
// carries the missing sentences: the card lacks them everywhere. An empty card is one
// finding, EmptyCardCheck.
func LintCardChildWith(raw []byte, rules []ChildRule) []CardHeaderFinding {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []CardHeaderFinding{{Check: EmptyCardCheck, Line: 1, Excerpt: "the card is empty"}}
	}
	var out []CardHeaderFinding
	text := foldBlanks(string(raw))
	have := map[string]bool{}
	for _, r := range rules {
		have[r.Name] = true
		if r.Name == LibrariesConsideredName {
			continue // read as the filled line of a card that builds code, below
		}
		if !strings.Contains(text, foldBlanks(r.Sentence)) {
			out = append(out, CardHeaderFinding{Check: "rule-" + r.Name, Line: 1, Excerpt: "missing: " + r.Sentence})
		}
	}
	builds, filled, unfilledAt, unfilled := false, false, 0, ""
	childLines(raw, func(n int, line string) {
		builds = builds || childBuilds(line)
		if m := childLibrariesLine.FindStringSubmatch(line); m != nil {
			if childLibrariesFilled(m[1]) {
				filled = true
			} else if unfilled == "" {
				unfilledAt, unfilled = n, line
			}
		}
		for _, sc := range childScans {
			if sc.Needs != "" && !have[sc.Needs] {
				continue
			}
			for _, m := range sc.RE.FindAllStringSubmatchIndex(line, -1) {
				at := m[2] // the command itself, group 1
				if childNegation.MatchString(childClause(line[:at])) {
					continue
				}
				if sc.Allow != nil && sc.Allow(line, at) {
					continue
				}
				out = append(out, CardHeaderFinding{Check: sc.Check, Line: n, Excerpt: line})
				break
			}
		}
	})
	if have[LibrariesConsideredName] && builds && !filled {
		if unfilled != "" {
			out = append(out, CardHeaderFinding{Check: LibrariesConsideredRule, Line: unfilledAt, Excerpt: "unfilled: " + strings.TrimSpace(unfilled)})
		} else {
			out = append(out, CardHeaderFinding{Check: LibrariesConsideredRule, Line: 1, Excerpt: "missing: Libraries considered: <what was found, why used or not>"})
		}
	}
	return out
}

// childLines calls fn with every line of the card outside the RULES paragraph, which runs
// from its `RULES` line to the first blank line and is where a card quotes what it forbids.
func childLines(raw []byte, fn func(n int, line string)) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n, inRules := 0, false
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			inRules = false
		} else if childRulesHeadRE.MatchString(line) {
			inRules = true
		}
		if !inRules {
			fn(n, line)
		}
	}
}

// childClause is the text of line before a command that belongs to the command's own
// clause: back to the last `;`, `,`, `&&`, `||` or `. `.
func childClause(before string) string {
	if all := childClauseEnd.FindAllStringIndex(before, -1); len(all) > 0 {
		return before[all[len(all)-1][1]:]
	}
	return before
}

// ChildRuleNames is every child-rule check token, sorted, for listings and tests.
func ChildRuleNames() []string {
	names := make([]string, 0, len(CardChildRemedies))
	for n := range CardChildRemedies {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ChildFindingLine is one finding as `nova-sprint add` and `nova-swarm lint` print it
// after their own prefix: `<check>: <line>: <excerpt>`.
func ChildFindingLine(f CardHeaderFinding) string {
	return fmt.Sprintf("%s: %d: %s", f.Check, f.Line, f.Excerpt)
}
