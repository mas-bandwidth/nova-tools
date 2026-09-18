package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// makefile_test.go is the layout check behind CARD-9019: the Makefile is the one
// entry for build, test and lint, so the commands in ci.yml that build, vet,
// format or test must be make invocations rather than their own `go ...` lines.
// The workflow may still discover its shards with `go list` and set up a
// toolchain by hand — those are setup steps, not the build/test/lint commands a
// reader compares to their own `make test` — and they are allowlisted by name
// below.
//
// Both files are read as text on purpose: the repository has no YAML library in
// go.mod (standard library only), and these tests must run on every platform the
// matrix covers and give the same answer on each — including Windows, where
// `make` is not installed, and including three GNU make versions (3.81 on the
// Studio, 4.3 on hulk, 4.4.1 on the space runner) whose output differs. Nothing
// here runs make; see TestMakefileIsTheOneEntry for why. It is the same approach
// ci_budget_test.go takes for the job names and timeouts.

// buildTestLintRe matches a build, test or lint COMMAND at the start of a shell
// statement: `go build`, `go vet`, `go test`, `gofmt`, or the nova-work
// acceptance script. `go version`, `go list` and `go env` are setup and are
// deliberately not matched. The command may follow a shell separator — `;`,
// `&&`, `||`, `|` or `$(` — so a command substitution is caught too.
var buildTestLintRe = regexp.MustCompile(`(^|[;&|($])\s*(go\s+(build|vet|test)\b|gofmt\b|\./lisp/nova-work/run-tests\.sh\b)`)

// commandAllowlist names the setup and shard-loop lines that are allowed not to
// be a make invocation. They run no test and check no formatting: they discover
// the package set or the toolchain the make target then runs against, or they
// are the fleet-probe's own diagnostic build. The fleet-probe is a
// workflow_dispatch job that proves a bench is real before the loop trusts it;
// its build and one-package run are the probe, not the CL build/test entry.
var commandAllowlist = []string{
	"go list",
	"go test -list",
	"go version",
	"go env",
	"command -v go",
	"GOMAXPROCS",
	"go build ./cmd/nova-sandbox",
	"go build ./... && go test -count=1 ./internal/oneline/",
}

// requiredTargets is the one entry CARD-9019 names: build, test (fast tier),
// test-full, lint, check (what CI runs) and clean.
var requiredTargets = []string{"build", "test", "test-full", "lint", "check", "clean", "help"}

// runCommand is one shell command line inside a step's `run:` block, with the
// ci.yml line number it came from so a red names the edit to make.
type runCommand struct {
	line int
	cmd  string
}

func TestCIBuildTestLintCommandsGoThroughMake(t *testing.T) {
	root := repoRoot(t)
	src := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	cmds := runCommands(src)
	if len(cmds) == 0 {
		t.Fatal("no run commands parsed from ci.yml; the parser is looking in the wrong place")
	}
	sawMake := false
	for _, c := range cmds {
		if c.cmd == "" || strings.HasPrefix(c.cmd, "#") {
			continue
		}
		isMake := strings.HasPrefix(c.cmd, "make ") || c.cmd == "make"
		if isMake {
			sawMake = true
		}
		if !buildTestLintRe.MatchString(c.cmd) {
			continue
		}
		if allowlistedCommand(c.cmd) || isMake {
			continue
		}
		t.Errorf("ci.yml:%d: build/test/lint command is not a make invocation: %q", c.line, c.cmd)
	}
	if !sawMake {
		t.Error("ci.yml names no make invocation; the Makefile is not the one entry for the CL tier")
	}
}

