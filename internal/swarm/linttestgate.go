package swarm

import (
	"fmt"
	"strings"
)

// A CARD'S TEST: LINE MUST NAME A GATE THAT RUNS IT (ideas #796).
//
// `test-named` (internal/swarm/lintheader.go) checks the SHAPE of a `TEST:` line: two
// fields, a repository-relative package, a Go test name. Shape is not truth. A card can
// read `TEST: ./internal/pulse TestThing` beside a gate of `RUN: go test ./internal/swarm/`
// -- a shape the header parser accepts and no command in the card ever runs. The card
// then claims a reproducing test the card's own gate cannot execute.
//
// THE GATE IS THE CARD'S RUN, IN EITHER OF ITS TWO SPEC-CARD SPELLINGS. Clause 6
// (docs/SPEC-CARD.md:202-212) makes the single `## Run` fenced region the "one operative
// authority"; the `RUN:` header key is an optional echo that must equal a one-line region.
// A v2 card may carry either or both, so this check treats a package as run when EITHER
// authority runs it -- lenient on purpose: the refusal is about a test NO gate runs, and
// a card with a matching region and a stale echo does run its test.
//
// WHY IT STOPS AT THE GATE THE CARD NAMES. This reads only the card's own bytes, like
// every other swarm lint rule: no repo, no execution. `make`/`gmake` run an unknown set
// of packages, `go test ./...` and a parent `/...` run every package under them, and a
// `go test ./internal/pulse/` runs exactly that package; those all count as running it.
// A `go vet`, a `gofmt -l`, or a `go test` of a DIFFERENT package does not. When nothing
// in the card's gate runs the TEST: package, the card draws `test-gate`.
//
// WHAT IT DOES NOT TOUCH. `TEST: none` is a declaration that there is no Go anchor
// (SPEC-CARD §5), so it carries no gate obligation and is left alone. A TEST: line whose
// shape is wrong belongs to `test-named`, not here: refusing both for one line is the
// two-answers-for-one-defect mistake the header comment warns about.

// CardTestGateRemedy is what the `test-gate` token wants, in one line, in the same table
// shape as `depends-on` (CardDependsRemedy). It is not merged into the typed-header
// remedies map on purpose -- that map's size is pinned by the `--rules` listing and the
// cardLintChecks count in cmd/nova-swarm/lint.go, which is the wiring this card's PATHS
// does not reach.
const CardTestGateRemedy = "the card's gate (`RUN:` and/or its `## Run` region) must run the package its `TEST:` line names: `go test <package>`, `go test ./...`, `go test <ancestor>/...`, or a `make`/`gmake` gate; a TEST: naming a package no command runs has no gate that runs it (SPEC-CARD.md §5, §6)"

// LintCardTestGate returns the `test-gate` findings for one card's bytes: a non-empty
// finding when the card's TEST: line names a package none of its gate commands run.
func LintCardTestGate(raw []byte) []CardHeaderFinding {
	h, _ := cardHeaderBlock(raw)
	t := h["TEST"]
	if !t.found {
		return nil // no TEST: line is test-named's finding, not this one
	}
	if t.value == "none" {
		return nil // a declaration of no Go anchor carries no gate obligation
	}
	fields := strings.Fields(t.value)
	if len(fields) != 2 || !goTestNameRE.MatchString(fields[1]) {
		return nil // a malformed TEST: line is test-named's, not a second token here
	}
	pkg, ok := cleanTestPkg(fields[0])
	if !ok {
		return nil // an unwalkable package is test-named's (the same path rule it runs)
	}
	gate := gateCommands(raw, h)
	if gateRunsPkg(gate, pkg) {
		return nil
	}
	return []CardHeaderFinding{{
		Check:   "test-gate",
		Line:    t.line,
		Excerpt: fmt.Sprintf("TEST: %q names package %s, which no gate in this card runs; its gate is %s", t.value, pkg, strings.Join(commandList(gate), " AND ")),
	}}
}

// gateCommands is every command the card's gate holds: the `## Run` region's lines (the
// clause-6 authority) and the `RUN:` header echo (the optional one-line twin). Either one
// running the TEST package is enough, so they are gathered together.
func gateCommands(raw []byte, h map[string]headerField) []string {
	var out []string
	out = append(out, runRegionCommands(raw)...)
	if f := h["RUN"]; f.found && f.value != "" {
		out = append(out, f.value)
	}
	return out
}

// runRegionCommands reads the fenced block of the card's single `## Run` section: the
// command lines between the opening and closing fences. A card with no region holds none.
func runRegionCommands(raw []byte) []string {
	var out []string
	inside := false
	fenced := false
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if !inside {
			if t == "## Run" {
				inside = true
			}
			continue
		}
		// Inside the section: stop at a later heading that is not the region's fence.
		if strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "## Run") {
			if !fenced {
				break
			}
		}
		if strings.HasPrefix(t, "```") {
			if fenced {
				break // closing fence
			}
			fenced = true // opening fence; the bare `RUN:` marker line is not a command
			continue
		}
		if fenced && t != "" {
			out = append(out, line)
		}
	}
	return out
}

