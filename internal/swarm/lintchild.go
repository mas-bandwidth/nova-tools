package swarm

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// THE CHILD RULES: EVERY RULE THE COORDINATOR GIVES A CHILD IS A RULE OF THE CARD LINT.
//
// A card is the whole of what a child is handed. The rules the coordinator gives every
// child -- a private GOCACHE, no redis-server, no force-push, `-timeout 600s` on every
// `go test`, the class tests before a push, the commit trailer, never merge, what the
// report says -- are carried by the card itself: a child that never read a rule breaks it.
// A card that would not stand alone in front of a stranger is not ready, so the rules are
// the lint's, and a card is refused before any spend when it does not carry them.
//
// TWO KINDS OF CHECK, BOTH OVER THE CARD'S TEXT AND NOTHING ELSE.
//
//  1. PRESENCE. Each rule is one row of CardChildRules: a name, the sentence the card
//     quotes verbatim (whitespace folded, so a wrapped line still matches), and the file
//     the rule came from. A card without the sentence draws `rule-<name>`, whose remedy
//     quotes the sentence, so the writer never has to find the brief it came from.
//  2. SCAN. Where a violation can be read off the text, the text is scanned: a line that
//     runs `redis-server`, `go clean`, `kill`, `rm -rf` outside the job, `git push
//     --force`, `git rebase`, `git stash` or `gh pr merge`, or a `go test` with no
//     `-timeout` of a positive duration, draws `step-<what>`. A clause (the text back to the
//     last `;`, `,`, `&&`, `||` or `. `) that says `never`, `not`, `no` or `without` before the
//     command is prose about the rule, and is not read as a command. The RULES paragraph,
//     which is where a card quotes what it forbids, runs from its `RULES` line to the first
//     blank line and is not scanned; every other line of the card is.
//
// WHAT THE TEXT CANNOT SHOW IS NOT CLAIMED. A card that says `Never kill a process you did
// not start` and then `kill $!` is read as the child's own process; whether the child
// kills what it did not start is a run's fact, not a card's. The lint checks the card.
//
// ONE ROW PER RULE. Adding a rule is one row of CardChildRules; its remedy, its place in
// `nova-swarm lint --rules` and the template that quotes it (ChildRulesParagraph) follow
// from the row, and the class tests in lintchild_test.go fail until they do.

// ChildRule is one rule of the child brief: Name is the token (`rule-<Name>` is the lint
// check), Sentence is what the card quotes verbatim, Source is the file it came from.
type ChildRule struct {
	Name     string
	Sentence string
	Source   string
}