// TestMakefileIsTheOneEntry pins the Makefile's own shape: the required targets
// are declared and phony, `check` is the union of the gates CI runs, and `clean`
// removes only the two explicit directories.
//
// IT PARSES THE MAKEFILE'S OWN RULES, and never runs make. The first revision of
// this test shelled out to `make -n check` and compared the dry-run text; it
// passed on hulk (GNU make 4.3) and failed on the space runner (4.4.1) and on
// the Studio (3.81), because the dry-run output is not a contract: 4.4 prints
// recipes it used to suppress, 3.81 lays out a `bash -c` line differently, and
// what $(GO) and $(PKGS) expand to depends on the environment make inherits — a
// GO or PKGS exported by a shell profile silently rewrites the very strings the
// assertion reads. A test whose verdict depends on the host's make version and
// environment says nothing about the repository.
//
// So the parser below reads the rules the way a reader does: targets, their
// prerequisites, their recipe lines, the variables, and any included file. It
// expands the Makefile's OWN variable values, not the environment's, so the
// answer is the same byte for byte on 3.81, on 4.4.1, on a runner with GO set to
// something else, and on the Windows legs where there is no make at all. The
// contract being pinned is the Makefile's text, which is what a friend reads and
// what CI runs; that is exactly what the parser sees.
func TestMakefileIsTheOneEntry(t *testing.T) {
	root := repoRoot(t)
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))

	for _, want := range requiredTargets {
		if _, ok := mk.recipes[want]; !ok {
			if _, dep := mk.deps[want]; !dep {
				t.Errorf("Makefile declares no %q target", want)
			}
		}
		if !mk.phony[want] {
			t.Errorf("Makefile .PHONY does not name %q", want)
		}
	}

	// clean removes exactly two named directories: one recipe line, an explicit
	// list, no computed path (the removal rule internal/ci holds elsewhere).
	clean := mk.recipeFor("clean")
	if len(clean) != 1 || strings.TrimSpace(clean[0]) != "rm -rf ./bin ./scratch" {
		t.Errorf("Makefile clean is not the explicit two-directory removal `rm -rf ./bin ./scratch`, it is %q", clean)
	}

	// check is the union of the gates CI runs, named as prerequisites.
	checkDeps := map[string]bool{}
	for _, d := range mk.deps["check"] {
		checkDeps[d] = true
	}
	for _, want := range []string{"build", "lint", "test", "test-e2e", "test-lisp"} {
		if !checkDeps[want] {
			t.Errorf("Makefile check does not run %q; the contract is build, lint, test, test-e2e and test-lisp", want)
		}
	}

	// And the gates themselves: every command `make check` would run, gathered
	// from check and the transitive closure of its prerequisites, with the
	// Makefile's own variables expanded. This is what the dry-run comparison was
	// reaching for, minus the host.
	recipes := strings.Join(mk.recipesUnder("check"), "\n")
	for _, gate := range []string{
		"go build ./...",
		"gofmt -l .",
		"go vet ./...",
		"go test -count=1 ./cmd/... ./internal/...",
		"go test -count=1 -run TestFriendSequence ./cmd/...",
		"./lisp/nova-work/run-tests.sh",
	} {
		if !strings.Contains(recipes, gate) {
			t.Errorf("`make check` does not reach %q; the recipes it runs are:\n%s", gate, recipes)
		}
	}

	help := strings.Join(mk.recipeFor("help"), "\n")
	for _, target := range requiredTargets {
		if !strings.Contains(help, "make "+target) {
			t.Errorf("the help target does not list %q:\n%s", target, help)
		}
	}
}

// parsedMakefile is what the parser below reads out of a Makefile: its
// variables, each target's prerequisites and recipe lines, and the .PHONY set.
// It is a SMALL parser on purpose — enough of GNU make's syntax to read this
// repository's Makefile exactly, and no more: simple and recursive variable
// assignment, target-specific variables, `include`, line continuations, and the
// recipe prefixes @ - and +. Anything it does not understand it leaves alone,
// which shows up as an assertion that cannot find its gate rather than as a
// quietly wrong answer.
type parsedMakefile struct {
	vars       map[string]string
	targetVars map[string]map[string]string
	deps       map[string][]string
	recipes    map[string][]string
	phony      map[string]bool
}

