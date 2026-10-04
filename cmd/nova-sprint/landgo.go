package main

// landgo.go is the lander's go runs in the clone (docs/SPEC-SPRINT.md section 7, the
// tree gate; the generated ledgers' update runs are landledger.go's). Every run is in the
// caller's environment with GOFLAGS=-mod=readonly: no run writes go.mod or go.sum, so a
// run never leaves the clone dirty (under a caller's -mod=mod the update runs of
// 2026-10-03 rewrote go.mod and every resolution was refused for "the update run
// changed go.mod"), and a module that needs them changed fails the run, which is the
// card's finding.
//
// The tree gate is what every tip land pushes passes: the module builds and vets (`go
// build ./...`, `go vet ./...`), and when the batch changes a Go file, a document, or
// testdata (.go, .md, testdata/), the packages that test the tree itself (treeTests,
// where the clone has them) pass. The base's tip is gated once a batch before any head is
// merged, so a base that is red refuses the batch and blames no card. The batch's heads
// are merged first and its tip gated once; a red tip is bisected to the first head whose
// merge turned it red, which ends the batch as a head that does not merge does, the
// gate's run and output its finding (landbatch.go). A clone with no go.mod has no module
// and no gate.
//
// Every go run is under the lander's own GOCACHE (app.landGoCache: .gocache under the land
// root, beside the clones), kept warm across landings: the machine's shared build cache is
// one the members trim, and a gate on a trimmed cache rebuilt the module from cold.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// landGoBudget bounds one go run in the clone: a build of the module, a vet, a test of
// the tree's own packages, or one update run (a build and two tests of one package).
const landGoBudget = 15 * time.Minute

// treeTests are the packages that test the tree itself (its docs and its tests), run by
// the gate when a head changes a .md or a _test.go file; one the clone lacks is not run.
var treeTests = []string{"internal/docs", "internal/ci"}

// goRun runs one go command (run) in the clone, in the lander's environment with
// GOFLAGS=-mod=readonly (caller flags preserved) and set (NAME=value each); its combined output.
func (l *lander) goRun(ctx context.Context, dir string, run []string, set ...string) (string, error) {
	b := subproc.Prepare(ctx, landGoBudget, run[0], run[1:]...)
	defer b.Cancel()
	var env []string
	if l.a != nil {
		env = l.a.gitEnv
	}
	if env == nil {
		env = os.Environ()
	}
	pin := []string{readonlyGoFlags(env)}
	if c := l.goCache(); c != "" {
		pin = append(pin, "GOCACHE="+c)
	}
	b.Cmd.Dir, b.Cmd.Env = dir, withEnv(env, append(pin, set...)...)
	out, err := b.Cmd.CombinedOutput()
	return string(out), b.Wrap(strings.Join(run, " "), err)
}

// readonlyGoFlags returns GOFLAGS=... with -mod=readonly set, preserving any other
// flags from env's GOFLAGS entries and dropping any existing -mod or -mod=... flag.
func readonlyGoFlags(env []string) string {
	var terms []string
	for _, e := range env {
		if val, ok := strings.CutPrefix(e, "GOFLAGS="); ok {
			for _, term := range strings.Fields(val) {
				if !strings.HasPrefix(term, "-mod=") && term != "-mod" {
					terms = append(terms, term)
				}
			}
		}
	}
	terms = append(terms, "-mod=readonly")
	return "GOFLAGS=" + strings.Join(terms, " ")
}

// withEnv is env with each of set (NAME=value) in place of the NAME it held, else added;
// duplicate entries of NAME in env are dropped.
func withEnv(env []string, set ...string) []string {
	out := slices.Clone(env)
	for _, kv := range set {
		name, _, _ := strings.Cut(kv, "=")
		prefix := name + "="
		first := slices.IndexFunc(out, func(e string) bool { return strings.HasPrefix(e, prefix) })
		if first >= 0 {
			out[first] = kv
			seen := false
			out = slices.DeleteFunc(out, func(e string) bool {
				if strings.HasPrefix(e, prefix) {
					if !seen {
						seen = true
						return false
					}
					return true
				}
				return false
			})
		} else {
			out = append(out, kv)
		}
	}
	return out
}

// treeTested says a change to p is one the tree tests read: a Go file, a document, or
// under testdata.
func treeTested(p string) bool {
	return strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".md") || strings.Contains(p, "testdata/")
}

// gateRuns is the tree gate's runs, in order: the build and the vet of the module, then
// the tree tests (have: the ones the clone holds) when tests is asked.
func gateRuns(tests bool, have []string) [][]string {
	runs := [][]string{{"go", "build", "./..."}, {"go", "vet", "./..."}}
	if tests && len(have) > 0 {
		run := []string{"go", "test"}
		for _, p := range have {
			run = append(run, "./"+p+"/")
		}
		runs = append(runs, run)
	}
	return runs
}