// CardChildRules is every rule the coordinator gives every child, in the order the RULES
// paragraph of the template prints them. SAFETY.md, VERBS-COMMON.md, READ-COMMON.md,
// DIRTY-TICK-SLICES.md and INTEGRATION.md are the coordinator's brief files; a rule
// that holds for one kind of card only (a reader's verdict, a verb item's snapshot, the
// integration order) is stated by that kind's own brief and is not a row here.
var CardChildRules = []ChildRule{
	{"worktree", "Work only in the NEW worktree this card names.", "SAFETY.md"},
	{"own-branch", "Touch only your own branch.", "SAFETY.md"},
	{"gocache", "Export a private GOCACHE (the path this card names) and GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 before any go command.", "SAFETY.md"},
	{"no-go-clean", "Never `go clean`, and never clean a shared cache.", "SAFETY.md"},
	{"no-redis-server", "NEVER start a redis-server on this machine.", "SAFETY.md"},
	{"no-kill", "Never kill a process you did not start.", "SAFETY.md"},
	{"go-test-timeout", "Every `go test` gets `-timeout 600s`.", "SAFETY.md, DIRTY-TICK-SLICES.md"},
	{"no-rm-rf", "No `rm -rf` outside the job directory.", "SAFETY.md"},
	{"no-force-push", "Never force-push.", "SAFETY.md, DIRTY-TICK-SLICES.md"},
	{"no-rebase", "Never rebase.", "DIRTY-TICK-SLICES.md"},
	{"no-stash", "Do not use git stash (the stash list is shared by every worktree).", "DIRTY-TICK-SLICES.md"},
	{"functional-in-container", "Functional tests (any test that needs Redis) run ONLY inside the container through `tools/functionalrun` (`--fresh-gocache --deadline 15m`), never against any other store.", "SAFETY.md"},
	{"parallel", "Every new test opens with `t.Parallel()`.", "SAFETY.md, DIRTY-TICK-SLICES.md"},
	{"class-tests", "Run `go test -count=1 -timeout 600s ./internal/ci/` before each push.", "SAFETY.md, DIRTY-TICK-SLICES.md"},
	{"no-names", "No names of people, machines or friends in code, comments or docs.", "SAFETY.md"},
	{"present-tense", "Docs and comments in the present tense.", "SAFETY.md"},
	{"cite", "Cite the model or the design section from every function that implements a rule.", "SAFETY.md, VERBS-COMMON.md"},
	{"only-named-files", "Touch only the files this card names; a fix that needs another file goes into your report as a proposed diff, not a commit.", "DIRTY-TICK-SLICES.md"},
	{"minimal-diff", "Keep the diff minimal: every added line traceable to one sentence of this card.", "DIRTY-TICK-SLICES.md"},
	{"commit-trailer", "Commit messages end with `Co-Authored-By: Claude <your model> <noreply@anthropic.com>`.", "SAFETY.md"},
	{"pr-line", "PR bodies end with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.", "SAFETY.md"},
	{"never-merge", "Open PRs against the base this card names; never merge.", "SAFETY.md, DIRTY-TICK-SLICES.md"},
	{"exit-codes", "exit codes: 0 done, 1 refused, 2 usage or a store that did not answer (fleet sync --check: there is drift), 3 fleet sync could not read the config", "the nova-sprint banner (cmd/nova-sprint usage text; a test holds the two equal)"},
	{"pr-diffstat", "The PR body states the diff stat and what was deleted.", "the owner's list"},
	{"pr-tests", "The PR body lists the tests, each with what it pins, and every local helper added.", "VERBS-COMMON.md"},
	{"report-shape", "Report under 80 lines: PR number and sha, every test package line, what you could not do and why.", "SAFETY.md, DIRTY-TICK-SLICES.md"},
	{"report-not-done", "\"Not done\" is a welcome report; a green claim you did not run is not.", "SAFETY.md"},
}

