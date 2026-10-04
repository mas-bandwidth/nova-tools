package main

// landgo.go is the lander's go runs in the clone (docs/SPEC-SPRINT.md section 7, the
// tree gate; the generated ledgers' update runs are landledger.go's). Every run is in the
// caller's environment with GOFLAGS=-mod=readonly: no run writes go.mod or go.sum, so a
// run never leaves the clone dirty (under a caller's -mod=mod the update runs of
// 2026-10-03 rewrote go.mod and every resolution was refused for "the update run
// changed go.mod"), and a module that needs them changed fails the run, which is the
// card's finding.
//
// The tree gate is what the tip of the batch branch passes before it is pushed: the
// module builds and vets (`go build ./...`, `go vet ./...`), and when the batch changes a
// Go file, a document, or testdata (.go, .md, testdata/), the packages that test the tree
// itself (treeTests, where the clone has them) pass. The base's tip is gated once before
// any head is merged, so a base that is red refuses the batch and blames no card. Every
// head is merged and checked by script first; the gate runs once, on the batch's tip
// (gateBatch). A red tip is bisected (greenPrefix): the first head whose prefix is red
// ends the batch as a head that does not merge does, the gate's run and output its
// finding, and the green prefix before it lands. Every go run uses the lander's own
// GOCACHE when it has one (app.landGoCache). A clone with no go.mod has no module and no
// gate.

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
	name := run[0]
	if name == "go" && l.a != nil && l.a.landGo != "" {
		name = l.a.landGo
	}
	b := subproc.Prepare(ctx, landGoBudget, name, run[1:]...)
	defer b.Cancel()
	var env []string
	if l.a != nil {
		env = l.a.gitEnv
	}
	if env == nil {
		env = os.Environ()
	}
	if l.goCache != "" {
		// the lander's own build cache, warm across its batches and its runs, which no
		// other build evicts (cmdLand, app.landGoCache)
		set = append([]string{"GOCACHE=" + l.goCache}, set...)
	}
	b.Cmd.Dir, b.Cmd.Env = dir, withEnv(env, append([]string{readonlyGoFlags(env)}, set...)...)
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

// landProbe is one tree gate of a prefix of the batch: the tip gated (the prefix's last
// merge, or the commit regenerating the generated ledgers on it), the finding ("" green),
// whether the finding is the regeneration's, and whether the tree tests ran.
type landProbe struct {
	tip, why      string
	ledger, tests bool
}

// greenPrefix is the longest prefix of n merged heads the gate passes, asking red(k) of
// the prefix of k heads: the whole batch first, the one gate a green batch costs; on red,
// a bisection between the base (gated green before any merge: k = 0) and the shortest
// prefix known red, so a red batch of n costs about log2(n) more gates. The head after the
// prefix is the one that turns it red (red(k+1)); when the gate is not monotone the prefix
// found is still one the gate passed.
func greenPrefix(n int, red func(k int) bool) int {
	if n == 0 || !red(n) {
		return n
	}
	lo, hi := 0, n
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if red(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return lo
}

// gateBatch gates the merged batch once (docs/SPEC-SPRINT.md section 7, the tree gate):
// the generated ledgers the merges deferred regenerated at its tip and committed, then the
// tree gate on that tip. Red, it bisects (greenPrefix) to the first head that turns the
// batch red and leaves the batch branch at the green prefix before it (its ledgers
// regenerated there): green is how many cards land, failed the card blamed, with the
// finding. env is git failing for a cause that is no card's. tips are each merge's commit.
func (l *lander) gateBatch(ctx context.Context, dir, stream, baseSha string, cards []landCard, tips []string, t *landTimes) (green int, failed conflictCard, env string) {
	probes := map[int]landProbe{}
	green = greenPrefix(len(cards), func(k int) bool {
		if env != "" {
			return true // stop asking: the batch is refused whatever the answer
		}
		p, penv := l.probe(ctx, dir, stream, baseSha, cards[:k], tips[k-1], t)
		if penv != "" {
			env = penv
			return true
		}
		probes[k] = p
		return p.why != ""
	})
	if env != "" {
		return 0, failed, env
	}
	at := baseSha
	l.greenTip = ""
	if green > 0 {
		p := probes[green]
		at = p.tip
		if p.tests {
			l.greenTip = at
		}
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", at); err != nil {
		return 0, failed, "the batch branch could not be reset to its green prefix: " + firstLine("", err)
	}
	if green < len(cards) {
		failed = blamed(cards[green], probes[green+1])
	}
	return green, failed, ""
}

// blamed is the card a red probe blames, as the conflict fact names it: a regeneration
// that failed on a card that deferred its ledgers is its merge that does not merge, as
// before the regeneration moved to the batch's tip; one that failed on any other card, the
// regeneration's; a red gate, the tree gate's finding.
func blamed(c landCard, p landProbe) conflictCard {
	why := "the head " + c.head + " of " + c.id + " fails the tree gate: " + p.why
	switch {
	case p.ledger && len(c.regen) > 0:
		why = "the head " + c.head + " of " + c.id + " does not merge: " + c.mergeWhy + "; " + p.why
	case p.ledger:
		why = "the head " + c.head + " of " + c.id + " fails the generated ledgers' regeneration at the batch's tip: " + p.why
	}
	return conflictCard{landCard: c, why: why, kind: c.mergeKind, paths: c.mergePaths}
}

// probe gates the prefix of the batch whose last merge is tip: the batch branch reset
// there, the generated ledgers any of its merges deferred regenerated (regenBatch), then
// the tree gate, the tree tests too when the prefix changes a file they read (treeTested).
// env is git failing for a cause that is no card's.
func (l *lander) probe(ctx context.Context, dir, stream, baseSha string, cards []landCard, tip string, t *landTimes) (landProbe, string) {
	p := landProbe{tip: tip}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", tip); err != nil {
		return p, "the batch branch could not be reset to " + tip + " for the tree gate: " + firstLine("", err)
	}
	if deferredLedgers(cards) {
		start := time.Now()
		why, env := l.regenBatch(ctx, dir, stream, cards)
		since(&t.Ledger, start)
		if env != "" {
			return p, env
		}
		if why != "" {
			p.why, p.ledger = why, true
			return p, ""
		}
		at, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return p, "the batch branch has no tip after the ledgers' regeneration: " + firstLine("", err)
		}
		p.tip = at
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return p, "" // no module, no gate (treeGate)
	}
	changed, err := l.git(ctx, dir, "diff", "--name-only", "-M", baseSha, p.tip)
	if err != nil {
		return p, "the files the batch changed could not be listed: " + firstLine("", err)
	}
	p.tests = slices.ContainsFunc(strings.Split(changed, "\n"), treeTested)
	start := time.Now()
	p.why = l.treeGate(ctx, dir, p.tests)
	since(&t.Gate, start)
	return p, ""
}
