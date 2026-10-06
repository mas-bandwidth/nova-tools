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
// merged: the module builds and vets (`go build ./...`, `go vet ./...`), and when a
// head changes a Go file, a document, or testdata (.go, .md, testdata/), the packages
// that test the tree itself (treeTests, where the clone has them) pass. The base's tip
// is gated once a batch before any head is merged, so a base that is red refuses the
// batch and blames no card, unless a head of the batch cures it (cureBase): that head lands
// first as the base fix. A head whose merged tree is red is taken off the batch branch
// and ends the batch as a head that does not merge does, the gate's run and output its
// finding. A clone with no go.mod has no module and no gate.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
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

// treePackages are the treeTests the clone at dir holds as Go packages: a directory with
// no .go file in it (the nova-sprint repo's internal/ci holds only data, 2026-10-05) is no
// package, and `go test` of it fails every batch on that base, so it is not run.
func treePackages(dir string) []string {
	var have []string
	for _, p := range treeTests {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(p), "*.go"))
		if len(matches) > 0 {
			have = append(have, p)
		}
	}
	return have
}

// treeGate runs the gate on the clone's tree, the tree tests too when tests: "" when it
// is green or the clone has no module, else the finding (gateWhy). When a fleet member
// other than this machine is up, the go runs go to that member's Go lane through the
// bench; otherwise they run in this process (goRun). The ledgers' update runs stay here.
func (l *lander) treeGate(ctx context.Context, dir string, tests bool) string {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return ""
	}
	runs := gateRuns(tests, treePackages(dir))
	host := l.gateBenchHost(ctx)
	if host == "" {
		for _, run := range runs {
			l.stage("gate", strings.Join(run, " "))
			if out, err := l.goRun(ctx, dir, run); err != nil {
				return gateWhy(run, err, out)
			}
		}
		return ""
	}
	if err := l.takeGateLane(ctx, host); err != nil {
		return gateWhy(runs[0], err, "")
	}
	defer l.giveGateLane(host)
	var wall time.Duration
	for _, run := range runs {
		l.stage("gate", strings.Join(run, " "))
		start := l.clock()
		out, code, err := l.runOnBench(ctx, host, dir, run)
		wall += l.clock().Sub(start)
		if err != nil {
			l.ranOnBench(host, wall)
			return gateWhy(run, err, out)
		}
		if code != 0 {
			l.ranOnBench(host, wall)
			return gateWhy(run, fmt.Errorf("exit status %d", code), out)
		}
	}
	l.ranOnBench(host, wall)
	return ""
}

// gateBenchHost is an up fleet member other than this machine that bench.CheckHost
// accepts, or "" when the gate runs here. Only the server's land loop sends the
// gate out: a land command on its own (a hand land, the install walkthrough)
// keeps it in this process. A unit test under the host guard with no bench seam
// keeps it here too, so a member brought up in a test is not sshed to.
func (l *lander) gateBenchHost(ctx context.Context) string {
	if l == nil || l.a == nil || l.st == nil {
		return ""
	}
	b := l.a.landState()
	b.mu.Lock()
	inLoop := b.flight != nil
	seam := b.gateBench
	b.mu.Unlock()
	if !inLoop || (testguard.Refusing() && seam == nil) {
		return ""
	}
	l.a.serial.Lock()
	s, err := l.st.Load(ctx, []string{sprint.Fleet}, nil)
	l.a.serial.Unlock()
	if err != nil || s == nil {
		return ""
	}
	self := l.a.machineName()
	for _, m := range s.UpMembers() {
		if self != "" && strings.EqualFold(m, self) {
			continue
		}
		if bench.CheckHost(m) != nil {
			continue
		}
		return m
	}
	return ""
}