// gateWhy is a red run as a finding, one line: the run, how it ended and its output.
func gateWhy(run []string, err error, out string) string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(run, " ") + ": " + oneline.Err(err) + ": " + oneline.Cap(strings.Join(lines, " | "), 1500)
}

// baseGateFail is a base commit's failures of its tree gate under the base-gate rule: how
// many, the last finding, and when it is gated again.
type baseGateFail struct {
	n    int
	why  string
	next time.Time
}

// treeGateBase gates the base's tip at baseSha: "" when green, else the finding, and stop
// when the stream stops on it. A green base is cached for its commit, so the same base
// commit is not re-gated across streams or rounds. A red one is the base-gate rule's
// (docs/SPEC-SPRINT.md section 8, answered by rule; the coordinator, 2026-10-04: five streams sat
// stopped 16 minutes on a toolchain's transient "package ... is not in std"): it is gated
// again after sprint.BaseGateRetries[0], then after [1], each landing in between refused
// with the finding and when it is gated again; its third failure stops every stream that
// lands on it (stop), each with the error, the coordinator's judgment. With the rule off
// (nova-config's sprint row answer_rules_off), a red base is cached for its commit as a
// green one is, as before the rule: every landing on it refused until the base moves.
func (l *lander) treeGateBase(ctx context.Context, dir, baseSha string) (why string, stop bool) {
	if l.baseGateCache == nil {
		l.baseGateCache = map[string]string{}
	}
	if l.baseGateFails == nil {
		l.baseGateFails = map[string]*baseGateFail{}
	}
	if why, cached := l.baseGateCache[baseSha]; cached {
		return why, false
	}
	now := l.clock()
	f := l.baseGateFails[baseSha]
	switch {
	case f != nil && f.n > len(sprint.BaseGateRetries):
		return f.why, true
	case f != nil && now.Before(f.next):
		return f.said(), false
	}
	why = l.treeGate(ctx, dir, true)
	if why == "" || slices.Contains(l.offRules(ctx), sprint.RuleBaseGate) {
		l.baseGateCache[baseSha] = why
		delete(l.baseGateFails, baseSha)
		return why, false
	}
	if f == nil {
		f = &baseGateFail{}
		l.baseGateFails[baseSha] = f
	}
	f.n, f.why = f.n+1, why
	if f.n > len(sprint.BaseGateRetries) {
		f.why = fmt.Sprintf("the base %s failed its tree gate %d times, %s apart then %s: %s", shortSha(baseSha), f.n, sprint.BaseGateRetries[0], sprint.BaseGateRetries[1], why)
		return f.why, true
	}
	f.next = now.Add(sprint.BaseGateRetries[f.n-1])
	return f.said(), false
}

// said is a failure as a refused landing says it: the finding, and when the base is gated
// again.
func (f *baseGateFail) said() string {
	return fmt.Sprintf("%s; gated again at %s (failure %d of %d)", f.why, f.next.UTC().Format("15:04:05 MST"), f.n, len(sprint.BaseGateRetries)+1)
}

// shortSha is a commit id as a line says it.
func shortSha(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// clock is the lander's clock: the app's, or the wall's.
func (l *lander) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	if l.a != nil && l.a.now != nil {
		return l.a.now()
	}
	return time.Now()
}

// offRules is the rules nova-config's sprint row turns off, read with the routes; given
// (a test), or none when the store does not say.
func (l *lander) offRules(ctx context.Context) []string {
	if l.rulesOff != nil || l.st == nil {
		return l.rulesOff
	}
	off, err := l.st.RulesOff(ctx)
	if err != nil {
		return nil
	}
	return off
}

// treeGate runs the gate on the clone's tree, the tree tests too when tests: "" when it
// is green or the clone has no module, else the finding (gateWhy).
func (l *lander) treeGate(ctx context.Context, dir string, tests bool) string {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return ""
	}
	var have []string
	for _, p := range treeTests {
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil && fi.IsDir() {
			have = append(have, p)
		}
	}
	for _, run := range gateRuns(tests, have) {
		if out, err := l.goRun(ctx, dir, run); err != nil {
			return gateWhy(run, err, out)
		}
	}
	return ""
}

// goCache is the GOCACHE of the lander's go runs: its own (app.landGoCache), made on first
// use; "" runs them in the caller's (no app, none set, or a directory that cannot be made,
// which costs a warm cache and nothing else).
func (l *lander) goCache() string {
	if l.a == nil || l.a.landGoCache == nil {
		return ""
	}
	dir, err := l.a.landGoCache()
	if err != nil || !filepath.IsAbs(dir) || os.MkdirAll(dir, 0o755) != nil {
		return ""
	}
	return dir
}

// landGoCacheIn is the lander's own build cache under the land root: .gocache, a name no
// clone's directory takes (repoDirName never starts with a dot).
func landGoCacheIn(root func() (string, error)) func() (string, error) {
	return func() (string, error) {
		r, err := root()
		if err != nil {
			return "", err
		}
		return filepath.Join(r, ".gocache"), nil
	}
}