// gateRunsPkg says whether any gate command runs the named repository-relative package.
func gateRunsPkg(commands []string, pkg string) bool {
	for _, cmd := range commands {
		for _, seg := range splitGateCommands(cmd) {
			fields := strings.Fields(seg)
			if len(fields) == 0 {
				continue
			}
			// An opaque make gate runs an unknown set: never refuse on it.
			if fields[0] == "make" || fields[0] == "gmake" {
				return true
			}
			if goTestSegmentsRun(fields, pkg) {
				return true
			}
		}
	}
	return false
}

// goTestSegmentsRun reports whether one whitespace-split command is a `go test` that runs
// the package: `go` immediately followed by `test`, with a package argument covering pkg.
//
// A token after a value-taking flag written detached (`-run ./internal/pulse`) is that
// flag's VALUE, not a package: `go test -run ./internal/pulse ./internal/swarm/` runs
// ./internal/swarm with ./internal/pulse as the -run regexp. Such a value is skipped, and
// everything after `-args` goes to the test binary, so neither can cover the package.
func goTestSegmentsRun(fields []string, pkg string) bool {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "go" || fields[i+1] != "test" {
			continue
		}
		args := fields[i+2:]
		for j := 0; j < len(args); j++ {
			arg := args[j]
			if strings.HasPrefix(arg, "-") {
				name := goTestFlagName(arg)
				if name == "args" {
					break // the rest is the test binary's argv, never a package
				}
				if !strings.Contains(arg, "=") && goTestValueFlags[name] {
					j++ // the next token is this flag's detached value
				}
				continue // a flag, never a package selector
			}
			if selectorRunsPkg(arg, pkg) {
				return true
			}
		}
		return false // a `go test` with no covering package argument
	}
	return false
}

// goTestFlagName is a flag token's bare name: leading dashes, any `=value`, and the
// test binary's `test.` prefix removed (`--test.run=X` -> `run`).
func goTestFlagName(arg string) string {
	name := strings.TrimLeft(arg, "-")
	if k := strings.IndexByte(name, '='); k >= 0 {
		name = name[:k]
	}
	return strings.TrimPrefix(name, "test.")
}

// goTestValueFlags are the `go test` flags (its own, the test binary's, and the build
// flags it accepts) that take a value, so a detached next token is that value. Boolean
// flags (-v, -race, -short, -cover, -failfast, -json, ...) take none and are absent.
var goTestValueFlags = map[string]bool{
	// test and test-binary flags
	"bench": true, "benchtime": true, "blockprofile": true, "blockprofilerate": true,
	"count": true, "coverprofile": true, "covermode": true, "coverpkg": true, "cpu": true,
	"cpuprofile": true, "exec": true, "fuzz": true, "fuzzcachedir": true,
	"fuzzminimizetime": true, "fuzztime": true, "list": true, "memprofile": true,
	"memprofilerate": true, "mutexprofile": true, "mutexprofilefraction": true, "o": true,
	"outputdir": true, "parallel": true, "run": true, "shuffle": true, "skip": true,
	"timeout": true, "trace": true, "vet": true,
	// build flags go test accepts
	"C": true, "asmflags": true, "buildmode": true, "compiler": true, "gccgoflags": true,
	"gcflags": true, "installsuffix": true, "ldflags": true, "mod": true, "modfile": true,
	"overlay": true, "p": true, "pgo": true, "pkgdir": true, "tags": true, "toolexec": true,
}

// selectorRunsPkg says whether one `go test` argument names the package, every package
// (`./...`), or an ancestor run with `/...`.
func selectorRunsPkg(arg, pkg string) bool {
	ellipsis := strings.HasSuffix(arg, "...")
	base := strings.TrimSpace(strings.TrimSuffix(arg, "..."))
	base = strings.TrimPrefix(base, "./")
	base = strings.Trim(base, "/")
	if base == "" || base == "." {
		return true // ./... (or ./ , .) runs every package
	}
	if strings.Contains(base, "..") {
		return false // an unwalkable selector runs nothing here
	}
	if base == pkg {
		return true
	}
	if ellipsis && strings.HasPrefix(pkg, base+"/") {
		return true // an ancestor run with /...
	}
	return false
}

// cleanTestPkg normalizes a repository-relative package token: strips a leading `./` and
// a trailing `/`, and reports an unwalkable path (empty, a climb above the repo).
func cleanTestPkg(s string) (string, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "./")
	s = strings.Trim(s, "/")
	if s == "" || s == "." || strings.Contains(s, "..") {
		return "", false
	}
	return s, true
}

// splitGateCommands breaks one gate line into its commands at the shell separators that
// join them: `&&`, `||`, `;`, and a pipe. It is not a shell parser; it only has to find a
// `go test` among the pieces a card writes on one line.
func splitGateCommands(cmd string) []string {
	cmd = strings.ReplaceAll(cmd, "&&", "\n")
	cmd = strings.ReplaceAll(cmd, "||", "\n")
	cmd = strings.ReplaceAll(cmd, ";", "\n")
	cmd = strings.ReplaceAll(cmd, "|", "\n")
	var out []string
	for _, seg := range strings.Split(cmd, "\n") {
		if strings.TrimSpace(seg) != "" {
			out = append(out, seg)
		}
	}
	return out
}

// commandList is the gate commands in a form safe to print on a finding's excerpt.
func commandList(commands []string) []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		if t := strings.TrimSpace(c); t != "" {
			out = append(out, t)
		}
	}
	return out
}