// takeGateLane asks for machine's Go lane until it is granted. The wait is the
// lane's own interval, not the land loop's sleep, so the beat keeps printing.
func (l *lander) takeGateLane(ctx context.Context, machine string) error {
	proc := "lane take go --machine " + machine + " --as " + landLaneWho
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.a.serial.Lock()
		ans, err := l.st.LaneStep(ctx, sprint.LaneGo, machine, landLaneWho, false)
		l.a.serial.Unlock()
		if err != nil {
			return err
		}
		if ans.Granted {
			return nil
		}
		l.stage("lane", proc)
		t := time.NewTimer(sprint.LaneAskEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// giveGateLane returns the Go lane. A cancelled landing still gives it back;
// the hold expires at LaneHoldFor when the give does not land.
func (l *lander) giveGateLane(machine string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	l.a.serial.Lock()
	defer l.a.serial.Unlock()
	_, _ = l.st.LaneStep(ctx, sprint.LaneGo, machine, landLaneWho, true) // ignored: the hold expires at LaneHoldFor when this give does not land
}

// runOnBench runs argv on host. A test's gateBench seam stands in for the bench;
// otherwise bench.Run copies the tree and runs it there.
func (l *lander) runOnBench(ctx context.Context, host, dir string, argv []string) (string, int, error) {
	if l.a != nil {
		if gate := l.a.landState().gate(); gate != nil {
			return gate(ctx, host, dir, argv)
		}
	}
	var buf bytes.Buffer
	res, err := bench.Run(ctx, bench.Exec{}, bench.Options{
		Hosts:  []string{host},
		Dir:    dir,
		Argv:   argv,
		Stdout: &buf,
		Stderr: &buf,
	})
	return buf.String(), res.Code, err
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
	why := l.treeGate(ctx, dir, slices.ContainsFunc(strings.Split(changed, "\n"), treeTested))
	if why == "" {
		return "", ""
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", before); err != nil {
		return "", "the batch branch could not be reset after " + c.id + " failed the tree gate: " + firstLine("", err)
	}
	return "the head " + c.head + " of " + c.id + " fails the tree gate: " + why, ""
}

// cureBase looks for the red base's fix among the batch's cards (sprint.FindBaseCure; docs/
// SPEC-SPRINT.md section 8, the base cure): each head merged onto the base alone, through the
// lander's own checks (mergeHead, checkCard) and the base's tree gate with the tree tests.
// The first green is the cure: its index in cards, the batch branch left at its merge, and its
// landing note naming the fix; -1 when no head cures the base, the branch at the base again.
// A head found no cure on this base is not tried on it again. env is a failure that is not a
// card's.
func (l *lander) cureBase(ctx context.Context, dir, stream string, cards []landCard, baseSha, why string) (cured int, env string) {
	if l.cureTried == nil {
		l.cureTried = map[string]bool{}
	}
	tried := func(h sprint.CureHead) string { return baseSha + " " + h.ID + "@" + h.Head }
	heads := make([]sprint.CureHead, len(cards))
	at := map[string]int{}
	for i, c := range cards {
		heads[i], at[c.id] = sprint.CureHead{ID: c.id, Head: c.head}, i
	}
	notes, repairs := map[string]string{}, map[string]string{}
	cure, err := sprint.FindBaseCure(ctx, sprint.BaseCureReq{RepoDir: dir, Base: baseSha, Heads: heads, Env: l.a.gitEnv,
		Merge: func(ctx context.Context, h sprint.CureHead) (string, string) {
			c := cards[at[h.ID]]
			card, env, note := l.mergeHead(ctx, dir, stream, c)
			if card == "" && env == "" {
				var repaired string
				if card, env, repaired = l.checkCard(ctx, dir, stream, c, baseSha); repaired != "" {
					note = strings.TrimPrefix(note+"; "+repaired, "; ")
				}
				repairs[h.ID] = repaired
			}
			notes[h.ID] = note
			return card, env
		},
		Gate:  func(ctx context.Context, dir string) string { return l.treeGate(ctx, dir, true) },
		Tried: func(h sprint.CureHead) bool { return l.cureTried[tried(h)] },
	})
	l.conflictKind, l.conflictPaths = "", nil // a try's conflict is no card's stop
	for _, t := range cure.Tried {
		l.cureTried[tried(t.CureHead)] = true
	}
	if err != nil {
		return -1, "the search for a fix of the red base " + shortSha(baseSha) + " failed: " + oneline.Err(err)
	}
	if !cure.Found() {
		return -1, ""
	}
	i := at[cure.ID]
	c := &cards[i]
	c.resolved = cure.Note(baseSha, why)
	if n := notes[cure.ID]; n != "" {
		c.resolved = n + "; " + c.resolved
	}
	if r := repairs[cure.ID]; r != "" {
		l.ledgerLog = append(l.ledgerLog, cure.ID+": "+r)
	}
	l.baseFix = cure.ID + " " + c.resolved
	return i, ""
}