var (
	makeVarRe       = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(\?=|:=|::=|\+=|=)\s*(.*)$`)
	makeRuleRe      = regexp.MustCompile(`^([^\t#=][^:=]*):(?:\s+(.*))?$`)
	makeIncludeRe   = regexp.MustCompile(`^-?include\s+(.*)$`)
	makeExpandRe    = regexp.MustCompile(`\$[({]([A-Za-z_][A-Za-z0-9_]*)[)}]`)
	makeRecipePfxRe = regexp.MustCompile(`^[@+-]+`)
)

// dollarDollar stands in for `$$` (an escaped dollar, which make hands to the
// shell as one `$`) while variables are expanded, so a shell expression like
// `$${TMPDIR:-/tmp}` is never mistaken for a make variable reference.
const dollarDollar = "\x00"

func parseMakefile(t *testing.T, path string) *parsedMakefile {
	t.Helper()
	mk := &parsedMakefile{
		vars:       map[string]string{},
		targetVars: map[string]map[string]string{},
		deps:       map[string][]string{},
		recipes:    map[string][]string{},
		phony:      map[string]bool{},
	}
	mk.read(t, path, 0)
	return mk
}

// read parses one file into mk, following `include` lines relative to the
// including file's directory. depth bounds the recursion so a Makefile that
// includes itself is a failed test and not a hung one.
func (mk *parsedMakefile) read(t *testing.T, path string, depth int) {
	t.Helper()
	if depth > 8 {
		t.Fatalf("include depth over 8 at %s; a Makefile includes itself", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	lines := joinContinuations(strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n"))
	current := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "\t") {
			// A recipe line belongs to the rule above it. A shell comment in a
			// recipe runs nothing, so it is not part of the contract.
			body := strings.TrimSpace(line)
			if current == "" || body == "" || strings.HasPrefix(body, "#") {
				continue
			}
			body = makeRecipePfxRe.ReplaceAllString(body, "")
			mk.recipes[current] = append(mk.recipes[current], body)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := makeIncludeRe.FindStringSubmatch(trimmed); m != nil {
			for _, inc := range strings.Fields(mk.expand(mk.vars, m[1])) {
				mk.read(t, filepath.Join(filepath.Dir(path), inc), depth+1)
			}
			continue
		}
		if m := makeVarRe.FindStringSubmatch(trimmed); m != nil {
			mk.assign(mk.vars, m[1], m[2], m[3])
			current = ""
			continue
		}
		m := makeRuleRe.FindStringSubmatch(trimmed)
		if m == nil {
			// Anything else — a conditional, a directive — is left alone; the
			// assertions above fail on a missing gate rather than on a guess.
			continue
		}
		targets := strings.Fields(strings.TrimSpace(m[1]))
		rest := strings.TrimSpace(m[2])
		// `target: VAR := value` is a target-specific variable, not a rule with
		// prerequisites, and it carries no recipe.
		if v := makeVarRe.FindStringSubmatch(rest); v != nil {
			for _, target := range targets {
				if mk.targetVars[target] == nil {
					mk.targetVars[target] = map[string]string{}
				}
				mk.assign(mk.targetVars[target], v[1], v[2], v[3])
			}
			continue
		}
		if len(targets) == 1 && targets[0] == ".PHONY" {
			for _, name := range strings.Fields(rest) {
				mk.phony[name] = true
			}
			current = ""
			continue
		}
		for _, target := range targets {
			mk.deps[target] = append(mk.deps[target], strings.Fields(rest)...)
		}
		current = targets[len(targets)-1]
	}
}

// assign records one variable assignment. `?=` keeps the first value, which is
// the Makefile's own default: the point of this test is that the FILE decides,
// so an environment variable of the same name is deliberately not consulted.
func (mk *parsedMakefile) assign(into map[string]string, name, op, value string) {
	switch op {
	case "?=":
		if _, ok := into[name]; !ok {
			into[name] = value
		}
	case "+=":
		if old, ok := into[name]; ok && old != "" {
			into[name] = old + " " + value
			return
		}
		into[name] = value
	default:
		into[name] = value
	}
}

// expand replaces $(NAME) and ${NAME} with NAME's value, repeatedly, so a
// variable whose value names another is resolved. An unknown name expands to
// nothing, exactly as make does.
func (mk *parsedMakefile) expand(vars map[string]string, s string) string {
	s = strings.ReplaceAll(s, "$$", dollarDollar)
	for i := 0; i < 10; i++ {
		next := makeExpandRe.ReplaceAllStringFunc(s, func(ref string) string {
			name := makeExpandRe.FindStringSubmatch(ref)[1]
			return vars[name]
		})
		if next == s {
			break
		}
		s = next
	}
	return strings.ReplaceAll(s, dollarDollar, "$")
}

// recipeFor returns one target's recipe lines with the variables expanded,
// target-specific values winning over the file's. (Real make also passes a
// target-specific value down to that target's prerequisites; no target in this
// Makefile both sets one and has prerequisites, so the distinction does not
// arise here — and a new one that did would show up as a gate the assertions
// cannot find.)
func (mk *parsedMakefile) recipeFor(target string) []string {
	vars := map[string]string{}
	for k, v := range mk.vars {
		vars[k] = v
	}
	for k, v := range mk.targetVars[target] {
		vars[k] = v
	}
	var out []string
	for _, line := range mk.recipes[target] {
		out = append(out, mk.expand(vars, line))
	}
	return out
}

// recipesUnder returns every recipe line `make <target>` would run: the target's
// own, plus those of its prerequisites, depth first, each target once.
func (mk *parsedMakefile) recipesUnder(target string) []string {
	seen := map[string]bool{}
	var walk func(string) []string
	walk = func(name string) []string {
		if seen[name] {
			return nil
		}
		seen[name] = true
		var out []string
		for _, dep := range mk.deps[name] {
			out = append(out, walk(dep)...)
		}
		return append(out, mk.recipeFor(name)...)
	}
	return walk(target)
}

// joinContinuations folds a backslash-continued line into one logical line, the
// way make reads it before handing a recipe to the shell.
func joinContinuations(lines []string) []string {
	var out []string
	var buf string
	joining := false
	for _, line := range lines {
		part := line
		if joining {
			part = strings.TrimLeft(line, " \t")
		}
		if strings.HasSuffix(part, "\\") {
			buf += strings.TrimSuffix(part, "\\") + " "
			joining = true
			continue
		}
		if joining {
			out = append(out, buf+part)
			buf = ""
			joining = false
			continue
		}
		out = append(out, part)
	}
	if joining {
		out = append(out, buf)
	}
	return out
}

// runCommands parses ci.yml's `run:` steps into their command lines. It handles
// both an inline `run: <cmd>` and a `run: |` block scalar, and it skips shell
// comment lines so a comment that mentions `go test` is not mistaken for one.
func runCommands(src string) []runCommand {
	lines := strings.Split(src, "\n")
	var out []runCommand
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		var rest string
		switch {
		case strings.HasPrefix(trimmed, "run:"):
			rest = strings.TrimSpace(strings.TrimPrefix(trimmed, "run:"))
		case strings.HasPrefix(trimmed, "- run:"):
			rest = strings.TrimSpace(strings.TrimPrefix(trimmed, "- run:"))
		default:
			continue
		}
		if rest != "" && !strings.HasPrefix(rest, "|") && !strings.HasPrefix(rest, ">") {
			out = append(out, runCommand{line: i + 1, cmd: rest})
			continue
		}
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			if strings.TrimSpace(next) == "" {
				continue
			}
			nindent := len(next) - len(strings.TrimLeft(next, " "))
			if nindent <= indent {
				break
			}
			out = append(out, runCommand{line: j + 1, cmd: strings.TrimSpace(next)})
			i = j
		}
	}
	return out
}

func allowlistedCommand(cmd string) bool {
	for _, a := range commandAllowlist {
		if strings.Contains(cmd, a) {
			return true
		}
	}
	return false
}