// childScan is one direct check: the check name, the command it looks for, what to do
// instead, and the rule it enforces (a name in CardChildRules).
type childScan struct {
	Check  string
	Rule   string
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
	{Check: "step-redis-server", Rule: "no-redis-server",
		RE:     childCmd(`redis-server\b`),
		Remedy: "no line starts a redis-server: a test that needs Redis is a functional test and runs only in the container through `tools/functionalrun`; a unit test opens no store (the machine's Redis belongs to whoever runs it)"},
	{Check: "step-go-clean", Rule: "no-go-clean",
		RE:     childCmd(`go[ \t]+clean\b`),
		Remedy: "no line runs `go clean`: a cache clean breaks every build that shares the cache; give the child a private GOCACHE (a path of its own) and let it be"},
	{Check: "step-kill", Rule: "no-kill",
		RE:     childCmd(`(?:kill|pkill|killall)(?:[ \t]|$)`),
		Remedy: "no line kills a process: a child stops only a process it started itself, and says so as `kill $!` or `kill %<n>`; `pkill` and `killall` name processes by pattern and reach another child's",
		Allow:  func(line string, at int) bool { return childKillOwn.MatchString(line[at:]) }},
	{Check: "step-rm-rf", Rule: "no-rm-rf",
		RE:     childCmd(`rm[ \t]+(?:-[A-Za-z]+[ \t]+)*(?:-[A-Za-z]*[rR][A-Za-z]*|--recursive)\b`),
		Remedy: "a recursive `rm` names a path inside the job: a relative path without `..`, or one under `$PWD` or `<job>`; never `/`, `~`, `$HOME`, `.`, `*` or a path above the job, and never a variable the card cannot show the value of",
		Allow:  childRmInsideJob},
	{Check: "step-force-push", Rule: "no-force-push",
		RE:     childCmd(gitCmd + `push\b[^\n;&|]*?(?:[ \t]--force[A-Za-z-]*|[ \t]-[A-Za-z]*f[A-Za-z]*|[ \t]\+\S)`),
		Remedy: "no line force-pushes (`--force`, `--force-with-lease`, `-f`, a `+` refspec): a child pushes its own branch with a plain `git push`, and a rewrite of a shared branch is the coordinator's act alone"},
	{Check: "step-rebase", Rule: "no-rebase",
		RE:     childCmd(gitCmd + `rebase\b`),
		Remedy: "no line rebases: merge the base forward with `git merge --no-edit`; a rebase rewrites the history another worktree shares"},
	{Check: "step-stash", Rule: "no-stash",
		RE:     childCmd(gitCmd + `stash\b`),
		Remedy: "no line stashes: the stash list is shared by every worktree of the repository, so a stash taken here is popped there; commit to the child's own branch instead"},
	{Check: "step-merge", Rule: "never-merge",
		RE:     childCmd(`gh[ \t]+pr[ \t]+merge\b`),
		Remedy: "no line merges a pull request: the child opens it against the base the card names and stops; the coordinator lands it"},
	{Check: "step-go-test-timeout", Rule: "go-test-timeout",
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

// CardChildRemedies is what each child-rule token wants, in the table shape of
// CardHeaderRemedies, so `nova-swarm lint --rules` prints them beside the rest. It is
// built from CardChildRules and childScans: one row each, never a second list.
var CardChildRemedies = map[string]string{}

func init() {
	seen := map[string]bool{}
	for _, r := range CardChildRules {
		if seen[r.Name] {
			panic("nova-swarm lint: two child rules named " + r.Name)
		}
		seen[r.Name] = true
		CardChildRemedies["rule-"+r.Name] = "the card quotes this rule verbatim, in its RULES paragraph: " + r.Sentence + " (from " + r.Source + "); `nova-swarm template --name card` prints the paragraph with every rule in place"
	}
	for _, s := range childScans {
		if !seen[s.Rule] {
			panic("nova-swarm lint: the scan " + s.Check + " enforces " + s.Rule + ", which is no child rule")
		}
		CardChildRemedies[s.Check] = s.Remedy
	}
}

// ChildRulesParagraph is the RULES paragraph with every rule sentence, one per line: what
// `template --name card` prints and what a card quotes.
func ChildRulesParagraph() string {
	var b strings.Builder
	b.WriteString("RULES.\n")
	for _, r := range CardChildRules {
		b.WriteString(r.Sentence)
		b.WriteString("\n")
	}
	return b.String()
}

// foldBlanks folds every run of blanks and newlines to one blank, so a rule wrapped over two lines
// is still the rule.
func foldBlanks(s string) string { return strings.Join(strings.Fields(s), " ") }

// LintCardChild returns the child-rule findings for one card: a `rule-<name>` for every
// required sentence the card does not quote, then a `step-<what>` for every line that
// runs a forbidden command, each with its line and text. Line 1 carries the missing
// sentences: the card lacks them everywhere.
func LintCardChild(raw []byte) []CardHeaderFinding {
	var out []CardHeaderFinding
	text := foldBlanks(string(raw))
	for _, r := range CardChildRules {
		if !strings.Contains(text, r.Sentence) {
			out = append(out, CardHeaderFinding{Check: "rule-" + r.Name, Line: 1, Excerpt: "missing: " + r.Sentence})
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n, inRules := 0, false
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		// the RULES paragraph runs from its line to the first blank line
		if strings.TrimSpace(line) == "" {
			inRules = false
		} else if childRulesHeadRE.MatchString(line) {
			inRules = true
		}
		if inRules {
			continue
		}
		for _, sc := range childScans {
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
	}
	return out
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
