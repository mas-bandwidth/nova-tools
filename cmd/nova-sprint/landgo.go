package main

// landgo.go is the lander's go runs in the clone (docs/SPEC-SPRINT.md section 7, the
// tree gate; the generated ledgers' update runs are landledger.go's). Every run is in the
// caller's environment with GOFLAGS=-mod=readonly: no run writes go.mod or go.sum, so a
// run never leaves the clone dirty (under a caller's -mod=mod the update runs of
// 2026-10-03 rewrote go.mod and every resolution was refused for "the update run
// changed go.mod"), and a module that needs them changed fails the run, which is the
// card's finding.
//
// The tree gate is what every tip of the batch branch passes before the next head is
// merged: the module builds and vets (`go build ./...`, `go vet ./...`), and when tests
// are asked (a Go file, a document, or testdata: .go, .md, testdata/, go.mod, go.sum),
// tests run for every package touched by the batch's changed files plus every package
// that imports them, plus the tree tests (treeTests, where the clone has them:
// internal/docs, internal/ci), plus the whole-tree functional checks on internal/ci
// (TestUncheckedErrors, TestStaticcheckFindings, TestDeadCode, TestEveryCommandMeetsTheOnboardingStandard).
// The base's tip is gated once a batch before any head is merged, so a base that is red
// refuses the batch and blames no card. A head whose merged tree is red is taken off
// the batch branch and ends the batch as a head that does not merge does, the gate's run
// and output its finding. A clone with no go.mod has no module and no gate.

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// landGoBudget bounds one go run in the clone: a build of the module, a vet, a test of
// the tree's own packages, or one update run (a build and two tests of one package).
const landGoBudget = 15 * time.Minute

// treeTests are the packages that test the tree itself (its docs and its tests), run by
// the gate when a head changes a .md or a _test.go file; one the clone lacks is not run.
var treeTests = sprint.TreeTests

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
	b.Cmd.Dir, b.Cmd.Env = dir, withEnv(env, append([]string{readonlyGoFlags(env)}, set...)...)
	out, err := b.Cmd.CombinedOutput()
	return string(out), b.Wrap(strings.Join(run, " "), err)
}

// readonlyGoFlags returns GOFLAGS=... with -mod=readonly set, preserving any other
// flags from env's GOFLAGS entries and dropping any existing -mod or -mod=... flag.
func readonlyGoFlags(env []string) string {
	return sprint.ReadonlyGoFlags(env)
}

// withEnv is env with each of set (NAME=value) in place of the NAME it held, else added;
// duplicate entries of NAME in env are dropped.
func withEnv(env []string, set ...string) []string {
	return sprint.WithEnv(env, set...)
}

// treeTested says a change to p is one the tree tests read: a Go file, a document, or
// under testdata, go.mod, or go.sum.
func treeTested(p string) bool {
	return sprint.TreeTested(p)
}

// gateRuns is the tree gate's runs, in order: the build and the vet of the module, then
// the packages to test when tests is asked, plus the functional whole-tree checks on
// internal/ci when the clone holds it.
func gateRuns(tests bool, have []string) [][]string {
	return sprint.GateRuns(tests, have)
}

// gateWhy is a red run as a finding, one line: the run, how it ended and its output.
func gateWhy(run []string, err error, out string) string {
	return sprint.GateWhy(run, err, out)
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
	why = l.treeGate(ctx, dir, nil, true)
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

// treePackages are the treeTests the clone at dir holds as Go packages: a directory with
// no .go file in it (the nova-sprint repo's internal/ci holds only data, 2026-10-05) is no
// package, and `go test` of it fails every batch on that base, so it is not run.
func treePackages(dir string) []string {
	return sprint.TreePackages(dir)
}

// gatePackages finds the packages to test for the gate: every package touched by changed
// files plus every package that imports one of them (direct and transitive importers),
// plus the tree tests the clone holds (treeTests).
// If go list fails, it returns (nil, why) with the go list error as the gate's finding.
func (l *lander) gatePackages(ctx context.Context, dir string, changed []string, tests bool) ([]string, string) {
	return sprint.GatePackages(ctx, dir, changed, tests, l.goRun)
}

// treeGate runs the gate on the clone's tree, the tests too when tests: "" when it
// is green or the clone has no module, else the finding (gateWhy).
// (docs/SPEC-SPRINT.md section 7, the tree gate).
func (l *lander) treeGate(ctx context.Context, dir string, changed []string, tests bool) string {
	return sprint.TreeGate(ctx, dir, changed, tests, l.goRun)
}

// gateCard is the tree gate on one card merged onto the batch branch at before: red, the
// card is taken off the batch branch (reset to before) and ends the batch as a head that
// does not merge does, the finding its reason; card and env are mergeHead's. A merge that
// made no commit (the head is in the base already) is not gated: it changes nothing the
// base does not hold.
func (l *lander) gateCard(ctx context.Context, dir string, c landCard, before string) (card, env string) {
	after, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || after == before {
		return "", ""
	}
	changed, err := l.git(ctx, dir, "diff", "--name-only", "-M", before, after)
	if err != nil {
		return "", "the files the merge of " + c.id + " changed could not be listed: " + firstLine("", err)
	}
	files := strings.Split(changed, "\n")
	why := l.treeGate(ctx, dir, files, slices.ContainsFunc(files, treeTested))
	if why == "" {
		return "", ""
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", before); err != nil {
		return "", "the batch branch could not be reset after " + c.id + " failed the tree gate: " + firstLine("", err)
	}
	return "the head " + c.head + " of " + c.id + " fails the tree gate: " + why, ""
}
